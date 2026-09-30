package inspection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Service 实现表单 / 笔的保存、历史与校验。
type Service struct {
	store *Store
}

// NewService 构造服务。
func NewService(st *Store) *Service { return &Service{store: st} }

// Store 暴露底层 store，便于 handler 做只读查询。
func (s *Service) Store() *Store { return s.store }

// Actor 描述本次操作的系统用户（来自会话，不来自请求体）。
type Actor struct {
	UserID      string
	DisplayName string
	IP          string
	UserAgent   string
}

// SaveInput 是一次保存请求（字段级合并语义）。
type SaveInput struct {
	FormNo      string          // 必填：表单号
	SequenceNo  *int            // 指定则更新该笔，未指定则新建一笔
	RecordID    string          // 直接按笔 ID 更新（内部使用）
	FormPatch   Patch           // 表单级字段补丁
	RecordPatch Patch           // 笔级字段补丁
	Lines       []RecordLine    // 提交则整笔替换明细
	LinesGiven  bool            // 是否提交了 lines
	Enforce     Enforce         // 校验强度
	SaveMode    string          // merge（默认）| replace
	Reason      string          // 变更原因（写入历史）
	Expected    *int            // 乐观锁：期望的 save_count
	Raw         json.RawMessage // 原始请求体，作为操作日志的 after 快照
}

// SaveResult 是保存结果。
type SaveResult struct {
	Action        string        `json:"action"` // create_form | create_record | save_record | replace_record
	Form          *Form         `json:"form"`
	Record        *FormRecord   `json:"record"`
	Issues        []httpx.Issue `json:"issues"`
	Warnings      []httpx.Issue `json:"warnings"`
	Errors        []httpx.Issue `json:"errors"`
	MissingFields []string      `json:"missing_fields"`
	IsComplete    bool          `json:"is_complete"`
}

