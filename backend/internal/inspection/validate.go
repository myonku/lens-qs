package inspection

import (
	"strconv"
	"strings"
	"time"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 校验规则编号（对应 docs/requirements.md §5.3）
const (
	RuleSumQualifiedPlusDefect = "V-1"  // 合格品 + 不合格品 = 检验总数
	RuleDefectSubtotal         = "V-2"  // 缺陷子项合计 = 不合格品（恒为警告）
	RuleDefectBoundary         = "V-3"  // 缺陷子项边界
	RuleNonNegative            = "V-4"  // 非负整数
	RulePassRate               = "V-5"  // 合格率派生（仅计算）
	RuleTimeOrder              = "V-6"  // 时间顺序（恒为警告）
	RuleUniqueFormNo           = "V-7"  // 唯一性（唯一硬阻断）
	RuleSampleNotMatched       = "V-8"  // 样品信息与参考清单不匹配（恒为警告）
	RuleSampleProjectMissing   = "V-9"  // 样品结构化项缺失（恒为警告）
	RuleFieldMissing           = "V-10" // 表单栏目缺失（恒为警告）
)

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

// ValidateLines 校验明细行。
//
// 默认全部只警告；`enforce=strict` 时 V-1/V-3/V-4 升级为阻断错误。
// V-2 在任何模式下都不阻断（已确认同一片镜片可计入多个缺陷类别）。
func ValidateLines(lines []Line, enforce Enforce) []httpx.Issue {
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
		field := func(name string) string { return lineField(i, name) }

		// V-4 非负
		if neg := negativeFields(l); len(neg) > 0 {
			issues = append(issues, httpx.Issue{
				Rule: RuleNonNegative, Level: levelFor(), Field: field(neg[0]),
				Message: "第 " + row + " 行的「" + formfields.FieldLabel(neg[0]) + "」为负值",
			})
		}

		// V-1 合格品 + 不合格品 = 检验总数（三者都为 0 时不提示）
		if !(l.TotalQty == 0 && l.QualifiedQty == 0 && l.DefectQty == 0) {
			if l.QualifiedQty+l.DefectQty != l.TotalQty {
				issues = append(issues, httpx.Issue{
					Rule: RuleSumQualifiedPlusDefect, Level: levelFor(), Field: field("total_qty"),
					Message: "第 " + row + " 行：合格品(" + strconv.Itoa(l.QualifiedQty) +
						") + 不合格品(" + strconv.Itoa(l.DefectQty) + ") 与检验总数(" +
						strconv.Itoa(l.TotalQty) + ") 不一致",
				})
			}
		}

		// V-3 缺陷子项边界
		if len(negativeFields(l)) == 0 {
			for _, d := range formfields.DefectGroups {
				sum := defectSum(l, d.Code)
				if l.DefectQty > 0 && sum > l.DefectQty {
					issues = append(issues, httpx.Issue{
						Rule: RuleDefectBoundary, Level: levelFor(), Field: field("defect_qty"),
						Message: "第 " + row + " 行：「" + d.Label + "」合计(" + strconv.Itoa(sum) +
							") 超过不合格品数(" + strconv.Itoa(l.DefectQty) + ")",
					})
				}
				if l.TotalQty > 0 {
					for _, s := range formfields.Surfaces {
						if v := l.Defects[d.Code][s]; v > l.TotalQty {
							issues = append(issues, httpx.Issue{
								Rule: RuleDefectBoundary, Level: levelFor(), Field: field("total_qty"),
								Message: "第 " + row + " 行：「" + d.Label + "-" +
									formfields.SurfaceLabels[s] + "」超过检验总数",
							})
							break
						}
					}
				}
			}
		}

		// V-2 恒为警告
		total := lineDefectTotal(l)
		if !(l.DefectQty == 0 && total == 0) && total != l.DefectQty {
			issues = append(issues, httpx.Issue{
				Rule: RuleDefectSubtotal, Level: "warning", Field: field("defect_qty"),
				Message: "第 " + row + " 行：缺陷子项合计(" + strconv.Itoa(total) +
					") 与不合格品数(" + strconv.Itoa(l.DefectQty) +
					") 不一致（可能同一片镜片被计入多个类别）",
			})
		}
	}
	return issues
}

