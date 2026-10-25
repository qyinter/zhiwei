package agent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"zhiwei/db/migrations"
	"zhiwei/internal/channel"
	"zhiwei/internal/kb"
	"zhiwei/internal/llm"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

// 端到端流程测试：真实 PostgreSQL（ZHIWEI_TEST_DSN）+ 假 LLM + 假渠道。

// fakeLLM 按 system / user 内容分辨调用类型并返回预设输出，同时记录每次请求。
type fakeLLM struct {
	mu    sync.Mutex
	reqs  []llm.Request
	reply func(req llm.Request) string
	// fail 非 nil 且返回错误时本次调用失败（模拟上游拒收）
	fail func(req llm.Request) error
}

func (f *fakeLLM) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	req.Messages = append([]llm.Message(nil), req.Messages...) // 调用方之后会改写自己的切片
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if f.fail != nil {
		if err := f.fail(req); err != nil {
			return llm.Response{Usage: llm.Usage{InputTokens: 10}}, err
		}
	}
	return llm.Response{Content: f.reply(req), Usage: llm.Usage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 60, CacheWriteTokens: 5}}, nil
}

func (f *fakeLLM) Model() string { return "fake" }

func (f *fakeLLM) count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if strings.Contains(r.System, substr) {
			n++
		}
	}
	return n
}

// fakeAdapter 记录发出的消息与入群审批。
type fakeAdapter struct {
	mu      sync.Mutex
	sent    []channel.Outbound
	reviews []bool
}

func (a *fakeAdapter) Name() string                                 { return channel.QQBot }
func (a *fakeAdapter) Start(context.Context, channel.Handler) error { return nil }
func (a *fakeAdapter) Stop()                                        {}
func (a *fakeAdapter) Status() channel.Status                       { return channel.Status{} }
func (a *fakeAdapter) Send(_ context.Context, out channel.Outbound) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, out)
	return nil
}
func (a *fakeAdapter) ReviewJoin(_ context.Context, _, _ string, _ channel.JoinRequest, approve bool, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reviews = append(a.reviews, approve)
	return nil
}

func (a *fakeAdapter) texts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, s := range a.sent {
		out = append(out, s.Text)
	}
	return out
}

type harness struct {
	o   *Orchestrator
	st  *store.Store
	llm *fakeLLM
	ad  *fakeAdapter
	cfg settings.All
}

func newHarness(t *testing.T, reply func(llm.Request) string) *harness {
	t.Helper()
	dsn := os.Getenv("ZHIWEI_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 ZHIWEI_TEST_DSN，跳过数据库测试")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool().Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, st.Pool(), migrations.PG, migrations.PGDir); err != nil {
		t.Fatal(err)
	}
	ad := &fakeAdapter{}
	reg := channel.NewRegistry()
	reg.Replace(ctx, []channel.Adapter{ad}, nil)
	cfg := settings.Defaults()
	cfg.Setup.Completed = true
	cfg.Agent.AckEnabled = false
	idx := kb.NewIndex(st)
	o := NewOrchestrator(st, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f := &fakeLLM{reply: reply}
	o.cfg, o.index, o.brain = cfg, idx, NewBrain(f, idx, cfg, nil, nil)
	return &harness{o: o, st: st, llm: f, ad: ad, cfg: cfg}
}

