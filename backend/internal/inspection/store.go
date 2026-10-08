package inspection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Store 负责检验表单提交 / 明细行 / 历史 / 操作日志的持久化。
type Store struct {
	db     *sqlx.DB
	driver string
}

// NewStore 构造 Store。driver 取 "sqlite" 或 "postgres"。
func NewStore(db *sqlx.DB, driver string) *Store { return &Store{db: db, driver: driver} }

// DB 暴露原始句柄（供 handler 做只读查询）。
func (s *Store) DB() *sqlx.DB { return s.db }

// rebind 把 ? 占位符转换为当前驱动需要的写法。
func (s *Store) rebind(q string) string { return s.db.Rebind(q) }

// ---------------------------------------------------------------- 提交（forms）

const submissionColumns = `id, form_no, operation_code, operation_text,
	sample_raw, sample_project, sample_model, sample_spec, sample_size, sample_side,
	paper_unit_id, paper_unit_text, paper_submitter, paper_submitted_at,
	paper_inspector, paper_inspected_at, paper_machine_operator, paper_handover_by,
	created_by, submitted_by, submitted_at, last_saved_by, last_saved_at, save_count,
	total_qty, qualified_qty, defect_qty, pass_rate, is_complete, missing_fields,
	state, voided_at, void_reason, remark, created_at, updated_at`

// submissionRow 是数据库行（指针字段允许 NULL）。
type submissionRow struct {
	ID            string  `db:"id"`
	FormNo        string  `db:"form_no"`
	OperationCode string  `db:"operation_code"`
	OperationText *string `db:"operation_text"`
	SampleRaw     string  `db:"sample_raw"`
	SampleProject *string `db:"sample_project"`
	SampleModel   *string `db:"sample_model"`
	SampleSpec    *string `db:"sample_spec"`
	SampleSize    *string `db:"sample_size"`
	SampleSide    *string `db:"sample_side"`

	// 纸质表单原有的表头栏目
	PaperUnitID          *string `db:"paper_unit_id"`
	PaperUnitText        *string `db:"paper_unit_text"`
	PaperSubmitter       *string `db:"paper_submitter"`
	PaperSubmittedAt     *string `db:"paper_submitted_at"`
	PaperInspector       *string `db:"paper_inspector"`
	PaperInspectedAt     *string `db:"paper_inspected_at"`
	PaperMachineOperator *string `db:"paper_machine_operator"`
	PaperHandoverBy      *string `db:"paper_handover_by"`

	CreatedBy     *string  `db:"created_by"`
	SubmittedBy   *string  `db:"submitted_by"`
	SubmittedAt   *string  `db:"submitted_at"`
	LastSavedBy   *string  `db:"last_saved_by"`
	LastSavedAt   *string  `db:"last_saved_at"`
	SaveCount     int      `db:"save_count"`
	TotalQty      int      `db:"total_qty"`
	QualifiedQty  int      `db:"qualified_qty"`
	DefectQty     int      `db:"defect_qty"`
	PassRate      *float64 `db:"pass_rate"`
	IsComplete    bool     `db:"is_complete"`
	MissingFields *string  `db:"missing_fields"`
	State         string   `db:"state"`
	VoidedAt      *string  `db:"voided_at"`
	VoidReason    *string  `db:"void_reason"`
	Remark        *string  `db:"remark"`
	CreatedAt     string   `db:"created_at"`
	UpdatedAt     string   `db:"updated_at"`
}

