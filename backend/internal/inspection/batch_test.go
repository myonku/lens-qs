package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

// 批量提交必须原子：任一笔失败则整批回滚，不留下"一半成功"的表单与笔。
func TestSaveBatchIsAtomic(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u-batch", DisplayName: "批量录入"}

	formPatch, _ := ParsePatch(json.RawMessage(`{"sample_raw":"M2 S2 SM R VID2","sample_project":"M2"}`))
	okRecord, _ := ParsePatch(json.RawMessage(`{"process_code":"一次加硬","inspected_by":"张赛赛"}`))
	// 第二笔故意在 strict 下制造矛盾：合格 90 + 不良 5 ≠ 总数 100
	badRecord, _ := ParsePatch(json.RawMessage(`{"process_code":"二次加硬","inspected_by":"张三"}`))

	_, err := svc.SaveBatch(ctx, actor, []SaveInput{
		{
			FormNo: "B-1", FormPatch: formPatch, RecordPatch: okRecord,
			Lines: []RecordLine{{TotalQty: 100, QualifiedQty: 100}}, LinesGiven: true,
			Enforce: EnforceStrict,
		},
		{
			FormNo: "B-1", FormPatch: Patch{}, RecordPatch: badRecord,
			Lines: []RecordLine{{TotalQty: 100, QualifiedQty: 90, DefectQty: 5}}, LinesGiven: true,
			Enforce: EnforceStrict,
		},
	})
	if err == nil {
		t.Fatal("第二笔校验失败时整批应报错")
	}
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 422 {
		t.Fatalf("应返回 422 校验错误，得到 %v", err)
	}

	// 关键断言：整批回滚 —— 表单与第一笔都不应存在
	form, err := svc.Store().GetFormByNo(ctx, "B-1")
	if err != nil {
		t.Fatalf("查询表单失败: %v", err)
	}
	if form != nil {
		t.Fatalf("整批回滚后表单不应存在，却查到 %s", form.FormNo)
	}
}

// 批量成功路径：多笔在同一事务内写入，序号连续。
func TestSaveBatchSuccess(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u-batch"}

	formPatch, _ := ParsePatch(json.RawMessage(`{"sample_raw":"M1 S1 MD L VID1 Rx","sample_project":"M1"}`))
	rec1, _ := ParsePatch(json.RawMessage(`{"process_code":"一次加硬","inspected_by":"张赛赛"}`))
	rec2, _ := ParsePatch(json.RawMessage(`{"process_code":"二次加硬","inspected_by":"张三"}`))

	results, err := svc.SaveBatch(ctx, actor, []SaveInput{
		{FormNo: "B-2", FormPatch: formPatch, RecordPatch: rec1,
			Lines: []RecordLine{{TotalQty: 200, QualifiedQty: 198, DefectQty: 2}}, LinesGiven: true, Enforce: EnforceWarn},
		{FormNo: "B-2", FormPatch: Patch{}, RecordPatch: rec2,
			Lines: []RecordLine{{TotalQty: 200, QualifiedQty: 200}}, LinesGiven: true, Enforce: EnforceWarn},
	})
	if err != nil {
		t.Fatalf("批量保存失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("应返回 2 个结果，得到 %d", len(results))
	}
	if results[0].Record.SequenceNo != 1 || results[1].Record.SequenceNo != 2 {
		t.Fatalf("序号应连续为 1、2，得到 %d、%d",
			results[0].Record.SequenceNo, results[1].Record.SequenceNo)
	}
	if results[0].Record.PassRate == nil || *results[0].Record.PassRate != 99 {
		t.Errorf("第一笔良率应为 99，得到 %v", results[0].Record.PassRate)
	}
	if results[1].Record.IsComplete {
		t.Errorf("第二笔只填了工序与检验人，应为未完成，missing=%v", results[1].MissingFields)
	}
	if len(results[1].MissingFields) == 0 {
		t.Error("未完成的笔应返回缺失字段清单")
	}
	if results[1].Record.TotalQty != 200 || results[1].Record.QualifiedQty != 200 {
		t.Errorf("第二笔汇总应为 200/200，得到 %d/%d",
			results[1].Record.TotalQty, results[1].Record.QualifiedQty)
	}

	form, err := svc.Store().GetFormByNo(ctx, "B-2")
	if err != nil || form == nil {
		t.Fatalf("查询表单失败: %v", err)
	}
	records, err := svc.Store().ListRecords(ctx, form.ID)
	if err != nil {
		t.Fatalf("查询笔失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("应有 2 笔，得到 %d", len(records))
	}
}

// 批量第一笔失败时不应创建表单（同样验证原子性）。
func TestSaveBatchFirstFailureLeavesNothing(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u-batch"}

	badRecord, _ := ParsePatch(json.RawMessage(`{"process_code":"一次加硬"}`))
	_, err := svc.SaveBatch(ctx, actor, []SaveInput{
		{FormNo: "B-3", FormPatch: Patch{}, RecordPatch: badRecord,
			Lines: []RecordLine{{TotalQty: 100, QualifiedQty: 1, DefectQty: 1}}, LinesGiven: true,
			Enforce: EnforceStrict},
	})
	if err == nil {
		t.Fatal("应报错")
	}
	form, err := svc.Store().GetFormByNo(ctx, "B-3")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if form != nil {
		t.Fatal("失败时不应留下表单")
	}
}

// 批量空数组应报参数错误。
func TestSaveBatchRequiresRecords(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	if _, err := svc.SaveBatch(context.Background(), Actor{UserID: "u"}, nil); err == nil {
		t.Fatal("空批量应报错")
	}
}
