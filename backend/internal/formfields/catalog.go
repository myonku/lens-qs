package formfields

import "strings"

type Field struct {
	Path     string `json:"path"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	ReadOnly bool   `json:"read_only"`
	Order    int    `json:"order"`
}
type FieldCatalog struct {
	SchemaVersion    string   `json:"schema_version"`
	Fields           []Field  `json:"fields"`
	Surfaces         []string `json:"surfaces"`
	DefectCategories []Defect `json:"defect_categories"`
}

func Catalog() FieldCatalog {
	c := FieldCatalog{SchemaVersion: "2", Surfaces: Surfaces, DefectCategories: DefectGroups}
	add := func(path, kind string, nullable, ro bool) {
		label := labels[path]
		if label == "" {
			label = path
		}
		c.Fields = append(c.Fields, Field{path, label, kind, nullable, ro, len(c.Fields) + 1})
	}
	for _, p := range strings.Fields("shared_form_no sample_identity operation_id operation_group_id batch_no inspection_kind scope") {
		add(p, "string", p == "batch_no" || p == "operation_group_id", p == "scope")
	}
	for _, p := range strings.Fields("paper_unit_id paper_unit_text paper_submitter paper_inspector paper_machine_operator paper_handover_by paper_submitted_date paper_submitted_at paper_inspected_date paper_inspected_at machine_no sample_raw sample_project sample_model sample_spec sample_size sample_side remark") {
		kind := "string"
		if strings.HasSuffix(p, "_date") {
			kind = "date"
		}
		if strings.HasSuffix(p, "_at") {
			kind = "instant"
		}
		add(p, kind, true, false)
	}
	for _, p := range []string{"total_qty", "qualified_qty", "defect_qty"} {
		add("counts."+p, "quantity", true, false)
		add("lines[]."+p, "quantity", true, false)
	}
	add("lines[].seq", "integer", false, false)
	add("lines[].remark", "string", true, false)
	for _, d := range DefectGroups {
		for _, s := range Surfaces {
			add("lines[].defects."+d.Code+"."+s, "quantity", true, false)
			c.Fields[len(c.Fields)-1].Label = d.Label + " / " + SurfaceLabels[s]
		}
	}
	for _, p := range strings.Fields("kind direction quantity reason occurred_date occurred_at") {
		kind := "string"
		if p == "quantity" {
			kind = "quantity"
		}
		if p == "occurred_date" {
			kind = "date"
		}
		if p == "occurred_at" {
			kind = "instant"
		}
		add("quantity_changes[]."+p, kind, p != "kind" && p != "direction" && p != "reason", false)
	}
	for _, f := range []struct {
		path, kind string
		nullable   bool
	}{{"pass_rate", "number", true}, {"pass_rate_reason", "string", true}, {"line_totals", "object", false}, {"count_differences", "object", false}, {"is_complete", "boolean", false}, {"missing_fields", "array", false}, {"paper_submitted_day", "date", true}, {"paper_inspected_day", "date", true}} {
		add("derived."+f.path, f.kind, f.nullable, true)
	}

	return c
}
func Layout() []string {
	out := []string{}
	for _, f := range Catalog().Fields {
		out = append(out, f.Path)
	}
	return out
}

var labels = map[string]string{
	"shared_form_no": "共享表单号", "sample_identity": "完整样品型号", "operation_id": "检验工序", "operation_group_id": "工序填报组", "batch_no": "检验批号", "inspection_kind": "检验类型", "scope": "整批/分批",
	"paper_unit_id": "计量单位ID", "paper_unit_text": "计量单位", "paper_submitter": "送检人", "paper_inspector": "检验人", "paper_machine_operator": "操机员", "paper_handover_by": "交接员", "paper_submitted_date": "送检日期", "paper_submitted_at": "送检时点", "paper_inspected_date": "检验日期", "paper_inspected_at": "检验时点", "machine_no": "机台号", "sample_raw": "样品原文", "sample_project": "项目", "sample_model": "型号", "sample_spec": "规格", "sample_size": "大小", "sample_side": "左右", "remark": "单据备注",
	"counts.total_qty": "表头检验量", "counts.qualified_qty": "表头合格量", "counts.defect_qty": "表头不合格量", "lines[].total_qty": "明细检验量", "lines[].qualified_qty": "明细合格量", "lines[].defect_qty": "明细不合格量", "lines[].seq": "明细序号", "lines[].remark": "明细备注",
	"quantity_changes[].kind": "变化类型", "quantity_changes[].direction": "变化方向", "quantity_changes[].quantity": "变化数量", "quantity_changes[].reason": "变化原因", "quantity_changes[].occurred_date": "发生日期", "quantity_changes[].occurred_at": "发生时点",
	"derived.pass_rate": "良率", "derived.pass_rate_reason": "良率不可计算原因", "derived.line_totals": "明细合计", "derived.count_differences": "表头与明细差异", "derived.is_complete": "资料齐全", "derived.missing_fields": "缺项", "derived.paper_submitted_day": "送检业务日", "derived.paper_inspected_day": "检验业务日",
}
