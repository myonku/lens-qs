package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

// Filter is shared by lists, statistics and range reports. SQL expressions are
// selected exclusively from this whitelist, never from user supplied SQL.
var filterColumns = map[string]string{
	"product_lot_id": "g.product_lot_id", "operation_group_id": "s.operation_group_id", "operation_id": "g.operation_id",
	"inspection_kind": "s.inspection_kind", "state": "s.state", "scope": "s.scope", "batch_no": "s.batch_no",
	"machine_no": "r.machine_no", "paper_inspector": "r.paper_inspector", "paper_submitter": "r.paper_submitter",
	"paper_machine_operator": "r.paper_machine_operator", "paper_handover_by": "r.paper_handover_by",
	"sample_project": "r.sample_project", "sample_model": "r.sample_model", "sample_spec": "r.sample_spec", "sample_size": "r.sample_size", "sample_side": "r.sample_side",
	"created_by": "s.created_by", "last_saved_by": "s.last_saved_by",
}

const joined = ` FROM inspection_submissions s JOIN submission_revisions r ON r.id=s.current_revision_id AND r.submission_id=s.id LEFT JOIN operation_groups g ON g.id=s.operation_group_id LEFT JOIN product_lots p ON p.id=g.product_lot_id`

type Filter struct {
	Values      url.Values
	Kind, Basis string
	Page, Size  int
	Stats       bool
}

func ParseFilter(v url.Values, stats bool) (Filter, error) {
	f := Filter{Values: v, Kind: "ALL", Basis: "inspection_day", Page: 1, Size: 20, Stats: stats}
	if stats {
		f.Kind = "NORMAL"
	}
	if v.Has("inspection_kind") {
		f.Kind = v.Get("inspection_kind")
	}
	if f.Kind != "NORMAL" && f.Kind != "SAMPLING" && f.Kind != "ALL" {
		return f, httpx.BadRequest("非法 inspection_kind")
	}
	if v.Has("date_basis") {
		f.Basis = v.Get("date_basis")
	}
	if f.Basis != "inspection_day" && f.Basis != "created_at" {
		return f, httpx.BadRequest("非法 date_basis")
	}
	for key, dst := range map[string]*int{"page": &f.Page, "page_size": &f.Size} {
		if v.Has(key) {
			n, e := strconv.Atoi(v.Get(key))
			if e != nil || n < 1 || n > 100000000 || (key == "page_size" && n > 100) {
				return f, httpx.BadRequest("非法分页", map[string]string{"parameter": key})
			}
			*dst = n
		}
	}
	for _, key := range []string{"unassigned", "incomplete", "sample_project_missing"} {
		if v.Has(key) && v.Get(key) != "true" && v.Get(key) != "false" {
			return f, httpx.BadRequest("布尔参数只能 true/false")
		}
	}
	if v.Has("state") && v.Get("state") != "ACTIVE" && v.Get("state") != "CANCELLED" {
		return f, httpx.BadRequest("非法 state")
	}
	if v.Has("scope") && v.Get("scope") != "WHOLE" && v.Get("scope") != "SPLIT" {
		return f, httpx.BadRequest("非法 scope")
	}
	if v.Has("paper_unit_text") && v.Get("paper_unit_text") != "片" {
		return f, invalid("invalid_measurement_unit", "paper_unit_text")
	}
	for _, key := range []string{"from", "to"} {
		if v.Has(key) {
			value := v.Get(key)
			var e error
			if f.Basis == "inspection_day" {
				d := &value
				e = eventDate(&d, new(*string), key)
			} else {
				a := &value
				e = eventDate(new(*string), &a, key)
				if e == nil {
					v.Set(key, *a)
				}
			}
			if e != nil {
				return f, e
			}
		}
	}
	if v.Has("from") && v.Has("to") && v.Get("from") > v.Get("to") {
		return f, invalid("invalid_date_range", "from")
	}
	return f, nil
}
func FilterNames(stats bool) []string {
	out := []string{"keyword", "paper_unit_text", "date_basis", "from", "to"}
	for key := range filterColumns {
		if !stats || key != "state" {
			out = append(out, key)
		}
	}
	if !stats {
		out = append(out, "unassigned", "incomplete", "sample_project_missing")
	}
	return out
}
func (f Filter) where(ignoreTime bool) (string, []any) {
	parts := []string{"1=1"}
	args := []any{}
	if f.Stats {
		parts = append(parts, "s.state='ACTIVE' AND s.operation_group_id IS NOT NULL")
	}
	for key, col := range filterColumns {
		if key == "inspection_kind" {
			continue
		}
		if f.Values.Has(key) {
			parts = append(parts, col+"=?")
			args = append(args, f.Values.Get(key))
		}
	}
	if f.Kind != "ALL" {
		parts = append(parts, "s.inspection_kind=?")
		args = append(args, f.Kind)
	}
	if f.Values.Has("paper_unit_text") {
		parts = append(parts, `r.paper_unit_text='片'`)
	}
	if f.Values.Has("keyword") {
		parts = append(parts, "(r.sample_raw LIKE ? ESCAPE '!' OR p.shared_form_no LIKE ? ESCAPE '!' OR p.sample_identity LIKE ? ESCAPE '!')")
		k := contains(f.Values.Get("keyword"))
		args = append(args, k, k, k)
	}
	if f.Values.Has("unassigned") {
		op := "IS NOT NULL"
		if f.Values.Get("unassigned") == "true" {
			op = "IS NULL"
		}
		parts = append(parts, "s.operation_group_id "+op)
	}
	if f.Values.Has("incomplete") {
		n := 1
		if f.Values.Get("incomplete") == "true" {
			n = 0
		}
		parts = append(parts, "r.is_complete=?")
		args = append(args, n)
	}
	if f.Values.Has("sample_project_missing") {
		// Match strings.TrimSpace's Unicode whitespace semantics in both SQL
		// dialects without relying on their different TRIM character sets.
		value := "r.sample_project"
		for _, space := range "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000" {
			value = "REPLACE(" + value + ",?,'')"
			args = append(args, string(space))
		}
		p := "(r.sample_project IS NULL OR " + value + "='')"
		if f.Values.Get("sample_project_missing") == "false" {
			p = "NOT " + p
		}
		parts = append(parts, p)
	}
	if !ignoreTime {
		col := "r.paper_inspected_day"
		if f.Basis == "created_at" {
			col = "s.created_at"
		}
		if f.Values.Has("from") {
			parts = append(parts, col+">=?")
			args = append(args, f.Values.Get("from"))
		}
		if f.Values.Has("to") {
			op := "<="
			if f.Basis == "created_at" {
				op = "<"
			}
			parts = append(parts, col+op+"?")
			args = append(args, f.Values.Get("to"))
		}
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}
func contains(s string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s) + "%"
}
func readOne[T any](ctx context.Context, q store.Reader, query string, args ...any) (T, error) {
	var t T
	e := q.GetContext(ctx, &t, q.Rebind(query), args...)
	if e == sql.ErrNoRows {
		e = httpx.NotFound("资源不存在")
	}
	return t, e
}

