package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

var dimensions = []string{"product_lot_id", "shared_form_no", "sample_identity", "operation_id", "inspection_kind", "batch_no", "scope", "machine_no", "paper_unit_text", "paper_inspector", "paper_submitter", "paper_machine_operator", "paper_handover_by", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side", "sample_raw", "created_by", "last_saved_by", "inspection_day", "inspection_month"}

func dimensionSQL(name string) string {
	switch name {
	case "shared_form_no":
		return "p.shared_form_no"
	case "sample_identity":
		return "p.sample_identity"
	case "sample_raw":
		return "r.sample_raw"
	case "inspection_day":
		return "r.paper_inspected_day"
	case "inspection_month":
		return "SUBSTR(r.paper_inspected_day,1,7)"
	case "paper_unit_text":
		return "r.paper_unit_text"
	}
	return filterColumns[name]
}

type dimensionValues struct {
	D0  *string `db:"d0"`
	D1  *string `db:"d1"`
	D2  *string `db:"d2"`
	D3  *string `db:"d3"`
	D4  *string `db:"d4"`
	D5  *string `db:"d5"`
	D6  *string `db:"d6"`
	D7  *string `db:"d7"`
	D8  *string `db:"d8"`
	D9  *string `db:"d9"`
	D10 *string `db:"d10"`
	D11 *string `db:"d11"`
	D12 *string `db:"d12"`
	D13 *string `db:"d13"`
	D14 *string `db:"d14"`
	D15 *string `db:"d15"`
	D16 *string `db:"d16"`
	D17 *string `db:"d17"`
	D18 *string `db:"d18"`
	D19 *string `db:"d19"`
	D20 *string `db:"d20"`
	D21 *string `db:"d21"`
	D22 *string `db:"d22"`
}
type statRow struct {
	dimensionValues
	ID         string `db:"id"`
	RevisionID string `db:"revision_id"`
	Kind       string `db:"inspection_kind"`
	Total      *int64 `db:"total_qty"`
	Qualified  *int64 `db:"qualified_qty"`
	Defect     *int64 `db:"defect_qty"`
}
type CountsResult struct {
	Total     CountUnit `json:"total_qty"`
	Qualified CountUnit `json:"qualified_qty"`
	Defect    CountUnit `json:"defect_qty"`
}
type Aggregate struct {
	Key    map[string]*string `json:"key"`
	Count  int                `json:"submission_count"`
	Counts CountsResult       `json:"counts"`
	Rate   *float64           `json:"pass_rate"`
	Reason *string            `json:"pass_rate_reason"`
}
type statisticMeta struct {
	AsOf     string   `json:"as_of"`
	Through  int64    `json:"through_event_id"`
	Included []string `json:"included_revision_ids"`
	Unknown  int      `json:"unknown_day_count"`
	Notes    []string `json:"notes"`
}
type SummaryResult struct {
	statisticMeta
	GroupBy     []string             `json:"group_by"`
	Groups      []Aggregate          `json:"groups"`
	TotalGroups int                  `json:"total_groups"`
	Page        int                  `json:"page"`
	Size        int                  `json:"page_size"`
	Total       *Aggregate           `json:"total,omitempty"`
	Totals      map[string]Aggregate `json:"totals,omitempty"`
}

func groupBy(value string, all bool) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	if value != "" {
		for _, name := range strings.Split(value, ",") {
			if dimensionSQL(name) == "" || seen[name] {
				return nil, httpx.BadRequest("非法或重复维度", gin.H{"supported": dimensions})
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	if all && !seen["inspection_kind"] {
		out = append(out, "inspection_kind")
	}
	return out, nil
}
func (h HTTP) dimensions(c *gin.Context) {
	if !query(c) {
		return
	}
	items := []gin.H{}
	labels := map[string]string{"product_lot_id": "生产批ID", "shared_form_no": "共享表单号", "sample_identity": "完整型号", "operation_id": "工序", "inspection_kind": "检验类型", "batch_no": "检验批号", "scope": "整批/分批", "machine_no": "机台", "paper_unit_text": "计量单位", "paper_inspector": "纸面检验人", "paper_submitter": "送检人", "paper_machine_operator": "操机员", "paper_handover_by": "交接员", "sample_project": "项目", "sample_model": "型号", "sample_spec": "规格", "sample_size": "大小", "sample_side": "左右", "sample_raw": "样品原文", "created_by": "创建账号", "last_saved_by": "最近保存账号", "inspection_day": "检验业务日", "inspection_month": "检验业务月"}
	for _, name := range dimensions {
		source := "content." + name
		switch name {
		case "product_lot_id", "shared_form_no", "sample_identity":
			source = "product_lot." + name
		case "operation_id":
			source = "operation_group.operation_id"
		case "inspection_kind", "batch_no", "scope", "created_by", "last_saved_by":
			source = "submission." + name
		case "inspection_day", "inspection_month":
			source = "derived." + name
		}
		items = append(items, gin.H{"code": name, "label": labels[name], "source": source})
	}
	httpx.OK(c, gin.H{"dimensions": items})
}
func statNames(extra ...string) []string { return append(FilterNames(true), extra...) }
func checkStatGroup(ctx context.Context, q store.Reader, f Filter) error {
	if id := f.Values.Get("operation_group_id"); id != "" && f.Values.Has("inspection_kind") && f.Kind != "ALL" {
		g, e := readOne[Group](ctx, q, `SELECT * FROM operation_groups WHERE id=?`, id)
		if e != nil {
			return e
		}
		if g.Kind != f.Kind {
			return invalid("inspection_kind_mismatch", "inspection_kind")
		}
	}
	return nil
}
func selectStats(ctx context.Context, q store.Reader, f Filter, by []string) ([]statRow, error) {
	cols := []string{"s.id", "r.id AS revision_id", "s.inspection_kind", "r.total_qty", "r.qualified_qty", "r.defect_qty"}
	for i, name := range by {
		cols = append(cols, fmt.Sprintf("%s AS d%d", dimensionSQL(name), i))
	}
	where, args := f.where(false)
	rows := []statRow{}
	e := q.SelectContext(ctx, &rows, q.Rebind(`SELECT `+strings.Join(cols, ",")+joined+where+` ORDER BY s.id`), args...)
	return rows, e
}
func aggregateRows(rows []statRow, key map[string]*string) (Aggregate, error) {
	contents := make([]Content, len(rows))
	for i, r := range rows {
		contents[i].Counts = Counts{r.Total, r.Qualified, r.Defect}
	}
	s, e := AggregateCounts(contents)
	return Aggregate{key, len(rows), CountsResult{s.Total, s.Qualified, s.Defect}, s.PassRate, s.Reason}, e
}
func meta(ctx context.Context, q store.Reader, f Filter, s store.Snapshot, rows []statRow) (statisticMeta, error) {
	m := statisticMeta{AsOf: s.AsofTime, Through: s.AsofEventID, Included: []string{}, Notes: []string{"检验工作量，可能重复计入同件产品"}}
	for _, r := range rows {
		m.Included = append(m.Included, r.RevisionID)
	}
	sort.Strings(m.Included)
	where, args := f.where(true)
	e := q.GetContext(ctx, &m.Unknown, q.Rebind(`SELECT COUNT(*)`+joined+where+` AND r.paper_inspected_day IS NULL`), args...)
	return m, e
}
func summary(ctx context.Context, q store.Reader, f Filter, s store.Snapshot, by []string) (SummaryResult, error) {
	out := SummaryResult{GroupBy: by, Groups: []Aggregate{}, Page: f.Page, Size: f.Size}
	if e := checkStatGroup(ctx, q, f); e != nil {
		return out, e
	}
	rows, e := selectStats(ctx, q, f, by)
	if e != nil {
		return out, e
	}
	out.statisticMeta, e = meta(ctx, q, f, s, rows)
	if e != nil {
		return out, e
	}
	type bucket struct {
		key  map[string]*string
		rows []statRow
	}
	buckets := map[string]*bucket{}
	partitions := map[string][]statRow{"NORMAL": {}, "SAMPLING": {}}
	for _, r := range rows {
		partitions[r.Kind] = append(partitions[r.Kind], r)
		if len(by) == 0 {
			continue
		}
		key := map[string]*string{}
		v := reflect.ValueOf(r.dimensionValues)
		for i, name := range by {
			key[name] = v.Field(i).Interface().(*string)
		}
		raw, _ := json.Marshal(key)
		id := string(raw)
		if buckets[id] == nil {
			buckets[id] = &bucket{key: key}
		}
		buckets[id].rows = append(buckets[id].rows, r)
	}
	keys := []string{}
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := buckets[keys[i]].key, buckets[keys[j]].key
		for _, name := range by {
			av, bv := a[name], b[name]
			if av == nil && bv != nil {
				return false
			}
			if av != nil && bv == nil {
				return true
			}
			if av != nil && bv != nil && *av != *bv {
				return *av < *bv
			}
		}
		return keys[i] < keys[j]
	})
	out.TotalGroups = len(keys)
	start := (f.Page - 1) * f.Size
	end := start + f.Size
	if end > len(keys) {
		end = len(keys)
	}
	for i := start; i < end; i++ {
		b := buckets[keys[i]]
		a, e := aggregateRows(b.rows, b.key)
		if e != nil {
			return out, e
		}
		out.Groups = append(out.Groups, a)
	}
	if f.Kind == "ALL" {
		out.Totals = map[string]Aggregate{}
		for _, kind := range []string{"NORMAL", "SAMPLING"} {
			a, e := aggregateRows(partitions[kind], map[string]*string{"inspection_kind": &kind})
			if e != nil {
				return out, e
			}
			out.Totals[kind] = a
		}
	} else {
		a, e := aggregateRows(rows, map[string]*string{})
		if e != nil {
			return out, e
		}
		out.Total = &a
	}
	return out, nil
}
func (h HTTP) summary(c *gin.Context) {
	if !query(c, statNames("group_by", "page", "page_size")...) {
		return
	}
	f, e := ParseFilter(c.Request.URL.Query(), true)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	by, e := groupBy(c.Query("group_by"), f.Kind == "ALL")
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		return summary(ctx, q, f, s, by)
	})
}

