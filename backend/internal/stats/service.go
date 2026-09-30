// Package stats 实现汇总统计（需求 §4.7，本期先提供基础能力）。
//
// 汇总按「笔」聚合（每笔已有落库的 total_qty / qualified_qty / defect_qty），
// 分组维度取自样品信息各子项、工序、单位、检验人、日期。
package stats

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 允许的分组维度 → 表达式
var groupExpr = map[string]string{
	"sample_project": "f.sample_project",
	"sample_model":   "f.sample_model",
	"sample_spec":    "f.sample_spec",
	"sample_size":    "f.sample_size",
	"sample_side":    "f.sample_side",
	"sample_class":   "f.sample_class",
	"sample_raw":     "f.sample_raw",
	"form_no":        "f.form_no",
	"process":        "r.process_code",
	"unit":           "COALESCE(f.unit_text, f.unit_id)",
	"inspected_by":   "r.inspected_by",
	"submitted_by":   "r.submitted_by",
	"machine_operator": "r.machine_operator",
	"handover_by":      "r.handover_by",
	"day":              "substr(r.inspected_at, 1, 10)",
	"month":            "substr(r.inspected_at, 1, 7)",
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
	From        string
	To          string
	Process     string
	Project     string
	Model       string
	Spec        string
	Size        string
	Side        string
	InspectedBy string
	Unit        string
	OnlyActive  bool
}

// GroupResult 是一个分组的结果。
type GroupResult struct {
	Key          string  `db:"g_key" json:"key"`
	RecordCount  int     `db:"record_count" json:"record_count"`
	TotalQty     int     `db:"total_qty" json:"total_qty"`
	QualifiedQty int     `db:"qualified_qty" json:"qualified_qty"`
	DefectQty    int     `db:"defect_qty" json:"defect_qty"`
	PassRate     float64 `db:"-" json:"pass_rate"`
}

// SummaryResult 是汇总返回。
type SummaryResult struct {
	GroupBy  []string      `json:"group_by"`
	Groups   []GroupResult `json:"groups"`
	Total    GroupResult   `json:"total"`
	Note     string        `json:"note,omitempty"`
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
		// GROUP BY 用原始表达式（SQLite 不接受按输出别名分组）
		groupExprs = append(groupExprs, expr)
		keyAliases = append(keyAliases, alias)
	}

	where, args := buildWhere(f, false)

	// 总计
	totalQ := `SELECT COUNT(*) AS record_count,
		COALESCE(SUM(r.total_qty),0) AS total_qty,
		COALESCE(SUM(r.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(r.defect_qty),0) AS defect_qty
		FROM form_records r JOIN forms f ON f.id = r.form_id WHERE ` + where

	var total GroupResult
	if err := s.db.GetContext(ctx, &total, s.rebind(totalQ), args...); err != nil {
		return nil, fmt.Errorf("统计总计失败: %w", err)
	}
	total.PassRate = rate(total.QualifiedQty, total.TotalQty)

	res := &SummaryResult{GroupBy: groupBy, Groups: []GroupResult{}, Total: total}
	res.Note = "不良子项合计可能大于不良品数（同一片镜片可计入多个类别，见需求 Q5）"

	if len(selects) == 0 {
		return res, nil
	}

	q := `SELECT ` + strings.Join(selects, ", ") + `,
		COUNT(*) AS record_count,
		COALESCE(SUM(r.total_qty),0) AS total_qty,
		COALESCE(SUM(r.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(r.defect_qty),0) AS defect_qty
		FROM form_records r JOIN forms f ON f.id = r.form_id
		WHERE ` + where +
		` GROUP BY ` + strings.Join(groupExprs, ", ") +
		` ORDER BY record_count DESC LIMIT 500`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("分组统计失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("读取统计列失败: %w", err)
	}
	// 按别名定位列，避免依赖驱动返回的列名大小写
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
		keyParts := make([]string, 0, len(keyAliases))
		for _, alias := range keyAliases {
			if i, ok := index[strings.ToLower(alias)]; ok {
				keyParts = append(keyParts, sqlToString(scan[i]))
			}
		}
		g.Key = strings.Join(keyParts, " | ")
		g.RecordCount = sqlToInt(scan[index["record_count"]])
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

// DefectBreakdown 返回各不良类别 × 表面的合计（按行汇总）。
func (s *Service) DefectBreakdown(ctx context.Context, f Filter) (map[string]any, error) {
	where, args := buildWhere(f, true)
	q := `SELECT
		COALESCE(SUM(l.spot_concave),0), COALESCE(SUM(l.spot_convex),0), COALESCE(SUM(l.spot_both),0),
		COALESCE(SUM(l.scratch_concave),0), COALESCE(SUM(l.scratch_convex),0), COALESCE(SUM(l.scratch_both),0),
		COALESCE(SUM(l.dirt_concave),0), COALESCE(SUM(l.dirt_convex),0), COALESCE(SUM(l.dirt_both),0),
		COALESCE(SUM(l.hard_spot_concave),0), COALESCE(SUM(l.hard_spot_convex),0), COALESCE(SUM(l.hard_spot_both),0),
		COALESCE(SUM(l.debris_concave),0), COALESCE(SUM(l.debris_convex),0), COALESCE(SUM(l.debris_both),0)
		FROM record_lines l
		JOIN form_records r ON r.id = l.record_id
		JOIN forms f ON f.id = r.form_id
		WHERE ` + where

	vals := make([]int, 15)
	dests := make([]any, 15)
	for i := range vals {
		dests[i] = &vals[i]
	}
	if err := s.db.QueryRowxContext(ctx, s.rebind(q), args...).Scan(dests...); err != nil {
		return nil, fmt.Errorf("不良分布统计失败: %w", err)
	}
	return map[string]any{
		"point":     surface(vals[0], vals[1], vals[2]),
		"scratch":   surface(vals[3], vals[4], vals[5]),
		"dirt":      surface(vals[6], vals[7], vals[8]),
		"hard_spot": surface(vals[9], vals[10], vals[11]),
		"debris":    surface(vals[12], vals[13], vals[14]),
		"note":      "同一片镜片可能被计入多个不良类别（需求 Q5，待定）",
	}, nil
}

