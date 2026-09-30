package inspection

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

func TestParsePatchAndMerge(t *testing.T) {
	var rec FormRecord
	rec.SequenceNo = 1

	patch, err := ParsePatch(json.RawMessage(`{"process_code":"一次加硬","inspected_by":"张赛赛"}`))
	if err != nil {
		t.Fatalf("解析补丁失败: %v", err)
	}
	unknown, err := patch.Merge(&rec, "sequence_no")
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("不应有未知字段: %v", unknown)
	}
	if rec.ProcessCode == nil || *rec.ProcessCode != "一次加硬" {
		t.Fatalf("工序未被写入: %+v", rec.ProcessCode)
	}
	if rec.SequenceNo != 1 {
		t.Fatalf("只读字段被改写: %d", rec.SequenceNo)
	}

	// 第二次只提交另一个字段：已有字段应保持原值（字段级合并语义）
	patch2, _ := ParsePatch(json.RawMessage(`{"machine_operator":"王五"}`))
	if _, err := patch2.Merge(&rec, "sequence_no"); err != nil {
		t.Fatalf("第二次合并失败: %v", err)
	}
	if rec.ProcessCode == nil || *rec.ProcessCode != "一次加硬" {
		t.Fatal("未提交的字段被清空了，字段级合并语义被破坏")
	}
	if rec.MachineOperator == nil || *rec.MachineOperator != "王五" {
		t.Fatal("新字段未写入")
	}
}

func TestMergeUnknownFieldReported(t *testing.T) {
	var rec FormRecord
	patch, _ := ParsePatch(json.RawMessage(`{"not_a_column":1}`))
	unknown, err := patch.Merge(&rec)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "not_a_column" {
		t.Fatalf("未知字段应被报告: %v", unknown)
	}
}

func TestMergeExplicitNullClearsPointer(t *testing.T) {
	val := "张赛赛"
	rec := FormRecord{InspectedBy: &val}
	patch, _ := ParsePatch(json.RawMessage(`{"inspected_by":null}`))
	if _, err := patch.Merge(&rec); err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if rec.InspectedBy != nil {
		t.Fatal("显式 null 应清空指针字段")
	}
}

func TestValidateLinesWarnByDefault(t *testing.T) {
	lines := []RecordLine{{Seq: 1, TotalQty: 100, QualifiedQty: 90, DefectQty: 5}} // 90+5 != 100
	issues := ValidateLines(lines, EnforceWarn)
	if len(httpx.Errors(issues)) != 0 {
		t.Fatalf("默认模式不应产生阻断错误: %+v", issues)
	}
	if len(httpx.Warnings(issues)) == 0 {
		t.Fatal("应产生 V-1 警告")
	}

	strict := ValidateLines(lines, EnforceStrict)
	if len(httpx.Errors(strict)) == 0 {
		t.Fatal("strict 模式下 V-1 应阻断")
	}
}

// V-2 在任何模式下都不阻断：业务方已确认同一片镜片可计入多个类别。
func TestValidateLinesV2NeverBlocks(t *testing.T) {
	lines := []RecordLine{{Seq: 1, TotalQty: 10, QualifiedQty: 8, DefectQty: 2, SpotConcave: 3}}
	for _, mode := range []Enforce{EnforceWarn, EnforceStrict} {
		issues := ValidateLines(lines, mode)
		for _, is := range httpx.Errors(issues) {
			if is.Rule == RuleDefectSubtotal {
				t.Fatalf("mode=%s 时 V-2 不应为阻断错误", mode)
			}
		}
	}
}

func TestComputeTotalsAndCompleteness(t *testing.T) {
	lines := []RecordLine{
		{Seq: 1, TotalQty: 100, QualifiedQty: 100},
		{Seq: 2, TotalQty: 50, QualifiedQty: 45, DefectQty: 5},
	}
	total, qualified, defect, rate := ComputeTotals(lines)
	if total != 150 || qualified != 145 || defect != 5 {
		t.Fatalf("汇总不正确: total=%d qualified=%d defect=%d", total, qualified, defect)
	}
	if rate == nil || *rate < 96.6 || *rate > 96.7 {
		t.Fatalf("良率不正确: %v", rate)
	}

	rec := FormRecord{
		SubmittedBy: strp("李四"), SubmittedAt: strp("2026-09-08T09:00:00+08:00"),
		InspectedBy: strp("张赛赛"), InspectedAt: strp("2026-09-09T10:30:00+08:00"),
		MachineOperator: strp("王五"), HandoverBy: strp("赵六"),
	}
	missing, complete := ComputeCompleteness(rec, lines)
	if !complete || len(missing) != 0 {
		t.Fatalf("应判定为完整，missing=%v", missing)
	}

	rec2 := FormRecord{ProcessCode: strp("一次加硬")}
	missing2, complete2 := ComputeCompleteness(rec2, nil)
	if complete2 {
		t.Fatal("缺字段与明细时应判定为不完整")
	}
	if len(missing2) == 0 {
		t.Fatal("应返回缺失字段清单")
	}
}

func TestDiffRecords(t *testing.T) {
	from := &FormRecord{ID: "r1", SaveCount: 1, TotalQty: 100, QualifiedQty: 100}
	to := &FormRecord{ID: "r1", SaveCount: 2, TotalQty: 100, QualifiedQty: 95, DefectQty: 5}

	changes := DiffRecords(from, []RecordLine{{Seq: 1, TotalQty: 100, QualifiedQty: 100}},
		to, []RecordLine{{Seq: 1, TotalQty: 100, QualifiedQty: 95, DefectQty: 5}})
	if len(changes) == 0 {
		t.Fatal("应检测到差异")
	}
	found := map[string]bool{}
	for _, c := range changes {
		found[c.Scope+":"+c.Field] = true
	}
	if !found["record:qualified_qty"] {
		t.Errorf("应检测到 record.qualified_qty 变化: %+v", changes)
	}
	if !found["line:defect_qty"] {
		t.Errorf("应检测到明细 defect_qty 变化: %+v", changes)
	}
}

func TestParseEnforceDefaultsToWarn(t *testing.T) {
	for _, in := range []string{"", "warn", "WARN", "随便什么", "incremental"} {
		if got := ParseEnforce(in); got != EnforceWarn {
			t.Errorf("ParseEnforce(%q) = %q, 期望 warn（最大灵活性原则）", in, got)
		}
	}
	if got := ParseEnforce("strict"); got != EnforceStrict {
		t.Errorf("ParseEnforce(\"strict\") = %q", got)
	}
}

func TestValidateRecordTimeOrderIsWarning(t *testing.T) {
	rec := FormRecord{
		ProcessCode: strp("一次加硬"),
		SubmittedAt: strp("2026-09-10T09:00:00+08:00"),
		InspectedAt: strp("2026-09-09T09:00:00+08:00"),
		InspectedBy: strp("张赛赛"),
	}
	issues := ValidateRecord(rec, &Form{SampleProject: strp("M2")}, time.Now())
	var found bool
	for _, is := range issues {
		if is.Rule == RuleTimeOrder {
			found = true
			if is.Level != "warning" {
				t.Errorf("V-6 应为警告，得到 %s", is.Level)
			}
		}
	}
	if !found {
		t.Errorf("应产生 V-6 警告: %+v", issues)
	}
}

func strp(s string) *string { return &s }
