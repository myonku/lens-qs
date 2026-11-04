// Package users 实现用户（管理员 / 普通用户两级）的读写。
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/pkg/password"
	"github.com/myonku/lens-qs-api/internal/store"
)

// User 对应数据库表 users。
type User struct {
	AccountType    string  `db:"account_type" json:"account_type"`
	ID             string  `db:"id" json:"id"`
	Username       string  `db:"username" json:"username"`
	DisplayName    string  `db:"display_name" json:"display_name"`
	EmployeeNo     *string `db:"employee_no" json:"employee_no,omitempty"`
	UnitID         *string `db:"unit_id" json:"unit_id,omitempty"`
	PasswordHash   string  `db:"password_hash" json:"-"`
	IsAdmin        bool    `db:"is_admin" json:"is_admin"`
	MustChangePwd  bool    `db:"must_change_pwd" json:"must_change_pwd"`
	Status         string  `db:"status" json:"status"`
	FailedAttempts int     `db:"failed_attempts" json:"failed_attempts"`
	LockedUntil    *string `db:"locked_until" json:"locked_until,omitempty"`
	LastLoginAt    *string `db:"last_login_at" json:"last_login_at,omitempty"`
	CreatedAt      string  `db:"created_at" json:"created_at"`
	UpdatedAt      string  `db:"updated_at" json:"updated_at"`
}

// Store 负责用户持久化。
type Store struct {
	db     *sqlx.DB
	driver string
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB, driver string) *Store { return &Store{db: db, driver: driver} }

// DB 暴露底层句柄。
func (s *Store) DB() *sqlx.DB { return s.db }

func (s *Store) rebind(q string) string { return s.db.Rebind(q) }

const columns = `id, username, display_name, employee_no, unit_id, password_hash,
	is_admin, account_type, must_change_pwd, status, failed_attempts, locked_until, last_login_at,
	created_at, updated_at`

// GetByID 按主键取用户。
func (s *Store) GetByID(ctx context.Context, userID string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, s.rebind(`SELECT `+columns+` FROM users WHERE id = ?`), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return &u, nil
}

// GetByUsername 按登录名取用户。
func (s *Store) GetByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, s.rebind(`SELECT `+columns+` FROM users WHERE username = ?`), username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return &u, nil
}

// List 分页查询用户。
func (s *Store) List(ctx context.Context, keyword string, offset, limit int) ([]User, int, error) {
	where := "1=1"
	args := []any{}
	if kw := strings.TrimSpace(keyword); kw != "" {
		where += " AND (username LIKE ? OR display_name LIKE ? OR employee_no LIKE ?)"
		like := "%" + kw + "%"
		args = append(args, like, like, like)
	}

	var total int
	if err := s.db.GetContext(ctx, &total, s.rebind(`SELECT COUNT(*) FROM users WHERE `+where), args...); err != nil {
		return nil, 0, fmt.Errorf("统计用户数失败: %w", err)
	}

	q := `SELECT ` + columns + ` FROM users WHERE ` + where + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	var out []User
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, 0, fmt.Errorf("查询用户列表失败: %w", err)
	}
	return out, total, nil
}

