package qqbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zhiwei/internal/channel"
	"zhiwei/internal/settings"
)

func newTestAdapter(got *[]channel.Inbound) *Adapter {
	a := New(settings.QQBot{AppID: "1", AppSecret: "naOC0ocQE3shWLAfffVLB1rhYPG7", Mode: "webhook", Group: true, C2C: true}, Options{}, slog.Default())
	a.handler = func(in channel.Inbound) { *got = append(*got, in) }
	a.names.denied.Store(true) // 默认不查群名，免得测试去连真实接口；群名的测试单独打开
	return a
}

func TestGroupName(t *testing.T) {
	calls := 0
	deny := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if deny {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":11253,"message":"应用无接口访问权限"}`))
			return
		}
		if r.URL.Path != "/v2/groups/G1/info" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"group_openid":"G1","group_name":"bot消息测试","group_member_num":5}`))
	}))
	defer srv.Close()

	var got []channel.Inbound
	a := newTestAdapter(&got)
	a.names.denied.Store(false)
	a.api.base, a.api.token, a.api.expires = srv.URL, "tok", time.Now().Add(time.Hour)
	a.dispatch("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m1","content":"你好","group_openid":"G1","author":{"id":"U1"}}`))
	a.dispatch("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m2","content":"在吗","group_openid":"G1","author":{"id":"U1"}}`))
	if len(got) != 2 || got[0].ChatTitle != "bot消息测试" || got[1].ChatTitle != "bot消息测试" {
		t.Fatalf("群名应带进消息：%+v", got)
	}
	if calls != 1 {
		t.Fatalf("第二条应命中缓存，实际调用 %d 次", calls)
	}

	// 无权限（白名单接口）：记住后不再调用
	deny = true
	if name := a.GroupName(context.Background(), "G2"); name != "" {
		t.Fatalf("无权限应返回空：%q", name)
	}
	_ = a.GroupName(context.Background(), "G3")
	if calls != 2 || !a.names.denied.Load() {
		t.Fatalf("无权限后不该再调用：calls=%d", calls)
	}
}

func TestWebhookValidation(t *testing.T) {
	a := newTestAdapter(new([]channel.Inbound))
	body := `{"op":13,"d":{"plain_token":"Arq0D5A61EgUu4OxUvOp","event_ts":"1725442341"}}`
	w := httptest.NewRecorder()
	a.ServeWebhook(w, httptest.NewRequest(http.MethodPost, "/webhook/qqbot", strings.NewReader(body)))
	var out struct {
		PlainToken string `json:"plain_token"`
		Signature  string `json:"signature"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sig, _ := hex.DecodeString(out.Signature)
	if out.PlainToken != "Arq0D5A61EgUu4OxUvOp" || !ed25519.Verify(a.priv.Public().(ed25519.PublicKey), []byte("1725442341Arq0D5A61EgUu4OxUvOp"), sig) {
		t.Fatalf("bad validation response: %s", w.Body.String())
	}
}