// IncompleteForm 是"未完成表单清单"的一项。
type IncompleteForm struct {
	FormID       string  `db:"form_id" json:"form_id" db:"form_id"`
	FormNo       string  `db:"form_no" json:"form_no"`
	RecordID     string  `db:"record_id" json:"record_id"`
	SequenceNo   int     `db:"sequence_no" json:"sequence_no"`
	ProcessCode  *string `db:"process_code" json:"process_code,omitempty"`
	MissingField *string `db:"missing_fields" json:"missing_fields,omitempty"`
	LastSavedAt  *string `db:"last_saved_at" json:"last_saved_at,omitempty"`
}

// IncompleteForms 返回存在未填完整笔的表单清单。
func (s *Service) IncompleteForms(ctx context.Context, limit int) ([]IncompleteForm, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT f.id AS form_id, f.form_no, r.id AS record_id, r.sequence_no,
		r.process_code, r.missing_fields, r.last_saved_at
		FROM form_records r JOIN forms f ON f.id = r.form_id
		WHERE r.state = 'ACTIVE' AND r.is_complete = 0 AND f.state = 'ACTIVE'
		ORDER BY r.last_saved_at DESC LIMIT ?`

	var out []IncompleteForm
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), limit); err != nil {
		return nil, fmt.Errorf("查询未完成表单失败: %w", err)
	}
	return out, nil
}

// Workload 是按检验人 / 录入人的工作量。
type Workload struct {
	Key          string  `json:"key"`
	RecordCount  int     `json:"record_count"`
	TotalQty     int     `json:"total_qty"`
	QualifiedQty int     `json:"qualified_qty"`
	DefectQty    int     `json:"defect_qty"`
	PassRate     float64 `json:"pass_rate"`
}

// workloadRow 是查询行（用 db 标签对齐列名）。
type workloadRow struct {
	Key          string `db:"g_key"`
	RecordCount  int    `db:"record_count"`
	TotalQty     int    `db:"total_qty"`
	QualifiedQty int    `db:"qualified_qty"`
	DefectQty    int    `db:"defect_qty"`
}

// WorkloadBy 返回按指定人员字段（inspected_by / recorded_by / last_saved_by）的工作量。
func (s *Service) WorkloadBy(ctx context.Context, f Filter, field string) ([]Workload, error) {
	col := map[string]string{
		"inspected_by":  "r.inspected_by",
		"recorded_by":   "r.recorded_by",
		"last_saved_by": "r.last_saved_by",
	}[field]
	if col == "" {
		return nil, httpx.BadRequest("不支持的人员维度: "+field)
	}
	where, args := buildWhere(f, false)
	q := `SELECT COALESCE(` + col + `, '(未填)') AS g_key, COUNT(*) AS record_count,
		COALESCE(SUM(r.total_qty),0) AS total_qty,
		COALESCE(SUM(r.qualified_qty),0) AS qualified_qty,
		COALESCE(SUM(r.defect_qty),0) AS defect_qty
		FROM form_records r JOIN forms f ON f.id = r.form_id
		WHERE ` + where + ` GROUP BY COALESCE(` + col + `, '(未填)') ORDER BY record_count DESC LIMIT 200`

	var rowsOut []workloadRow
	if err := s.db.SelectContext(ctx, &rowsOut, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("工作量统计失败: %w", err)
	}
	out := make([]Workload, 0, len(rowsOut))
	for _, r := range rowsOut {
		out = append(out, Workload{
			Key: r.Key, RecordCount: r.RecordCount, TotalQty: r.TotalQty,
			QualifiedQty: r.QualifiedQty, DefectQty: r.DefectQty,
			PassRate: rate(r.QualifiedQty, r.TotalQty),
		})
	}
	return out, nil
}

// ------------------------------- 内部 -------------------------------

func buildWhere(f Filter, withLines bool) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	if f.OnlyActive {
		clauses = append(clauses, "r.state = 'ACTIVE'", "f.state = 'ACTIVE'")
	}
	if f.From != "" {
		clauses = append(clauses, "COALESCE(r.inspected_at, r.created_at) >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		clauses = append(clauses, "COALESCE(r.inspected_at, r.created_at) <= ?")
		args = append(args, f.To+"T23:59:59Z")
	}
	add := func(col, val string) {
		if strings.TrimSpace(val) != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, strings.TrimSpace(val))
		}
	}
	add("r.process_code", f.Process)
	add("f.sample_project", f.Project)
	add("f.sample_model", f.Model)
	add("f.sample_spec", f.Spec)
	add("f.sample_size", f.Size)
	add("f.sample_side", f.Side)
	add("r.inspected_by", f.InspectedBy)
	if strings.TrimSpace(f.Unit) != "" {
		clauses = append(clauses, "COALESCE(f.unit_text, f.unit_id) = ?")
		args = append(args, strings.TrimSpace(f.Unit))
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

func surface(concave, convex, both int) map[string]int {
	return map[string]int{"concave": concave, "convex": convex, "both": both, "total": concave + convex + both}
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
		n := 0
		for _, b := range t {
			if b < '0' || b > '9' {
				return n
			}
			n = n*10 + int(b-'0')
		}
		return n
	default:
		return 0
	}
}
