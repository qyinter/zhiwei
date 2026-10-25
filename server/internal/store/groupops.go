package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GroupMessage 群消息时间线的一行（见迁移 20261023162000）。
type GroupMessage struct {
	ID            int64     `json:"id"`
	Channel       string    `json:"channel"`
	ChatType      string    `json:"chatType"`
	ChatID        string    `json:"chatId"`
	UserID        string    `json:"userId"`
	UserName      string    `json:"userName"`
	Role          string    `json:"role"`
	Content       string    `json:"content"`
	PlatformMsgID string    `json:"-"`
	Mentioned     bool      `json:"mentioned"`
	CreatedAt     time.Time `json:"createdAt"`
}

// IsStaff 群主 / 管理员的发言视为客服答复。
func (m GroupMessage) IsStaff() bool {
	return m.Role == "owner" || m.Role == "admin"
}

type ChatRef struct {
	Channel  string `json:"channel"`
	ChatType string `json:"chatType"`
	ChatID   string `json:"chatId"`
}

// Key channel:chatId，与群白名单、游标的写法一致。
func (c ChatRef) Key() string { return c.Channel + ":" + c.ChatID }

const groupMsgCols = `id, channel, chat_type, chat_id, user_id, user_name, role, content, platform_msg_id, mentioned, created_at`

