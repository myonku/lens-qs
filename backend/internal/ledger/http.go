package ledger

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

type HTTP struct{ DB *sqlx.DB }

func Register(g *gin.RouterGroup, db *sqlx.DB) {
	h := HTTP{db}
	g.GET("/product-lots", h.lots)
	g.POST("/product-lots", h.createLot)
	g.GET("/product-lots/:id", h.lot)
	g.GET("/product-lots/:id/operation-groups", h.groups)
	g.POST("/product-lots/:id/operation-groups", h.createGroup)
	g.GET("/operation-groups/:id", h.group)
	g.POST("/operation-groups/:id/close", h.groupState)
	g.POST("/operation-groups/:id/reopen", h.groupState)
	g.POST("/submissions", h.createSubmission)
	g.POST("/submissions/batch", h.batch)
	g.PATCH("/submissions/:id", h.save)
	g.POST("/submissions/:id/cancel", h.cancel)
	g.GET("/submissions", h.submissions)
	g.GET("/submissions/:id", h.submission)
	g.GET("/submissions/:id/revisions", h.revisions)
	g.GET("/submissions/:id/revisions/:revision_id", h.revision)
	g.GET("/submissions/:id/diff", h.diff)
	g.GET("/stats/dimensions", h.dimensions)
	g.GET("/stats/summary", h.summary)
	g.GET("/stats/defects", h.defects)
	g.GET("/stats/quantity-changes", h.changes)
	g.GET("/stats/workload", h.workload)
	g.GET("/reports/product-lots/:id", h.productReport)
	g.GET("/reports/operations/:id", h.operationReport)
	g.GET("/audit-logs", h.audit)
}
func query(c *gin.Context, names ...string) bool {
	if e := httpx.Query(c, names...); e != nil {
		httpx.Fail(c, e)
		return false
	}
	return true
}
func body(c *gin.Context, dst any) ([]byte, error) {
	mediaType, _, mediaErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return nil, httpx.BadRequest("需要 application/json")
	}
	raw, e := io.ReadAll(io.LimitReader(c.Request.Body, 4*1024*1024+1))
	if e != nil || len(raw) > 4*1024*1024 {
		return nil, httpx.BadRequest("正文过大或不可读")
	}
	return raw, httpx.DecodeStrict(raw, dst)
}
func kindAllowed(kind *string, r *Repository) error {
	if kind == nil {
		return nil
	}
	if *kind != "NORMAL" && *kind != "SAMPLING" {
		return httpx.BadRequest("非法 inspection_kind")
	}
	if *kind != r.w.Actor.Kind {
		return httpx.NewError(403, "forbidden", "检验类型不允许", map[string]string{"reason": "inspection_kind_forbidden", "expected_kind": r.w.Actor.Kind, "target_kind": *kind})
	}
	return nil
}
func (h HTTP) write(c *gin.Context, raw []byte, status int, fn func(*Repository) (Mutation, error), project func(Mutation) any) {
	if !query(c) {
		return
	}
	m, replay, e := Write(c.Request.Context(), h.DB, Request{ActorID: middleware.CurrentUserID(c), Key: c.GetHeader("X-Idempotency-Key"), Method: c.Request.Method, Path: c.Request.URL.Path, Raw: raw, SuccessStatus: status}, nil, fn)
	if e != nil {
		httpx.Fail(c, store.StorageError(e))
		return
	}
	if replay {
		c.Header("X-Idempotent-Replay", "true")
	}
	c.JSON(status, gin.H{"code": "ok", "message": "", "data": project(m)})
}
func (h HTTP) read(c *gin.Context, fn func(context.Context, store.Reader, store.Snapshot) (any, error)) {
	var result any
	_, e := store.ReadView(c.Request.Context(), h.DB, func(q store.Reader, s store.Snapshot) error {
		var e error
		result, e = fn(c.Request.Context(), q, s)
		return e
	})
	if e != nil {
		httpx.Fail(c, store.StorageError(e))
		return
	}
	httpx.OK(c, result)
}
func snapshotTime(s store.Snapshot) time.Time { t, _ := time.Parse(time.RFC3339, s.AsofTime); return t }
func (h HTTP) createLot(c *gin.Context) {
	var in struct {
		Shared   string  `json:"shared_form_no"`
		Identity string  `json:"sample_identity"`
		Raw      *string `json:"sample_raw"`
		Project  *string `json:"sample_project"`
		Model    *string `json:"sample_model"`
		Spec     *string `json:"sample_spec"`
		Size     *string `json:"sample_size"`
		Side     *string `json:"sample_side"`
	}
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if strings.TrimSpace(in.Shared) == "" || strings.TrimSpace(in.Identity) == "" {
		httpx.Fail(c, httpx.BadRequest("生产批身份必填"))
		return
	}
	h.write(c, raw, 201, func(r *Repository) (Mutation, error) {
		l, e := r.CreateLot(in.Shared, in.Identity, Content{SampleRaw: in.Raw, SampleProject: in.Project, SampleModel: in.Model, SampleSpec: in.Spec, SampleSize: in.Size, SampleSide: in.Side})
		return Mutation{Lot: &l}, e
	}, func(m Mutation) any { return m.Lot })
}
func (h HTTP) createGroup(c *gin.Context) {
	var in struct {
		Operation string          `json:"operation_id"`
		Kind      json.RawMessage `json:"inspection_kind"`
	}
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if strings.TrimSpace(in.Operation) == "" {
		httpx.Fail(c, httpx.BadRequest("operation_id 必填"))
		return
	}
	var kind *string
	if len(in.Kind) > 0 {
		var s string
		if string(in.Kind) == "null" || json.Unmarshal(in.Kind, &s) != nil {
			httpx.Fail(c, httpx.BadRequest("非法 inspection_kind"))
			return
		}
		kind = &s
	}
	h.write(c, raw, 201, func(r *Repository) (Mutation, error) {
		if e := kindAllowed(kind, r); e != nil {
			return Mutation{}, e
		}
		g, e := r.CreateGroup(c.Param("id"), in.Operation)
		return Mutation{Group: &g}, e
	}, func(m Mutation) any { return m.Group })
}

