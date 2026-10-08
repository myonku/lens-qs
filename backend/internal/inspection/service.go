package inspection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/operations"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Service 实现表单提交的保存、历史与校验。
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

// SaveInput 是一次表单提交请求（字段级合并语义）。
type SaveInput struct {
	FormNo       string          // 表单号（为空时自动生成）
	SubmissionID string          // 指定则更新该提交，否则按 FormNo 找/建
	Operation    string          // 工序表单编码（决定默认列集合；可空）
	Patch        *FormPatch      // 字段补丁
	LinesGiven   bool            // 是否提交了 lines（提交即整笔替换）
	Enforce      Enforce         // 校验强度
	Reason       string          // 变更原因（写入历史与日志）
	Expected     *int            // 乐观锁：期望的 save_count
	Raw          json.RawMessage // 原始请求体（操作日志的 after）
}

// SaveResult 是保存结果。
type SaveResult struct {
	Action        string        `json:"action"` // create_form | save_form
	Submission    *Submission   `json:"submission"`
	Issues        []httpx.Issue `json:"issues"`
	Warnings      []httpx.Issue `json:"warnings"`
	Errors        []httpx.Issue `json:"errors"`
	MissingFields []string      `json:"missing_fields"`
	IsComplete    bool          `json:"is_complete"`
}

