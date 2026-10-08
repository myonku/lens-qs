package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

func newSvc(t *testing.T) (*Service, context.Context) {
	t.Helper()
	db := store.NewTestDB(t)
	return NewService(NewStore(db, "sqlite")), context.Background()
}

func mustPatch(t *testing.T, raw string) *FormPatch {
	t.Helper()
	p, unknown, err := ParseFormPatch(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("解析补丁失败: %v", err)
	}
	if len(unknown) > 0 {
		t.Fatalf("未知字段: %v", unknown)
	}
	return p
}

// 一次完整提交：表头 + 明细（含多个缺陷列）→ 汇总与合格率由服务端计算。
func TestSaveCreatesSubmissionWithAllFields(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1", DisplayName: "张赛赛"}

	patch := mustPatch(t, `{
		"operation_code": "一次加硬",
		"sample_raw": "M2 S2 ND2 SMR",
		"sample_project": "M2",
		"sample_spec": "S2",
		"unit_text": "一厂",
		"lines": [
			{ "total_qty": 100, "qualified_qty": 97, "defect_qty": 3,
			  "defects": {
				"prick": { "concave": 1, "convex": 1 },
				"flange_recess": { "both": 1 },
				"hardening_bubble": { "concave": 1 }
			  } }
		]
	}`)

	res, err := svc.Save(ctx, actor, SaveInput{
		FormNo:     "F-001",
		Operation:  "一次加硬",
		Patch:      patch,
		LinesGiven: true,
		Enforce:    EnforceWarn,
		Raw:        json.RawMessage(`{"form_no":"F-001"}`),
	})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if res.Action != "create_form" {
		t.Errorf("action 应为 create_form，得到 %s", res.Action)
	}
	s := res.Submission
	if s.FormNo != "F-001" || s.OperationCode != "一次加硬" {
		t.Errorf("表头不正确: %+v", s)
	}
	if s.SaveCount != 1 {
		t.Errorf("save_count 应为 1，得到 %d", s.SaveCount)
	}
	if s.TotalQty != 100 || s.QualifiedQty != 97 || s.DefectQty != 3 {
		t.Errorf("汇总未按明细重算: %+v", s)
	}
	if s.PassRate == nil || *s.PassRate != 97 {
		t.Errorf("合格率应为 97，得到 %v", s.PassRate)
	}
	if s.SubmittedBy == nil || *s.SubmittedBy != "u-1" {
		t.Errorf("应记录实际录入的系统用户: %v", s.SubmittedBy)
	}
	if !s.IsComplete {
		t.Errorf("工序/单位/样品/明细齐全应判为完成，missing=%v", s.MissingFields)
	}
	if len(s.Lines) != 1 {
		t.Fatalf("应有 1 行明细，得到 %d", len(s.Lines))
	}
	l := s.Lines[0]
	if l.Defects["prick"]["concave"] != 1 || l.Defects["prick"]["convex"] != 1 {
		t.Errorf("点伤未落库: %+v", l.Defects)
	}
	if l.Defects["flange_recess"]["both"] != 1 {
		t.Errorf("法兰边凹痕未落库: %+v", l.Defects)
	}
	if l.Defects["hardening_bubble"]["concave"] != 1 {
		t.Errorf("加硬气泡未落库: %+v", l.Defects)
	}
	if l.Defects["scratch"] != nil {
		t.Errorf("未填的缺陷列不应出现: %+v", l.Defects["scratch"])
	}
}

