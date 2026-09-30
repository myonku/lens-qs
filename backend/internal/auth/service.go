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

	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	// 用户不存在与密码错误返回同一提示，避免账号枚举
	if u == nil {
		return nil, httpx.Unauthorized("登录名或密码不正确")
	}
	if u.Status != "active" {
		return nil, httpx.Forbidden("账号已停用，请联系管理员")
	}
	if u.LockedUntil != nil && *u.LockedUntil != "" && store.ParseTime(*u.LockedUntil).After(time.Now()) {
		return nil, httpx.Forbidden("账号因多次登录失败被暂时锁定，请稍后再试")
	}

	if err := password.Verify(plainPassword, u.PasswordHash); err != nil {
		if !errors.Is(err, password.ErrMismatch) {
			return nil, httpx.Unauthorized("登录名或密码不正确")
		}
		failed := u.FailedAttempts + 1
		var lockedUntil *string
		if s.cfg.MaxLoginFail > 0 && failed >= s.cfg.MaxLoginFail {
			t := store.FormatTime(time.Now().Add(s.cfg.LockDuration))
			lockedUntil = &t
			failed = 0
		}
		if err := s.users.UpdateLoginState(ctx, u.ID, failed, lockedUntil, nil); err != nil {
			return nil, err
		}
		if lockedUntil != nil {
			return nil, httpx.Forbidden("连续登录失败次数过多，账号已被暂时锁定")
		}
		return nil, httpx.Unauthorized("登录名或密码不正确")
	}

	now := time.Now()
	rawToken := id.Token(32)
	expiresAt := store.FormatTime(now.Add(s.cfg.SessionTTL))
	if err := s.insertSession(ctx, u.ID, tokenHash(rawToken), ip, userAgent, expiresAt); err != nil {
		return nil, err
	}
	lastLogin := store.FormatTime(now)
	if err := s.users.UpdateLoginState(ctx, u.ID, 0, nil, &lastLogin); err != nil {
		return nil, err
	}
	return &LoginResult{Token: rawToken, ExpiresAt: expiresAt, User: u}, nil
}

// Authenticate 校验令牌并返回调用者身份；同时推进会话的活跃时间。
func (s *Service) Authenticate(token string) (*middleware.Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, httpx.Unauthorized("")
	}
	ctx := context.Background()

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
		UserID:      u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
	}, nil
}

// Logout 注销指定令牌对应的会话。
func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`),
		store.FormatTime(time.Now()), tokenHash(token))
	if err != nil {
		return fmt.Errorf("注销会话失败: %w", err)
	}
	return nil
}

// RevokeAll 注销某用户的全部会话（管理员停用账号或用户改密后调用）。
func (s *Service) RevokeAll(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`),
		store.FormatTime(time.Now()), userID)
	if err != nil {
		return fmt.Errorf("注销会话失败: %w", err)
	}
	return nil
}

// ChangePassword 修改自己的密码（需要提供原密码）。
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	if len(strings.TrimSpace(newPassword)) < 6 {
		return httpx.BadRequest("新密码长度至少 6 位")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.NotFound("用户不存在")
	}
	if err := password.Verify(oldPassword, u.PasswordHash); err != nil {
		return httpx.BadRequest("原密码不正确")
	}
	hash, err := password.Hash(newPassword)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if err := s.users.SetPassword(ctx, userID, hash, false); err != nil {
		return err
	}
	return s.RevokeAll(ctx, userID)
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

func (s *Service) insertSession(ctx context.Context, userID, hash, ip, userAgent, expiresAt string) error {
	sess := &Session{
		ID:         id.New(),
		UserID:     userID,
		TokenHash:  hash,
		CreatedAt:  store.FormatTime(time.Now()),
		LastSeenAt: store.FormatTime(time.Now()),
		ExpiresAt:  expiresAt,
	}
	if strings.TrimSpace(ip) != "" {
		sess.IP = &ip
	}
	if strings.TrimSpace(userAgent) != "" {
		sess.UserAgent = &userAgent
	}
	_, err := s.db.NamedExecContext(ctx, `INSERT INTO sessions
		(id, user_id, token_hash, ip, user_agent, created_at, last_seen_at, expires_at, revoked_at)
		VALUES (:id, :user_id, :token_hash, :ip, :user_agent, :created_at, :last_seen_at, :expires_at, :revoked_at)`, sess)
	if err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}
	return nil
}

func (s *Service) rebind(q string) string { return s.db.Rebind(q) }

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
