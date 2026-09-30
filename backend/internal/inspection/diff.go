package inspection

import (
	"reflect"
	"strconv"
)

// FieldChange 是一条字段级变更。
type FieldChange struct {
	Scope string `json:"scope"`           // record | line
	Field string `json:"field"`           // 字段名（数据库列名）
	Label string `json:"label,omitempty"` // 中文名
	Index int    `json:"index,omitempty"` // 明细行序号（scope=line 时有效）
	From  any    `json:"from"`
	To    any    `json:"to"`
}

// DiffRecords 比较两版记录（含明细）的差异，用于"这次改了什么"。
func DiffRecords(from *FormRecord, fromLines []RecordLine, to *FormRecord, toLines []RecordLine) []FieldChange {
	out := make([]FieldChange, 0, 8)
	if from == nil && to == nil {
		return out
	}
	if from == nil {
		from = &FormRecord{}
	}
	if to == nil {
		to = &FormRecord{}
	}

	out = append(out, diffStruct("record", 0, *from, *to,
		"total_qty", "qualified_qty", "defect_qty", "pass_rate",
		"is_complete", "missing_fields", "save_count",
		"last_saved_by", "last_saved_at")...)

	// 明细：按 seq 配对比较，长度不同也报出来
	max := len(fromLines)
	if len(toLines) > max {
		max = len(toLines)
	}
	for i := 0; i < max; i++ {
		idx := i + 1
		if i >= len(fromLines) {
			out = append(out, FieldChange{Scope: "line", Field: "lines", Label: "明细行", Index: idx,
				From: nil, To: summarizeLine(toLines[i])})
			continue
		}
		if i >= len(toLines) {
			out = append(out, FieldChange{Scope: "line", Field: "lines", Label: "明细行", Index: idx,
				From: summarizeLine(fromLines[i]), To: nil})
			continue
		}
		out = append(out, diffStruct("line", idx, fromLines[i], toLines[i],
			"total_qty", "qualified_qty", "defect_qty",
			"spot_concave", "spot_convex", "spot_both",
			"scratch_concave", "scratch_convex", "scratch_both",
			"dirt_concave", "dirt_convex", "dirt_both",
			"hard_spot_concave", "hard_spot_convex", "hard_spot_both",
			"debris_concave", "debris_convex", "debris_both",
			"process_code", "process_text", "remark")...)
	}
	return out
}

func diffStruct(scope string, index int, from, to any, only ...string) []FieldChange {
	fv := reflect.ValueOf(from)
	tv := reflect.ValueOf(to)
	ft := fv.Type()
	changes := make([]FieldChange, 0, 4)

	// 允许只比较指定字段（为空则比较全部可导出字段）
	filter := map[string]bool{}
	for _, f := range only {
		filter[f] = true
	}

	for i := 0; i < ft.NumField(); i++ {
		sf := ft.Field(i)
		col := columnOf(sf)
		if col == "" || col == "-" {
			continue
		}
		if len(filter) > 0 && !filter[col] {
			continue
		}
		if !sf.IsExported() {
			continue
		}
		a := fv.Field(i).Interface()
		b := tv.Field(i).Interface()
		if !reflect.DeepEqual(a, b) {
			changes = append(changes, FieldChange{
				Scope: scope, Field: col, Label: Label(col), Index: index,
				From: normalizeVal(a), To: normalizeVal(b),
			})
		}
	}
	return changes
}

func normalizeVal(v any) any {
	switch t := v.(type) {
	case *string:
		if t == nil {
			return nil
		}
		return *t
	case *int:
		if t == nil {
			return nil
		}
		return *t
	case *float64:
		if t == nil {
			return nil
		}
		return *t
	case *bool:
		if t == nil {
			return nil
		}
		return *t
	case []byte:
		return string(t)
	default:
		return v
	}
}

func summarizeLine(l RecordLine) string {
	return "总数 " + strconv.Itoa(l.TotalQty) +
		" / 合格 " + strconv.Itoa(l.QualifiedQty) +
		" / 不良 " + strconv.Itoa(l.DefectQty)
}
