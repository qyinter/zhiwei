// Package store 是知微唯一的持久层：pgx 连接池 + 手写 SQL。
//
// 不引 ORM：表少、查询直白，手写 SQL 让每条语句的行为（锁、条件更新、索引命中）
// 在代码里一眼可见。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Close() { s.pool.Close() }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ── system_settings ──

// GetSetting 把 key 对应的 JSON 解到 out；不存在返回 ErrNotFound（out 保持调用方给的默认值）。
func (s *Store) GetSetting(ctx context.Context, key string, out any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return notFound(err)
	}
	return json.Unmarshal(raw, out)
}

func (s *Store) PutSetting(ctx context.Context, key string, value any) error {
	return putSetting(ctx, s.pool, key, value)
}

// dbtx 是 pool 与 tx 的公共子集，让同一段写入既能单独跑也能并进事务。
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func putSetting(ctx context.Context, db dbtx, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO system_settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return err
}
