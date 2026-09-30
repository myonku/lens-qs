// Package inspection 实现核心业务：表单 / 笔 / 明细 的保存、历史快照与校验。
//
// 三层模型（对应 docs/design.md §4.2.3）：
//
//	Form        一张纸质表单（form_no 唯一）
//	FormRecord  该表单的一次工序填写（(form_id, sequence_no) 唯一）
//	RecordLine  一笔里的一行计数（默认 1 行）
//
// 设计原则（对应 docs/requirements.md v0.4 §1.4 最大灵活性）：
//   - 除 form_no 与其在表单内的序号唯一之外，不做任何强制
//   - 保存是"字段级合并"：请求里给了的字段才改，没给的保持原值
//   - 每次保存前写入快照，历史只增不改
//   - 校验默认只警告；只有调用方显式要求 enforce=strict 才可能返回阻断错误
package inspection

// Form 对应数据库表 forms。
type Form struct {
	ID         string `db:"id" json:"id"`
	FormNo     string `db:"form_no" json:"form_no"`
	UnitID     *string `db:"unit_id" json:"unit_id,omitempty"`
	UnitText   *string `db:"unit_text" json:"unit_text,omitempty"`
	SampleRaw  string `db:"sample_raw" json:"sample_raw"`

	SampleProject *string `db:"sample_project" json:"sample_project,omitempty"`
	SampleModel   *string `db:"sample_model" json:"sample_model,omitempty"`
	SampleSpec    *string `db:"sample_spec" json:"sample_spec,omitempty"`
	SampleSize    *string `db:"sample_size" json:"sample_size,omitempty"`
	SampleSide    *string `db:"sample_side" json:"sample_side,omitempty"`
	SampleClass   *string `db:"sample_class" json:"sample_class,omitempty"`
	SampleIsRx    *bool   `db:"sample_is_rx" json:"sample_is_rx,omitempty"`

	SampleDrawingID       *string `db:"sample_drawing_id" json:"sample_drawing_id,omitempty"`
	SampleUnmatchedReason *string `db:"sample_unmatched_reason" json:"sample_unmatched_reason,omitempty"`
	SampleParsedBy        string  `db:"sample_parsed_by" json:"sample_parsed_by"`
	SampleConfidence      float64 `db:"sample_confidence" json:"sample_confidence"`
	SampleVerified        bool    `db:"sample_verified" json:"sample_verified"`
	NeedsReview           bool    `db:"needs_review" json:"needs_review"`
	UnknownTokens         *string `db:"unknown_tokens" json:"unknown_tokens,omitempty"`

	State       string  `db:"state" json:"state"`
	VoidedAt    *string `db:"voided_at" json:"voided_at,omitempty"`
	VoidReason  *string `db:"void_reason" json:"void_reason,omitempty"`
	Remark      *string `db:"remark" json:"remark,omitempty"`
	CreatedBy   *string `db:"created_by" json:"created_by,omitempty"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	UpdatedBy   *string `db:"updated_by" json:"updated_by,omitempty"`
	UpdatedAt   string  `db:"updated_at" json:"updated_at"`

	Records []FormRecord `db:"-" json:"records,omitempty"`
}

// FormRecord 对应数据库表 form_records（一笔 = 一次工序填写）。
type FormRecord struct {
	ID         string `db:"id" json:"id"`
	FormID     string `db:"form_id" json:"form_id"`
	SequenceNo int    `db:"sequence_no" json:"sequence_no"`

	ProcessCode *string `db:"process_code" json:"process_code,omitempty"`
	ProcessText *string `db:"process_text" json:"process_text,omitempty"`

	// 表单既有栏目（按纸面文字记录，不与系统账号绑定）
	SubmittedBy     *string `db:"submitted_by" json:"submitted_by,omitempty"`
	SubmittedAt     *string `db:"submitted_at" json:"submitted_at,omitempty"`
	InspectedBy     *string `db:"inspected_by" json:"inspected_by,omitempty"`
	InspectedAt     *string `db:"inspected_at" json:"inspected_at,omitempty"`
	MachineOperator *string `db:"machine_operator" json:"machine_operator,omitempty"`
	HandoverBy      *string `db:"handover_by" json:"handover_by,omitempty"`

	// 系统账号留痕（谁录入 / 谁最后保存）
	RecordedBy   *string `db:"recorded_by" json:"recorded_by,omitempty"`
	RecordedAt   *string `db:"recorded_at" json:"recorded_at,omitempty"`
	SaveCount    int     `db:"save_count" json:"save_count"`
	LastSavedBy  *string `db:"last_saved_by" json:"last_saved_by,omitempty"`
	LastSavedAt  *string `db:"last_saved_at" json:"last_saved_at,omitempty"`

	IsComplete    bool    `db:"is_complete" json:"is_complete"`
	MissingFields *string `db:"missing_fields" json:"missing_fields,omitempty"`
	Remark        *string `db:"remark" json:"remark,omitempty"`

	TotalQty     int      `db:"total_qty" json:"total_qty"`
	QualifiedQty int      `db:"qualified_qty" json:"qualified_qty"`
	DefectQty    int      `db:"defect_qty" json:"defect_qty"`
	PassRate     *float64 `db:"pass_rate" json:"pass_rate,omitempty"`

	State      string  `db:"state" json:"state"`
	VoidedAt   *string `db:"voided_at" json:"voided_at,omitempty"`
	VoidReason *string `db:"void_reason" json:"void_reason,omitempty"`

	CreatedAt string `db:"created_at" json:"created_at"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`

	// 以下由服务层填充，不是数据库列
	Form  *Form        `db:"-" json:"-"`
	Lines []RecordLine `db:"-" json:"lines"`
}

// RecordLine 对应数据库表 record_lines（一行 = 纸质表单的一行计数）。
type RecordLine struct {
	ID       string `db:"id" json:"id,omitempty"`
	RecordID string `db:"record_id" json:"record_id,omitempty"`
	Seq      int    `db:"seq" json:"seq"`

	TotalQty     int `db:"total_qty" json:"total_qty"`
	QualifiedQty int `db:"qualified_qty" json:"qualified_qty"`
	DefectQty    int `db:"defect_qty" json:"defect_qty"`

	SpotConcave int `db:"spot_concave" json:"spot_concave"`
	SpotConvex  int `db:"spot_convex" json:"spot_convex"`
	SpotBoth    int `db:"spot_both" json:"spot_both"`

	ScratchConcave int `db:"scratch_concave" json:"scratch_concave"`
	ScratchConvex  int `db:"scratch_convex" json:"scratch_convex"`
	ScratchBoth    int `db:"scratch_both" json:"scratch_both"`

	DirtConcave int `db:"dirt_concave" json:"dirt_concave"`
	DirtConvex  int `db:"dirt_convex" json:"dirt_convex"`
	DirtBoth    int `db:"dirt_both" json:"dirt_both"`

	HardSpotConcave int `db:"hard_spot_concave" json:"hard_spot_concave"`
	HardSpotConvex  int `db:"hard_spot_convex" json:"hard_spot_convex"`
	HardSpotBoth    int `db:"hard_spot_both" json:"hard_spot_both"`

	DebrisConcave int `db:"debris_concave" json:"debris_concave"`
	DebrisConvex  int `db:"debris_convex" json:"debris_convex"`
	DebrisBoth    int `db:"debris_both" json:"debris_both"`

	// 工序同时保留在行上（不收敛未知语义，见需求 §5.1）
	ProcessCode *string `db:"process_code" json:"process_code,omitempty"`
	ProcessText *string `db:"process_text" json:"process_text,omitempty"`
	Remark      *string `db:"remark" json:"remark,omitempty"`

	CreatedAt string `db:"created_at" json:"created_at"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`
}

// DefectGroup 是"不良类别 → 三个表面子项"的映射，用于校验与汇总。
var DefectGroups = []struct {
	Code    string
	Name    string
	Concave func(RecordLine) int
	Convex  func(RecordLine) int
	Both    func(RecordLine) int
}{
	{"spot", "点伤", func(l RecordLine) int { return l.SpotConcave }, func(l RecordLine) int { return l.SpotConvex }, func(l RecordLine) int { return l.SpotBoth }},
	{"scratch", "划伤", func(l RecordLine) int { return l.ScratchConcave }, func(l RecordLine) int { return l.ScratchConvex }, func(l RecordLine) int { return l.ScratchBoth }},
	{"dirt", "脏污", func(l RecordLine) int { return l.DirtConcave }, func(l RecordLine) int { return l.DirtConvex }, func(l RecordLine) int { return l.DirtBoth }},
	{"hard_spot", "加硬点", func(l RecordLine) int { return l.HardSpotConcave }, func(l RecordLine) int { return l.HardSpotConvex }, func(l RecordLine) int { return l.HardSpotBoth }},
	{"debris", "垃圾点", func(l RecordLine) int { return l.DebrisConcave }, func(l RecordLine) int { return l.DebrisConvex }, func(l RecordLine) int { return l.DebrisBoth }},
}

// DefectTotal 返回一行中五类不良 15 个子项之和。
func DefectTotal(l RecordLine) int {
	sum := 0
	for _, g := range DefectGroups {
		sum += g.Concave(l) + g.Convex(l) + g.Both(l)
	}
	return sum
}
