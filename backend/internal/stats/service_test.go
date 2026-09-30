package stats

import (
	"context"
	"testing"

	"github.com/myonku/lens-qs-api/internal/store"
)

// 汇总统计直接读「笔」上的落库字段，因此这里先造两笔数据再验证分组。
func TestSummaryGroupBySampleDimensions(t *testing.T) {
	db := store.NewTestDB(t)
	ctx := context.Background()

	seed := []struct {
		formNo   string
		project  string
		spec     string
		size     string
		process  string
		total    int
		qualified int
		defect   int
	}{
		{"F-1", "M2", "S2", "SM", "一次加硬", 100, 100, 0},
		{"F-2", "M2", "S2", "SM", "二次加硬", 50, 48, 2},
		{"F-3", "M1", "S1", "MD", "一次加硬", 200, 190, 10},
	}

	for i, s := range seed {
		if _, err := db.ExecContext(ctx, `INSERT INTO forms (id, form_no, sample_raw, sample_project,
			sample_spec, sample_size, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'ACTIVE', '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
			"form-"+s.formNo, s.formNo, "raw", s.project, s.spec, s.size); err != nil {
			t.Fatalf("插入表单失败: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO form_records (id, form_id, sequence_no, process_code,
			total_qty, qualified_qty, defect_qty, is_complete, state, save_count, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, ?, ?, 1, 'ACTIVE', 1, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
			"rec-"+s.formNo, "form-"+s.formNo, s.process, s.total, s.qualified, s.defect); err != nil {
			t.Fatalf("插入工序记录失败: %v", err)
		}
		_ = i
	}

	svc := NewService(db, "sqlite")

	// 不分组：总计
	total, err := svc.Summary(ctx, Filter{OnlyActive: true}, nil)
	if err != nil {
		t.Fatalf("总计失败: %v", err)
	}
	if total.Total.RecordCount != 3 || total.Total.TotalQty != 350 || total.Total.QualifiedQty != 338 {
		t.Fatalf("总计不正确: %+v", total.Total)
	}

	// 按规格分组
	bySpec, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"sample_spec"})
	if err != nil {
		t.Fatalf("按规格分组失败: %v", err)
	}
	if len(bySpec.Groups) != 2 {
		t.Fatalf("应有 2 个规格分组，得到 %d: %+v", len(bySpec.Groups), bySpec.Groups)
	}
	got := map[string]GroupResult{}
	for _, g := range bySpec.Groups {
		got[g.Key] = g
	}
	if g := got["S2"]; g.RecordCount != 2 || g.TotalQty != 150 {
		t.Errorf("S2 分组不正确: %+v", g)
	}
	if g := got["S1"]; g.RecordCount != 1 || g.TotalQty != 200 || g.PassRate != 95 {
		t.Errorf("S1 分组不正确: %+v", g)
	}

	// 按项目 + 规格组合
	combo, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"sample_project", "sample_spec"})
	if err != nil {
		t.Fatalf("组合分组失败: %v", err)
	}
	if len(combo.Groups) != 2 {
		t.Fatalf("应有 2 个组合分组，得到 %d: %+v", len(combo.Groups), combo.Groups)
	}

	// 按工序分组
	byProc, err := svc.Summary(ctx, Filter{OnlyActive: true}, []string{"process"})
	if err != nil {
		t.Fatalf("按工序分组失败: %v", err)
	}
	if len(byProc.Groups) != 2 {
		t.Fatalf("应有 2 个工序分组，得到 %d", len(byProc.Groups))
	}

	// 非法维度应返回 400
	if _, err := svc.Summary(ctx, Filter{}, []string{"not_a_dimension"}); err == nil {
		t.Error("非法分组维度应报错")
	}
}

func TestSupportedDimensions(t *testing.T) {
	dims := SupportedDimensions()
	if len(dims) < 10 {
		t.Fatalf("可分组维度过少: %v", dims)
	}
	want := map[string]bool{"sample_project": false, "sample_spec": false, "sample_size": false, "process": false}
	for _, d := range dims {
		if _, ok := want[d]; ok {
			want[d] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("缺少分组维度 %s（列表: %v）", k, dims)
		}
	}
}
