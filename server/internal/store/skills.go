package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrSkillExists 技能重名（name 唯一约束）。
var ErrSkillExists = errors.New("skill name exists")

type Skill struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Content     string      `json:"content,omitempty"`
	Enabled     bool        `json:"enabled"`
	UseCount    int         `json:"useCount"`
	LastUsedAt  *time.Time  `json:"lastUsedAt"`
	FileCount   int         `json:"fileCount"`
	Size        int         `json:"size"` // SKILL.md + 附属文件字节数
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	Files       []SkillFile `json:"files,omitempty"`
}

type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

const skillCols = `s.id, s.name, s.description, s.enabled, s.use_count, s.last_used_at, s.created_at, s.updated_at,
	(SELECT count(*) FROM skill_files f WHERE f.skill_id = s.id),
	octet_length(s.content) + COALESCE((SELECT sum(octet_length(f.content)) FROM skill_files f WHERE f.skill_id = s.id), 0)`

func scanSkill(row pgx.Row, k *Skill) error {
	return row.Scan(&k.ID, &k.Name, &k.Description, &k.Enabled, &k.UseCount, &k.LastUsedAt, &k.CreatedAt, &k.UpdatedAt, &k.FileCount, &k.Size)
}

func (s *Store) ListSkills(ctx context.Context) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+skillCols+` FROM skills s ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Skill{}
	for rows.Next() {
		var k Skill
		if err := scanSkill(rows, &k); err != nil {
			return nil, err
		}
		list = append(list, k)
	}
	return list, rows.Err()
}

// Skill 单个技能（含 SKILL.md 原文与附属文件）。
func (s *Store) Skill(ctx context.Context, id int64) (Skill, error) {
	var k Skill
	if err := scanSkill(s.pool.QueryRow(ctx, `SELECT `+skillCols+` FROM skills s WHERE s.id = $1`, id), &k); err != nil {
		return k, notFound(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT content FROM skills WHERE id = $1`, id).Scan(&k.Content); err != nil {
		return k, notFound(err)
	}
	files, err := s.skillFiles(ctx, []int64{id})
	k.Files = files[id]
	if k.Files == nil {
		k.Files = []SkillFile{}
	}
	return k, err
}

// EnabledSkills 全部启用技能及其附属文件，供运行时装进客服大脑。
func (s *Store) EnabledSkills(ctx context.Context) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, description, content FROM skills WHERE enabled ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var (
		list []Skill
		ids  []int64
	)
	for rows.Next() {
		var k Skill
		if err := rows.Scan(&k.ID, &k.Name, &k.Description, &k.Content); err != nil {
			return nil, err
		}
		list = append(list, k)
		ids = append(ids, k.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files, err := s.skillFiles(ctx, ids)
	for i := range list {
		list[i].Files = files[list[i].ID]
	}
	return list, err
}

func (s *Store) skillFiles(ctx context.Context, ids []int64) (map[int64][]SkillFile, error) {
	out := map[int64][]SkillFile{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT skill_id, path, content FROM skill_files WHERE skill_id = ANY($1) ORDER BY skill_id, path`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			f  SkillFile
		)
		if err := rows.Scan(&id, &f.Path, &f.Content); err != nil {
			return nil, err
		}
		out[id] = append(out[id], f)
	}
	return out, rows.Err()
}

// SaveSkill 新建（ID=0）或更新 SKILL.md；files 非 nil 时整体替换附属文件。重名返回 ErrSkillExists。
func (s *Store) SaveSkill(ctx context.Context, k *Skill, files []SkillFile) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return saveSkill(ctx, tx, k, files)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrSkillExists
	}
	return err
}

func saveSkill(ctx context.Context, tx pgx.Tx, k *Skill, files []SkillFile) error {
	if k.ID == 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO skills (name, description, content, enabled) VALUES ($1, $2, $3, $4)
			RETURNING id, created_at, updated_at`, k.Name, k.Description, k.Content, k.Enabled).Scan(&k.ID, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return err
		}
	} else if err := tx.QueryRow(ctx, `UPDATE skills SET name = $2, description = $3, content = $4, enabled = $5, updated_at = now()
		WHERE id = $1 RETURNING created_at, updated_at`, k.ID, k.Name, k.Description, k.Content, k.Enabled).Scan(&k.CreatedAt, &k.UpdatedAt); err != nil {
		return notFound(err)
	}
	if files == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM skill_files WHERE skill_id = $1`, k.ID); err != nil {
		return err
	}
	rows := make([][]any, len(files))
	for i, f := range files {
		rows[i] = []any{k.ID, f.Path, f.Content}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"skill_files"}, []string{"skill_id", "path", "content"}, pgx.CopyFromRows(rows))
	return err
}

func (s *Store) SetSkillEnabled(ctx context.Context, id int64, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE skills SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteSkill(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM skills WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// TouchSkill 记一次模型读取（read_skill）。
func (s *Store) TouchSkill(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE skills SET use_count = use_count + 1, last_used_at = now() WHERE id = $1`, id)
	return err
}

type ImportSkill struct {
	Name, Description, Content string
	Files                      []SkillFile
}

// ImportSkills 一个事务里批量导入，按 name 匹配：overwrite 时替换正文与附属文件（保留启用状态与使用统计），否则跳过。
func (s *Store) ImportSkills(ctx context.Context, list []ImportSkill, overwrite bool) (ImportResult, error) {
	var res ImportResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		res = ImportResult{}
		for _, in := range list {
			k := Skill{Name: in.Name, Description: in.Description, Content: in.Content, Enabled: true}
			err := tx.QueryRow(ctx, `SELECT id, enabled FROM skills WHERE name = $1`, in.Name).Scan(&k.ID, &k.Enabled)
			switch {
			case err == nil && !overwrite:
				res.Skipped++
				continue
			case err == nil:
				res.Updated++
			case errors.Is(err, pgx.ErrNoRows):
				res.Created++
			default:
				return err
			}
			files := in.Files
			if files == nil {
				files = []SkillFile{}
			}
			if err := saveSkill(ctx, tx, &k, files); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}
