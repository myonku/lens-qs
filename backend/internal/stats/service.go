// Package stats 实现汇总统计（需求 §5.8）。
//
// 汇总按**表单提交**（一次提交 = 某工序填的那张表单）聚合，指标取自提交上的
// total_qty / qualified_qty / defect_qty；不良缺陷分布则按明细行逐列求和。
// 分组维度：检验工序、样品信息各子项、单位、录入人、日期，以及任意组合。
package stats

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// groupExpr 是允许的分组维度 → SQL 表达式（白名单，防止注入）。
var groupExpr = map[string]string{
	"operation_code": "f.operation_code",
	"operation_text": "f.operation_text",
	"sample_project": "f.sample_project",
	"sample_model":   "f.sample_model",
	"sample_spec":    "f.sample_spec",
	"sample_size":    "f.sample_size",
	"sample_side":    "f.sample_side",
	"sample_raw":     "f.sample_raw",
	"form_no":        "f.form_no",
	"unit":           "COALESCE(f.paper_unit_text, f.paper_unit_id)",
	// 纸面人员（手写人名，B-11/B-17）
	"paper_inspector":  "f.paper_inspector",
	"paper_submitter":  "f.paper_submitter",
	"paper_operator":   "f.paper_machine_operator",
	"paper_handover":   "f.paper_handover_by",
	// 系统留痕（实际录入人）
	"submitted_by":  "f.submitted_by",
	"last_saved_by": "f.last_saved_by",
	"created_by":    "f.created_by",
	// 日期
	"day":   "substr(COALESCE(f.paper_submitted_at, f.submitted_at, f.created_at), 1, 10)",
	"month": "substr(COALESCE(f.paper_submitted_at, f.submitted_at, f.created_at), 1, 7)",
}

// Service 提供汇总统计。
type Service struct {
	db     *sqlx.DB
	driver string
}

// NewService 构造服务。
func NewService(db *sqlx.DB, driver string) *Service { return &Service{db: db, driver: driver} }

// Filter 是统计筛选条件。
type Filter struct {
	From          string
	To            string
	OperationCode string
	Project       string
	Model         string
	Spec          string
	Size          string
	Side          string
	Inspector     string // 纸面"检验人"
	Submitter     string // 纸面"送检人"
	SubmittedBy   string // 系统录入人
	Unit          string
	OnlyActive    bool
}

// GroupResult 是一个分组的结果。
type GroupResult struct {
	Key          string  `db:"g_key" json:"key"`
	FormCount    int     `db:"form_count" json:"form_count"`
	TotalQty     int     `db:"total_qty" json:"total_qty"`
	QualifiedQty int     `db:"qualified_qty" json:"qualified_qty"`
	DefectQty    int     `db:"defect_qty" json:"defect_qty"`
	PassRate     float64 `db:"-" json:"pass_rate"`
}

// SummaryResult 是汇总返回。
type SummaryResult struct {
	GroupBy []string      `json:"group_by"`
	Groups  []GroupResult `json:"groups"`
	Total   GroupResult   `json:"total"`
	Note    string        `json:"note,omitempty"`
}