type revisionRow struct {
	ID           string  `db:"id"`
	SubmissionID string  `db:"submission_id"`
	No           int64   `db:"revision_no"`
	BaseID       *string `db:"base_revision_id"`
	Template     string  `db:"template_version"`
	Schema       string  `db:"content_schema_version"`
	Content      string  `db:"content_json"`
	SavedBy      string  `db:"saved_by"`
	SavedName    string  `db:"saved_actor_name"`
	SavedAt      string  `db:"saved_at"`
	Reason       *string `db:"reason"`
}

const revisionColumns = `id,submission_id,revision_no,base_revision_id,template_version,content_schema_version,content_json,saved_by,saved_actor_name,saved_at,reason`

func (r revisionRow) revision() (Revision, error) {
	out := Revision{ID: r.ID, SubmissionID: r.SubmissionID, No: r.No, BaseID: r.BaseID, Template: r.Template, Schema: r.Schema, SavedBy: r.SavedBy, SavedName: r.SavedName, SavedAt: r.SavedAt, Reason: r.Reason}
	e := json.Unmarshal([]byte(r.Content), &out.Content)
	return out, e
}
func details(ctx context.Context, q store.Reader, subs []Submission, at time.Time) ([]Detail, error) {
	out := make([]Detail, 0, len(subs))
	if len(subs) > 500 {
		for start := 0; start < len(subs); start += 500 {
			end := start + 500
			if end > len(subs) {
				end = len(subs)
			}
			ds, e := details(ctx, q, subs[start:end], at)
			if e != nil {
				return nil, e
			}
			out = append(out, ds...)
		}
		return out, nil
	}
	if len(subs) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(subs))
	for _, s := range subs {
		ids = append(ids, s.CurrentRevisionID)
	}
	sqlQuery, args, e := sqlx.In(`SELECT `+revisionColumns+` FROM submission_revisions WHERE id IN (?)`, ids)
	if e != nil {
		return nil, e
	}
	var rows []revisionRow
	if e = q.SelectContext(ctx, &rows, q.Rebind(sqlQuery), args...); e != nil {
		return nil, e
	}
	versions := map[string]Revision{}
	decoded := make([]Revision, 0, len(rows))
	for _, r := range rows {
		rev, e := r.revision()
		if e != nil {
			return nil, e
		}
		if rev.Schema != "2" {
			return nil, fmt.Errorf("unsupported saved schema %s", rev.Schema)
		}
		decoded = append(decoded, rev)
	}
	if e = attachChangeIDs(ctx, q, decoded); e != nil {
		return nil, e
	}
	for _, rev := range decoded {
		versions[rev.ID] = rev
	}
	for _, s := range subs {
		rev, ok := versions[s.CurrentRevisionID]
		if !ok || rev.SubmissionID != s.ID {
			return nil, fmt.Errorf("invalid revision ownership")
		}
		d, issues, e := Derive(rev.Content, s.GroupID != nil, at)
		if e != nil {
			return nil, e
		}
		out = append(out, Detail{s, rev, d, issues})
	}
	return out, nil
}

