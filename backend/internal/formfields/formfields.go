// Package formfields 定义检验表单的**列目录**：顶层栏目 + 不良缺陷明细。
//
// 现实依据：每道工序都要填写表单，但各工序所填内容不同；表单的完整列集合由业务方给出
// （见 docs/requirements.md 的字段清单与 docs/api.md）。本包把这些列做成**唯一的事实来源**，
// 供：
//   - 前端渲染（GET /api/form-templates 返回 layout：分组 + 列 + 顺序 + 表面维度）
//   - 后端校验与统计映射（合法的字段名白名单）
//
// 设计取向（最大灵活性 F-1/F-8）：
//   - 没有"必填"，任何列都可以留空；
//   - 缺陷列统一为 3 个表面维度（凹面 concave / 凸面 convex / 双面 both）；
//   - 新增列只需在此处加一条 + 加一次迁移，不影响其它逻辑。
package formfields

// 表面维度。
const (
	SurfaceConcave = "concave" // 凹面
	SurfaceConvex  = "convex"  // 凸面
	SurfaceBoth    = "both"    // 双面
)

// Surfaces 是缺陷列的表面维度顺序（与纸质表单列顺序一致）。
var Surfaces = []string{SurfaceConcave, SurfaceConvex, SurfaceBoth}

// SurfaceLabels 表面维度的中文名。
var SurfaceLabels = map[string]string{
	SurfaceConcave: "凹面",
	SurfaceConvex:  "凸面",
	SurfaceBoth:    "双面",
}

// Kind 表示列的取值类型。
type Kind string

const (
	KindText   Kind = "text"   // 自由文本
	KindInt    Kind = "int"    // 整数计数
	KindNumber Kind = "number" // 数值（良率等）
)

// Defect 是一个不良缺陷列（对应纸质表单「不良缺陷明细」下的一个表头）。
type Defect struct {
	Code  string `json:"code"`  // 稳定编码，如 "touch_mark"
	Label string `json:"label"` // 中文列名，如 "点伤"
}

// DefectGroups 是全部不良缺陷列，顺序与纸质表单一致。
//
// 说明：每一项都包含凹面 / 凸面 / 双面三个计数子列。
var DefectGroups = []Defect{
	{"prick", "点伤"},
	{"scratch", "划伤"},
	{"flange_prick", "法兰边点伤"},
	{"flange_scratch", "法兰边划伤"},
	{"collapse_edge", "崩边"},
	{"coating_particle", "涂层瑕疵"},
	{"bright_spot", "亮点"},
	{"crack", "裂"},
	{"overflow_glue", "溢胶"},
	{"discolor_particle", "变色不良颗粒"},
	{"protective_particle", "保护不良颗粒"},
	{"spin_particle", "旋涂颗粒"},
	{"fiber", "纤维"},
	{"flange_particle", "法兰边颗粒"},
	{"hardening_bubble", "加硬气泡"},
	{"hairline_crack", "发丝裂"},
	{"dirt", "脏污"},
	{"spin_mark", "旋痕"},
	{"raised_point", "凸点"},
	{"recess", "凹坑"},
	{"bubble", "气泡"},
	{"shape", "面形"},
	{"watermark", "水印"},
	{"hard_spot", "加硬点"},
	{"flange_round_point", "法兰边圆点"},
	{"severe_flange_damage", "严重法兰边伤"},
	{"garbage", "垃圾"},
	{"flange_damage", "法兰边伤"},
	{"flange_imprint", "法兰边印痕"},
	{"press_imprint", "压痕/法兰边压痕"},
	{"flow_mark", "流痕"},
	{"inner_fold", "镜内褶皱"},
	{"flange_fold", "褶皱/法兰边褶皱"},
	{"inner_flow_glue", "内挂流/挂流"},
	{"flange_recess", "法兰边凹痕"},
}

// DefectCodes 返回全部缺陷列编码（顺序同 DefectGroups）。
func DefectCodes() []string {
	out := make([]string, 0, len(DefectGroups))
	for _, d := range DefectGroups {
		out = append(out, d.Code)
	}
	return out
}

// DefectFieldName 返回缺陷列在"扁平字段名"里的写法，形如 touch_mark_concave。
// 扁平命名用于：数据库列名（form_lines）、统计 group_by、以及前端简单模式。
func DefectFieldName(code, surface string) string { return code + "_" + surface }

