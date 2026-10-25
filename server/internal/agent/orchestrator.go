package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"zhiwei/internal/catalog"
	"zhiwei/internal/channel"
	"zhiwei/internal/kb"
	"zhiwei/internal/llm"
	"zhiwei/internal/media"
	"zhiwei/internal/settings"
	"zhiwei/internal/skill"
	"zhiwei/internal/store"
)

// 整句匹配的关键词命令（不必走 LLM）。整句才算，「我想重置密码」不会触发重置。
var (
	cmdReset   = regexp.MustCompile(`(?i)^\s*(重新开始|重置对话|重置会话|重置|/new|/reset|/clear)\s*$`)
	cmdHelp    = regexp.MustCompile(`(?i)^\s*(帮助|怎么用|使用说明|/help|help)\s*$`)
	cmdHandoff = regexp.MustCompile(`^\s*(人工|转人工|人工客服|转接人工|找人工)\s*$`)
)

const (
	replyReset        = "对话已重置，我们重新开始吧。"
	replyHandoff      = "已为你转接人工客服，请稍候，客服会尽快回复你。期间自动回复暂停。"
	replyHandoffAgain = "人工客服正在处理中，请耐心等待。"
	replyResumed      = "已恢复自动客服，有问题可以继续问我。"
	replyAck          = "收到，查询中…"
)

// Orchestrator 把渠道消息变成客服回复。每个会话一个串行队列：
// 一轮推理期间到达的新消息合并成下一轮的一批，不会并发抢答，也不会每条各答一遍。
type Orchestrator struct {
	st  *store.Store
	reg *channel.Registry
	log *slog.Logger

	mu      sync.Mutex
	cfg     settings.All
	brain   *Brain // LLM 未配置时为 nil
	index   *kb.Index
	skills  []skill.Skill   // 启用的技能，ReloadSkills 时整体替换
	catalog *catalog.Client // 模型目录缓存跨设置变更复用
	workers map[int64]*worker

	// reflectMu 串行化反思的提炼 / 整理 / 升格（定时与后台手动触发不交错）
	reflectMu sync.Mutex
	// topicMu 串行化问题归类（定时与后台「立即归类」）；topicFails 各来源当前这一批连续失败的次数，受 topicMu 保护
	topicMu    sync.Mutex
	topicFails map[string]topicFail
}

type worker struct {
	pending []queued
	quoted  string
	running bool
}

// queued 一条待处理的入站消息及其图片（下载入口）。
type queued struct {
	msg    store.Message
	images []channel.Image
}

func NewOrchestrator(st *store.Store, reg *channel.Registry, log *slog.Logger) *Orchestrator {
	return &Orchestrator{st: st, reg: reg, log: log.With("mod", "agent"), workers: map[int64]*worker{}, catalog: catalog.NewClient()}
}

// Configure 设置变更后由 runtime 调用：替换 LLM 客户端与配置快照。正在跑的轮次用旧快照跑完。
func (o *Orchestrator) Configure(cfg settings.All, index *kb.Index) error {
	client, err := llm.New(cfg.LLM)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfg = cfg
	o.index = index
	o.brain = nil
	if err != nil {
		return err
	}
	o.brain = o.newBrain(client)
	return nil
}

// newBrain 调用方持有 o.mu。
func (o *Orchestrator) newBrain(client llm.Client) *Brain {
	b := NewBrain(client, o.index, o.cfg, o.skills, o.catalog)
	b.onSkill = func(id int64) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := o.st.TouchSkill(ctx, id); err != nil {
				o.log.Warn("记录技能使用失败", "err", err)
			}
		}()
	}
	return b
}

// ReloadSkills 从库里重新加载启用技能并重建大脑（system prompt 里的技能目录随之更新）。
// 启动时与后台增删改技能后调用；解析失败的技能跳过并记日志，不影响其他技能。
func (o *Orchestrator) ReloadSkills(ctx context.Context) error {
	rows, err := o.st.EnabledSkills(ctx)
	if err != nil {
		return err
	}
	list := make([]skill.Skill, 0, len(rows))
	for _, r := range rows {
		m, err := skill.Parse(r.Content, r.Name)
		if err != nil {
			o.log.Warn("技能解析失败，已跳过", "skill", r.Name, "err", err)
			continue
		}
		k := skill.Skill{ID: r.ID, Name: r.Name, Description: r.Description, Body: m.Body, Files: map[string]string{}}
		for _, f := range r.Files {
			k.Files[f.Path] = f.Content
		}
		list = append(list, k)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.skills = list
	if o.brain != nil {
		o.brain = o.newBrain(o.brain.client)
	}
	return nil
}

func (o *Orchestrator) snapshot() (settings.All, *Brain) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg, o.brain
}