func scanGroupMessages(rows pgx.Rows) ([]GroupMessage, error) {
	defer rows.Close()
	list := []GroupMessage{}
	for rows.Next() {
		var m GroupMessage
		if err := rows.Scan(&m.ID, &m.Channel, &m.ChatType, &m.ChatID, &m.UserID, &m.UserName, &m.Role, &m.Content,
			&m.PlatformMsgID, &m.Mentioned, &m.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// AddGroupMessage 写入群时间线。同一平台消息重复投递只合并 mentioned：QQ 全量模式下
// 一条 @ 消息会以 GROUP_MESSAGE_CREATE 与 GROUP_AT_MESSAGE_CREATE 各推一次，前者可能识别不出 @。
func (s *Store) AddGroupMessage(ctx context.Context, m GroupMessage) error {
	if m.ChatType == "" {
		m.ChatType = "group"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO group_messages (channel, chat_type, chat_id, user_id, user_name, role, content, platform_msg_id, mentioned)
		VALUES ($1, $9, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (channel, chat_id, platform_msg_id) WHERE platform_msg_id <> ''
		DO UPDATE SET mentioned = group_messages.mentioned OR EXCLUDED.mentioned`,
		m.Channel, m.ChatID, m.UserID, m.UserName, m.Role, m.Content, m.PlatformMsgID, m.Mentioned, m.ChatType)
	return err
}

// GroupChats 时间线里出现过的全部群（扫描器按群推进）。
func (s *Store) GroupChats(ctx context.Context) ([]ChatRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT channel, chat_type, chat_id FROM group_messages ORDER BY channel, chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ChatRef{}
	for rows.Next() {
		var c ChatRef
		if err := rows.Scan(&c.Channel, &c.ChatType, &c.ChatID); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// UnansweredCandidates 主动回复候选：(from, until] 内普通群友未 @ 机器人的消息，按时间正序。
func (s *Store) UnansweredCandidates(ctx context.Context, c ChatRef, from, until time.Time) ([]GroupMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+groupMsgCols+` FROM group_messages
		WHERE channel = $1 AND chat_id = $2 AND created_at > $3 AND created_at <= $4
			AND role NOT IN ('owner', 'admin') AND NOT mentioned
		ORDER BY created_at, id`, c.Channel, c.ChatID, from, until)
	if err != nil {
		return nil, err
	}
	return scanGroupMessages(rows)
}

// HasStaffMessageBetween (from, until] 内群主 / 管理员是否发过言。
func (s *Store) HasStaffMessageBetween(ctx context.Context, c ChatRef, from, until time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_messages
		WHERE channel = $1 AND chat_id = $2 AND created_at > $3 AND created_at <= $4 AND role IN ('owner', 'admin'))`,
		c.Channel, c.ChatID, from, until).Scan(&ok)
	return ok, err
}

// GroupWindow 反思窗口：cursor 之前的 pre 条作上下文 + (cursor, until] 内最多 max 条，按时间正序。
func (s *Store) GroupWindow(ctx context.Context, c ChatRef, cursor, until time.Time, pre, max int) ([]GroupMessage, error) {
	rows, err := s.pool.Query(ctx, `(SELECT `+groupMsgCols+` FROM (SELECT `+groupMsgCols+` FROM group_messages
			WHERE channel = $1 AND chat_id = $2 AND created_at <= $3 ORDER BY created_at DESC, id DESC LIMIT $5) pre)
		UNION ALL
		(SELECT `+groupMsgCols+` FROM group_messages
			WHERE channel = $1 AND chat_id = $2 AND created_at > $3 AND created_at <= $4 ORDER BY created_at, id LIMIT $6)
		ORDER BY created_at, id`, c.Channel, c.ChatID, cursor, until, pre, max)
	if err != nil {
		return nil, err
	}
	return scanGroupMessages(rows)
}

// ChatFloor 某个群里晚于 After 的时间线还有用（问题排行尚未归类到那里），清理时保留。
type ChatFloor struct {
	Chat  string // channel:chatId
	After time.Time
}

// PruneGroupMessages 删除早于 before 的群时间线，keep 里各群晚于 After 的除外。
func (s *Store) PruneGroupMessages(ctx context.Context, before time.Time, keep []ChatFloor) (int64, error) {
	chats, afters := make([]string, len(keep)), make([]time.Time, len(keep))
	for i, f := range keep {
		chats[i], afters[i] = f.Chat, f.After
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM group_messages g WHERE g.created_at < $1
		AND NOT EXISTS (SELECT 1 FROM unnest($2::text[], $3::timestamptz[]) AS f(chat, after)
			WHERE f.chat = g.channel || ':' || g.chat_id AND g.created_at > f.after)`, before, chats, afters)
	return tag.RowsAffected(), err
}

type GroupChatStat struct {
	ChatRef
	Title    string    `json:"title"`
	Messages int       `json:"messages"`
	LastAt   time.Time `json:"lastAt"`
}

// GroupChatStats 每个群的时间线条数、最近消息时间与群名（取自会话表）。
func (s *Store) GroupChatStats(ctx context.Context) ([]GroupChatStat, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.channel, min(g.chat_type), g.chat_id, count(*), max(g.created_at),
			COALESCE((SELECT c.title FROM conversations c WHERE c.channel = g.channel AND c.chat_id = g.chat_id AND c.title <> ''
				ORDER BY c.last_message_at DESC LIMIT 1), '')
		FROM group_messages g GROUP BY g.channel, g.chat_id ORDER BY max(g.created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []GroupChatStat{}
	for rows.Next() {
		var st GroupChatStat
		if err := rows.Scan(&st.Channel, &st.ChatType, &st.ChatID, &st.Messages, &st.LastAt, &st.Title); err != nil {
			return nil, err
		}
		list = append(list, st)
	}
	return list, rows.Err()
}

// KnownUserName 同一渠道下某用户最近一次出现的昵称（入群欢迎拿不到昵称时兜底）。
func (s *Store) KnownUserName(ctx context.Context, channel, userID string) string {
	var name string
	_ = s.pool.QueryRow(ctx, `SELECT user_name FROM (
			SELECT user_name, last_message_at AS at FROM conversations WHERE channel = $1 AND user_id = $2 AND user_name <> ''
			UNION ALL
			SELECT user_name, created_at FROM group_messages WHERE channel = $1 AND user_id = $2 AND user_name <> ''
		) t ORDER BY at DESC LIMIT 1`, channel, userID).Scan(&name)
	return name
}

// ── 扫描游标 ──

// Cursor 读游标；不存在返回 ok=false。
func (s *Store) Cursor(ctx context.Context, key string) (at time.Time, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT at FROM poller_cursors WHERE key = $1`, key).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return at, err == nil, err
}

func (s *Store) SetCursor(ctx context.Context, key string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO poller_cursors (key, at) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET at = EXCLUDED.at, updated_at = now()`, key, at)
	return err
}

// CursorsByPrefix key → (游标时间, 最近一次推进时间)。
func (s *Store) CursorsByPrefix(ctx context.Context, prefix string) (map[string][2]time.Time, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, at, updated_at FROM poller_cursors WHERE starts_with(key, $1)`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]time.Time{}
	for rows.Next() {
		var k string
		var at, upd time.Time
		if err := rows.Scan(&k, &at, &upd); err != nil {
			return nil, err
		}
		out[k] = [2]time.Time{at, upd}
	}
	return out, rows.Err()
}

// ── 主动回复 ──

type ProactiveReply struct {
	ID        int64     `json:"id"`
	Channel   string    `json:"channel"`
	ChatID    string    `json:"chatId"`
	UserID    string    `json:"userId"`
	UserName  string    `json:"userName"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Quality   string    `json:"quality"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) AddProactiveReply(ctx context.Context, r ProactiveReply) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO proactive_replies (channel, chat_id, user_id, user_name, question, answer)
		VALUES ($1, $2, $3, $4, $5, $6)`, r.Channel, r.ChatID, r.UserID, r.UserName, r.Question, r.Answer)
	return err
}

func (s *Store) ListProactiveReplies(ctx context.Context, limit int) ([]ProactiveReply, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, channel, chat_id, user_id, user_name, question, answer, quality, created_at
		FROM proactive_replies ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ProactiveReply{}
	for rows.Next() {
		var r ProactiveReply
		if err := rows.Scan(&r.ID, &r.Channel, &r.ChatID, &r.UserID, &r.UserName, &r.Question, &r.Answer, &r.Quality, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// ProactiveCounts 每个群的主动回复次数，key = channel:chatId。
func (s *Store) ProactiveCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel || ':' || chat_id, count(*) FROM proactive_replies GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func (s *Store) SetProactiveQuality(ctx context.Context, id int64, quality string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE proactive_replies SET quality = $2 WHERE id = $1`, id, quality)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ConversationByKey 按会话键查会话（不存在返回 ErrNotFound）。
func (s *Store) ConversationByKey(ctx context.Context, channel, chatID, userID string) (Conversation, error) {
	var c Conversation
	err := scanConv(s.pool.QueryRow(ctx, `SELECT `+convCols+` FROM conversations
		WHERE channel = $1 AND chat_id = $2 AND user_id = $3`, channel, chatID, userID), &c)
	return c, notFound(err)
}

// RepliedSince 会话在 since 之后是否有过机器人 / 人工回复（主动回复据此判断主流程已答过）。
func (s *Store) RepliedSince(ctx context.Context, convID int64, since time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages
		WHERE conversation_id = $1 AND direction IN ('out', 'human') AND created_at > $2)`, convID, since).Scan(&ok)
	return ok, err
}

// PruneProactive 清理早于 before 的主动回复记录。
func (s *Store) PruneProactive(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM proactive_replies WHERE created_at < $1`, before)
	return err
}

