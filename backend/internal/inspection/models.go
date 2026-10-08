// Package inspection 实现核心业务：**检验表单提交**（每道工序一份表单）。
//
// 现实模型（对应 docs/requirements.md §5）：
//
//	Submission  一次表单提交 = 某个工序填的那张表单（表头栏目 + 缺陷明细的一行或多行）
//	Line        明细行：检验总数 / 合格品 / 不合格品 + 全部不良缺陷列（每列 凹/凸/双）
//	History     每次保存前的内容快照（只增不可改）
//
// 设计原则（需求 §1.4 最大灵活性 F-1~F-8）：
//   - 没有必填列，任何列都可以留空；
//   - 保存是**字段级合并**：请求里出现过的字段才改，没出现的保持原值；
//   - 每次保存前写快照，历史只增；
//   - 校验默认只警告，`enforce=strict` 时才拦"自相矛盾"的数据。
package inspection

import (
	"encoding/json"

	"github.com/myonku/lens-qs-api/internal/formfields"
)

// 状态取值：提交即生效（无审批流）。
const (
	StateActive = "ACTIVE"
	StateVoid   = "VOID"
)

// Defects 是"缺陷列编码 → 表面 → 计数"的嵌套结构（对外 JSON 的友好形态）：
//
//	"defects": { "prick": { "concave": 1, "convex": 0, "both": 2 }, ... }
type Defects map[string]map[string]int