// ValidateSubmission 校验表头与时间（V-6 / V-9 / V-10，全部只警告）。
func ValidateSubmission(s Submission, now time.Time) []httpx.Issue {
	issues := make([]httpx.Issue, 0, 6)

	// V-6 时间顺序
	if str(s.SubmittedAt) != "" && str(s.LastSavedAt) != "" && str(s.SubmittedAt) > str(s.LastSavedAt) {
		issues = append(issues, httpx.Issue{
			Rule: RuleTimeOrder, Level: "warning", Field: "submitted_at",
			Message: "提交时间(" + str(s.SubmittedAt) + ") 晚于最后保存时间(" + str(s.LastSavedAt) + ")",
		})
	}
	if v := str(s.SubmittedAt); v != "" {
		if t := parseAnyTime(v); !t.IsZero() && t.After(now) {
			issues = append(issues, httpx.Issue{
				Rule: RuleTimeOrder, Level: "warning", Field: "submitted_at",
				Message: "提交时间(" + v + ") 晚于当前时间",
			})
		}
	}

	// V-9 样品结构化项缺失（项目）
	if str(s.SampleProject) == "" {
		issues = append(issues, httpx.Issue{
			Rule: RuleSampleProjectMissing, Level: "warning", Field: "sample_project",
			Message: "样品信息的「项目」（M1/M2 等）尚未填写",
		})
	}

	// V-10 表单栏目缺失（已经"开始填了"才提示，避免噪声）
	if submissionTouched(s) {
		for _, f := range MissingFields(s) {
			issues = append(issues, httpx.Issue{
				Rule: RuleFieldMissing, Level: "warning", Field: f,
				Message: "「" + formfields.FieldLabel(f) + "」尚未填写",
			})
		}
	}
	return issues
}
// MissingFields 返回缺失的表单栏目（不阻断保存，仅作完成度提示）。
//
// 口径（需求 §6.4）：
//   - 纸面栏目：单位、检验人（纸质表单既有栏目，B-11）
//   - 计数区：检验工序、是否有明细行
//
// 说明：需求允许"还没填完"（F-3），因此这里只是提示；
// 另外**不把"送检人/检验人"等与系统用户混为一谈**——纸面人名可以为空，不影响保存。
func MissingFields(s Submission) []string {
	missing := make([]string, 0, 6)
	if strings.TrimSpace(s.OperationCode) == "" {
		missing = append(missing, "operation_code")
	}
	if str(s.PaperUnitID) == "" && str(s.PaperUnitText) == "" && str(s.UnitID) == "" && str(s.UnitText) == "" {
		missing = append(missing, "paper_unit_id")
	}
	if str(s.PaperInspector) == "" {
		missing = append(missing, "paper_inspector")
	}
	if strings.TrimSpace(s.SampleRaw) == "" {
		missing = append(missing, "sample_raw")
	}
	if len(s.Lines) == 0 {
		missing = append(missing, "lines")
	}
	return missing
}

// ComputeCompleteness 计算完成度。
func ComputeCompleteness(s Submission, lines []Line) (missing []string, complete bool) {
	s.Lines = lines
	missing = MissingFields(s)
	return missing, len(missing) == 0
}

func submissionTouched(s Submission) bool {
	return s.OperationCode != "" || s.TotalQty != 0 || s.QualifiedQty != 0 ||
		s.DefectQty != 0 || len(s.Lines) > 0 || strings.TrimSpace(s.SampleRaw) != ""
}

func negativeFields(l Line) []string {
	out := make([]string, 0, 4)
	check := func(name string, v int) {
		if v < 0 {
			out = append(out, name)
		}
	}
	check("total_qty", l.TotalQty)
	check("qualified_qty", l.QualifiedQty)
	check("defect_qty", l.DefectQty)
	for _, d := range formfields.DefectGroups {
		for _, s := range formfields.Surfaces {
			check(formfields.DefectFieldName(d.Code, s), l.Defects[d.Code][s])
		}
	}
	return out
}

func defectSum(l Line, code string) int {
	sum := 0
	for _, s := range formfields.Surfaces {
		sum += l.Defects[code][s]
	}
	return sum
}

func lineDefectTotal(l Line) int {
	sum := 0
	for _, d := range formfields.DefectGroups {
		sum += defectSum(l, d.Code)
	}
	return sum
}

func lineField(idx int, name string) string {
	if idx == 0 {
		return name
	}
	return "lines[" + strconv.Itoa(idx) + "]." + name
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
