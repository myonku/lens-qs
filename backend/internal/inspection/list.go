package inspection

import (
	"context"
	"fmt"
	"strings"
)

// FormFilter 是表单列表查询条件（全部可选）。
type FormFilter struct {
	Keyword     string // 表单号 / 样品原始串 模糊
	State       string // ACTIVE | VOID | ""（全部）
	NeedsReview bool
	From        string // 创建时间起（含）
	To          string // 创建时间止（含）
	Project     string
	Model       string
	Spec        string
	Size        string
	Side        string
	Unit        string
	Offset      int
	Limit       int
}

// ListForms 分页查询表单。
func (s *Store) ListForms(ctx context.Context, f FormFilter) ([]Form, int, error) {
	where, args := formWhere(f)

	var total int
	if err := s.db.GetContext(ctx, &total,
		s.rebind(`SELECT COUNT(*) FROM forms f WHERE `+where), args...); err != nil {
		return nil, 0, fmt.Errorf("统计表单数失败: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := `SELECT ` + formColumns + ` FROM forms f WHERE ` + where +
		` ORDER BY f.created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	var out []Form
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, 0, fmt.Errorf("查询表单列表失败: %w", err)
	}
	return out, total, nil
}

func formWhere(f FormFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}

	if st := strings.TrimSpace(f.State); st != "" {
		clauses = append(clauses, "f.state = ?")
		args = append(args, strings.ToUpper(st))
	}
	if f.NeedsReview {
		clauses = append(clauses, "f.needs_review = 1")
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		clauses = append(clauses, "(f.form_no LIKE ? OR f.sample_raw LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like)
	}
	if v := strings.TrimSpace(f.From); v != "" {
		clauses = append(clauses, "f.created_at >= ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.To); v != "" {
		clauses = append(clauses, "f.created_at <= ?")
		args = append(args, v+"T23:59:59Z")
	}
	add := func(col, val string) {
		if v := strings.TrimSpace(val); v != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, v)
		}
	}
	add("f.sample_project", f.Project)
	add("f.sample_model", f.Model)
	add("f.sample_spec", f.Spec)
	add("f.sample_size", f.Size)
	add("f.sample_side", f.Side)
	if v := strings.TrimSpace(f.Unit); v != "" {
		clauses = append(clauses, "COALESCE(f.unit_text, f.unit_id) = ?")
		args = append(args, v)
	}
	return strings.Join(clauses, " AND "), args
}
