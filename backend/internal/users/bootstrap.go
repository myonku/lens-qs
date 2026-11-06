package users

import (
	"context"
	"fmt"
)

// BootstrapAdmin creates an administrator only in an empty user database.
// The empty check is repeated under the shared write gate to serialize starts.
// A nil result means disabled or already initialized; existing accounts are untouched.
func (s *Store) BootstrapAdmin(ctx context.Context, in CreateInput) (*User, error) {
	n, err := s.Count(ctx)
	if err != nil || n != 0 {
		return nil, err
	}
	if in.Username == "" && in.Password == "" {
		return nil, nil
	}
	if in.Username == "" || in.Password == "" {
		return nil, fmt.Errorf("空用户库引导需要同时配置 BOOTSTRAP_ADMIN_USER 和 BOOTSTRAP_ADMIN_PASS")
	}
	in.IsAdmin = true
	in.MustChangePwd = false
	return s.create(ctx, in, true)
}

// ConfigureAdmin applies explicitly supplied profile values to a named admin.
// It never changes an existing login name, password, status or privileges.
func (s *Store) ConfigureAdmin(ctx context.Context, in CreateInput) (*User, error) {
	if in.Username == "" {
		return nil, fmt.Errorf("需要 BOOTSTRAP_ADMIN_USER")
	}
	u, err := s.GetByUsername(ctx, in.Username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		created, err := s.BootstrapAdmin(ctx, in)
		if err != nil {
			return nil, err
		}
		if created == nil {
			return nil, fmt.Errorf("用户库非空且指定管理员不存在，请使用显式 -username/-password 创建")
		}
		return created, nil
	}
	if !u.IsAdmin {
		return nil, fmt.Errorf("指定账号不是管理员，不通过引导配置提升权限")
	}
	patch := UpdateInput{}
	changed := false
	if in.AccountType != "" && in.AccountType != u.AccountType {
		patch.AccountType = &in.AccountType
		changed = true
	}
	if in.DisplayName != "" && in.DisplayName != u.DisplayName {
		patch.DisplayName = &in.DisplayName
		changed = true
	}
	if in.EmployeeNo != nil && (u.EmployeeNo == nil || *in.EmployeeNo != *u.EmployeeNo) {
		patch.EmployeeNo.Set, patch.EmployeeNo.Value = true, in.EmployeeNo
		changed = true
	}
	if in.Department != nil && (u.Department == nil || *in.Department != *u.Department) {
		patch.Department.Set, patch.Department.Value = true, in.Department
		changed = true
	}
	if !changed {
		return u, nil
	}
	return s.Update(ctx, u.ID, patch)
}
