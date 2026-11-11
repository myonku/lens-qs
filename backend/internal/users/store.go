// Package users 实现用户（管理员 / 普通用户两级）的读写。
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	OperationID    *string  `db:"operation_id" json:"operation_id"`
	OperationIDs   []string `db:"-" json:"operation_ids"`
	AccountType    string   `db:"account_type" json:"account_type"`
	ID             string   `db:"id" json:"id"`
	Username       string   `db:"username" json:"username"`
	DisplayName    string   `db:"display_name" json:"display_name"`
	EmployeeNo     *string  `db:"employee_no" json:"employee_no"`
	Department     *string  `db:"department" json:"department"`
	PasswordHash   string   `db:"password_hash" json:"-"`
	IsAdmin        bool     `db:"is_admin" json:"is_admin"`
	MustChangePwd  bool     `db:"must_change_pwd" json:"-"`
	Status         string   `db:"status" json:"status"`
	FailedAttempts int      `db:"failed_attempts" json:"-"`
	LockedUntil    *string  `db:"locked_until" json:"-"`
	LastLoginAt    *string  `db:"last_login_at" json:"-"`
	CreatedAt      string   `db:"created_at" json:"created_at"`
	UpdatedAt      string   `db:"updated_at" json:"updated_at"`
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

const columns = `id, username, display_name, employee_no, department, password_hash,
	is_admin, account_type, must_change_pwd, status, failed_attempts, locked_until, last_login_at,
	created_at, updated_at, operation_id`

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
	if err := s.LoadOperationIDs(ctx, &u); err != nil {
		return nil, err
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
	if err := s.LoadOperationIDs(ctx, &u); err != nil {
		return nil, err
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
	for i := range out {
		if err := s.LoadOperationIDs(ctx, &out[i]); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

func (s *Store) LoadOperationIDs(ctx context.Context, u *User) error {
	var values []string
	if err := s.db.SelectContext(ctx, &values, s.rebind(`SELECT operation_id FROM user_operations WHERE user_id=? ORDER BY operation_id`), u.ID); err != nil {
		return fmt.Errorf("查询用户工序权限失败: %w", err)
	}
	u.OperationIDs = append([]string{}, values...)
	return nil
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
	OperationID   *string
	OperationIDs  []string
	AccountType   string
	Username      string
	Password      string
	DisplayName   string
	EmployeeNo    *string
	Department    *string
	IsAdmin       bool
	MustChangePwd bool
}

// Create 创建用户。
func (s *Store) Create(ctx context.Context, in CreateInput) (*User, error) {
	return s.create(ctx, in, false)
}

func (s *Store) create(ctx context.Context, in CreateInput, onlyEmpty bool) (*User, error) {
	if in.AccountType == "" {
		in.AccountType = "NORMAL"
	}
	if !validAccountType(in.AccountType) {
		return nil, httpx.Validation("account_type 必须为 NORMAL 或 SAMPLING", nil)
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		return nil, httpx.BadRequest("登录名（username）不能为空")
	}
	if err := password.Validate(in.Password); err != nil {
		return nil, err
	}
	if existing, err := s.GetByUsername(ctx, username); err != nil {
		return nil, err
	} else if existing != nil {
		if onlyEmpty {
			return nil, nil
		}
		return nil, httpx.Conflict("登录名已存在", map[string]string{"reason": "user_exists"})
	}

	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, httpx.BadRequest(err.Error())
	}
	name := strings.TrimSpace(in.DisplayName)

	now := store.FormatTime(time.Now())
	u := &User{
		AccountType:   in.AccountType,
		OperationID:   clean(in.OperationID),
		OperationIDs:  normalizeOperations(in.OperationIDs),
		ID:            id.New(),
		Username:      username,
		DisplayName:   name,
		EmployeeNo:    clean(in.EmployeeNo),
		Department:    clean(in.Department),
		PasswordHash:  hash,
		IsAdmin:       in.IsAdmin,
		MustChangePwd: in.MustChangePwd,
		Status:        "active",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if in.OperationIDs == nil && u.OperationID != nil {
		u.OperationIDs = []string{*u.OperationID}
	}
	u.OperationID = nil
	if len(u.OperationIDs) > 0 {
		u.OperationID = &u.OperationIDs[0]
	}
	w, err := store.BeginWrite(ctx, s.db, "", false, nil)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if onlyEmpty {
		var count int
		if err := w.GetContext(w.Context, &count, `SELECT COUNT(*) FROM users`); err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, nil
		}
	}
	if w.Actor.ID != "" && !w.Actor.Admin {
		return nil, httpx.Forbidden("admin required")
	}
	if err := validateOperation(w, u.OperationID); err != nil {
		return nil, err
	}
	if err := validateOperations(w, u.OperationIDs); err != nil {
		return nil, err
	}
	query, args, err := sqlx.Named(`INSERT INTO users (`+columns+`) VALUES (:id,:username,:display_name,:employee_no,:department,:password_hash,:is_admin,:account_type,:must_change_pwd,:status,:failed_attempts,:locked_until,:last_login_at,:created_at,:updated_at,:operation_id)`, u)
	if err != nil {
		return nil, err
	}
	for i, v := range args {
		if b, ok := v.(bool); ok {
			args[i] = store.BoolInt(b)
		}
	}
	if _, err = w.ExecContext(w.Context, w.Rebind(query), args...); err != nil {
		return nil, userError(err)
	}
	if err := replaceOperations(w, u.ID, u.OperationIDs); err != nil {
		return nil, err
	}
	if err = w.Event("create", "user", u.ID, nil, u, nil); err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}

	return u, nil
}

// UpdateInput 是修改用户的输入（nil 表示不改）。
type UpdateInput struct {
	OperationID  httpx.Optional[string]
	OperationIDs httpx.Optional[[]string]
	AccountType  *string
	DisplayName  *string
	EmployeeNo   httpx.Optional[string]
	Department   httpx.Optional[string]
	IsAdmin      *bool
	Status       *string
	Password     *string
}

// Update 修改用户。
func (s *Store) Update(ctx context.Context, userID string, in UpdateInput) (*User, error) {
	tx, err := store.BeginWrite(ctx, s.db, "", false, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	if tx.Actor.ID != "" && !tx.Actor.Admin {
		return nil, httpx.Forbidden("admin required")
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
	var existingOperations []string
	if err := tx.SelectContext(tx.Context, &existingOperations, tx.Rebind(`SELECT operation_id FROM user_operations WHERE user_id=? ORDER BY operation_id`), u.ID); err != nil {
		return nil, err
	}
	u.OperationIDs = append([]string{}, existingOperations...)
	before := *u
	operationsChanged := false
	if in.OperationID.Set {
		u.OperationID = clean(in.OperationID.Value)
		if err := validateOperation(tx, u.OperationID); err != nil {
			return nil, err
		}
		if !in.OperationIDs.Set {
			u.OperationIDs = []string{}
			if u.OperationID != nil {
				u.OperationIDs = []string{*u.OperationID}
			}
			operationsChanged = true
		}
	}
	if in.OperationIDs.Set {
		if in.OperationIDs.Value == nil {
			u.OperationIDs = []string{}
		} else {
			u.OperationIDs = normalizeOperations(*in.OperationIDs.Value)
		}
		if err := validateOperations(tx, u.OperationIDs); err != nil {
			return nil, err
		}
		if values := u.OperationIDs; len(values) > 0 {
			u.OperationID = &values[0]
		} else {
			u.OperationID = nil
		}
		operationsChanged = true
	}
	if in.AccountType != nil {
		if !validAccountType(*in.AccountType) {
			return nil, httpx.Validation("account_type 必须为 NORMAL 或 SAMPLING", nil)
		}
		u.AccountType = *in.AccountType
	}
	if in.DisplayName != nil {
		u.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.EmployeeNo.Set {
		u.EmployeeNo = clean(in.EmployeeNo.Value)
	}
	if in.Department.Set {
		u.Department = clean(in.Department.Value)
	}
	if in.IsAdmin != nil {
		u.IsAdmin = *in.IsAdmin
	}
	if in.Status != nil {
		st := *in.Status
		if st != "active" && st != "disabled" {
			return nil, httpx.Validation("状态只能是 active 或 disabled", nil)
		}
		u.Status = st
	}
	if in.Password != nil {
		if err := password.Validate(*in.Password); err != nil {
			return nil, err
		}
		hash, err := password.Hash(*in.Password)
		if err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		u.PasswordHash = hash
		u.FailedAttempts = 0
		u.LockedUntil = nil
	}
	if tx.Actor.ID == u.ID && (in.IsAdmin != nil && !*in.IsAdmin || in.Status != nil && *in.Status == "disabled") {
		return nil, httpx.Conflict("self_admin_change_forbidden", map[string]string{"reason": "self_admin_change_forbidden"})
	}
	if !u.IsAdmin || u.Status != "active" {
		var n int
		if err = tx.GetContext(tx.Context, &n, tx.Rebind(`SELECT COUNT(*) FROM users WHERE is_admin=1 AND status='active' AND id<>?`), u.ID); err != nil {
			return nil, err
		}
		if n == 0 {
			var wasAdmin bool
			if err = tx.GetContext(tx.Context, &wasAdmin, tx.Rebind(`SELECT is_admin FROM users WHERE id=?`), u.ID); err != nil {
				return nil, err
			}
			if wasAdmin {
				return nil, httpx.Conflict("last_admin_required")
			}
		}
	}
	u.UpdatedAt = store.FormatTime(time.Now())

	query, args, err := sqlx.Named(`UPDATE users SET
		display_name = :display_name, employee_no = :employee_no, department = :department, operation_id = :operation_id,
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
		return nil, userError(err)
	}
	if operationsChanged {
		if err = replaceOperations(tx, u.ID, u.OperationIDs); err != nil {
			return nil, err
		}
	}
	if operationsChanged || before.IsAdmin != u.IsAdmin || in.AccountType != nil || in.Password != nil || in.Status != nil && *in.Status == "disabled" {
		if _, err = tx.ExecContext(ctx, tx.Rebind("UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL"), nowTime(), u.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, tx.Rebind("DELETE FROM v2_idempotency_requests WHERE actor_id = ?"), u.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Event("update", "user", u.ID, before, u, nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

func validateOperation(tx *store.WriteTx, operationID *string) error {
	if operationID == nil {
		return nil
	}
	var id string
	err := tx.GetContext(tx.Context, &id, tx.Rebind(`SELECT id FROM dict_items WHERE id=? AND category='inspection_operation' AND active=1`), *operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.Validation("operation_id 必须为已启用的检验工序 ID", map[string]string{"reason": "invalid_operation", "field": "operation_id"})
	}
	return err
}

func validateOperations(tx *store.WriteTx, operationIDs []string) error {
	for _, operationID := range operationIDs {
		if err := validateOperation(tx, &operationID); err != nil {
			return err
		}
	}
	return nil
}

func replaceOperations(tx *store.WriteTx, userID string, operationIDs []string) error {
	if _, err := tx.ExecContext(tx.Context, tx.Rebind(`DELETE FROM user_operations WHERE user_id=?`), userID); err != nil {
		return err
	}
	for _, operationID := range operationIDs {
		if _, err := tx.ExecContext(tx.Context, tx.Rebind(`INSERT INTO user_operations(user_id,operation_id) VALUES(?,?)`), userID, operationID); err != nil {
			return userError(err)
		}
	}
	return nil
}

func normalizeOperations(operationIDs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(operationIDs))
	for _, operationID := range operationIDs {
		operationID = strings.TrimSpace(operationID)
		if !seen[operationID] {
			seen[operationID] = true
			out = append(out, operationID)
		}
	}
	sort.Strings(out)
	return out
}

func clean(s *string) *string {
	if s == nil {
		return nil
	}
	return blankToNil(*s)
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

func userError(err error) error {
	e := store.StorageError(err)
	var h *httpx.Error
	if errors.As(e, &h) && h.Status == 409 {
		return httpx.Conflict("登录名或工号已存在", map[string]string{"reason": "user_exists"})
	}
	return e
}
