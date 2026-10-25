// Package channel 定义渠道适配器的统一契约。
//
// 适配器只做两件事：把平台事件翻译成 Inbound 交给 Handler，把 Outbound 翻译成平台 API 调用。
// 触发规则（@、白名单）、会话、LLM 一概不在这里——新增渠道（企业微信、Discord…）只需实现 Adapter。
package channel

import (
	"context"
	"io"
	"net/http"
)

// 渠道标识，也是 conversations.channel 列的取值。
const (
	QQBot      = "qqbot"
	Telegram   = "telegram"
	WeCom      = "wecom"
	Playground = "playground"
)

// 聊天类型
const (
	ChatGroup   = "group"   // QQ 群 / TG 群
	ChatPrivate = "private" // QQ 单聊(C2C) / TG 私聊
	ChatGuild   = "guild"   // QQ 频道子频道
	ChatGuildDM = "guild_dm"
)

// 入站事件类型。普通消息 Kind 为空。
const (
	KindMemberJoin  = "member_join"  // 新成员入群（入群欢迎）
	KindJoinRequest = "join_request" // 入群申请（入群审核）
)

// 群内角色：owner / admin 视为客服发言（反思沉淀的素材、主动回复的抑制条件）。
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Inbound 平台无关的入站事件（绝大多数是消息）。
type Inbound struct {
	// Kind 为空 = 消息；其余见 Kind* 常量
	Kind       string
	Channel    string
	ChatType   string
	ChatID     string
	ChatTitle  string
	MessageID  string
	SenderID   string
	SenderName string
	// SenderRole 群内角色（RoleOwner / RoleAdmin / RoleMember），平台不提供时为空
	SenderRole string
	// Text 已剥掉 @机器人 的正文
	Text string
	// Mentioned 是否 @了机器人 / 回复了机器人；私聊恒为 true
	Mentioned bool
	// Quoted 用户引用的那条消息文本（有就带上，给模型补上下文）
	Quoted string
	// Images 消息附带的图片（含引用消息里的图片）。只是下载入口，真正下载在编排器里做
	Images []Image
	// ReplyCtx 适配器自用的回复上下文（如 QQ 的 guild_id），原样回传给 Send
	ReplyCtx map[string]string
	// JoinRequest Kind=KindJoinRequest 时有值
	JoinRequest *JoinRequest
}

// MaxImages 一条消息最多取几张图片（多的忽略）。
const MaxImages = 4

// Image 入站消息附带的一张图片。Open 由适配器实现（QQ 直接下载附件地址，Telegram 先 getFile 换下载地址），
// 编排器只在需要时（@ 机器人的消息、私聊）才下载，不阻塞渠道的事件循环。
type Image struct {
	ContentType string
	// Size 平台给出的字节数，未知为 0
	Size int64
	Open func(ctx context.Context) (io.ReadCloser, error)
}

// JoinRequest 入群申请。Answer 是申请人填写的验证消息 / 入群问题答案（多个答案按行拼接）。
type JoinRequest struct {
	RequestID string
	Answer    string
	// AutoApproved 平台侧策略已自动通过（只是通知，不需要再审批）
	AutoApproved bool
}

// Outbound 发给某个聊天的一条文本。
type Outbound struct {
	ChatType string
	ChatID   string
	Text     string
	// ReplyTo 被回复的平台消息 ID：QQ 官方机器人被动回复必填（5 分钟内有效），TG 用于引用
	ReplyTo string
	// Seq 同一 ReplyTo 下第几条回复（QQ msg_seq 去重用，从 1 起）
	Seq int
	// Quote 以「引用 ReplyTo 那条消息」的样式展示（拆条回复只有第一条引用；ReplyTo 为空时忽略）
	Quote bool
	// AtUser 需要 @ 的用户平台 ID（群聊回复提问人用）；渠道不支持或已开关闭时忽略
	AtUser string
	// Format 正文格式，见 Format* 常量
	Format   string
	ReplyCtx map[string]string
}

const (
	// FormatText 纯文本，原样发送
	FormatText = ""
	// FormatMarkdown 回复用的精简 Markdown（markdown.go），各渠道发送前渲染成自己的格式
	FormatMarkdown = "markdown"
	// FormatQQMarkdown QQ 原生 Markdown（可含 <qqbot-at-user> 等标签，入群欢迎用）：
	// 只有 QQ 群聊 / 单聊按 Markdown 消息发，其他渠道与场景按原文发
	FormatQQMarkdown = "qqmarkdown"
)

// Handler 由 agent 编排器实现。适配器对每条入站消息调用一次，不等待处理结果。
type Handler func(Inbound)

type Status struct {
	Channel   string `json:"channel"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	// Detail 人类可读的状态补充（机器人名、接入方式、最近错误）
	Detail    string `json:"detail"`
	LastError string `json:"lastError"`
}

type Adapter interface {
	Name() string
	// Start 非阻塞：建立连接 / 启动轮询后立即返回，连接失败在后台退避重试。
	Start(ctx context.Context, h Handler) error
	Stop()
	Send(ctx context.Context, out Outbound) error
	Status() Status
}

// JoinReviewer 支持审批入群申请的适配器（QQ 官方机器人需是群管理员）。
type JoinReviewer interface {
	ReviewJoin(ctx context.Context, chatID, userID string, req JoinRequest, approve bool, reason string) error
}

// GroupNamer 能按群 ID 查群名的适配器（QQ 官方机器人：群事件只带 openid，群名要另调接口）。查不到返回空串。
type GroupNamer interface {
	GroupName(ctx context.Context, chatID string) string
}

// WebhookAdapter 需要接收平台 HTTP 回调的适配器（QQ webhook 模式、企业微信）。
// 路由固定挂在 /webhook/{Name()}，由 httpapi 转发。
type WebhookAdapter interface {
	Adapter
	ServeWebhook(w http.ResponseWriter, r *http.Request)
}
