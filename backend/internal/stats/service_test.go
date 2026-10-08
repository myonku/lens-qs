package stats

import (
	"context"
	"testing"

	"github.com/myonku/lens-qs-api/internal/store"
)

// 汇总按「表单提交」聚合：先造几张不同工序 / 样品维度的表单，再验证分组。
func TestSummaryGroupByDimensions(t *testing.T) {
	db := store.NewTestDB(t)
	ctx := context.Background()

	type seed struct {
		id        string
		formNo    string
		operation string
		project   string
		spec      string
		size      string
		total     int
		qualified int
		defect    int
	}
	rows := []seed{
		{"f1", "F-001", "一次加硬", "M2", "S2", "SM", 100, 100, 0},
		{"f2", "F-002", "二次加硬", "M2", "S2", "SM", 50, 48, 2},
		{"f3", "F-003", "一次加硬", "M1", "S1", "MD", 200, 190, 10},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO forms
			(id, form_no, operation_code, sample_raw, sample_project, sample_spec, sample_size,
			 state, save_count, total_qty, qualified_qty, defect_qty, is_complete, created_at, updated_at)
			VALUES (?, ?, ?, 'raw', ?, ?, ?, 'ACTIVE', 1, ?, ?, ?, 1,
			 '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
			r.id, r.formNo, r.operation, r.project, r.spec, r.size, r.total, r.qualified, r.defect); err != nil {
			t.Fatalf("插入表单失败: %v", err)
		}
	}

	svc := NewService(db, "sqlite")

	// 总计
	total, err := svc.Summary(ctx, Filter{OnlyActive: true}, nil)
	if err != nil {
		t.Fatalf("总计失败: %v", err)
	}
	if total.Total.FormCount != 3 || total.Total.TotalQty != 350 || total.Total.QualifiedQty != 338 {
		t.Fatalf("总计不正确: %+v", total.Total)
	}

	// 按规格
	bySpec, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"sample_spec"})
	if err != nil {
		t.Fatalf("按规格分组失败: %v", err)
	}
	got := map[string]GroupResult{}
	for _, g := range bySpec.Groups {
		got[g.Key] = g
	}
	if g := got["S2"]; g.FormCount != 2 || g.TotalQty != 150 {
		t.Errorf("S2 分组不正确: %+v", g)
	}
	if g := got["S1"]; g.FormCount != 1 || g.PassRate != 95 {
		t.Errorf("S1 分组不正确: %+v", g)
	}

	// 按工序（本次重构的重点：工序是表单的第一列）
	byOp, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"operation_code"})
	if err != nil {
		t.Fatalf("按工序分组失败: %v", err)
	}
	opTotals := map[string]int{}
	for _, g := range byOp.Groups {
		opTotals[g.Key] = g.TotalQty
	}
	if opTotals["一次加硬"] != 300 || opTotals["二次加硬"] != 50 {
		t.Fatalf("按工序分组不正确: %+v", opTotals)
	}

	// 组合维度
	combo, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"operation_code", "sample_spec"})
	if err != nil {
		t.Fatalf("组合分组失败: %v", err)
	}
	if len(combo.Groups) != 3 {
		t.Fatalf("应有 3 个组合分组，得到 %d: %+v", len(combo.Groups), combo.Groups)
	}

	// 非法维度
	if _, err := svc.Summary(ctx, Filter{}, []string{"not_a_dimension"}); err == nil {
		t.Error("非法分组维度应报错")
	}
}

// 不良缺陷分布：按明细行的缺陷列求和。
func TestDefectBreakdown(t *testing.T) {
	db := store.NewTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO forms
		(id, form_no, operation_code, sample_raw, state, save_count, created_at, updated_at)
		VALUES ('f1', 'F-001', '一次加硬', 'raw', 'ACTIVE', 1,
		 '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`); err != nil {
		t.Fatalf("插入表单失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO form_lines
		(id, form_id, seq, total_qty, qualified_qty, defect_qty,
		 prick_concave, prick_convex, prick_both, dirt_concave, garbage_both,
		 created_at, updated_at)
		VALUES ('l1', 'f1', 1, 100, 95, 5, 2, 1, 0, 3, 4,
		 '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`); err != nil {
		t.Fatalf("插入明细失败: %v", err)
	}

	svc := NewService(db, "sqlite")
	res, err := svc.DefectBreakdown(ctx, Filter{OnlyActive: true})
	if err != nil {
		t.Fatalf("缺陷分布失败: %v", err)
	}
	defects, ok := res["defects"].(map[string]any)
	if !ok {
		t.Fatalf("返回结构不正确: %+v", res)
	}
	prick, ok := defects["prick"].(map[string]any)
	if !ok {
		t.Fatalf("缺少点伤: %+v", defects)
	}
	surfaces, ok := prick["surfaces"].(map[string]int)
	if !ok {
		t.Fatalf("surface 结构不正确: %T", prick["surfaces"])
	}
	if surfaces["concave"] != 2 || surfaces["convex"] != 1 || surfaces["both"] != 0 || surfaces["total"] != 3 {
		t.Fatalf("点伤统计不正确: %+v", surfaces)
	}
	// 未填的列应为 0
	dirt, _ := defects["dirt"].(map[string]any)
	dirtSurfaces, _ := dirt["surfaces"].(map[string]int)
	if dirtSurfaces["concave"] != 3 {
		t.Fatalf("脏污统计不正确: %+v", dirtSurfaces)
	}
	garbage, _ := defects["garbage"].(map[string]any)
	garbageSurfaces, _ := garbage["surfaces"].(map[string]int)
	if garbageSurfaces["both"] != 4 {
		t.Fatalf("垃圾点统计不正确: %+v", garbageSurfaces)
	}
}

func TestSupportedDimensions(t *testing.T) {
	dims := SupportedDimensions()
	if len(dims) < 10 {
		t.Fatalf("可分组维度过少: %v", dims)
	}
	want := []string{"operation_code", "sample_project", "sample_spec", "sample_size", "unit", "day", "month"}
	for _, w := range want {
		found := false
		for _, d := range dims {
			if d == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("缺少分组维度 %s（列表: %v）", w, dims)
		}
	}
}