type DefectResult struct {
	Code     string               `json:"code"`
	Label    string               `json:"label"`
	Surfaces map[string]CountUnit `json:"surfaces"`
}

func defectStats(ctx context.Context, q store.Reader, f Filter, s store.Snapshot) (gin.H, error) {
	rows, e := selectStats(ctx, q, f, nil)
	if e != nil {
		return nil, e
	}
	m, e := meta(ctx, q, f, s, rows)
	if e != nil {
		return nil, e
	}
	where, args := f.where(false)
	out := []DefectResult{}
	inner, outer := []string{}, []string{}
	for i, d := range formfields.DefectGroups {
		for j, surface := range formfields.Surfaces {
			n := i*3 + j
			col := d.Code + "_" + surface
			inner = append(inner, fmt.Sprintf("COALESCE(SUM(l.%s),0) c%d,CASE WHEN COUNT(l.seq)=0 OR COUNT(l.%s)<COUNT(l.seq) THEN 1 ELSE 0 END u%d", col, n, col, n))
			outer = append(outer, fmt.Sprintf("CAST(COALESCE(SUM(c%d),0) AS TEXT),COALESCE(SUM(u%d),0)", n, n))
		}
	}
	result, e := q.QueryxContext(ctx, q.Rebind(`SELECT `+strings.Join(outer, ",")+` FROM (SELECT `+strings.Join(inner, ",")+joined+` LEFT JOIN revision_lines l ON l.revision_id=r.id`+where+` GROUP BY s.id) cell_totals`), args...)
	if e != nil {
		if strings.Contains(strings.ToLower(e.Error()), "overflow") {
			return nil, invalid("quantity_overflow", "defects")
		}
		return nil, e
	}
	defer result.Close()
	known := make([]string, 105)
	unknown := make([]int, 105)
	dest := make([]any, 0, 210)
	for i := range known {
		dest = append(dest, &known[i], &unknown[i])
	}
	if !result.Next() {
		if e = result.Err(); e == nil {
			e = fmt.Errorf("missing defect aggregate")
		}
		if strings.Contains(strings.ToLower(e.Error()), "overflow") {
			return nil, invalid("quantity_overflow", "defects")
		}
		return nil, e
	}
	if e = result.Scan(dest...); e != nil {
		return nil, e
	}
	result.Close()
	index := 0
	for _, d := range formfields.DefectGroups {
		item := DefectResult{Code: d.Code, Label: d.Label, Surfaces: map[string]CountUnit{}}
		for _, surface := range formfields.Surfaces {
			n, e := strconv.ParseInt(known[index], 10, 64)
			if e != nil || n < 0 || n > MaxQuantity {
				return nil, invalid("quantity_overflow", "defects")
			}
			u := CountUnit{KnownSum: n, UnknownCount: unknown[index]}
			index++
			u.Complete = u.UnknownCount == 0
			if u.Complete {
				v := u.KnownSum
				u.Value = &v
			}
			item.Surfaces[surface] = u
		}
		out = append(out, item)
	}
	return gin.H{"defects": out, "as_of": m.AsOf, "through_event_id": m.Through, "included_revision_ids": m.Included, "unknown_day_count": m.Unknown, "notes": m.Notes}, nil
}
func (h HTTP) defects(c *gin.Context) {
	if !query(c, statNames()...) {
		return
	}
	f, e := ParseFilter(c.Request.URL.Query(), true)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		if e := checkStatGroup(ctx, q, f); e != nil {
			return nil, e
		}
		return partitionStats(ctx, q, f, s, defectStats)
	})
}
func partitionStats(ctx context.Context, q store.Reader, f Filter, s store.Snapshot, fn func(context.Context, store.Reader, Filter, store.Snapshot) (gin.H, error)) (any, error) {
	if f.Kind != "ALL" {
		return fn(ctx, q, f, s)
	}
	parts := []gin.H{}
	for _, kind := range []string{"NORMAL", "SAMPLING"} {
		part := f
		part.Kind = kind
		r, e := fn(ctx, q, part, s)
		if e != nil {
			return nil, e
		}
		r["inspection_kind"] = kind
		parts = append(parts, r)
	}
	return gin.H{"partitions": parts, "as_of": s.AsofTime, "through_event_id": s.AsofEventID}, nil
}