// DefectFieldNames 返回全部缺陷列的扁平字段名（按"列 → 三个表面"展开）。
func DefectFieldNames() []string {
	out := make([]string, 0, len(DefectGroups)*len(Surfaces))
	for _, d := range DefectGroups {
		for _, s := range Surfaces {
			out = append(out, DefectFieldName(d.Code, s))
		}
	}
	return out
}

// IsDefectField 报告扁平字段名是否为合法的缺陷列。
func IsDefectField(name string) bool {
	for _, d := range DefectGroups {
		for _, s := range Surfaces {
			if name == DefectFieldName(d.Code, s) {
				return true
			}
		}
	}
	return false
}

// HeaderColumn 是顶层栏目（不良缺陷明细之外的列）。
type HeaderColumn struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Kind     Kind   `json:"kind"`
	Computed bool   `json:"computed"` // true 表示由服务端计算，提交无效
}

// HeaderColumns 是顶层栏目，顺序与纸质表单一致。
var HeaderColumns = []HeaderColumn{
	{"operation", "检验工序", KindText, false},
	{"total_qty", "检验总数", KindInt, false},
	{"qualified_qty", "合格品", KindInt, false},
	{"defect_qty", "不合格品", KindInt, false},
	{"pass_rate", "合格率", KindNumber, true}, // 服务端可由 合格品/检验总数 推导
}

// HeaderFieldNames 返回顶层栏目的字段名（用于合法性判断）。
func HeaderFieldNames() []string {
	out := make([]string, 0, len(HeaderColumns))
	for _, h := range HeaderColumns {
		out = append(out, h.Code)
	}
	return out
}

// IsHeaderField 报告字段名是否为合法顶层栏目。
func IsHeaderField(name string) bool {
	for _, h := range HeaderColumns {
		if h.Code == name {
			return true
		}
	}
	return false
}

// Layout 是给前端渲染用的完整布局。
type Layout struct {
	Header        []HeaderColumn `json:"header_columns"`
	Surfaces      []string       `json:"surfaces"`
	SurfaceLabels map[string]string `json:"surface_labels"`
	DefectGroup   struct {
		Title   string   `json:"title"`
		Columns []Defect `json:"columns"`
	} `json:"defect_group"`
	// DefectColumns 是"列 × 表面"展开后的完整列清单（前端可直接顺序渲染）
	DefectColumns []LayoutColumn `json:"defect_columns"`
}

// LayoutColumn 是"缺陷列 × 表面"展开后的一列。
type LayoutColumn struct {
	Field   string `json:"field"`   // 扁平字段名，如 touch_mark_concave
	Defect  string `json:"defect"`  // 缺陷编码
	Label   string `json:"label"`   // 缺陷中文名
	Surface string `json:"surface"` // concave | convex | both
	Header  string `json:"header"`  // 表头显示，如 点伤-凹面
}

// BuildLayout 组装完整布局。
func BuildLayout() Layout {
	var l Layout
	l.Header = HeaderColumns
	l.Surfaces = Surfaces
	l.SurfaceLabels = SurfaceLabels
	l.DefectGroup.Title = "不良缺陷明细"
	l.DefectGroup.Columns = DefectGroups
	l.DefectColumns = make([]LayoutColumn, 0, len(DefectGroups)*len(Surfaces))
	for _, d := range DefectGroups {
		for _, s := range Surfaces {
			l.DefectColumns = append(l.DefectColumns, LayoutColumn{
				Field:   DefectFieldName(d.Code, s),
				Defect:  d.Code,
				Label:   d.Label,
				Surface: s,
				Header:  d.Label + "-" + SurfaceLabels[s],
			})
		}
	}
	return l
}

// FieldLabel 返回字段名对应的中文名（顶层栏目或缺陷列）。
func FieldLabel(name string) string {
	for _, h := range HeaderColumns {
		if h.Code == name {
			return h.Label
		}
	}
	for _, d := range DefectGroups {
		for _, s := range Surfaces {
			if DefectFieldName(d.Code, s) == name {
				return d.Label + "-" + SurfaceLabels[s]
			}
		}
	}
	return name
}