func chatKey(channelName, chatID string) string { return channelName + ":" + chatID }

// Handle 渠道入站入口（channel.Handler）。快速返回，推理在会话 worker 里异步进行。
func (o *Orchestrator) Handle(in channel.Inbound) {
	cfg, _ := o.snapshot()
	group := in.ChatType == channel.ChatGroup || in.ChatType == channel.ChatGuild
	if group && !chatAllowed(cfg.Agent, in.Channel, in.ChatID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch in.Kind {
	case channel.KindMemberJoin:
		o.welcome(ctx, cfg, in)
		return
	case channel.KindJoinRequest:
		// 审核要调外部核对接口（最长 5s）+ 平台审批接口，不阻塞渠道的事件循环
		go o.reviewJoin(cfg, in)
		return
	}

	// 群消息一律进群时间线（主动回复 / 反思沉淀的素材），之后只有 @ 机器人的才进入问答流程
	if group {
		o.bufferGroupMessage(ctx, in)
		if !in.Mentioned {
			return
		}
	}

	conv, err := o.st.UpsertConversation(ctx, store.ConvKey{
		Channel: in.Channel, ChatType: in.ChatType, ChatID: in.ChatID,
		UserID: in.SenderID, UserName: in.SenderName, Title: in.ChatTitle,
	})
	if err != nil {
		o.log.Error("建会话失败", "err", err)
		return
	}
	msg := store.Message{
		ConversationID: conv.ID, Direction: store.DirIn, SenderID: in.SenderID,
		SenderName: in.SenderName, Content: withImageNote(in.Text, len(in.Images)), PlatformMsgID: in.MessageID,
	}
	if err := o.st.AddMessage(ctx, &msg); err != nil {
		if !errors.Is(err, store.ErrDuplicate) {
			o.log.Error("消息落库失败", "err", err)
		}
		return
	}
	conv.LastInboundMsgID = in.MessageID

	// 关键词命令只认纯文字消息；带图的一律交给模型
	switch {
	case len(in.Images) > 0:
	case cmdReset.MatchString(in.Text):
		if err := o.st.ResetContext(ctx, conv.ID); err != nil {
			o.log.Error("重置上下文失败", "err", err)
		}
		if conv.Status == store.ConvHandoff {
			o.reply(ctx, conv, in.MessageID, "对话已重置。人工客服仍在处理中，请耐心等待。")
			return
		}
		o.reply(ctx, conv, in.MessageID, replyReset)
		return
	case cmdHelp.MatchString(in.Text) || strings.TrimSpace(in.Text) == "":
		o.reply(ctx, conv, in.MessageID, helpText(cfg))
		return
	case cmdHandoff.MatchString(in.Text):
		o.openHandoff(ctx, conv, in.MessageID, HandoffRequest{Reason: "客户主动要求人工", Summary: in.Text}, true)
		return
	}
	if conv.Status == store.ConvHandoff {
		// 人工接待中：只落库，不自动回复；图片照样存下来给后台看
		if len(in.Images) > 0 {
			go o.keepImages(msg.ID, in.Images)
		}
		return
	}
	o.enqueue(conv, queued{msg: msg, images: in.Images}, in.Quoted)
}

func helpText(cfg settings.All) string {
	name := cfg.Brand.Name
	if name == "" {
		name = "本产品"
	}
	s := fmt.Sprintf("我是%s的智能客服，直接描述你的问题即可。发送「重置」重新开始对话，发送「人工」转接人工客服。", name)
	if cfg.Brand.SupportURL != "" {
		s += "官网：" + cfg.Brand.SupportURL
	}
	return s
}

func (o *Orchestrator) enqueue(conv store.Conversation, q queued, quoted string) {
	o.mu.Lock()
	w := o.workers[conv.ID]
	if w == nil {
		w = &worker{}
		o.workers[conv.ID] = w
	}
	w.pending = append(w.pending, q)
	if quoted != "" {
		w.quoted = quoted
	}
	if w.running {
		o.mu.Unlock()
		return
	}
	w.running = true
	o.mu.Unlock()
	go o.drain(conv.ID)
}

// drain 循环取走整批待处理消息跑一轮，直到队列空；空了就删掉 worker，map 不会无限增长。
func (o *Orchestrator) drain(convID int64) {
	for {
		o.mu.Lock()
		w := o.workers[convID]
		if len(w.pending) == 0 {
			delete(o.workers, convID)
			o.mu.Unlock()
			return
		}
		batch, quoted := w.pending, w.quoted
		w.pending, w.quoted = nil, ""
		o.mu.Unlock()
		if imagesOnly(batch) {
			time.Sleep(imageWait)
			o.mu.Lock()
			batch = append(batch, w.pending...)
			if w.quoted != "" {
				quoted = w.quoted
			}
			w.pending, w.quoted = nil, ""
			o.mu.Unlock()
		}
		o.turn(convID, batch, quoted)
	}
}

func (o *Orchestrator) turn(convID int64, batch []queued, quoted string) {
	cfg, brain := o.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Agent.RunTimeoutSec)*time.Second)
	defer cancel()

	msgs := make([]store.Message, len(batch))
	texts := make([]string, len(batch))
	for i, q := range batch {
		msgs[i], texts[i] = q.msg, q.msg.Content
	}
	conv, err := o.st.Conversation(ctx, convID)
	// 不进入问答（人工接待中 / LLM 未配置）时图片照样下载保存，留给后台人工查看
	if err != nil || conv.Status == store.ConvHandoff || brain == nil {
		if err == nil && brain == nil && conv.Status != store.ConvHandoff {
			o.log.Warn("LLM 未配置，跳过自动回复", "conv", convID)
		}
		for _, q := range batch {
			if len(q.images) > 0 {
				o.keepImages(q.msg.ID, q.images)
			}
		}
		return
	}
	replyTo := msgs[len(msgs)-1].PlatformMsgID

	history, err := o.history(ctx, conv, msgs, cfg.Agent)
	if err != nil {
		o.log.Error("读取历史失败", "err", err)
		return
	}

	// 群里推理要几秒到几十秒，先回一句让用户知道机器人接到了（被动回复；不落库、不进历史）
	if cfg.Agent.AckEnabled && (conv.ChatType == channel.ChatGroup || conv.ChatType == channel.ChatGuild) {
		out := channel.Outbound{ChatType: conv.ChatType, ChatID: conv.ChatID, Text: replyAck, ReplyTo: replyTo, Seq: seqFor(replyTo, 0)}
		if err := o.reg.Send(ctx, conv.Channel, out); err != nil {
			o.log.Warn("发送收到提示失败", "conv", convID, "err", err)
		}
	}

	// 图片下载放在「收到」提示之后：下载最长十几秒，先让用户知道机器人接到了
	in := RunInput{History: history, Texts: texts, Quoted: quoted}
	imgs, sent := o.batchImages(ctx, batch)
	in.ImageCount = sent
	if cfg.Agent.Vision {
		in.Images = imgs
	}

	start := time.Now()
	res, runErr := brain.Run(ctx, in)
	if res.ImagesRejected {
		o.log.Warn("模型拒收图片，已去掉图片重试，之后不再带图；模型不支持识图时请在设置里关闭「识别图片」", "conv", convID)
	}
	o.recordCall(conv.ID, res, runErr, time.Since(start))
	if runErr != nil {
		o.log.Error("推理失败", "conv", convID, "err", runErr)
		o.reply(context.Background(), conv, replyTo, fallbackText(cfg))
		return
	}
	if res.Handoff != nil {
		o.openHandoff(ctx, conv, replyTo, *res.Handoff, false)
	}
	if res.Reply == "" || strings.Contains(res.Reply, NoAnswer) {
		return
	}
	o.reply(ctx, conv, replyTo, res.Reply)
}

