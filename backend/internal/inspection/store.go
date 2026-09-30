package inspection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Store 负责表单 / 笔 / 明细 / 历史 / 操作日志的持久化。
type Store struct {
	db     *sqlx.DB
	driver string
}

// NewStore 构造 Store。driver 取 "sqlite" 或 "postgres"，用于占位符重写。
func NewStore(db *sqlx.DB, driver string) *Store {
	return &Store{db: db, driver: driver}
}

// DB 暴露原始句柄（供 handler 做只读查询）。
func (s *Store) DB() *sqlx.DB { return s.db }

// rebind 把 ? 占位符转换为当前驱动需要的写法。
func (s *Store) rebind(q string) string { return s.db.Rebind(q) }

// ------------------------------- 表单 -------------------------------

const formColumns = `id, form_no, unit_id, unit_text, sample_raw,
	sample_project, sample_model, sample_spec, sample_size, sample_side,
	sample_class, sample_is_rx, sample_drawing_id, sample_unmatched_reason,
	sample_parsed_by, sample_confidence, sample_verified, needs_review, unknown_tokens,
	state, voided_at, void_reason, remark, created_by, created_at, updated_by, updated_at`

// GetFormByNo 按表单号取表单；不存在返回 (nil, nil)。
func (s *Store) GetFormByNo(ctx context.Context, formNo string) (*Form, error) {
	var f Form
	err := s.db.GetContext(ctx, &f, s.rebind(`SELECT `+formColumns+` FROM forms WHERE form_no = ?`), formNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询表单失败: %w", err)
	}
	return &f, nil
}

// GetFormByID 按主键取表单。
func (s *Store) GetFormByID(ctx context.Context, formID string) (*Form, error) {
	var f Form
	err := s.db.GetContext(ctx, &f, s.rebind(`SELECT `+formColumns+` FROM forms WHERE id = ?`), formID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询表单失败: %w", err)
	}
	return &f, nil
}

