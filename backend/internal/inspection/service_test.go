package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

// 覆盖需求 §8 的两个场景：
//   A) 分次增量填写：每次只提交本次字段，未提交字段保持原值
//   B) 一次性完整录入：一次提交表单 + 多笔 + 每笔明细
//
// 以及每次保存都必须留历史快照与操作日志。
func TestSaveRecordMergeAndHistory(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u-test", DisplayName: "测试用户", IP: "127.0.0.1"}

	// ---- 场景 A：第 1 次录入（只填了工序与检验人）----
	formPatch, _ := ParsePatch(json.RawMessage(`{
		"sample_raw": "M2 S2 ND2 SMR",
		"sample_project": "M2",
		"sample_spec": "S2",
		"unit_text": "一厂"
	}`))
	recPatch, _ := ParsePatch(json.RawMessage(`{
		"process_code": "一次加硬",
		"inspected_by": "张赛赛",
		"inspected_at": "2026-09-09T10:30:00+08:00"
	}`))
	res, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo:      "M-260908-01",
		FormPatch:   formPatch,
		RecordPatch: recPatch,
		Lines:       []RecordLine{{TotalQty: 100, QualifiedQty: 100}},
		LinesGiven:  true,
		Enforce:     EnforceWarn,
		Raw:         json.RawMessage(`{"note":"第一次录入"}`),
	})
	if err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}
	if res.Record.SequenceNo != 1 || res.Record.SaveCount != 1 {
		t.Fatalf("首笔序号/保存次数不正确: seq=%d save=%d", res.Record.SequenceNo, res.Record.SaveCount)
	}
	if res.Record.TotalQty != 100 || res.Record.QualifiedQty != 100 {
		t.Fatalf("汇总未被服务端重算: %+v", res.Record)
	}
	if res.Record.State != "ACTIVE" {
		t.Fatalf("状态应为 ACTIVE: %s", res.Record.State)
	}
	// 缺表单既有栏目 → 应给出警告而非错误
	if res.IsComplete {
		t.Error("只填了部分栏目时应标记为未完成")
	}
	if len(res.MissingFields) == 0 {
		t.Error("应返回缺失字段清单")
	}
	if len(res.Errors) != 0 {
		t.Errorf("默认模式不应有阻断错误: %+v", res.Errors)
	}

	recordID := res.Record.ID

	// ---- 场景 A：第 2 次保存（只补两个字段，其余必须保持）----
	recPatch2, _ := ParsePatch(json.RawMessage(`{
		"submitted_by": "李四",
		"machine_operator": "王五"
	}`))
	res2, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo:      "M-260908-01",
		RecordID:    recordID,
		RecordPatch: recPatch2,
		Enforce:     EnforceWarn,
		Reason:      "补填送检人与操机员",
	})
	if err != nil {
		t.Fatalf("增量保存失败: %v", err)
	}
	if res2.Record.SaveCount != 2 {
		t.Fatalf("save_count 应为 2，得到 %d", res2.Record.SaveCount)
	}
	if res2.Record.ProcessCode == nil || *res2.Record.ProcessCode != "一次加硬" {
		t.Fatal("未提交的工序字段被清空了（字段级合并语义被破坏）")
	}
	if res2.Record.InspectedBy == nil || *res2.Record.InspectedBy != "张赛赛" {
		t.Fatal("未提交的检验人字段被清空了")
	}
	if res2.Record.MachineOperator == nil || *res2.Record.MachineOperator != "王五" {
		t.Fatal("本次提交的字段未写入")
	}
	// 未提交明细时，明细应保持原样
	if len(res2.Record.Lines) != 1 || res2.Record.Lines[0].TotalQty != 100 {
		t.Fatalf("未提交明细时明细应保持原样: %+v", res2.Record.Lines)
	}

	// ---- 历史快照：第 2 次保存前的内容应被留档 ----
	history, err := svc.Store().ListHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("查询历史失败: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("应恰好有 1 条历史快照，得到 %d", len(history))
	}
	if history[0].SaveCount != 1 {
		t.Fatalf("历史快照对应的保存次数应为 1，得到 %d", history[0].SaveCount)
	}
	if history[0].ChangedBy == nil || *history[0].ChangedBy != "u-test" {
		t.Error("历史快照应记录"+"修改者（系统用户）")
	}

	// ---- 操作日志：create_form、create_record、save_record 都应留痕 ----
	logs, err := svc.Store().ListAudit(ctx, "form_record", recordID, 20)
	if err != nil {
		t.Fatalf("查询操作日志失败: %v", err)
	}
	if len(logs) < 2 {
		t.Fatalf("操作日志应至少 2 条（创建 + 保存），得到 %d", len(logs))
	}

	// ---- 场景 B：新增第 2 笔（另一道工序，由系统把序号排在 2）----
	recPatch3, _ := ParsePatch(json.RawMessage(`{
		"process_code": "二次加硬",
		"inspected_by": "张三",
		"inspected_at": "2026-09-10T09:00:00+08:00",
		"submitted_by": "李四",
		"submitted_at": "2026-09-09T08:00:00+08:00",
		"machine_operator": "王五",
		"handover_by": "赵六"
	}`))
	res3, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo:      "M-260908-01",
		FormPatch:   Patch{},
		RecordPatch: recPatch3,
		Lines:       []RecordLine{{TotalQty: 50, QualifiedQty: 48, DefectQty: 2, SpotConcave: 2}},
		LinesGiven:  true,
		Enforce:     EnforceWarn,
	})
	if err != nil {
		t.Fatalf("新增第二笔失败: %v", err)
	}
	if res3.Record.SequenceNo != 2 {
		t.Fatalf("第二笔序号应为 2，得到 %d", res3.Record.SequenceNo)
	}
	if res3.Record.IsComplete != true {
		t.Errorf("第二笔栏目齐全且有问题明细，应判定为完整，missing=%v", res3.MissingFields)
	}
	if res3.Record.PassRate == nil || *res3.Record.PassRate != 96 {
		t.Errorf("良率应为 96，得到 %v", res3.Record.PassRate)
	}

	// ---- 表单查询：应返回 2 笔，每笔带明细 ----
	form, err := svc.Store().GetFormByNo(ctx, "M-260908-01")
	if err != nil || form == nil {
		t.Fatalf("查询表单失败: %v", err)
	}
	records, err := svc.Store().ListRecords(ctx, form.ID)
	if err != nil {
		t.Fatalf("查询工序记录失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("应有 2 笔记录，得到 %d", len(records))
	}
	lines, err := svc.Store().ListLines(ctx, records[1].ID)
	if err != nil {
		t.Fatalf("查询明细失败: %v", err)
	}
	if len(lines) != 1 || lines[0].QualifiedQty != 48 {
		t.Fatalf("第二笔明细不正确: %+v", lines)
	}

	// ---- 明细提交即整笔替换 ----
	res4, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo:      "M-260908-01",
		RecordID:    recordID,
		RecordPatch: Patch{},
		Lines: []RecordLine{
			{TotalQty: 60, QualifiedQty: 60},
			{TotalQty: 40, QualifiedQty: 40},
		},
		LinesGiven: true,
		Enforce:    EnforceWarn,
	})
	if err != nil {
		t.Fatalf("替换明细失败: %v", err)
	}
	if len(res4.Record.Lines) != 2 {
		t.Fatalf("明细应被替换为 2 行，得到 %d", len(res4.Record.Lines))
	}
	if res4.Record.TotalQty != 100 || res4.Record.QualifiedQty != 100 {
		t.Fatalf("替换明细后汇总应重算为 100/100，得到 %d/%d", res4.Record.TotalQty, res4.Record.QualifiedQty)
	}
}