// 分次录入：第二次只提交部分栏目，未提交的保持原值，save_count 递增，并留下历史快照。
func TestSaveMergesPartialUpdateAndKeepsHistory(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}

	first := mustPatch(t, `{
		"operation_code": "一次加硬",
		"sample_raw": "M2 S2 ND2 SMR",
		"unit_text": "一厂",
		"lines": [ { "total_qty": 100, "qualified_qty": 100, "defect_qty": 0 } ]
	}`)
	res1, err := svc.Save(ctx, actor, SaveInput{FormNo: "F-002", Patch: first, LinesGiven: true})
	if err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}
	id := res1.Submission.ID

	// 第二次只改总数与一个缺陷列（不提交明细）
	second := mustPatch(t, `{"total_qty": 120, "defects": {"dirt": {"convex": 2}}}`)
	res2, err := svc.Save(ctx, actor, SaveInput{
		FormNo: "", SubmissionID: id, Patch: second, Reason: "补填总数",
	})
	if err != nil {
		t.Fatalf("第二次保存失败: %v", err)
	}
	s := res2.Submission
	if s.OperationCode != "一次加硬" {
		t.Error("未提交的工序被清空了")
	}
	if s.SampleRaw != "M2 S2 ND2 SMR" {
		t.Error("未提交的样品原始串被改动了（原始串是事实来源）")
	}
	if s.UnitText == nil || *s.UnitText != "一厂" {
		t.Error("未提交的单位被清空了")
	}
	if s.TotalQty != 120 {
		t.Errorf("总数应更新为 120，得到 %d", s.TotalQty)
	}
	if s.SaveCount != 2 {
		t.Errorf("save_count 应为 2，得到 %d", s.SaveCount)
	}
	// 未提交明细 → 明细保持原样（缺陷列不会被清空，也不会新增行）
	if len(s.Lines) != 1 || s.Lines[0].QualifiedQty != 100 {
		t.Errorf("未提交明细时明细应保持原样: %+v", s.Lines)
	}

	// 历史快照：应有 1 条（保存前的版本），记录修改者
	hist, err := svc.Store().ListHistory(ctx, id)
	if err != nil {
		t.Fatalf("查询历史失败: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("应有 1 条历史快照，得到 %d", len(hist))
	}
	if hist[0].SaveCount != 1 {
		t.Errorf("历史快照的 save_count 应为 1，得到 %d", hist[0].SaveCount)
	}
	if hist[0].ChangedBy == nil || *hist[0].ChangedBy != "u-1" {
		t.Error("历史快照应记录修改者")
	}
	// 快照里应能看到保存前的原始串
	fromSub, _, err := DecodeSnapshot(&hist[0])
	if err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	if fromSub.TotalQty != 100 {
		t.Errorf("快照应为保存前的总数 100，得到 %d", fromSub.TotalQty)
	}

	// 差异
	changes := DiffSubmissions(&fromSub, s, nil, s.Lines)
	var foundTotal bool
	for _, c := range changes {
		if c.Scope == "submission" && c.Field == "total_qty" {
			foundTotal = true
		}
	}
	if !foundTotal {
		t.Errorf("差异应包含 total_qty: %+v", changes)
	}

	// 操作日志
	logs, err := svc.Store().ListAudit(ctx, "form", id, 10)
	if err != nil {
		t.Fatalf("查询日志失败: %v", err)
	}
	if len(logs) < 2 {
		t.Errorf("操作日志应至少 2 条（创建 + 保存），得到 %d", len(logs))
	}
}

// 明细整体替换：提交 lines 即替换（旧行与列都会被覆盖）。
func TestSaveReplacesLines(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}

	first := mustPatch(t, `{"operation_code":"一次加硬","lines":[
		{"total_qty":100,"qualified_qty":100},
		{"total_qty":50,"qualified_qty":50}
	]}`)
	res, err := svc.Save(ctx, actor, SaveInput{FormNo: "F-003", Patch: first, LinesGiven: true})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if res.Submission.TotalQty != 150 {
		t.Fatalf("两行合计应为 150，得到 %d", res.Submission.TotalQty)
	}

	second := mustPatch(t, `{"lines":[{"total_qty":10,"qualified_qty":9,"defect_qty":1,
		"defects":{"crack":{"both":1}}}]}`)
	res2, err := svc.Save(ctx, actor, SaveInput{SubmissionID: res.Submission.ID, Patch: second, LinesGiven: true})
	if err != nil {
		t.Fatalf("替换明细失败: %v", err)
	}
	if len(res2.Submission.Lines) != 1 {
		t.Fatalf("明细应被替换为 1 行，得到 %d", len(res2.Submission.Lines))
	}
	if res2.Submission.TotalQty != 10 || res2.Submission.DefectQty != 1 {
		t.Errorf("汇总应重算为 10/1，得到 %d/%d", res2.Submission.TotalQty, res2.Submission.DefectQty)
	}
	if res2.Submission.Lines[0].Defects["crack"]["both"] != 1 {
		t.Errorf("新明细的缺陷列未写入: %+v", res2.Submission.Lines[0].Defects)
	}
}