type Progress struct {
	Registered int `json:"registered_count"`
	Active     int `json:"active_count"`
	Cancelled  int `json:"cancelled_count"`
	Incomplete int `json:"incomplete_count"`
}
type GroupDetail struct {
	Group    Group    `json:"group"`
	Progress Progress `json:"progress"`
}

func groupDetail(ctx context.Context, q store.Reader, id string) (GroupDetail, error) {
	g, e := readOne[Group](ctx, q, `SELECT * FROM operation_groups WHERE id=?`, id)
	if e != nil {
		return GroupDetail{}, e
	}
	var p struct {
		Registered int `db:"registered"`
		Active     int `db:"active"`
		Cancelled  int `db:"cancelled"`
		Incomplete int `db:"incomplete"`
	}
	e = q.GetContext(ctx, &p, q.Rebind(`SELECT COUNT(*) registered,COALESCE(SUM(CASE WHEN s.state='ACTIVE' THEN 1 ELSE 0 END),0) active,COALESCE(SUM(CASE WHEN s.state='CANCELLED' THEN 1 ELSE 0 END),0) cancelled,COALESCE(SUM(CASE WHEN s.state='ACTIVE' AND r.is_complete=0 THEN 1 ELSE 0 END),0) incomplete`+joined+` WHERE s.operation_group_id=?`), id)
	return GroupDetail{g, Progress{p.Registered, p.Active, p.Cancelled, p.Incomplete}}, e
}

type ListResult struct {
	Items   []Detail `json:"items"`
	Total   int      `json:"total"`
	Page    int      `json:"page"`
	Size    int      `json:"page_size"`
	Unknown int      `json:"unknown_day_count"`
}

func list(ctx context.Context, q store.Reader, f Filter, at time.Time) (ListResult, error) {
	out := ListResult{Page: f.Page, Size: f.Size}
	where, args := f.where(false)
	if e := q.GetContext(ctx, &out.Total, q.Rebind(`SELECT COUNT(*)`+joined+where), args...); e != nil {
		return out, e
	}
	unknown, ua := f.where(true)
	if e := q.GetContext(ctx, &out.Unknown, q.Rebind(`SELECT COUNT(*)`+joined+unknown+` AND r.paper_inspected_day IS NULL`), ua...); e != nil {
		return out, e
	}
	var subs []Submission
	args = append(args, f.Size, (f.Page-1)*f.Size)
	if e := q.SelectContext(ctx, &subs, q.Rebind(`SELECT s.*`+joined+where+` ORDER BY s.created_at DESC,s.id DESC LIMIT ? OFFSET ?`), args...); e != nil {
		return out, e
	}
	var e error
	out.Items, e = details(ctx, q, subs, at)
	return out, e
}