func fallbackText(cfg settings.All) string {
	s := "系统繁忙，请稍后重试；需要人工请发送「人工」。"
	if cfg.Brand.SupportURL != "" {
		s += "也可访问 " + cfg.Brand.SupportURL
	}
	return s
}

// history 取上下文起点后的最近消息（不含本批），遇到超过 IdleResetMin 的空档就截断——
// 隔了半小时的新问题不该背着旧话题的包袱。
func (o *Orchestrator) history(ctx context.Context, conv store.Conversation, batch []store.Message, ag settings.Agent) ([]llm.Message, error) {
	if ag.HistoryLimit == 0 {
		return nil, nil
	}
	rows, err := o.st.RecentMessages(ctx, conv.ID, conv.ContextSince, ag.HistoryLimit+len(batch))
	if err != nil {
		return nil, err
	}
	inBatch := map[int64]bool{}
	for _, m := range batch {
		inBatch[m.ID] = true
	}
	var kept []store.Message
	for _, m := range rows {
		if !inBatch[m.ID] {
			kept = append(kept, m)
		}
	}
	if ag.IdleResetMin > 0 {
		gap := time.Duration(ag.IdleResetMin) * time.Minute
		next := batch[0].CreatedAt
		for i := len(kept) - 1; i >= 0; i-- {
			if next.Sub(kept[i].CreatedAt) > gap {
				kept = kept[i+1:]
				break
			}
			next = kept[i].CreatedAt
		}
	}
	if len(kept) > ag.HistoryLimit {
		kept = kept[len(kept)-ag.HistoryLimit:]
	}
	// 历史里的客户消息同样是不可信数据，剥掉伪造定界符；人工回复以助手身份出现并注明
	out := make([]llm.Message, 0, len(kept))
	for _, m := range kept {
		switch m.Direction {
		case store.DirIn:
			out = append(out, llm.Message{Role: llm.RoleUser, Content: sanitize(m.Content)})
		case store.DirOut:
			out = append(out, llm.Message{Role: llm.RoleAssistant, Content: m.Content})
		case store.DirHuman:
			out = append(out, llm.Message{Role: llm.RoleAssistant, Content: "（人工客服）" + m.Content})
		}
	}
	return mergeRoles(out), nil
}

