package ledger

import (
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"net/url"
	"sort"
	"strconv"
)

// replay uses immutable content and audit object snapshots from one consistent read.
// A returned through_event_id can be pinned on a subsequent request even within the same second.
func (u *unit) report(key string, q url.Values) (any, error) {
	if e := queryAllowed(q, "as_of", "through_event_id", "inspection_kind"); e != nil {
		return nil, e
	}
	cutoff := now()
	var e error
	if q.Has("as_of") {
		cutoff, e = normalizeInstant(q.Get("as_of"))
		if e != nil {
			return nil, e
		}
		if cutoff > now() {
			return nil, validation("as_of 不能晚于当前时间")
		}
	}
	baseline := u.get("SELECT history_started_at FROM v2_release_state WHERE id = 1")
	if baseline == nil {
		return nil, validation("历史基线缺失")
	}
	if cutoff < str(baseline["history_started_at"]) {
		return nil, httpx.Validation("指定时间早于 v2 历史起点", Object{"reason": "history_unavailable", "history_started_at": baseline["history_started_at"]})
	}
	kind := q.Get("inspection_kind")
	if kind == "" {
		kind = "NORMAL"
	}
	if !contains([]string{"NORMAL", "SAMPLING", "ALL"}, kind) {
		return nil, formatErr("inspection_kind")
	}
	max := u.get("SELECT COALESCE(MAX(id),0) AS n FROM audit_logs")
	limit := num(max["n"])
	if q.Has("through_event_id") {
		limit, e = strconv.ParseInt(q.Get("through_event_id"), 10, 64)
		if e != nil || limit < 0 || limit > num(max["n"]) {
			return nil, formatErr("through_event_id")
		}
	}
	lots, groups, subs := map[string]Object{}, map[string]Object{}, map[string]Object{}
	last := int64(0)
	for _, a := range u.rows(`SELECT id,entity,entity_id,"after",created_at FROM audit_logs WHERE created_at <= ? AND id <= ? ORDER BY id`, cutoff, limit) {
		var target map[string]Object
		switch a["entity"] {
		case "product_lot":
			target = lots
		case "operation_group":
			target = groups
		case "inspection_submission":
			target = subs
		default:
			continue
		}
		if a["after"] == nil {
			continue
		}
		m, e := decode([]byte(str(a["after"])))
		if e != nil {
			return nil, e
		}
		target[str(a["entity_id"])] = m
		last = num(a["id"])
	}
	lot := lots[key]
	if lot == nil {
		return nil, httpx.NotFound("指定时点产品批不存在")
	}
	order := []string{}
	for id, g := range groups {
		if g["product_lot_id"] == key && (kind == "ALL" || g["inspection_kind"] == kind) {
			order = append(order, id)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := groups[order[i]], groups[order[j]]
		return str(a["operation_code"])+str(a["inspection_kind"])+order[i] < str(b["operation_code"])+str(b["inspection_kind"])+order[j]
	})
	out := []Object{}
	ids := []string{}
	for _, gid := range order {
		g := clone(groups[gid])
		items := []Object{}
		formal := []fact{}
		var registered, published, initial, revision, cancelled int
		suborder := []string{}
		for id, s := range subs {
			if s["operation_group_id"] == gid {
				suborder = append(suborder, id)
			}
		}
		sort.Slice(suborder, func(i, j int) bool {
			return str(subs[suborder[i]]["batch_no"])+suborder[i] < str(subs[suborder[j]]["batch_no"])+suborder[j]
		})
		mode := "EMPTY"
		for _, sid := range suborder {
			s := subs[sid]
			d := Object{"submission": submissionView(s), "current_revision": nil, "draft_revision": nil}
			for _, field := range []string{"current_revision_id", "draft_revision_id"} {
				if s[field] == nil {
					continue
				}
				r := obj(u.revisionView(s[field]))
				if u.err != nil {
					return nil, u.err
				}
				r = clone(r)
				if field == "draft_revision_id" {
					r["reason"] = r["saved_reason"]
					r["published_by"] = nil
					r["published_at"] = nil
					d["draft_revision"] = r
				} else {
					d["current_revision"] = r
				}
			}
			selected := obj(d["current_revision"])
			if selected == nil {
				selected = obj(d["draft_revision"])
			}
			if selected != nil {
				for k, v := range derived(obj(selected["content"]), true) {
					d[k] = v
				}
			}
			items = append(items, d)
			if s["state"] == "CANCELLED" {
				cancelled++
				continue
			}
			registered++
			mode = str(submissionView(s)["scope"])
			if s["current_revision_id"] == nil {
				initial++
			} else {
				published++
				if s["draft_revision_id"] != nil {
					revision++
				}
				r := obj(d["current_revision"])
				formal = append(formal, fact{sub: s, content: obj(r["content"]), revisionID: str(r["id"])})
				ids = append(ids, str(r["id"]))
			}
		}
		g["batch_mode"] = mode
		g["submissions"] = items
		g["registered_count"] = registered
		g["published_count"] = published
		g["initial_draft_count"] = initial
		g["revision_draft_count"] = revision
		g["cancelled_count"] = cancelled
		g["subtotal"] = aggregate(formal, Object{"inspection_kind": g["inspection_kind"]})
		out = append(out, g)
	}
	sort.Strings(ids)
	return Object{"product_lot": lot, "groups": out, "as_of": cutoff, "through_event_id": last, "history_started_at": baseline["history_started_at"], "included_revision_ids": ids, "notes": []string{"各工序独立展示，草稿/取消不计正式小计；相同秒内通过事件序号固定视图"}}, nil
}
func (u *unit) operationReport(operation string, q url.Values) (any, error) {
	if e := queryAllowed(q, "from", "to", "date_basis", "product_lot_id", "inspection_kind", "page", "page_size"); e != nil {
		return nil, e
	}
	if !q.Has("from") || !q.Has("to") {
		return nil, httpx.BadRequest("from/to 必填")
	}
	if _, _, e := pagination(q); e != nil {
		return nil, e
	}
	op, e := u.must("dict_items", operation)
	if e != nil {
		return nil, e
	}
	if !contains([]string{"inspection_operation", "process"}, str(op["category"])) {
		return nil, validation("operation_id 必须为工序")
	}
	if q.Has("product_lot_id") {
		if _, e = u.must("product_lots", q.Get("product_lot_id")); e != nil {
			return nil, e
		}
	}
	kind := q.Get("inspection_kind")
	if kind == "" {
		kind = "NORMAL"
	}
	if !contains([]string{"NORMAL", "SAMPLING", "ALL"}, kind) {
		return nil, formatErr("inspection_kind")
	}
	filters := url.Values{}
	for _, name := range []string{"from", "to", "date_basis", "product_lot_id"} {
		if q.Has(name) {
			filters.Set(name, q.Get(name))
		}
	}
	filters.Set("operation_id", operation)
	if kind != "ALL" {
		filters.Set("inspection_kind", kind)
	}
	fs, unknown, e := u.facts(filters, false)
	if e != nil {
		return nil, e
	}
	items := []Object{}
	formal := []fact{}
	var initial, revision, cancelled int
	for _, f := range fs {
		d := u.detail(f.sub)
		d["product_lot_id"] = f.key["product_lot_id"]
		d["shared_form_no"] = f.key["shared_form_no"]
		d["sample_identity"] = f.key["sample_identity"]
		d["listing_revision_id"] = f.revisionID
		for k, v := range derived(f.content, true) {
			d[k] = v
		}
		items = append(items, d)
		if f.sub["state"] == "CANCELLED" {
			cancelled++
			continue
		}
		if f.sub["state"] == "PUBLISHED" {
			formal = append(formal, f)
			if f.sub["draft_revision_id"] != nil {
				revision++
			}
		} else {
			initial++
		}
	}
	sq := url.Values{"inspection_kind": {kind}, "group_by": {"product_lot_id,inspection_kind"}}
	sum, e := summary(formal, sq, unknown)
	if e != nil {
		return nil, e
	}
	paged, e := page(items, q)
	if e != nil {
		return nil, e
	}
	out := obj(paged)
	out["operation"] = op
	out["summary"] = sum
	out["initial_draft_count"] = initial
	out["revision_draft_count"] = revision
	out["cancelled_count"] = cancelled
	out["unknown_day_count"] = unknown
	out["date_basis"] = filters.Get("date_basis")
	if out["date_basis"] == "" {
		out["date_basis"] = "inspection_day"
	}
	out["from"] = filters.Get("from")
	out["to"] = filters.Get("to")
	out["as_of"] = sum["as_of"]
	return out, nil
}
