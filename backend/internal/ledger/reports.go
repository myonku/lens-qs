package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

type DifferenceEntry struct {
	Path string `json:"path"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}

func compare(path string, a, b any, out *[]DifferenceEntry) {
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
		names := []string{}
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p := k
			if path != "" {
				p = path + "." + k
			}
			compare(p, am[k], bm[k], out)
		}
		return
	}
	ar, _ := json.Marshal(a)
	br, _ := json.Marshal(b)
	if string(ar) != string(br) {
		*out = append(*out, DifferenceEntry{path, a, b})
	}
}
func (h HTTP) diff(c *gin.Context) {
	if !query(c, "from", "to") {
		return
	}
	if c.Query("from") == "" || c.Query("to") == "" {
		httpx.Fail(c, httpx.BadRequest("两个版本 ID 必填"))
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		values := []map[string]any{}
		for _, id := range []string{c.Query("from"), c.Query("to")} {
			row, e := readOne[revisionRow](ctx, q, `SELECT `+revisionColumns+` FROM submission_revisions WHERE id=? AND submission_id=?`, id, c.Param("id"))
			if e != nil {
				return nil, e
			}
			var value map[string]any
			decoder := json.NewDecoder(strings.NewReader(row.Content))
			decoder.UseNumber()
			if e = decoder.Decode(&value); e != nil {
				return nil, e
			}
			values = append(values, value)
		}
		out := []DifferenceEntry{}
		a, b := values[0], values[1]
		al, _ := a["lines"].([]any)
		bl, _ := b["lines"].([]any)
		delete(a, "lines")
		delete(b, "lines")
		compare("", a, b, &out)
		lineMap := func(lines []any) map[string]any {
			m := map[string]any{}
			for _, line := range lines {
				v := line.(map[string]any)
				m[fmt.Sprint(v["seq"])] = v
			}
			return m
		}
		am, bm := lineMap(al), lineMap(bl)
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		seqs := []string{}
		for k := range keys {
			seqs = append(seqs, k)
		}
		sort.Slice(seqs, func(i, j int) bool {
			a, _ := strconv.ParseInt(seqs[i], 10, 64)
			b, _ := strconv.ParseInt(seqs[j], 10, 64)
			return a < b
		})
		for _, seq := range seqs {
			compare("lines[seq="+seq+"]", am[seq], bm[seq], &out)
		}
		return gin.H{"from_revision_id": c.Query("from"), "to_revision_id": c.Query("to"), "changes": out}, nil
	})
}

type domainEvent struct {
	ID       int64   `db:"event_id"`
	Entity   string  `db:"entity"`
	EntityID string  `db:"entity_id"`
	Action   string  `db:"action"`
	After    *string `db:"after_json"`
}
type ReportGroup struct {
	Group       Group     `json:"group"`
	Submissions []Detail  `json:"submissions"`
	Progress    Progress  `json:"progress"`
	Subtotal    Aggregate `json:"subtotal"`
}

func (h HTTP) productReport(c *gin.Context) {
	if !query(c, "inspection_kind", "as_of", "through_event_id") {
		return
	}
	kind := c.DefaultQuery("inspection_kind", "NORMAL")
	if kind != "NORMAL" && kind != "SAMPLING" && kind != "ALL" {
		httpx.Fail(c, httpx.BadRequest("非法 inspection_kind"))
		return
	}
	at := c.Query("as_of")
	if c.Request.URL.Query().Has("as_of") {
		a := &at
		if e := eventDate(new(*string), &a, "as_of"); e != nil {
			httpx.Fail(c, e)
			return
		}
		at = *a
	}
	through := int64(0)
	if c.Request.URL.Query().Has("through_event_id") {
		var e error
		through, e = strconv.ParseInt(c.Query("through_event_id"), 10, 64)
		if e != nil || through < 1 || at == "" {
			httpx.Fail(c, httpx.BadRequest("事件上界需要有效 as_of 和正整数"))
			return
		}
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		// Compare against the persisted logical present, so a report returned
		// during a wall-clock rollback can still be replayed verbatim.
		if at != "" && at > s.AsofTime {
			return nil, invalid("future_as_of", "as_of")
		}
		if _, e := readOne[ProductLot](ctx, q, `SELECT * FROM product_lots WHERE id=?`, c.Param("id")); e != nil {
			return nil, e
		}
		cutoff := at
		if cutoff == "" {
			cutoff = s.AsofTime
		}
		if s.HistoryStartedAt == nil || cutoff < *s.HistoryStartedAt {
			return nil, httpx.Validation("历史不可还原", gin.H{"reason": "history_unavailable", "history_started_at": s.HistoryStartedAt})
		}
		boundary := through
		if boundary > 0 {
			var n int
			if e := q.GetContext(ctx, &n, q.Rebind(`SELECT COUNT(*) FROM event_transactions WHERE domain=1 AND last_event_id=? AND created_at<=?`), boundary, cutoff); e != nil {
				return nil, e
			}
			if n != 1 {
				return nil, invalid("invalid_history_boundary", "through_event_id")
			}
		} else {
			if e := q.GetContext(ctx, &boundary, q.Rebind(`SELECT COALESCE(MAX(last_event_id),0) FROM event_transactions WHERE domain=1 AND created_at<=?`), cutoff); e != nil {
				return nil, e
			}
		}
		events := []domainEvent{}
		sqlQuery := `SELECT e.event_id,e.entity,e.entity_id,e.action,e.after_json FROM business_events e JOIN event_transactions t ON t.id=e.transaction_id WHERE t.domain=1 AND e.event_id<=? AND e.created_at<=? AND ((e.entity='product_lot' AND e.entity_id=?) OR (e.entity='operation_group' AND e.entity_id IN (SELECT id FROM operation_groups WHERE product_lot_id=?)) OR (e.entity='submission' AND e.entity_id IN (SELECT ss.id FROM inspection_submissions ss JOIN operation_groups gg ON gg.id=ss.operation_group_id WHERE gg.product_lot_id=?))) ORDER BY e.event_id`
		if e := q.SelectContext(ctx, &events, q.Rebind(sqlQuery), boundary, cutoff, c.Param("id"), c.Param("id"), c.Param("id")); e != nil {
			return nil, e
		}
		var lot *ProductLot
		groups := map[string]Group{}
		subs := map[string]Submission{}
		for _, ev := range events {
			if ev.After == nil {
				continue
			}
			switch ev.Entity {
			case "product_lot":
				var l ProductLot
				if e := json.Unmarshal([]byte(*ev.After), &l); e != nil {
					return nil, e
				}
				lot = &l
			case "operation_group":
				var g Group
				if e := json.Unmarshal([]byte(*ev.After), &g); e != nil {
					return nil, e
				}
				groups[g.ID] = g
			case "submission":
				var sub Submission
				if e := json.Unmarshal([]byte(*ev.After), &sub); e != nil {
					return nil, e
				}
				subs[sub.ID] = sub
			}
		}
		if lot == nil {
			return nil, httpx.NotFound("该时点生产批不存在")
		}
		ordered := []Group{}
		for _, g := range groups {
			if kind == "ALL" || g.Kind == kind {
				ordered = append(ordered, g)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := ordered[i], ordered[j]
			if a.OperationCode != b.OperationCode {
				return a.OperationCode < b.OperationCode
			}
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.ID < b.ID
		})
		selected := []Submission{}
		for _, sub := range subs {
			if sub.GroupID != nil && (kind == "ALL" || sub.Kind == kind) {
				selected = append(selected, sub)
			}
		}
		sort.Slice(selected, func(i, j int) bool {
			a, b := selected[i], selected[j]
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt > b.CreatedAt
			}
			return a.ID > b.ID
		})
		ds, e := details(ctx, q, selected, snapshotNow(cutoff))
		if e != nil {
			return nil, e
		}
		byGroup := map[string][]Detail{}
		for _, d := range ds {
			byGroup[*d.Submission.GroupID] = append(byGroup[*d.Submission.GroupID], d)
		}
		result := []ReportGroup{}
		included := []string{}
		for _, g := range ordered {
			items := byGroup[g.ID]
			if items == nil {
				items = []Detail{}
			}
			p := Progress{Registered: len(items)}
			rows := []statRow{}
			for _, d := range items {
				if d.Submission.State == "CANCELLED" {
					p.Cancelled++
				} else {
					p.Active++
					if !d.Derived.Complete {
						p.Incomplete++
					}
					rows = append(rows, statRow{Total: d.Revision.Content.Counts.Total, Qualified: d.Revision.Content.Counts.Qualified, Defect: d.Revision.Content.Counts.Defect})
					included = append(included, d.Revision.ID)
				}
			}
			subtotal, e := aggregateRows(rows, map[string]*string{"operation_id": &g.OperationID, "inspection_kind": &g.Kind})
			if e != nil {
				return nil, e
			}
			result = append(result, ReportGroup{g, items, p, subtotal})
		}
		sort.Strings(included)
		return gin.H{"product_lot": lot, "groups": result, "as_of": cutoff, "through_event_id": boundary, "history_started_at": s.HistoryStartedAt, "included_revision_ids": included, "notes": []string{"各工序及类型独立；不提供全流程良率或实物流向"}}, nil
	})
}
func (h HTTP) operationReport(c *gin.Context) {
	if !query(c, "from", "to", "date_basis", "product_lot_id", "inspection_kind", "page", "page_size") {
		return
	}
	if !c.Request.URL.Query().Has("from") || !c.Request.URL.Query().Has("to") {
		httpx.Fail(c, httpx.BadRequest("from/to 两端必填"))
		return
	}
	v := cloneValues(c.Request.URL.Query())
	v.Set("operation_id", c.Param("id"))
	if !v.Has("inspection_kind") {
		v.Set("inspection_kind", "NORMAL")
	}
	f, e := ParseFilter(v, false)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		var op struct {
			ID    string `db:"id" json:"id"`
			Code  string `db:"code" json:"code"`
			Label string `db:"label" json:"label"`
		}
		e := q.GetContext(ctx, &op, q.Rebind(`SELECT id,code,label FROM dict_items WHERE id=? AND category='inspection_operation'`), c.Param("id"))
		if e != nil {
			return nil, httpx.NotFound("工序不存在")
		}
		l, e := list(ctx, q, f, snapshotTime(s))
		if e != nil {
			return nil, e
		}
		sf := f
		sf.Stats = true
		sf.Page = 1
		sf.Size = 100000000
		by, _ := groupBy("product_lot_id,inspection_kind", sf.Kind == "ALL")
		summary, e := summary(ctx, q, sf, s, by)
		if e != nil {
			return nil, e
		}
		rangeSummary := gin.H{"groups": summary.Groups, "included_revision_ids": summary.Included, "notes": summary.Notes}
		if summary.Total != nil {
			rangeSummary["total"] = summary.Total
		} else {
			rangeSummary["totals"] = summary.Totals
		}
		where, args := f.where(false)
		var counts struct {
			Active     int `db:"active"`
			Cancelled  int `db:"cancelled"`
			Incomplete int `db:"incomplete"`
		}
		e = q.GetContext(ctx, &counts, q.Rebind(`SELECT COALESCE(SUM(CASE WHEN s.state='ACTIVE' THEN 1 ELSE 0 END),0) active,COALESCE(SUM(CASE WHEN s.state='CANCELLED' THEN 1 ELSE 0 END),0) cancelled,COALESCE(SUM(CASE WHEN s.state='ACTIVE' AND r.is_complete=0 THEN 1 ELSE 0 END),0) incomplete`+joined+where), args...)
		if e != nil {
			return nil, e
		}
		type listing struct {
			Detail
			LotID      string `json:"product_lot_id"`
			Shared     string `json:"shared_form_no"`
			Identity   string `json:"sample_identity"`
			RevisionID string `json:"listing_revision_id"`
		}
		items := []listing{}
		lotMap := map[string]ProductLot{}
		ids := []string{}
		for _, d := range l.Items {
			ids = append(ids, *d.Submission.GroupID)
		}
		if len(ids) > 0 {
			sqlQuery, args, e := sqlx.In(`SELECT g.id group_id,p.id,p.shared_form_no,p.sample_identity FROM operation_groups g JOIN product_lots p ON p.id=g.product_lot_id WHERE g.id IN (?)`, ids)
			if e != nil {
				return nil, e
			}
			var rows []struct {
				GroupID  string `db:"group_id"`
				ID       string `db:"id"`
				Shared   string `db:"shared_form_no"`
				Identity string `db:"sample_identity"`
			}
			if e = q.SelectContext(ctx, &rows, q.Rebind(sqlQuery), args...); e != nil {
				return nil, e
			}
			for _, r := range rows {
				lotMap[r.GroupID] = ProductLot{ID: r.ID, SharedFormNo: r.Shared, SampleIdentity: r.Identity}
			}
		}
		for _, d := range l.Items {
			lot := lotMap[*d.Submission.GroupID]
			items = append(items, listing{d, lot.ID, lot.SharedFormNo, lot.SampleIdentity, d.Revision.ID})
		}
		return gin.H{"operation": op, "items": items, "total": l.Total, "page": l.Page, "page_size": l.Size, "summary": rangeSummary, "active_count": counts.Active, "cancelled_count": counts.Cancelled, "incomplete_count": counts.Incomplete, "unknown_day_count": l.Unknown, "date_basis": f.Basis, "from": v.Get("from"), "to": v.Get("to"), "as_of": s.AsofTime, "through_event_id": s.AsofEventID}, nil
	})
}

type evidenceJSON []byte

func (j *evidenceJSON) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*j = []byte("null")
	case string:
		*j = []byte(x)
	case []byte:
		*j = append((*j)[:0], x...)
	default:
		return fmt.Errorf("invalid evidence JSON type")
	}
	return nil
}
func (j evidenceJSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

type AuditEntry struct {
	ID          int64        `db:"event_id" json:"event_id"`
	Transaction string       `db:"transaction_id" json:"transaction_id"`
	Actor       string       `db:"actor_id" json:"actor_id"`
	Name        string       `db:"actor_name" json:"actor_name"`
	Action      string       `db:"action" json:"action"`
	Entity      string       `db:"entity" json:"entity"`
	EntityID    string       `db:"entity_id" json:"entity_id"`
	Before      evidenceJSON `db:"before_json" json:"before"`
	After       evidenceJSON `db:"after_json" json:"after"`
	Reason      *string      `db:"reason" json:"reason"`
	Time        string       `db:"created_at" json:"created_at"`
}

func (h HTTP) audit(c *gin.Context) {
	if !query(c, "entity", "entity_id", "actor_id", "from", "to", "page", "page_size") {
		return
	}
	page, size, e := httpx.Pagination(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	entities := map[string]bool{"product_lot": true, "operation_group": true, "submission": true, "user": true, "dict_item": true, "sample_cache": true, "auth": true}
	if c.Request.URL.Query().Has("entity") && !entities[c.Query("entity")] {
		httpx.Fail(c, httpx.BadRequest("非法 entity"))
		return
	}
	v := cloneValues(c.Request.URL.Query())
	v.Set("date_basis", "created_at")
	if _, e = ParseFilter(v, false); e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, _ store.Snapshot) (any, error) {
		where := " WHERE 1=1"
		args := []any{}
		for _, key := range []string{"entity", "entity_id", "actor_id"} {
			if v.Has(key) {
				where += " AND " + key + "=?"
				args = append(args, v.Get(key))
			}
		}
		if v.Has("from") {
			where += " AND created_at>=?"
			args = append(args, v.Get("from"))
		}
		if v.Has("to") {
			where += " AND created_at<?"
			args = append(args, v.Get("to"))
		}
		var total int
		if e := q.GetContext(ctx, &total, q.Rebind(`SELECT COUNT(*) FROM business_events`+where), args...); e != nil {
			return nil, e
		}
		items := []AuditEntry{}
		args = append(args, size, (page-1)*size)
		e := q.SelectContext(ctx, &items, q.Rebind(`SELECT event_id,transaction_id,actor_id,actor_name,action,entity,entity_id,COALESCE(before_json,'null') before_json,COALESCE(after_json,'null') after_json,reason,created_at FROM business_events`+where+` ORDER BY event_id DESC LIMIT ? OFFSET ?`), args...)
		return httpx.NewPage(items, total, page, size), e
	})
}
func (h HTTP) saveActions(c *gin.Context) {
	if !query(c, "metric", "by", "from", "to", "product_lot_id", "operation_id", "inspection_kind", "saved_by", "page", "page_size") {
		return
	}
	if c.DefaultQuery("by", "saved_by") != "saved_by" {
		httpx.Fail(c, httpx.BadRequest("非法 by"))
		return
	}
	if !c.Request.URL.Query().Has("from") || !c.Request.URL.Query().Has("to") {
		httpx.Fail(c, httpx.BadRequest("from/to 必填"))
		return
	}
	v := cloneValues(c.Request.URL.Query())
	v.Set("date_basis", "created_at")
	f, e := ParseFilter(v, true)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	h.read(c, func(ctx context.Context, q store.Reader, s store.Snapshot) (any, error) {
		where := ` WHERE e.action='content_saved' AND e.entity='submission' AND e.created_at>=? AND e.created_at<?`
		args := []any{v.Get("from"), v.Get("to")}
		if v.Has("saved_by") {
			where += ` AND e.actor_id=?`
			args = append(args, v.Get("saved_by"))
		}
		events := []struct {
			Actor    string `db:"actor_id"`
			EntityID string `db:"entity_id"`
			After    string `db:"after_json"`
		}{}
		if e := q.SelectContext(ctx, &events, q.Rebind(`SELECT e.actor_id,e.entity_id,e.after_json FROM business_events e JOIN event_transactions t ON t.id=e.transaction_id AND t.domain=1`+where), args...); e != nil {
			return nil, e
		}
		var groups []Group
		if e := q.SelectContext(ctx, &groups, `SELECT * FROM operation_groups`); e != nil {
			return nil, e
		}
		gm := map[string]Group{}
		for _, g := range groups {
			gm[g.ID] = g
		}
		type workloadItem struct {
			SavedBy  string `json:"saved_by"`
			Kind     string `json:"inspection_kind"`
			Count    int    `json:"save_count"`
			Distinct int    `json:"distinct_submission_count"`
		}
		buckets := map[string]*workloadItem{}
		sets := map[string]map[string]bool{}
		for _, ev := range events {
			var sub Submission
			if e := json.Unmarshal([]byte(ev.After), &sub); e != nil {
				return nil, e
			}
			if f.Kind != "ALL" && sub.Kind != f.Kind {
				continue
			}
			if v.Has("product_lot_id") || v.Has("operation_id") {
				if sub.GroupID == nil {
					continue
				}
				g := gm[*sub.GroupID]
				if v.Has("product_lot_id") && g.LotID != v.Get("product_lot_id") {
					continue
				}
				if v.Has("operation_id") && g.OperationID != v.Get("operation_id") {
					continue
				}
			}
			key := ev.Actor + "\x00" + sub.Kind
			if buckets[key] == nil {
				buckets[key] = &workloadItem{SavedBy: ev.Actor, Kind: sub.Kind}
				sets[key] = map[string]bool{}
			}
			buckets[key].Count++
			sets[key][sub.ID] = true
		}
		keys := []string{}
		for k := range buckets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := []workloadItem{}
		start := (f.Page - 1) * f.Size
		for i := start; i < len(keys) && i < start+f.Size; i++ {
			k := keys[i]
			buckets[k].Distinct = len(sets[k])
			items = append(items, *buckets[k])
		}
		return gin.H{"metric": "save_actions", "by": "saved_by", "items": items, "total": len(keys), "page": f.Page, "page_size": f.Size, "as_of": s.AsofTime, "through_event_id": s.AsofEventID}, nil
	})
}