// strict 模式阻塞矛盾数据；默认模式放行（最大灵活性）。
func TestSaveEnforceLevels(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}
	bad := mustPatch(t, `{"operation_code":"一次加硬","lines":[
		{"total_qty":100,"qualified_qty":90,"defect_qty":5}
	]}`)

	// 默认：能存下去，且给出警告
	res, err := svc.Save(ctx, actor, SaveInput{FormNo: "F-W1", Patch: bad, LinesGiven: true, Enforce: EnforceWarn})
	if err != nil {
		t.Fatalf("默认模式应允许保存: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("应产生 V-1 警告")
	}
	if len(res.Errors) != 0 {
		t.Errorf("默认模式不应有阻断错误: %+v", res.Errors)
	}

	// strict：422
	_, err = svc.Save(ctx, actor, SaveInput{FormNo: "F-W2", Patch: bad, LinesGiven: true, Enforce: EnforceStrict})
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 422 {
		t.Fatalf("strict 应返回 422，得到 %v", err)
	}
}

// 部分录入：只填工序也能保存（不强制必填），完成度为 false 并给出缺失项。
func TestSaveAllowsIncomplete(t *testing.T) {
	svc, ctx := newSvc(t)
	patch := mustPatch(t, `{"operation_code":"二次加硬"}`)
	res, err := svc.Save(ctx, Actor{UserID: "u-1"}, SaveInput{FormNo: "F-004", Patch: patch})
	if err != nil {
		t.Fatalf("只填工序也应能保存: %v", err)
	}
	if res.Submission.IsComplete {
		t.Error("只填工序应判为未完成")
	}
	if len(res.MissingFields) == 0 {
		t.Error("应给出缺失字段清单")
	}
	if len(res.Submission.Lines) != 0 {
		t.Errorf("不应有明细: %+v", res.Submission.Lines)
	}
}

// 表单号为空时自动生成（{YYMMDD}-{流水}）。
func TestSaveGeneratesFormNo(t *testing.T) {
	svc, ctx := newSvc(t)
	patch := mustPatch(t, `{"operation_code":"一次加硬"}`)
	res, err := svc.Save(ctx, Actor{UserID: "u-1"}, SaveInput{Patch: patch})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if res.Submission.FormNo == "" {
		t.Fatal("应自动生成表单号")
	}
	// 第二张应递增
	res2, err := svc.Save(ctx, Actor{UserID: "u-1"}, SaveInput{Patch: patch})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if res2.Submission.FormNo == res.Submission.FormNo {
		t.Fatalf("表单号应递增，得到相同的 %s", res.Submission.FormNo)
	}
}

// 乐观锁：save_count 不匹配 → 409。
func TestSaveOptimisticLock(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}
	res, err := svc.Save(ctx, actor, SaveInput{FormNo: "F-005", Patch: mustPatch(t, `{}`)})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	wrong := 99
	_, err = svc.Save(ctx, actor, SaveInput{
		SubmissionID: res.Submission.ID, Patch: mustPatch(t, `{}`), Expected: &wrong,
	})
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != 409 {
		t.Fatalf("期望 409，得到 %v", err)
	}
}

