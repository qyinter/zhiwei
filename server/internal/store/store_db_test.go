package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"zhiwei/db/migrations"
	"zhiwei/internal/store"
)

// 需要真实 PostgreSQL：设置 ZHIWEI_TEST_DSN 指向一个可清空的测试库才运行，例如
//
//	ZHIWEI_TEST_DSN='postgres://zhiwei:zhiwei@127.0.0.1:5432/zhiwei_test?sslmode=disable' go test ./internal/store/
//
// 每次运行先清空 public schema 再跑全部迁移。
func openTestStore(t *testing.T) *store.Store {
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
	return st
}

func TestGroupTimeline(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	chat := store.ChatRef{Channel: "qqbot", ChatType: "group", ChatID: "G1"}
	add := func(user, role, text, msgID string, mentioned bool) {
		t.Helper()
		if err := st.AddGroupMessage(ctx, store.GroupMessage{Channel: "qqbot", ChatType: "group", ChatID: "G1", UserID: user,
			Role: role, Content: text, PlatformMsgID: msgID, Mentioned: mentioned}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(-time.Second)
	add("U1", "member", "怎么充值", "m1", false)
	add("U1", "member", "怎么充值", "m1", true) // 同一条以 @ 事件再推一次：只合并 mentioned
	add("U2", "member", "API Key 在哪里", "m2", false)
	add("A1", "admin", "控制台-令牌页面", "m3", false)
	now := time.Now().Add(time.Second)

	cands, err := st.UnansweredCandidates(ctx, chat, start, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].UserID != "U2" {
		t.Fatalf("candidates = %+v（m1 被合并为已 @，管理员发言不是候选）", cands)
	}
	if ok, err := st.HasStaffMessageBetween(ctx, chat, start, now); err != nil || !ok {
		t.Fatalf("staff = %v %v", ok, err)
	}
	win, err := st.GroupWindow(ctx, chat, cands[0].CreatedAt, now, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 游标前 1 条上下文（m2 本身）+ 游标后的 m3
	if len(win) != 2 || win[0].PlatformMsgID != "m2" || win[1].PlatformMsgID != "m3" {
		t.Fatalf("window = %+v", win)
	}
	chats, err := st.GroupChats(ctx)
	if err != nil || len(chats) != 1 || chats[0] != chat {
		t.Fatalf("chats = %+v %v", chats, err)
	}
	if _, err := st.GroupChatStats(ctx); err != nil {
		t.Fatal(err)
	}
	// 问题排行还没归类到的部分保留：游标在 start，三条都晚于它
	if n, err := st.PruneGroupMessages(ctx, now, []store.ChatFloor{{Chat: "qqbot:G1", After: start}}); err != nil || n != 0 {
		t.Fatalf("pruned with floor %d %v", n, err)
	}
	if n, err := st.PruneGroupMessages(ctx, now, []store.ChatFloor{{Chat: "qqbot:G2", After: start}}); err != nil || n != 3 {
		t.Fatalf("pruned %d %v", n, err)
	}
}

func TestCursors(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, ok, err := st.Cursor(ctx, "proactive:qqbot:G1"); err != nil || ok {
		t.Fatalf("missing cursor: ok=%v err=%v", ok, err)
	}
	at := time.Now().Truncate(time.Microsecond)
	if err := st.SetCursor(ctx, "proactive:qqbot:G1", at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Cursor(ctx, "proactive:qqbot:G1")
	if err != nil || !ok || !got.Equal(at) {
		t.Fatalf("cursor = %v %v %v", got, ok, err)
	}
	all, err := st.CursorsByPrefix(ctx, "proactive:")
	if err != nil || len(all) != 1 {
		t.Fatalf("prefix = %v %v", all, err)
	}
}

func TestReflectionLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	var ids []int64
	for _, faq := range []string{"充值：在控制台充值页面选择金额", "充值到账：一般 1 分钟内到账", "令牌：在令牌页面创建"} {
		r := store.Reflection{Channel: "qqbot", ChatID: "G1", FAQ: faq}
		if err := st.AddReflection(ctx, &r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	chunks, err := st.EnabledKBChunks(ctx, "")
	if err != nil || len(chunks) != 3 || chunks[0].ReflectionID == 0 || chunks[0].DocTitle != store.ReflectionDocTitle {
		t.Fatalf("chunks = %+v %v", chunks, err)
	}

	if err := st.SetReflectionStatus(ctx, ids[2], store.ReflectRejected); err != nil {
		t.Fatal(err)
	}
	approved, _ := st.ListReflections(ctx, store.ReflectApproved, 100)
	if len(approved) != 2 {
		t.Fatalf("approved = %d", len(approved))
	}
	if err := st.CompactReflections(ctx, approved, []string{"充值：在控制台充值页面选择金额，一般 1 分钟内到账"}); err != nil {
		t.Fatal(err)
	}
	approved, _ = st.ListReflections(ctx, store.ReflectApproved, 100)
	if len(approved) != 1 || !approved[0].Compacted {
		t.Fatalf("after compact = %+v", approved)
	}
	cps, err := st.ListCompactions(ctx, 10)
	if err != nil || len(cps) != 1 || cps[0].BeforeCount != 2 || cps[0].AfterCount != 1 {
		t.Fatalf("compactions = %+v %v", cps, err)
	}
	if _, err := st.Compaction(ctx, cps[0].ID); err != nil {
		t.Fatal(err)
	}

	doc := store.KBDocument{Title: "升格沉淀", Content: approved[0].FAQ, Enabled: true}
	if err := st.PromoteReflection(ctx, approved[0].ID, &doc, []store.ChunkInput{{Content: approved[0].FAQ}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PromoteReflection(ctx, approved[0].ID, &store.KBDocument{Title: "x"}, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("promoting twice must fail, got %v", err)
	}
	r, _ := st.Reflection(ctx, approved[0].ID)
	if r.Status != store.ReflectPromoted || r.PromotedDocID == nil || *r.PromotedDocID != doc.ID {
		t.Fatalf("promoted = %+v", r)
	}
	// 升格后：沉淀条目不再单独检索，文档块进入检索
	chunks, _ = st.EnabledKBChunks(ctx, "")
	if len(chunks) != 1 || chunks[0].ReflectionID != 0 || chunks[0].DocumentID != doc.ID {
		t.Fatalf("chunks after promote = %+v", chunks)
	}
	counts, err := st.ReflectionCounts(ctx)
	if err != nil || counts[store.ReflectPromoted] != 1 || counts[store.ReflectRejected] != 1 {
		t.Fatalf("counts = %v %v", counts, err)
	}
}

func TestProactiveAndJoinRecords(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.AddProactiveReply(ctx, store.ProactiveReply{Channel: "qqbot", ChatID: "G1", UserID: "U1", Question: "q", Answer: "a"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListProactiveReplies(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	if err := st.SetProactiveQuality(ctx, list[0].ID, "ok"); err != nil {
		t.Fatal(err)
	}
	if counts, _ := st.ProactiveCounts(ctx); counts["qqbot:G1"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	if err := st.AddJoinRequest(ctx, store.JoinRequestRecord{Channel: "qqbot", ChatID: "G1", UserID: "U9", Decision: store.JoinApproved}); err != nil {
		t.Fatal(err)
	}
	jr, total, err := st.ListJoinRequests(ctx, 1, 20)
	if err != nil || total != 1 || len(jr) != 1 || jr[0].Decision != store.JoinApproved {
		t.Fatalf("join = %+v %d %v", jr, total, err)
	}
	conv, err := st.UpsertConversation(ctx, store.ConvKey{Channel: "qqbot", ChatType: "group", ChatID: "G1", UserID: "U1", UserName: "小明"})
	if err != nil {
		t.Fatal(err)
	}
	// 单聊会话没有昵称：借用同一用户在群里的昵称
	priv, err := st.UpsertConversation(ctx, store.ConvKey{Channel: "qqbot", ChatType: "private", ChatID: "U1", UserID: "U1"})
	if err != nil || priv.UserName != "小明" {
		t.Fatalf("private conv name = %q %v", priv.UserName, err)
	}
	if got, err := st.ConversationByKey(ctx, "qqbot", "G1", "U1"); err != nil || got.ID != conv.ID {
		t.Fatalf("by key = %+v %v", got, err)
	}
	if replied, err := st.RepliedSince(ctx, conv.ID, time.Now().Add(-time.Hour)); err != nil || replied {
		t.Fatalf("replied = %v %v", replied, err)
	}
	if name := st.KnownUserName(ctx, "qqbot", "U1"); name != "小明" {
		t.Fatalf("known name = %q", name)
	}
}

func TestKBFoldersImportEmbedding(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	chunks := func(s string) []store.ChunkInput { return []store.ChunkInput{{Content: s}} }

	d := store.KBDocument{Path: "计费/退款", Title: "退款规则", Content: "七天内可退", Enabled: false}
	if err := st.SaveKBDocument(ctx, &d, chunks("七天内可退")); err != nil {
		t.Fatal(err)
	}
	res, err := st.ImportKBDocuments(ctx, []store.ImportDoc{
		{Path: "计费/退款", Title: "退款规则", Content: "十四天内可退", Chunks: chunks("十四天内可退")},
		{Path: "计费", Title: "充值", Content: "支付宝", Chunks: chunks("支付宝")},
	}, false)
	if err != nil || res != (store.ImportResult{Created: 1, Skipped: 1}) {
		t.Fatalf("skip 模式：%+v %v", res, err)
	}
	res, err = st.ImportKBDocuments(ctx, []store.ImportDoc{
		{Path: "计费/退款", Title: "退款规则", Content: "十四天内可退", Chunks: chunks("十四天内可退")},
	}, true)
	if err != nil || res.Updated != 1 {
		t.Fatalf("覆盖模式：%+v %v", res, err)
	}
	got, _ := st.KBDocument(ctx, d.ID)
	if got.Content != "十四天内可退" || got.Enabled {
		t.Fatalf("覆盖应改正文、保留停用状态：%+v", got)
	}

	if n, err := st.RenameKBFolder(ctx, "计费", "财务"); err != nil || n != 2 {
		t.Fatalf("改名影响 %d 篇 %v", n, err)
	}
	if n, _ := st.RenameKBFolder(ctx, "财务/退款", ""); n != 1 {
		t.Fatalf("移到根目录影响 %d 篇", n)
	}
	list, _ := st.ListKBDocuments(ctx)
	paths := map[string]string{}
	for _, d := range list {
		paths[d.Title] = d.Path
	}
	if paths["退款规则"] != "" || paths["充值"] != "财务" {
		t.Fatalf("目录改名结果不对：%v", paths)
	}

	// 向量化：停用文档不参与；换模型后旧向量作废
	if err := st.SetReflectionStatus(ctx, mustReflection(t, st), store.ReflectApproved); err != nil {
		t.Fatal(err)
	}
	items, err := st.PendingEmbeddings(ctx, "m1", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("待向量化应为 1 块 + 1 条沉淀：%+v %v", items, err)
	}
	vecs := make([][]float32, len(items))
	for i := range items {
		vecs[i] = []float32{1, 0}
	}
	if err := st.SaveEmbeddings(ctx, "m1", items, vecs); err != nil {
		t.Fatal(err)
	}
	if items, _ := st.PendingEmbeddings(ctx, "m1", 10); len(items) != 0 {
		t.Fatalf("回填后不应再有待向量化：%+v", items)
	}
	if items, _ := st.PendingEmbeddings(ctx, "m2", 10); len(items) != 2 {
		t.Fatalf("换模型后应全部重算：%+v", items)
	}
	all, _ := st.EnabledKBChunks(ctx, "m1")
	for _, c := range all {
		if len(c.Vec) != 2 {
			t.Fatalf("应带回 m1 的向量：%+v", c)
		}
	}
	all, _ = st.EnabledKBChunks(ctx, "m2")
	for _, c := range all {
		if c.Vec != nil {
			t.Fatalf("模型不一致不应带回向量：%+v", c)
		}
	}
}

func mustReflection(t *testing.T, st *store.Store) int64 {
	t.Helper()
	r := store.Reflection{Channel: "qqbot", ChatID: "G1", Question: "q", Answer: "a", FAQ: "问：q\n答：a"}
	if err := st.AddReflection(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func TestSkills(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	k := store.Skill{Name: "refund", Description: "退款", Content: "---\nname: refund\n---\n正文", Enabled: true}
	if err := st.SaveSkill(ctx, &k, []store.SkillFile{{Path: "a.md", Content: "附件"}}); err != nil {
		t.Fatal(err)
	}
	dup := store.Skill{Name: "refund", Description: "x", Content: "x", Enabled: true}
	if err := st.SaveSkill(ctx, &dup, nil); !errors.Is(err, store.ErrSkillExists) {
		t.Fatalf("重名应返回 ErrSkillExists，得到 %v", err)
	}

	// 编辑（files=nil）不动附属文件
	k.Description = "退款规则"
	if err := st.SaveSkill(ctx, &k, nil); err != nil {
		t.Fatal(err)
	}
	got, err := st.Skill(ctx, k.ID)
	if err != nil || got.Description != "退款规则" || len(got.Files) != 1 || got.FileCount != 1 || got.Size != len(k.Content)+len("附件") {
		t.Fatalf("%+v %v", got, err)
	}

	_ = st.TouchSkill(ctx, k.ID)
	if err := st.SetSkillEnabled(ctx, k.ID, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.EnabledSkills(ctx); len(list) != 0 {
		t.Fatalf("停用后不应出现在启用列表：%+v", list)
	}

	// 覆盖导入：替换正文与附属文件，保留停用状态与使用统计
	res, err := st.ImportSkills(ctx, []store.ImportSkill{
		{Name: "refund", Description: "新", Content: "新正文", Files: []store.SkillFile{{Path: "b.md", Content: "b"}, {Path: "c.md", Content: "c"}}},
		{Name: "subscription", Description: "价格", Content: "价格正文"},
	}, true)
	if err != nil || res.Created != 1 || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	list, _ := st.ListSkills(ctx)
	if len(list) != 2 || list[0].Name != "refund" || list[0].Enabled || list[0].UseCount != 1 || list[0].FileCount != 2 || list[0].Description != "新" {
		t.Fatalf("%+v", list)
	}
	if res, _ := st.ImportSkills(ctx, []store.ImportSkill{{Name: "subscription", Description: "x", Content: "x"}}, false); res.Skipped != 1 {
		t.Fatalf("skip 模式应跳过同名：%+v", res)
	}
	enabled, _ := st.EnabledSkills(ctx)
	if len(enabled) != 1 || enabled[0].Name != "subscription" || enabled[0].Content != "价格正文" {
		t.Fatalf("%+v", enabled)
	}

	if err := st.DeleteSkill(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM skill_files`).Scan(&n)
	if n != 0 {
		t.Fatalf("删除技能应级联删除附属文件，剩 %d", n)
	}
}

func TestUsage(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for _, c := range []store.LLMCall{
		{Model: "m1", InputTokens: 1000, OutputTokens: 100, CacheReadTokens: 600, CacheWriteTokens: 50, ToolNames: []string{"kb_search", "kb_search", "read_skill"}, Images: 1},
		{Model: "m1", InputTokens: 500, OutputTokens: 50, Purpose: store.PurposeJudge},
		{Model: "m2", InputTokens: 10, Error: "LLM HTTP 500", Purpose: store.PurposeTopic},
	} {
		if err := st.RecordLLMCall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.UsageSince(ctx, time.Now().Add(-time.Hour), "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if tt := u.Totals; tt.Calls != 3 || tt.Errors != 1 || tt.InputTokens != 1510 || tt.CacheReadTokens != 600 || tt.CacheWriteTokens != 50 || tt.Images != 1 {
		t.Fatalf("totals = %+v", tt)
	}
	if len(u.Days) != 1 || u.Days[0].Day != time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02") {
		t.Fatalf("按东八区切日：%+v", u.Days)
	}
	if len(u.ByPurpose) != 3 || u.ByPurpose[0].Key != store.PurposeReply || len(u.ByModel) != 2 {
		t.Fatalf("分组：%+v %+v", u.ByPurpose, u.ByModel)
	}
	if len(u.Tools) != 2 || u.Tools[0] != (store.ToolUsage{Tool: "kb_search", Calls: 2, Runs: 1}) {
		t.Fatalf("工具：%+v", u.Tools)
	}
	if len(u.Errors) != 1 || u.Errors[0].Purpose != store.PurposeTopic {
		t.Fatalf("错误：%+v", u.Errors)
	}
	o, err := st.Overview(ctx, time.Now().Add(-time.Hour))
	if err != nil || o.TodayLLMCalls != 3 || o.TodayCacheReadTokens != 600 {
		t.Fatalf("overview = %+v %v", o, err)
	}
}

func TestTopicAdmin(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	src := func(user, text string, at time.Time) store.TopicSource {
		return store.TopicSource{Channel: "qqbot", ChatType: "group", ChatID: "G1", UserID: user, Text: text, At: at}
	}
	ids, err := st.SaveTopicBatch(ctx, []store.TopicAssign{
		{Source: src("U1", "怎么退款", now.Add(-time.Hour)), NewTitle: "退款流程"},
		{Source: src("U2", "怎么退款", now.Add(-50*time.Minute)), NewTitle: "退款流程"},
		{Source: src("U3", "退款多久到账", now.Add(-40*time.Minute)), NewTitle: "退款到账"},
		{Source: src("U4", "去年的问题", now.AddDate(0, 0, -100)), NewTitle: "旧主题"},
	}, "topic:qqbot:G1", now)
	if err != nil || len(ids) != 3 {
		t.Fatalf("save = %v %v", ids, err)
	}
	if at, ok, _ := st.Cursor(ctx, "topic:qqbot:G1"); !ok || !at.Equal(now.Truncate(time.Microsecond)) {
		t.Fatal("游标应与归类同事务写入")
	}
	r, _ := st.TopicRanking(ctx, now.Add(-24*time.Hour), 10)
	if len(r) != 2 || r[0].Title != "退款流程" || r[0].Count != 2 || len(r[0].Samples) != 1 {
		t.Fatalf("同句只算一条样例、次数照计：%+v", r)
	}
	if err := st.RenameTopic(ctx, ids["退款到账"], "退款流程"); !errors.Is(err, store.ErrTopicExists) {
		t.Fatalf("改名撞名应报 ErrTopicExists：%v", err)
	}
	if err := st.MergeTopic(ctx, ids["退款到账"], ids["退款流程"]); err != nil {
		t.Fatal(err)
	}
	r, _ = st.TopicRanking(ctx, now.Add(-24*time.Hour), 10)
	if len(r) != 1 || r[0].Count != 3 || r[0].Users != 3 {
		t.Fatalf("合并后：%+v", r)
	}
	occ, total, _ := st.TopicOccurrences(ctx, ids["退款流程"], now.Add(-24*time.Hour), 1, 20)
	if total != 3 || occ[0].Text != "退款多久到账" {
		t.Fatalf("原话最近的在前：%+v", occ)
	}
	// 保留 90 天：去年的记录与随之变空的旧主题被清理
	_, _ = st.Pool().Exec(ctx, `UPDATE question_topics SET updated_at = $1 WHERE title = '旧主题'`, now.AddDate(0, 0, -100))
	if err := st.PruneTopics(ctx, now.AddDate(0, 0, -90)); err != nil {
		t.Fatal(err)
	}
	all, _ := st.QuestionTopics(ctx, 10)
	if len(all) != 1 || all[0].Title != "退款流程" {
		t.Fatalf("prune 后：%+v", all)
	}
}
