package inspection

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/myonku/lens-qs-api/internal/formfields"
)

// ------------------------------- 三态值 -------------------------------
//
// 字段级合并必须能区分三态：
//
//	键不存在  → 不改（IsSet=false）
//	键为 null → 清空（IsSet=true, Cleared=true）
//	键有值    → 写入（IsSet=true, Cleared=false）
//
// 注意：这里**必须用值类型 + IsSet 标记，不能用指针**。
// 因为 encoding/json 对"指针字段 + JSON null"会直接把指针置 nil，
// 根本不调用自定义 UnmarshalJSON，导致无法区分"未提交"和"显式 null"。

// NullableString 字符串字段的三态补丁。
type NullableString struct {
	IsSet   bool
	Cleared bool
	Value   string
}

// UnmarshalJSON 实现三态解析。
func (n *NullableString) UnmarshalJSON(raw []byte) error {
	n.IsSet = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		n.Cleared, n.Value = true, ""
		return nil
	}
	n.Cleared = false
	return json.Unmarshal(raw, &n.Value)
}

// ApplyPtr 写入字符串指针字段。
func (n *NullableString) ApplyPtr(dst **string) {
	if n == nil || !n.IsSet {
		return
	}
	if n.Cleared {
		*dst = nil
		return
	}
	v := n.Value
	*dst = &v
}

// ApplyStr 写入非指针字符串字段。
func (n *NullableString) ApplyStr(dst *string) {
	if n == nil || !n.IsSet {
		return
	}
	if n.Cleared {
		*dst = ""
		return
	}
	*dst = n.Value
}

// NullableInt 整数字段的三态补丁（整数无"清空"语义，null 视为不修改）。
type NullableInt struct {
	IsSet bool
	Value int
}

// UnmarshalJSON 实现三态解析。
func (n *NullableInt) UnmarshalJSON(raw []byte) error {
	n.IsSet = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		n.Value = 0
		return nil
	}
	return json.Unmarshal(raw, &n.Value)
}

// Apply 写入整数字段。
func (n *NullableInt) Apply(dst *int) {
	if n == nil || !n.IsSet {
		return
	}
	*dst = n.Value
}

// NullableFloat 浮点字段的三态补丁。
type NullableFloat struct {
	IsSet   bool
	Cleared bool
	Value   float64
}

// UnmarshalJSON 实现三态解析。
func (n *NullableFloat) UnmarshalJSON(raw []byte) error {
	n.IsSet = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		n.Cleared, n.Value = true, 0
		return nil
	}
	n.Cleared = false
	return json.Unmarshal(raw, &n.Value)
}

// DefectValue 是单个缺陷格子的三态值（null 表示把这个格子清零）。
type DefectValue struct {
	Cleared bool
	Value   int
}

// UnmarshalJSON 实现缺陷格子的三态解析。
func (d *DefectValue) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		d.Cleared, d.Value = true, 0
		return nil
	}
	d.Cleared = false
	return json.Unmarshal(raw, &d.Value)
}

// ------------------------------- 补丁结构 -------------------------------

// FormPatch 是一次**字段级合并**的输入。
//
// 语义（需求 §5.4）：
//   - 请求里出现的字段才改；没出现的保持原值；
//   - 可空字段显式传 null → 清空；
//   - `defects` 只改给出的格子；给空对象 `{}` → 清空全部缺陷计数；
//   - `lines` 一旦给出即**整体替换**明细（不做行级合并）。
type FormPatch struct {
	// 表头栏目
	Operation     NullableString  `json:"operation_code"`
	OperationAlt  NullableString  `json:"operation"` // 兼容写法
	OperationText NullableString  `json:"operation_text"`
	TotalQty      NullableInt     `json:"total_qty"`
	QualifiedQty  NullableInt     `json:"qualified_qty"`
	DefectQty     NullableInt     `json:"defect_qty"`
	PassRate      NullableFloat   `json:"pass_rate"`

	// 样品信息
	SampleRaw     NullableString `json:"sample_raw"`
	SampleProject NullableString `json:"sample_project"`
	SampleModel   NullableString `json:"sample_model"`
	SampleSpec    NullableString `json:"sample_spec"`
	SampleSize    NullableString `json:"sample_size"`
	SampleSide    NullableString `json:"sample_side"`
	UnitID        NullableString `json:"unit_id"`
	UnitText      NullableString `json:"unit_text"`

	Remark NullableString `json:"remark"`

	// 缺陷明细：只改给出的格子
	Defects DefectsPatch `json:"defects"`

	// 明细整体替换
	Lines []LinePatch `json:"lines"`
}