func (h *harness) groupMsg(t *testing.T, user, role, text, msgID string, at time.Time) {
	t.Helper()
	_, err := h.st.Pool().Exec(context.Background(), `INSERT INTO group_messages (channel, chat_id, user_id, user_name, role, content, platform_msg_id, created_at)
		VALUES ('qqbot', 'G1', $1, $1, $2, $3, $4, $5)`, user, role, text, msgID, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProactiveFlow(t *testing.T) {
	h := newHarness(t, func(req llm.Request) string {
		if strings.Contains(req.System, "分诊员") {
			if strings.Contains(req.Messages[0].Content, "哈哈") {
				return `{"answer": false}`
			}
			return `{"answer": true}`
		}
		return "在控制台的令牌页面创建 API Key。"
	})
	ctx := context.Background()
	chat := store.ChatRef{Channel: "qqbot", ChatType: "group", ChatID: "G1"}
	cfg := h.cfg
	cfg.Proactive = settings.Proactive{Enabled: true, SilenceSec: 60, ScanSec: 1, MaxPerScan: 2}

	// 冷启动：第一次扫描只建游标，不补位
	h.groupMsg(t, "U0", "member", "上线前的旧问题怎么办", "m0", time.Now().Add(-10*time.Minute))
	if err := h.o.proactiveChat(ctx, cfg, chat); err != nil {
		t.Fatal(err)
	}
	if len(h.ad.texts()) != 0 {
		t.Fatalf("cold start must not reply, sent %v", h.ad.texts())
	}
	// 把游标拨回 5 分钟前，模拟之后来了新消息
	_ = h.st.SetCursor(ctx, "proactive:qqbot:G1", time.Now().Add(-5*time.Minute))
	old := time.Now().Add(-2 * time.Minute)
	h.groupMsg(t, "U3", "member", "怎么充值", "m4", old)
	h.groupMsg(t, "A1", "admin", "充值在充值页", "m5", old.Add(10*time.Second)) // 管理员在 U3 之后发过言 → 抑制 U3
	h.groupMsg(t, "U1", "member", "API Key 在哪里创建", "m1", old.Add(20*time.Second))
	h.groupMsg(t, "U1", "member", "找了半天", "m2", old.Add(21*time.Second))
	h.groupMsg(t, "U2", "member", "哈哈哈", "m3", old.Add(22*time.Second))
	h.groupMsg(t, "U4", "member", "刚问的", "m6", time.Now()) // 还在静默期内

	if err := h.o.proactiveChat(ctx, cfg, chat); err != nil {
		t.Fatal(err)
	}
	sent := h.ad.texts()
	if len(sent) != 1 || !strings.Contains(sent[0], "令牌页面") {
		t.Fatalf("expected exactly one proactive reply to U1, got %v", sent)
	}
	if h.ad.sent[0].ReplyTo != "m2" {
		t.Fatalf("reply should quote the user's latest message, got %q", h.ad.sent[0].ReplyTo)
	}
	// U1 的两条合并成一个问题；U2 被分诊拒绝；U3 被管理员发言抑制（不调分诊）
	if n := h.llm.count("分诊员"); n != 2 {
		t.Fatalf("judge calls = %d, want 2 (U1, U2)", n)
	}
	replies, _ := h.st.ListProactiveReplies(ctx, 10)
	if len(replies) != 1 || replies[0].UserID != "U1" || replies[0].Question != "API Key 在哪里创建\n找了半天" {
		t.Fatalf("records = %+v", replies)
	}
	conv, err := h.st.ConversationByKey(ctx, "qqbot", "G1", "U1")
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := h.st.RecentMessages(ctx, conv.ID, time.Time{}, 10)
	if len(msgs) != 3 {
		t.Fatalf("conversation should hold 2 questions + 1 reply, got %d", len(msgs))
	}
	// 再扫一遍：游标已推进，不重复补位
	if err := h.o.proactiveChat(ctx, cfg, chat); err != nil {
		t.Fatal(err)
	}
	if len(h.ad.texts()) != 1 {
		t.Fatalf("second scan must not reply again, sent %v", h.ad.texts())
	}
}

func TestReflectFlow(t *testing.T) {
	h := newHarness(t, func(req llm.Request) string {
		switch {
		case strings.Contains(req.System, "知识运营助手"):
			return `{"items":[{"question":"怎么充值","answer":"控制台充值页","effective":true,"faq":"充值方法：在控制台的充值页面选择金额并完成支付，一般 1 分钟内到账。"},
				{"question":"在吗","answer":"在","effective":false,"faq":"在"}]}`
		case strings.Contains(req.System, "整理助手"):
			return `{"items":[{"faq":"充值：控制台充值页选择金额支付，一般 1 分钟内到账；未到账请刷新。"}]}`
		}
		return ""
	})
	ctx := context.Background()
	chat := store.ChatRef{Channel: "qqbot", ChatType: "group", ChatID: "G1"}
	base := time.Now().Add(-30 * time.Minute)
	h.groupMsg(t, "U1", "member", "怎么充值", "m1", base)
	h.groupMsg(t, "A1", "owner", "在控制台的充值页面选择金额支付就行，一般 1 分钟内到账", "m2", base.Add(time.Minute))
	h.groupMsg(t, "U1", "member", "好的谢谢", "m3", base.Add(2*time.Minute))

	if err := h.o.reflectChat(ctx, h.cfg, chat); err != nil {
		t.Fatal(err)
	}
	entries, _ := h.st.ListReflections(ctx, store.ReflectApproved, 10)
	if len(entries) != 1 || !strings.Contains(entries[0].FAQ, "充值方法") {
		t.Fatalf("entries = %+v", entries)
	}
	// 沉淀立即进入检索
	if hits := h.o.kbIndex().Search(ctx, "怎么充值", 3); len(hits) == 0 || hits[0].ReflectionID != entries[0].ID {
		t.Fatalf("reflection should be searchable, hits = %+v", hits)
	}
	// 再提炼同样内容：判重，不新增
	_ = h.st.SetCursor(ctx, "reflect:qqbot:G1", base.Add(-time.Minute))
	if err := h.o.reflectChat(ctx, h.cfg, chat); err != nil {
		t.Fatal(err)
	}
	if entries, _ = h.st.ListReflections(ctx, store.ReflectApproved, 10); len(entries) != 1 {
		t.Fatalf("duplicate must be skipped, entries = %d", len(entries))
	}
	// 区间内无客服发言：不调模型，直接推进
	calls := h.llm.count("知识运营助手")
	_ = h.st.SetCursor(ctx, "reflect:qqbot:G1", base.Add(5*time.Minute))
	if err := h.o.reflectChat(ctx, h.cfg, chat); err != nil {
		t.Fatal(err)
	}
	if h.llm.count("知识运营助手") != calls {
		t.Fatal("no staff message → no LLM call")
	}

	// 整理：两条近义条目合并为一条
	r2 := store.Reflection{FAQ: "充值到账时间：一般 1 分钟内"}
	_ = h.st.AddReflection(ctx, &r2)
	cfg := h.cfg
	cfg.Reflect.CompactMinEntries = 2
	msg, err := h.o.compactReflections(ctx, cfg)
	if err != nil || !strings.Contains(msg, "2 → 1") {
		t.Fatalf("compact = %q %v", msg, err)
	}
	entries, _ = h.st.ListReflections(ctx, store.ReflectApproved, 10)
	if len(entries) != 1 || !entries[0].Compacted {
		t.Fatalf("after compact = %+v", entries)
	}

	// 升格：编造的 id 被忽略，真实 id 升格为知识库文档
	id := entries[0].ID
	h.llm.reply = func(req llm.Request) string {
		return strings.ReplaceAll(`{"decisions":[{"id":999,"promote":true},{"id":%ID%,"promote":true}]}`, "%ID%", strconv.FormatInt(id, 10))
	}
	msg, err = h.o.promoteReview(ctx, cfg)
	if err != nil || !strings.Contains(msg, "升格 #") {
		t.Fatalf("promote = %q %v", msg, err)
	}
	r, _ := h.st.Reflection(ctx, id)
	if r.Status != store.ReflectPromoted || r.PromotedDocID == nil {
		t.Fatalf("promoted = %+v", r)
	}
	docs, _ := h.st.ListKBDocuments(ctx)
	if len(docs) != 1 || docs[0].ChunkCount == 0 {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestWelcomeAndJoinReview(t *testing.T) {
	h := newHarness(t, func(llm.Request) string { return "" })
	ctx := context.Background()
	cfg := h.cfg
	cfg.Welcome.Enabled = true
	h.o.welcome(ctx, cfg, channel.Inbound{Kind: channel.KindMemberJoin, Channel: channel.QQBot, ChatType: channel.ChatGroup, ChatID: "G1", SenderID: "U9"})
	if sent := h.ad.texts(); len(sent) != 1 || !strings.Contains(sent[0], `<qqbot-at-user id="U9" /> 你好`) ||
		h.ad.sent[0].ReplyTo != "" || h.ad.sent[0].Format != channel.FormatQQMarkdown {
		t.Fatalf("welcome = %v", sent)
	}

	// 核对接口不可达 → 留给管理员，不审批
	cfg.JoinReview.Enabled = true
	cfg.JoinReview.CheckURL = "http://127.0.0.1:1/check"
	h.o.reviewJoin(cfg, channel.Inbound{Kind: channel.KindJoinRequest, Channel: channel.QQBot, ChatType: channel.ChatGroup, ChatID: "G1",
		SenderID: "U8", SenderName: "小光", JoinRequest: &channel.JoinRequest{RequestID: "R1", Answer: "ID: Ab12Cd34Ef"}})
	// 没填答案 → 直接拒绝
	h.o.reviewJoin(cfg, channel.Inbound{Kind: channel.KindJoinRequest, Channel: channel.QQBot, ChatType: channel.ChatGroup, ChatID: "G1",
		SenderID: "U7", JoinRequest: &channel.JoinRequest{RequestID: "R2"}})
	if len(h.ad.reviews) != 1 || h.ad.reviews[0] {
		t.Fatalf("reviews = %v, want one decline", h.ad.reviews)
	}
	list, _, _ := h.st.ListJoinRequests(ctx, 1, 20)
	if len(list) != 2 || list[1].Decision != store.JoinPending || list[1].ExtractedID != "Ab12Cd34Ef" || list[0].Decision != store.JoinDeclined {
		t.Fatalf("records = %+v", list)
	}
}

func TestTopicsFlow(t *testing.T) {
	h := newHarness(t, func(req llm.Request) string {
		u := req.Messages[0].Content
		switch {
		case strings.Contains(u, "怎么退款"):
			return `{"items":[{"i":0,"newTitle":"退款流程"},{"i":1,"noise":true},{"i":2,"newTitle":"退款 流程"},{"i":3,"topicId":999}]}`
		case strings.Contains(u, "私聊问充值"):
			return `{"items":[{"i":0,"newTitle":"充值方式"}]}`
		case strings.Contains(u, "模型总答不好"):
			return "抱歉，我无法处理这个请求。"
		}
		return `{"items":[]}`
	})
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	h.groupMsg(t, "U1", "member", "请问怎么退款", "m1", base)
	h.groupMsg(t, "U2", "member", "哈哈哈哈", "m2", base.Add(time.Second))
	h.groupMsg(t, "A1", "admin", "退款找人工", "m3", base.Add(2*time.Second)) // 管理员的是答复，不算提问
	h.groupMsg(t, "U3", "member", "退款要多久到账", "m4", base.Add(3*time.Second))
	h.groupMsg(t, "U4", "member", "随便问问别的", "m5", base.Add(4*time.Second))
	h.groupMsg(t, "U5", "member", "好", "m6", base.Add(5*time.Second)) // 太短，不送模型
	conv, _ := h.st.UpsertConversation(ctx, store.ConvKey{Channel: "qqbot", ChatType: "private", ChatID: "P1", UserID: "P1"})
	pm := store.Message{ConversationID: conv.ID, Direction: store.DirIn, Content: "私聊问充值怎么弄"}
	_ = h.st.AddMessage(ctx, &pm)
	_, _ = h.st.Pool().Exec(ctx, `UPDATE messages SET created_at = $2 WHERE id = $1`, pm.ID, base) // 越过归类前的静置期
	h.o.cfg.Topics.WindowMax = 50

	n, err := h.o.ScanTopics(ctx)
	if err != nil || n != 3 {
		t.Fatalf("应归类 3 条（退款 ×2 近义并入 + 充值 ×1）：n=%d err=%v", n, err)
	}
	ranking, err := h.st.TopicRanking(ctx, base.Add(-time.Hour), 10)
	if err != nil || len(ranking) != 2 || ranking[0].Title != "退款流程" || ranking[0].Count != 2 || ranking[0].Users != 2 {
		t.Fatalf("排行 = %+v %v", ranking, err)
	}
	if len(ranking[0].Samples) != 2 || ranking[1].Title != "充值方式" {
		t.Fatalf("样例与私聊来源：%+v", ranking)
	}
	// 再扫：游标已推进，不重复计数
	if n, err := h.o.ScanTopics(ctx); err != nil || n != 0 {
		t.Fatalf("second scan n=%d err=%v", n, err)
	}
	if n := h.llm.count("问题归类助手"); n != 2 {
		t.Fatalf("归类调用 %d 次，want 2（群 + 私聊各一批）", n)
	}
	// 单群关闭问题排行：新消息不再归类
	off := false
	h.o.cfg.Groups.Policies = []settings.GroupPolicy{{Chat: "qqbot:G1", Topics: &off}}
	h.groupMsg(t, "U6", "member", "请问怎么退款呀", "m7", time.Now().Add(-5*time.Minute))
	if n, _ := h.o.ScanTopics(ctx); n != 0 {
		t.Fatalf("关闭的群不该归类，n=%d", n)
	}

	// 某一批模型一直答不出 JSON：前两轮报错、游标不动，第三轮跳过这一批，后面的消息照常归类
	bad := store.Message{ConversationID: conv.ID, Direction: store.DirIn, Content: "这条模型总答不好"}
	_ = h.st.AddMessage(ctx, &bad)
	_, _ = h.st.Pool().Exec(ctx, `UPDATE messages SET created_at = $2 WHERE id = $1`, bad.ID, time.Now().Add(-10*time.Minute))
	before := time.Now().Add(-20 * time.Minute).Truncate(time.Microsecond)
	_ = h.st.SetCursor(ctx, topicPrivateCursor, before)
	for i := 1; i < topicMaxFails; i++ {
		if _, err := h.o.ScanTopics(ctx); err == nil {
			t.Fatalf("第 %d 轮应报错", i)
		}
		if at, _, _ := h.st.Cursor(ctx, topicPrivateCursor); !at.Equal(before) {
			t.Fatalf("第 %d 轮游标不该动", i)
		}
	}
	if _, err := h.o.ScanTopics(ctx); err != nil {
		t.Fatalf("第 %d 轮应跳过这一批：%v", topicMaxFails, err)
	}
	if at, _, _ := h.st.Cursor(ctx, topicPrivateCursor); !at.After(before) {
		t.Fatal("跳过后游标应越过这一批")
	}
}

func TestTrimTies(t *testing.T) {
	t0 := time.Now()
	src := func(id int64, at time.Time) store.TopicSource { return store.TopicSource{ID: id, At: at} }
	got := trimTies([]store.TopicSource{src(1, t0), src(2, t0.Add(time.Second)), src(3, t0.Add(time.Second))})
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("末尾同一时刻的应留给下一批：%+v", got)
	}
	same := []store.TopicSource{src(1, t0), src(2, t0)}
	if got := trimTies(same); len(got) != 2 {
		t.Fatalf("整批同一时刻只能原样推进：%+v", got)
	}
}

// 带图消息：下载缩放后存库、随 user 消息交给模型；用量记下图片数与工具名。
func TestImageTurn(t *testing.T) {
	h := newHarness(t, func(req llm.Request) string {
		if req.HasImages() {
			return "看到了，是余额不足的报错。"
		}
		return "没看到图"
	})
	ctx := context.Background()
	img := channel.Image{ContentType: "image/png", Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(pngBytes(t))), nil
	}}
	h.o.Handle(channel.Inbound{Channel: channel.QQBot, ChatType: channel.ChatPrivate, ChatID: "U1", SenderID: "U1",
		MessageID: "m1", Text: "这个报错什么意思", Mentioned: true, Images: []channel.Image{img}})
	waitFor(t, func() bool { return len(h.ad.texts()) > 0 })
	if sent := h.ad.texts(); !strings.Contains(sent[0], "余额不足") {
		t.Fatalf("模型应看到图片：%v", sent)
	}
	conv, _ := h.st.ConversationByKey(ctx, "qqbot", "U1", "U1")
	msgs, _ := h.st.ListMessages(ctx, conv.ID, 0, 10)
	if len(msgs) < 1 || msgs[0].Content != "这个报错什么意思 [图片]" || len(msgs[0].Images) != 1 {
		t.Fatalf("入站消息应带占位与图片：%+v", msgs)
	}
	if im, err := h.st.MessageImage(ctx, msgs[0].Images[0].ID); err != nil || im.MIME != "image/png" || len(im.Data) == 0 {
		t.Fatalf("图片字节：%v", err)
	}
	var images int
	var tools []string
	_ = h.st.Pool().QueryRow(ctx, `SELECT images, tool_names FROM llm_calls ORDER BY id DESC LIMIT 1`).Scan(&images, &tools)
	if images != 1 {
		t.Fatalf("用量应记 1 张图：%d %v", images, tools)
	}

	// 关闭识图：图片照样存，但不交给模型
	h.o.cfg.Agent.Vision = false
	h.o.Handle(channel.Inbound{Channel: channel.QQBot, ChatType: channel.ChatPrivate, ChatID: "U1", SenderID: "U1",
		MessageID: "m2", Text: "再看这个", Mentioned: true, Images: []channel.Image{img}})
	waitFor(t, func() bool { return len(h.ad.texts()) > 1 })
	if sent := h.ad.texts(); sent[1] != "没看到图" {
		t.Fatalf("关闭识图后不应带图：%v", sent)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等待超时")
}