func (r submissionRow) toModel() Submission {
	s := Submission{
		ID: r.ID, FormNo: r.FormNo, OperationCode: r.OperationCode, OperationText: r.OperationText,
		SampleRaw: r.SampleRaw, SampleProject: r.SampleProject, SampleModel: r.SampleModel,
		SampleSpec: r.SampleSpec, SampleSize: r.SampleSize, SampleSide: r.SampleSide,

		PaperUnitID: r.PaperUnitID, PaperUnitText: r.PaperUnitText,
		PaperSubmitter: r.PaperSubmitter, PaperSubmittedAt: r.PaperSubmittedAt,
		PaperInspector: r.PaperInspector, PaperInspectedAt: r.PaperInspectedAt,
		PaperMachineOperator: r.PaperMachineOperator, PaperHandoverBy: r.PaperHandoverBy,

		CreatedBy: r.CreatedBy, SubmittedBy: r.SubmittedBy, SubmittedAt: r.SubmittedAt,
		LastSavedBy: r.LastSavedBy, LastSavedAt: r.LastSavedAt, SaveCount: r.SaveCount,
		TotalQty: r.TotalQty, QualifiedQty: r.QualifiedQty, DefectQty: r.DefectQty,
		PassRate: r.PassRate, IsComplete: r.IsComplete,
		MissingFields: UnmarshalStrings(r.MissingFields),
		State:         r.State, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason, Remark: r.Remark,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	// 单位别名回显（只读）
	s.UnitID, s.UnitText = s.PaperUnitID, s.PaperUnitText
	return s
}

func modelToRow(sub Submission) submissionRow {
	return submissionRow{
		ID: sub.ID, FormNo: sub.FormNo, OperationCode: sub.OperationCode, OperationText: sub.OperationText,
		SampleRaw: sub.SampleRaw, SampleProject: sub.SampleProject, SampleModel: sub.SampleModel,
		SampleSpec: sub.SampleSpec, SampleSize: sub.SampleSize, SampleSide: sub.SampleSide,

		PaperUnitID: sub.PaperUnitID, PaperUnitText: sub.PaperUnitText,
		PaperSubmitter: sub.PaperSubmitter, PaperSubmittedAt: sub.PaperSubmittedAt,
		PaperInspector: sub.PaperInspector, PaperInspectedAt: sub.PaperInspectedAt,
		PaperMachineOperator: sub.PaperMachineOperator, PaperHandoverBy: sub.PaperHandoverBy,

		CreatedBy: sub.CreatedBy, SubmittedBy: sub.SubmittedBy, SubmittedAt: sub.SubmittedAt,
		LastSavedBy: sub.LastSavedBy, LastSavedAt: sub.LastSavedAt, SaveCount: sub.SaveCount,
		TotalQty: sub.TotalQty, QualifiedQty: sub.QualifiedQty, DefectQty: sub.DefectQty,
		PassRate: sub.PassRate, IsComplete: sub.IsComplete,
		MissingFields: MarshalStrings(sub.MissingFields),
		State:         sub.State, VoidedAt: sub.VoidedAt, VoidReason: sub.VoidReason, Remark: sub.Remark,
		CreatedAt: sub.CreatedAt, UpdatedAt: sub.UpdatedAt,
	}
}

// ---------------------------------------------------------------- 明细行列清单

// lineColumns 由 formfields 生成：合计 3 列 + 全部缺陷列 × 3 表面 + 备注与时间。
// 这样"新增一个缺陷列"只需改 formfields + 加一次迁移，SQL 无需改动。
var lineColumns = buildLineColumns()

func buildLineColumns() []string {
	cols := []string{"id", "form_id", "seq", "total_qty", "qualified_qty", "defect_qty"}
	cols = append(cols, formfields.DefectFieldNames()...)
	cols = append(cols, "remark", "created_at", "updated_at")
	return cols
}

func lineSelect() string { return strings.Join(lineColumns, ", ") }

// ---------------------------------------------------------------- 提交读写

// GetSubmission 按主键取一次提交（含明细）。
func (s *Store) GetSubmission(ctx context.Context, submissionID string) (*Submission, error) {
	var row submissionRow
	err := s.db.GetContext(ctx, &row, s.rebind(`SELECT `+submissionColumns+` FROM forms WHERE id = ?`), submissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询表单提交失败: %w", err)
	}
	out := row.toModel()
	lines, err := s.ListLines(ctx, out.ID)
	if err != nil {
		return nil, err
	}
	out.Lines = lines
	return &out, nil
}

// GetSubmissionByNo 按表单号取提交。
func (s *Store) GetSubmissionByNo(ctx context.Context, formNo string) (*Submission, error) {
	var row submissionRow
	err := s.db.GetContext(ctx, &row, s.rebind(`SELECT `+submissionColumns+` FROM forms WHERE form_no = ?`), formNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询表单提交失败: %w", err)
	}
	out := row.toModel()
	lines, err := s.ListLines(ctx, out.ID)
	if err != nil {
		return nil, err
	}
	out.Lines = lines
	return &out, nil
}

// LockSubmissionByNo 在事务内按表单号加锁读取。
func (s *Store) LockSubmissionByNo(ctx context.Context, tx *sqlx.Tx, formNo string) (*Submission, error) {
	q := `SELECT ` + submissionColumns + ` FROM forms WHERE form_no = ?`
	if s.driver == "postgres" {
		q += ` FOR UPDATE`
	}
	var row submissionRow
	err := tx.GetContext(ctx, &row, s.rebind(q), formNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("加锁读取表单提交失败: %w", err)
	}
	out := row.toModel()
	return &out, nil
}

// LockSubmission 在事务内按主键加锁读取。
func (s *Store) LockSubmission(ctx context.Context, tx *sqlx.Tx, submissionID string) (*Submission, error) {
	q := `SELECT ` + submissionColumns + ` FROM forms WHERE id = ?`
	if s.driver == "postgres" {
		q += ` FOR UPDATE`
	}
	var row submissionRow
	err := tx.GetContext(ctx, &row, s.rebind(q), submissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("加锁读取表单提交失败: %w", err)
	}
	out := row.toModel()
	return &out, nil
}

// InsertSubmission 插入一次提交（ID / 时间由这里补全）。
func (s *Store) InsertSubmission(ctx context.Context, tx *sqlx.Tx, sub *Submission) error {
	if sub.ID == "" {
		sub.ID = id.New()
	}
	now := store.FormatTime(time.Now())
	if sub.CreatedAt == "" {
		sub.CreatedAt = now
	}
	sub.UpdatedAt = now
	if sub.State == "" {
		sub.State = StateActive
	}
	if sub.SaveCount <= 0 {
		sub.SaveCount = 1
	}
	row := modelToRow(*sub)
	_, err := tx.NamedExecContext(ctx, `INSERT INTO forms (`+submissionColumns+`) VALUES (
		:id, :form_no, :operation_code, :operation_text,
		:sample_raw, :sample_project, :sample_model, :sample_spec, :sample_size, :sample_side,
		:paper_unit_id, :paper_unit_text, :paper_submitter, :paper_submitted_at,
		:paper_inspector, :paper_inspected_at, :paper_machine_operator, :paper_handover_by,
		:created_by, :submitted_by, :submitted_at, :last_saved_by, :last_saved_at, :save_count,
		:total_qty, :qualified_qty, :defect_qty, :pass_rate, :is_complete, :missing_fields,
		:state, :voided_at, :void_reason, :remark, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("写入表单提交失败: %w", err)
	}
	return nil
}

// UpdateSubmission 更新一次提交（全字段）。
func (s *Store) UpdateSubmission(ctx context.Context, tx *sqlx.Tx, sub *Submission) error {
	sub.UpdatedAt = store.FormatTime(time.Now())
	row := modelToRow(*sub)
	_, err := tx.NamedExecContext(ctx, `UPDATE forms SET
		form_no = :form_no, operation_code = :operation_code, operation_text = :operation_text,
		sample_raw = :sample_raw, sample_project = :sample_project, sample_model = :sample_model,
		sample_spec = :sample_spec, sample_size = :sample_size, sample_side = :sample_side,
		paper_unit_id = :paper_unit_id, paper_unit_text = :paper_unit_text,
		paper_submitter = :paper_submitter, paper_submitted_at = :paper_submitted_at,
		paper_inspector = :paper_inspector, paper_inspected_at = :paper_inspected_at,
		paper_machine_operator = :paper_machine_operator, paper_handover_by = :paper_handover_by,
		created_by = :created_by,
		submitted_by = :submitted_by, submitted_at = :submitted_at,
		last_saved_by = :last_saved_by, last_saved_at = :last_saved_at, save_count = :save_count,
		total_qty = :total_qty, qualified_qty = :qualified_qty, defect_qty = :defect_qty,
		pass_rate = :pass_rate, is_complete = :is_complete, missing_fields = :missing_fields,
		state = :state, voided_at = :voided_at, void_reason = :void_reason,
		remark = :remark, updated_at = :updated_at
		WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("更新表单提交失败: %w", err)
	}
	return nil
}

// FormFilter 是表单提交列表的查询条件（全部可选）。
type FormFilter struct {
	Keyword       string // 表单号 / 样品原始串 模糊
	State         string // ACTIVE | VOID | ""（全部）
	OperationCode string
	SampleProject string
	SampleModel   string
	SampleSpec    string
	SampleSize    string
	SampleSide    string
	Inspector     string // 纸面"检验人"
	Submitter     string // 纸面"送检人"
	Unit          string // 单位
	From          string // 提交时间起
	To            string // 提交时间止
	Incomplete    bool   // 只看未填完整
	// SampleUnresolved 只看"样品信息未拆解出项目"的表单（原「待核对清单」的口径）。
	SampleUnresolved bool
	Offset           int
	Limit            int
}

// ListSubmissions 分页查询表单提交。
func (s *Store) ListSubmissions(ctx context.Context, f FormFilter) ([]Submission, int, error) {
	where, args := s.filterWhere(f)

	var total int
	if err := s.db.GetContext(ctx, &total,
		s.rebind(`SELECT COUNT(*) FROM forms WHERE `+where), args...); err != nil {
		return nil, 0, fmt.Errorf("统计表单数失败: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := `SELECT ` + submissionColumns + ` FROM forms WHERE ` + where +
		` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	var rows []submissionRow
	if err := s.db.SelectContext(ctx, &rows, s.rebind(q), args...); err != nil {
		return nil, 0, fmt.Errorf("查询表单列表失败: %w", err)
	}
	out := make([]Submission, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toModel())
	}
	return out, total, nil
}

func (s *Store) filterWhere(f FormFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}

	if st := strings.TrimSpace(f.State); st != "" {
		clauses = append(clauses, "state = ?")
		args = append(args, strings.ToUpper(st))
	}
	if f.Incomplete {
		clauses = append(clauses, "is_complete = 0")
	}
	if f.SampleUnresolved {
		clauses = append(clauses, "COALESCE(sample_project, '') = ''")
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		clauses = append(clauses, "(form_no LIKE ? OR sample_raw LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like)
	}
	if v := strings.TrimSpace(f.From); v != "" {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.To); v != "" {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, v+"T23:59:59Z")
	}
	add := func(col, val string) {
		if v := strings.TrimSpace(val); v != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, v)
		}
	}
	add("operation_code", f.OperationCode)
	add("sample_project", f.SampleProject)
	add("sample_model", f.SampleModel)
	add("sample_spec", f.SampleSpec)
	add("sample_size", f.SampleSize)
	add("sample_side", f.SampleSide)
	add("paper_inspector", f.Inspector)
	add("paper_submitter", f.Submitter)
	if v := strings.TrimSpace(f.Unit); v != "" {
		clauses = append(clauses, "COALESCE(paper_unit_text, paper_unit_id) = ?")
		args = append(args, v)
	}
	return strings.Join(clauses, " AND "), args
}

// ---------------------------------------------------------------- 明细行

// ListLines 取某次提交的全部明细行。
func (s *Store) ListLines(ctx context.Context, formID string) ([]Line, error) {
	return s.listLines(ctx, s.db, formID)
}

// ListLinesTx 在事务内取明细行。
func (s *Store) ListLinesTx(ctx context.Context, tx *sqlx.Tx, formID string) ([]Line, error) {
	return s.listLines(ctx, tx, formID)
}

type queryer interface {
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
}

func (s *Store) listLines(ctx context.Context, q queryer, formID string) ([]Line, error) {
	rows, err := q.QueryxContext(ctx,
		s.rebind(`SELECT `+lineSelect()+` FROM form_lines WHERE form_id = ? ORDER BY seq`), formID)
	if err != nil {
		return nil, fmt.Errorf("查询明细行失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Line, 0, 4)
	for rows.Next() {
		raw := map[string]any{}
		if err := rows.MapScan(raw); err != nil {
			return nil, fmt.Errorf("读取明细行失败: %w", err)
		}
		out = append(out, mapToLine(raw))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历明细行失败: %w", err)
	}
	return out, nil
}

// ReplaceLines 用给定明细整体替换该提交的明细（出现即整笔替换）。
func (s *Store) ReplaceLines(ctx context.Context, tx *sqlx.Tx, formID string, lines []Line) error {
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM form_lines WHERE form_id = ?`), formID); err != nil {
		return fmt.Errorf("清空明细行失败: %w", err)
	}
	now := store.FormatTime(time.Now())
	for i := range lines {
		l := &lines[i]
		l.ID = id.New()
		l.FormID = formID
		if l.Seq <= 0 {
			l.Seq = i + 1
		}
		l.CreatedAt = now
		l.UpdatedAt = now
		if err := s.insertLine(ctx, tx, *l); err != nil {
			return err
		}
	}
	return nil
}

// insertLine 写入一条明细行（列清单与占位符由 formfields 生成）。
func (s *Store) insertLine(ctx context.Context, tx *sqlx.Tx, l Line) error {
	names := make([]string, 0, len(lineColumns))
	holders := make([]string, 0, len(lineColumns))
	args := make([]any, 0, len(lineColumns))

	for _, c := range lineColumns {
		names = append(names, c)
		holders = append(holders, "?")
		args = append(args, lineValue(l, c))
	}
	q := s.rebind(`INSERT INTO form_lines (` + strings.Join(names, ", ") + `) VALUES (` +
		strings.Join(holders, ", ") + `)`)
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("写入明细行失败: %w", err)
	}
	return nil
}

