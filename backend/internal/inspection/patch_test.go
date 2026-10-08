package inspection

import (
	"encoding/json"
	"testing"

	"github.com/myonku/lens-qs-api/internal/formfields"
)

// 字段目录：不良缺陷明细每一项都必须有 凹/凸/双 三个表面。
func TestFormFieldCatalog(t *testing.T) {
	const wantDefects = 35 // 逐列抄自表单截图「不良缺陷明细」
	if len(formfields.DefectGroups) != wantDefects {
		t.Fatalf("缺陷列应为 %d 个，得到 %d", wantDefects, len(formfields.DefectGroups))
	}
	names := formfields.DefectFieldNames()
	if len(names) != wantDefects*3 {
		t.Fatalf("扁平列数应为 %d，得到 %d", wantDefects*3, len(names))
	}
	// 抽查几个来源截图的列名
	for _, want := range []string{
		"prick_concave", "scratch_both",
		"flange_prick_concave", "coating_particle_convex",
		"hardening_bubble_both", "inner_flow_glue_concave", "flange_recess_both",
		"overflow_glue_convex", "watermark_both", "severe_flange_damage_concave",
	} {
		if !formfields.IsDefectField(want) {
			t.Errorf("应有缺陷列 %s", want)
		}
	}
	// 顶层栏目
	for _, want := range []string{"operation", "total_qty", "qualified_qty", "defect_qty", "pass_rate"} {
		if !formfields.IsHeaderField(want) {
			t.Errorf("应有顶层栏目 %s", want)
		}
	}
	if len(formfields.HeaderColumns) != 5 {
		t.Fatalf("顶层栏目应为 5 个，得到 %d", len(formfields.HeaderColumns))
	}
	layout := formfields.BuildLayout()
	if len(layout.DefectColumns) != wantDefects*3 {
		t.Fatalf("布局中的缺陷子列应为 %d 个，得到 %d", wantDefects*3, len(layout.DefectColumns))
	}
	if layout.DefectColumns[0].Header != "点伤-凹面" {
		t.Errorf("第一列显示名应为 点伤-凹面，得到 %s", layout.DefectColumns[0].Header)
	}
	// 顺序抽查：与截图一致（点伤 → 划伤 → 法兰边点伤 …），每个缺陷占 3 个子列
	if layout.DefectColumns[0].Defect != "prick" || layout.DefectColumns[0].Surface != "concave" {
		t.Errorf("第 1 个子列应为 点伤-凹面，得到 %s-%s",
			layout.DefectColumns[0].Defect, layout.DefectColumns[0].Surface)
	}
	if layout.DefectColumns[3].Defect != "scratch" || layout.DefectColumns[3].Surface != "concave" {
		t.Errorf("第 4 个子列应为 划伤-凹面，得到 %s-%s",
			layout.DefectColumns[3].Defect, layout.DefectColumns[3].Surface)
	}
	if layout.DefectColumns[6].Defect != "flange_prick" {
		t.Errorf("第 7 个子列应属于法兰边点伤，得到 %s", layout.DefectColumns[6].Defect)
	}
}

// 嵌套缺陷结构与扁平字段名的互相转换必须可逆。
func TestDefectsFlattenRoundTrip(t *testing.T) {
	d := Defects{
		"prick":      {"concave": 1, "convex": 2, "both": 3},
		"flange_fold": {"concave": 4},
	}
	flat := d.Flatten()
	if flat["prick_concave"] != 1 || flat["prick_convex"] != 2 || flat["prick_both"] != 3 {
		t.Fatalf("展开不正确: %+v", flat)
	}
	if flat["flange_fold_concave"] != 4 {
		t.Fatalf("展开不正确: %+v", flat)
	}
	back := DefectsFromFlat(flat)
	if back["prick"]["concave"] != 1 || back["prick"]["both"] != 3 || back["flange_fold"]["concave"] != 4 {
		t.Fatalf("还原不正确: %+v", back)
	}
	// 非法编码/表面会被忽略
	bad := Defects{"not_a_defect": {"concave": 9}, "prick": {"weird": 9, "convex": 5}}
	bflat := bad.Flatten()
	if _, ok := bflat["not_a_defect_concave"]; ok {
		t.Error("非法缺陷编码不应被展开")
	}
	if _, ok := bflat["prick_weird"]; ok {
		t.Error("非法表面维度不应被展开")
	}
	if bflat["prick_convex"] != 5 {
		t.Errorf("合法表面应保留: %+v", bflat)
	}
}