type newSubmission struct {
	Group   *string         `json:"operation_group_id"`
	Kind    json.RawMessage `json:"inspection_kind"`
	Batch   *string         `json:"batch_no"`
	Content json.RawMessage `json:"content"`
}

func (in newSubmission) save(r *Repository) (Detail, error) {
	var kind *string
	if len(in.Kind) > 0 {
		var s string
		if string(in.Kind) == "null" || json.Unmarshal(in.Kind, &s) != nil {
			return Detail{}, httpx.BadRequest("非法 inspection_kind")
		}
		kind = &s
	}
	if e := kindAllowed(kind, r); e != nil {
		return Detail{}, e
	}
	if len(in.Content) == 0 {
		return Detail{}, httpx.BadRequest("content 必填")
	}
	return r.Save(SaveInput{GroupID: in.Group, BatchNo: in.Batch, BatchSet: true, Patch: in.Content})
}
func (h HTTP) createSubmission(c *gin.Context) {
	var in newSubmission
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.write(c, raw, 201, func(r *Repository) (Mutation, error) { d, e := in.save(r); return Mutation{Details: []Detail{d}}, e }, func(m Mutation) any { return m.Details[0] })
}
func (h HTTP) batch(c *gin.Context) {
	var in struct {
		Items []newSubmission `json:"items"`
	}
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if len(in.Items) < 1 || len(in.Items) > 100 {
		httpx.Fail(c, httpx.BadRequest("items 必须 1–100 项"))
		return
	}
	h.write(c, raw, 201, func(r *Repository) (Mutation, error) {
		m := Mutation{Details: []Detail{}}
		for i, item := range in.Items {
			d, e := item.save(r)
			if e != nil {
				if he, ok := e.(*httpx.Error); ok {
					det := map[string]any{"index": i}
					b, _ := json.Marshal(he.Details)
					_ = json.Unmarshal(b, &det)
					if det == nil {
						det = map[string]any{}
					}
					det["index"] = i
					return Mutation{}, httpx.NewError(he.Status, he.Code, he.Message, det)
				}
				return Mutation{}, e
			}
			m.Details = append(m.Details, d)
		}
		return m, nil
	}, func(m Mutation) any { return gin.H{"items": m.Details} })
}
func (h HTTP) save(c *gin.Context) {
	var in struct {
		Expected int64                  `json:"expected_version"`
		Patch    json.RawMessage        `json:"patch"`
		Reason   *string                `json:"reason"`
		Group    httpx.Optional[string] `json:"operation_group_id"`
		Batch    httpx.Optional[string] `json:"batch_no"`
	}
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.write(c, raw, 200, func(r *Repository) (Mutation, error) {
		d, e := r.Save(SaveInput{ID: c.Param("id"), ExpectedVersion: in.Expected, GroupID: in.Group.Value, GroupSet: in.Group.Set, BatchNo: in.Batch.Value, BatchSet: in.Batch.Set, Patch: in.Patch, Reason: in.Reason})
		return Mutation{Details: []Detail{d}}, e
	}, func(m Mutation) any { return m.Details[0] })
}

