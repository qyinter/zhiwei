package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationName = regexp.MustCompile(`^\d{14}_[a-z0-9_]+\.sql$`)

// 任意固定值：多实例同时启动时串行执行迁移。
const migrateLockKey = 0x6b61697975616e

// Migrate 执行 dir 下尚未应用的迁移。已应用文件的 checksum 变了直接报错——
// 改已上线的迁移等于让新旧库 schema 分叉，宁可拒绝启动。
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) (applied []string, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return nil, err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLockKey) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    VARCHAR(255) PRIMARY KEY,
		checksum   VARCHAR(64)  NOT NULL,
		applied_at TIMESTAMPTZ  NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, err
	}

	done := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v, c string
		if err := rows.Scan(&v, &c); err != nil {
			return nil, err
		}
		done[v] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			return nil, fmt.Errorf("迁移文件名不合规: %s", e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		checksum := hex.EncodeToString(sum[:])
		if prev, ok := done[name]; ok {
			if prev != checksum {
				return nil, fmt.Errorf("已应用的迁移 %s 内容被修改（checksum 不一致），请还原文件并新增迁移", name)
			}
			continue
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, name, checksum)
			return err
		}); err != nil {
			return applied, fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
