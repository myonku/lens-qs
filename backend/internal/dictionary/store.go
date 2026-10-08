// Package dictionary 实现字典与参考部件号的读写。
//
// 字典覆盖：工序、单位、样品信息各维度取值、不良类别文案等。
// 设计取向（需求 §1.4 F-8）：新增取值只需加数据，不需要改代码。
package dictionary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// 字典类别常量。
const (
	CategoryProcess       = "process"              // 工序，如 一次加硬（兼容旧命名）
	CategoryInspectionOp  = "inspection_operation" // 检验工序（表单第一列的可选值）
	CategorySampleProject = "sample_project"       // 项目，如 M1 / M2
	CategorySampleModel   = "sample_model"         // 型号，如 VID1 RX / VID2
	CategorySampleSpec    = "sample_spec"          // 规格，如 S1 / S2
	CategorySampleSize    = "sample_size"          // 大小，如 MD / SM / LG
	CategorySampleSide    = "sample_side"          // 左右，如 L / R
	CategorySampleClass   = "sample_class"         // 结构类别（含义未确认，冗余维度）
	CategoryUnit          = "unit"                 // 单位
)

// Item 对应数据库表 dict_items。
type Item struct {
	ID        string  `db:"id" json:"id"`
	Category  string  `db:"category" json:"category"`
	Code      string  `db:"code" json:"code"`
	Label     string  `db:"label" json:"label"`
	SortNo    int     `db:"sort_no" json:"sort_no"`
	Active    bool    `db:"active" json:"active"`
	Meta      *string `db:"meta" json:"meta,omitempty"`
	CreatedAt string  `db:"created_at" json:"created_at"`
	UpdatedAt string  `db:"updated_at" json:"updated_at"`
}

// Unit 对应数据库表 units。
type Unit struct {
	ID        string `db:"id" json:"id"`
	Code      string `db:"code" json:"code"`
	Name      string `db:"name" json:"name"`
	SortNo    int    `db:"sort_no" json:"sort_no"`
	Active    bool   `db:"active" json:"active"`
	CreatedAt string `db:"created_at" json:"created_at"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`
}

// Drawing 对应数据库表 sample_drawings（参考部件号清单，仅供参考）。
type Drawing struct {
	ID          string  `db:"id" json:"id"`
	SeqNo       *string `db:"seq_no" json:"seq_no,omitempty"`
	DrawingNo   string  `db:"drawing_no" json:"drawing_no"`
	ProjectCode *string `db:"project_code" json:"project_code,omitempty"`
	ModelCode   *string `db:"model_code" json:"model_code,omitempty"`
	SpecCode    *string `db:"spec_code" json:"spec_code,omitempty"`
	SizeCode    *string `db:"size_code" json:"size_code,omitempty"`
	SideCode    *string `db:"side_code" json:"side_code,omitempty"`
	ClassCode   *string `db:"class_code" json:"class_code,omitempty"`
	IsRx        bool    `db:"is_rx" json:"is_rx"`
	Active      bool    `db:"active" json:"active"`
	Note        *string `db:"note" json:"note,omitempty"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	UpdatedAt   string  `db:"updated_at" json:"updated_at"`
}

// Store 负责字典持久化。
type Store struct {
	db     *sqlx.DB
	driver string
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB, driver string) *Store { return &Store{db: db, driver: driver} }

func (s *Store) rebind(q string) string { return s.db.Rebind(q) }

// ------------------------------- 字典项 -------------------------------

const itemColumns = `id, category, code, label, sort_no, active, meta, created_at, updated_at`

// ListItems 按类别查询字典项。
func (s *Store) ListItems(ctx context.Context, category string, includeInactive bool) ([]Item, error) {
	q := `SELECT ` + itemColumns + ` FROM dict_items WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(category) != "" {
		q += ` AND category = ?`
		args = append(args, category)
	}
	if !includeInactive {
		q += ` AND active = ?`
		args = append(args, true)
	}
	q += ` ORDER BY category, sort_no, code`

	var out []Item
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("查询字典失败: %w", err)
	}
	return out, nil
}

