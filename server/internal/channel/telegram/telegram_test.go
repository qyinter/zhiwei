package telegram

import (
	"context"
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

func parseJSON(t *testing.T, a *Adapter, raw string) (string, bool, bool) {
	t.Helper()
	var m tgMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	in, ok := a.parse(&m)
	return in.Text, in.Mentioned, ok
}

func TestParseMentions(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID, a.botUsername = 99, "ZhiweiBot"

	// 表情占 2 个 UTF-16 码元：偏移必须按 UTF-16 计算
	text, mentioned, ok := parseJSON(t, a, `{"message_id":1,"from":{"id":1,"first_name":"A"},"chat":{"id":-100,"type":"supergroup"},
		"text":"😀 @zhiweibot 怎么充值","entities":[{"type":"mention","offset":3,"length":10}]}`)
	if !ok || !mentioned || text != "😀  怎么充值" {
		t.Fatalf("got text=%q mentioned=%v ok=%v", text, mentioned, ok)
	}

	_, mentioned, _ = parseJSON(t, a, `{"message_id":2,"from":{"id":1},"chat":{"id":-100,"type":"group"},"text":"大家好"}`)
	if mentioned {
		t.Fatal("plain group message must not count as mention")
	}

	_, mentioned, _ = parseJSON(t, a, `{"message_id":3,"from":{"id":1},"chat":{"id":-100,"type":"group"},"text":"那退款呢",
		"reply_to_message":{"message_id":2,"from":{"id":99},"text":"支持支付宝"}}`)
	if !mentioned {
		t.Fatal("reply to bot should count as mention")
	}

	_, _, ok = parseJSON(t, a, `{"message_id":4,"from":{"id":99},"chat":{"id":5,"type":"private"},"text":"自己发的"}`)
	if ok {
		t.Fatal("bot's own messages must be ignored")
	}
}

func TestMemberJoins(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID = 99
	var m tgMessage
	_ = json.Unmarshal([]byte(`{"message_id":5,"chat":{"id":-100,"type":"supergroup","title":"用户群"},
		"new_chat_members":[{"id":7,"first_name":"Li","last_name":"Lei"},{"id":99,"is_bot":true,"first_name":"Bot"}]}`), &m)
	got := a.memberJoins(&m)
	if len(got) != 1 || got[0].SenderID != "7" || got[0].SenderName != "Li Lei" || got[0].ChatID != "-100" || got[0].ChatTitle != "用户群" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := a.parse(&m); ok {
		t.Fatal("service message without text must not be parsed as a message")
	}
}

func TestSendHTML(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if b["parse_mode"] == "HTML" && strings.Contains(b["text"].(string), "坏") {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unsupported start tag"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	a := New(settings.Telegram{BotToken: "T", APIBase: srv.URL}, slog.Default())

	if err := a.Send(context.Background(), channel.Outbound{ChatID: "42", Text: "点 **新建**", Format: channel.FormatMarkdown, ReplyTo: "7", Quote: true}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["parse_mode"] != "HTML" || bodies[0]["text"] != "点 <b>新建</b>" || bodies[0]["reply_parameters"] == nil {
		t.Fatalf("html body = %v", bodies[0])
	}
	bodies = nil
	if err := a.Send(context.Background(), channel.Outbound{ChatID: "42", Text: "坏 **x**", Format: channel.FormatMarkdown}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["parse_mode"] != nil || bodies[1]["text"] != "坏 x" {
		t.Fatalf("fallback bodies = %v", bodies)
	}
}

func TestParsePhoto(t *testing.T) {
	a := New(settings.Telegram{Private: true}, slog.Default())
	a.botID, a.botUsername = 99, "ZhiweiBot"
	var m tgMessage
	// 群里带说明的图片：@ 在 caption_entities 里；photo 取不超过上限的最大尺寸；引用的图也带上
	_ = json.Unmarshal([]byte(`{"message_id":5,"from":{"id":1},"chat":{"id":-100,"type":"supergroup"},
		"caption":"@ZhiweiBot 这是啥","caption_entities":[{"type":"mention","offset":0,"length":10}],
		"photo":[{"file_id":"s","file_size":1000},{"file_id":"l","file_size":90000000},{"file_id":"m","file_size":50000}],
		"reply_to_message":{"message_id":4,"from":{"id":2},"chat":{"id":-100,"type":"supergroup"},"document":{"file_id":"d","mime_type":"image/webp","file_size":10}}}`), &m)
	in, ok := a.parse(&m)
	if !ok || !in.Mentioned || in.Text != "这是啥" || len(in.Images) != 2 {
		t.Fatalf("parse = %+v ok=%v", in, ok)
	}
	// 私聊里只发图、不带文字也要处理
	m = tgMessage{}
	_ = json.Unmarshal([]byte(`{"message_id":6,"from":{"id":1},"chat":{"id":1,"type":"private"},"photo":[{"file_id":"s","file_size":10}]}`), &m)
	if in, ok := a.parse(&m); !ok || in.Text != "" || len(in.Images) != 1 {
		t.Fatalf("纯图片私聊 = %+v ok=%v", in, ok)
	}
}

func TestParseFeatures(t *testing.T) {
	a := New(settings.Telegram{Private: false}, slog.Default())
	a.botID, a.botUsername = 99, "ZhiweiBot"
	var m tgMessage
	// 链接实体、文字链接、@ 其他人、频道转发
	_ = json.Unmarshal([]byte(`{"message_id":7,"from":{"id":1},"chat":{"id":-100,"type":"supergroup"},
		"text":"看 https://spam.example 和 这里 @someone",
		"entities":[{"type":"url","offset":2,"length":20},{"type":"text_link","offset":25,"length":2,"url":"https://t.me/x"},{"type":"mention","offset":28,"length":8}],
		"forward_origin":{"type":"channel"}}`), &m)
	in, ok := a.parse(&m)
	if !ok || in.Mentioned || !in.MentionsUser || in.ForwardFrom != "channel" || len(in.Links) != 2 || in.Links[1] != "https://spam.example" || in.Links[0] != "https://t.me/x" {
		t.Fatalf("parse = %+v ok=%v", in, ok)
	}
	// 关闭「响应私聊」：私聊仍上报，但 Mentioned=false
	m = tgMessage{}
	_ = json.Unmarshal([]byte(`{"message_id":8,"from":{"id":1},"chat":{"id":1,"type":"private"},"text":"Ab12Cd34Ef"}`), &m)
	if in, ok := a.parse(&m); !ok || in.Mentioned || in.ChatType != channel.ChatPrivate || in.Text != "Ab12Cd34Ef" {
		t.Fatalf("private = %+v ok=%v", in, ok)
	}
}

func TestJoinedAndDedupe(t *testing.T) {
	a := New(settings.Telegram{}, slog.Default())
	a.botID = 99
	var cm tgChatMemberUpdated
	_ = json.Unmarshal([]byte(`{"chat":{"id":-100,"type":"supergroup","title":"用户群"},
		"old_chat_member":{"status":"left"},"new_chat_member":{"status":"member","user":{"id":7,"first_name":"Li"}}}`), &cm)
	if !joined(&cm) {
		t.Fatal("left → member must count as joined")
	}
	in, ok := a.memberJoin(cm.Chat, cm.New.User)
	if !ok || in.Kind != channel.KindMemberJoin || in.SenderID != "7" || in.ChatTitle != "用户群" {
		t.Fatalf("join = %+v ok=%v", in, ok)
	}
	// 同一人的服务消息紧接着到：去重
	var m tgMessage
	_ = json.Unmarshal([]byte(`{"message_id":5,"chat":{"id":-100,"type":"supergroup"},"new_chat_members":[{"id":7,"first_name":"Li"},{"id":8,"first_name":"Wang"}]}`), &m)
	if got := a.memberJoins(&m); len(got) != 1 || got[0].SenderID != "8" {
		t.Fatalf("dedupe = %+v", got)
	}
	// 被限制且非成员 → 成员：算入群；管理员降为成员：不算
	_ = json.Unmarshal([]byte(`{"chat":{"id":-100,"type":"supergroup"},"old_chat_member":{"status":"restricted","is_member":false},"new_chat_member":{"status":"restricted","is_member":true,"user":{"id":9}}}`), &cm)
	if !joined(&cm) {
		t.Fatal("restricted non-member → restricted member must count as joined")
	}
	_ = json.Unmarshal([]byte(`{"chat":{"id":-100,"type":"supergroup"},"old_chat_member":{"status":"administrator"},"new_chat_member":{"status":"member","user":{"id":9}}}`), &cm)
	if joined(&cm) {
		t.Fatal("admin → member must not count as joined")
	}
}

func TestJoinRequestAndModeration(t *testing.T) {
	var calls []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	a := New(settings.Telegram{BotToken: "T", APIBase: srv.URL}, slog.Default())
	ctx := context.Background()

	if err := a.ReviewJoin(ctx, "-100", "7", channel.JoinRequest{}, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.ReviewJoin(ctx, "-100", "7", channel.JoinRequest{}, false, "理由"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteMessage(ctx, "-100", "42"); err != nil {
		t.Fatal(err)
	}
	if err := a.MuteMember(ctx, "-100", "7", time.Unix(1_900_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := a.BanMember(ctx, "-100", "7"); err != nil {
		t.Fatal(err)
	}
	if err := a.Unrestrict(ctx, "-100", "7"); err != nil {
		t.Fatal(err)
	}
	want := []string{"approveChatJoinRequest", "declineChatJoinRequest", "deleteMessage", "restrictChatMember", "banChatMember", "unbanChatMember", "restrictChatMember"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v", calls)
	}
	if bodies[2]["message_id"] != float64(42) || bodies[3]["until_date"] != float64(1_900_000_000) {
		t.Fatalf("bodies = %v", bodies)
	}
	perms := bodies[3]["permissions"].(map[string]any)
	if perms["can_send_messages"] != false || bodies[6]["permissions"].(map[string]any)["can_send_messages"] != true {
		t.Fatalf("permissions = %v / %v", perms, bodies[6]["permissions"])
	}
	if bodies[5]["only_if_banned"] != true {
		t.Fatalf("unban body = %v", bodies[5])
	}
	if err := a.DeleteMessage(ctx, "x", "1"); err == nil || err.Error() != "telegram chat_id 非法" {
		t.Fatalf("bad chat id err = %v", err)
	}
}
