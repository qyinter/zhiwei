package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ConvActive  = "active"
	ConvHandoff = "handoff"

	DirIn    = "in"
	DirOut   = "out"
	DirHuman = "human"
)

type Conversation struct {
	ID               int64     `json:"id"`
	Channel          string    `json:"channel"`
	ChatType         string    `json:"chatType"`
	ChatID           string    `json:"chatId"`
	UserID           string    `json:"userId"`
	UserName         string    `json:"userName"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	ContextSince     time.Time `json:"contextSince"`
	LastInboundMsgID string    `json:"-"`
	LastMessageAt    time.Time `json:"lastMessageAt"`
	CreatedAt        time.Time `json:"createdAt"`
	MessageCount     int       `json:"messageCount"`
}

const convCols = `id, channel, chat_type, chat_id, user_id, user_name, title, status, context_since, last_inbound_msg_id, last_message_at, created_at`

func scanConv(row pgx.Row, c *Conversation) error {
	return row.Scan(&c.ID, &c.Channel, &c.ChatType, &c.ChatID, &c.UserID, &c.UserName, &c.Title, &c.Status,
		&c.ContextSince, &c.LastInboundMsgID, &c.LastMessageAt, &c.CreatedAt)
}

type ConvKey struct {
	Channel  string
	ChatType string
	ChatID   string
	UserID   string
	UserName string
	Title    string
}

// UpsertConversation 按 (channel, chat_id, user_id) 取或建会话；名称非空时顺带刷新。
func (s *Store) UpsertConversation(ctx context.Context, k ConvKey) (Conversation, error) {
	var c Conversation
	// 单聊事件不带昵称：新建会话时借用同一用户在其他会话（通常是群聊）里的昵称
	err := scanConv(s.pool.QueryRow(ctx, `INSERT INTO conversations (channel, chat_type, chat_id, user_id, user_name, title)
		VALUES ($1::text, $2::text, $3::text, $4::text, COALESCE(NULLIF($5::text, ''),
			(SELECT o.user_name FROM conversations o WHERE o.channel = $1::text AND o.user_id = $4::text AND o.user_name <> ''
			 ORDER BY o.last_message_at DESC LIMIT 1), ''), $6::text)
		ON CONFLICT (channel, chat_id, user_id) DO UPDATE SET
			title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE conversations.title END,
			user_name = CASE WHEN EXCLUDED.user_name <> '' THEN EXCLUDED.user_name ELSE conversations.user_name END
		RETURNING `+convCols, k.Channel, k.ChatType, k.ChatID, k.UserID, k.UserName, k.Title), &c)
	return c, err
}

func (s *Store) Conversation(ctx context.Context, id int64) (Conversation, error) {
	var c Conversation
	err := scanConv(s.pool.QueryRow(ctx, `SELECT `+convCols+` FROM conversations WHERE id = $1`, id), &c)
	return c, notFound(err)
}

type ConvFilter struct {
	Channel string
	Status  string
	Keyword string
	Page    int
	Size    int
}

func (s *Store) ListConversations(ctx context.Context, f ConvFilter) ([]Conversation, int, error) {
	where := `WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)
		AND ($3 = '' OR title ILIKE '%' || $3 || '%' OR chat_id ILIKE '%' || $3 || '%'
			OR user_name ILIKE '%' || $3 || '%' OR user_id ILIKE '%' || $3 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM conversations `+where, f.Channel, f.Status, f.Keyword).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+convCols+`,
			(SELECT count(*) FROM messages m WHERE m.conversation_id = conversations.id)
		FROM conversations `+where+` ORDER BY last_message_at DESC LIMIT $4 OFFSET $5`,
		f.Channel, f.Status, f.Keyword, f.Size, (f.Page-1)*f.Size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []Conversation{}
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Channel, &c.ChatType, &c.ChatID, &c.UserID, &c.UserName, &c.Title, &c.Status,
			&c.ContextSince, &c.LastInboundMsgID, &c.LastMessageAt, &c.CreatedAt, &c.MessageCount); err != nil {
			return nil, 0, err
		}
		list = append(list, c)
	}
	return list, total, rows.Err()
}

// ResetContext 推进上下文起点：之前的消息仍可在后台查看，但不再喂给模型。
func (s *Store) ResetContext(ctx context.Context, convID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET context_since = now() WHERE id = $1`, convID)
	return err
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversationId"`
	Direction      string    `json:"direction"`
	SenderID       string    `json:"senderId"`
	SenderName     string    `json:"senderName"`
	Content        string    `json:"content"`
	PlatformMsgID  string    `json:"platformMsgId"`
	CreatedAt      time.Time `json:"createdAt"`
	// Images 只在后台消息列表（ListMessages）里填充
	Images []MessageImage `json:"images,omitempty"`
}

// ErrDuplicate 入站消息已处理过（平台重投）。
var ErrDuplicate = errors.New("duplicate message")

