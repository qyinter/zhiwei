// Package telegram 对接 Telegram Bot API（long polling，无需公网）。
//
// 触发规则：私聊（开关可关）直接响应；群里只响应 @机器人 或回复机器人的消息。
// 注意 BotFather 的 Privacy Mode 默认开启——开着时群里机器人只收得到这几类消息，问答不受影响；
// 主动回复、反思沉淀与群消息治理需要全量群消息：把机器人设为群管理员即可（管理员机器人不受 Privacy Mode 限制），
// 或在 BotFather 关闭 Privacy Mode。
// 入群欢迎用 new_chat_members 服务消息（Privacy Mode 开着也能收到）与 chat_member 更新（需管理员）两路去重。
// 入群审核（chat_join_request）、删消息 / 禁言 / 封禁都要求机器人是群管理员并勾选对应权限。
//
// 单实例假设：同一 token 只能有一个 getUpdates 消费者，第二个会拿到 409。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"zhiwei/internal/channel"
	"zhiwei/internal/media"
	"zhiwei/internal/settings"
)

const pollTimeoutSec = 30

type Adapter struct {
	cfg  settings.Telegram
	base string
	// fileBase 文件下载地址前缀（含 token，只在进程内使用，不落库、不进日志）
	fileBase string
	http     *http.Client
	status   *channel.StatusBox
	log      *slog.Logger

	botID       int64
	botUsername string

	// admins 群管理员缓存（chat_id → user_id → 角色），10 分钟刷新一次；只在 poll 协程里读写
	admins map[int64]adminCache
	// recentJoins 最近处理过的入群（chat:user → 时间）：同一次入群会以 new_chat_members 服务消息
	// 和 chat_member 更新各到一次，只上报一次；只在 poll 协程里读写
	recentJoins map[string]time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(cfg settings.Telegram, log *slog.Logger) *Adapter {
	base := strings.TrimRight(cfg.APIBase, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return &Adapter{
		cfg:      cfg,
		base:     base + "/bot" + cfg.BotToken,
		fileBase: base + "/file/bot" + cfg.BotToken,
		// 长轮询本身挂 30s，硬超时再留 15s 余量；单次调用超时另由 ctx 控制
		http:        &http.Client{Timeout: (pollTimeoutSec + 15) * time.Second},
		status:      channel.NewStatusBox(channel.Telegram, "long polling"),
		log:         log.With("channel", channel.Telegram),
		admins:      map[int64]adminCache{},
		recentJoins: map[string]time.Time{},
	}
}

type adminCache struct {
	roles map[int64]string
	at    time.Time
}

const adminCacheTTL = 10 * time.Minute

// role 群内角色：creator → owner、administrator → admin，其余 member。
// 查询失败也缓存（空表），避免每条消息都打一次接口。刷新时顺带检查机器人自己的权限，缺权限记一条告警。
func (a *Adapter) role(ctx context.Context, chatID, userID int64) string {
	c, ok := a.admins[chatID]
	if !ok || time.Since(c.at) > adminCacheTTL {
		c = adminCache{roles: map[int64]string{}, at: time.Now()}
		var list []struct {
			Status            string `json:"status"`
			User              tgUser `json:"user"`
			CanDeleteMessages bool   `json:"can_delete_messages"`
			CanRestrict       bool   `json:"can_restrict_members"`
			CanInvite         bool   `json:"can_invite_users"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := a.call(callCtx, "getChatAdministrators", map[string]any{"chat_id": chatID}, &list)
		cancel()
		if err != nil {
			a.log.Warn("获取群管理员失败", "chat", chatID, "err", err)
		}
		isAdmin := false
		for _, m := range list {
			switch m.Status {
			case "creator":
				c.roles[m.User.ID] = channel.RoleOwner
			case "administrator":
				c.roles[m.User.ID] = channel.RoleAdmin
			}
			if m.User.ID == a.botID {
				isAdmin = true
				var missing []string
				if !m.CanDeleteMessages {
					missing = append(missing, "删除消息")
				}
				if !m.CanRestrict {
					missing = append(missing, "封禁用户")
				}
				if !m.CanInvite {
					missing = append(missing, "添加成员（审批入群）")
				}
				if len(missing) > 0 {
					a.log.Warn("机器人缺少群管理权限，相关功能不可用", "chat", chatID, "missing", strings.Join(missing, "、"))
				}
			}
		}
		if err == nil && !isAdmin {
			a.log.Warn("机器人不是群管理员：收不到全量群消息，也无法审批入群、删消息、封禁", "chat", chatID)
		}
		a.admins[chatID] = c
	}
	if r, ok := c.roles[userID]; ok {
		return r
	}
	return channel.RoleMember
}

func (a *Adapter) Name() string           { return channel.Telegram }
func (a *Adapter) Status() channel.Status { return a.status.Get() }

type apiResp struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

func (a *Adapter) call(ctx context.Context, method string, in, out any) error {
	raw, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/"+method, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return stripURL("telegram "+method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %d %s", method, r.ErrorCode, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (a *Adapter) Start(ctx context.Context, h channel.Handler) error {
	var me struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.call(callCtx, "getMe", map[string]any{}, &me); err != nil {
		a.status.Failed(err)
		return err
	}
	a.botID, a.botUsername = me.ID, me.Username
	a.status.Connected("@" + me.Username)

	runCtx, stop := context.WithCancel(context.Background())
	a.cancel = stop
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.poll(runCtx, h)
	}()
	return nil
}

func (a *Adapter) Stop() {
	if a.cancel != nil {
		a.cancel()
		a.wg.Wait()
	}
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgMessage struct {
	MessageID int64      `json:"message_id"`
	From      tgUser     `json:"from"`
	Chat      tgChat     `json:"chat"`
	Text      string     `json:"text"`
	Caption   string     `json:"caption"`
	Entities  []tgEntity `json:"entities"`
	// CaptionEntities 图片说明里的 @ 等标记（偏移相对 caption）
	CaptionEntities []tgEntity `json:"caption_entities"`
	// Photo 同一张图的多个尺寸，从小到大
	Photo []struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
	} `json:"photo"`
	// Document 以文件形式发送的图片（mime_type 为 image/*）
	Document *struct {
		FileID   string `json:"file_id"`
		MimeType string `json:"mime_type"`
		FileSize int64  `json:"file_size"`
	} `json:"document"`
	ReplyToMessage *tgMessage `json:"reply_to_message"`
	// NewChatMembers 新成员入群的服务消息（privacy mode 开启也能收到）
	NewChatMembers []tgUser `json:"new_chat_members"`
	// ForwardOrigin 转发来源（type：user / hidden_user / chat / channel）
	ForwardOrigin *struct {
		Type string `json:"type"`
	} `json:"forward_origin"`
}

type tgEntity struct {
	Type   string  `json:"type"`
	Offset int     `json:"offset"`
	Length int     `json:"length"`
	User   *tgUser `json:"user"`
	// URL text_link 实体的目标地址
	URL string `json:"url"`
}

// tgChat 群 / 私聊的基本信息。
type tgChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// tgJoinRequest chat_join_request 更新：申请人、申请时的个人简介。user_chat_id 即申请人的私聊 chat_id，
// 申请期间机器人可以给对方发私聊（对方没点过 Start 也行）。
type tgJoinRequest struct {
	Chat tgChat `json:"chat"`
	From tgUser `json:"from"`
	Bio  string `json:"bio"`
}

// tgChatMemberUpdated chat_member 更新：成员状态变化（需机器人是管理员且显式订阅）。
type tgChatMemberUpdated struct {
	Chat tgChat `json:"chat"`
	Old  struct {
		Status   string `json:"status"`
		IsMember bool   `json:"is_member"`
	} `json:"old_chat_member"`
	New struct {
		Status   string `json:"status"`
		IsMember bool   `json:"is_member"`
		User     tgUser `json:"user"`
	} `json:"new_chat_member"`
}

// images 消息里的图片：photo 取不超过下载上限的最大尺寸，image/* 的 document 也算。
func (a *Adapter) images(m *tgMessage) []channel.Image {
	var out []channel.Image
	for i := len(m.Photo) - 1; i >= 0; i-- {
		if p := m.Photo[i]; p.FileSize <= media.MaxDownload {
			out = append(out, a.image(p.FileID, "image/jpeg", p.FileSize))
			break
		}
	}
	if d := m.Document; d != nil && strings.HasPrefix(d.MimeType, "image/") && d.FileSize <= media.MaxDownload {
		out = append(out, a.image(d.FileID, d.MimeType, d.FileSize))
	}
	return out
}

func (a *Adapter) image(fileID, contentType string, size int64) channel.Image {
	return channel.Image{ContentType: contentType, Size: size, Open: func(ctx context.Context) (io.ReadCloser, error) {
		var f struct {
			FilePath string `json:"file_path"`
		}
		if err := a.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
			return nil, err
		}
		if f.FilePath == "" {
			return nil, errors.New("telegram getFile 没有返回文件路径")
		}
		body, err := media.Get(ctx, a.fileBase+"/"+f.FilePath)
		return body, stripURL("telegram 下载图片", err)
	}}
}

// stripURL 网络错误（*url.Error）的文本带完整请求地址，而地址里有 bot token：去掉地址只留原因，免得 token 进日志。
func stripURL(op string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", op, ue.Err)
	}
	return err
}

// poll offset 只在本进程内推进：重启后从平台未确认的更新继续（平台保留 24 小时），
// 最多重复处理少量消息，由 messages 入站唯一索引去重。
func (a *Adapter) poll(ctx context.Context, h channel.Handler) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		var updates []struct {
			UpdateID    int64                `json:"update_id"`
			Message     *tgMessage           `json:"message"`
			JoinRequest *tgJoinRequest       `json:"chat_join_request"`
			ChatMember  *tgChatMemberUpdated `json:"chat_member"`
		}
		// chat_join_request / chat_member 不显式列出就收不到；两者都要求机器人是群管理员
		err := a.call(ctx, "getUpdates", map[string]any{
			"offset": offset, "timeout": pollTimeoutSec, "allowed_updates": []string{"message", "chat_join_request", "chat_member"},
		}, &updates)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.status.Failed(err)
			a.log.Warn("getUpdates 失败", "err", err, "retryIn", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 60*time.Second)
			continue
		}
		// 任意一次成功都要把状态拉回已连接，否则一次失败后会永远显示断开
		backoff = time.Second
		a.status.Connected("")
		for _, u := range updates {
			offset = u.UpdateID + 1
			if jr := u.JoinRequest; jr != nil && isGroup(jr.Chat.Type) {
				h(channel.Inbound{
					Kind: channel.KindJoinRequest, Channel: channel.Telegram, ChatType: channel.ChatGroup,
					ChatID: strconv.FormatInt(jr.Chat.ID, 10), ChatTitle: jr.Chat.Title,
					SenderID: strconv.FormatInt(jr.From.ID, 10), SenderName: fullName(jr.From),
					JoinRequest: &channel.JoinRequest{Answer: strings.TrimSpace(jr.Bio), NeedsAnswer: true},
				})
			}
			if cm := u.ChatMember; cm != nil && isGroup(cm.Chat.Type) && joined(cm) {
				if in, ok := a.memberJoin(cm.Chat, cm.New.User); ok {
					h(in)
				}
			}
			if u.Message == nil {
				continue
			}
			for _, in := range a.memberJoins(u.Message) {
				h(in)
			}
			if in, ok := a.parse(u.Message); ok {
				if in.ChatType == channel.ChatGroup {
					in.SenderRole = a.role(ctx, u.Message.Chat.ID, u.Message.From.ID)
				}
				h(in)
			}
		}
	}
}

func isGroup(chatType string) bool { return chatType == "group" || chatType == "supergroup" }

func fullName(u tgUser) string { return strings.TrimSpace(u.FirstName + " " + u.LastName) }

// joined chat_member 更新是否表示「成为成员」：之前不在群里（left / kicked / 被限制且非成员），现在在群里。
func joined(cm *tgChatMemberUpdated) bool {
	wasIn := isMember(cm.Old.Status, cm.Old.IsMember)
	nowIn := isMember(cm.New.Status, cm.New.IsMember)
	return !wasIn && nowIn
}

func isMember(status string, isMember bool) bool {
	switch status {
	case "member", "administrator", "creator":
		return true
	case "restricted":
		return isMember
	}
	return false
}

// recentJoinTTL 同一人的入群在这段时间内只上报一次（服务消息与 chat_member 更新先后到达）。
const recentJoinTTL = 2 * time.Minute

// memberJoin 生成入群事件；机器人（含自己）与短时间内已上报过的跳过。
func (a *Adapter) memberJoin(chat tgChat, u tgUser) (channel.Inbound, bool) {
	if u.IsBot {
		return channel.Inbound{}, false
	}
	now := time.Now()
	key := strconv.FormatInt(chat.ID, 10) + ":" + strconv.FormatInt(u.ID, 10)
	if at, ok := a.recentJoins[key]; ok && now.Sub(at) < recentJoinTTL {
		return channel.Inbound{}, false
	}
	for k, at := range a.recentJoins {
		if now.Sub(at) >= recentJoinTTL {
			delete(a.recentJoins, k)
		}
	}
	a.recentJoins[key] = now
	return channel.Inbound{
		Kind: channel.KindMemberJoin, Channel: channel.Telegram, ChatType: channel.ChatGroup,
		ChatID: strconv.FormatInt(chat.ID, 10), ChatTitle: chat.Title,
		SenderID: strconv.FormatInt(u.ID, 10), SenderName: fullName(u),
	}, true
}

// memberJoins 把入群服务消息转成入群事件（跳过机器人，包括自己）。
func (a *Adapter) memberJoins(m *tgMessage) []channel.Inbound {
	if !isGroup(m.Chat.Type) {
		return nil
	}
	var out []channel.Inbound
	for _, u := range m.NewChatMembers {
		if in, ok := a.memberJoin(m.Chat, u); ok {
			out = append(out, in)
		}
	}
	return out
}

func (a *Adapter) parse(m *tgMessage) (channel.Inbound, bool) {
	text, entities := m.Text, m.Entities
	if text == "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	images := a.images(m)
	if (text == "" && len(images) == 0) || m.From.ID == a.botID {
		return channel.Inbound{}, false
	}
	in := channel.Inbound{
		Channel:    channel.Telegram,
		ChatID:     strconv.FormatInt(m.Chat.ID, 10),
		ChatTitle:  m.Chat.Title,
		MessageID:  strconv.FormatInt(m.MessageID, 10),
		SenderID:   strconv.FormatInt(m.From.ID, 10),
		SenderName: strings.TrimSpace(m.From.FirstName + " " + m.From.LastName),
	}
	if m.ReplyToMessage != nil {
		in.Quoted = m.ReplyToMessage.Text
		if in.Quoted == "" {
			in.Quoted = m.ReplyToMessage.Caption
		}
		// 引用了一张图问「这是什么问题」：被引用的图也交给模型
		images = append(images, a.images(m.ReplyToMessage)...)
	}
	in.Images = images[:min(len(images), channel.MaxImages)]
	if m.ForwardOrigin != nil {
		in.ForwardFrom = m.ForwardOrigin.Type
	}
	switch m.Chat.Type {
	case "private":
		// 关闭「响应私聊」时照样上报（Mentioned=false）：入群验证要靠私聊回复，由编排器决定是否进入问答
		in.ChatType, in.Mentioned, in.Text = channel.ChatPrivate, a.cfg.Private, strings.TrimSpace(text)
		return in, true
	case "group", "supergroup":
		in.ChatType = channel.ChatGroup
	default:
		return in, false
	}

	// entity 偏移是 UTF-16 码元：按 UTF-16 切片比对用户名，从后往前删掉 @bot 保持前面的偏移不变
	u16 := utf16.Encode([]rune(text))
	for i := len(entities) - 1; i >= 0; i-- {
		e := entities[i]
		if e.Offset < 0 || e.Offset+e.Length > len(u16) {
			continue
		}
		hit := false
		switch e.Type {
		case "mention":
			name := string(utf16.Decode(u16[e.Offset : e.Offset+e.Length]))
			hit = strings.EqualFold(strings.TrimPrefix(name, "@"), a.botUsername)
			if !hit {
				in.MentionsUser = true
			}
		case "text_mention":
			hit = e.User != nil && e.User.ID == a.botID
			if !hit {
				in.MentionsUser = true
			}
		case "url":
			in.Links = append(in.Links, string(utf16.Decode(u16[e.Offset:e.Offset+e.Length])))
		case "text_link":
			if e.URL != "" {
				in.Links = append(in.Links, e.URL)
			}
		}
		if hit {
			in.Mentioned = true
			u16 = append(u16[:e.Offset:e.Offset], u16[e.Offset+e.Length:]...)
		}
	}
	if m.ReplyToMessage != nil && m.ReplyToMessage.From.ID == a.botID {
		in.Mentioned = true
	}
	in.Text = strings.TrimSpace(string(utf16.Decode(u16)))
	return in, true
}

// Send 超过 4096 字符（TG 硬上限）的部分由上层按 maxReplyChars 拆条；这里兜底硬切。
func (a *Adapter) Send(ctx context.Context, out channel.Outbound) error {
	chatID, err := strconv.ParseInt(out.ChatID, 10, 64)
	if err != nil {
		return errors.New("telegram chat_id 非法")
	}
	text := out.Text
	if out.Format != channel.FormatText {
		text = channel.RenderPlain(text)
	}
	// 精简 Markdown 转 HTML 发（单条 4096 字以内）；HTML 被拒或超长时按纯文本拆条发
	if out.Format == channel.FormatMarkdown {
		if h := channel.RenderTelegramHTML(out.Text); utf8.RuneCountInString(h) <= 4096 {
			err := a.sendMessage(ctx, chatID, h, "HTML", quoteID(out))
			if err == nil || !strings.Contains(err.Error(), "can't parse entities") {
				return err
			}
			a.log.Warn("HTML 消息被拒，改发纯文本", "chat", out.ChatID, "err", err)
		}
	}
	r := []rune(text)
	for first := true; len(r) > 0; first = false {
		n := min(len(r), 4096)
		replyTo := ""
		if first {
			replyTo = quoteID(out)
		}
		if err := a.sendMessage(ctx, chatID, string(r[:n]), "", replyTo); err != nil {
			return err
		}
		r = r[n:]
	}
	return nil
}

// quoteID 需要引用时返回被引用的消息 ID。
func quoteID(out channel.Outbound) string {
	if out.Quote {
		return out.ReplyTo
	}
	return ""
}

func (a *Adapter) sendMessage(ctx context.Context, chatID int64, text, parseMode, replyTo string) error {
	body := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		body["parse_mode"] = parseMode
		body["link_preview_options"] = map[string]any{"is_disabled": true}
	}
	if replyTo != "" {
		if id, err := strconv.ParseInt(replyTo, 10, 64); err == nil {
			body["reply_parameters"] = map[string]any{"message_id": id, "allow_sending_without_reply": true}
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return a.call(callCtx, "sendMessage", body, nil)
}

// ── 入群审批 / 群消息治理（机器人需是群管理员并有对应权限）──

// ReviewJoin 审批入群申请。TG 的拒绝接口不带理由：拒绝理由由编排器私聊告知申请人，这里忽略 reason。
func (a *Adapter) ReviewJoin(ctx context.Context, chatID, userID string, _ channel.JoinRequest, approve bool, _ string) error {
	method := "declineChatJoinRequest"
	if approve {
		method = "approveChatJoinRequest"
	}
	return a.callIDs(ctx, method, chatID, userID, nil)
}

func (a *Adapter) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	cid, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return errors.New("telegram chat_id 非法")
	}
	mid, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil {
		return errors.New("telegram message_id 非法")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.call(callCtx, "deleteMessage", map[string]any{"chat_id": cid, "message_id": mid}, nil)
}

// MuteMember 收回全部发言权限到 until（TG 规定不足 30 秒或超过 366 天视为永久）。
func (a *Adapter) MuteMember(ctx context.Context, chatID, userID string, until time.Time) error {
	return a.callIDs(ctx, "restrictChatMember", chatID, userID, map[string]any{
		"permissions": permissions(false), "until_date": until.Unix(),
	})
}

func (a *Adapter) BanMember(ctx context.Context, chatID, userID string) error {
	return a.callIDs(ctx, "banChatMember", chatID, userID, nil)
}

// Unrestrict 解封（仅对被封禁的生效，不会把在群里的人踢出去）并恢复全部发言权限。
func (a *Adapter) Unrestrict(ctx context.Context, chatID, userID string) error {
	if err := a.callIDs(ctx, "unbanChatMember", chatID, userID, map[string]any{"only_if_banned": true}); err != nil {
		return err
	}
	return a.callIDs(ctx, "restrictChatMember", chatID, userID, map[string]any{"permissions": permissions(true)})
}

// permissions 成员权限表，全开或全关。
func permissions(on bool) map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{
		"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos",
		"can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages",
		"can_add_web_page_previews", "can_invite_users", "can_pin_messages", "can_change_info", "can_manage_topics",
	} {
		out[k] = on
	}
	return out
}

// callIDs 带 chat_id / user_id 的管理接口调用，10 秒超时。
func (a *Adapter) callIDs(ctx context.Context, method, chatID, userID string, extra map[string]any) error {
	cid, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return errors.New("telegram chat_id 非法")
	}
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return errors.New("telegram user_id 非法")
	}
	body := map[string]any{"chat_id": cid, "user_id": uid}
	for k, v := range extra {
		body[k] = v
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.call(callCtx, method, body, nil)
}
