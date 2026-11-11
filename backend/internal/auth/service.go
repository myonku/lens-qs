// Package auth 实现登录、会话与密码修改。
package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/pkg/password"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

// Config 是认证相关配置。
type Config struct {
	SessionTTL     time.Duration // 会话绝对有效期
	SessionIdleTTL time.Duration // 空闲超时
	MaxLoginFail   int           // 连续失败上限
	LockDuration   time.Duration // 达到上限后的锁定时长
}

// Session 对应数据库表 sessions。
type Session struct {
	ID         string  `db:"id" json:"id"`
	UserID     string  `db:"user_id" json:"user_id"`
	TokenHash  string  `db:"token_hash" json:"-"`
	IP         *string `db:"ip" json:"ip,omitempty"`
	UserAgent  *string `db:"user_agent" json:"user_agent,omitempty"`
	CreatedAt  string  `db:"created_at" json:"created_at"`
	LastSeenAt string  `db:"last_seen_at" json:"last_seen_at"`
	ExpiresAt  string  `db:"expires_at" json:"expires_at"`
	RevokedAt  *string `db:"revoked_at" json:"revoked_at,omitempty"`
}

// Service 提供登录 / 校验 / 登出 / 改密。
type Service struct {
	db     *sqlx.DB
	users  *users.Store
	cfg    Config
	driver string
}

// NewService 构造认证服务。
func NewService(db *sqlx.DB, userStore *users.Store, cfg Config, driver string) *Service {
	return &Service{db: db, users: userStore, cfg: cfg, driver: driver}
}

// LoginResult 是登录成功的结果。
type LoginResult struct {
	Token     string      `json:"token"`
	ExpiresAt string      `json:"expires_at"`
	User      *users.User `json:"user"`
}

