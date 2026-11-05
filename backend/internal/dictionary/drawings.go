package dictionary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

func nullableBool(b *bool) any {
	if b == nil {
		return nil
	}
	return store.BoolInt(*b)
}

const DrawingColumns = `id,seq_no,drawing_no,project_code,model_code,spec_code,size_code,side_code,class_code,is_rx,active,note,created_at,updated_at`

func (d Drawing) MarshalJSON() ([]byte, error) {
	type value Drawing
	return json.Marshal(struct {
		value
		SeqNo   *string `json:"seq_no"`
		Project *string `json:"project_code"`
		Model   *string `json:"model_code"`
		Spec    *string `json:"spec_code"`
		Size    *string `json:"size_code"`
		Side    *string `json:"side_code"`
		Class   *string `json:"class_code"`
		Note    *string `json:"note"`
	}{value(d), d.SeqNo, d.ProjectCode, d.ModelCode, d.SpecCode, d.SizeCode, d.SideCode, d.ClassCode, d.Note})
}

type DrawingInput struct {
	SeqNo     *string              `json:"seq_no"`
	DrawingNo string               `json:"drawing_no"`
	Project   *string              `json:"project_code"`
	Model     *string              `json:"model_code"`
	Spec      *string              `json:"spec_code"`
	Size      *string              `json:"size_code"`
	Side      *string              `json:"side_code"`
	Class     *string              `json:"class_code"`
	IsRx      *bool                `json:"is_rx"`
	Active    httpx.Optional[bool] `json:"active"`
	Note      *string              `json:"note"`
}