// 字段级合并：只改提交的字段，未提交的保持原值；显式 null 清空；空 defects 对象清空缺陷。
func TestFormPatchMerge(t *testing.T) {
	sub := Submission{
		FormNo:        "F-1",
		OperationCode: "一次加硬",
		SampleRaw:     "M2 S2 ND2 SMR",
		TotalQty:      100,
		QualifiedQty:  100,
	}
	origQualified := "李四"
	sub.SubmittedBy = &origQualified

	raw := json.RawMessage(`{
		"defect_qty": 0,
		"sample_project": "M2",
		"defects": { "prick": { "concave": 2, "both": 1 } },
		"lines": [
			{ "total_qty": 100, "qualified_qty": 98, "defect_qty": 2,
			  "defects": { "prick": { "concave": 2 } } }
		]
	}`)
	patch, unknown, err := ParseFormPatch(raw)
	if err != nil {
		t.Fatalf("解析补丁失败: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("不应有未知字段: %v", unknown)
	}
	patch.ApplyTo(&sub)

	if sub.OperationCode != "一次加硬" {
		t.Error("未提交的工序被改动了")
	}
	if sub.QualifiedQty != 100 {
		t.Error("未提交的合格品被改动了")
	}
	if sub.SampleProject == nil || *sub.SampleProject != "M2" {
		t.Error("样品项目未写入")
	}
	if sub.TotalQty != 100 || sub.DefectQty != 0 {
		t.Errorf("表头栏目不正确: total=%d defect=%d", sub.TotalQty, sub.DefectQty)
	}
	if sub.SubmittedBy == nil || *sub.SubmittedBy != "李四" {
		t.Error("未提交的录入人被清空了")
	}
	if len(patch.Defects) != 1 || patch.Defects["prick"]["concave"].Value != 2 {
		t.Errorf("缺陷补丁不正确: %+v", patch.Defects)
	}
	if len(patch.Lines) != 1 {
		t.Fatalf("明细补丁未解析: %+v", patch.Lines)
	}
	line := Line{Seq: 1}
	patch.Lines[0].ApplyToLine(&line)
	if line.TotalQty != 100 || line.QualifiedQty != 98 || line.DefectQty != 2 {
		t.Errorf("明细行合并不正确: %+v", line)
	}
	if line.Defects["prick"]["concave"] != 2 {
		t.Errorf("明细行缺陷未写入: %+v", line.Defects)
	}

	// 显式 null 清空可空字段
	clear := json.RawMessage(`{"sample_project": null}`)
	p2, _, err := ParseFormPatch(clear)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	p2.ApplyTo(&sub)
	if sub.SampleProject != nil {
		t.Error("显式 null 应清空样品项目")
	}

	// 缺陷格子显式 null → 该格子清零
	clearDefect := json.RawMessage(`{"defects": {"prick": {"concave": null, "convex": 7}}}`)
	p3, _, err := ParseFormPatch(clearDefect)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	p3.ApplyTo(&sub) // 先把补丁里的 defects 拿出来
	l3 := Line{Seq: 1, Defects: Defects{"prick": {"concave": 5, "convex": 1, "both": 3}}}
	lp := LinePatch{Defects: p3.Defects}
	lp.ApplyToLine(&l3)
	if _, ok := l3.Defects["prick"]["concave"]; ok {
		t.Errorf("显式 null 应清掉该格子: %+v", l3.Defects["prick"])
	}
	if l3.Defects["prick"]["convex"] != 7 || l3.Defects["prick"]["both"] != 3 {
		t.Errorf("其它格子应保持/更新: %+v", l3.Defects["prick"])
	}
}

// 未知字段必须报出来（帮助前端发现拼写错误），并给出合法列清单提示。
func TestFormPatchUnknownField(t *testing.T) {
	raw := json.RawMessage(`{"sample_projekt": "M2"}`)
	_, unknown, err := ParseFormPatch(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "sample_projekt" {
		t.Fatalf("应报告未知字段，得到 %v", unknown)
	}
}

// 顶层也允许直接写扁平缺陷列名。
func TestFormPatchFlatDefectFields(t *testing.T) {
	raw := json.RawMessage(`{"prick_concave": 5, "flange_recess_both": 2, "total_qty": 10}`)
	patch, unknown, err := ParseFormPatch(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("扁平缺陷列不应算未知字段: %v", unknown)
	}
	if patch.Defects["prick"]["concave"].Value != 5 || patch.Defects["flange_recess"]["both"].Value != 2 {
		t.Fatalf("扁平缺陷列未并入: %+v", patch.Defects)
	}
	if !patch.TotalQty.IsSet || patch.TotalQty.Value != 10 {
		t.Fatalf("表头栏目未解析: %+v", patch.TotalQty)
	}
}

// 非法缺陷编码/表面必须被拒绝（400）。
func TestFormPatchInvalidDefectCode(t *testing.T) {
	raw := json.RawMessage(`{"defects": {"not_a_defect": {"concave": 1}}}`)
	patch, _, err := ParseFormPatch(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := patch.ValidateFieldNames(); err == nil {
		t.Fatal("非法缺陷编码应报错")
	}

	raw2 := json.RawMessage(`{"defects": {"prick": {"side": 1}}}`)
	p2, _, err := ParseFormPatch(raw2)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := p2.ValidateFieldNames(); err == nil {
		t.Fatal("非法表面维度应报错")
	}
}
