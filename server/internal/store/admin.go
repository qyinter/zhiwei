package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type AdminUser struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
}

func (s *Store) CountAdmins(ctx context.Context) (n int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&n)
	return
}

func (s *Store) AdminByUsername(ctx context.Context, username string) (AdminUser, error) {
	var u AdminUser
	err := s.pool.QueryRow(ctx, `SELECT id, username, password_hash, created_at, last_login_at
		FROM admin_users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.LastLoginAt)
	return u, notFound(err)
}

func (s *Store) AdminBySession(ctx context.Context, tokenHash string) (AdminUser, error) {
	var u AdminUser
	err := s.pool.QueryRow(ctx, `SELECT u.id, u.username, u.password_hash, u.created_at, u.last_login_at
		FROM admin_sessions s JOIN admin_users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.LastLoginAt)
	return u, notFound(err)
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO admin_sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, time.Now().Add(ttl))
	if err != nil {
		return err
	}
	if _, err = s.pool.Exec(ctx, `UPDATE admin_users SET last_login_at = now() WHERE id = $1`, userID); err != nil {
		return err
	}
	// 顺手清理过期会话，表规模始终 ≈ 活跃会话数
	_, err = s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < now()`)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) UpdateAdminPassword(ctx context.Context, userID int64, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_users SET password_hash = $2 WHERE id = $1`, userID, hash)
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE user_id = $1`, userID)
	}
	return err
}

// SetupInput 初始化向导一次性写入的全部内容。
type SetupInput struct {
	Username     string
	PasswordHash string
	// Settings key → value，与管理员账号同一事务落库
	Settings map[string]any
}

// CompleteSetup 在一个事务里创建首个管理员、写入设置并打上 setup.completed 标记。
// 以「admin_users 为空」为前提做条件插入：两个浏览器同时提交向导，只有一个能成功。
func (s *Store) CompleteSetup(ctx context.Context, in SetupInput) (ok bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE admin_users IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO admin_users (username, password_hash)
			SELECT $1, $2 WHERE NOT EXISTS (SELECT 1 FROM admin_users)`, in.Username, in.PasswordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		for k, v := range in.Settings {
			if err := putSetting(ctx, tx, k, v); err != nil {
				return err
			}
		}
		ok = true
		return putSetting(ctx, tx, "setup", map[string]any{"completed": true, "completedAt": time.Now()})
	})
	return ok, err
}