// SupportedDimensions 返回可用于分组的维度列表（供调用方发现能力）。
func SupportedDimensions() []string {
	out := make([]string, 0, len(groupExpr))
	for k := range groupExpr {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Summary 按指定维度分组汇总。groupBy 为空时只返回总计。
func (s *Service) Summary(ctx context.Context, f Filter, groupBy []string) (*SummaryResult, error) {
	selects := make([]string, 0, len(groupBy))
	groupExprs := make([]string, 0, len(groupBy))
	keyAliases := make([]string, 0, len(groupBy))
	for i, g := range groupBy {
		expr, ok := groupExpr[strings.TrimSpace(g)]
		if !ok {
			return nil, httpx.BadRequest("不支持的分组维度: "+g,
				map[string]any{"supported": SupportedDimensions()})
		}
		alias := "g_" + sanitize(g) + "_" + strconv.Itoa(i)
		selects = append(selects, expr+" AS "+alias)
		groupExprs = append(groupExprs, expr) // GROUP BY 用原始表达式（SQLite 不接受别名）
		keyAliases = append(keyAliases, alias)
	}

	where, args := buildWhere(f)

	// 总计
	totalQ := `SELECT COUNT(*) AS form_count,
		COALESCE(SUM(f.total_qty),0) AS total_qty,
		COALESCE(SUM(f.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(f.defect_qty),0) AS defect_qty
		FROM forms f WHERE ` + where

	var total GroupResult
	if err := s.db.GetContext(ctx, &total, s.rebind(totalQ), args...); err != nil {
		return nil, fmt.Errorf("统计总计失败: %w", err)
	}
	total.PassRate = rate(total.QualifiedQty, total.TotalQty)

	res := &SummaryResult{GroupBy: groupBy, Groups: []GroupResult{}, Total: total}
	res.Note = "不良缺陷子项合计可能大于不合格品数（同一片镜片可计入多个类别，见需求 Q5）"

	if len(selects) == 0 {
		return res, nil
	}

	q := `SELECT ` + strings.Join(selects, ", ") + `,
		COUNT(*) AS form_count,
		COALESCE(SUM(f.total_qty),0) AS total_qty,
		COALESCE(SUM(f.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(f.defect_qty),0) AS defect_qty
		FROM forms f
		WHERE ` + where +
		` GROUP BY ` + strings.Join(groupExprs, ", ") +
		` ORDER BY form_count DESC LIMIT 500`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("分组统计失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("读取统计列失败: %w", err)
	}
	index := make(map[string]int, len(cols))
	for i, c := range cols {
		index[strings.ToLower(c)] = i
	}

	for rows.Next() {
		scan := make([]any, len(cols))
		holders := make([]any, len(cols))
		for i := range holders {
			holders[i] = &scan[i]
		}
		if err := rows.Scan(holders...); err != nil {
			return nil, fmt.Errorf("读取统计行失败: %w", err)
		}
		var g GroupResult
		parts := make([]string, 0, len(keyAliases))
		for _, alias := range keyAliases {
			if i, ok := index[strings.ToLower(alias)]; ok {
				parts = append(parts, sqlToString(scan[i]))
			}
		}
		g.Key = strings.Join(parts, " | ")
		g.FormCount = sqlToInt(scan[index["form_count"]])
		g.TotalQty = sqlToInt(scan[index["total_qty"]])
		g.QualifiedQty = sqlToInt(scan[index["qualified_qty"]])
		g.DefectQty = sqlToInt(scan[index["defect_qty"]])
		g.PassRate = rate(g.QualifiedQty, g.TotalQty)
		res.Groups = append(res.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历统计结果失败: %w", err)
	}
	return res, nil
}

// DefectBreakdown 返回各不良缺陷列 × 表面的合计（按明细行求和）。
func (s *Service) DefectBreakdown(ctx context.Context, f Filter) (map[string]any, error) {
	where, args := buildWhere(f)

	fieldNames := formfields.DefectFieldNames()
	selects := make([]string, 0, len(fieldNames))
	for _, name := range fieldNames {
		selects = append(selects, "COALESCE(SUM(l."+name+"),0) AS "+name)
	}
	q := `SELECT ` + strings.Join(selects, ", ") + `
		FROM form_lines l JOIN forms f ON f.id = l.form_id
		WHERE ` + where

	vals := make([]int, len(selects))
	holders := make([]any, len(selects))
	for i := range vals {
		holders[i] = &vals[i]
	}
	if err := s.db.QueryRowxContext(ctx, s.rebind(q), args...).Scan(holders...); err != nil {
		return nil, fmt.Errorf("不良缺陷分布统计失败: %w", err)
	}

	defects := make(map[string]any, len(formfields.DefectGroups))
	grandTotal := 0
	for _, d := range formfields.DefectGroups {
		surfaces := map[string]int{}
		total := 0
		for _, sf := range formfields.Surfaces {
			name := formfields.DefectFieldName(d.Code, sf)
			v := 0
			for i, n := range fieldNames {
				if n == name {
					v = vals[i]
					break
				}
			}
			surfaces[sf] = v
			total += v
		}
		surfaces["total"] = total
		grandTotal += total
		defects[d.Code] = map[string]any{
			"label":    d.Label,
			"surfaces": surfaces,
		}
	}

	return map[string]any{
		"defects":     defects,
		"grand_total": grandTotal,
		"surfaces":    formfields.Surfaces,
		"note": "每列为 凹面/凸面/双面 三项；同一片镜片可能被计入多个缺陷类别" +
			"（需求 Q5，登记待定）",
	}, nil
}

// IncompleteForm 是"未填完整清单"的一项。
type IncompleteForm struct {
	FormID        string  `db:"id" json:"id"`
	FormNo        string  `db:"form_no" json:"form_no"`
	OperationCode string  `db:"operation_code" json:"operation_code"`
	MissingFields *string `db:"missing_fields" json:"missing_fields,omitempty"`
	LastSavedAt   *string `db:"last_saved_at" json:"last_saved_at,omitempty"`
	CreatedAt     string  `db:"created_at" json:"created_at"`
}

// IncompleteForms 返回未填完整的表单清单。
func (s *Service) IncompleteForms(ctx context.Context, limit int) ([]IncompleteForm, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, form_no, operation_code, missing_fields, last_saved_at, created_at
		FROM forms WHERE state = 'ACTIVE' AND is_complete = 0
		ORDER BY created_at DESC LIMIT ?`

	var out []IncompleteForm
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), limit); err != nil {
		return nil, fmt.Errorf("查询未完成表单失败: %w", err)
	}
	return out, nil
}

// Workload 是按录入人的工作量。
type Workload struct {
	Key          string  `json:"key"`
	FormCount    int     `json:"form_count"`
	TotalQty     int     `json:"total_qty"`
	QualifiedQty int     `json:"qualified_qty"`
	DefectQty    int     `json:"defect_qty"`
	PassRate     float64 `json:"pass_rate"`
}

type workloadRow struct {
	Key          string `db:"g_key"`
	FormCount    int    `db:"form_count"`
	TotalQty     int    `db:"total_qty"`
	QualifiedQty int    `db:"qualified_qty"`
	DefectQty    int    `db:"defect_qty"`
}

// WorkloadBy 返回按指定人员字段（submitted_by / last_saved_by）的工作量。
// 说明：系统只记录"实际录入人"，因此按人统计以这两个字段为准。
func (s *Service) WorkloadBy(ctx context.Context, f Filter, field string) ([]Workload, error) {
	col := map[string]string{
		"submitted_by":  "f.submitted_by",
		"last_saved_by": "f.last_saved_by",
	}[field]
	if col == "" {
		return nil, httpx.BadRequest("不支持的人员维度: "+field+"（可选 submitted_by | last_saved_by）")
	}
	where, args := buildWhere(f)
	q := `SELECT COALESCE(` + col + `, '(未填)') AS g_key, COUNT(*) AS form_count,
		COALESCE(SUM(f.total_qty),0) AS total_qty,
		COALESCE(SUM(f.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(f.defect_qty),0) AS defect_qty
		FROM forms f WHERE ` + where +
		` GROUP BY COALESCE(` + col + `, '(未填)') ORDER BY form_count DESC LIMIT 200`

	var rowsOut []workloadRow
	if err := s.db.SelectContext(ctx, &rowsOut, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("工作量统计失败: %w", err)
	}
	out := make([]Workload, 0, len(rowsOut))
	for _, r := range rowsOut {
		out = append(out, Workload{
			Key: r.Key, FormCount: r.FormCount, TotalQty: r.TotalQty,
			QualifiedQty: r.QualifiedQty, DefectQty: r.DefectQty,
			PassRate: rate(r.QualifiedQty, r.TotalQty),
		})
	}
	return out, nil
}

// ------------------------------- 内部 -------------------------------

func buildWhere(f Filter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	if f.OnlyActive {
		clauses = append(clauses, "f.state = 'ACTIVE'")
	}
	if f.From != "" {
		clauses = append(clauses, "COALESCE(f.paper_submitted_at, f.submitted_at, f.created_at) >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		clauses = append(clauses, "COALESCE(f.paper_submitted_at, f.submitted_at, f.created_at) <= ?")
		args = append(args, f.To+"T23:59:59Z")
	}
	add := func(col, val string) {
		if v := strings.TrimSpace(val); v != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, v)
		}
	}
	add("f.operation_code", f.OperationCode)
	add("f.sample_project", f.Project)
	add("f.sample_model", f.Model)
	add("f.sample_spec", f.Spec)
	add("f.sample_size", f.Size)
	add("f.sample_side", f.Side)
	add("f.paper_inspector", f.Inspector)
	add("f.paper_submitter", f.Submitter)
	add("f.submitted_by", f.SubmittedBy)
	if v := strings.TrimSpace(f.Unit); v != "" {
		clauses = append(clauses, "COALESCE(f.paper_unit_text, f.paper_unit_id) = ?")
		args = append(args, v)
	}
	return strings.Join(clauses, " AND "), args
}

func (s *Service) rebind(q string) string { return s.db.Rebind(q) }

func rate(qualified, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(int64(float64(qualified)/float64(total)*10000+0.5)) / 100
}

func surfaceOf(field string) string {
	for _, sf := range formfields.Surfaces {
		if strings.HasSuffix(field, "_"+sf) {
			return sf
		}
	}
	return ""
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			out = append(out, r)
		}
	}
	return string(out)
}

func sqlToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339)
	default:
		return fmt.Sprint(t)
	}
}

func sqlToInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	case []byte:
		return parseIntBytes(t)
	default:
		return 0
	}
}

func parseIntBytes(b []byte) int {
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