// mergeRoles 合并相邻同角色消息：连发多条的客户消息、拆条发送的回复在模型眼里是一条。
func mergeRoles(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content += "\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	// 历史以助手消息开头时补一条占位：部分协议要求首条必须是 user
	if len(out) > 0 && out[0].Role == llm.RoleAssistant {
		out = append([]llm.Message{{Role: llm.RoleUser, Content: "（更早的对话已省略）"}}, out...)
	}
	return out
}

func (o *Orchestrator) recordCall(convID int64, res RunResult, runErr error, latency time.Duration) {
	o.recordRun(&convID, res, runErr, latency, store.PurposeReply)
}

// recordRun 记一轮推理的用量；convID 为 nil 表示不属于任何会话（如未作答的主动回复）。
func (o *Orchestrator) recordRun(convID *int64, res RunResult, runErr error, latency time.Duration, purpose string) {
	if res.LLMCalls == 0 {
		return
	}
	c := store.LLMCall{
		ConversationID: convID, Model: res.Model, InputTokens: res.Usage.InputTokens,
		OutputTokens: res.Usage.OutputTokens, CacheReadTokens: res.Usage.CacheReadTokens, CacheWriteTokens: res.Usage.CacheWriteTokens,
		LatencyMs: int(latency.Milliseconds()), ToolCalls: len(res.Steps), Images: res.Images, Purpose: purpose,
	}
	for _, s := range res.Steps {
		c.ToolNames = append(c.ToolNames, s.Tool)
	}
	if runErr != nil {
		c.Error = truncateRunes(runErr.Error(), 1000)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.st.RecordLLMCall(ctx, c); err != nil {
		o.log.Warn("记录用量失败", "err", err)
	}
}

func (o *Orchestrator) openHandoff(ctx context.Context, conv store.Conversation, replyTo string, h HandoffRequest, announce bool) {
	created, err := o.st.OpenHandoff(ctx, conv.ID, h.Reason, h.Summary)
	if err != nil {
		o.log.Error("转人工失败", "err", err)
		return
	}
	o.log.Info("转人工", "conv", conv.ID, "reason", h.Reason, "created", created)
	// 模型发起的转人工由模型自己在回复里告知客户；关键词触发的才由系统发固定话术
	if announce {
		if created {
			o.reply(ctx, conv, replyTo, replyHandoff)
		} else {
			o.reply(ctx, conv, replyTo, replyHandoffAgain)
		}
	}
}

// reply 拆条发送并逐条落库。只有第一条引用原消息；QQ 的 msg_seq 逐条递增防平台去重。
func (o *Orchestrator) reply(ctx context.Context, conv store.Conversation, replyTo, text string) {
	o.send(ctx, conv, replyTo, text, store.DirOut, "")
}

// 机器人回复按精简 Markdown 发（渠道各自渲染），人工回复保持纯文本原样。
func (o *Orchestrator) send(ctx context.Context, conv store.Conversation, replyTo, text, dir, sender string) error {
	cfg, _ := o.snapshot()
	format, parts := channel.FormatText, splitReply(text, cfg.Agent.MaxReplyChars)
	if dir == store.DirOut && cfg.Agent.ReplyMarkdown {
		format, parts = channel.FormatMarkdown, splitMarkdown(text, cfg.Agent.MaxReplyChars)
	}
	for i, p := range parts {
		out := channel.Outbound{ChatType: conv.ChatType, ChatID: conv.ChatID, Text: p, ReplyTo: replyTo, Seq: seqFor(replyTo, i), Format: format}
		if i == 0 {
			out.AtUser, out.Quote = conv.UserID, true
		}
		if conv.Channel != channel.Playground {
			if err := o.reg.Send(ctx, conv.Channel, out); err != nil {
				o.log.Error("发送失败", "channel", conv.Channel, "conv", conv.ID, "err", err)
				return err
			}
		}
		m := store.Message{ConversationID: conv.ID, Direction: dir, SenderName: sender, Content: p}
		if err := o.st.AddMessage(ctx, &m); err != nil {
			o.log.Warn("回复落库失败", "err", err)
		}
	}
	return nil
}

// seqFor 同一条被回复消息下的序号。同一 msg_id 可能先后收到命令回执、模型回复、人工回复，
// 用纳秒时间低位打散，避免不同批次撞上同一 seq 被 QQ 判为重复消息。
func seqFor(replyTo string, i int) int {
	if replyTo == "" {
		return i + 1
	}
	return int(time.Now().UnixNano()/1e6%100000)*10 + i + 1
}

// ── 管理后台动作 ──

// HumanReply 后台人工回复：经原渠道发出（QQ 借最近一条入站消息做被动回复，超窗后适配器自动改发主动消息）。
func (o *Orchestrator) HumanReply(ctx context.Context, convID int64, adminName, text string) error {
	conv, err := o.st.Conversation(ctx, convID)
	if err != nil {
		return err
	}
	return o.send(ctx, conv, conv.LastInboundMsgID, text, store.DirHuman, adminName)
}

// Resume 结束人工接待、恢复自动回复，并告知客户。adminID=nil 表示超时自动恢复。
func (o *Orchestrator) Resume(ctx context.Context, convID int64, adminID *int64) error {
	conv, err := o.st.Conversation(ctx, convID)
	if err != nil {
		return err
	}
	if err := o.st.ResolveHandoff(ctx, convID, adminID); err != nil {
		return err
	}
	if conv.Status == store.ConvHandoff && conv.Channel != channel.Playground {
		o.reply(ctx, conv, conv.LastInboundMsgID, replyResumed)
	}
	return nil
}

// RunHandoffSweeper 每分钟把超时未处理的人工会话自动恢复。
func (o *Orchestrator) RunHandoffSweeper(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg, _ := o.snapshot()
		if cfg.Agent.HandoffTimeoutMin <= 0 {
			continue
		}
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ids, err := o.st.StaleHandoffConversations(sweepCtx, time.Duration(cfg.Agent.HandoffTimeoutMin)*time.Minute)
		if err != nil {
			o.log.Warn("扫描超时人工会话失败", "err", err)
		}
		for _, id := range ids {
			if err := o.Resume(sweepCtx, id, nil); err != nil {
				o.log.Warn("自动恢复失败", "conv", id, "err", err)
			}
		}
		cancel()
	}
}