type actionInput struct {
	Expected int64  `json:"expected_version"`
	Reason   string `json:"reason"`
}

func (h HTTP) cancel(c *gin.Context) {
	var in actionInput
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		httpx.Fail(c, httpx.BadRequest("原因必填"))
		return
	}
	h.write(c, raw, 200, func(r *Repository) (Mutation, error) {
		s, e := r.Cancel(c.Param("id"), in.Expected, in.Reason)
		if e != nil {
			return Mutation{}, e
		}
		ds, e := details(r.w.Context, r.w, []Submission{s}, snapshotNow(r.w.Time))
		return Mutation{Details: ds}, e
	}, func(m Mutation) any { return m.Details[0] })
}
func snapshotNow(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
func (h HTTP) groupState(c *gin.Context) {
	var in actionInput
	raw, e := body(c, &in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		httpx.Fail(c, httpx.BadRequest("原因必填"))
		return
	}
	h.write(c, raw, 200, func(r *Repository) (Mutation, error) {
		g, e := r.GroupState(c.Param("id"), in.Expected, strings.HasSuffix(c.Request.URL.Path, "/close"), in.Reason)
		if e != nil {
			return Mutation{}, e
		}
		gd, e := groupDetail(r.w.Context, r.w, g.ID)
		return Mutation{Group: &g, Progress: &gd.Progress}, e
	}, func(m Mutation) any { return GroupDetail{*m.Group, *m.Progress} })
}
func (h HTTP) lot(c *gin.Context) {
	if !query(c) {
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		return readOne[ProductLot](ctx, q, `SELECT * FROM product_lots WHERE id=?`, c.Param("id"))
	})
}
func (h HTTP) lots(c *gin.Context) {
	if !query(c, "page", "page_size", "shared_form_no", "sample_identity", "keyword") {
		return
	}
	page, size, e := httpx.Pagination(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		where := " WHERE 1=1"
		args := []any{}
		for _, key := range []string{"shared_form_no", "sample_identity"} {
			if c.Request.URL.Query().Has(key) {
				where += " AND " + key + "=?"
				args = append(args, strings.TrimSpace(c.Query(key)))
			}
		}
		if c.Request.URL.Query().Has("keyword") {
			where += " AND (shared_form_no LIKE ? ESCAPE '!' OR sample_identity LIKE ? ESCAPE '!' OR sample_raw LIKE ? ESCAPE '!')"
			k := contains(c.Query("keyword"))
			args = append(args, k, k, k)
		}
		var n int
		if e := q.GetContext(ctx, &n, q.Rebind(`SELECT COUNT(*) FROM product_lots`+where), args...); e != nil {
			return nil, e
		}
		out := []ProductLot{}
		args = append(args, size, (page-1)*size)
		e := q.SelectContext(ctx, &out, q.Rebind(`SELECT * FROM product_lots`+where+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`), args...)
		return httpx.NewPage(out, n, page, size), e
	})
}
func (h HTTP) group(c *gin.Context) {
	if !query(c) {
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		return groupDetail(ctx, q, c.Param("id"))
	})
}
func (h HTTP) groups(c *gin.Context) {
	if !query(c, "inspection_kind") {
		return
	}
	kind := c.DefaultQuery("inspection_kind", "ALL")
	if kind != "NORMAL" && kind != "SAMPLING" && kind != "ALL" {
		httpx.Fail(c, httpx.BadRequest("非法 inspection_kind"))
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		if _, e := readOne[ProductLot](ctx, q, `SELECT * FROM product_lots WHERE id=?`, c.Param("id")); e != nil {
			return nil, e
		}
		var rows []struct {
			Group
			Registered int `db:"registered"`
			Active     int `db:"active"`
			Cancelled  int `db:"cancelled"`
			Incomplete int `db:"incomplete"`
		}
		sqlQuery := `SELECT g.*,COALESCE(progress.registered,0) registered,COALESCE(progress.active,0) active,COALESCE(progress.cancelled,0) cancelled,COALESCE(progress.incomplete,0) incomplete FROM operation_groups g LEFT JOIN (SELECT s.operation_group_id,COUNT(*) registered,SUM(CASE WHEN s.state='ACTIVE' THEN 1 ELSE 0 END) active,SUM(CASE WHEN s.state='CANCELLED' THEN 1 ELSE 0 END) cancelled,SUM(CASE WHEN s.state='ACTIVE' AND r.is_complete=0 THEN 1 ELSE 0 END) incomplete FROM inspection_submissions s JOIN submission_revisions r ON r.id=s.current_revision_id WHERE s.operation_group_id IN (SELECT id FROM operation_groups WHERE product_lot_id=?) GROUP BY s.operation_group_id) progress ON progress.operation_group_id=g.id WHERE g.product_lot_id=?`
		args := []any{c.Param("id"), c.Param("id")}
		if kind != "ALL" {
			sqlQuery += ` AND g.inspection_kind=?`
			args = append(args, kind)
		}
		sqlQuery += ` ORDER BY g.created_at DESC,g.id DESC`
		if e := q.SelectContext(ctx, &rows, q.Rebind(sqlQuery), args...); e != nil {
			return nil, e
		}
		items := []GroupDetail{}
		for _, row := range rows {
			items = append(items, GroupDetail{row.Group, Progress{row.Registered, row.Active, row.Cancelled, row.Incomplete}})
		}
		return gin.H{"items": items, "total": len(items)}, nil
	})
}
func (h HTTP) submissions(c *gin.Context) {
	if !query(c, append(FilterNames(false), "page", "page_size")...) {
		return
	}
	f, e := ParseFilter(c.Request.URL.Query(), false)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		return list(ctx, q, f, snapshotTime(s))
	})
}
func (h HTTP) submission(c *gin.Context) {
	if !query(c) {
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		sub, e := readOne[Submission](ctx, q, `SELECT * FROM inspection_submissions WHERE id=?`, c.Param("id"))
		if e != nil {
			return nil, e
		}
		ds, e := details(ctx, q, []Submission{sub}, snapshotTime(s))
		if e != nil {
			return nil, e
		}
		return ds[0], nil
	})
}
func (h HTTP) revisions(c *gin.Context) {
	if !query(c, "page", "page_size") {
		return
	}
	page, size, e := httpx.Pagination(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		if _, e := readOne[Submission](ctx, q, `SELECT * FROM inspection_submissions WHERE id=?`, c.Param("id")); e != nil {
			return nil, e
		}
		var n int
		if e := q.GetContext(ctx, &n, q.Rebind(`SELECT COUNT(*) FROM submission_revisions WHERE submission_id=?`), c.Param("id")); e != nil {
			return nil, e
		}
		rows := []revisionRow{}
		e := q.SelectContext(ctx, &rows, q.Rebind(`SELECT `+revisionColumns+` FROM submission_revisions WHERE submission_id=? ORDER BY revision_no DESC LIMIT ? OFFSET ?`), c.Param("id"), size, (page-1)*size)
		if e != nil {
			return nil, e
		}
		items := []Revision{}
		for _, row := range rows {
			rev, e := row.revision()
			if e != nil {
				return nil, e
			}
			items = append(items, rev)
		}
		if e = attachChangeIDs(ctx, q, items); e != nil {
			return nil, e
		}
		return httpx.NewPage(items, n, page, size), nil
	})
}
func (h HTTP) revision(c *gin.Context) {
	if !query(c) {
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		row, e := readOne[revisionRow](ctx, q, `SELECT `+revisionColumns+` FROM submission_revisions WHERE id=? AND submission_id=?`, c.Param("revision_id"), c.Param("id"))
		if e != nil {
			return nil, e
		}
		rev, e := row.revision()
		if e != nil {
			return nil, e
		}
		output := []Revision{rev}
		if e = attachChangeIDs(ctx, q, output); e != nil {
			return nil, e
		}
		rev = output[0]
		grouped, e := revisionGrouped(ctx, q, rev.ID)
		if e != nil {
			return nil, e
		}
		d, issues, e := Derive(rev.Content, grouped, snapshotNow(rev.SavedAt))
		return gin.H{"revision": rev, "derived": d, "issues": issues}, e
	})
}
func revisionGrouped(ctx context.Context, q store.Reader, id string) (bool, error) {
	var raw string
	e := q.GetContext(ctx, &raw, q.Rebind(`SELECT after_json FROM business_events WHERE action='content_saved' AND entity='submission' AND after_json LIKE ? ORDER BY event_id LIMIT 1`), `%`+id+`%`)
	if e != nil {
		return false, e
	}
	var s Submission
	e = json.Unmarshal([]byte(raw), &s)
	return s.GroupID != nil, e
}
