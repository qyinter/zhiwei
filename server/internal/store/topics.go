package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── 问题排行 ──

// TopicSource 一条待归类的客户消息（来自群时间线或私聊消息）。
type TopicSource struct {
	ID       int64
	Channel  string
	ChatType string
	ChatID   string
	UserID   string
	UserName string
	Text     string
	At       time.Time
}

// GroupTopicSources 群时间线 (after, until] 内普通群友的发言（群主 / 管理员的是答复，不算提问），按时间正序最多 limit 条。
func (s *Store) GroupTopicSources(ctx context.Context, c ChatRef, after, until time.Time, limit int) ([]TopicSource, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, channel, chat_type, chat_id, user_id, user_name, content, created_at
		FROM group_messages
		WHERE channel = $1 AND chat_id = $2 AND created_at > $3 AND created_at <= $4 AND role NOT IN ('owner', 'admin')
		ORDER BY created_at, id LIMIT $5`, c.Channel, c.ChatID, after, until, limit)
	if err != nil {
		return nil, err
	}
	return collectTopicSources(rows)
}

// PrivateTopicSources 私聊（QQ 单聊、TG 私聊、频道私信）里客户发来的消息。群里 @ 机器人的消息已在群时间线里，不重复取。
func (s *Store) PrivateTopicSources(ctx context.Context, after, until time.Time, limit int) ([]TopicSource, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id, c.channel, c.chat_type, c.chat_id, c.user_id, c.user_name, m.content, m.created_at
		FROM messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE m.direction = 'in' AND c.chat_type IN ('private', 'guild_dm') AND c.channel <> 'playground'
			AND m.created_at > $1 AND m.created_at <= $2
		ORDER BY m.created_at, m.id LIMIT $3`, after, until, limit)
	if err != nil {
		return nil, err
	}
	return collectTopicSources(rows)
}

func collectTopicSources(rows pgx.Rows) ([]TopicSource, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TopicSource, error) {
		var t TopicSource
		err := r.Scan(&t.ID, &t.Channel, &t.ChatType, &t.ChatID, &t.UserID, &t.UserName, &t.Text, &t.At)
		return t, err
	})
}

type QuestionTopic struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// QuestionTopics 现有主题，最近被归入的在前（交给模型做归并参考）。
func (s *Store) QuestionTopics(ctx context.Context, limit int) ([]QuestionTopic, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, title FROM question_topics ORDER BY updated_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[QuestionTopic])
}

// TopicAssign 一条消息的归类结果：TopicID 非 0 归入已有主题，否则按 NewTitle 新建（同名复用）。
type TopicAssign struct {
	Source   TopicSource
	TopicID  int64
	NewTitle string
}

// SaveTopicBatch 新建主题 + 记出现 + 推进游标在同一事务里：中途失败整批回滚、游标不动，下一轮重跑不会重复计数。
// 返回新建主题的 ID（按 NewTitle）。
func (s *Store) SaveTopicBatch(ctx context.Context, items []TopicAssign, cursorKey string, cursorAt time.Time) (map[string]int64, error) {
	created := map[string]int64{}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, it := range items {
			id := it.TopicID
			if id == 0 {
				if prev, ok := created[it.NewTitle]; ok {
					id = prev
				} else {
					if err := tx.QueryRow(ctx, `INSERT INTO question_topics (title) VALUES ($1)
						ON CONFLICT (title) DO UPDATE SET updated_at = now() RETURNING id`, it.NewTitle).Scan(&id); err != nil {
						return err
					}
					created[it.NewTitle] = id
				}
			}
			src := it.Source
			if _, err := tx.Exec(ctx, `INSERT INTO question_occurrences (topic_id, channel, chat_type, chat_id, user_id, user_name, text, asked_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				id, src.Channel, src.ChatType, src.ChatID, src.UserID, src.UserName, src.Text, src.At); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE question_topics SET updated_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO poller_cursors (key, at) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET at = EXCLUDED.at, updated_at = now()`, cursorKey, cursorAt)
		return err
	})
	return created, err
}

type TopicRank struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Count 时间窗内被问到的次数；PrevCount 上一个等长时间窗的次数（看趋势）
	Count     int       `json:"count"`
	PrevCount int       `json:"prevCount"`
	Users     int       `json:"users"`
	Chats     int       `json:"chats"`
	LastAt    time.Time `json:"lastAt"`
	Samples   []string  `json:"samples"`
}