func RegisterDrawings(g *gin.RouterGroup, db *sqlx.DB) {
	g.GET("/dict/drawings", func(c *gin.Context) {
		if e := httpx.Query(c, "page", "page_size", "project", "model", "spec", "size", "side", "keyword", "include_inactive"); e != nil {
			httpx.Fail(c, e)
			return
		}
		page, size, e := httpx.Pagination(c)
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		if v, ok := c.GetQuery("include_inactive"); ok && v != "true" && v != "false" {
			httpx.Fail(c, httpx.BadRequest("非法 include_inactive"))
			return
		}
		var result any
		_, e = store.ReadView(c.Request.Context(), db, func(q store.Reader, _ store.Snapshot) error {
			where := ` WHERE 1=1`
			args := []any{}
			if c.Query("include_inactive") != "true" {
				where += ` AND active=1`
			}
			for _, key := range []string{"project", "model", "spec", "size", "side"} {
				if c.Request.URL.Query().Has(key) {
					where += ` AND ` + key + `_code=?`
					args = append(args, c.Query(key))
				}
			}
			if c.Request.URL.Query().Has("keyword") {
				where += ` AND drawing_no LIKE ? ESCAPE '!'`
				args = append(args, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(c.Query("keyword"))+"%")
			}
			var n int
			if e := q.GetContext(c.Request.Context(), &n, q.Rebind(`SELECT COUNT(*) FROM sample_drawings`+where), args...); e != nil {
				return e
			}
			rows := []Drawing{}
			args = append(args, size, (page-1)*size)
			e := q.SelectContext(c.Request.Context(), &rows, q.Rebind(`SELECT `+DrawingColumns+` FROM sample_drawings`+where+` ORDER BY CASE WHEN seq_no IS NULL THEN 1 ELSE 0 END,seq_no,drawing_no,id LIMIT ? OFFSET ?`), args...)
			result = httpx.NewPage(rows, n, page, size)
			return e
		})
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		httpx.OK(c, result)
	})
	g.POST("/dict/drawings/import", func(c *gin.Context) {
		if e := httpx.Query(c); e != nil {
			httpx.Fail(c, e)
			return
		}
		var input struct {
			Items []DrawingInput `json:"items"`
		}
		if e := httpx.BindJSON(c, &input); e != nil {
			httpx.Fail(c, e)
			return
		}
		if len(input.Items) < 1 || len(input.Items) > 1000 {
			httpx.Fail(c, httpx.BadRequest("items 必须 1–1000 项"))
			return
		}
		inserted, updated, e := ImportDrawings(c.Request.Context(), db, input.Items)
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		httpx.OK(c, gin.H{"inserted": inserted, "updated": updated, "total": len(input.Items)})
	})
}
func ImportDrawings(ctx context.Context, db *sqlx.DB, items []DrawingInput) (int, int, error) {
	w, e := store.BeginWrite(ctx, db, "", false, nil)
	if e != nil {
		return 0, 0, e
	}
	defer w.Close()
	inserted, updated := 0, 0
	seen := map[string]bool{}
	seqs := map[string]bool{}
	// Validate the whole request before touching rows; unique seq numbers may be
	// exchanged within this import, so release only those rows inside the transaction.
	for i, in := range items {
		if in.Active.Set && in.Active.Value == nil {
			return 0, 0, httpx.BadRequest("active cannot be null", gin.H{"index": i})
		}
		if strings.TrimSpace(in.DrawingNo) == "" || seen[in.DrawingNo] {
			return 0, 0, httpx.Validation("非法或重复图号", gin.H{"reason": "invalid_drawing", "index": i})
		}
		seen[in.DrawingNo] = true
		if in.SeqNo != nil {
			if seqs[*in.SeqNo] {
				return 0, 0, httpx.Validation("重复序号", gin.H{"reason": "drawing_seq_conflict", "index": i})
			}
			seqs[*in.SeqNo] = true
		}
	}
	before := map[string]Drawing{}
	for _, in := range items {
		var d Drawing
		e = w.GetContext(w.Context, &d, w.Rebind(`SELECT `+DrawingColumns+` FROM sample_drawings WHERE drawing_no=?`), in.DrawingNo)
		if e == nil {
			before[in.DrawingNo] = d
			if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sample_drawings SET seq_no=NULL WHERE id=?`), d.ID); e != nil {
				return 0, 0, e
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return 0, 0, e
		}
	}
	for i, in := range items {
		var conflict string
		if in.SeqNo != nil {
			e = w.GetContext(w.Context, &conflict, w.Rebind(`SELECT id FROM sample_drawings WHERE seq_no=? AND drawing_no<>?`), *in.SeqNo, in.DrawingNo)
			if e == nil {
				return 0, 0, httpx.Validation("序号已占用", gin.H{"reason": "drawing_seq_conflict", "index": i})
			}
			if e != sql.ErrNoRows {
				return 0, 0, e
			}
		}
		old, exists := before[in.DrawingNo]
		d := Drawing{ID: old.ID, SeqNo: in.SeqNo, DrawingNo: in.DrawingNo, ProjectCode: in.Project, ModelCode: in.Model, SpecCode: in.Spec, SizeCode: in.Size, SideCode: in.Side, ClassCode: in.Class, IsRx: in.IsRx, Active: true, Note: in.Note, CreatedAt: old.CreatedAt, UpdatedAt: w.Time}
		if in.Active.Value != nil {
			d.Active = *in.Active.Value
		}
		var previous any
		if exists {
			updated++
			previous = old
		} else {
			inserted++
			d.ID = id.New()
			d.CreatedAt = w.Time
		}
		if exists {
			_, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sample_drawings SET seq_no=?,project_code=?,model_code=?,spec_code=?,size_code=?,side_code=?,class_code=?,is_rx=?,active=?,note=?,updated_at=? WHERE id=?`), d.SeqNo, d.ProjectCode, d.ModelCode, d.SpecCode, d.SizeCode, d.SideCode, d.ClassCode, nullableBool(d.IsRx), store.BoolInt(d.Active), d.Note, d.UpdatedAt, d.ID)
		} else {
			_, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO sample_drawings(`+DrawingColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), d.ID, d.SeqNo, d.DrawingNo, d.ProjectCode, d.ModelCode, d.SpecCode, d.SizeCode, d.SideCode, d.ClassCode, nullableBool(d.IsRx), store.BoolInt(d.Active), d.Note, d.CreatedAt, d.UpdatedAt)
		}
		if e != nil {
			return 0, 0, e
		}
		if e = w.Event("maintain_drawing", "dict_item", d.ID, previous, d, nil); e != nil {
			return 0, 0, e
		}
	}
	if _, e = w.ExecContext(w.Context, `UPDATE shared_catalog_state SET version=version+1 WHERE id=1`); e != nil {
		return 0, 0, e
	}
	return inserted, updated, w.Commit()
}