// Save 保存一次表单提交（核心接口）。
//
// 语义：
//   - 表单号不存在则创建（save_count=1），存在则**字段级合并**（save_count+1）；
//   - `lines` 一旦给出即整笔替换明细；
//   - 有明细时表头汇总按明细行重算；合格率按 合格品/检验总数 计算；
//   - 保存前写快照 + 操作日志。
func (s *Service) Save(ctx context.Context, actor Actor, in SaveInput) (*SaveResult, error) {
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := s.saveTx(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return res, nil
}

// SaveBatch 在同一事务内依次保存多张表单（整批原子：任一失败则整批回滚）。
func (s *Service) SaveBatch(ctx context.Context, actor Actor, ins []SaveInput) ([]*SaveResult, error) {
	if len(ins) == 0 {
		return nil, httpx.BadRequest("forms 不能为空")
	}
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	results := make([]*SaveResult, 0, len(ins))
	for i := range ins {
		res, err := s.saveTx(ctx, tx, actor, ins[i])
		if err != nil {
			return nil, fmt.Errorf("第 %d 张表单保存失败: %w", i+1, err)
		}
		results = append(results, res)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return results, nil
}

func (s *Service) saveTx(ctx context.Context, tx *sqlx.Tx, actor Actor, in SaveInput) (*SaveResult, error) {
	patch := in.Patch
	if patch == nil {
		patch = &FormPatch{}
	}
	if err := patch.ValidateFieldNames(); err != nil {
		return nil, httpx.BadRequest(err.Error())
	}

	// 1) 定位或创建提交
	var (
		sub    *Submission
		before *Submission
		action = "save_form"
	)

	if in.SubmissionID != "" {
		cur, err := s.store.LockSubmission(ctx, tx, in.SubmissionID)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			return nil, httpx.NotFound("表单提交不存在")
		}
		sub = cur
	} else {
		formNo := strings.TrimSpace(in.FormNo)
		if formNo == "" {
			generated, err := s.nextFormNo(ctx, tx)
			if err != nil {
				return nil, err
			}
			formNo = generated
		}
		cur, err := s.store.LockSubmissionByNo(ctx, tx, formNo)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			sub = &Submission{FormNo: formNo, State: StateActive, SaveCount: 1}
			if op := strings.TrimSpace(in.Operation); op != "" {
				sub.OperationCode = op
			}
			action = "create_form"
		} else {
			sub = cur
		}
	}

	isNew := action == "create_form"

	// 2) 乐观锁
	if !isNew && in.Expected != nil && *in.Expected != sub.SaveCount {
		return nil, httpx.Conflict(
			fmt.Sprintf("该表单已被他人修改（期望保存次数 %d，实际 %d）", *in.Expected, sub.SaveCount),
			map[string]any{"current": sub, "current_save_count": sub.SaveCount},
		)
	}

	// 3) 取保存前的旧内容（历史快照与操作日志的 before）
	var beforeLines []Line
	if !isNew {
		cur := *sub
		before = &cur
		lines, err := s.store.ListLinesTx(ctx, tx, sub.ID)
		if err != nil {
			return nil, err
		}
		beforeLines = lines
	}

	// 4) 应用补丁（字段级合并）
	patch.ApplyTo(sub)
	if isNew {
		sub.SaveCount = 1
	} else {
		sub.SaveCount++
	}
	now := store.FormatTime(time.Now())
	if sub.CreatedBy == nil {
		sub.CreatedBy = &actor.UserID
	}
	if sub.SubmittedBy == nil {
		sub.SubmittedBy = &actor.UserID
	}
	if sub.SubmittedAt == nil {
		sub.SubmittedAt = &now
	}
	sub.LastSavedBy = &actor.UserID
	sub.LastSavedAt = &now
	if sub.State == "" {
		sub.State = StateActive
	}

	// 5) 明细：给出即整笔替换
	var lines []Line
	if in.LinesGiven {
		lines = buildLines(patch.Lines)
	} else if !isNew {
		cur, err := s.store.ListLinesTx(ctx, tx, sub.ID)
		if err != nil {
			return nil, err
		}
		lines = cur
	}

	// 6) 汇总：
	//    a) 有明细 → 按明细重算；
	//    b) 但本次补丁里**显式给出**的表头栏目优先（支持"表头是合计值、与明细不全对应"的纸质填法）。
	if len(lines) > 0 {
		total, qualified, defect := Aggregate(lines)
		totalSet, qualifiedSet, defectSet := patch.AppliedHeaderFields()
		if !totalSet {
			sub.TotalQty = total
		}
		if !qualifiedSet {
			sub.QualifiedQty = qualified
		}
		if !defectSet {
			sub.DefectQty = defect
		}
	}
	if !patch.PassRate.IsSet {
		if r := ComputePassRate(sub.QualifiedQty, sub.TotalQty); r != nil {
			sub.PassRate = r
		}
	}

	// 7) 完成度与校验
	missing, complete := ComputeCompleteness(*sub, lines)
	sub.MissingFields = missing
	sub.IsComplete = complete

	issues := make([]httpx.Issue, 0, 12)
	issues = append(issues, ValidateSubmission(*sub, time.Now())...)
	issues = append(issues, ValidateLines(lines, in.Enforce)...)
	if errs := httpx.Errors(issues); len(errs) > 0 {
		return nil, httpx.Validation("校验未通过（enforce=strict）", errs)
	}

	// 8) 落库
	if isNew {
		if err := s.store.InsertSubmission(ctx, tx, sub); err != nil {
			return nil, err
		}
	} else {
		if err := s.writeHistory(ctx, tx, before, beforeLines, actor, in.Reason); err != nil {
			return nil, err
		}
		if err := s.store.UpdateSubmission(ctx, tx, sub); err != nil {
			return nil, err
		}
	}
	if in.LinesGiven {
		if err := s.store.ReplaceLines(ctx, tx, sub.ID, lines); err != nil {
			return nil, err
		}
	}

	// 9) 操作日志
	var beforeJSON []byte
	if before != nil {
		beforeJSON, _ = json.Marshal(map[string]any{"submission": before, "lines": beforeLines})
	}
	if err := s.writeAudit(ctx, tx, actor, action, sub.ID, beforeJSON, in.Raw, nil); err != nil {
		return nil, err
	}

	sub.Lines = lines
	if sub.Lines == nil {
		sub.Lines = []Line{}
	}
	return &SaveResult{
		Action:        action,
		Submission:    sub,
		Issues:        issues,
		Warnings:      httpx.Warnings(issues),
		Errors:        httpx.Errors(issues),
		MissingFields: missing,
		IsComplete:    complete,
	}, nil
}

