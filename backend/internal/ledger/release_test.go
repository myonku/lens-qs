package ledger

import (
	"context"
	"fmt"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/users"
	"strings"
	"testing"
	"time"
)

func TestAccountTypeIsolation(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("PERM", "M")
	normal := h.group(lot, "a", "NORMAL")
	sampling := h.group(lot, "a", "SAMPLING")
	nd := h.sub(normal, "n", Object{})
	sd := h.sub(sampling, "s", Object{})
	ns, ss := obj(nd["submission"]), obj(sd["submission"])
	h.db.Exec("UPDATE users SET is_admin = 1 WHERE id = 'actor'")
	for _, tc := range []struct {
		actor, group string
		sub          Object
		opposite     string
	}{{"actor", sampling, ss, "SAMPLING"}, {"sampler", normal, ns, "NORMAL"}} {
		h.actor = tc.actor
		cases := []struct {
			method, path string
			req          Object
		}{{"POST", "/product-lots/" + lot + "/operation-groups", Object{"operation_id": "b", "inspection_kind": tc.opposite}}, {"POST", "/submissions", Object{"inspection_kind": tc.opposite}}, {"POST", "/submissions", Object{"operation_group_id": tc.group}}, {"PATCH", "/submissions/" + str(tc.sub["id"]) + "/draft", Object{"expected_version": tc.sub["version"], "patch": Object{"remark": "forbidden"}}}, {"POST", "/submissions/" + str(tc.sub["id"]) + "/publish", Object{"expected_version": tc.sub["version"], "draft_revision_id": tc.sub["draft_revision_id"]}}, {"POST", "/submissions/" + str(tc.sub["id"]) + "/cancel", Object{"expected_version": tc.sub["version"], "reason": "forbidden"}}, {"POST", "/operation-groups/" + tc.group + "/close", Object{"expected_version": 2, "reason": "forbidden"}}, {"POST", "/operation-groups/" + tc.group + "/reopen", Object{"expected_version": 2, "reason": "forbidden"}}}
		for i, c := range cases {
			status, m, _ := h.call(c.method, c.path, fmt.Sprintf("forbidden-%s-%d", tc.actor, i), c.req)
			if status != 403 || obj(m["details"])["reason"] != "inspection_kind_forbidden" {
				t.Fatal(c, status, m)
			}
		}
		status, m, _ := h.call("POST", "/submissions", "default-"+tc.actor, Object{})
		if status != 201 {
			t.Fatal(status, m)
		}
		draft := obj(obj(m["data"])["submission"])
		expected := "NORMAL"
		if tc.actor == "sampler" {
			expected = "SAMPLING"
		}
		if draft["inspection_kind"] != expected {
			t.Fatal(draft)
		}
		status, m, _ = h.call("PATCH", "/submissions/"+str(draft["id"])+"/draft", "bind-"+tc.actor, Object{"expected_version": 1, "operation_group_id": tc.group})
		if status != 403 {
			t.Fatal(status, m)
		}
	}
	h.actor = "sampler"
	var before, after int
	h.db.Get(&before, "SELECT COUNT(*) FROM inspection_submissions")
	status, m, _ := h.call("POST", "/submissions/batch", "mixed-permissions", Object{"items": []any{Object{"operation_group_id": sampling, "batch_no": "atomic"}, Object{"operation_group_id": normal, "batch_no": "atomic"}}})
	if status != 403 || num(obj(m["details"])["index"]) != 1 {
		t.Fatal(status, m)
	}
	h.db.Get(&after, "SELECT COUNT(*) FROM inspection_submissions")
	if before != after {
		t.Fatal("partial batch", before, after)
	}
}
func TestAccountTypeChangeRevokesSessionAndCache(t *testing.T) {
	h := newHarness(t)
	driver := "sqlite"
	if h.db.DriverName() == "pgx" {
		driver = "postgres"
	}
	us := users.NewStore(h.db, driver)
	u, e := us.Create(context.Background(), users.CreateInput{Username: "changing", Password: "unchanged-password"})
	if e != nil {
		t.Fatal(e)
	}
	svc := auth.NewService(h.db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, driver)
	login, e := svc.Login(context.Background(), u.Username, "unchanged-password", "", "")
	if e != nil {
		t.Fatal(e)
	}
	h.actor = u.ID
	status, m, _ := h.call("POST", "/submissions", "cached", Object{})
	if status != 201 {
		t.Fatal(m)
	}
	old := obj(obj(m["data"])["submission"])
	kind := "SAMPLING"
	changed, e := us.Update(context.Background(), u.ID, users.UpdateInput{AccountType: &kind})
	if e != nil || changed.PasswordHash != u.PasswordHash {
		t.Fatal(changed, e)
	}
	if _, e = svc.Authenticate(login.Token); e == nil {
		t.Fatal("old session still valid")
	}
	var n int
	h.db.Get(&n, h.db.Rebind("SELECT COUNT(*) FROM v2_idempotency_requests WHERE actor_id = ?"), u.ID)
	if n != 0 {
		t.Fatal("old cache retained")
	}
	status, m, w := h.call("POST", "/submissions", "cached", Object{})
	if status != 201 || w.Header().Get("X-Idempotent-Replay") != "" || obj(obj(m["data"])["submission"])["inspection_kind"] != "SAMPLING" {
		t.Fatal(status, m)
	}
	status, m, _ = h.call("POST", "/submissions/"+str(old["id"])+"/cancel", "old-normal", Object{"expected_version": 1, "reason": "old scope"})
	if status != 403 {
		t.Fatal(status, m)
	}
	principalLogin, e := svc.Login(context.Background(), u.Username, "unchanged-password", "", "")
	if e != nil {
		t.Fatal(e)
	}
	principal, e := svc.Authenticate(principalLogin.Token)
	if e != nil || principal.AccountType != "SAMPLING" {
		t.Fatal(principal, e)
	}
}
func TestHistoricalReportAndPinnedEvents(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("HISTORY", "M")
	g := h.group(lot, "a", "NORMAL")
	d := h.sub(g, nil, content(100, 96, 4))
	s := obj(d["submission"])
	path := "/reports/product-lots/" + lot
	draftSnapshot := h.get(path)
	earlyURL := path + "?as_of=" + str(draftSnapshot["as_of"]) + "&through_event_id=" + fmt.Sprint(num(draftSnapshot["through_event_id"]))
	published := h.publish(d, "published later")
	pubSnapshot := h.get(path)
	pubURL := path + "?as_of=" + str(pubSnapshot["as_of"]) + "&through_event_id=" + fmt.Sprint(num(pubSnapshot["through_event_id"]))
	ps := obj(published["submission"])
	revision := h.patch(str(ps["id"]), Object{"expected_version": ps["version"], "patch": Object{"counts": Object{"qualified_qty": 90, "defect_qty": 10}}, "reason": "draft reason"})
	revSnapshot := h.get(path)
	revURL := path + "?as_of=" + str(revSnapshot["as_of"]) + "&through_event_id=" + fmt.Sprint(num(revSnapshot["through_event_id"]))
	revised := h.publish(revision, "publication reason")
	rs := obj(revised["submission"])
	h.write("/submissions/"+str(rs["id"])+"/cancel", Object{"expected_version": rs["version"], "reason": "cancelled later"})
	for url, want := range map[string]Object{earlyURL: draftSnapshot, pubURL: pubSnapshot, revURL: revSnapshot} {
		got := h.get(url)
		if stringify(got) != stringify(want) {
			t.Fatalf("historical view changed\n%s\n%s", stringify(want), stringify(got))
		}
	}
	firstGroup := obj(draftSnapshot["groups"].([]any)[0])
	if firstGroup["batch_mode"] != "WHOLE" || num(firstGroup["version"]) != 2 {
		t.Fatal(firstGroup)
	}
	oldDraft := obj(obj(firstGroup["submissions"].([]any)[0])["draft_revision"])
	if oldDraft["published_at"] != nil || oldDraft["reason"] != "" {
		t.Fatal("future publication leaked", oldDraft)
	}
	current := h.get(path)
	cg := obj(current["groups"].([]any)[0])
	if num(cg["cancelled_count"]) != 1 || num(obj(cg["subtotal"])["submission_count"]) != 0 {
		t.Fatal(current)
	}
	status, m, _ := h.call("GET", path+"?as_of=2000-01-01T00:00:00Z", "", nil)
	if status != 422 || obj(m["details"])["reason"] != "history_unavailable" {
		t.Fatal(status, m)
	}
	_ = s
}
func TestOperationRangeReports(t *testing.T) {
	h := newHarness(t)
	lot1 := h.lot("RANGE-1", "M")
	lot2 := h.lot("RANGE-2", "M")
	a := h.group(lot1, "a", "NORMAL")
	b := h.group(lot2, "a", "NORMAL")
	sg := h.group(lot1, "a", "SAMPLING")
	h.publish(h.sub(a, "a", content(100, 96, 4)), "")
	h.publish(h.sub(b, "a", content(200, 180, 20)), "")
	h.sub(a, "draft", content(999, 999, 0))
	cancel := h.sub(a, "cancel", content(10, 10, 0))
	cs := obj(cancel["submission"])
	h.write("/submissions/"+str(cs["id"])+"/cancel", Object{"expected_version": cs["version"], "reason": "test"})
	h.publish(h.sub(sg, "a", content(10, 9, 1)), "")
	h.sub(a, "unknown-day", Object{})
	base := "/reports/operations/a?from=2026-11-03&to=2026-11-03"
	r := h.get(base + "&page_size=1")
	if num(r["total"]) != 4 || len(r["items"].([]any)) != 1 || num(r["unknown_day_count"]) != 1 {
		t.Fatal(r)
	}
	sum := obj(r["summary"])
	total := obj(sum["total"])
	if num(obj(obj(total["counts"])["total_qty"])["known_sum"]) != 300 || num(total["submission_count"]) != 2 {
		t.Fatal(r)
	}
	one := h.get(base + "&product_lot_id=" + lot1)
	if num(one["total"]) != 3 || num(obj(obj(obj(obj(one["summary"])["total"])["counts"])["total_qty"])["known_sum"]) != 100 {
		t.Fatal(one)
	}
	all := h.get(base + "&inspection_kind=ALL")
	if _, mixed := obj(all["summary"])["total"]; mixed || len(obj(all["summary"])["totals"].([]any)) != 2 {
		t.Fatal(all)
	}
	system := h.get("/reports/operations/a?date_basis=created_at&from=2000-01-01T00:00:00Z&to=9999-01-01T00:00:00Z")
	if num(system["total"]) != 5 {
		t.Fatal(system)
	}
	for _, query := range []string{"?from=2026-11-03", "?from=2026-11-04&to=2026-11-03", "?from=2026-11-03&to=2026-11-03&state=DRAFT"} {
		status, _, _ := h.call("GET", "/reports/operations/a"+query, "", nil)
		if status != 400 && status != 422 {
			t.Fatal(query, status)
		}
	}
}
func TestResetPreservesAccountsAndReferences(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("CLEAR", "M")
	g := h.group(lot, "a", "NORMAL")
	d := h.publish(h.sub(g, nil, content(1, 1, 0)), "")
	s := obj(d["submission"])
	h.patch(str(s["id"]), Object{"expected_version": s["version"], "patch": Object{"remark": "revision"}})
	if e := New(h.db).ResetTestData(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"inspection_submissions", "submission_revisions", "product_lots", "operation_groups", "audit_logs", "v2_idempotency_requests"} {
		var n int
		if e := h.db.Get(&n, "SELECT COUNT(*) FROM "+table); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	var hash string
	h.db.Get(&hash, "SELECT password_hash FROM users WHERE id = 'actor'")
	if hash != "unchanged" {
		t.Fatal(hash)
	}
	var n int
	h.db.Get(&n, "SELECT COUNT(*) FROM dict_items")
	if n != 3 {
		t.Fatal(n)
	}
	for _, table := range []string{"forms", "form_lines", "form_history", "legacy_migration_candidates", "idempotency_requests"} {
		var n int
		if e := h.db.Get(&n, "SELECT COUNT(*) FROM "+table); e == nil {
			t.Fatal("legacy table survives", table)
		}
	}
	// Old implementations and switches are removed from the source tree as well as routing.
	if strings.Contains(fmt.Sprint(h.get("/form-fields")), "legacy") {
		t.Fatal("legacy catalogue")
	}
}

func TestHistoricalGroupClosureAndTypePartitions(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("GROUP-HISTORY", "M")
	g := h.group(lot, "a", "NORMAL")
	sampling := h.group(lot, "a", "SAMPLING")
	d := h.publish(h.sub(g, nil, content(100, 96, 4)), "")
	h.publish(h.sub(sampling, nil, content(10, 9, 1)), "")
	s := obj(d["submission"])
	h.patch(str(s["id"]), Object{"expected_version": s["version"], "patch": Object{"remark": "unpublished revision"}})
	group := h.get("/operation-groups/" + g)
	closed := h.write("/operation-groups/"+g+"/close", Object{"expected_version": group["version"], "reason": "closed"})
	path := "/reports/product-lots/" + lot + "?inspection_kind=ALL"
	snapshot := h.get(path)
	pinned := path + "&as_of=" + str(snapshot["as_of"]) + "&through_event_id=" + fmt.Sprint(num(snapshot["through_event_id"]))
	h.write("/operation-groups/"+g+"/reopen", Object{"expected_version": closed["version"], "reason": "reopen"})
	again := h.get(pinned)
	if stringify(snapshot) != stringify(again) {
		t.Fatal("old group state changed")
	}
	if len(again["groups"].([]any)) != 2 {
		t.Fatal(again)
	}
	for _, v := range again["groups"].([]any) {
		g := obj(v)
		if g["inspection_kind"] == "NORMAL" && (g["reporting_state"] != "CLOSED" || num(g["revision_draft_count"]) != 1) {
			t.Fatal(g)
		}
		if g["inspection_kind"] == "SAMPLING" && g["reporting_state"] != "OPEN" {
			t.Fatal(g)
		}
	}
}
