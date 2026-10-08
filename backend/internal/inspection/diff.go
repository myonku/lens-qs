package inspection

import (
	"reflect"

	"github.com/myonku/lens-qs-api/internal/formfields"
)

// FieldChange 是一条字段级变更。
type FieldChange struct {
	Scope string `json:"scope"`           // submission | line
	Field string `json:"field"`           // 字段名（表头栏目或扁平缺陷列名）
	Label string `json:"label,omitempty"` // 中文名
	Index int    `json:"index,omitempty"` // 明细行序号（scope=line 时有效）
	From  any    `json:"from"`
	To    any    `json:"to"`
}

// submissionScalarFields 参与差异比较的表头字段。
var submissionScalarFields = []struct {
	Field string
	Get   func(Submission) any
}{
	{"operation_code", func(s Submission) any { return s.OperationCode }},
	{"operation_text", func(s Submission) any { return deref(s.OperationText) }},
	{"total_qty", func(s Submission) any { return s.TotalQty }},
	{"qualified_qty", func(s Submission) any { return s.QualifiedQty }},
	{"defect_qty", func(s Submission) any { return s.DefectQty }},
	{"pass_rate", func(s Submission) any { return derefFloat(s.PassRate) }},
	{"sample_raw", func(s Submission) any { return s.SampleRaw }},
	{"sample_project", func(s Submission) any { return deref(s.SampleProject) }},
	{"sample_model", func(s Submission) any { return deref(s.SampleModel) }},
	{"sample_spec", func(s Submission) any { return deref(s.SampleSpec) }},
	{"sample_size", func(s Submission) any { return deref(s.SampleSize) }},
	{"sample_side", func(s Submission) any { return deref(s.SampleSide) }},

	// 纸质表单原有的表头栏目（B-11）
	{"paper_unit_id", func(s Submission) any { return deref(s.PaperUnitID) }},
	{"paper_unit_text", func(s Submission) any { return deref(s.PaperUnitText) }},
	{"paper_submitter", func(s Submission) any { return deref(s.PaperSubmitter) }},
	{"paper_submitted_at", func(s Submission) any { return deref(s.PaperSubmittedAt) }},
	{"paper_inspector", func(s Submission) any { return deref(s.PaperInspector) }},
	{"paper_inspected_at", func(s Submission) any { return deref(s.PaperInspectedAt) }},
	{"paper_machine_operator", func(s Submission) any { return deref(s.PaperMachineOperator) }},
	{"paper_handover_by", func(s Submission) any { return deref(s.PaperHandoverBy) }},

	{"remark", func(s Submission) any { return deref(s.Remark) }},
	{"is_complete", func(s Submission) any { return s.IsComplete }},
	{"missing_fields", func(s Submission) any { return s.MissingFields }},
	{"save_count", func(s Submission) any { return s.SaveCount }},
	{"submitted_by", func(s Submission) any { return deref(s.SubmittedBy) }},
	{"submitted_at", func(s Submission) any { return deref(s.SubmittedAt) }},
	{"last_saved_by", func(s Submission) any { return deref(s.LastSavedBy) }},
	{"last_saved_at", func(s Submission) any { return deref(s.LastSavedAt) }},
}

// DiffSubmissions 比较两版提交（含明细）的差异，用于"这次改了什么"。
func DiffSubmissions(from, to *Submission, fromLines, toLines []Line) []FieldChange {
	out := make([]FieldChange, 0, 8)
	if from == nil && to == nil {
		return out
	}
	if from == nil {
		from = &Submission{}
	}
	if to == nil {
		to = &Submission{}
	}

	for _, f := range submissionScalarFields {
		a, b := f.Get(*from), f.Get(*to)
		if !reflect.DeepEqual(a, b) {
			out = append(out, FieldChange{
				Scope: "submission", Field: f.Field, Label: formfields.FieldLabel(f.Field),
				From: a, To: b,
			})
		}
	}

	// 明细：按位置配对比较
	maxLen := len(fromLines)
	if len(toLines) > maxLen {
		maxLen = len(toLines)
	}
	for i := 0; i < maxLen; i++ {
		idx := i + 1
		switch {
		case i >= len(fromLines):
			out = append(out, FieldChange{Scope: "line", Field: "lines", Label: "明细行",
				Index: idx, From: nil, To: summarizeLine(toLines[i])})
		case i >= len(toLines):
			out = append(out, FieldChange{Scope: "line", Field: "lines", Label: "明细行",
				Index: idx, From: summarizeLine(fromLines[i]), To: nil})
		default:
			out = append(out, diffLine(idx, fromLines[i], toLines[i])...)
		}
	}
	return out
}

func diffLine(idx int, a, b Line) []FieldChange {
	out := make([]FieldChange, 0, 4)
	add := func(field string, from, to any) {
		if !reflect.DeepEqual(from, to) {
			out = append(out, FieldChange{
				Scope: "line", Field: field, Label: formfields.FieldLabel(field),
				Index: idx, From: from, To: to,
			})
		}
	}
	add("total_qty", a.TotalQty, b.TotalQty)
	add("qualified_qty", a.QualifiedQty, b.QualifiedQty)
	add("defect_qty", a.DefectQty, b.DefectQty)
	add("remark", deref(a.Remark), deref(b.Remark))

	// 缺陷列：逐格比较（"未填"与 0 在库里都表现为缺少键，统一按 0 处理）
	for _, d := range formfields.DefectGroups {
		for _, s := range formfields.Surfaces {
			av := a.Defects[d.Code][s]
			bv := b.Defects[d.Code][s]
			if av != bv {
				add(formfields.DefectFieldName(d.Code, s), av, bv)
			}
		}
	}
	return out
}

func summarizeLine(l Line) string {
	return "检验总数 " + itoa(l.TotalQty) + " / 合格品 " + itoa(l.QualifiedQty) +
		" / 不合格品 " + itoa(l.DefectQty)
}

func deref(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
