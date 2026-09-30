package inspection

import (
	"strconv"
	"strings"
	"time"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 校验规则编号（对应 docs/requirements.md §4.1.3）
const (
	RuleSumQualifiedPlusDefect = "V-1"  // 合格 + 不良 = 总数
	RuleDefectSubtotal         = "V-2"  // 15 个子项之和 = 不良品（恒为警告）
	RuleDefectBoundary         = "V-3"  // 子项边界
	RuleNonNegative            = "V-4"  // 非负整数
	RulePassRate               = "V-5"  // 良率派生（仅计算）
	RuleTimeOrder              = "V-6"  // 时间顺序（恒为警告）
	RuleUniqueFormNo           = "V-7"  // 唯一性（唯一硬阻断）
	RuleSampleNotMatched       = "V-8"  // 样品信息与参考清单不匹配（恒为警告）
	RuleSampleProjectMissing   = "V-9"  // 结构化项缺失（恒为警告）
	RulePaperFieldMissing      = "V-10" // 表单既有栏目缺失（恒为警告）
)

// FormPaperFields 是表单既有栏目在「笔」上的字段名（B-11）。
var FormPaperFields = []string{
	"submitted_by",
	"submitted_at",
	"inspected_by",
	"inspected_at",
	"machine_operator",
	"handover_by",
}

// 字段列名 → 中文名（用于缺失提示）
var fieldLabels = map[string]string{
	"form_no":          "表单号",
	"sample_raw":       "样品信息",
	"sample_project":   "项目",
	"sample_model":     "型号",
	"sample_spec":      "规格",
	"sample_size":      "大小",
	"sample_side":      "左右",
	"unit_id":          "单位",
	"unit_text":        "单位",
	"process_code":     "工序",
	"submitted_by":     "送检人",
	"submitted_at":     "送检时间",
	"inspected_by":     "检验人",
	"inspected_at":     "检验时间",
	"machine_operator": "操机员",
	"handover_by":      "交接员",
	"lines":            "计数明细",
}

// Label 返回字段的中文名（未知字段返回原名）。
func Label(field string) string {
	if l, ok := fieldLabels[field]; ok {
		return l
	}
	return field
}

// Enforce 描述校验强度。
type Enforce string

const (
	// EnforceWarn 是默认强度：只提示，不阻断（最大灵活性原则 F-7）。
	EnforceWarn Enforce = "warn"
	// EnforceStrict 由调用方显式要求：把自相矛盾的数据拦下来。
	EnforceStrict Enforce = "strict"
)

// ParseEnforce 解析 enforce 参数；缺省或非法值都回落到 warn。
func ParseEnforce(s string) Enforce {
	if strings.EqualFold(strings.TrimSpace(s), string(EnforceStrict)) {
		return EnforceStrict
	}
	return EnforceWarn
}

// ValidateLines 校验一笔的明细行。
//
// 默认全部只警告；enforce=strict 时 V-1/V-3/V-4 升级为阻断错误。
// V-2 在任何模式下都不阻断（业务方已确认同一片镜片可计入多个不良类别，登记为待定）。
func ValidateLines(lines []RecordLine, enforce Enforce) []httpx.Issue {
	issues := make([]httpx.Issue, 0, 8)
	strict := enforce == EnforceStrict
	levelFor := func() string {
		if strict {
			return "error"
		}
		return "warning"
	}

	for i, l := range lines {
		row := strconv.Itoa(i + 1)

		// V-4 非负
		if negative := negativeCounts(l); len(negative) > 0 {
			issues = append(issues, httpx.Issue{
				Rule: RuleNonNegative, Level: levelFor(),
				Field:   lineField(i, negative[0]),
				Message: "第 " + row + " 行的「" + Label(negative[0]) + "」为负值",
			})
		}

		// V-1 合格 + 不良 = 总数（三者都还没填时不提示）
		if !(l.TotalQty == 0 && l.QualifiedQty == 0 && l.DefectQty == 0) {
			if l.QualifiedQty+l.DefectQty != l.TotalQty {
				issues = append(issues, httpx.Issue{
					Rule: RuleSumQualifiedPlusDefect, Level: levelFor(),
					Field: lineField(i, "total_qty"),
					Message: "第 " + row + " 行：合格品(" + strconv.Itoa(l.QualifiedQty) +
						") + 不良品(" + strconv.Itoa(l.DefectQty) + ") 与检验总数(" +
						strconv.Itoa(l.TotalQty) + ") 不一致",
				})
			}
		}

		// V-3 子项边界
		if len(negativeCounts(l)) == 0 {
			for _, g := range DefectGroups {
				sum := g.Concave(l) + g.Convex(l) + g.Both(l)
				if l.DefectQty > 0 && sum > l.DefectQty {
					issues = append(issues, httpx.Issue{
						Rule: RuleDefectBoundary, Level: levelFor(),
						Field: lineField(i, "defect_qty"),
						Message: "第 " + row + " 行：「" + g.Name + "」合计(" + strconv.Itoa(sum) +
							") 超过不良品数(" + strconv.Itoa(l.DefectQty) + ")",
					})
				}
				if l.TotalQty > 0 && (g.Concave(l) > l.TotalQty || g.Convex(l) > l.TotalQty || g.Both(l) > l.TotalQty) {
					issues = append(issues, httpx.Issue{
						Rule: RuleDefectBoundary, Level: levelFor(),
						Field: lineField(i, "total_qty"),
						Message: "第 " + row + " 行：「" + g.Name + "」的某个子项超过检验总数",
					})
				}
			}
		}

		// V-2 恒为警告
		if !(l.DefectQty == 0 && DefectTotal(l) == 0) && DefectTotal(l) != l.DefectQty {
			issues = append(issues, httpx.Issue{
				Rule: RuleDefectSubtotal, Level: "warning",
				Field: lineField(i, "defect_qty"),
				Message: "第 " + row + " 行：五类不良子项合计(" + strconv.Itoa(DefectTotal(l)) +
					") 与不良品数(" + strconv.Itoa(l.DefectQty) +
					") 不一致（可能同一片镜片被计入多个类别）",
			})
		}
	}
	return issues
}

// ValidateRecord 校验一笔记录的笔级字段（V-6 / V-9 / V-10，全部只警告）。
func ValidateRecord(rec FormRecord, form *Form, now time.Time) []httpx.Issue {
	issues := make([]httpx.Issue, 0, 4)

	// V-6 送检时间 ≤ 检验时间 ≤ 当前时间（纸面时间常为人工补记，不阻断）
	if str(rec.SubmittedAt) != "" && str(rec.InspectedAt) != "" && str(rec.SubmittedAt) > str(rec.InspectedAt) {
		issues = append(issues, httpx.Issue{
			Rule: RuleTimeOrder, Level: "warning", Field: "submitted_at",
			Message: "送检时间(" + str(rec.SubmittedAt) + ") 晚于检验时间(" + str(rec.InspectedAt) + ")",
		})
	}
	if s := str(rec.InspectedAt); s != "" {
		if t := parseAnyTime(s); !t.IsZero() && t.After(now) {
			issues = append(issues, httpx.Issue{
				Rule: RuleTimeOrder, Level: "warning", Field: "inspected_at",
				Message: "检验时间(" + s + ") 晚于当前时间",
			})
		}
	}

	// V-9 结构化项缺失（项目 M1/M2）
	if form != nil && str(form.SampleProject) == "" {
		issues = append(issues, httpx.Issue{
			Rule: RuleSampleProjectMissing, Level: "warning", Field: "sample_project",
			Message: "样品信息的「项目」（M1/M2 等）尚未填写",
		})
	}

	// V-10 表单既有栏目缺失（仅在该笔"已经开始填"时提示，避免噪声）
	if recordTouched(rec) {
		for _, f := range MissingPaperFields(rec) {
			issues = append(issues, httpx.Issue{
				Rule: RulePaperFieldMissing, Level: "warning", Field: f,
				Message: "表单既有栏目「" + Label(f) + "」尚未填写",
			})
		}
	}
	return issues
}

// ValidateForm 校验表单级字段（唯一性由数据库约束保证，见 V-7）。
func ValidateForm(f Form) []httpx.Issue {
	issues := make([]httpx.Issue, 0, 3)

	if f.NeedsReview {
		msg := "样品信息与参考部件号清单不匹配，已标记待核对（不影响保存）"
		if str(f.SampleUnmatchedReason) != "" {
			msg += "，原因: " + str(f.SampleUnmatchedReason)
		}
		issues = append(issues, httpx.Issue{
			Rule: RuleSampleNotMatched, Level: "warning", Field: "sample_raw", Message: msg,
		})
	}
	if strings.TrimSpace(f.SampleRaw) == "" {
		issues = append(issues, httpx.Issue{
			Rule: RulePaperFieldMissing, Level: "warning", Field: "sample_raw",
			Message: "「样品信息」尚未填写",
		})
	}
	if str(f.UnitID) == "" && str(f.UnitText) == "" {
		issues = append(issues, httpx.Issue{
			Rule: RulePaperFieldMissing, Level: "warning", Field: "unit_id",
			Message: "「单位」尚未填写",
		})
	}
	return issues
}

// MissingPaperFields 返回该笔缺失的表单既有栏目的列名。
func MissingPaperFields(rec FormRecord) []string {
	missing := make([]string, 0, len(FormPaperFields))
	add := func(field string, v *string) {
		if str(v) == "" {
			missing = append(missing, field)
		}
	}
	add("submitted_by", rec.SubmittedBy)
	add("submitted_at", rec.SubmittedAt)
	add("inspected_by", rec.InspectedBy)
	add("inspected_at", rec.InspectedAt)
	add("machine_operator", rec.MachineOperator)
	add("handover_by", rec.HandoverBy)
	return missing
}

// ComputeCompleteness 计算完成度：缺失项清单与 is_complete。
func ComputeCompleteness(rec FormRecord, lines []RecordLine) (missing []string, complete bool) {
	missing = MissingPaperFields(rec)
	if len(lines) == 0 {
		missing = append(missing, "lines")
	}
	return missing, len(missing) == 0
}

// ComputeTotals 按笔的明细行汇总（服务端重算，忽略调用方上报的汇总值）。
func ComputeTotals(lines []RecordLine) (total, qualified, defect int, passRate *float64) {
	for _, l := range lines {
		total += l.TotalQty
		qualified += l.QualifiedQty
		defect += l.DefectQty
	}
	if total > 0 {
		r := round2(float64(qualified) / float64(total) * 100)
		passRate = &r
	}
	return total, qualified, defect, passRate
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func recordTouched(rec FormRecord) bool {
	return rec.ProcessCode != nil || rec.SubmittedBy != nil || rec.InspectedBy != nil ||
		rec.MachineOperator != nil || rec.HandoverBy != nil ||
		rec.SubmittedAt != nil || rec.InspectedAt != nil
}

func negativeCounts(l RecordLine) []string {
	out := make([]string, 0, 4)
	check := func(field string, v int) {
		if v < 0 {
			out = append(out, field)
		}
	}
	check("total_qty", l.TotalQty)
	check("qualified_qty", l.QualifiedQty)
	check("defect_qty", l.DefectQty)
	for _, g := range DefectGroups {
		check(g.Code+"_concave", g.Concave(l))
		check(g.Code+"_convex", g.Convex(l))
		check(g.Code+"_both", g.Both(l))
	}
	return out
}

func lineField(idx int, field string) string {
	if idx == 0 {
		return field
	}
	return "lines[" + strconv.Itoa(idx) + "]." + field
}

func parseAnyTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// str 安全解引用字符串指针。
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
