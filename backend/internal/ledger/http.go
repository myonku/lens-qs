package ledger

import (
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

func (s *Service) Register(r *gin.RouterGroup) {
	for _, path := range []string{"/product-lots", "/product-lots/:id/operation-groups", "/submissions", "/submissions/batch", "/operation-groups/:id/close", "/operation-groups/:id/reopen", "/submissions/:id/publish", "/submissions/:id/cancel"} {
		r.POST(path, s.writeHTTP)
	}
	r.PATCH("/submissions/:id/draft", s.writeHTTP)
	for _, path := range []string{"/product-lots", "/product-lots/:id", "/product-lots/:id/operation-groups", "/operation-groups/:id", "/submissions", "/submissions/:id", "/submissions/:id/revisions", "/submissions/:id/revisions/:revision_id", "/submissions/:id/diff", "/form-fields", "/form-templates", "/form-templates/:operation_id", "/reports/product-lots/:id", "/stats/summary", "/stats/defects", "/stats/quantity-changes"} {
		r.GET(path, s.readHTTP)
	}
}
func (s *Service) writeHTTP(c *gin.Context) {
	if len(c.Request.URL.Query()) > 0 {
		httpx.Fail(c, httpx.BadRequest("写接口不接受查询参数"))
		return
	}
	body, e := io.ReadAll(io.LimitReader(c.Request.Body, 4*1024*1024+1))
	if e != nil || len(body) > 4*1024*1024 {
		httpx.Fail(c, httpx.BadRequest("请求体读取失败或超过 4 MiB"))
		return
	}
	req, e := decode(body)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	path := strings.TrimPrefix(c.FullPath(), "/api/v2")
	key := c.Param("id")
	result, status, replay, e := s.Write(c.Request.Context(), middleware.CurrentUserID(c), c.GetHeader("X-Idempotency-Key"), fingerprint(c.Request.Method, c.Request.URL.EscapedPath()+"?"+c.Request.URL.Query().Encode(), body), func(u *unit) (any, int, error) {
		var data any
		var err error
		status := 200
		switch path {
		case "/product-lots":
			data, err = u.createLot(req)
			status = 201
		case "/product-lots/:id/operation-groups":
			data, err = u.createGroup(key, req)
			status = 201
		case "/operation-groups/:id/close":
			data, err = u.groupAction(key, "close", req)
		case "/operation-groups/:id/reopen":
			data, err = u.groupAction(key, "reopen", req)
		case "/submissions":
			data, err = u.createSubmission(req)
			status = 201
		case "/submissions/:id/draft":
			data, err = u.saveDraft(key, req)
		case "/submissions/:id/publish":
			data, err = u.submissionAction(key, "publish", req)
		case "/submissions/:id/cancel":
			data, err = u.submissionAction(key, "cancel", req)
		case "/submissions/batch":
			if err = allowed(req, "items"); err != nil {
				break
			}
			items, ok := req["items"].([]any)
			if !ok || len(items) < 1 || len(items) > 100 {
				err = httpx.BadRequest("items 需要 1–100 项")
				break
			}
			out := []any{}
			for i, v := range items {
				var d any
				if obj(v) == nil {
					err = formatErr("items")
				} else {
					d, err = u.createSubmission(obj(v))
				}
				if err != nil {
					if he, ok := err.(*httpx.Error); ok {
						details := obj(he.Details)
						if details == nil {
							details = Object{}
						}
						details["index"] = i
						he.Details = details
					}
					break
				}
				out = append(out, d)
			}
			data = Object{"items": out}
			status = 201
		default:
			err = httpx.NotFound("")
		}
		return data, status, err
	})
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if replay {
		c.Header("X-Idempotent-Replay", "true")
	}
	c.Data(status, "application/json; charset=utf-8", result)
}
func (s *Service) readHTTP(c *gin.Context) {
	q := c.Request.URL.Query()
	path := strings.TrimPrefix(c.FullPath(), "/api/v2")
	key := c.Param("id")
	data, e := s.read(c.Request.Context(), func(u *unit) (any, error) {
		switch path {
		case "/product-lots":
			if e := queryAllowed(q, "page", "page_size", "shared_form_no", "sample_identity", "keyword"); e != nil {
				return nil, e
			}
			items := []Object{}
			for _, m := range u.rows("SELECT * FROM product_lots ORDER BY created_at,id") {
				if q.Has("shared_form_no") && m["shared_form_no"] != strings.TrimSpace(q.Get("shared_form_no")) {
					continue
				}
				if q.Has("sample_identity") && m["sample_identity_key"] != strings.TrimSpace(q.Get("sample_identity")) {
					continue
				}
				if q.Has("keyword") && !strings.Contains(str(m["shared_form_no"])+" "+str(m["sample_identity"]), q.Get("keyword")) {
					continue
				}
				items = append(items, m)
			}
			return page(items, q)
		case "/product-lots/:id":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			return u.must("product_lots", key)
		case "/product-lots/:id/operation-groups":
			if e := queryAllowed(q, "inspection_kind", "page", "page_size"); e != nil {
				return nil, e
			}
			if _, e := u.must("product_lots", key); e != nil {
				return nil, e
			}
			if q.Has("inspection_kind") && !contains([]string{"NORMAL", "SAMPLING"}, q.Get("inspection_kind")) {
				return nil, formatErr("inspection_kind")
			}
			items := []Object{}
			for _, g := range u.rows("SELECT * FROM operation_groups WHERE product_lot_id = ? ORDER BY operation_code,inspection_kind,id", key) {
				if q.Has("inspection_kind") && g["inspection_kind"] != q.Get("inspection_kind") {
					continue
				}
				items = append(items, u.groupView(g))
			}
			return page(items, q)
		case "/operation-groups/:id":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			g, e := u.must("operation_groups", key)
			if e != nil {
				return nil, e
			}
			return u.groupView(g), nil
		case "/submissions":
			if e := queryAllowed(q, append(append([]string{}, submissionFilters...), "page", "page_size")...); e != nil {
				return nil, e
			}
			fs, _, e := u.facts(q, false)
			if e != nil {
				return nil, e
			}
			items := []Object{}
			for _, f := range fs {
				d := u.detail(f.sub)
				for k, v := range derived(f.content, f.sub["operation_group_id"] != nil) {
					d[k] = v
				}
				d["listing_revision_id"] = f.revisionID
				items = append(items, d)
			}
			return page(items, q)
		case "/submissions/:id":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			sub, e := u.must("inspection_submissions", key)
			if e != nil {
				return nil, e
			}
			return u.detail(sub), nil
		case "/submissions/:id/revisions":
			if e := queryAllowed(q, "page", "page_size"); e != nil {
				return nil, e
			}
			if _, e := u.must("inspection_submissions", key); e != nil {
				return nil, e
			}
			items := []Object{}
			for _, r := range u.rows("SELECT id FROM submission_revisions WHERE submission_id = ? ORDER BY revision_no DESC,id", key) {
				items = append(items, obj(u.revisionView(r["id"])))
			}
			return page(items, q)
		case "/submissions/:id/revisions/:revision_id":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			r, e := u.must("submission_revisions", c.Param("revision_id"))
			if e != nil {
				return nil, e
			}
			if r["submission_id"] != key {
				return nil, httpx.NotFound("")
			}
			return u.revisionView(r["id"]), nil
		case "/submissions/:id/diff":
			if e := queryAllowed(q, "from", "to"); e != nil {
				return nil, e
			}
			rs := []Object{}
			for _, name := range []string{"from", "to"} {
				if q.Get(name) == "" {
					return nil, httpx.BadRequest("from/to 必须为明确版本 ID")
				}
				r, e := u.must("submission_revisions", q.Get(name))
				if e != nil {
					return nil, e
				}
				if r["submission_id"] != key {
					return nil, httpx.NotFound("")
				}
				m, e := decode([]byte(str(r["content"])))
				if e != nil {
					return nil, e
				}
				rs = append(rs, m)
			}
			out := []Object{}
			diff(rs[0], rs[1], "", &out)
			sub, e := u.must("inspection_submissions", key)
			if e != nil {
				return nil, e
			}
			return Object{"from": q.Get("from"), "to": q.Get("to"), "inspection_kind": sub["inspection_kind"], "changes": out}, nil
		case "/form-fields":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			return fieldCatalog(), nil
		case "/form-templates":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			items := []Object{}
			for _, op := range u.rows("SELECT id,code,label FROM dict_items WHERE category IN ('inspection_operation','process') AND active = 1 ORDER BY sort_no,code,id") {
				items = append(items, Object{"operation": op, "template_version": "v2.1", "fields": fieldCatalog()})
			}
			return Object{"items": items, "total": len(items)}, nil
		case "/form-templates/:operation_id":
			if e := queryAllowed(q); e != nil {
				return nil, e
			}
			op, e := u.must("dict_items", c.Param("operation_id"))
			if e != nil {
				return nil, e
			}
			if !contains([]string{"inspection_operation", "process"}, str(op["category"])) {
				return nil, httpx.NotFound("")
			}
			return Object{"operation": op, "template_version": "v2.1", "fields": fieldCatalog()}, nil
		case "/reports/product-lots/:id":
			return u.report(key, q)
		case "/stats/summary", "/stats/defects", "/stats/quantity-changes":
			return u.stats(strings.TrimPrefix(path, "/stats/"), q)
		}
		return nil, httpx.NotFound("")
	})
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, data)
}
func (u *unit) groupView(g Object) Object {
	m := clone(g)
	subs := u.rows("SELECT state,current_revision_id,draft_revision_id FROM inspection_submissions WHERE operation_group_id = ?", g["id"])
	var registered, published, initial, revision, cancelled int
	for _, s := range subs {
		if s["state"] == "CANCELLED" {
			cancelled++
			continue
		}
		registered++
		if s["current_revision_id"] == nil {
			initial++
		} else {
			published++
			if s["draft_revision_id"] != nil {
				revision++
			}
		}
	}
	m["registered_count"] = registered
	m["published_count"] = published
	m["initial_draft_count"] = initial
	m["revision_draft_count"] = revision
	m["cancelled_count"] = cancelled
	return m
}
func fieldCatalog() Object {
	fields := []Object{}
	for _, name := range textFields {
		kind := "text"
		if strings.HasSuffix(name, "_date") {
			kind = "date"
		}
		if strings.HasSuffix(name, "_at") {
			kind = "instant"
		}
		fields = append(fields, Object{"name": name, "type": kind, "nullable": true, "read_only": false})
	}
	for _, p := range []string{"counts", "declared_counts"} {
		for _, name := range countFields {
			fields = append(fields, Object{"name": p + "." + name, "type": "integer", "nullable": true, "minimum": 0, "read_only": false})
		}
	}
	for _, name := range []string{"pass_rate", "line_totals", "declared_differences", "is_complete", "missing_fields", "paper_inspected_day", "paper_submitted_day"} {
		fields = append(fields, Object{"name": name, "read_only": true})
	}
	return Object{"template_version": "v2.1", "fields": fields, "defects": formfields.DefectGroups, "surfaces": formfields.Surfaces, "surface_labels": formfields.SurfaceLabels, "lines": Object{"replacement": "array", "nullable": false}, "quantity_changes": Object{"replacement": "array", "kinds": []string{"LOSS", "ADJUSTMENT", "OTHER"}, "directions": []string{"DECREASE", "INCREASE", "UNKNOWN"}, "quantity_nullable": true}, "date_format": "YYYY-MM-DD", "instant_format": "YYYY-MM-DDTHH:mm:ssZ", "business_timezone": "Asia/Shanghai", "inspection_kinds": []string{"NORMAL", "SAMPLING"}, "sampling_loss_validation": "caller", "unknown_is_zero": false}
}