// lineValue 按列名取出对应的值。
func lineValue(l Line, column string) any {
	switch column {
	case "id":
		return l.ID
	case "form_id":
		return l.FormID
	case "seq":
		return l.Seq
	case "total_qty":
		return l.TotalQty
	case "qualified_qty":
		return l.QualifiedQty
	case "defect_qty":
		return l.DefectQty
	case "remark":
		return l.Remark
	case "created_at":
		return l.CreatedAt
	case "updated_at":
		return l.UpdatedAt
	}
	for _, d := range formfields.DefectGroups {
		for _, sf := range formfields.Surfaces {
			if column == formfields.DefectFieldName(d.Code, sf) {
				return l.Defects[d.Code][sf]
			}
		}
	}
	return 0
}

// mapToLine 把 MapScan 的结果还原为 Line。
func mapToLine(raw map[string]any) Line {
	l := Line{Defects: Defects{}}
	for k, v := range raw {
		switch k {
		case "id":
			l.ID = asString(v)
		case "form_id":
			l.FormID = asString(v)
		case "seq":
			l.Seq = asInt(v)
		case "total_qty":
			l.TotalQty = asInt(v)
		case "qualified_qty":
			l.QualifiedQty = asInt(v)
		case "defect_qty":
			l.DefectQty = asInt(v)
		case "remark":
			if s := asString(v); s != "" {
				l.Remark = &s
			}
		case "created_at":
			l.CreatedAt = asString(v)
		case "updated_at":
			l.UpdatedAt = asString(v)
		default:
			for _, d := range formfields.DefectGroups {
				for _, sf := range formfields.Surfaces {
					if k == formfields.DefectFieldName(d.Code, sf) {
						if n := asInt(v); n != 0 {
							if l.Defects[d.Code] == nil {
								l.Defects[d.Code] = map[string]int{}
							}
							l.Defects[d.Code][sf] = n
						}
					}
				}
			}
		}
	}
	if len(l.Defects) == 0 {
		l.Defects = nil
	}
	return l
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case []byte:
		return parseIntBytes(t)
	case string:
		return parseIntBytes([]byte(t))
	default:
		return 0
	}
}