// ── 入群申请 ──

type JoinRequestRecord struct {
	ID          int64     `json:"id"`
	Channel     string    `json:"channel"`
	ChatID      string    `json:"chatId"`
	ChatTitle   string    `json:"chatTitle"`
	UserID      string    `json:"userId"`
	UserName    string    `json:"userName"`
	Answer      string    `json:"answer"`
	ExtractedID string    `json:"extractedId"`
	Decision    string    `json:"decision"`
	Detail      string    `json:"detail"`
	CreatedAt   time.Time `json:"createdAt"`
}

const (
	JoinApproved = "approved"
	JoinDeclined = "declined"
	JoinPending  = "pending"
	JoinAuto     = "auto"
	JoinFailed   = "failed"
)

func (s *Store) AddJoinRequest(ctx context.Context, r JoinRequestRecord) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO join_requests (channel, chat_id, user_id, user_name, answer, extracted_id, decision, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.Channel, r.ChatID, r.UserID, r.UserName, r.Answer, r.ExtractedID, r.Decision, r.Detail)
	return err
}

func (s *Store) ListJoinRequests(ctx context.Context, page, size int) ([]JoinRequestRecord, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM join_requests`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT j.id, j.channel, j.chat_id,
			COALESCE((SELECT c.title FROM conversations c WHERE c.channel = j.channel AND c.chat_id = j.chat_id AND c.title <> ''
				ORDER BY c.last_message_at DESC LIMIT 1), ''),
			j.user_id, j.user_name, j.answer, j.extracted_id, j.decision, j.detail, j.created_at
		FROM join_requests j ORDER BY j.created_at DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []JoinRequestRecord{}
	for rows.Next() {
		var r JoinRequestRecord
		if err := rows.Scan(&r.ID, &r.Channel, &r.ChatID, &r.ChatTitle, &r.UserID, &r.UserName, &r.Answer,
			&r.ExtractedID, &r.Decision, &r.Detail, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		list = append(list, r)
	}
	return list, total, rows.Err()
}

// ChatUser 某群里的一个已知用户。
type ChatUser struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
}

// LatestChatUser 群里最近发过言的用户（取自会话表）；没有返回 ErrNotFound。
func (s *Store) LatestChatUser(ctx context.Context, channel, chatID string) (ChatUser, error) {
	var u ChatUser
	err := s.pool.QueryRow(ctx, `SELECT user_id, user_name FROM conversations
		WHERE channel = $1 AND chat_id = $2 AND chat_type = 'group' ORDER BY last_message_at DESC LIMIT 1`, channel, chatID).
		Scan(&u.UserID, &u.UserName)
	return u, notFound(err)
}

type GroupChat struct {
	Channel string    `json:"channel"`
	ChatID  string    `json:"chatId"`
	Title   string    `json:"title"`
	LastAt  time.Time `json:"lastAt"`
}

// FillGroupTitle 给还没有群名的群会话补上群名（后台查到群名时顺手写回，会话列表也能显示）。
func (s *Store) FillGroupTitle(ctx context.Context, channel, chatID, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET title = $3
		WHERE channel = $1 AND chat_id = $2 AND chat_type = 'group' AND title = ''`, channel, chatID, title)
	return err
}

// KnownGroups 出现过会话的群（后台选择测试目标用）。
func (s *Store) KnownGroups(ctx context.Context) ([]GroupChat, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel, chat_id, max(title), max(last_message_at) FROM conversations
		WHERE chat_type = 'group' GROUP BY channel, chat_id ORDER BY max(last_message_at) DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []GroupChat{}
	for rows.Next() {
		var g GroupChat
		if err := rows.Scan(&g.Channel, &g.ChatID, &g.Title, &g.LastAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}
