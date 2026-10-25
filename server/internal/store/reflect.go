package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// 反思条目状态
const (
	ReflectApproved = "approved"
	ReflectRejected = "rejected"
	ReflectPromoted = "promoted"
)

type Reflection struct {
	ID            int64     `json:"id"`
	Channel       string    `json:"channel"`
	ChatID        string    `json:"chatId"`
	Question      string    `json:"question"`
	Answer        string    `json:"answer"`
	FAQ           string    `json:"faq"`
	Status        string    `json:"status"`
	Compacted     bool      `json:"compacted"`
	PromotedDocID *int64    `json:"promotedDocId"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const reflectCols = `id, channel, chat_id, question, answer, faq, status, compacted, promoted_doc_id, created_at, updated_at`

func scanReflections(rows pgx.Rows) ([]Reflection, error) {
	defer rows.Close()
	list := []Reflection{}
	for rows.Next() {
		var r Reflection
		if err := rows.Scan(&r.ID, &r.Channel, &r.ChatID, &r.Question, &r.Answer, &r.FAQ, &r.Status, &r.Compacted,
			&r.PromotedDocID, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) AddReflection(ctx context.Context, r *Reflection) error {
	return s.pool.QueryRow(ctx, `INSERT INTO kb_reflections (channel, chat_id, question, answer, faq)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, status, created_at, updated_at`,
		r.Channel, r.ChatID, r.Question, r.Answer, r.FAQ).Scan(&r.ID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
}

// ListReflections status 为空列出全部，按时间倒序。
func (s *Store) ListReflections(ctx context.Context, status string, limit int) ([]Reflection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+reflectCols+` FROM kb_reflections
		WHERE $1 = '' OR status = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return scanReflections(rows)
}

func (s *Store) Reflection(ctx context.Context, id int64) (Reflection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+reflectCols+` FROM kb_reflections WHERE id = $1`, id)
	if err != nil {
		return Reflection{}, err
	}
	list, err := scanReflections(rows)
	if err != nil {
		return Reflection{}, err
	}
	if len(list) == 0 {
		return Reflection{}, ErrNotFound
	}
	return list[0], nil
}

// SetReflectionStatus 驳回 / 恢复入库。已升格的条目不可再改状态。
func (s *Store) SetReflectionStatus(ctx context.Context, id int64, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE kb_reflections SET status = $2, updated_at = now()
		WHERE id = $1 AND status <> 'promoted'`, id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ReflectionCounts 各状态条目数。
func (s *Store) ReflectionCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM kb_reflections GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{ReflectApproved: 0, ReflectRejected: 0, ReflectPromoted: 0}
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

// ReflectionCountsByChat 每个群已入库（含已升格）的沉淀条数，key = channel:chatId。
func (s *Store) ReflectionCountsByChat(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel || ':' || chat_id, count(*) FROM kb_reflections
		WHERE chat_id <> '' AND status <> 'rejected' GROUP BY 1`)
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

// CompactReflections 整理：一个事务里删掉 oldIDs、插入合并后的条目，并记一条整理记录。
// 合并产生的条目不再有单条来源（compacted=true，来源群为空）。
func (s *Store) CompactReflections(ctx context.Context, before []Reflection, after []string) error {
	ids := make([]int64, len(before))
	beforeFAQ := make([]map[string]any, len(before))
	for i, r := range before {
		ids[i] = r.ID
		beforeFAQ[i] = map[string]any{"id": r.ID, "faq": r.FAQ}
	}
	bj, _ := json.Marshal(beforeFAQ)
	aj, _ := json.Marshal(after)
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM kb_reflections WHERE id = ANY($1) AND status = 'approved'`, ids); err != nil {
			return err
		}
		for _, faq := range after {
			if _, err := tx.Exec(ctx, `INSERT INTO kb_reflections (faq, compacted) VALUES ($1, true)`, faq); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO reflect_compactions (before_count, after_count, before_json, after_json)
			VALUES ($1, $2, $3, $4)`, len(before), len(after), bj, aj)
		return err
	})
}

type Compaction struct {
	ID          int64           `json:"id"`
	BeforeCount int             `json:"beforeCount"`
	AfterCount  int             `json:"afterCount"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

func (s *Store) ListCompactions(ctx context.Context, limit int) ([]Compaction, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, before_count, after_count, created_at FROM reflect_compactions
		ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Compaction{}
	for rows.Next() {
		var c Compaction
		if err := rows.Scan(&c.ID, &c.BeforeCount, &c.AfterCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (s *Store) Compaction(ctx context.Context, id int64) (Compaction, error) {
	var c Compaction
	err := s.pool.QueryRow(ctx, `SELECT id, before_count, after_count, before_json, after_json, created_at
		FROM reflect_compactions WHERE id = $1`, id).Scan(&c.ID, &c.BeforeCount, &c.AfterCount, &c.Before, &c.After, &c.CreatedAt)
	return c, notFound(err)
}

// PromoteReflection 升格：在同一事务里新建知识库文档（含切块）并把条目标为 promoted。
// 条目已驳回或已升格时返回 ErrNotFound。
func (s *Store) PromoteReflection(ctx context.Context, id int64, doc *KBDocument, chunks []ChunkInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM kb_reflections WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return notFound(err)
		}
		if status != ReflectApproved {
			return ErrNotFound
		}
		if err := saveKBDocument(ctx, tx, doc, chunks); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE kb_reflections SET status = 'promoted', promoted_doc_id = $2, updated_at = now() WHERE id = $1`, id, doc.ID)
		return err
	})
}