type ChangeAggregate struct {
	Kind      string    `json:"kind"`
	Direction string    `json:"direction"`
	Count     int       `json:"item_count"`
	Quantity  CountUnit `json:"quantity"`
}

func changeStats(ctx context.Context, q store.Reader, f Filter, s store.Snapshot) (gin.H, error) {
	rows, e := selectStats(ctx, q, f, nil)
	if e != nil {
		return nil, e
	}
	m, e := meta(ctx, q, f, s, rows)
	if e != nil {
		return nil, e
	}
	where, args := f.where(false)
	var changes []struct {
		Kind      string `db:"kind"`
		Direction string `db:"direction"`
		Quantity  *int64 `db:"quantity"`
	}
	e = q.SelectContext(ctx, &changes, q.Rebind(`SELECT c.kind,c.direction,c.quantity`+joined+` JOIN quantity_changes c ON c.revision_id=r.id`+where+` ORDER BY c.kind,c.direction,c.id`), args...)
	if e != nil {
		return nil, e
	}
	groups := []ChangeAggregate{}
	quality := []string{}
	for _, c := range changes {
		if f.Kind == "SAMPLING" && c.Kind == "LOSS" {
			quality = append(quality, "sampling_loss_forbidden")
			continue
		}
		i := len(groups) - 1
		if i < 0 || groups[i].Kind != c.Kind || groups[i].Direction != c.Direction {
			groups = append(groups, ChangeAggregate{Kind: c.Kind, Direction: c.Direction})
			i++
		}
		g := &groups[i]
		g.Count++
		if c.Quantity == nil {
			g.Quantity.UnknownCount++
		} else {
			g.Quantity.KnownSum, e = Add(g.Quantity.KnownSum, *c.Quantity)
			if e != nil {
				return nil, e
			}
		}
	}
	for i := range groups {
		u := &groups[i].Quantity
		u.Complete = u.UnknownCount == 0
		if u.Complete {
			v := u.KnownSum
			u.Value = &v
		}
	}
	result := gin.H{"groups": groups, "as_of": m.AsOf, "through_event_id": m.Through, "included_revision_ids": m.Included, "unknown_day_count": m.Unknown, "notes": m.Notes}
	if len(quality) > 0 {
		result["data_quality_issues"] = quality
	}
	return result, nil
}
func (h HTTP) changes(c *gin.Context) {
	if !query(c, statNames()...) {
		return
	}
	f, e := ParseFilter(c.Request.URL.Query(), true)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		if e := checkStatGroup(ctx, q, f); e != nil {
			return nil, e
		}
		return partitionStats(ctx, q, f, s, changeStats)
	})
}
func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string{}, vs...)
	}
	return out
}
func (h HTTP) workload(c *gin.Context) {
	metric := c.DefaultQuery("metric", "inspection")
	if metric == "save_actions" {
		h.saveActions(c)
		return
	}
	if metric != "inspection" {
		httpx.Fail(c, httpx.BadRequest("非法 metric"))
		return
	}
	if !query(c, statNames("metric", "by", "page", "page_size")...) {
		return
	}
	by := c.DefaultQuery("by", "paper_inspector")
	if by != "paper_inspector" && by != "created_by" && by != "last_saved_by" {
		httpx.Fail(c, httpx.BadRequest("非法 by"))
		return
	}
	f, e := ParseFilter(c.Request.URL.Query(), true)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	dims, _ := groupBy(by, f.Kind == "ALL")
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		r, e := summary(ctx, q, f, s, dims)
		if e != nil {
			return nil, e
		}
		return struct {
			SummaryResult
			Metric string `json:"metric"`
			By     string `json:"by"`
		}{r, metric, by}, nil
	})
}
