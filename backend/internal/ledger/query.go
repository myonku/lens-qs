package ledger

import (
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

var dimensions = strings.Fields("product_lot_id shared_form_no sample_identity operation_id inspection_kind batch_no scope machine_no paper_unit_text paper_inspector paper_submitter paper_machine_operator paper_handover_by sample_project sample_model sample_spec sample_size sample_side created_by inspection_day inspection_month")
var submissionFilters = strings.Fields("product_lot_id operation_group_id operation_id inspection_kind state scope batch_no machine_no paper_inspector created_by unassigned has_draft incomplete date_basis from to")

func queryAllowed(q url.Values, names ...string) error {
	for k, v := range q {
		if !contains(names, k) {
			return httpx.BadRequest("未知查询参数: " + k)
		}
		if len(v) != 1 {
			return httpx.BadRequest("重复查询参数: " + k)
		}
	}
	return nil
}
func pagination(q url.Values) (int, int, error) {
	p, n := 1, 20
	var e error
	if q.Has("page") {
		p, e = strconv.Atoi(q.Get("page"))
		if e != nil || p < 1 {
			return 0, 0, formatErr("page")
		}
	}
	if q.Has("page_size") {
		n, e = strconv.Atoi(q.Get("page_size"))
		if e != nil || n < 1 || n > 100 {
			return 0, 0, formatErr("page_size")
		}
	}
	return p, n, nil
}
func page(items []Object, q url.Values) (any, error) {
	p, n, e := pagination(q)
	if e != nil {
		return nil, e
	}
	start := (p - 1) * n
	if start < 0 || start > len(items) {
		start = len(items)
	}
	end := start + n
	if end > len(items) {
		end = len(items)
	}
	return Object{"items": items[start:end], "total": len(items), "page": p, "page_size": n}, nil
}
func validateFilters(q url.Values, stats bool) error {
	if stats && q.Has("state") {
		return httpx.BadRequest("统计固定使用正式版本")
	}
	for _, k := range []string{"unassigned", "has_draft", "incomplete"} {
		if q.Has(k) && q.Get(k) != "true" && q.Get(k) != "false" {
			return formatErr(k)
		}
	}
	for k, vals := range map[string][]string{"inspection_kind": {"NORMAL", "SAMPLING", "ALL"}, "scope": {"WHOLE", "SPLIT"}, "state": {"DRAFT", "PUBLISHED", "CANCELLED"}, "date_basis": {"inspection_day", "created_at"}} {
		if q.Has(k) && !contains(vals, q.Get(k)) {
			return formatErr(k)
		}
	}
	if !stats && q.Get("inspection_kind") == "ALL" {
		return formatErr("inspection_kind")
	}
	for _, k := range []string{"from", "to"} {
		if !q.Has(k) {
			continue
		}
		var v string
		var e error
		if q.Get("date_basis") == "created_at" {
			v, e = normalizeInstant(q.Get(k))
		} else {
			v, e = normalizeDate(q.Get(k))
		}
		if e != nil {
			return e
		}
		q.Set(k, v)
	}
	if q.Has("from") && q.Has("to") && q.Get("from") > q.Get("to") {
		return validation("起点大于终点")
	}
	return nil
}

type fact struct {
	sub, content, key Object
	revisionID        string
	day               any
}

func (u *unit) facts(q url.Values, formal bool) ([]fact, int, error) {
	if e := validateFilters(q, formal); e != nil {
		return nil, 0, e
	}
	if q.Get("operation_group_id") != "" {
		g, e := u.must("operation_groups", q.Get("operation_group_id"))
		if e != nil {
			return nil, 0, e
		}
		kind := q.Get("inspection_kind")
		if formal && kind == "" {
			kind = "NORMAL"
		}
		if kind != "" && kind != "ALL" && g["inspection_kind"] != kind {
			return nil, 0, validation("筛选类型与工序组不一致")
		}
	}
	subs := u.rows("SELECT * FROM inspection_submissions ORDER BY created_at,id")
	out := []fact{}
	unknown := 0
	for _, sub := range subs {
		if formal && sub["state"] != "PUBLISHED" {
			continue
		}
		kind := q.Get("inspection_kind")
		if formal && kind == "" {
			kind = "NORMAL"
		}
		if kind != "" && kind != "ALL" && sub["inspection_kind"] != kind {
			continue
		}
		var g, lot Object
		if sub["operation_group_id"] != nil {
			g, _ = u.must("operation_groups", str(sub["operation_group_id"]))
			if g != nil {
				lot, _ = u.must("product_lots", str(g["product_lot_id"]))
			}
		}
		key := Object{"inspection_kind": sub["inspection_kind"], "batch_no": sub["batch_no"], "scope": submissionView(sub)["scope"], "created_by": sub["created_by"], "operation_group_id": sub["operation_group_id"], "state": sub["state"]}
		if g != nil {
			for _, k := range []string{"operation_id", "product_lot_id"} {
				key[k] = g[k]
			}
		}
		if lot != nil {
			key["shared_form_no"] = lot["shared_form_no"]
			key["sample_identity"] = lot["sample_identity"]
		}
		rid := sub["current_revision_id"]
		if !formal && rid == nil {
			rid = sub["draft_revision_id"]
		}
		if rid == nil {
			continue
		}
		r, e := u.must("submission_revisions", str(rid))
		if e != nil {
			return nil, 0, e
		}
		c, e := decode([]byte(str(r["content"])))
		if e != nil {
			return nil, 0, e
		}
		for _, k := range dimensions {
			if v, ok := c[k]; ok {
				key[k] = v
			}
		}
		d := day(c["paper_inspected_date"], c["paper_inspected_at"])
		key["inspection_day"] = d
		key["inspection_month"] = nil
		if d != nil {
			key["inspection_month"] = str(d)[:7]
		}
		match := true
		for _, k := range []string{"product_lot_id", "operation_group_id", "operation_id", "state", "scope", "batch_no", "machine_no", "paper_inspector", "created_by"} {
			if q.Has(k) && str(key[k]) != q.Get(k) {
				match = false
			}
		}
		for k, b := range map[string]bool{"unassigned": sub["operation_group_id"] == nil, "has_draft": sub["draft_revision_id"] != nil, "incomplete": derived(c, sub["operation_group_id"] != nil)["is_complete"] == false} {
			if q.Has(k) && (q.Get(k) == "true") != b {
				match = false
			}
		}
		if !match {
			continue
		}
		if d == nil {
			unknown++
		}
		var date string
		if q.Get("date_basis") == "created_at" {
			date = str(sub["created_at"])
		} else {
			date = str(d)
		}
		if q.Has("from") && (date == "" || date < q.Get("from")) {
			continue
		}
		if q.Has("to") && (date == "" || date > q.Get("to") || q.Get("date_basis") == "created_at" && date == q.Get("to")) {
			continue
		}
		out = append(out, fact{sub: sub, content: c, key: key, revisionID: str(rid), day: d})
	}
	return out, unknown, nil
}
func aggregate(fs []fact, key Object) Object {
	counts := Object{}
	for _, field := range countFields {
		var sum, unknown int64
		for _, f := range fs {
			n, ok := integer(obj(f.content["counts"])[field])
			sum += n
			if !ok {
				unknown++
			}
		}
		var value any = sum
		if unknown > 0 {
			value = nil
		}
		counts[field] = Object{"value": value, "known_sum": sum, "unknown_count": unknown, "complete": unknown == 0}
	}
	result := Object{"key": key, "submission_count": len(fs), "counts": counts}
	t := obj(counts["total_qty"])["value"]
	q := obj(counts["qualified_qty"])["value"]
	r := derived(Object{"counts": Object{"total_qty": t, "qualified_qty": q}, "declared_counts": Object{}, "lines": []any{}}, true)
	result["pass_rate"] = r["pass_rate"]
	result["pass_rate_reason"] = r["pass_rate_reason"]
	return result
}
func summary(fs []fact, q url.Values, unknown int) (Object, error) {
	dims := []string{}
	if q.Get("group_by") != "" {
		dims = strings.Split(q.Get("group_by"), ",")
	}
	seen := map[string]bool{}
	for _, d := range dims {
		if !contains(dimensions, d) || seen[d] {
			return nil, httpx.BadRequest("非法统计维度", Object{"supported": dimensions})
		}
		seen[d] = true
	}
	if q.Get("inspection_kind") == "ALL" && !contains(dims, "inspection_kind") {
		dims = append([]string{"inspection_kind"}, dims...)
	}
	groups := map[string][]fact{}
	keys := map[string]Object{}
	ids := []string{}
	for _, f := range fs {
		k := Object{}
		for _, d := range dims {
			k[d] = f.key[d]
		}
		s := stringify(k)
		keys[s] = k
		groups[s] = append(groups[s], f)
		ids = append(ids, f.revisionID)
	}
	order := []string{}
	for k := range groups {
		order = append(order, k)
	}
	sort.Strings(order)
	items := []Object{}
	for _, k := range order {
		items = append(items, aggregate(groups[k], keys[k]))
	}
	sort.Strings(ids)
	result := Object{"group_by": dims, "groups": items, "as_of": now(), "included_revision_ids": ids, "unknown_day_count": unknown, "notes": []string{"跨工序相加是检验工作量，不是唯一产品数；数量变化不自动扣改计数"}}
	if q.Get("inspection_kind") == "ALL" {
		totals := []Object{}
		for _, kind := range []string{"NORMAL", "SAMPLING"} {
			subset := []fact{}
			for _, f := range fs {
				if f.sub["inspection_kind"] == kind {
					subset = append(subset, f)
				}
			}
			totals = append(totals, aggregate(subset, Object{"inspection_kind": kind}))
		}
		result["totals"] = totals
	} else {
		result["total"] = aggregate(fs, Object{})
	}
	return result, nil
}
func defectStats(fs []fact) []Object {
	items := []Object{}
	for _, d := range formfields.DefectCodes() {
		for _, s := range formfields.Surfaces {
			var sum, unknown int64
			for _, f := range fs {
				complete := len(f.content["lines"].([]any)) > 0
				for _, v := range f.content["lines"].([]any) {
					n, ok := integer(obj(obj(obj(v)["defects"])[d])[s])
					sum += n
					complete = complete && ok
				}
				if !complete {
					unknown++
				}
			}
			var value any = sum
			if unknown > 0 {
				value = nil
			}
			items = append(items, Object{"defect": d, "surface": s, "known_sum": sum, "unknown_count": unknown, "complete": unknown == 0, "value": value})
		}
	}
	return items
}
func changeStats(fs []fact) []Object {
	groups := map[string]Object{}
	for _, f := range fs {
		for _, v := range f.content["quantity_changes"].([]any) {
			q := obj(v)
			if f.sub["inspection_kind"] == "SAMPLING" && q["kind"] == "LOSS" {
				continue
			}
			k := str(q["kind"]) + "/" + str(q["direction"])
			if groups[k] == nil {
				groups[k] = Object{"kind": q["kind"], "direction": q["direction"], "known_sum": int64(0), "unknown_count": int64(0)}
			}
			g := groups[k]
			n, ok := integer(q["quantity"])
			g["known_sum"] = num(g["known_sum"]) + n
			if !ok {
				g["unknown_count"] = num(g["unknown_count"]) + 1
			}
		}
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []Object{}
	for _, k := range keys {
		out = append(out, groups[k])
	}
	return out
}
func (u *unit) stats(kind string, q url.Values) (any, error) {
	names := append([]string{}, submissionFilters...)
	names = append(names, "group_by")
	if e := queryAllowed(q, names...); e != nil {
		return nil, e
	}
	fs, unknown, e := u.facts(q, true)
	if e != nil {
		return nil, e
	}
	result, e := summary(fs, q, unknown)
	if e != nil {
		return nil, e
	}
	if kind == "summary" {
		return result, nil
	}
	// ALL never emits a mixed aggregate, including defects and changes.
	if q.Get("inspection_kind") == "ALL" {
		partitions := []Object{}
		for _, k := range []string{"NORMAL", "SAMPLING"} {
			subset := []fact{}
			for _, f := range fs {
				if f.sub["inspection_kind"] == k {
					subset = append(subset, f)
				}
			}
			var items []Object
			if kind == "defects" {
				items = defectStats(subset)
			} else {
				items = changeStats(subset)
			}
			partitions = append(partitions, Object{"inspection_kind": k, "items": items})
		}
		result["partitions"] = partitions
	} else if kind == "defects" {
		result["items"] = defectStats(fs)
	} else {
		result["items"] = changeStats(fs)
	}
	result["notes"] = append(result["notes"].([]string), "SAMPLING 无损耗填报口径，LOSS 不参与抽检数量变化汇总")
	return result, nil
}
func (u *unit) report(key string, q url.Values) (any, error) {
	if e := queryAllowed(q, "inspection_kind", "date_basis", "from", "to"); e != nil {
		return nil, e
	}
	lot, e := u.must("product_lots", key)
	if e != nil {
		return nil, e
	}
	q.Set("product_lot_id", key)
	fs, unknown, e := u.facts(q, true)
	if e != nil {
		return nil, e
	}
	sum, e := summary(fs, q, unknown)
	if e != nil {
		return nil, e
	}
	groups := u.rows("SELECT * FROM operation_groups WHERE product_lot_id = ? ORDER BY operation_code,inspection_kind,id", key)
	out := []Object{}
	for _, g := range groups {
		kind := q.Get("inspection_kind")
		if kind == "" {
			kind = "NORMAL"
		}
		if kind != "ALL" && g["inspection_kind"] != kind {
			continue
		}
		gf := []fact{}
		for _, f := range fs {
			if f.sub["operation_group_id"] == g["id"] {
				gf = append(gf, f)
			}
		}
		detail := clone(g)
		subs := u.rows("SELECT * FROM inspection_submissions WHERE operation_group_id = ? ORDER BY batch_no,id", g["id"])
		var registered, published, initial, revision, cancelled int
		items := []Object{}
		for _, s := range subs {
			if s["state"] == "CANCELLED" {
				cancelled++
				continue
			}
			registered++
			if s["current_revision_id"] != nil {
				published++
				if s["draft_revision_id"] != nil {
					revision++
				}
			} else {
				initial++
			}
			items = append(items, u.detail(s))
		}
		detail["submissions"] = items
		detail["registered_count"] = registered
		detail["published_count"] = published
		detail["initial_draft_count"] = initial
		detail["revision_draft_count"] = revision
		detail["cancelled_count"] = cancelled
		detail["subtotal"] = aggregate(gf, Object{})
		out = append(out, detail)
	}
	sum["product_lot"] = lot
	sum["groups"] = out
	delete(sum, "total")
	delete(sum, "totals")
	return sum, nil
}
func diff(a, b any, path string, out *[]Object) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		order := []string{}
		for k := range keys {
			order = append(order, k)
		}
		sort.Strings(order)
		for _, k := range order {
			diff(am[k], bm[k], path+"/"+k, out)
		}
		return
	}
	if !reflect.DeepEqual(a, b) {
		*out = append(*out, Object{"path": path, "old": a, "new": b})
	}
}