func TestWebhookDispatchRequiresSignature(t *testing.T) {
	var got []channel.Inbound
	a := newTestAdapter(&got)
	body := `{"op":0,"t":"GROUP_AT_MESSAGE_CREATE","d":{"id":"m1","content":" 怎么充值 ","group_openid":"G1","author":{"member_openid":"U1"}}}`

	w := httptest.NewRecorder()
	a.ServeWebhook(w, httptest.NewRequest(http.MethodPost, "/webhook/qqbot", strings.NewReader(body)))
	if w.Code != http.StatusUnauthorized || len(got) != 0 {
		t.Fatalf("unsigned request must be rejected, code=%d", w.Code)
	}

	ts := "1725442341"
	sig := ed25519.Sign(a.priv, []byte(ts+body))
	req := httptest.NewRequest(http.MethodPost, "/webhook/qqbot", strings.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	w = httptest.NewRecorder()
	a.ServeWebhook(w, req)
	if w.Code != http.StatusOK || len(got) != 1 {
		t.Fatalf("signed request should dispatch, code=%d got=%d", w.Code, len(got))
	}
	in := got[0]
	if in.ChatType != channel.ChatGroup || in.ChatID != "G1" || in.SenderID != "U1" || in.Text != "怎么充值" || !in.Mentioned {
		t.Fatalf("parsed = %+v", in)
	}
}

func TestGuildMentionStripped(t *testing.T) {
	var got []channel.Inbound
	a := newTestAdapter(&got)
	a.dispatch("AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m2","channel_id":"C1","content":"<@!12345> 你好","author":{"id":"A1","username":"小明"}}`))
	if len(got) != 1 || got[0].Text != "你好" || got[0].ChatType != channel.ChatGuild || got[0].SenderName != "小明" {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestGroupAllMessages(t *testing.T) {
	var got []channel.Inbound
	a := newTestAdapter(&got)
	a.dispatch("GROUP_MESSAGE_CREATE", json.RawMessage(`{"id":"m3","content":"有人知道怎么充值吗","group_openid":"G1","author":{"member_openid":"U1"}}`))
	if len(got) != 0 {
		t.Fatalf("groupAll off must ignore full-mode events, got %+v", got)
	}

	a.cfg.GroupAll = true
	a.dispatch("GROUP_MESSAGE_CREATE", json.RawMessage(`{"id":"m3","content":"有人知道怎么充值吗","group_openid":"G1","author":{"member_openid":"U1"}}`))
	a.dispatch("GROUP_MESSAGE_CREATE", json.RawMessage(`{"id":"m4","content":"<@BOTOPENID> 怎么充值","group_openid":"G1","author":{"member_openid":"U2"},"mentions":[{"member_openid":"BOTOPENID","is_you":true,"bot":true}]}`))
	a.dispatch("GROUP_MESSAGE_CREATE", json.RawMessage(`{"id":"m5","content":"自动回复","group_openid":"G1","author":{"member_openid":"B1","bot":true}}`))
	if len(got) != 2 {
		t.Fatalf("got %d inbound, want 2 (bot's own message skipped)", len(got))
	}
	if got[0].Mentioned || got[0].Text != "有人知道怎么充值吗" {
		t.Fatalf("plain message = %+v", got[0])
	}
	if !got[1].Mentioned || got[1].Text != "怎么充值" || got[1].SenderID != "U2" {
		t.Fatalf("mention message = %+v", got[1])
	}
}

func TestSendFallsBackToActiveMessage(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if _, passive := b["msg_id"]; passive {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":40034005,"message":"回复消息msg_id已过期"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	a := newTestAdapter(new([]channel.Inbound))
	a.cfg.AtUser = true
	a.api.base, a.api.token, a.api.expires = srv.URL, "tok", time.Now().Add(time.Hour)
	err := a.Send(context.Background(), channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "你好", ReplyTo: "m1", Seq: 1, AtUser: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("want passive then active, got %d calls", len(bodies))
	}
	if _, ok := bodies[1]["msg_id"]; ok {
		t.Fatalf("active retry must drop msg_id: %v", bodies[1])
	}
	if bodies[1]["content"] != `<qqbot-at-user id="U1" /> 你好` {
		t.Fatalf("content = %v", bodies[1]["content"])
	}
}

func TestMemberEvents(t *testing.T) {
	var got []channel.Inbound
	a := newTestAdapter(&got)
	a.dispatch("GROUP_MEMBER_ADD", json.RawMessage(`{"timestamp":1784276757,"group_openid":"G1","member_openid":"U9"}`))
	a.dispatch("GROUP_JOIN_REQUEST", json.RawMessage(`{"group_openid":"G1","join_request_id":"R1","member_openid":"U8","username":"小光",
		"verify_info":{"method":"admin_review_qa","review_qa_list":[{"question":"你的用户 ID？","answer":" Ab12Cd34Ef "}]}}`))
	if len(got) != 2 {
		t.Fatalf("got %d events", len(got))
	}
	if got[0].Kind != channel.KindMemberJoin || got[0].ChatID != "G1" || got[0].SenderID != "U9" {
		t.Fatalf("member add = %+v", got[0])
	}
	jr := got[1]
	if jr.Kind != channel.KindJoinRequest || jr.SenderName != "小光" || jr.JoinRequest == nil ||
		jr.JoinRequest.RequestID != "R1" || jr.JoinRequest.Answer != "Ab12Cd34Ef" || jr.JoinRequest.AutoApproved {
		t.Fatalf("join request = %+v %+v", jr, jr.JoinRequest)
	}
}

func TestIntentsMemberEvents(t *testing.T) {
	a := newTestAdapter(new([]channel.Inbound))
	if a.intents()&intentGroupMember != 0 {
		t.Fatal("member intent must be off by default")
	}
	a.opts.MemberEvents = true
	if a.intents()&intentGroupMember == 0 {
		t.Fatal("member intent expected")
	}
}

func TestSendMarkdown(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(new([]channel.Inbound))
	a.api.base, a.api.token, a.api.expires = srv.URL, "tok", time.Now().Add(time.Hour)
	if err := a.Send(context.Background(), channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "# 欢迎", Format: channel.FormatQQMarkdown}); err != nil {
		t.Fatal(err)
	}
	md, _ := body["markdown"].(map[string]any)
	if body["msg_type"] != float64(2) || md["content"] != "# 欢迎" || body["content"] != nil || body["msg_id"] != nil {
		t.Fatalf("body = %v", body)
	}
}

func TestSendReplyMarkdown(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["msg_type"] == float64(2) && strings.Contains(r.URL.Path, "/users/") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":304003,"message":"markdown not allowed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(new([]channel.Inbound))
	a.api.base, a.api.token, a.api.expires = srv.URL, "tok", time.Now().Add(time.Hour)
	text := "点 **新建**，再执行：\n```\nexport A=1\n```"

	// 群聊：渲染成 QQ Markdown（代码块转引用）
	if err := a.Send(context.Background(), channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: text, Format: channel.FormatMarkdown, ReplyTo: "m1"}); err != nil {
		t.Fatal(err)
	}
	md, _ := bodies[0]["markdown"].(map[string]any)
	if bodies[0]["msg_type"] != float64(2) || md["content"] != "点 **新建**，再执行：\n\n> export A=1" || bodies[0]["msg_id"] != "m1" {
		t.Fatalf("group body = %v", bodies[0])
	}
	// 单聊被拒：同一条改发纯文本
	bodies = nil
	if err := a.Send(context.Background(), channel.Outbound{ChatType: channel.ChatPrivate, ChatID: "U1", Text: text, Format: channel.FormatMarkdown, ReplyTo: "m2"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["msg_type"] != float64(0) || bodies[1]["content"] != "点 新建，再执行：\n\nexport A=1" || bodies[1]["markdown"] != nil {
		t.Fatalf("fallback bodies = %v", bodies)
	}
}

func TestQuoteReply(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if ref, _ := b["message_reference"].(map[string]any); ref != nil && ref["message_id"] == "REFIDX_gone" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":40054010,"message":"reference not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(new([]channel.Inbound))
	a.api.base, a.api.token, a.api.expires = srv.URL, "tok", time.Now().Add(time.Hour)
	a.dispatch("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m1","content":" 你好","group_openid":"G1","author":{"id":"U1"},
		"message_scene":{"source":"default","ext":["msg_idx=REFIDX_abc==","auth_token=t"]}}`))

	send := func(out channel.Outbound) map[string]any {
		t.Helper()
		bodies = nil
		if err := a.Send(context.Background(), out); err != nil {
			t.Fatal(err)
		}
		return bodies[len(bodies)-1]
	}
	b := send(channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "答", ReplyTo: "m1", Quote: true})
	if ref, _ := b["message_reference"].(map[string]any); ref["message_id"] != "REFIDX_abc==" || b["msg_id"] != "m1" {
		t.Fatalf("首条应引用 msg_idx：%v", b)
	}
	if b := send(channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "续", ReplyTo: "m1", Seq: 2}); b["message_reference"] != nil {
		t.Fatalf("后续拆条不引用：%v", b)
	}
	if b := send(channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "答", ReplyTo: "unknown", Quote: true}); b["message_reference"] != nil {
		t.Fatalf("没有 msg_idx 时不带引用：%v", b)
	}
	// 引用被拒：去掉引用重发
	a.refs.put("m2", "REFIDX_gone")
	b = send(channel.Outbound{ChatType: channel.ChatGroup, ChatID: "G1", Text: "答", ReplyTo: "m2", Quote: true})
	if len(bodies) != 2 || b["message_reference"] != nil || b["msg_id"] != "m2" {
		t.Fatalf("引用被拒应去掉引用重发：%v", bodies)
	}
}

func TestImageAttachments(t *testing.T) {
	var got []channel.Inbound
	a := newTestAdapter(&got)
	a.dispatch("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m1","content":" ","group_openid":"G1","author":{"id":"U1"},
		"attachments":[{"content_type":"image/png","filename":"a.png","size":100,"url":"multimedia.nt.qq.com.cn/download?a=1"},
			{"content_type":"file","filename":"b.zip","url":"https://x/b.zip"},
			{"content_type":"","filename":"c.JPG","url":"//gchat.qpic.cn/c"}]}`))
	if len(got) != 1 || got[0].Text != "" || len(got[0].Images) != 2 || got[0].Images[0].Size != 100 {
		t.Fatalf("应只取 2 张图片：%+v", got)
	}
}