// Login 校验账号密码并创建会话。
func (s *Service) Login(ctx context.Context, username, plainPassword, ip, userAgent string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || plainPassword == "" {
		return nil, httpx.BadRequest("请输入登录名与密码")
	}
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return nil, e
	}
	defer w.Close()
	var u users.User
	e = w.GetContext(w.Context, &u, w.Rebind(`SELECT id,username,display_name,employee_no,department,password_hash,is_admin,account_type,must_change_pwd,status,failed_attempts,locked_until,last_login_at,created_at,updated_at,operation_id FROM users WHERE username=?`), username)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	valid := e == nil && u.Status == "active" && (u.LockedUntil == nil || !store.ParseTime(*u.LockedUntil).After(time.Now()))
	if valid {
		valid = password.Verify(plainPassword, u.PasswordHash) == nil
	}
	if !valid {
		if u.ID != "" {
			w.Actor = store.Actor{ID: u.ID, Name: u.DisplayName}
			failed := u.FailedAttempts + 1
			var locked *string
			if u.LockedUntil != nil && store.ParseTime(*u.LockedUntil).After(time.Now()) {
				locked = u.LockedUntil
				failed = u.FailedAttempts
			} else if s.cfg.MaxLoginFail > 0 && failed >= s.cfg.MaxLoginFail {
				v := store.FormatTime(time.Now().Add(s.cfg.LockDuration))
				locked = &v
				failed = 0
			}
			if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE users SET failed_attempts=?,locked_until=? WHERE id=?`), failed, locked, u.ID); e != nil {
				return nil, e
			}
		}
		if e = w.Event("login_failed", "auth", u.ID, nil, nil, nil); e != nil {
			return nil, e
		}
		if e = w.Commit(); e != nil {
			return nil, e
		}
		return nil, httpx.Unauthorized("登录名或密码不正确")
	}
	w.Actor = store.Actor{ID: u.ID, Name: u.DisplayName}
	token := id.Token(32)
	expires := store.FormatTime(time.Now().Add(s.cfg.SessionTTL))
	if _, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO sessions(id,user_id,token_hash,ip,user_agent,created_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`), id.New(), u.ID, tokenHash(token), ip, userAgent, w.Time, w.Time, expires); e != nil {
		return nil, e
	}
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE users SET failed_attempts=0,locked_until=NULL,last_login_at=? WHERE id=?`), w.Time, u.ID); e != nil {
		return nil, e
	}
	if e = w.Event("login", "auth", u.ID, nil, nil, nil); e != nil {
		return nil, e
	}
	if e = w.Commit(); e != nil {
		return nil, e
	}
	return &LoginResult{token, expires, &u}, nil
}

// Authenticate 校验令牌并返回调用者身份；同时推进会话的活跃时间。
func (s *Service) Authenticate(token string) (*middleware.Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, httpx.Unauthorized("")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sess Session
	err := s.db.GetContext(ctx, &sess,
		s.rebind(`SELECT id, user_id, token_hash, ip, user_agent, created_at, last_seen_at, expires_at, revoked_at
			FROM sessions WHERE token_hash = ?`), tokenHash(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.Unauthorized("会话不存在或已失效，请重新登录")
	}
	if err != nil {
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	now := time.Now()
	if sess.RevokedAt != nil && *sess.RevokedAt != "" {
		return nil, httpx.Unauthorized("会话已注销，请重新登录")
	}
	if store.ParseTime(sess.ExpiresAt).Before(now) {
		return nil, httpx.Unauthorized("会话已过期，请重新登录")
	}
	if s.cfg.SessionIdleTTL > 0 && store.ParseTime(sess.LastSeenAt).Add(s.cfg.SessionIdleTTL).Before(now) {
		return nil, httpx.Unauthorized("会话空闲超时，请重新登录")
	}

	u, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != "active" {
		return nil, httpx.Unauthorized("账号不可用，请重新登录")
	}

	// 滑动续期（失败不影响本次请求）
	_, _ = s.db.ExecContext(ctx, s.rebind(`UPDATE sessions SET last_seen_at = ? WHERE id = ?`),
		store.FormatTime(now), sess.ID)

	return &middleware.Principal{
		AccountType: u.AccountType,
		UserID:      u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
	}, nil
}

// Logout 注销指定令牌对应的会话。
func (s *Service) Logout(ctx context.Context, token string) error {
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return e
	}
	defer w.Close()
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL`), w.Time, tokenHash(token)); e != nil {
		return e
	}
	if e = w.Event("logout", "auth", w.Actor.ID, nil, nil, nil); e != nil {
		return e
	}
	return w.Commit()
}
func (s *Service) RevokeAll(ctx context.Context, userID string) error {
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return e
	}
	defer w.Close()
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`), w.Time, userID); e != nil {
		return e
	}
	if e = w.Event("sessions_revoked", "auth", userID, nil, nil, nil); e != nil {
		return e
	}
	return w.Commit()
}
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	if e := password.Validate(newPassword); e != nil {
		return e
	}
	w, e := store.BeginWrite(ctx, s.db, userID, false, nil)
	if e != nil {
		return e
	}
	defer w.Close()
	var hash string
	if e = w.GetContext(w.Context, &hash, w.Rebind(`SELECT password_hash FROM users WHERE id=?`), userID); e != nil {
		return e
	}
	if password.Verify(oldPassword, hash) != nil {
		return httpx.Unauthorized("原密码不正确")
	}
	hash, e = password.Hash(newPassword)
	if e != nil {
		return e
	}
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE users SET password_hash=?,must_change_pwd=0,failed_attempts=0,locked_until=NULL,updated_at=? WHERE id=?`), hash, w.Time, userID); e != nil {
		return e
	}
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`), w.Time, userID); e != nil {
		return e
	}
	if e = w.Event("password_changed", "user", userID, nil, nil, nil); e != nil {
		return e
	}
	return w.Commit()
}

// CleanupExpired 清理已过期会话（可由定时任务调用）。
func (s *Service) CleanupExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM sessions WHERE expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)`),
		store.FormatTime(time.Now()), store.FormatTime(time.Now().Add(-24*time.Hour)))
	if err != nil {
		return 0, fmt.Errorf("清理会话失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Service) rebind(q string) string { return s.db.Rebind(q) }

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