// AddMessage 落一条消息并刷新会话的活跃时间；入站消息同时记下平台消息 ID（被动回复用）。
// 同一条入站消息重复投递返回 ErrDuplicate（uq_messages_inbound 判重）。
func (s *Store) AddMessage(ctx context.Context, m *Message) error {
	err := s.pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, direction, sender_id, sender_name, content, platform_msg_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (conversation_id, platform_msg_id) WHERE direction = 'in' AND platform_msg_id <> '' DO NOTHING
		RETURNING id, created_at`,
		m.ConversationID, m.Direction, m.SenderID, m.SenderName, m.Content, m.PlatformMsgID).Scan(&m.ID, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	if m.Direction == DirIn && m.PlatformMsgID != "" {
		_, err = s.pool.Exec(ctx, `UPDATE conversations SET last_message_at = now(), last_inbound_msg_id = $2 WHERE id = $1`,
			m.ConversationID, m.PlatformMsgID)
	} else {
		_, err = s.pool.Exec(ctx, `UPDATE conversations SET last_message_at = now() WHERE id = $1`, m.ConversationID)
	}
	return err
}

// RecentMessages 取上下文起点之后最近 limit 条，按时间正序返回。
func (s *Store) RecentMessages(ctx context.Context, convID int64, since time.Time, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT * FROM (
			SELECT id, conversation_id, direction, sender_id, sender_name, content, platform_msg_id, created_at
			FROM messages WHERE conversation_id = $1 AND created_at >= $2
			ORDER BY created_at DESC, id DESC LIMIT $3
		) t ORDER BY created_at, id`, convID, since, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows)
}

func (s *Store) ListMessages(ctx context.Context, convID int64, beforeID int64, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT * FROM (
			SELECT id, conversation_id, direction, sender_id, sender_name, content, platform_msg_id, created_at
			FROM messages WHERE conversation_id = $1 AND ($2 = 0 OR id < $2)
			ORDER BY id DESC LIMIT $3
		) t ORDER BY id`, convID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	list, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	return list, s.attachImages(ctx, list)
}

func collectMessages(rows pgx.Rows) ([]Message, error) {
	defer rows.Close()
	list := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Direction, &m.SenderID, &m.SenderName,
			&m.Content, &m.PlatformMsgID, &m.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// ── 转人工 ──

type Handoff struct {
	ID             int64      `json:"id"`
	ConversationID int64      `json:"conversationId"`
	Reason         string     `json:"reason"`
	Summary        string     `json:"summary"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	ResolvedAt     *time.Time `json:"resolvedAt"`
	Channel        string     `json:"channel"`
	ChatID         string     `json:"chatId"`
	UserID         string     `json:"userId"`
	UserName       string     `json:"userName"`
	Title          string     `json:"title"`
}

// OpenHandoff 把会话置为 handoff 并建工单。会话已在人工中则不重复建单（返回 false）。
func (s *Store) OpenHandoff(ctx context.Context, convID int64, reason, summary string) (created bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE conversations SET status = 'handoff' WHERE id = $1 AND status <> 'handoff'`, convID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		created = true
		_, err = tx.Exec(ctx, `INSERT INTO handoffs (conversation_id, reason, summary) VALUES ($1, $2, $3)`, convID, reason, summary)
		return err
	})
	return created, err
}

// ResolveHandoff 关闭该会话所有未结工单、恢复自动回复，并推进上下文起点——
// 人工处理期间的对话不该被模型当成「自己说过的话」续写。
// adminID=nil 表示超时自动恢复。
func (s *Store) ResolveHandoff(ctx context.Context, convID int64, adminID *int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE handoffs SET status = 'resolved', resolved_at = now(), resolved_by = $2
			WHERE conversation_id = $1 AND status = 'open'`, convID, adminID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET status = 'active', context_since = now() WHERE id = $1`, convID)
		return err
	})
}

// StaleHandoffConversations 人工接待超过 timeout 仍未处理的会话（超时自动恢复用）。
func (s *Store) StaleHandoffConversations(ctx context.Context, timeout time.Duration) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT conversation_id FROM handoffs
		WHERE status = 'open' AND created_at < $1`, time.Now().Add(-timeout))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func (s *Store) ListHandoffs(ctx context.Context, status string, page, size int) ([]Handoff, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM handoffs WHERE ($1 = '' OR status = $1)`, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT h.id, h.conversation_id, h.reason, h.summary, h.status, h.created_at, h.resolved_at,
			c.channel, c.chat_id, c.user_id, c.user_name, c.title
		FROM handoffs h JOIN conversations c ON c.id = h.conversation_id
		WHERE ($1 = '' OR h.status = $1) ORDER BY h.created_at DESC LIMIT $2 OFFSET $3`, status, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []Handoff{}
	for rows.Next() {
		var h Handoff
		if err := rows.Scan(&h.ID, &h.ConversationID, &h.Reason, &h.Summary, &h.Status, &h.CreatedAt, &h.ResolvedAt,
			&h.Channel, &h.ChatID, &h.UserID, &h.UserName, &h.Title); err != nil {
			return nil, 0, err
		}
		list = append(list, h)
	}
	return list, total, rows.Err()
}

// ── 用量 ──

type LLMCall struct {
	ConversationID   *int64
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	LatencyMs        int
	ToolCalls        int
	// ToolNames 本轮依次调用的工具名（可重复）
	ToolNames []string
	Images    int
	Error     string
	// Purpose 调用用途（PurposeReply 等），空 = reply
	Purpose string
}