// DefectsPatch 是"缺陷编码 → 表面 → 三态计数"的补丁。
type DefectsPatch map[string]map[string]DefectValue

// LinePatch 是一条明细行的输入。
type LinePatch struct {
	Seq          NullableInt    `json:"seq"`
	TotalQty     NullableInt    `json:"total_qty"`
	QualifiedQty NullableInt    `json:"qualified_qty"`
	DefectQty    NullableInt    `json:"defect_qty"`
	Defects      DefectsPatch   `json:"defects"`
	Remark       NullableString `json:"remark"`
}

// FormPatchKeys 是 FormPatch 认识的键（未知键会被报出来，便于前端发现拼写错误）。
var FormPatchKeys = []string{
	"operation_code", "operation", "operation_text",
	"total_qty", "qualified_qty", "defect_qty", "pass_rate",
	"sample_raw", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side",
	"unit_id", "unit_text", "remark", "defects", "lines",
}

// LinePatchKeys 是 LinePatch 认识的键。
var LinePatchKeys = []string{
	"seq", "total_qty", "qualified_qty", "defect_qty", "defects", "remark",
}

// ParseFormPatch 解析请求体里的 form 对象；未知键列表由调用方决定如何处理。
func ParseFormPatch(raw json.RawMessage) (*FormPatch, []string, error) {
	if isNull(raw) {
		return &FormPatch{}, nil, nil
	}
	unknown, err := unknownKeys(raw, FormPatchKeys)
	if err != nil {
		return nil, nil, err
	}
	var p FormPatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, fmt.Errorf("form 字段格式不正确: %w", err)
	}
	// 兼容：顶层直接写扁平缺陷列名（prick_concave 等）
	flat, err := flatDefectsFromRaw(raw)
	if err != nil {
		return nil, nil, err
	}
	if len(flat) > 0 {
		if p.Defects == nil {
			p.Defects = DefectsPatch{}
		}
		for name, v := range flat {
			code, surface := splitDefectField(name)
			if p.Defects[code] == nil {
				p.Defects[code] = map[string]DefectValue{}
			}
			p.Defects[code][surface] = v
		}
	}
	return &p, unknown, nil
}

// ParseLinePatches 解析 lines 数组。
func ParseLinePatches(raw json.RawMessage) ([]LinePatch, error) {
	if isNull(raw) {
		return nil, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("lines 格式不正确: %w", err)
	}
	out := make([]LinePatch, 0, len(raws))
	for i, r := range raws {
		unknown, err := unknownKeys(r, LinePatchKeys)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行: %w", i+1, err)
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("第 %d 行存在无法识别的字段: %v", i+1, unknown)
		}
		var lp LinePatch
		if err := json.Unmarshal(r, &lp); err != nil {
			return nil, fmt.Errorf("第 %d 行格式不正确: %w", i+1, err)
		}
		flat, err := flatDefectsFromRaw(r)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行: %w", i+1, err)
		}
		if len(flat) > 0 {
			if lp.Defects == nil {
				lp.Defects = DefectsPatch{}
			}
			for name, v := range flat {
				code, surface := splitDefectField(name)
				if lp.Defects[code] == nil {
					lp.Defects[code] = map[string]DefectValue{}
				}
				lp.Defects[code][surface] = v
			}
		}
		out = append(out, lp)
	}
	return out, nil
}

// ApplyTo 把补丁合并进一次提交（就地修改）。
func (p *FormPatch) ApplyTo(s *Submission) {
	if p == nil || s == nil {
		return
	}
	if p.Operation.IsSet {
		p.Operation.ApplyStr(&s.OperationCode)
	} else if p.OperationAlt.IsSet {
		p.OperationAlt.ApplyStr(&s.OperationCode)
	}
	p.OperationText.ApplyPtr(&s.OperationText)
	p.TotalQty.Apply(&s.TotalQty)
	p.QualifiedQty.Apply(&s.QualifiedQty)
	p.DefectQty.Apply(&s.DefectQty)
	if p.PassRate.IsSet {
		if p.PassRate.Cleared {
			s.PassRate = nil
		} else {
			v := p.PassRate.Value
			s.PassRate = &v
		}
	}
	p.SampleRaw.ApplyStr(&s.SampleRaw)
	p.SampleProject.ApplyPtr(&s.SampleProject)
	p.SampleModel.ApplyPtr(&s.SampleModel)
	p.SampleSpec.ApplyPtr(&s.SampleSpec)
	p.SampleSize.ApplyPtr(&s.SampleSize)
	p.SampleSide.ApplyPtr(&s.SampleSide)
	p.UnitID.ApplyPtr(&s.UnitID)
	p.UnitText.ApplyPtr(&s.UnitText)
	p.Remark.ApplyPtr(&s.Remark)
}

