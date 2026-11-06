package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── 群成员入群时间 ──

// TouchGroupMember 记录一次入群（重新入群以最新时间为准）。
func (s *Store) TouchGroupMember(ctx context.Context, channel, chatID, userID, userName string, joinedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO group_members (channel, chat_id, user_id, user_name, joined_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (channel, chat_id, user_id) DO UPDATE SET joined_at = EXCLUDED.joined_at,
			user_name = CASE WHEN EXCLUDED.user_name <> '' THEN EXCLUDED.user_name ELSE group_members.user_name END`,
		channel, chatID, userID, userName, joinedAt)
	return err
}

// MemberJoinedAt 某人在某群的入群时间；没有记录（机器人接入前就在群里）返回 ok=false。
func (s *Store) MemberJoinedAt(ctx context.Context, channel, chatID, userID string) (at time.Time, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT joined_at FROM group_members WHERE channel = $1 AND chat_id = $2 AND user_id = $3`,
		channel, chatID, userID).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return at, err == nil, err
}

// ── 处置记录 ──

type ModerationAction struct {
	ID         int64      `json:"id"`
	Channel    string     `json:"channel"`
	ChatID     string     `json:"chatId"`
	ChatTitle  string     `json:"chatTitle"`
	UserID     string     `json:"userId"`
	UserName   string     `json:"userName"`
	MessageID  string     `json:"messageId"`
	Content    string     `json:"content"`
	Source     string     `json:"source"`
	Reason     string     `json:"reason"`
	Confidence float32    `json:"confidence"`
	Action     string     `json:"action"`
	Detail     string     `json:"detail"`
	RevertedAt *time.Time `json:"revertedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

const (
	ModSourceRule = "rule"
	ModSourceLLM  = "llm"

	ModActionNone   = "none"
	ModActionDelete = "delete"
	ModActionMute   = "mute"
	ModActionBan    = "ban"
)

const modCols = `id, channel, chat_id, chat_title, user_id, user_name, message_id, content, source, reason, confidence, action, detail, reverted_at, created_at`

func scanModeration(row pgx.Row) (ModerationAction, error) {
	var m ModerationAction
	err := row.Scan(&m.ID, &m.Channel, &m.ChatID, &m.ChatTitle, &m.UserID, &m.UserName, &m.MessageID, &m.Content,
		&m.Source, &m.Reason, &m.Confidence, &m.Action, &m.Detail, &m.RevertedAt, &m.CreatedAt)
	return m, err
}

func (s *Store) AddModerationAction(ctx context.Context, m *ModerationAction) error {
	return s.pool.QueryRow(ctx, `INSERT INTO moderation_actions
		(channel, chat_id, chat_title, user_id, user_name, message_id, content, source, reason, confidence, action, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id, created_at`,
		m.Channel, m.ChatID, m.ChatTitle, m.UserID, m.UserName, m.MessageID, m.Content, m.Source, m.Reason, m.Confidence, m.Action, m.Detail,
	).Scan(&m.ID, &m.CreatedAt)
}

// SetModerationDetail 回写执行结果（处置在后台异步执行，记录先落库）。
func (s *Store) SetModerationDetail(ctx context.Context, id int64, action, detail string) error {
	_, err := s.pool.Exec(ctx, `UPDATE moderation_actions SET action = $2, detail = $3 WHERE id = $1`, id, action, detail)
	return err
}

func (s *Store) ModerationAction(ctx context.Context, id int64) (ModerationAction, error) {
	m, err := scanModeration(s.pool.QueryRow(ctx, `SELECT `+modCols+` FROM moderation_actions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Store) ListModerationActions(ctx context.Context, page, size int) ([]ModerationAction, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM moderation_actions`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+modCols+` FROM moderation_actions ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []ModerationAction{}
	for rows.Next() {
		m, err := scanModeration(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, m)
	}
	return list, total, rows.Err()
}

// RevertModerationAction 标记误判恢复；已恢复的不重复标记（返回 false）。
func (s *Store) RevertModerationAction(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE moderation_actions SET reverted_at = now() WHERE id = $1 AND reverted_at IS NULL`, id)
	return tag.RowsAffected() > 0, err
}

// ModerationStrikes 某人在某群近期（since 之后）未被恢复的实际处置次数（仅记录的不算），用于累计升级封禁。
func (s *Store) ModerationStrikes(ctx context.Context, channel, chatID, userID string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM moderation_actions
		WHERE channel = $1 AND chat_id = $2 AND user_id = $3 AND created_at > $4 AND reverted_at IS NULL AND action <> 'none'`,
		channel, chatID, userID, since).Scan(&n)
	return n, err
}

// ModerationCounts 各群的处置总数（后台群管理 / 治理页概览用）。
func (s *Store) ModerationCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel, chat_id, count(*) FROM moderation_actions WHERE action <> 'none' GROUP BY channel, chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ch, id string
		var n int
		if err := rows.Scan(&ch, &id, &n); err != nil {
			return nil, err
		}
		out[ch+":"+id] = n
	}
	return out, rows.Err()
}

// PruneModeration 清理早于 before 的处置记录。
func (s *Store) PruneModeration(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM moderation_actions WHERE created_at < $1`, before)
	return err
}

// DeleteGroupMessage 把被判定为广告的消息从群时间线删掉（模型层异步判定时消息已经写入）。
func (s *Store) DeleteGroupMessage(ctx context.Context, channel, chatID, platformMsgID string) error {
	if platformMsgID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM group_messages WHERE channel = $1 AND chat_id = $2 AND platform_msg_id = $3`, channel, chatID, platformMsgID)
	return err
}
