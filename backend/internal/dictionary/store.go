// Package dictionary 实现字典与参考部件号的读写。
//
// 字典覆盖：工序、单位、样品信息各维度取值、不良类别文案等。
// 设计取向（需求 §1.4 F-8）：新增取值只需加数据，不需要改代码。
package dictionary

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// 字典类别常量。
const (
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
	IsRx        *bool   `db:"is_rx" json:"is_rx"`
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
	if category != "" && !validCategory(category) {
		return nil, httpx.Validation("invalid_dictionary_category", nil)
	}
	q := `SELECT ` + itemColumns + ` FROM dict_items WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(category) != "" {
		q += ` AND category = ?`
		args = append(args, category)
	}
	if !includeInactive {
		q += ` AND active = ?`
		args = append(args, 1)
	}
	q += ` ORDER BY category, sort_no, code`

	var out []Item
	if err := s.db.SelectContext(ctx, &out, s.rebind(q), args...); err != nil {
		return nil, fmt.Errorf("查询字典失败: %w", err)
	}
	return out, nil
}

func Categories() []string {
	return []string{CategoryInspectionOp, CategorySampleProject, CategorySampleModel, CategorySampleSpec, CategorySampleSize, CategorySampleSide, CategorySampleClass}
}
func validCategory(c string) bool {
	for _, v := range Categories() {
		if v == c {
			return true
		}
	}
	return false
}
func validMeta(meta *string) error {
	if meta == nil {
		return nil
	}
	var value map[string]any
	if e := json.Unmarshal([]byte(*meta), &value); e != nil {
		return httpx.BadRequest("meta 必须是对象或 null")
	}
	if aliases, ok := value["aliases"]; ok && aliases != nil {
		list, ok := aliases.([]any)
		if !ok {
			return httpx.BadRequest("meta.aliases must be string array")
		}
		for _, v := range list {
			if _, ok := v.(string); !ok {
				return httpx.BadRequest("meta.aliases must be string array")
			}
		}
	}
	return nil
}

// Metadata is encoded as object/null, never a JSON string.
func (it Item) MarshalJSON() ([]byte, error) {
	type alias Item
	var meta json.RawMessage = []byte("null")
	if it.Meta != nil {
		meta = []byte(*it.Meta)
	}
	return json.Marshal(struct {
		alias
		Meta json.RawMessage `json:"meta"`
	}{alias(it), meta})
}
func (s *Store) writeItem(w *store.WriteTx, it *Item, before any) (*Item, error) {
	result, err := s.persistItem(w, it, before)
	if err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// persistItem shares validation and audit behavior within a caller-owned transaction.
func (s *Store) persistItem(w *store.WriteTx, it *Item, before any) (*Item, error) {
	if !validCategory(it.Category) {
		return nil, httpx.Validation("invalid_dictionary_category", nil)
	}
	if e := validMeta(it.Meta); e != nil {
		return nil, e
	}
	it.Code = strings.TrimSpace(it.Code)
	if it.Code == "" {
		return nil, httpx.BadRequest("code required")
	}
	if strings.TrimSpace(it.Label) == "" {
		it.Label = it.Code
	}
	it.UpdatedAt = w.Time
	if _, e := w.ExecContext(w.Context, `UPDATE shared_catalog_state SET version=version+1 WHERE id=1`); e != nil {
		return nil, e
	}
	q := `UPDATE dict_items SET label=:label,sort_no=:sort_no,active=:active,meta=:meta,updated_at=:updated_at WHERE id=:id`
	if before == nil {
		it.ID = id.New()
		it.CreatedAt = w.Time
		q = `INSERT INTO dict_items (` + itemColumns + `) VALUES(:id,:category,:code,:label,:sort_no,:active,:meta,:created_at,:updated_at)`
	}
	query, args, e := sqlx.Named(q, it)
	if e != nil {
		return nil, e
	}
	for i, v := range args {
		if b, ok := v.(bool); ok {
			args[i] = store.BoolInt(b)
		}
	}
	if _, e = w.ExecContext(w.Context, w.Rebind(query), args...); e != nil {
		return nil, store.StorageError(e)
	}
	if e = w.Event("maintain", "dict_item", it.ID, before, it, nil); e != nil {
		return nil, e
	}
	return it, nil
}
func (s *Store) UpsertItem(ctx context.Context, it *Item) (*Item, error) {
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return nil, e
	}
	defer w.Close()
	var old Item
	e = w.GetContext(w.Context, &old, w.Rebind(`SELECT `+itemColumns+` FROM dict_items WHERE category=? AND code=?`), it.Category, it.Code)
	var before any
	if e == nil {
		before = old
		it.ID = old.ID
		it.CreatedAt = old.CreatedAt
	} else if e != sql.ErrNoRows {
		return nil, e
	}
	return s.writeItem(w, it, before)
}
func (s *Store) InitializeItem(ctx context.Context, it *Item) error {
	var n int
	if e := s.db.GetContext(ctx, &n, s.rebind(`SELECT COUNT(*) FROM dict_items WHERE category=? AND code=?`), it.Category, it.Code); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	_, e := s.UpsertItem(ctx, it)
	return e
}
func (s *Store) UpdateItem(ctx context.Context, itemID string, label *string, sortNo *int, active *bool, meta httpx.Optional[json.RawMessage]) (*Item, error) {
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return nil, e
	}
	defer w.Close()
	var it Item
	if e = w.GetContext(w.Context, &it, w.Rebind(`SELECT `+itemColumns+` FROM dict_items WHERE id=?`), itemID); e != nil {
		if e == sql.ErrNoRows {
			return nil, httpx.NotFound("dict item")
		}
		return nil, e
	}
	before := it
	if label != nil {
		it.Label = *label
	}
	if sortNo != nil {
		it.SortNo = *sortNo
	}
	if active != nil {
		it.Active = *active
	}
	if meta.Set {
		it.Meta = nil
		if meta.Value != nil {
			v := string(*meta.Value)
			it.Meta = &v
		}
	}
	return s.writeItem(w, &it, before)
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

// InitializeDrawings fills missing reference rows without overwriting manual configuration.
func (s *Store) InitializeDrawings(ctx context.Context, list []Drawing) (int, error) {
	w, e := store.BeginWrite(ctx, s.db, "", false, nil)
	if e != nil {
		return 0, e
	}
	defer w.Close()
	n := 0
	for _, d := range list {
		if strings.TrimSpace(d.DrawingNo) == "" {
			return 0, httpx.Validation("empty drawing_no", nil)
		}
		var count int
		if e = w.GetContext(w.Context, &count, w.Rebind(`SELECT COUNT(*) FROM sample_drawings WHERE drawing_no=?`), d.DrawingNo); e != nil {
			return 0, e
		}
		if count > 0 {
			continue
		}
		d.ID = id.New()
		d.CreatedAt = w.Time
		d.UpdatedAt = w.Time
		if _, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO sample_drawings(id,seq_no,drawing_no,project_code,model_code,spec_code,size_code,side_code,class_code,is_rx,active,note,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), d.ID, d.SeqNo, d.DrawingNo, d.ProjectCode, d.ModelCode, d.SpecCode, d.SizeCode, d.SideCode, d.ClassCode, nullableBool(d.IsRx), store.BoolInt(d.Active), d.Note, d.CreatedAt, d.UpdatedAt); e != nil {
			return 0, e
		}
		n++
	}
	if n > 0 {
		if _, e = w.ExecContext(w.Context, `UPDATE shared_catalog_state SET version=version+1 WHERE id=1`); e != nil {
			return 0, e
		}
		if e = w.Event("initialize", "dict_item", "reference-drawings", nil, struct{ Inserted int }{n}, nil); e != nil {
			return 0, e
		}
	}
	return n, w.Commit()
}
