// Package formfields provides the shared v2 defect/surface catalogue.
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
