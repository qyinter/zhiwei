package qqbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"zhiwei/internal/channel"
	"zhiwei/internal/media"
	"zhiwei/internal/settings"
)

// 订阅位（官方 intents 定义）
const (
	intentDirectMessage       = 1 << 12
	intentGroupMember         = 1 << 24 // GROUP_MEMBER_ADD / GROUP_JOIN_REQUEST 等群成员事件
	intentGroupAndC2C         = 1 << 25
	intentPublicGuildMessages = 1 << 30
)

// 网关 opcode
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
	opHTTPAck        = 12
	opValidation     = 13
)

type payload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  int64           `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	ID string          `json:"id,omitempty"`
}

// Options 由其他设置分区决定的适配器行为。
type Options struct {
	// MemberEvents 订阅群成员事件（入群欢迎 / 入群审核开启时需要）。
	// 未在开放平台开通该事件权限时网关会拒绝连接，所以只在用到时订阅。
	MemberEvents bool
}

type Adapter struct {
	cfg    settings.QQBot
	opts   Options
	api    *apiClient
	status *channel.StatusBox
	log    *slog.Logger
	// webhook 签名私钥（由 AppSecret 派生）
	priv ed25519.PrivateKey

	// selfID 机器人自身 ID（READY 下发），全量群消息里判断是否 @ 了自己
	selfID atomic.Value
	// refs msg_id → msg_idx，回复时据此带 message_reference 显示为引用
	refs      refCache
	noIdxOnce sync.Once
	// names group_openid → 群名缓存（见 groupinfo.go）
	names groupNames

	handler channel.Handler
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(cfg settings.QQBot, opts Options, log *slog.Logger) *Adapter {
	return &Adapter{
		cfg:    cfg,
		opts:   opts,
		api:    newAPIClient(cfg.AppID, cfg.AppSecret, cfg.Sandbox),
		status: channel.NewStatusBox(channel.QQBot, "接入方式 "+cfg.Mode),
		log:    log.With("channel", channel.QQBot),
		priv:   deriveKey(cfg.AppSecret),
	}
}

// deriveKey 官方算法：AppSecret 反复拼接到 ≥32 字节，取前 32 字节作 Ed25519 seed。
func deriveKey(secret string) ed25519.PrivateKey {
	if secret == "" {
		return nil
	}
	seed := secret
	for len(seed) < ed25519.SeedSize {
		seed += seed
	}
	return ed25519.NewKeyFromSeed([]byte(seed[:ed25519.SeedSize]))
}

func (a *Adapter) Name() string           { return channel.QQBot }
func (a *Adapter) Status() channel.Status { return a.status.Get() }

func (a *Adapter) Start(ctx context.Context, h channel.Handler) error {
	a.handler = h
	// 先换一次 token：AppID / AppSecret 填错在这里就能报出来
	if _, err := a.api.accessToken(ctx); err != nil {
		a.status.Failed(err)
		if a.cfg.Mode == "webhook" {
			return err
		}
	}
	if a.cfg.Mode == "webhook" {
		a.status.Connected("webhook 模式 · 回调地址 /webhook/qqbot")
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runGateway(runCtx)
	}()
	return nil
}

func (a *Adapter) Stop() {
	if a.cancel != nil {
		a.cancel()
		a.wg.Wait()
	}
}

func (a *Adapter) intents() int {
	n := 0
	if a.cfg.Group || a.cfg.C2C {
		n |= intentGroupAndC2C
	}
	if a.cfg.Guild {
		n |= intentPublicGuildMessages
	}
	if a.cfg.GuildDM {
		n |= intentDirectMessage
	}
	if a.cfg.Group && a.opts.MemberEvents {
		n |= intentGroupMember
	}
	return n
}

// ── websocket 网关 ──

// runGateway 连接—会话—断线的外层循环：退避 1s 起倍增、上限 60s；
// 有 session_id 时优先 Resume（断线期间的事件平台会补发）。
func (a *Adapter) runGateway(ctx context.Context) {
	backoff := time.Second
	var sess gwSession
	for ctx.Err() == nil {
		err := a.session(ctx, &sess)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.status.Failed(err)
			a.log.Warn("QQ 网关断开", "err", err, "retryIn", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 60*time.Second)
		if sess.ready {
			backoff = time.Second
			sess.ready = false
		}
	}
}

type gwSession struct {
	id    string
	seq   int64
	ready bool
}

func (a *Adapter) session(ctx context.Context, sess *gwSession) error {
	url, err := a.api.gatewayURL(ctx)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("连接网关: %w", err)
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()

	hello, err := readPayload(ctx, conn)
	if err != nil {
		return err
	}
	if hello.Op != opHello {
		return fmt.Errorf("预期 Hello，收到 op=%d", hello.Op)
	}
	var hd struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	_ = json.Unmarshal(hello.D, &hd)
	if hd.HeartbeatInterval <= 0 {
		hd.HeartbeatInterval = 40000
	}

	tok, err := a.api.accessToken(ctx)
	if err != nil {
		return err
	}
	if sess.id != "" {
		err = writeJSON(ctx, conn, map[string]any{"op": opResume, "d": map[string]any{
			"token": "QQBot " + tok, "session_id": sess.id, "seq": sess.seq,
		}})
	} else {
		err = writeJSON(ctx, conn, map[string]any{"op": opIdentify, "d": map[string]any{
			"token": "QQBot " + tok, "intents": a.intents(), "shard": []int{0, 1},
			"properties": map[string]string{"$os": "linux", "$browser": "zhiwei", "$device": "zhiwei"},
		}})
	}
	if err != nil {
		return err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var seqMu sync.Mutex
	// 心跳：d 为最近收到的序号；access_token 过期不影响已建立的长连接
	go func() {
		t := time.NewTicker(time.Duration(hd.HeartbeatInterval) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				seqMu.Lock()
				seq := sess.seq
				seqMu.Unlock()
				var d any
				if seq > 0 {
					d = seq
				}
				if err := writeJSON(sessCtx, conn, map[string]any{"op": opHeartbeat, "d": d}); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		// 读超时 = 3 个心跳周期：平台侧假死时 Read 不会自己返回
		readCtx, readCancel := context.WithTimeout(sessCtx, 3*time.Duration(hd.HeartbeatInterval)*time.Millisecond)
		p, err := readPayload(readCtx, conn)
		readCancel()
		if err != nil {
			return err
		}
		if p.S > 0 {
			seqMu.Lock()
			sess.seq = p.S
			seqMu.Unlock()
		}
		switch p.Op {
		case opDispatch:
			switch p.T {
			case "READY":
				var r struct {
					SessionID string `json:"session_id"`
					User      struct {
						ID       string `json:"id"`
						Username string `json:"username"`
					} `json:"user"`
				}
				_ = json.Unmarshal(p.D, &r)
				sess.id, sess.ready = r.SessionID, true
				a.selfID.Store(r.User.ID)
				a.status.Connected("websocket 已连接 · " + r.User.Username)
				a.log.Info("QQ 网关就绪", "bot", r.User.Username)
			case "RESUMED":
				sess.ready = true
				a.status.Connected("")
			default:
				a.dispatch(p.T, p.D)
			}
		case opReconnect:
			return errors.New("平台要求重连")
		case opInvalidSession:
			// 会话失效：丢弃 session，下次重新 Identify
			sess.id, sess.seq = "", 0
			return errors.New("会话失效（检查 intents 权限是否已在开放平台开通）")
		case opHeartbeatAck:
		}
	}
}

func readPayload(ctx context.Context, conn *websocket.Conn) (payload, error) {
	var p payload
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(raw, &p)
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, _ := json.Marshal(v)
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, raw)
}

// ── webhook ──

// ServeWebhook 处理开放平台回调：op=13 回调地址验证；op=0 事件（先验签）。
func (a *Adapter) ServeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil || a.priv == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if p.Op == opValidation {
		var v struct {
			PlainToken string `json:"plain_token"`
			EventTS    string `json:"event_ts"`
		}
		_ = json.Unmarshal(p.D, &v)
		sig := ed25519.Sign(a.priv, []byte(v.EventTS+v.PlainToken))
		_ = json.NewEncoder(w).Encode(map[string]string{"plain_token": v.PlainToken, "signature": hex.EncodeToString(sig)})
		return
	}

	sig, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	ts := r.Header.Get("X-Signature-Timestamp")
	if err != nil || len(sig) != ed25519.SignatureSize || ts == "" ||
		!ed25519.Verify(a.priv.Public().(ed25519.PublicKey), append([]byte(ts), body...), sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if p.Op == opDispatch {
		a.dispatch(p.T, p.D)
	}
	_ = json.NewEncoder(w).Encode(map[string]int{"op": opHTTPAck})
}

// ── 事件解析 ──

type msgEvent struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	GroupOpenID string `json:"group_openid"`
	ChannelID   string `json:"channel_id"`
	GuildID     string `json:"guild_id"`
	Author      struct {
		ID           string `json:"id"`
		UserOpenID   string `json:"user_openid"`
		MemberOpenID string `json:"member_openid"`
		Username     string `json:"username"`
		Bot          bool   `json:"bot"`
		MemberRole   string `json:"member_role"`
	} `json:"author"`
	Mentions []struct {
		ID           string `json:"id"`
		MemberOpenID string `json:"member_openid"`
		IsYou        bool   `json:"is_you"`
		Bot          bool   `json:"bot"`
	} `json:"mentions"`
	MessageReference struct {
		MessageID string `json:"message_id"`
	} `json:"message_reference"`
	Attachments []struct {
		ContentType string `json:"content_type"`
		Filename    string `json:"filename"`
		Size        int64  `json:"size"`
		URL         string `json:"url"`
	} `json:"attachments"`
	// MessageScene.Ext 形如 ["msg_idx=REFIDX_xxx", "auth_token=…"]；msg_idx 是引用回复要填的消息索引
	MessageScene struct {
		Ext []string `json:"ext"`
	} `json:"message_scene"`
}

// msgIdx 取事件里的 msg_idx（群聊 / 单聊引用回复用，与 msg_id 不是一回事）。
func (ev *msgEvent) msgIdx() string {
	for _, kv := range ev.MessageScene.Ext {
		if v, ok := strings.CutPrefix(kv, "msg_idx="); ok {
			return v
		}
	}
	return ""
}

// 频道消息里的 @ 形如 <@!123456>；全量群消息里 @成员 形如 <@member_openid>、@全体 为 <@all>。
// GROUP_AT_MESSAGE_CREATE 平台已剥掉 @机器人 只留前导空格。
var mentionTag = regexp.MustCompile(`<@!?[0-9A-Za-z_-]+>`)

// mentionsMe 全量群消息是否 @ 了本机器人：优先看 is_you，其次比对 READY 下发的机器人 ID。
func (a *Adapter) mentionsMe(ev *msgEvent) bool {
	self, _ := a.selfID.Load().(string)
	for _, m := range ev.Mentions {
		if m.IsYou || (self != "" && m.Bot && (m.ID == self || m.MemberOpenID == self)) {
			return true
		}
	}
	return false
}

func (a *Adapter) dispatch(t string, d json.RawMessage) {
	if a.handler == nil {
		return
	}
	switch t {
	case "GROUP_MEMBER_ADD", "GROUP_JOIN_REQUEST":
		if a.cfg.Group {
			a.dispatchMember(t, d)
		}
		return
	}
	var ev msgEvent
	if err := json.Unmarshal(d, &ev); err != nil {
		return
	}
	in := channel.Inbound{
		Channel:   channel.QQBot,
		MessageID: ev.ID,
		Text:      strings.TrimSpace(mentionTag.ReplaceAllString(ev.Content, "")),
		Mentioned: true, // @ 事件与单聊天然面向机器人；全量群消息在下面单独判断
		Images:    ev.images(),
	}
	switch t {
	case "GROUP_AT_MESSAGE_CREATE":
		if !a.cfg.Group {
			return
		}
		in.ChatType, in.ChatID, in.ChatTitle = channel.ChatGroup, ev.GroupOpenID, a.groupTitle(ev.GroupOpenID)
	case "GROUP_MESSAGE_CREATE":
		// 全量模式下同一条 @ 消息可能再以 GROUP_AT_MESSAGE_CREATE 推一次，由编排器按 msg_id 去重
		if !a.cfg.Group || !a.cfg.GroupAll || ev.Author.Bot {
			return
		}
		in.ChatType, in.ChatID, in.ChatTitle = channel.ChatGroup, ev.GroupOpenID, a.groupTitle(ev.GroupOpenID)
		in.Mentioned = a.mentionsMe(&ev)
	case "C2C_MESSAGE_CREATE":
		if !a.cfg.C2C {
			return
		}
		in.ChatType, in.ChatID = channel.ChatPrivate, firstNonEmpty(ev.Author.UserOpenID, ev.Author.ID)
	case "AT_MESSAGE_CREATE":
		in.ChatType, in.ChatID = channel.ChatGuild, ev.ChannelID
	case "DIRECT_MESSAGE_CREATE":
		in.ChatType, in.ChatID = channel.ChatGuildDM, ev.GuildID
	default:
		return
	}
	// 用户 ID 统一取 author.id：实测同一机器人下，同一个人群聊的 member_openid 与单聊的 user_openid
	// 都等于 author.id（2026-10-24），据此可把一个人的群聊 / 单聊会话关联起来
	in.SenderID = firstNonEmpty(ev.Author.ID, ev.Author.MemberOpenID, ev.Author.UserOpenID)
	in.SenderName = ev.Author.Username
	in.SenderRole = ev.Author.MemberRole
	if idx := ev.msgIdx(); idx != "" {
		a.refs.put(ev.ID, idx)
	} else if in.ChatType == channel.ChatGroup || in.ChatType == channel.ChatPrivate {
		a.noIdxOnce.Do(func() {
			a.log.Info("消息事件未带 msg_idx，群聊 / 单聊回复无法显示为引用", "event", t)
		})
	}
	a.handler(in)
}

// images 消息里的图片附件（语音、视频、文件不处理）。附件地址有时不带协议头。
func (ev *msgEvent) images() []channel.Image {
	var out []channel.Image
	for _, a := range ev.Attachments {
		isImage := strings.HasPrefix(a.ContentType, "image/") || (a.ContentType == "" && hasImageExt(a.Filename))
		if !isImage || a.URL == "" || len(out) >= channel.MaxImages {
			continue
		}
		url := a.URL
		if strings.HasPrefix(url, "//") {
			url = "https:" + url
		} else if !strings.Contains(url, "://") {
			url = "https://" + url
		}
		out = append(out, channel.Image{ContentType: a.ContentType, Size: a.Size, Open: func(ctx context.Context) (io.ReadCloser, error) {
			return media.Get(ctx, url)
		}})
	}
	return out
}

func hasImageExt(name string) bool {
	name = strings.ToLower(name)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// memberEvent GROUP_MEMBER_ADD / GROUP_JOIN_REQUEST 的事件体（字段平铺在 d 上）。
type memberEvent struct {
	GroupOpenID   string `json:"group_openid"`
	MemberOpenID  string `json:"member_openid"`
	Username      string `json:"username"`
	Bot           bool   `json:"bot"`
	JoinRequestID string `json:"join_request_id"`
	VerifyInfo    struct {
		Method        string `json:"method"`
		VerifyMessage string `json:"verify_message"`
		ReviewQAList  []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
		} `json:"review_qa_list"`
	} `json:"verify_info"`
	AutoApproved *struct {
		StrategyID string `json:"strategy_id"`
	} `json:"auto_approved"`
}

func (a *Adapter) dispatchMember(t string, d json.RawMessage) {
	var ev memberEvent
	if err := json.Unmarshal(d, &ev); err != nil || ev.GroupOpenID == "" || ev.MemberOpenID == "" || ev.Bot {
		return
	}
	in := channel.Inbound{
		Channel: channel.QQBot, ChatType: channel.ChatGroup, ChatID: ev.GroupOpenID, ChatTitle: a.groupTitle(ev.GroupOpenID),
		SenderID: ev.MemberOpenID, SenderName: ev.Username,
	}
	if t == "GROUP_MEMBER_ADD" {
		in.Kind = channel.KindMemberJoin
		a.handler(in)
		return
	}
	// 入群申请：验证消息与各问题答案按行拼接，交给审核逻辑提取 ID
	answers := []string{}
	if v := strings.TrimSpace(ev.VerifyInfo.VerifyMessage); v != "" {
		answers = append(answers, v)
	}
	for _, qa := range ev.VerifyInfo.ReviewQAList {
		if v := strings.TrimSpace(qa.Answer); v != "" {
			answers = append(answers, v)
		}
	}
	in.Kind = channel.KindJoinRequest
	in.JoinRequest = &channel.JoinRequest{
		RequestID: ev.JoinRequestID, Answer: strings.Join(answers, "\n"), AutoApproved: ev.AutoApproved != nil,
	}
	a.handler(in)
}

// ReviewJoin 审批入群申请（机器人需是群管理员，接口 60 QPM）。
func (a *Adapter) ReviewJoin(ctx context.Context, chatID, userID string, req channel.JoinRequest, approve bool, reason string) error {
	body := map[string]any{"op": "decline", "join_request_id": req.RequestID}
	if approve {
		body["op"] = "approve"
	} else if reason != "" {
		body["reject_reason"] = reason
	}
	return a.api.call(ctx, http.MethodPost, "/v2/groups/"+chatID+"/approval_join_request/"+userID, body, nil)
}

// ── 发送 ──

// 被动回复不可用的错误码：超时或次数超限 / msg_id 已过期。遇到即改发主动消息。
const (
	codePassiveLimit   = 40034128
	codePassiveExpired = 40034005
)

func (a *Adapter) Send(ctx context.Context, out channel.Outbound) error {
	// 自定义 Markdown：群聊 / 单聊自 2026-04-23 起对所有机器人开放（频道仍需内邀，按纯文本发）
	mdOK := out.ChatType == channel.ChatGroup || out.ChatType == channel.ChatPrivate
	text, md := out.Text, false
	switch out.Format {
	case channel.FormatMarkdown:
		if mdOK {
			text, md = channel.RenderQQ(text), true
		} else {
			text = channel.RenderPlain(text)
		}
	case channel.FormatQQMarkdown:
		md = mdOK
	}
	if out.ChatType == channel.ChatGroup && a.cfg.AtUser && out.AtUser != "" {
		text = `<qqbot-at-user id="` + out.AtUser + `" /> ` + text
	}
	body := map[string]any{"content": text, "msg_type": 0}
	if md {
		body = map[string]any{"msg_type": 2, "markdown": map[string]string{"content": text}}
	}
	if out.ReplyTo != "" {
		body["msg_id"] = out.ReplyTo
	}
	// 引用回复：群聊 / 单聊要用入站事件里的 msg_idx（REFIDX_…），频道直接用消息 ID
	if out.Quote && out.ReplyTo != "" {
		ref := out.ReplyTo
		if mdOK {
			ref = a.refs.get(out.ReplyTo)
		}
		if ref != "" {
			body["message_reference"] = map[string]string{"message_id": ref}
		}
	}
	var path string
	switch out.ChatType {
	case channel.ChatGroup:
		path = "/v2/groups/" + out.ChatID + "/messages"
		body["msg_seq"] = max(out.Seq, 1)
	case channel.ChatPrivate:
		path = "/v2/users/" + out.ChatID + "/messages"
		body["msg_seq"] = max(out.Seq, 1)
	case channel.ChatGuild:
		path = "/channels/" + out.ChatID + "/messages"
		delete(body, "msg_type")
	case channel.ChatGuildDM:
		path = "/dms/" + out.ChatID + "/messages"
		delete(body, "msg_type")
	default:
		return fmt.Errorf("QQ 不支持的聊天类型 %s", out.ChatType)
	}
	err := a.api.call(ctx, http.MethodPost, path, body, nil)
	var ae *apiError
	if out.ReplyTo != "" && errors.As(err, &ae) && (ae.Code == codePassiveLimit || ae.Code == codePassiveExpired) {
		a.log.Info("被动回复不可用，改发主动消息", "chat", out.ChatID, "code", ae.Code)
		delete(body, "msg_id")
		delete(body, "msg_seq")
		err = a.api.call(ctx, http.MethodPost, path, body, nil)
	}
	// 引用的消息取不到（被撤回、索引失效等）：去掉引用再发，引用只是锦上添花
	if _, quoted := body["message_reference"]; quoted && errors.As(err, &ae) && ae.Status/100 == 4 &&
		ae.Code != codePassiveLimit && ae.Code != codePassiveExpired {
		a.log.Info("引用回复被拒，改为不引用", "chat", out.ChatID, "code", ae.Code, "msg", ae.Message)
		delete(body, "message_reference")
		err = a.api.call(ctx, http.MethodPost, path, body, nil)
	}
	// 平台拒收 Markdown（内容不合规、能力被收回等）：同一条改成纯文本再发一次，宁可难看也别丢消息
	if md && out.Format == channel.FormatMarkdown && errors.As(err, &ae) && ae.Status/100 == 4 &&
		ae.Code != codePassiveLimit && ae.Code != codePassiveExpired {
		a.log.Warn("Markdown 消息被拒，改发纯文本", "chat", out.ChatID, "code", ae.Code, "msg", ae.Message)
		body["msg_type"], body["content"] = 0, channel.RenderPlain(out.Text)
		delete(body, "markdown")
		err = a.api.call(ctx, http.MethodPost, path, body, nil)
	}
	return err
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// refCache msg_id → msg_idx 的短期缓存。被动回复窗口最长 60 分钟，留 2 小时足够；超量时顺手清理过期项。
type refCache struct {
	mu sync.Mutex
	m  map[string]refItem
}

type refItem struct {
	idx string
	at  time.Time
}

const refTTL = 2 * time.Hour

func (c *refCache) put(msgID, idx string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]refItem{}
	}
	if len(c.m) >= 5000 {
		for k, v := range c.m {
			if time.Since(v.at) > refTTL {
				delete(c.m, k)
			}
		}
	}
	c.m[msgID] = refItem{idx, time.Now()}
}

func (c *refCache) get(msgID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if it, ok := c.m[msgID]; ok && time.Since(it.at) <= refTTL {
		return it.idx
	}
	return ""
}