// InsertForm 插入表单（调用方负责设置 ID / 时间字段）。
func (s *Store) InsertForm(ctx context.Context, tx *sqlx.Tx, f *Form) error {
	if f.ID == "" {
		f.ID = id.New()
	}
	now := store.FormatTime(time.Now())
	if f.CreatedAt == "" {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if f.State == "" {
		f.State = "ACTIVE"
	}
	if f.SampleParsedBy == "" {
		f.SampleParsedBy = "auto"
	}
	_, err := tx.NamedExecContext(ctx, `INSERT INTO forms (`+formColumns+`) VALUES (
		:id, :form_no, :unit_id, :unit_text, :sample_raw,
		:sample_project, :sample_model, :sample_spec, :sample_size, :sample_side,
		:sample_class, :sample_is_rx, :sample_drawing_id, :sample_unmatched_reason,
		:sample_parsed_by, :sample_confidence, :sample_verified, :needs_review, :unknown_tokens,
		:state, :voided_at, :void_reason, :remark, :created_by, :created_at, :updated_by, :updated_at)`, f)
	if err != nil {
		return fmt.Errorf("写入表单失败: %w", err)
	}
	return nil
}

// UpdateForm 全字段更新表单。
func (s *Store) UpdateForm(ctx context.Context, tx *sqlx.Tx, f *Form) error {
	f.UpdatedAt = store.FormatTime(time.Now())
	_, err := tx.NamedExecContext(ctx, `UPDATE forms SET
		form_no = :form_no, unit_id = :unit_id, unit_text = :unit_text, sample_raw = :sample_raw,
		sample_project = :sample_project, sample_model = :sample_model, sample_spec = :sample_spec,
		sample_size = :sample_size, sample_side = :sample_side, sample_class = :sample_class,
		sample_is_rx = :sample_is_rx, sample_drawing_id = :sample_drawing_id,
		sample_unmatched_reason = :sample_unmatched_reason, sample_parsed_by = :sample_parsed_by,
		sample_confidence = :sample_confidence, sample_verified = :sample_verified,
		needs_review = :needs_review, unknown_tokens = :unknown_tokens,
		state = :state, voided_at = :voided_at, void_reason = :void_reason, remark = :remark,
		updated_by = :updated_by, updated_at = :updated_at
		WHERE id = :id`, f)
	if err != nil {
		return fmt.Errorf("更新表单失败: %w", err)
	}
	return nil
}

// LockFormByNo 在事务内按表单号加锁读取（PostgreSQL 有效；SQLite 为库级写锁）。
func (s *Store) LockFormByNo(ctx context.Context, tx *sqlx.Tx, formNo string) (*Form, error) {
	q := `SELECT ` + formColumns + ` FROM forms WHERE form_no = ?`
	if s.driver == "postgres" {
		q += ` FOR UPDATE`
	}
	var f Form
	err := tx.GetContext(ctx, &f, s.rebind(q), formNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("加锁读取表单失败: %w", err)
	}
	return &f, nil
}

// ------------------------------- 笔 -------------------------------

const recordColumns = `id, form_id, sequence_no, process_code, process_text,
	submitted_by, submitted_at, inspected_by, inspected_at, machine_operator, handover_by,
	recorded_by, recorded_at, save_count, last_saved_by, last_saved_at,
	is_complete, missing_fields, remark, total_qty, qualified_qty, defect_qty, pass_rate,
	state, voided_at, void_reason, created_at, updated_at`

// ListRecords 取某表单下的全部笔（按序号升序）。
func (s *Store) ListRecords(ctx context.Context, formID string) ([]FormRecord, error) {
	var out []FormRecord
	err := s.db.SelectContext(ctx, &out,
		s.rebind(`SELECT `+recordColumns+` FROM form_records WHERE form_id = ? ORDER BY sequence_no`), formID)
	if err != nil {
		return nil, fmt.Errorf("查询工序记录失败: %w", err)
	}
	return out, nil
}

// GetRecord 按主键取一笔。
func (s *Store) GetRecord(ctx context.Context, recordID string) (*FormRecord, error) {
	var r FormRecord
	err := s.db.GetContext(ctx, &r, s.rebind(`SELECT `+recordColumns+` FROM form_records WHERE id = ?`), recordID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询工序记录失败: %w", err)
	}
	return &r, nil
}

// LockRecord 在事务内按主键加锁读取。
func (s *Store) LockRecord(ctx context.Context, tx *sqlx.Tx, recordID string) (*FormRecord, error) {
	q := `SELECT ` + recordColumns + ` FROM form_records WHERE id = ?`
	if s.driver == "postgres" {
		q += ` FOR UPDATE`
	}
	var r FormRecord
	err := tx.GetContext(ctx, &r, s.rebind(q), recordID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("加锁读取工序记录失败: %w", err)
	}
	return &r, nil
}

// NextSequenceNo 返回该表单下一个可用的笔序号。
func (s *Store) NextSequenceNo(ctx context.Context, tx *sqlx.Tx, formID string) (int, error) {
	var max sql.NullInt64
	err := tx.GetContext(ctx, &max,
		s.rebind(`SELECT MAX(sequence_no) FROM form_records WHERE form_id = ?`), formID)
	if err != nil {
		return 0, fmt.Errorf("计算笔序号失败: %w", err)
	}
	if !max.Valid {
		return 1, nil
	}
	return int(max.Int64) + 1, nil
}

// InsertRecord 插入一笔。
func (s *Store) InsertRecord(ctx context.Context, tx *sqlx.Tx, r *FormRecord) error {
	if r.ID == "" {
		r.ID = id.New()
	}
	now := store.FormatTime(time.Now())
	if r.CreatedAt == "" {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if r.State == "" {
		r.State = "ACTIVE"
	}
	_, err := tx.NamedExecContext(ctx, `INSERT INTO form_records (`+recordColumns+`) VALUES (
		:id, :form_id, :sequence_no, :process_code, :process_text,
		:submitted_by, :submitted_at, :inspected_by, :inspected_at, :machine_operator, :handover_by,
		:recorded_by, :recorded_at, :save_count, :last_saved_by, :last_saved_at,
		:is_complete, :missing_fields, :remark, :total_qty, :qualified_qty, :defect_qty, :pass_rate,
		:state, :voided_at, :void_reason, :created_at, :updated_at)`, r)
	if err != nil {
		return fmt.Errorf("写入工序记录失败: %w", err)
	}
	return nil
}

// UpdateRecord 全字段更新一笔。
func (s *Store) UpdateRecord(ctx context.Context, tx *sqlx.Tx, r *FormRecord) error {
	r.UpdatedAt = store.FormatTime(time.Now())
	_, err := tx.NamedExecContext(ctx, `UPDATE form_records SET
		sequence_no = :sequence_no, process_code = :process_code, process_text = :process_text,
		submitted_by = :submitted_by, submitted_at = :submitted_at,
		inspected_by = :inspected_by, inspected_at = :inspected_at,
		machine_operator = :machine_operator, handover_by = :handover_by,
		recorded_by = :recorded_by, recorded_at = :recorded_at, save_count = :save_count,
		last_saved_by = :last_saved_by, last_saved_at = :last_saved_at,
		is_complete = :is_complete, missing_fields = :missing_fields, remark = :remark,
		total_qty = :total_qty, qualified_qty = :qualified_qty, defect_qty = :defect_qty,
		pass_rate = :pass_rate, state = :state, voided_at = :voided_at,
		void_reason = :void_reason, updated_at = :updated_at
		WHERE id = :id`, r)
	if err != nil {
		return fmt.Errorf("更新工序记录失败: %w", err)
	}
	return nil
}

// ------------------------------- 明细行 -------------------------------

const lineColumns = `id, record_id, seq, total_qty, qualified_qty, defect_qty,
	spot_concave, spot_convex, spot_both,
	scratch_concave, scratch_convex, scratch_both,
	dirt_concave, dirt_convex, dirt_both,
	hard_spot_concave, hard_spot_convex, hard_spot_both,
	debris_concave, debris_convex, debris_both,
	process_code, process_text, remark, created_at, updated_at`

// ListLines 取某笔的全部明细行。
func (s *Store) ListLines(ctx context.Context, recordID string) ([]RecordLine, error) {
	var out []RecordLine
	err := s.db.SelectContext(ctx, &out,
		s.rebind(`SELECT `+lineColumns+` FROM record_lines WHERE record_id = ? ORDER BY seq`), recordID)
	if err != nil {
		return nil, fmt.Errorf("查询明细行失败: %w", err)
	}
	return out, nil
}

// ListLinesTx 在事务内取明细行。
func (s *Store) ListLinesTx(ctx context.Context, tx *sqlx.Tx, recordID string) ([]RecordLine, error) {
	var out []RecordLine
	err := tx.SelectContext(ctx, &out,
		s.rebind(`SELECT `+lineColumns+` FROM record_lines WHERE record_id = ? ORDER BY seq`), recordID)
	if err != nil {
		return nil, fmt.Errorf("查询明细行失败: %w", err)
	}
	return out, nil
}

// ReplaceLines 用给定明细整体替换该笔的明细（提交即整笔替换，不做行级合并）。
func (s *Store) ReplaceLines(ctx context.Context, tx *sqlx.Tx, recordID string, lines []RecordLine) error {
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM record_lines WHERE record_id = ?`), recordID); err != nil {
		return fmt.Errorf("清空明细行失败: %w", err)
	}
	now := store.FormatTime(time.Now())
	for i := range lines {
		l := &lines[i]
		l.ID = id.New()
		l.RecordID = recordID
		if l.Seq <= 0 {
			l.Seq = i + 1
		}
		l.CreatedAt = now
		l.UpdatedAt = now
		if _, err := tx.NamedExecContext(ctx, `INSERT INTO record_lines (`+lineColumns+`) VALUES (
			:id, :record_id, :seq, :total_qty, :qualified_qty, :defect_qty,
			:spot_concave, :spot_convex, :spot_both,
			:scratch_concave, :scratch_convex, :scratch_both,
			:dirt_concave, :dirt_convex, :dirt_both,
			:hard_spot_concave, :hard_spot_convex, :hard_spot_both,
			:debris_concave, :debris_convex, :debris_both,
			:process_code, :process_text, :remark, :created_at, :updated_at)`, l); err != nil {
			return fmt.Errorf("写入明细行失败: %w", err)
		}
	}
	return nil
}

// ------------------------------- 历史与日志 -------------------------------

// InsertHistory 写入一次保存快照（追加写，不可改不可删）。
func (s *Store) InsertHistory(ctx context.Context, tx *sqlx.Tx, h *HistoryEntry) error {
	if h.ID == "" {
		h.ID = id.New()
	}
	if h.ChangedAt == "" {
		h.ChangedAt = store.FormatTime(time.Now())
	}
	_, err := tx.NamedExecContext(ctx, `INSERT INTO record_history (
		id, record_id, form_id, save_count, form_snapshot, record_snapshot, lines_snapshot,
		saved_by, saved_at, changed_by, changed_at, reason) VALUES (
		:id, :record_id, :form_id, :save_count, :form_snapshot, :record_snapshot, :lines_snapshot,
		:saved_by, :saved_at, :changed_by, :changed_at, :reason)`, h)
	if err != nil {
		return fmt.Errorf("写入历史快照失败: %w", err)
	}
	return nil
}

// HistoryEntry 对应表 record_history。
type HistoryEntry struct {
	ID             string  `db:"id" json:"id"`
	RecordID       string  `db:"record_id" json:"record_id"`
	FormID         string  `db:"form_id" json:"form_id"`
	SaveCount      int     `db:"save_count" json:"save_count"`
	FormSnapshot   *string `db:"form_snapshot" json:"form_snapshot,omitempty"`
	RecordSnapshot *string `db:"record_snapshot" json:"record_snapshot,omitempty"`
	LinesSnapshot  *string `db:"lines_snapshot" json:"lines_snapshot,omitempty"`
	SavedBy        *string `db:"saved_by" json:"saved_by,omitempty"`
	SavedAt        *string `db:"saved_at" json:"saved_at,omitempty"`
	ChangedBy      *string `db:"changed_by" json:"changed_by,omitempty"`
	ChangedAt      string  `db:"changed_at" json:"changed_at"`
	Reason         *string `db:"reason" json:"reason,omitempty"`
}

// ListHistory 取某笔的历史快照（按保存次数倒序）。
func (s *Store) ListHistory(ctx context.Context, recordID string) ([]HistoryEntry, error) {
	var out []HistoryEntry
	err := s.db.SelectContext(ctx, &out, s.rebind(`SELECT
		id, record_id, form_id, save_count, form_snapshot, record_snapshot, lines_snapshot,
		saved_by, saved_at, changed_by, changed_at, reason
		FROM record_history WHERE record_id = ? ORDER BY save_count DESC`), recordID)
	if err != nil {
		return nil, fmt.Errorf("查询历史快照失败: %w", err)
	}
	return out, nil
}

// GetHistoryEntry 取单条历史快照。
func (s *Store) GetHistoryEntry(ctx context.Context, historyID string) (*HistoryEntry, error) {
	var h HistoryEntry
	err := s.db.GetContext(ctx, &h, s.rebind(`SELECT
		id, record_id, form_id, save_count, form_snapshot, record_snapshot, lines_snapshot,
		saved_by, saved_at, changed_by, changed_at, reason
		FROM record_history WHERE id = ?`), historyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询历史快照失败: %w", err)
	}
	return &h, nil
}

// AuditEntry 对应表 audit_logs。
type AuditEntry struct {
	ID        int64   `db:"id" json:"id"`
	ActorID   *string `db:"actor_id" json:"actor_id,omitempty"`
	ActorName *string `db:"actor_name" json:"actor_name,omitempty"`
	Action    string  `db:"action" json:"action"`
	Entity    string  `db:"entity" json:"entity"`
	EntityID  *string `db:"entity_id" json:"entity_id,omitempty"`
	Before    *string `db:"before" json:"before,omitempty"`
	After     *string `db:"after" json:"after,omitempty"`
	IP        *string `db:"ip" json:"ip,omitempty"`
	UserAgent *string `db:"user_agent" json:"user_agent,omitempty"`
	Detail    *string `db:"detail" json:"detail,omitempty"`
	CreatedAt string  `db:"created_at" json:"created_at"`
}

// InsertAudit 写入操作日志（与业务写在同一事务内）。
func (s *Store) InsertAudit(ctx context.Context, tx *sqlx.Tx, e *AuditEntry) error {
	if e.CreatedAt == "" {
		e.CreatedAt = store.FormatTime(time.Now())
	}
	_, err := tx.NamedExecContext(ctx, `INSERT INTO audit_logs (
		actor_id, actor_name, action, entity, entity_id, before, after, ip, user_agent, detail, created_at)
		VALUES (:actor_id, :actor_name, :action, :entity, :entity_id, :before, :after, :ip, :user_agent, :detail, :created_at)`, e)
	if err != nil {
		return fmt.Errorf("写入操作日志失败: %w", err)
	}
	return nil
}

// ListAudit 查询操作日志（按时间倒序）。
func (s *Store) ListAudit(ctx context.Context, entity, entityID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := strings.Builder{}
	q.WriteString(`SELECT id, actor_id, actor_name, action, entity, entity_id, before, after, ip, user_agent, detail, created_at FROM audit_logs WHERE 1=1`)
	args := []any{}
	if entity != "" {
		q.WriteString(` AND entity = ?`)
		args = append(args, entity)
	}
	if entityID != "" {
		q.WriteString(` AND entity_id = ?`)
		args = append(args, entityID)
	}
	q.WriteString(` ORDER BY id DESC LIMIT ?`)
	args = append(args, limit)

	var out []AuditEntry
	if err := s.db.SelectContext(ctx, &out, s.rebind(q.String()), args...); err != nil {
		return nil, fmt.Errorf("查询操作日志失败: %w", err)
	}
	return out, nil
}