// VoidForm 逻辑作废一次提交（数据与历史全部保留）。
func (s *Service) VoidForm(ctx context.Context, actor Actor, submissionID, reason string) (*Submission, error) {
	tx, err := s.store.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sub, err := s.store.LockSubmission(ctx, tx, submissionID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, httpx.NotFound("表单提交不存在")
	}
	now := store.FormatTime(time.Now())
	sub.State = StateVoid
	sub.VoidedAt = &now
	if strings.TrimSpace(reason) != "" {
		sub.VoidReason = &reason
	}
	if err := s.store.UpdateSubmission(ctx, tx, sub); err != nil {
		return nil, err
	}
	if err := s.writeAudit(ctx, tx, actor, "void_form", sub.ID, nil, nil, &reason); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}
	return sub, nil
}

// buildLines 把行补丁转换为明细行（seq 缺省时按顺序编号）。
func buildLines(patches []LinePatch) []Line {
	out := make([]Line, 0, len(patches))
	for i := range patches {
		l := Line{Seq: i + 1}
		patches[i].ApplyToLine(&l)
		out = append(out, l)
	}
	return out
}

// nextFormNo 生成表单号：{YYMMDD}-{当日流水:3}。
//
// 说明：与纸质表单的 `M-260908-01` 格式不同，这里用更简单的日期+流水；
// 调用方也可以直接传 form_no 覆盖（需求 N-3）。
func (s *Service) nextFormNo(ctx context.Context, tx *sqlx.Tx) (string, error) {
	day := time.Now().Format("060102")
	prefix := day + "-"
	var maxNo *string
	err := tx.GetContext(ctx, &maxNo,
		s.store.rebind(`SELECT MAX(form_no) FROM forms WHERE form_no LIKE ?`), prefix+"%")
	if err != nil {
		return "", fmt.Errorf("生成表单号失败: %w", err)
	}
	seq := 1
	if maxNo != nil {
		parts := strings.SplitN(*maxNo, "-", 2)
		if len(parts) == 2 {
			if n := parseIntBytes([]byte(parts[1])); n > 0 {
				seq = n + 1
			}
		}
	}
	return fmt.Sprintf("%s%03d", prefix, seq), nil
}

func (s *Service) writeHistory(ctx context.Context, tx *sqlx.Tx, before *Submission, beforeLines []Line, actor Actor, reason string) error {
	if before == nil {
		return nil
	}
	entry := &HistoryEntry{
		FormID:    before.ID,
		SaveCount: before.SaveCount,
		SavedBy:   before.LastSavedBy,
		SavedAt:   before.LastSavedAt,
		ChangedBy: &actor.UserID,
	}
	if b, err := json.Marshal(before); err == nil {
		str := string(b)
		entry.SubmissionSnapshot = &str
	}
	if b, err := json.Marshal(beforeLines); err == nil {
		str := string(b)
		entry.LinesSnapshot = &str
	}
	if strings.TrimSpace(reason) != "" {
		entry.Reason = &reason
	}
	return s.store.InsertHistory(ctx, tx, entry)
}

func (s *Service) writeAudit(ctx context.Context, tx *sqlx.Tx, actor Actor, action, entityID string, before, after []byte, detail *string) error {
	e := &AuditEntry{
		Action:    action,
		Entity:    "form",
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
		str := string(before)
		e.Before = &str
	}
	if len(after) > 0 {
		str := string(after)
		e.After = &str
	}
	e.Detail = detail
	return s.store.InsertAudit(ctx, tx, e)
}

// TemplateFor 返回某工序对应的表单模板。
func TemplateFor(operationCode string) operations.Template {
	return operations.Find(operationCode)
}

// LayoutFor 返回某工序表单的列布局（供前端渲染）。
func LayoutFor(operationCode string) map[string]any {
	tpl := TemplateFor(operationCode)
	layout := formfields.BuildLayout()
	return map[string]any{
		"operation":      tpl,
		"header_columns": layout.Header,
		"defect_group":   layout.DefectGroup,
		"surfaces":       layout.Surfaces,
		"surface_labels": layout.SurfaceLabels,
		"defect_columns": layout.DefectColumns,
	}
}