func parseIntBytes(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	neg := false
	i := 0
	if b[0] == '-' {
		neg = true
		i = 1
	}
	n := 0
	for ; i < len(b); i++ {
		if b[i] < '0' || b[i] > '9' {
			return 0
		}
		n = n*10 + int(b[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

// ---------------------------------------------------------------- 历史与日志

// HistoryEntry 对应表 form_history。
type HistoryEntry struct {
	ID                 string  `db:"id" json:"id"`
	FormID             string  `db:"form_id" json:"form_id"`
	SaveCount          int     `db:"save_count" json:"save_count"`
	SubmissionSnapshot *string `db:"submission_snapshot" json:"submission_snapshot,omitempty"`
	LinesSnapshot      *string `db:"lines_snapshot" json:"lines_snapshot,omitempty"`
	SavedBy            *string `db:"saved_by" json:"saved_by,omitempty"`
	SavedAt            *string `db:"saved_at" json:"saved_at,omitempty"`
	ChangedBy          *string `db:"changed_by" json:"changed_by,omitempty"`
	ChangedAt          string  `db:"changed_at" json:"changed_at"`
	Reason             *string `db:"reason" json:"reason,omitempty"`
}

const historyColumns = `id, form_id, save_count, submission_snapshot, lines_snapshot,
	saved_by, saved_at, changed_by, changed_at, reason`

// InsertHistory 写入一次保存快照（追加写，不可改不可删）。
func (s *Store) InsertHistory(ctx context.Context, tx *sqlx.Tx, h *HistoryEntry) error {
	if h.ID == "" {
		h.ID = id.New()
	}
	if h.ChangedAt == "" {
		h.ChangedAt = store.FormatTime(time.Now())
	}
	_, err := tx.NamedExecContext(ctx, `INSERT INTO form_history (`+historyColumns+`) VALUES (
		:id, :form_id, :save_count, :submission_snapshot, :lines_snapshot,
		:saved_by, :saved_at, :changed_by, :changed_at, :reason)`, h)
	if err != nil {
		return fmt.Errorf("写入历史快照失败: %w", err)
	}
	return nil
}

// ListHistory 取某次提交的历史快照（按保存次数倒序）。
func (s *Store) ListHistory(ctx context.Context, formID string) ([]HistoryEntry, error) {
	var out []HistoryEntry
	err := s.db.SelectContext(ctx, &out, s.rebind(`SELECT `+historyColumns+`
		FROM form_history WHERE form_id = ? ORDER BY save_count DESC`), formID)
	if err != nil {
		return nil, fmt.Errorf("查询历史快照失败: %w", err)
	}
	return out, nil
}

// GetHistoryEntry 取单条历史快照。
func (s *Store) GetHistoryEntry(ctx context.Context, historyID string) (*HistoryEntry, error) {
	var h HistoryEntry
	err := s.db.GetContext(ctx, &h, s.rebind(`SELECT `+historyColumns+`
		FROM form_history WHERE id = ?`), historyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询历史快照失败: %w", err)
	}
	return &h, nil
}

// DecodeSnapshot 解析历史快照里的提交与明细。
func DecodeSnapshot(h *HistoryEntry) (Submission, []Line, error) {
	var sub Submission
	if h.SubmissionSnapshot != nil && *h.SubmissionSnapshot != "" {
		if err := json.Unmarshal([]byte(*h.SubmissionSnapshot), &sub); err != nil {
			return sub, nil, fmt.Errorf("历史快照解析失败: %w", err)
		}
	}
	lines := []Line{}
	if h.LinesSnapshot != nil && *h.LinesSnapshot != "" {
		if err := json.Unmarshal([]byte(*h.LinesSnapshot), &lines); err != nil {
			return sub, nil, fmt.Errorf("历史明细解析失败: %w", err)
		}
	}
	return sub, lines, nil
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
	q := `SELECT id, actor_id, actor_name, action, entity, entity_id, before, after, ip, user_agent, detail, created_at
		FROM audit_logs WHERE 1=1`
	args := []any{}
	if entity != "" {
		q += ` AND entity = ?`
		args = append(args, entity)
	}
	if entityID != "" {
		q += ` AND entity_id = ?`
		args = append(args, entityID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	var out []AuditEntry
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("查询操作日志失败: %w", err)
	}
	return out, nil
}