// strict 模式应阻断自相矛盾的数据；默认模式不阻断。
func TestSaveRecordEnforceStrict(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u2", DisplayName: "测试用户"}

	bad := []RecordLine{{TotalQty: 100, QualifiedQty: 90, DefectQty: 5}} // 90+5 != 100

	// 默认（warn）：能存下去
	_, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo: "M-1", FormPatch: Patch{}, RecordPatch: Patch{},
		Lines: bad, LinesGiven: true, Enforce: EnforceWarn,
	})
	if err != nil {
		t.Fatalf("默认模式应允许保存不完整/矛盾的数据: %v", err)
	}

	// strict：应返回 422 校验错误
	_, err = svc.SaveRecord(ctx, actor, SaveInput{
		FormNo: "M-2", FormPatch: Patch{}, RecordPatch: Patch{},
		Lines: bad, LinesGiven: true, Enforce: EnforceStrict,
	})
	var e *httpx.Error
	if !errors.As(err, &e) || e.Status != 422 {
		t.Fatalf("strict 模式应返回 422 校验错误，得到 %v", err)
	}
}

// 乐观锁：save_count 不匹配时返回 409。
func TestSaveRecordOptimisticLock(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u3"}

	res, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo: "M-3", FormPatch: Patch{}, RecordPatch: Patch{}, Enforce: EnforceWarn,
	})
	if err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}

	wrong := 99
	_, err = svc.SaveRecord(ctx, actor, SaveInput{
		FormNo: "M-3", RecordID: res.Record.ID, FormPatch: Patch{}, RecordPatch: Patch{},
		Enforce: EnforceWarn, Expected: &wrong,
	})
	var e *httpx.Error
	if !errors.As(err, &e) || e.Status != 409 {
		t.Fatalf("期望 409 冲突，得到 %v", err)
	}
}