// SaveForm 创建或更新表单级信息（不涉及笔）。
func (s *Service) SaveForm(ctx context.Context, actor Actor, in SaveInput) (*SaveResult, error) {
	if strings.TrimSpace(in.FormNo) == "" {
		return nil, httpx.BadRequest("表单号（form_no）不能为空")
	}
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	form, action, unknown, err := s.upsertForm(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		return nil, httpx.BadRequest("存在无法识别的表单字段", ginH(unknown))
	}

	issues := append(ValidateForm(*form), formLevelUnknownPlaceholder()...)
	if err := s.writeAudit(ctx, tx, actor, action, "form", form.ID, nil, in.Raw, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return &SaveResult{Action: action, Form: form, Issues: issues, Warnings: httpx.Warnings(issues), Errors: httpx.Errors(issues)}, nil
}

// SaveRecord 保存一笔工序记录（核心接口，字段级合并）。
// 单笔调用：自带事务，处理完即提交。
func (s *Service) SaveRecord(ctx context.Context, actor Actor, in SaveInput) (*SaveResult, error) {
	if strings.TrimSpace(in.FormNo) == "" {
		return nil, httpx.BadRequest("表单号（form_no）不能为空")
	}
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := s.saveRecordTx(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return res, nil
}

// SaveBatch 在同一事务内依次保存同一表单下的多笔记录（次日集中录入场景）。
//
// 与逐笔调用 SaveRecord 的区别：**整批原子** —— 任一笔失败则整批回滚，
// 不会留下"一半成功"的表单与笔。
func (s *Service) SaveBatch(ctx context.Context, actor Actor, ins []SaveInput) ([]*SaveResult, error) {
	if len(ins) == 0 {
		return nil, httpx.BadRequest("records 不能为空")
	}
	for i := range ins {
		if strings.TrimSpace(ins[i].FormNo) == "" {
			return nil, httpx.BadRequest("表单号（form_no）不能为空")
		}
		// 批量语义：逐笔新建，忽略顶层传入的笔 ID 与乐观锁期望值
		ins[i].RecordID = ""
		ins[i].Expected = nil
	}

	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	results := make([]*SaveResult, 0, len(ins))
	for i := range ins {
		res, err := s.saveRecordTx(ctx, tx, actor, ins[i])
		if err != nil {
			// 整批回滚：用第几笔定位问题，便于调用方排查
			return nil, fmt.Errorf("第 %d 笔保存失败: %w", i+1, err)
		}
		results = append(results, res)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return results, nil
}

// saveRecordTx 是保存一笔的实际逻辑（调用方负责事务的开启与提交）。
func (s *Service) saveRecordTx(ctx context.Context, tx *sqlx.Tx, actor Actor, in SaveInput) (*SaveResult, error) {
	// 1) 表单：不存在则创建，存在则按补丁合并
	form, formAction, unknown, err := s.upsertForm(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		return nil, httpx.BadRequest("存在无法识别的表单字段", ginH(unknown))
	}

	// 2) 定位目标笔：按 ID 或按 (表单, 序号)，都没有则新建
	var (
		rec         *FormRecord
		recordActed string
	)
	switch {
	case in.RecordID != "":
		rec, err = s.store.LockRecord(ctx, tx, in.RecordID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, httpx.NotFound("工序记录不存在")
		}
		if rec.FormID != form.ID {
			return nil, httpx.BadRequest("该工序记录不属于指定表单")
		}
		recordActed = "save_record"
	case in.SequenceNo != nil:
		rec, err = s.findRecordBySeq(ctx, tx, form.ID, *in.SequenceNo)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			rec = &FormRecord{FormID: form.ID, SequenceNo: *in.SequenceNo}
			recordActed = "create_record"
		} else {
			recordActed = "save_record"
		}
	default:
		seq, err := s.store.NextSequenceNo(ctx, tx, form.ID)
		if err != nil {
			return nil, err
		}
		rec = &FormRecord{FormID: form.ID, SequenceNo: seq}
		recordActed = "create_record"
	}

	isNew := rec.ID == ""
	if !isNew && in.Expected != nil && *in.Expected != rec.SaveCount {
		return nil, httpx.Conflict(
			fmt.Sprintf("该记录已被他人修改（期望保存次数 %d，实际 %d）", *in.Expected, rec.SaveCount),
			ginH(map[string]any{"current": rec, "current_save_count": rec.SaveCount}),
		)
	}

	// 3) 更新前先取旧内容：用于历史快照与操作日志的 before
	var (
		beforeForm   *Form
		beforeRecord *FormRecord
		beforeLines  []RecordLine
	)
	if !isNew {
		beforeFormCopy := *form
		beforeRecordCopy := *rec
		beforeForm = &beforeFormCopy
		beforeRecord = &beforeRecordCopy
		beforeLines, err = s.store.ListLinesTx(ctx, tx, rec.ID)
		if err != nil {
			return nil, err
		}
	}

	// 4) 字段级合并笔级字段
	if in.SaveMode == "replace" {
		// 整体替换：保留不可变字段，其余清零后由补丁写入
		preserved := FormRecord{
			ID: rec.ID, FormID: rec.FormID, SequenceNo: rec.SequenceNo,
			RecordedBy: rec.RecordedBy, RecordedAt: rec.RecordedAt,
			CreatedAt: rec.CreatedAt, State: rec.State,
		}
		*rec = preserved
	}
	unknownRec, err := in.RecordPatch.Merge(rec, "form_id", "sequence_no", "save_count",
		"is_complete", "missing_fields", "total_qty", "qualified_qty", "defect_qty",
		"pass_rate", "state", "voided_at", "void_reason", "created_at")
	if err != nil {
		return nil, httpx.BadRequest("工序记录字段解析失败: " + err.Error())
	}
	if len(unknownRec) > 0 {
		return nil, httpx.BadRequest("存在无法识别的工序记录字段", ginH(unknownRec))
	}

	// 5) 系统留痕
	now := store.FormatTime(time.Now())
	if isNew {
		rec.RecordedBy = &actor.UserID
		rec.RecordedAt = &now
		rec.SaveCount = 1
	} else {
		rec.SaveCount++
	}
	rec.LastSavedBy = &actor.UserID
	rec.LastSavedAt = &now

	// 6) 明细：提交则整笔替换
	var lines []RecordLine
	if in.LinesGiven {
		lines = in.Lines
	} else if rec.ID != "" {
		lines, err = s.store.ListLinesTx(ctx, tx, rec.ID)
		if err != nil {
			return nil, err
		}
	}

	// 7) 服务端重算汇总与完成度
	rec.TotalQty, rec.QualifiedQty, rec.DefectQty, rec.PassRate = ComputeTotals(lines)
	missing, complete := ComputeCompleteness(*rec, lines)
	rec.IsComplete = complete
	if b, err := json.Marshal(missing); err == nil {
		mf := string(b)
		rec.MissingFields = &mf
	}

	// 8) 校验（默认只警告）
	issues := make([]httpx.Issue, 0, 12)
	issues = append(issues, ValidateForm(*form)...)
	issues = append(issues, ValidateRecord(*rec, form, time.Now())...)
	issues = append(issues, ValidateLines(lines, in.Enforce)...)
	if errs := httpx.Errors(issues); len(errs) > 0 {
		return nil, httpx.Validation("校验未通过（enforce=strict）", errs)
	}

	// 9) 落库
	if isNew {
		if err := s.store.InsertRecord(ctx, tx, rec); err != nil {
			return nil, err
		}
	} else {
		// 写历史快照（保存前的内容）
		if err := s.writeHistory(ctx, tx, beforeForm, beforeRecord, beforeLines, actor, in.Reason); err != nil {
			return nil, err
		}
		if err := s.store.UpdateRecord(ctx, tx, rec); err != nil {
			return nil, err
		}
	}
	if in.LinesGiven {
		if err := s.store.ReplaceLines(ctx, tx, rec.ID, lines); err != nil {
			return nil, err
		}
	}

	// 10) 操作日志
	action := recordActed
	if formAction == "create_form" {
		action = "create_form+" + recordActed
	}
	var before []byte
	if beforeRecord != nil {
		before, _ = json.Marshal(struct {
			Form   *Form        `json:"form"`
			Record *FormRecord  `json:"record"`
			Lines  []RecordLine `json:"lines"`
		}{beforeForm, beforeRecord, beforeLines})
	}
	if err := s.writeAudit(ctx, tx, actor, action, "form_record", rec.ID, before, in.Raw, nil); err != nil {
		return nil, err
	}

	rec.Form = form
	rec.Lines = lines
	if lines == nil {
		rec.Lines = []RecordLine{}
	}
	return &SaveResult{
		Action:        action,
		Form:          form,
		Record:        rec,
		Issues:        issues,
		Warnings:      httpx.Warnings(issues),
		Errors:        httpx.Errors(issues),
		MissingFields: missing,
		IsComplete:    complete,
	}, nil
}

// VoidRecord 逻辑作废一笔（数据与历史全部保留）。
func (s *Service) VoidRecord(ctx context.Context, actor Actor, recordID, reason string) (*FormRecord, error) {
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rec, err := s.store.LockRecord(ctx, tx, recordID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, httpx.NotFound("工序记录不存在")
	}
	now := store.FormatTime(time.Now())
	rec.State = "VOID"
	rec.VoidedAt = &now
	if strings.TrimSpace(reason) != "" {
		rec.VoidReason = &reason
	}
	if err := s.store.UpdateRecord(ctx, tx, rec); err != nil {
		return nil, err
	}
	if err := s.writeAudit(ctx, tx, actor, "void_record", "form_record", rec.ID, nil, nil, &reason); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return rec, nil
}

// ------------------------------- 内部辅助 -------------------------------

func (s *Service) upsertForm(ctx context.Context, tx *sqlx.Tx, actor Actor, in SaveInput) (*Form, string, []string, error) {
	form, err := s.store.LockFormByNo(ctx, tx, in.FormNo)
	if err != nil {
		return nil, "", nil, err
	}
	action := "update_form"
	if form == nil {
		form = &Form{FormNo: in.FormNo, State: "ACTIVE", SampleParsedBy: "auto"}
		action = "create_form"
	}
	if unknown, err := in.FormPatch.Merge(form, "form_no", "created_by", "created_at"); err != nil {
		return nil, "", nil, httpx.BadRequest("表单字段解析失败: " + err.Error())
	} else if len(unknown) > 0 {
		return form, action, unknown, nil
	}
	form.UpdatedBy = &actor.UserID
	if action == "create_form" {
		form.CreatedBy = &actor.UserID
		if err := s.store.InsertForm(ctx, tx, form); err != nil {
			return nil, "", nil, err
		}
		return form, action, nil, nil
	}
	if err := s.store.UpdateForm(ctx, tx, form); err != nil {
		return nil, "", nil, err
	}
	return form, action, nil, nil
}

func (s *Service) findRecordBySeq(ctx context.Context, tx *sqlx.Tx, formID string, seq int) (*FormRecord, error) {
	var r FormRecord
	err := tx.GetContext(ctx, &r, s.store.rebind(`SELECT `+recordColumns+`
		FROM form_records WHERE form_id = ? AND sequence_no = ?`), formID, seq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询工序记录失败: %w", err)
	}
	return &r, nil
}

func (s *Service) writeHistory(ctx context.Context, tx *sqlx.Tx, form *Form, rec *FormRecord, lines []RecordLine, actor Actor, reason string) error {
	if rec == nil {
		return nil
	}
	entry := &HistoryEntry{
		RecordID:  rec.ID,
		FormID:    rec.FormID,
		SaveCount: rec.SaveCount,
		ChangedBy: &actor.UserID,
	}
	if form != nil {
		if b, err := json.Marshal(form); err == nil {
			s := string(b)
			entry.FormSnapshot = &s
		}
	}
	if b, err := json.Marshal(rec); err == nil {
		s := string(b)
		entry.RecordSnapshot = &s
	}
	if b, err := json.Marshal(lines); err == nil {
		s := string(b)
		entry.LinesSnapshot = &s
	}
	entry.SavedBy = rec.LastSavedBy
	entry.SavedAt = rec.LastSavedAt
	if strings.TrimSpace(reason) != "" {
		entry.Reason = &reason
	}
	return s.store.InsertHistory(ctx, tx, entry)
}

func (s *Service) writeAudit(ctx context.Context, tx *sqlx.Tx, actor Actor, action, entity, entityID string, before, after []byte, detail *string) error {
	e := &AuditEntry{
		Action:    action,
		Entity:    entity,
		EntityID:  &entityID,
		CreatedAt: store.FormatTime(time.Now()),
	}
	if actor.UserID != "" {
		e.ActorID = &actor.UserID
	}
	if actor.DisplayName != "" {
		e.ActorName = &actor.DisplayName
	}
	if actor.IP != "" {
		e.IP = &actor.IP
	}
	if actor.UserAgent != "" {
		e.UserAgent = &actor.UserAgent
	}
	if len(before) > 0 {
		s := string(before)
		e.Before = &s
	}
	if len(after) > 0 {
		s := string(after)
		e.After = &s
	}
	e.Detail = detail
	return s.store.InsertAudit(ctx, tx, e)
}

// ginH 构造响应 details（避免直接依赖 gin）。
func ginH(v any) any { return v }

// formLevelUnknownPlaceholder 预留：表单级额外提示（当前无）。
func formLevelUnknownPlaceholder() []httpx.Issue { return nil }

// NewID 暴露 ID 生成，供 handler 在批量保存时预分配。
func NewID() string { return id.New() }
