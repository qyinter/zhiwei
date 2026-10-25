package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"zhiwei/internal/channel"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

// ── 入群欢迎 ──

// welcome 新成员入群时发欢迎语。QQ 的入群事件不支持被动回复，走主动消息；
// QQ 入群事件不带昵称（成员信息接口仅白名单开放），取不到时用「新朋友」。
func (o *Orchestrator) welcome(ctx context.Context, cfg settings.All, in channel.Inbound) {
	if !cfg.WelcomeOn(in.Channel, in.ChatID) {
		return
	}
	name := in.SenderName
	if name == "" {
		name = o.st.KnownUserName(ctx, in.Channel, in.SenderID)
	}
	if err := o.sendWelcome(ctx, cfg, in.Channel, in.ChatType, in.ChatID, in.SenderID, name); err != nil {
		o.log.Warn("发送入群欢迎失败", "channel", in.Channel, "chat", in.ChatID, "err", err)
		return
	}
	o.log.Info("已发送入群欢迎", "channel", in.Channel, "chat", in.ChatID, "user", in.SenderID)
}

func (o *Orchestrator) sendWelcome(ctx context.Context, cfg settings.All, ch, chatType, chatID, userID, name string) error {
	w := cfg.Welcome
	tpl, markdown := w.MessageQQ, w.FormatQQ == "markdown"
	if ch == channel.Telegram {
		tpl, markdown = w.MessageTG, false
	}
	if strings.TrimSpace(tpl) == "" {
		return errors.New("欢迎语为空")
	}
	if name == "" {
		name = "新朋友"
	}
	text := renderWelcome(tpl, welcomeVars{brand: cfg.Brand.Name, name: name, userID: userID, qq: ch == channel.QQBot, markdown: markdown})
	out := channel.Outbound{ChatType: chatType, ChatID: chatID, Text: text}
	if markdown {
		out.Format = channel.FormatQQMarkdown
	}
	return o.reg.Send(ctx, ch, out)
}

type welcomeVars struct {
	brand, name, userID string
	qq, markdown        bool
}

var askPlaceholder = regexp.MustCompile(`\{ask:([^{}]+)\}`)

// renderWelcome 替换欢迎语占位。QQ Markdown 下 {at} 渲染为官方 @ 标签、{ask:问题} 渲染为「参数指令」
// （点击后把问题填进输入框，参数按文档要求 urlencode）；其他场景退化为纯文字。
func renderWelcome(tpl string, v welcomeVars) string {
	at := v.name
	if v.qq && v.markdown && v.userID != "" {
		at = `<qqbot-at-user id="` + v.userID + `" />`
	}
	out := askPlaceholder.ReplaceAllStringFunc(tpl, func(m string) string {
		q := strings.TrimSpace(askPlaceholder.FindStringSubmatch(m)[1])
		if v.qq && v.markdown {
			e := url.PathEscape(q)
			return `<qqbot-cmd-input text="` + e + `" show="` + e + `" reference="false" />`
		}
		return "「" + q + "」"
	})
	brand := v.brand
	if brand == "" {
		brand = "本群"
	}
	return strings.NewReplacer("{name}", v.name, "{brand}", brand, "{at}", at).Replace(out)
}

// WelcomeTest 后台「发送测试」：按草稿设置把欢迎语发到指定群，@ 该群最近发过言的人（没有则不 @）。
func (o *Orchestrator) WelcomeTest(ctx context.Context, w settings.Welcome, ch, chatID string) (string, error) {
	cfg, _ := o.snapshot()
	cfg.Welcome = w
	u, err := o.st.LatestChatUser(ctx, ch, chatID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if err := o.sendWelcome(ctx, cfg, ch, channel.ChatGroup, chatID, u.UserID, u.UserName); err != nil {
		return "", err
	}
	return u.UserName, nil
}

// ── 入群审核 ──

// checkTimeout 核对接口超时。
const checkTimeout = 5 * time.Second

var checkClient = &http.Client{Timeout: checkTimeout}

// extractMemberID 从申请答案里提取待核对 ID。
// 规则：按 pattern 取第一个前后不紧贴字母数字的匹配；pattern 为空或无匹配时返回整段答案（trim 后）。
func extractMemberID(answer, pattern string) string {
	answer = strings.TrimSpace(answer)
	if pattern == "" || answer == "" {
		return answer
	}
	re, err := regexp.Compile(`(?:^|[^A-Za-z0-9])(` + pattern + `)(?:[^A-Za-z0-9]|$)`)
	if err != nil {
		return answer
	}
	if m := re.FindStringSubmatch(answer); m != nil {
		return m[1]
	}
	return answer
}

// checkMember GET {checkURL}?id=…，只认 {"status":"ok"} / {"status":"error"}；
// 其余情况（网络错误、超时、非 2xx、其他内容）返回空串表示无法判断。
func checkMember(ctx context.Context, checkURL, id string) (string, error) {
	u, err := url.Parse(checkURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("id", id)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := checkClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("核对接口返回 HTTP %d", resp.StatusCode)
	}
	var r struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &r); err != nil || (r.Status != "ok" && r.Status != "error") {
		return "", fmt.Errorf("核对接口返回无法识别：%s", truncateRunes(string(body), 200))
	}
	return r.Status, nil
}

// reviewJoin 入群申请：提取 ID → 核对 → 同意 / 拒绝；核对失败不处理，留给管理员。每次都记一行处理记录。
func (o *Orchestrator) reviewJoin(cfg settings.All, in channel.Inbound) {
	jr := cfg.JoinReview
	req := in.JoinRequest
	if !cfg.JoinReviewOn(in.Channel, in.ChatID) || req == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec := store.JoinRequestRecord{
		Channel: in.Channel, ChatID: in.ChatID, UserID: in.SenderID, UserName: in.SenderName, Answer: req.Answer,
	}
	defer func() {
		if err := o.st.AddJoinRequest(ctx, rec); err != nil {
			o.log.Warn("记录入群申请失败", "err", err)
		}
		o.log.Info("入群申请", "chat", in.ChatID, "user", in.SenderID, "decision", rec.Decision, "detail", rec.Detail)
	}()

	if req.AutoApproved {
		rec.Decision, rec.Detail = store.JoinAuto, "平台入群策略已自动通过"
		return
	}
	rec.ExtractedID = extractMemberID(req.Answer, jr.IDPattern)
	approve, reason := false, jr.RejectReason
	if rec.ExtractedID == "" {
		rec.Detail = "申请未填写答案"
	} else {
		status, err := checkMember(ctx, jr.CheckURL, rec.ExtractedID)
		if err != nil {
			rec.Decision, rec.Detail = store.JoinPending, "核对失败，留给管理员处理："+err.Error()
			return
		}
		approve = status == "ok"
		rec.Detail = "核对接口返回 " + status
	}
	if err := o.reg.ReviewJoin(ctx, in.Channel, in.ChatID, in.SenderID, *req, approve, reason); err != nil {
		rec.Decision, rec.Detail = store.JoinFailed, rec.Detail+"；审批接口出错："+err.Error()
		return
	}
	if approve {
		rec.Decision = store.JoinApproved
	} else {
		rec.Decision = store.JoinDeclined
	}
}