// UpsertItem 新增或更新一个字典项（按 category+code 唯一）。
func (s *Store) UpsertItem(ctx context.Context, it *Item) (*Item, error) {
	it.Category = strings.TrimSpace(it.Category)
	it.Code = strings.TrimSpace(it.Code)
	if it.Category == "" || it.Code == "" {
		return nil, httpx.BadRequest("字典项的 category 与 code 不能为空")
	}
	if strings.TrimSpace(it.Label) == "" {
		it.Label = it.Code
	}
	now := store.FormatTime(time.Now())

	var existing Item
	err := s.db.GetContext(ctx, &existing, s.rebind(`SELECT `+itemColumns+`
		FROM dict_items WHERE category = ? AND code = ?`), it.Category, it.Code)
	switch {
	case err == nil:
		existing.Label = it.Label
		existing.SortNo = it.SortNo
		existing.Active = it.Active
		existing.Meta = it.Meta
		existing.UpdatedAt = now
		if _, err := s.db.NamedExecContext(ctx, `UPDATE dict_items SET
			label = :label, sort_no = :sort_no, active = :active, meta = :meta, updated_at = :updated_at
			WHERE id = :id`, &existing); err != nil {
			return nil, fmt.Errorf("更新字典项失败: %w", err)
		}
		return &existing, nil
	case errors.Is(err, sql.ErrNoRows):
		if it.ID == "" {
			it.ID = id.New()
		}
		it.CreatedAt = now
		it.UpdatedAt = now
		if _, err := s.db.NamedExecContext(ctx, `INSERT INTO dict_items (`+itemColumns+`) VALUES (
			:id, :category, :code, :label, :sort_no, :active, :meta, :created_at, :updated_at)`, it); err != nil {
			return nil, fmt.Errorf("新增字典项失败: %w", err)
		}
		return it, nil
	default:
		return nil, fmt.Errorf("查询字典项失败: %w", err)
	}
}

// UpdateItem 按主键更新字典项（用于改标签 / 停用）。
func (s *Store) UpdateItem(ctx context.Context, itemID string, label *string, sortNo *int, active *bool, meta *string) (*Item, error) {
	var it Item
	err := s.db.GetContext(ctx, &it, s.rebind(`SELECT `+itemColumns+` FROM dict_items WHERE id = ?`), itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("字典项不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("查询字典项失败: %w", err)
	}
	if label != nil {
		it.Label = strings.TrimSpace(*label)
	}
	if sortNo != nil {
		it.SortNo = *sortNo
	}
	if active != nil {
		it.Active = *active
	}
	if meta != nil {
		it.Meta = meta
	}
	it.UpdatedAt = store.FormatTime(time.Now())
	if _, err := s.db.NamedExecContext(ctx, `UPDATE dict_items SET
		label = :label, sort_no = :sort_no, active = :active, meta = :meta, updated_at = :updated_at
		WHERE id = :id`, &it); err != nil {
		return nil, fmt.Errorf("更新字典项失败: %w", err)
	}
	return &it, nil
}

// ------------------------------- 单位 -------------------------------

const unitColumns = `id, code, name, sort_no, active, created_at, updated_at`

// ListUnits 查询单位。
func (s *Store) ListUnits(ctx context.Context, includeInactive bool) ([]Unit, error) {
	q := `SELECT ` + unitColumns + ` FROM units WHERE 1=1`
	if !includeInactive {
		// 用 1 而非 TRUE：SQLite 与 PostgreSQL（boolean 可与整数比较）都适用
		q += ` AND active = 1`
	}
	q += ` ORDER BY sort_no, code`

	var out []Unit
	if err := s.db.SelectContext(ctx, &out, s.rebind(q)); err != nil {
		return nil, fmt.Errorf("查询单位失败: %w", err)
	}
	return out, nil
}

// UpsertUnit 新增或更新单位。
func (s *Store) UpsertUnit(ctx context.Context, u *Unit) (*Unit, error) {
	u.Code = strings.TrimSpace(u.Code)
	if u.Code == "" {
		return nil, httpx.BadRequest("单位编码（code）不能为空")
	}
	if strings.TrimSpace(u.Name) == "" {
		u.Name = u.Code
	}
	now := store.FormatTime(time.Now())

	var existing Unit
	err := s.db.GetContext(ctx, &existing, s.rebind(`SELECT `+unitColumns+` FROM units WHERE code = ?`), u.Code)
	switch {
	case err == nil:
		existing.Name = u.Name
		existing.SortNo = u.SortNo
		existing.Active = u.Active
		existing.UpdatedAt = now
		if _, err := s.db.NamedExecContext(ctx, `UPDATE units SET
			name = :name, sort_no = :sort_no, active = :active, updated_at = :updated_at
			WHERE id = :id`, &existing); err != nil {
			return nil, fmt.Errorf("更新单位失败: %w", err)
		}
		return &existing, nil
	case errors.Is(err, sql.ErrNoRows):
		if u.ID == "" {
			u.ID = id.New()
		}
		u.CreatedAt = now
		u.UpdatedAt = now
		if _, err := s.db.NamedExecContext(ctx, `INSERT INTO units (`+unitColumns+`) VALUES (
			:id, :code, :name, :sort_no, :active, :created_at, :updated_at)`, u); err != nil {
			return nil, fmt.Errorf("新增单位失败: %w", err)
		}
		return u, nil
	default:
		return nil, fmt.Errorf("查询单位失败: %w", err)
	}
}

// ------------------------------- 参考部件号 -------------------------------

const drawingColumns = `id, seq_no, drawing_no, project_code, model_code, spec_code, size_code,
	side_code, class_code, is_rx, active, note, created_at, updated_at`

// DrawingFilter 是参考部件号查询条件（全部可选）。
type DrawingFilter struct {
	Project string
	Model   string
	Spec    string
	Size    string
	Side    string
	Keyword string
	Limit   int
}

// ListDrawings 查询参考部件号（不匹配不算错误，仅作为参考）。
func (s *Store) ListDrawings(ctx context.Context, f DrawingFilter) ([]Drawing, error) {
	q := `SELECT ` + drawingColumns + ` FROM sample_drawings WHERE active = 1`
	args := []any{}
	add := func(col, val string) {
		if strings.TrimSpace(val) != "" {
			q += ` AND ` + col + ` = ?`
			args = append(args, strings.TrimSpace(val))
		}
	}
	add("project_code", f.Project)
	add("model_code", f.Model)
	add("spec_code", f.Spec)
	add("size_code", f.Size)
	add("side_code", f.Side)
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		q += ` AND drawing_no LIKE ?`
		args = append(args, "%"+kw+"%")
	}
	q += ` ORDER BY seq_no, drawing_no`
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q += ` LIMIT ?`
	args = append(args, limit)

	var out []Drawing
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("查询参考部件号失败: %w", err)
	}
	return out, nil
}