// TopicRanking [from, now) 内按出现次数排行，带上一个等长窗口的次数与每个主题最近 3 条不同的原话。
func (s *Store) TopicRanking(ctx context.Context, from time.Time, limit int) ([]TopicRank, error) {
	prevFrom := from.Add(-time.Since(from))
	rows, err := s.pool.Query(ctx, `SELECT t.id, t.title, count(*), count(DISTINCT o.channel || ':' || o.user_id),
			count(DISTINCT o.channel || ':' || o.chat_id), max(o.asked_at),
			(SELECT count(*) FROM question_occurrences p WHERE p.topic_id = t.id AND p.asked_at >= $2 AND p.asked_at < $1)
		FROM question_occurrences o JOIN question_topics t ON t.id = o.topic_id
		WHERE o.asked_at >= $1
		GROUP BY t.id ORDER BY count(*) DESC, max(o.asked_at) DESC LIMIT $3`, from, prevFrom, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TopicRank, error) {
		t := TopicRank{Samples: []string{}}
		err := r.Scan(&t.ID, &t.Title, &t.Count, &t.Users, &t.Chats, &t.LastAt, &t.PrevCount)
		return t, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]int64, len(list))
	pos := map[int64]int{}
	for i, t := range list {
		ids[i], pos[t.ID] = t.ID, i
	}
	rows, err = s.pool.Query(ctx, `SELECT topic_id, text FROM (
			SELECT topic_id, text, row_number() OVER (PARTITION BY topic_id ORDER BY max(asked_at) DESC) AS rn
			FROM question_occurrences WHERE asked_at >= $1 AND topic_id = ANY($2) GROUP BY topic_id, text
		) t WHERE rn <= 3 ORDER BY topic_id, rn`, from, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		list[pos[id]].Samples = append(list[pos[id]].Samples, text)
	}
	return list, rows.Err()
}

type TopicOccurrence struct {
	ID        int64     `json:"id"`
	Channel   string    `json:"channel"`
	ChatType  string    `json:"chatType"`
	ChatID    string    `json:"chatId"`
	ChatTitle string    `json:"chatTitle"`
	UserID    string    `json:"userId"`
	UserName  string    `json:"userName"`
	Text      string    `json:"text"`
	AskedAt   time.Time `json:"askedAt"`
}

// TopicOccurrences 某主题在 from 之后的原话，最近的在前。
func (s *Store) TopicOccurrences(ctx context.Context, topicID int64, from time.Time, page, size int) ([]TopicOccurrence, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM question_occurrences WHERE topic_id = $1 AND asked_at >= $2`,
		topicID, from).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.channel, o.chat_type, o.chat_id,
			COALESCE((SELECT c.title FROM conversations c WHERE c.channel = o.channel AND c.chat_id = o.chat_id AND c.title <> ''
				ORDER BY c.last_message_at DESC LIMIT 1), ''),
			o.user_id, o.user_name, o.text, o.asked_at
		FROM question_occurrences o WHERE o.topic_id = $1 AND o.asked_at >= $2
		ORDER BY o.asked_at DESC, o.id DESC LIMIT $3 OFFSET $4`, topicID, from, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TopicOccurrence])
	return list, total, err
}

// ErrTopicExists 改名撞上已有主题（应改用合并）。
var ErrTopicExists = errors.New("topic exists")

func (s *Store) RenameTopic(ctx context.Context, id int64, title string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE question_topics SET title = $2 WHERE id = $1`, id, title)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrTopicExists
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MergeTopic 把 from 的全部出现记录并入 into，并删除 from。
func (s *Store) MergeTopic(ctx context.Context, from, into int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM question_topics WHERE id = ANY($1)`, []int64{from, into}).Scan(&n); err != nil {
			return err
		}
		if n != 2 || from == into {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE question_occurrences SET topic_id = $2 WHERE topic_id = $1`, from, into); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE question_topics SET updated_at = now() WHERE id = $1`, into); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM question_topics WHERE id = $1`, from)
		return err
	})
}

func (s *Store) DeleteTopic(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM question_topics WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PruneTopics 删除早于 before 的出现记录，以及因此不再有记录、且之后没被归入过的主题。
func (s *Store) PruneTopics(ctx context.Context, before time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM question_occurrences WHERE asked_at < $1`, before); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM question_topics t WHERE t.updated_at < $1
		AND NOT EXISTS (SELECT 1 FROM question_occurrences o WHERE o.topic_id = t.id)`, before)
	return err
}