// 作废是逻辑标记：数据与历史保留，不物理删除。
func TestVoidRecordKeepsData(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u4"}

	res, err := svc.SaveRecord(ctx, actor, SaveInput{
		FormNo: "M-4", FormPatch: Patch{}, RecordPatch: Patch{},
		Lines: []RecordLine{{TotalQty: 10, QualifiedQty: 10}}, LinesGiven: true, Enforce: EnforceWarn,
	})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	rec, err := svc.VoidRecord(ctx, actor, res.Record.ID, "录入错误")
	if err != nil {
		t.Fatalf("作废失败: %v", err)
	}
	if rec.State != "VOID" || rec.VoidReason == nil || *rec.VoidReason != "录入错误" {
		t.Fatalf("作废标记不正确: %+v", rec)
	}
	// 数据仍在
	again, err := svc.Store().GetRecord(ctx, res.Record.ID)
	if err != nil || again == nil {
		t.Fatalf("作废后记录不应消失: %v", err)
	}
	lines, err := svc.Store().ListLines(ctx, res.Record.ID)
	if err != nil || len(lines) != 1 {
		t.Fatalf("作废后明细不应消失: %v %+v", err, lines)
	}
}

// 表单级更新也是字段级合并：只改一个字段时，样品原始串不能被清空。
func TestSaveFormMergeKeepsRaw(t *testing.T) {
	db := store.NewTestDB(t)
	svc := NewService(NewStore(db, "sqlite"))
	ctx := context.Background()
	actor := Actor{UserID: "u5"}

	p1, _ := ParsePatch(json.RawMessage(`{"sample_raw":"M2 S2 ND2 SMR","sample_project":"M2"}`))
	if _, err := svc.SaveForm(ctx, actor, SaveInput{FormNo: "M-5", FormPatch: p1, Enforce: EnforceWarn}); err != nil {
		t.Fatalf("创建表单失败: %v", err)
	}

	p2, _ := ParsePatch(json.RawMessage(`{"unit_text":"一厂"}`))
	if _, err := svc.SaveForm(ctx, actor, SaveInput{FormNo: "M-5", FormPatch: p2, Enforce: EnforceWarn}); err != nil {
		t.Fatalf("更新表单失败: %v", err)
	}

	form, err := svc.Store().GetFormByNo(ctx, "M-5")
	if err != nil || form == nil {
		t.Fatalf("查询表单失败: %v", err)
	}
	if form.SampleRaw != "M2 S2 ND2 SMR" {
		t.Fatalf("原始串被改写或清空: %q", form.SampleRaw)
	}
	if form.UnitText == nil || *form.UnitText != "一厂" {
		t.Fatalf("单位未写入: %+v", form.UnitText)
	}
}