// FindDrawingByDims 按维度组合反查参考部件号；找不到返回 (nil, nil)。
func (s *Store) FindDrawingByDims(ctx context.Context, project, model, spec, size, side string) (*Drawing, error) {
	q := `SELECT ` + drawingColumns + ` FROM sample_drawings WHERE active = 1
		AND COALESCE(spec_code,'') = ? AND COALESCE(size_code,'') = ?
		AND COALESCE(side_code,'') = ? AND COALESCE(model_code,'') = ?`
	args := []any{spec, size, side, model}
	if strings.TrimSpace(project) != "" {
		q += ` AND COALESCE(project_code,'') = ?`
		args = append(args, project)
	}
	q += ` ORDER BY seq_no LIMIT 1`

	var d Drawing
	err := s.db.GetContext(ctx, &d, s.rebind(q), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("匹配参考部件号失败: %w", err)
	}
	return &d, nil
}

// UpsertDrawings 批量导入参考部件号，返回新增/更新条数。
func (s *Store) UpsertDrawings(ctx context.Context, list []Drawing) (inserted, updated int, err error) {
	now := store.FormatTime(time.Now())
	for i := range list {
		d := &list[i]
		d.DrawingNo = strings.TrimSpace(d.DrawingNo)
		if d.DrawingNo == "" {
			continue
		}
		var existing Drawing
		e := s.db.GetContext(ctx, &existing, s.rebind(`SELECT `+drawingColumns+`
			FROM sample_drawings WHERE drawing_no = ?`), d.DrawingNo)
		switch {
		case e == nil:
			existing.SeqNo = d.SeqNo
			existing.ProjectCode = d.ProjectCode
			existing.ModelCode = d.ModelCode
			existing.SpecCode = d.SpecCode
			existing.SizeCode = d.SizeCode
			existing.SideCode = d.SideCode
			existing.ClassCode = d.ClassCode
			existing.IsRx = d.IsRx
			existing.Active = d.Active
			existing.Note = d.Note
			existing.UpdatedAt = now
			if _, err := s.db.NamedExecContext(ctx, `UPDATE sample_drawings SET
				seq_no = :seq_no, project_code = :project_code, model_code = :model_code,
				spec_code = :spec_code, size_code = :size_code, side_code = :side_code,
				class_code = :class_code, is_rx = :is_rx, active = :active, note = :note,
				updated_at = :updated_at WHERE id = :id`, &existing); err != nil {
				return inserted, updated, fmt.Errorf("更新参考部件号失败: %w", err)
			}
			updated++
		case errors.Is(e, sql.ErrNoRows):
			if d.ID == "" {
				d.ID = id.New()
			}
			d.CreatedAt = now
			d.UpdatedAt = now
			if _, err := s.db.NamedExecContext(ctx, `INSERT INTO sample_drawings (`+drawingColumns+`) VALUES (
				:id, :seq_no, :drawing_no, :project_code, :model_code, :spec_code, :size_code,
				:side_code, :class_code, :is_rx, :active, :note, :created_at, :updated_at)`, d); err != nil {
				return inserted, updated, fmt.Errorf("新增参考部件号失败: %w", err)
			}
			inserted++
		default:
			return inserted, updated, fmt.Errorf("查询参考部件号失败: %w", e)
		}
	}
	return inserted, updated, nil
}