// 作废是逻辑标记：数据与明细保留。
func TestVoidKeepsData(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}
	patch := mustPatch(t, `{"operation_code":"一次加硬","lines":[{"total_qty":10,"qualified_qty":10}]}`)
	res, err := svc.Save(ctx, actor, SaveInput{FormNo: "F-006", Patch: patch, LinesGiven: true})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	sub, err := svc.VoidForm(ctx, actor, res.Submission.ID, "录入有误")
	if err != nil {
		t.Fatalf("作废失败: %v", err)
	}
	if sub.State != StateVoid || sub.VoidReason == nil || *sub.VoidReason != "录入有误" {
		t.Fatalf("作废标记不正确: %+v", sub)
	}
	again, err := svc.Store().GetSubmission(ctx, res.Submission.ID)
	if err != nil || again == nil {
		t.Fatalf("作废后仍应可读: %v", err)
	}
	if len(again.Lines) != 1 {
		t.Errorf("作废后明细应保留，得到 %d 行", len(again.Lines))
	}
}

// 批量提交整批原子：第二张失败则整批回滚。
func TestSaveBatchAtomic(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}

	ok := SaveInput{FormNo: "B-1", Patch: mustPatch(t, `{"operation_code":"一次加硬","lines":[
		{"total_qty":100,"qualified_qty":100}]}`), LinesGiven: true, Enforce: EnforceStrict}
	bad := SaveInput{FormNo: "B-2", Patch: mustPatch(t, `{"operation_code":"二次加硬","lines":[
		{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}`), LinesGiven: true, Enforce: EnforceStrict}

	if _, err := svc.SaveBatch(ctx, actor, []SaveInput{ok, bad}); err == nil {
		t.Fatal("第二张失败时整批应报错")
	}
	for _, no := range []string{"B-1", "B-2"} {
		sub, err := svc.Store().GetSubmissionByNo(ctx, no)
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if sub != nil {
			t.Fatalf("整批回滚后 %s 不应存在", no)
		}
	}
}

// 批量成功：多张表单一次写入。
func TestSaveBatchSuccess(t *testing.T) {
	svc, ctx := newSvc(t)
	actor := Actor{UserID: "u-1"}
	ins := []SaveInput{
		{FormNo: "B-10", Patch: mustPatch(t, `{"operation_code":"一次加硬","lines":[
			{"total_qty":100,"qualified_qty":98,"defect_qty":2,"defects":{"dirt":{"concave":2}}}]}`),
			LinesGiven: true},
		{FormNo: "B-11", Patch: mustPatch(t, `{"operation_code":"二次加硬","lines":[
			{"total_qty":50,"qualified_qty":50}]}`), LinesGiven: true},
	}
	results, err := svc.SaveBatch(ctx, actor, ins)
	if err != nil {
		t.Fatalf("批量保存失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("应返回 2 个结果，得到 %d", len(results))
	}
	if results[0].Submission.PassRate == nil || *results[0].Submission.PassRate != 98 {
		t.Errorf("第一张合格率应为 98，得到 %v", results[0].Submission.PassRate)
	}
	if results[1].Submission.OperationCode != "二次加硬" {
		t.Errorf("第二张工序不正确: %s", results[1].Submission.OperationCode)
	}
	if _, err := svc.SaveBatch(ctx, actor, nil); err == nil {
		t.Error("空批量应报错")
	}
}

// 工序表单注册表：未登记的工序按通用模板处理（不报错）。
func TestTemplateForUnknownOperation(t *testing.T) {
	tpl := TemplateFor("某个新工序")
	if tpl.Code != "某个新工序" {
		t.Errorf("应回落到以工序编码为标识的通用模板，得到 %+v", tpl)
	}
	if len(tpl.AllowedFields) != 0 {
		t.Error("通用模板应不限制列（开放全部）")
	}
	tpl2 := TemplateFor("一次加硬")
	if tpl2.Label != "一次加硬" {
		t.Errorf("已登记工序应返回登记信息: %+v", tpl2)
	}
}