// Flatten 把嵌套形态展开为扁平字段名（用于落库与统计）。
// 只输出合法缺陷列；非法编码被忽略（调用方应先行校验）。
func (d Defects) Flatten() map[string]int {
	if len(d) == 0 {
		return nil
	}
	out := make(map[string]int, len(d)*len(formfields.Surfaces))
	for code, surfaces := range d {
		if !isDefectCode(code) {
			continue
		}
		for surface, v := range surfaces {
			if !isSurface(surface) {
				continue
			}
			out[formfields.DefectFieldName(code, surface)] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DefectsFromFlat 把扁平字段名还原为嵌套形态。
func DefectsFromFlat(flat map[string]int) Defects {
	if len(flat) == 0 {
		return nil
	}
	out := Defects{}
	for name, v := range flat {
		for _, code := range formfields.DefectGroups {
			for _, s := range formfields.Surfaces {
				if name == formfields.DefectFieldName(code.Code, s) {
					if out[code.Code] == nil {
						out[code.Code] = map[string]int{}
					}
					out[code.Code][s] = v
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isDefectCode(code string) bool {
	for _, d := range formfields.DefectGroups {
		if d.Code == code {
			return true
		}
	}
	return false
}

func isSurface(s string) bool {
	for _, v := range formfields.Surfaces {
		if v == s {
			return true
		}
	}
	return false
}

// Line 是一条明细行（对应纸质表单表格里的一行）。
type Line struct {
	ID     string `json:"id,omitempty"`
	FormID string `json:"form_id,omitempty"`
	Seq    int    `json:"seq"`

	TotalQty     int `json:"total_qty"`
	QualifiedQty int `json:"qualified_qty"`
	DefectQty    int `json:"defect_qty"` // 不合格品

	// Defects 是全部不良缺陷列的计数（每列含凹面/凸面/双面）。
	Defects Defects `json:"defects,omitempty"`

	Remark    *string `json:"remark,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

// Submission 是一次表单提交（= 某工序填的那张表单）。
type Submission struct {
	ID string `json:"id"`
	// FormNo 表单号（系统生成或调用方指定）
	FormNo string `json:"form_no"`
	// OperationCode 检验工序（表单第一列；也是"找到自己工序所在表单"的依据）
	OperationCode string  `json:"operation_code"`
	OperationText *string `json:"operation_text,omitempty"`

	// 样品信息：原始串（事实来源，永不被改写）+ 拆解字段
	SampleRaw     string  `json:"sample_raw"`
	SampleProject *string `json:"sample_project,omitempty"`
	SampleModel   *string `json:"sample_model,omitempty"`
	SampleSpec    *string `json:"sample_spec,omitempty"`
	SampleSize    *string `json:"sample_size,omitempty"`
	SampleSide    *string `json:"sample_side,omitempty"`

	// ---- 纸质表单原有的表头栏目（需求 B-11：全部纳入，不能省略）----
	// 与下面的系统留痕字段**并存且不混用**：这里存纸面上手写的值。
	PaperUnitID          *string `json:"paper_unit_id,omitempty"`
	PaperUnitText        *string `json:"paper_unit_text,omitempty"`
	PaperSubmitter       *string `json:"paper_submitter,omitempty"`
	PaperSubmittedAt     *string `json:"paper_submitted_at,omitempty"`
	PaperInspector       *string `json:"paper_inspector,omitempty"`
	PaperInspectedAt     *string `json:"paper_inspected_at,omitempty"`
	PaperMachineOperator *string `json:"paper_machine_operator,omitempty"`
	PaperHandoverBy      *string `json:"paper_handover_by,omitempty"`

	// ---- 系统留痕（与纸面人名无关，B-17）----
	CreatedBy   *string `json:"created_by,omitempty"` // 首次录入的系统用户
	SubmittedBy *string `json:"submitted_by,omitempty"`
	SubmittedAt *string `json:"submitted_at,omitempty"`
	LastSavedBy *string `json:"last_saved_by,omitempty"`
	LastSavedAt *string `json:"last_saved_at,omitempty"`
	SaveCount   int     `json:"save_count"`

	// UnitID / UnitText 是 paper_unit_* 的**只读别名**，仅为兼容早期接口与前端而保留。
	// 写入请用 paper_unit_id / paper_unit_text（落库后这里会回显同样的值）。
	UnitID   *string `json:"unit_id,omitempty"`
	UnitText *string `json:"unit_text,omitempty"`

	// 汇总（服务端按明细行重算；仅当调用方未显式给出时回填）
	TotalQty     int      `json:"total_qty"`
	QualifiedQty int      `json:"qualified_qty"`
	DefectQty    int      `json:"defect_qty"`
	PassRate     *float64 `json:"pass_rate,omitempty"`

	IsComplete    bool     `json:"is_complete"`
	MissingFields []string `json:"missing_fields"`

	State      string  `json:"state"`
	VoidedAt   *string `json:"voided_at,omitempty"`
	VoidReason *string `json:"void_reason,omitempty"`
	Remark     *string `json:"remark,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	Lines []Line `json:"lines"`
}

// Aggregate 按明细行汇总总量（"检验总数"等顶层栏目在纸质表单上是一行合计值）。
func Aggregate(lines []Line) (total, qualified, defect int) {
	for _, l := range lines {
		total += l.TotalQty
		qualified += l.QualifiedQty
		defect += l.DefectQty
	}
	return total, qualified, defect
}

// ComputePassRate 计算合格率（%）。分母为 0 时返回 nil。
func ComputePassRate(qualified, total int) *float64 {
	if total <= 0 {
		return nil
	}
	r := float64(qualified) / float64(total) * 100
	r = float64(int64(r*100+0.5)) / 100
	return &r
}

// DefectTotals 返回每种缺陷列的合计（凹/凸/双），用于统计接口。
func DefectTotals(lines []Line) map[string]map[string]int {
	out := make(map[string]map[string]int, len(formfields.DefectGroups))
	for _, l := range lines {
		for code, surfaces := range l.Defects {
			if out[code] == nil {
				out[code] = map[string]int{}
			}
			for s, v := range surfaces {
				out[code][s] += v
			}
		}
	}
	return out
}

// MarshalDefects 把嵌套缺陷结构序列化为落库文本。
func MarshalDefects(d Defects) *string {
	if len(d) == 0 {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// UnmarshalDefects 解析落库文本。
func UnmarshalDefects(s *string) Defects {
	if s == nil || *s == "" {
		return Defects{}
	}
	var d Defects
	if err := json.Unmarshal([]byte(*s), &d); err != nil {
		return Defects{}
	}
	if d == nil {
		return Defects{}
	}
	return d
}

// MarshalStrings 把字符串数组序列化为落库文本（缺失字段清单等）。
func MarshalStrings(list []string) *string {
	if len(list) == 0 {
		return nil
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// UnmarshalStrings 解析字符串数组文本。
func UnmarshalStrings(s *string) []string {
	if s == nil || *s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(*s), &out); err != nil {
		return nil
	}
	return out
}