// PlaygroundResult 后台体验台的一轮结果（含工具轨迹与检索命中，方便调 prompt 与知识库）。
type PlaygroundResult struct {
	Reply     string          `json:"reply"`
	Handoff   *HandoffRequest `json:"handoff"`
	Steps     []Step          `json:"steps"`
	KBHits    []kb.Hit        `json:"kbHits"`
	Usage     llm.Usage       `json:"usage"`
	LatencyMs int64           `json:"latencyMs"`
	Model     string          `json:"model"`
	// Images 交给模型的图片张数；ImagesDropped 上游拒收图片、已去图重试
	Images        int  `json:"images"`
	ImagesDropped bool `json:"imagesDropped"`
}

// Playground 同步跑一轮，不经任何渠道。会话按管理员隔离（channel=playground），历史规则与线上一致；
// 转人工只回报不生效，避免体验台被自己挂起。images 是上传的原图（最多 channel.MaxImages 张），与线上一样缩放后交给模型。
func (o *Orchestrator) Playground(ctx context.Context, adminID int64, text string, images [][]byte, reset bool) (PlaygroundResult, error) {
	cfg, brain := o.snapshot()
	if brain == nil {
		return PlaygroundResult{}, errors.New("LLM 未配置或配置无效，请先在设置页完成配置")
	}
	id := "admin-" + strconv.FormatInt(adminID, 10)
	conv, err := o.st.UpsertConversation(ctx, store.ConvKey{Channel: channel.Playground, ChatType: channel.ChatPrivate, ChatID: id, UserID: id, Title: "体验台"})
	if err != nil {
		return PlaygroundResult{}, err
	}
	if reset {
		if err := o.st.ResetContext(ctx, conv.ID); err != nil {
			return PlaygroundResult{}, err
		}
		conv.ContextSince = time.Now()
	}
	var imgs []media.Image
	for _, raw := range images {
		im, err := media.Normalize(raw)
		if err != nil {
			return PlaygroundResult{}, err
		}
		imgs = append(imgs, im)
	}
	msg := store.Message{ConversationID: conv.ID, Direction: store.DirIn, SenderID: id, Content: withImageNote(text, len(imgs))}
	if err := o.st.AddMessage(ctx, &msg); err != nil {
		return PlaygroundResult{}, err
	}
	o.saveImages(ctx, msg.ID, imgs)
	in := RunInput{Texts: []string{msg.Content}, ImageCount: len(imgs)}
	if cfg.Agent.Vision {
		for _, im := range imgs {
			in.Images = append(in.Images, llm.Image{MIME: im.MIME, Data: im.Data})
		}
	}
	history, err := o.history(ctx, conv, []store.Message{msg}, cfg.Agent)
	if err != nil {
		return PlaygroundResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Agent.RunTimeoutSec)*time.Second)
	defer cancel()
	start := time.Now()
	in.History = history
	res, runErr := brain.Run(runCtx, in)
	latency := time.Since(start)
	o.recordCall(conv.ID, res, runErr, latency)
	if runErr != nil {
		return PlaygroundResult{}, runErr
	}
	reply := res.Reply
	if strings.Contains(reply, NoAnswer) {
		reply = ""
	}
	if reply != "" {
		m := store.Message{ConversationID: conv.ID, Direction: store.DirOut, Content: reply}
		_ = o.st.AddMessage(ctx, &m)
	}
	return PlaygroundResult{
		Reply: reply, Handoff: res.Handoff, Steps: res.Steps, KBHits: res.KBHits,
		Usage: res.Usage, LatencyMs: latency.Milliseconds(), Model: res.Model,
		Images: res.Images, ImagesDropped: res.ImagesDropped,
	}, nil
}

