package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type KBDocument struct {
	ID int64 `json:"id"`
	// Path 所在目录，「/」分隔的多级路径，空串 = 根目录
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	Content    string    `json:"content,omitempty"`
	Enabled    bool      `json:"enabled"`
	ChunkCount int       `json:"chunkCount"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type KBChunk struct {
	ID         int64
	DocumentID int64
	DocPath    string
	DocTitle   string
	Heading    string
	Content    string
	// ReflectionID 非 0 表示这是一条反思沉淀条目（不属于任何文档）
	ReflectionID int64
	// Vec 当前嵌入模型生成的向量；还没向量化（或模型已更换）时为 nil
	Vec []float32
}

// ReflectionDocTitle 反思沉淀条目在检索结果里显示的来源名。
const ReflectionDocTitle = "人工沉淀"

type ChunkInput struct {
	Heading string
	Content string
}

func (s *Store) ListKBDocuments(ctx context.Context) ([]KBDocument, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id, d.path, d.title, d.enabled, d.created_at, d.updated_at,
			(SELECT count(*) FROM kb_chunks c WHERE c.document_id = d.id)
		FROM kb_documents d ORDER BY d.path, d.title, d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []KBDocument{}
	for rows.Next() {
		var d KBDocument
		if err := rows.Scan(&d.ID, &d.Path, &d.Title, &d.Enabled, &d.CreatedAt, &d.UpdatedAt, &d.ChunkCount); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *Store) KBDocument(ctx context.Context, id int64) (KBDocument, error) {
	var d KBDocument
	err := s.pool.QueryRow(ctx, `SELECT id, path, title, content, enabled, created_at, updated_at FROM kb_documents WHERE id = $1`, id).
		Scan(&d.ID, &d.Path, &d.Title, &d.Content, &d.Enabled, &d.CreatedAt, &d.UpdatedAt)
	return d, notFound(err)
}

// SaveKBDocument 新建（id=0）或覆盖文档，并在同一事务里整篇重切块。
func (s *Store) SaveKBDocument(ctx context.Context, d *KBDocument, chunks []ChunkInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return saveKBDocument(ctx, tx, d, chunks)
	})
}

func saveKBDocument(ctx context.Context, tx pgx.Tx, d *KBDocument, chunks []ChunkInput) error {
	if d.ID == 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO kb_documents (path, title, content, enabled) VALUES ($1, $2, $3, $4)
			RETURNING id, created_at, updated_at`, d.Path, d.Title, d.Content, d.Enabled).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return err
		}
	} else {
		if err := tx.QueryRow(ctx, `UPDATE kb_documents SET path = $2, title = $3, content = $4, enabled = $5, updated_at = now()
			WHERE id = $1 RETURNING created_at, updated_at`, d.ID, d.Path, d.Title, d.Content, d.Enabled).Scan(&d.CreatedAt, &d.UpdatedAt); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM kb_chunks WHERE document_id = $1`, d.ID); err != nil {
			return err
		}
	}
	rows := make([][]any, len(chunks))
	for i, c := range chunks {
		rows[i] = []any{d.ID, i, c.Heading, c.Content}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"kb_chunks"}, []string{"document_id", "ord", "heading", "content"}, pgx.CopyFromRows(rows))
	d.ChunkCount = len(chunks)
	return err
}

func (s *Store) DeleteKBDocument(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM kb_documents WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// EnabledKBChunks 全量读取启用文档的块 + 已入库（approved）的反思沉淀条目，供内存检索索引重建。
// 只带回由 model 生成的向量；model 为空（未启用向量检索）时一律不带。
func (s *Store) EnabledKBChunks(ctx context.Context, model string) ([]KBChunk, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.document_id, d.path, d.title, c.heading, c.content, 0::bigint,
			CASE WHEN $2::text <> '' AND c.embed_model = $2::text THEN c.embedding END
		FROM kb_chunks c JOIN kb_documents d ON d.id = c.document_id
		WHERE d.enabled
		UNION ALL
		SELECT 0, 0, '', $1, '', r.faq, r.id, CASE WHEN $2::text <> '' AND r.embed_model = $2::text THEN r.embedding END
		FROM kb_reflections r WHERE r.status = 'approved'
		ORDER BY 2, 1, 7`, ReflectionDocTitle, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []KBChunk{}
	for rows.Next() {
		var c KBChunk
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.DocPath, &c.DocTitle, &c.Heading, &c.Content, &c.ReflectionID, &c.Vec); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ── 目录 ──

// CleanKBPath 规范化目录路径：统一「/」分隔，去掉每段首尾空白与空段。
func CleanKBPath(p string) string {
	parts := strings.FieldsFunc(strings.ReplaceAll(p, "\\", "/"), func(r rune) bool { return r == '/' })
	out := parts[:0]
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

// RenameKBFolder 把目录 from（含全部子目录）整体改到 to 下，返回受影响的文档数。
// to 为空串表示移到根目录。
func (s *Store) RenameKBFolder(ctx context.Context, from, to string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE kb_documents
		SET path = CASE WHEN path = $1::text THEN $2::text ELSE ltrim($2::text || substr(path, length($1::text) + 1), '/') END,
			updated_at = now()
		WHERE path = $1::text OR starts_with(path, $1::text || '/')`, from, to)
	return tag.RowsAffected(), err
}

// ── 批量导入 ──

type ImportDoc struct {
	Path    string
	Title   string
	Content string
	Chunks  []ChunkInput
}

type ImportResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

// ImportKBDocuments 一个事务里批量导入。同目录同标题的文档视为同一篇：overwrite 时覆盖正文（保留启用状态），否则跳过。
func (s *Store) ImportKBDocuments(ctx context.Context, docs []ImportDoc, overwrite bool) (ImportResult, error) {
	var res ImportResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		res = ImportResult{}
		for _, in := range docs {
			d := KBDocument{Path: in.Path, Title: in.Title, Content: in.Content, Enabled: true}
			err := tx.QueryRow(ctx, `SELECT id, enabled FROM kb_documents WHERE path = $1 AND title = $2 ORDER BY id LIMIT 1`,
				in.Path, in.Title).Scan(&d.ID, &d.Enabled)
			switch {
			case err == nil && !overwrite:
				res.Skipped++
				continue
			case err == nil:
				res.Updated++
			case err == pgx.ErrNoRows:
				res.Created++
			default:
				return err
			}
			if err := saveKBDocument(ctx, tx, &d, in.Chunks); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// ── 向量化 ──

// EmbedItem 一条待向量化的文本：知识块或反思沉淀条目。
type EmbedItem struct {
	ID         int64
	Reflection bool
	Text       string
}

// PendingEmbeddings 取还没有 model 向量的启用块与已入库沉淀条目。块的向量化文本 = 文档标题 + 小节标题 + 正文。
func (s *Store) PendingEmbeddings(ctx context.Context, model string, limit int) ([]EmbedItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT false, c.id, concat_ws(E'\n', d.title, NULLIF(c.heading, ''), c.content)
		FROM kb_chunks c JOIN kb_documents d ON d.id = c.document_id
		WHERE d.enabled AND (c.embedding IS NULL OR c.embed_model <> $1)
		UNION ALL
		SELECT true, r.id, r.faq FROM kb_reflections r
		WHERE r.status = 'approved' AND (r.embedding IS NULL OR r.embed_model <> $1)
		LIMIT $2`, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []EmbedItem{}
	for rows.Next() {
		var it EmbedItem
		if err := rows.Scan(&it.Reflection, &it.ID, &it.Text); err != nil {
			return nil, err
		}
		list = append(list, it)
	}
	return list, rows.Err()
}

// SaveEmbeddings 回填向量。期间被删除 / 重切的块按 id 匹配不上，自然跳过。
func (s *Store) SaveEmbeddings(ctx context.Context, model string, items []EmbedItem, vecs [][]float32) error {
	b := &pgx.Batch{}
	for i, it := range items {
		table := "kb_chunks"
		if it.Reflection {
			table = "kb_reflections"
		}
		b.Queue(`UPDATE `+table+` SET embedding = $2, embed_model = $3 WHERE id = $1`, it.ID, vecs[i], model)
	}
	return s.pool.SendBatch(ctx, b).Close()
}

// EmbedProgress 参与检索的条目总数与其中已有 model 向量的条数。
func (s *Store) EmbedProgress(ctx context.Context, model string) (total, done int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE embed_model = $1 AND embedding IS NOT NULL) FROM (
			SELECT c.embedding, c.embed_model FROM kb_chunks c JOIN kb_documents d ON d.id = c.document_id WHERE d.enabled
			UNION ALL
			SELECT embedding, embed_model FROM kb_reflections WHERE status = 'approved') t`, model).Scan(&total, &done)
	return
}