// AppliedHeaderFields 返回本次补丁里显式给出的表头汇总字段（用于判定"表头优先"）。
func (p *FormPatch) AppliedHeaderFields() (total, qualified, defect bool) {
	if p == nil {
		return
	}
	return p.TotalQty.IsSet, p.QualifiedQty.IsSet, p.DefectQty.IsSet
}

// ApplyToLine 把行补丁合并进一条明细行（就地修改）。
func (lp *LinePatch) ApplyToLine(l *Line) {
	if lp == nil || l == nil {
		return
	}
	lp.Seq.Apply(&l.Seq)
	lp.TotalQty.Apply(&l.TotalQty)
	lp.QualifiedQty.Apply(&l.QualifiedQty)
	lp.DefectQty.Apply(&l.DefectQty)
	lp.Remark.ApplyPtr(&l.Remark)
	if lp.Defects != nil {
		if l.Defects == nil {
			l.Defects = Defects{}
		}
		if len(lp.Defects) == 0 {
			l.Defects = Defects{} // 显式空对象 = 清空
		}
		for code, surfaces := range lp.Defects {
			cur := l.Defects[code]
			if cur == nil {
				cur = map[string]int{}
			}
			for surface, dv := range surfaces {
				if dv.Cleared {
					delete(cur, surface)
					continue
				}
				cur[surface] = dv.Value
			}
			if len(cur) == 0 {
				delete(l.Defects, code)
			} else {
				l.Defects[code] = cur
			}
		}
	}
}

// ValidateFieldNames 校验补丁里出现的缺陷编码/表面维度是否合法。
func (p *FormPatch) ValidateFieldNames() error {
	if p == nil {
		return nil
	}
	if err := validateDefectsPatch(p.Defects); err != nil {
		return err
	}
	for i := range p.Lines {
		if err := validateDefectsPatch(p.Lines[i].Defects); err != nil {
			return fmt.Errorf("第 %d 行: %w", i+1, err)
		}
	}
	return nil
}

func validateDefectsPatch(d DefectsPatch) error {
	for code, surfaces := range d {
		if !isDefectCode(code) {
			return fmt.Errorf("无法识别的不良缺陷列: %s（合法列见 GET /api/form-fields）", code)
		}
		for s := range surfaces {
			if !isSurface(s) {
				return fmt.Errorf("缺陷列 %s 的表面维度不合法: %s（合法值: concave/convex/both）", code, s)
			}
		}
	}
	return nil
}

// ------------------------------- 内部工具 -------------------------------

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// unknownKeys 返回 raw 中不属于 allowed、也不是合法扁平缺陷列的键。
func unknownKeys(raw json.RawMessage, allowed []string) ([]string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("字段对象格式不正确: %w", err)
	}
	ok := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		ok[k] = true
	}
	var unknown []string
	for k := range m {
		if !ok[k] && !formfields.IsDefectField(k) {
			unknown = append(unknown, k)
		}
	}
	return unknown, nil
}

// flatDefectsFromRaw 从原始对象里挑出扁平的缺陷列（如 prick_concave）。
func flatDefectsFromRaw(raw json.RawMessage) (map[string]DefectValue, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := map[string]DefectValue{}
	for k, v := range m {
		if !formfields.IsDefectField(k) {
			continue
		}
		var dv DefectValue
		if err := json.Unmarshal(v, &dv); err != nil {
			return nil, fmt.Errorf("缺陷列 %s 必须是整数或 null", k)
		}
		out[k] = dv
	}
	return out, nil
}

// splitDefectField 把扁平列名拆成 (缺陷编码, 表面)。
func splitDefectField(name string) (string, string) {
	for _, d := range formfields.DefectGroups {
		for _, s := range formfields.Surfaces {
			if name == formfields.DefectFieldName(d.Code, s) {
				return d.Code, s
			}
		}
	}
	return name, ""
}