// chatAllowed 群白名单：AllowAllChats 关闭时只响应 Chats 里的群（私聊不经过这里）。
func chatAllowed(ag settings.Agent, ch, chatID string) bool {
	return ag.AllowAllChats || slices.Contains(ag.Chats, chatKey(ch, chatID))
}

// bufferGroupMessage 群消息写入群时间线。整句命令与空消息不进（它们不是问题，也不是客服答复）。
func (o *Orchestrator) bufferGroupMessage(ctx context.Context, in channel.Inbound) {
	text := strings.TrimSpace(in.Text)
	if isCommand(text) && len(in.Images) == 0 {
		return
	}
	if text = withImageNote(text, len(in.Images)); text == "" {
		return
	}
	err := o.st.AddGroupMessage(ctx, store.GroupMessage{
		Channel: in.Channel, ChatType: in.ChatType, ChatID: in.ChatID, UserID: in.SenderID, UserName: in.SenderName,
		Role: in.SenderRole, Content: text, PlatformMsgID: in.MessageID, Mentioned: in.Mentioned,
	})
	if err != nil {
		o.log.Warn("群消息写入时间线失败", "err", err)
	}
}

func isCommand(text string) bool {
	return cmdReset.MatchString(text) || cmdHelp.MatchString(text) || cmdHandoff.MatchString(text)
}