// Count 返回用户总数（用于判断是否需要初始化管理员）。
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM users`); err != nil {
		return 0, fmt.Errorf("统计用户数失败: %w", err)
	}
	return n, nil
}

// CreateInput 是创建用户的输入。
type CreateInput struct {
	AccountType   string
	Username      string
	Password      string
	DisplayName   string
	EmployeeNo    *string
	UnitID        *string
	IsAdmin       bool
	MustChangePwd bool
}

// Create 创建用户。
func (s *Store) Create(ctx context.Context, in CreateInput) (*User, error) {
	if in.AccountType == "" {
		in.AccountType = "NORMAL"
	}
	if !validAccountType(in.AccountType) {
		return nil, httpx.BadRequest("account_type 必须为 NORMAL 或 SAMPLING")
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		return nil, httpx.BadRequest("登录名（username）不能为空")
	}
	if strings.TrimSpace(in.Password) == "" {
		return nil, httpx.BadRequest("密码不能为空")
	}
	if existing, err := s.GetByUsername(ctx, username); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, httpx.Conflict("登录名已存在: " + username)
	}

	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, httpx.BadRequest(err.Error())
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name = username
	}
	now := store.FormatTime(time.Now())
	u := &User{
		AccountType:   in.AccountType,
		ID:            id.New(),
		Username:      username,
		DisplayName:   name,
		EmployeeNo:    in.EmployeeNo,
		UnitID:        in.UnitID,
		PasswordHash:  hash,
		IsAdmin:       in.IsAdmin,
		MustChangePwd: in.MustChangePwd,
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err = store.NamedExec(ctx, s.db, `INSERT INTO users (`+columns+`) VALUES (
		:id, :username, :display_name, :employee_no, :unit_id, :password_hash,
		:is_admin, :account_type, :must_change_pwd, :status, :failed_attempts, :locked_until, :last_login_at,
		:created_at, :updated_at)`, u)
	if err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}
	return u, nil
}

// UpdateInput 是修改用户的输入（nil 表示不改）。
type UpdateInput struct {
	AccountType *string
	DisplayName *string
	EmployeeNo  *string
	UnitID      *string
	IsAdmin     *bool
	Status      *string
	Password    *string
}

// Update 修改用户。
func (s *Store) Update(ctx context.Context, userID string, in UpdateInput) (*User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE v2_write_lock SET version = version + 1 WHERE id = 1"); err != nil {
		return nil, err
	}
	u := &User{}
	err = tx.GetContext(ctx, u, tx.Rebind("SELECT "+columns+" FROM users WHERE id = ?"), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("用户不存在")
	}
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, httpx.NotFound("用户不存在")
	}
	if in.AccountType != nil {
		if !validAccountType(*in.AccountType) {
			return nil, httpx.BadRequest("account_type 必须为 NORMAL 或 SAMPLING")
		}
		u.AccountType = *in.AccountType
	}
	if in.DisplayName != nil {
		u.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.EmployeeNo != nil {
		u.EmployeeNo = blankToNil(*in.EmployeeNo)
	}
	if in.UnitID != nil {
		u.UnitID = blankToNil(*in.UnitID)
	}
	if in.IsAdmin != nil {
		u.IsAdmin = *in.IsAdmin
	}
	if in.Status != nil {
		st := strings.ToLower(strings.TrimSpace(*in.Status))
		if st != "active" && st != "disabled" {
			return nil, httpx.BadRequest("状态只能是 active 或 disabled")
		}
		u.Status = st
	}
	if in.Password != nil {
		if strings.TrimSpace(*in.Password) == "" {
			return nil, httpx.BadRequest("密码不能为空")
		}
		hash, err := password.Hash(*in.Password)
		if err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		u.PasswordHash = hash
		u.FailedAttempts = 0
		u.LockedUntil = nil
	}
	u.UpdatedAt = store.FormatTime(time.Now())

	query, args, err := sqlx.Named(`UPDATE users SET
		display_name = :display_name, employee_no = :employee_no, unit_id = :unit_id,
		password_hash = :password_hash, is_admin = :is_admin, account_type = :account_type, status = :status,
		failed_attempts = :failed_attempts, locked_until = :locked_until, updated_at = :updated_at
		WHERE id = :id`, u)
	if err != nil {
		return nil, err
	}
	for i, v := range args {
		if b, ok := v.(bool); ok {
			args[i] = store.BoolInt(b)
		}
	}
	if _, err = tx.ExecContext(ctx, tx.Rebind(query), args...); err != nil {
		return nil, fmt.Errorf("更新用户失败: %w", err)
	}
	if in.AccountType != nil || in.Password != nil || in.Status != nil && *in.Status == "disabled" {
		if _, err = tx.ExecContext(ctx, tx.Rebind("UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL"), nowTime(), u.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, tx.Rebind("DELETE FROM v2_idempotency_requests WHERE actor_id = ?"), u.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateLoginState 更新登录失败计数、锁定时间与最后登录时间。
func (s *Store) UpdateLoginState(ctx context.Context, userID string, failed int, lockedUntil, lastLogin *string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE users SET
		failed_attempts = ?, locked_until = ?, last_login_at = COALESCE(?, last_login_at),
		updated_at = ? WHERE id = ?`),
		failed, lockedUntil, lastLogin, store.FormatTime(time.Now()), userID)
	if err != nil {
		return fmt.Errorf("更新登录状态失败: %w", err)
	}
	return nil
}

// SetPassword 直接设置密码哈希（用于改密）。
func (s *Store) SetPassword(ctx context.Context, userID, hash string, mustChange bool) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE users SET
		password_hash = ?, must_change_pwd = ?, failed_attempts = 0, locked_until = NULL,
		updated_at = ? WHERE id = ?`), hash, store.BoolInt(mustChange), store.FormatTime(time.Now()), userID)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	return nil
}

func blankToNil(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func validAccountType(s string) bool { return s == "NORMAL" || s == "SAMPLING" }
func nowTime() string                { return store.FormatTime(time.Now()) }