const (
	PurposeReply     = "reply"
	PurposeProactive = "proactive"
	PurposeJudge     = "judge"
	PurposeReflect   = "reflect"
	PurposeCompact   = "compact"
	PurposePromote   = "promote"
	PurposeTopic     = "topic"
)

func (s *Store) RecordLLMCall(ctx context.Context, c LLMCall) error {
	if c.Purpose == "" {
		c.Purpose = PurposeReply
	}
	if c.ToolNames == nil {
		c.ToolNames = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO llm_calls (conversation_id, model, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, latency_ms, tool_calls, tool_names, images, error, purpose)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		c.ConversationID, c.Model, c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens,
		c.LatencyMs, c.ToolCalls, c.ToolNames, c.Images, c.Error, c.Purpose)
	return err
}

type Overview struct {
	Conversations        int `json:"conversations"`
	OpenHandoffs         int `json:"openHandoffs"`
	TodayInbound         int `json:"todayInbound"`
	TodayReplies         int `json:"todayReplies"`
	TodayLLMCalls        int `json:"todayLlmCalls"`
	TodayLLMErrors       int `json:"todayLlmErrors"`
	TodayInputTokens     int `json:"todayInputTokens"`
	TodayOutputTokens    int `json:"todayOutputTokens"`
	TodayCacheReadTokens int `json:"todayCacheReadTokens"`
	KBDocuments          int `json:"kbDocuments"`
}

// Overview dayStart 是「今日」的起点（按后台浏览器所在时区算好传进来；库的时区通常是 UTC）。
func (s *Store) Overview(ctx context.Context, dayStart time.Time) (o Overview, err error) {
	err = s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM conversations WHERE channel <> 'playground'),
		(SELECT count(*) FROM handoffs WHERE status = 'open'),
		(SELECT count(*) FROM messages m JOIN conversations c ON c.id = m.conversation_id
			WHERE m.direction = 'in' AND c.channel <> 'playground' AND m.created_at >= $1),
		(SELECT count(*) FROM messages m JOIN conversations c ON c.id = m.conversation_id
			WHERE m.direction = 'out' AND c.channel <> 'playground' AND m.created_at >= $1),
		(SELECT count(*) FROM llm_calls WHERE created_at >= $1),
		(SELECT count(*) FROM llm_calls WHERE error <> '' AND created_at >= $1),
		(SELECT coalesce(sum(input_tokens), 0) FROM llm_calls WHERE created_at >= $1),
		(SELECT coalesce(sum(output_tokens), 0) FROM llm_calls WHERE created_at >= $1),
		(SELECT coalesce(sum(cache_read_tokens), 0) FROM llm_calls WHERE created_at >= $1),
		(SELECT count(*) FROM kb_documents)`, dayStart).
		Scan(&o.Conversations, &o.OpenHandoffs, &o.TodayInbound, &o.TodayReplies, &o.TodayLLMCalls,
			&o.TodayLLMErrors, &o.TodayInputTokens, &o.TodayOutputTokens, &o.TodayCacheReadTokens, &o.KBDocuments)
	return
}

// ── 消息图片 ──

type MessageImage struct {
	ID     int64  `json:"id"`
	MIME   string `json:"-"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Data   []byte `json:"-"`
}

// AddMessageImages 记下一条消息附带的图片（已缩放），ord 按传入顺序。
func (s *Store) AddMessageImages(ctx context.Context, msgID int64, imgs []MessageImage) error {
	for i, im := range imgs {
		if _, err := s.pool.Exec(ctx, `INSERT INTO message_images (message_id, ord, mime, width, height, data) VALUES ($1, $2, $3, $4, $5, $6)`,
			msgID, i, im.MIME, im.Width, im.Height, im.Data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MessageImage(ctx context.Context, id int64) (MessageImage, error) {
	var im MessageImage
	err := s.pool.QueryRow(ctx, `SELECT id, mime, width, height, data FROM message_images WHERE id = $1`, id).
		Scan(&im.ID, &im.MIME, &im.Width, &im.Height, &im.Data)
	return im, notFound(err)
}

// attachImages 给一批消息挂上图片元数据（不含字节，后台按 ID 另取）。
func (s *Store) attachImages(ctx context.Context, list []Message) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, len(list))
	pos := map[int64]int{}
	for i, m := range list {
		ids[i], pos[m.ID] = m.ID, i
	}
	rows, err := s.pool.Query(ctx, `SELECT id, message_id, width, height FROM message_images WHERE message_id = ANY($1) ORDER BY message_id, ord`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var im MessageImage
		var msgID int64
		if err := rows.Scan(&im.ID, &msgID, &im.Width, &im.Height); err != nil {
			return err
		}
		list[pos[msgID]].Images = append(list[pos[msgID]].Images, im)
	}
	return rows.Err()
}

// PruneMessageImages 删除早于 before 的图片（消息本身保留「[图片]」占位）。
func (s *Store) PruneMessageImages(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM message_images WHERE created_at < $1`, before)
	return tag.RowsAffected(), err
}
