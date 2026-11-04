package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/users"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

type harness struct {
	t  *testing.T
	db *sqlx.DB
	r  *gin.Engine
	n  int
}

func newHarness(t *testing.T) *harness {
	if os.Getenv("LENS_TEST_POSTGRES_DSN") != "" {
		return postgresHarness(t)
	}
	return harnessDB(t, store.NewTestDB(t))
}
func postgresHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("LENS_TEST_POSTGRES_DSN")
	admin, e := database.Open("postgres", dsn, 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	schema := "accept_" + strings.ReplaceAll(id.New(), "-", "")
	if _, e = admin.Exec("CREATE SCHEMA " + schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	parsed, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, e := database.Open("postgres", parsed.String(), 8, 4)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if _, e = store.Migrate(context.Background(), db, "postgres"); e != nil {
		t.Fatal(e)
	}
	if applied, e := store.Migrate(context.Background(), db, "postgres"); e != nil || len(applied) != 0 {
		t.Fatal(applied, e)
	}
	return harnessDB(t, db)
}
func harnessDB(t *testing.T, db *sqlx.DB) *harness {
	t.Helper()
	_, e := db.Exec(`INSERT INTO users(id,username,password_hash) VALUES('actor','tester','unchanged')`)
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range []string{"a", "b", "c"} {
		_, e = db.Exec(db.Rebind(`INSERT INTO dict_items(id,category,code,label,active) VALUES(?, 'inspection_operation', ?, ?, 1)`), op, op, op)
		if e != nil {
			t.Fatal(e)
		}
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(db).Register(r.Group("/api/v2", func(c *gin.Context) { c.Set(middleware.CtxUserID, "actor") }))
	return &harness{t: t, db: db, r: r}
}
func (h *harness) call(method, path, key string, req any) (int, Object, *httptest.ResponseRecorder) {
	h.t.Helper()
	var b []byte
	if s, ok := req.(string); ok {
		b = []byte(s)
	} else if req != nil {
		b, _ = json.Marshal(req)
	}
	r := httptest.NewRequest(method, "/api/v2"+path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, r)
	m, e := decode(w.Body.Bytes())
	if e != nil {
		h.t.Fatalf("invalid response %d: %s", w.Code, w.Body)
	}
	return w.Code, m, w
}
func (h *harness) write(path string, req any) Object {
	h.t.Helper()
	h.n++
	s, m, _ := h.call("POST", path, fmt.Sprint(h.n), req)
	if s != 200 && s != 201 {
		h.t.Fatalf("POST %s: %d %v", path, s, m)
	}
	return obj(m["data"])
}
func (h *harness) patch(key string, req Object) Object {
	h.t.Helper()
	h.n++
	s, m, _ := h.call("PATCH", "/submissions/"+key+"/draft", fmt.Sprint(h.n), req)
	if s != 200 {
		h.t.Fatalf("PATCH: %d %v", s, m)
	}
	return obj(m["data"])
}
func (h *harness) get(path string) Object {
	h.t.Helper()
	s, m, _ := h.call("GET", path, "", nil)
	if s != 200 {
		h.t.Fatalf("GET %s: %d %v", path, s, m)
	}
	return obj(m["data"])
}
func (h *harness) lot(no, identity string) string {
	return str(h.write("/product-lots", Object{"shared_form_no": no, "sample_identity": identity})["id"])
}
func (h *harness) group(lot, op, kind string) string {
	return str(h.write("/product-lots/"+lot+"/operation-groups", Object{"operation_id": op, "inspection_kind": kind})["id"])
}
func (h *harness) sub(group string, batch any, c Object) Object {
	return h.write("/submissions", Object{"operation_group_id": group, "batch_no": batch, "content": c})
}
func (h *harness) publish(d Object, reason string) Object {
	s := obj(d["submission"])
	return h.write("/submissions/"+str(s["id"])+"/publish", Object{"expected_version": s["version"], "draft_revision_id": s["draft_revision_id"], "reason": reason})
}
func content(t, q, d any) Object {
	return Object{"counts": Object{"total_qty": t, "qualified_qty": q, "defect_qty": d}, "paper_inspected_at": "2026-11-03T00:30:00+08:00", "sample_raw": "M2 原文", "paper_unit_text": "一厂", "paper_inspector": "张三", "lines": []any{Object{"seq": 1, "defects": Object{"prick": Object{"concave": 2}}}}}
}
func requireReason(t *testing.T, status int, m Object, want string) {
	t.Helper()
	if status != 409 || obj(m["details"])["reason"] != want {
		t.Fatalf("want 409 %s: %d %v", want, status, m)
	}
}
func TestLifecycle(t *testing.T) { exerciseLifecycle(t, newHarness(t)) }
func exerciseLifecycle(t *testing.T, h *harness) {
	lot := h.lot(" QS-001 ", "M2 完整型号")
	h.lot("QS-001", "M2 另一型号")
	h.lot("QS-002", "M2 完整型号")
	status, m, _ := h.call("POST", "/product-lots", "dup", Object{"shared_form_no": "QS-001", "sample_identity": " M2 完整型号 "})
	requireReason(t, status, m, "product_lot_exists")
	a := h.group(lot, "a", "NORMAL")
	b := h.group(lot, "b", "NORMAL")
	sampling := h.group(lot, "a", "SAMPLING")
	bd := h.publish(h.sub(b, "c", content(700, 690, 10)), "")
	h.publish(h.sub(b, "d", content(300, 295, 5)), "")
	ad := h.publish(h.sub(a, "a", content(400, 390, 10)), "")
	h.publish(h.sub(a, "b", content(300, 295, 5)), "")
	h.publish(h.sub(a, "c", content(300, 290, 10)), "")
	total := h.get("/stats/summary?product_lot_id=" + lot)
	if num(obj(total["total"])["submission_count"]) != 5 || len(total["included_revision_ids"].([]any)) != 5 {
		t.Fatal(total)
	}
	sub := obj(ad["submission"])
	draft := h.patch(str(sub["id"]), Object{"expected_version": sub["version"], "patch": Object{"counts": Object{"qualified_qty": 380, "defect_qty": 20}}})
	before := h.get("/stats/summary?operation_group_id=" + a)
	if num(obj(obj(obj(before["total"])["counts"])["qualified_qty"])["known_sum"]) != 975 {
		t.Fatal(before)
	}
	revised := h.publish(draft, "修正误录")
	after := h.get("/stats/summary?operation_group_id=" + a)
	if num(obj(obj(obj(after["total"])["counts"])["qualified_qty"])["known_sum"]) != 965 {
		t.Fatal(after)
	}
	bdetail := h.get("/submissions/" + str(obj(bd["submission"])["id"]))
	if str(obj(bdetail["submission"])["current_revision_id"]) != str(obj(bd["submission"])["current_revision_id"]) {
		t.Fatal("downstream changed")
	}
	c := obj(obj(revised["current_revision"])["content"])
	if c["paper_inspected_at"] != "2026-11-02T16:30:00Z" || c["paper_inspected_day"] != "2026-11-03" {
		t.Fatal(c)
	}
	diff := h.get("/submissions/" + str(sub["id"]) + "/diff?from=" + str(sub["current_revision_id"]) + "&to=" + str(obj(revised["submission"])["current_revision_id"]))
	if len(diff["changes"].([]any)) != 2 {
		t.Fatal(diff)
	}
	sd := h.publish(h.sub(sampling, "a", content(10, 9, 1)), "")
	all := h.get("/stats/summary?inspection_kind=ALL&product_lot_id=" + lot)
	if _, ok := all["total"]; ok {
		t.Fatal("mixed total")
	}
	if len(all["totals"].([]any)) != 2 {
		t.Fatal(all)
	}
	sr := obj(sd["submission"])
	h.write("/submissions/"+str(sr["id"])+"/cancel", Object{"expected_version": sr["version"], "reason": "撤销抽检"})
	normal := h.get("/stats/summary?product_lot_id=" + lot)
	if num(obj(normal["total"])["submission_count"]) != 5 {
		t.Fatal(normal)
	}
	history := h.get("/submissions/" + str(sub["id"]) + "/revisions")
	if num(history["total"]) != 2 {
		t.Fatal(history)
	}
}
func TestSlotsAndAtomicBatch(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("S", "M")
	g := h.group(lot, "a", "NORMAL")
	sg := h.group(lot, "a", "SAMPLING")
	d := h.sub(g, "  ", Object{})
	sub := obj(d["submission"])
	if sub["batch_no"] != nil || sub["scope"] != "WHOLE" {
		t.Fatal(sub)
	}
	h.sub(sg, nil, Object{})
	for i, b := range []any{nil, "", "  ", "a"} {
		status, m, _ := h.call("POST", "/submissions", fmt.Sprint("duplicate", i), Object{"operation_group_id": g, "batch_no": b})
		want := "whole_submission_exists"
		if b == "a" {
			want = "batch_scope_conflict"
		}
		requireReason(t, status, m, want)
	}
	group := h.get("/operation-groups/" + g)
	status, m, _ := h.call("POST", "/operation-groups/"+g+"/close", "close-draft", Object{"expected_version": group["version"], "reason": "结束"})
	requireReason(t, status, m, "unfinished_submissions")
	h.write("/submissions/"+str(sub["id"])+"/cancel", Object{"expected_version": sub["version"], "reason": "改为分批"})
	a := h.sub(g, "a", Object{})
	h.sub(g, "b", Object{})
	as := obj(a["submission"])
	h.write("/submissions/"+str(as["id"])+"/cancel", Object{"expected_version": as["version"], "reason": "取消"})
	status, m, _ = h.call("POST", "/submissions", "still-split", Object{"operation_group_id": g})
	requireReason(t, status, m, "batch_scope_conflict")
	sd := h.get("/submissions?operation_group_id=" + sg)
	h.publish(obj(sd["items"].([]any)[0]), "")
	group = h.get("/operation-groups/" + sg)
	closed := h.write("/operation-groups/"+sg+"/close", Object{"expected_version": group["version"], "reason": "结束"})
	status, m, _ = h.call("POST", "/submissions", "closed-create", Object{"operation_group_id": sg})
	requireReason(t, status, m, "operation_group_closed")
	h.write("/operation-groups/"+sg+"/reopen", Object{"expected_version": closed["version"], "reason": "继续"})
	cg := h.group(lot, "c", "NORMAL")
	status, m, _ = h.call("POST", "/submissions/batch", "atomic", Object{"items": []any{Object{"operation_group_id": cg, "batch_no": "a"}, Object{"operation_group_id": cg}}})
	requireReason(t, status, m, "batch_scope_conflict")
	if num(obj(m["details"])["index"]) != 1 {
		t.Fatal(m)
	}
	empty := h.get("/operation-groups/" + cg)
	if empty["batch_mode"] != "EMPTY" || num(empty["registered_count"]) != 0 {
		t.Fatal(empty)
	}
}
func TestIdempotency(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("I", "M")
	g := h.group(lot, "a", "NORMAL")
	req := Object{"operation_group_id": g}
	status, m, w := h.call("POST", "/submissions", "one", req)
	if status != 201 {
		t.Fatal(m)
	}
	first := w.Body.String()
	s := obj(obj(m["data"])["submission"])
	status, m, w = h.call("POST", "/submissions", "one", req)
	if status != 201 || w.Header().Get("X-Idempotent-Replay") != "true" || w.Body.String() != first {
		t.Fatal(status, m)
	}
	status, m, _ = h.call("POST", "/submissions", "one", Object{"operation_group_id": g, "batch_no": "x"})
	requireReason(t, status, m, "idempotency_key_reused")
	patch := Object{"expected_version": s["version"], "patch": Object{"remark": "保存"}}
	path := "/submissions/" + str(s["id"]) + "/draft"
	status, m, _ = h.call("PATCH", path, "patch-key", patch)
	if status != 200 {
		t.Fatal(m)
	}
	status, m, w = h.call("PATCH", path, "patch-key", patch)
	if status != 200 || w.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatal(m)
	}
	status, m, _ = h.call("PATCH", "/submissions/nonexistent/draft", "patch-key", patch)
	requireReason(t, status, m, "idempotency_key_reused")
	_, e := h.db.Exec("DROP TABLE v2_idempotency_requests")
	if e != nil {
		t.Fatal(e)
	}
	status, _, _ = h.call("POST", "/product-lots", "storage-down", Object{"shared_form_no": "NEVER", "sample_identity": "M"})
	if status != 503 {
		t.Fatal(status)
	}
	var n int
	if e = h.db.Get(&n, "SELECT COUNT(*) FROM product_lots WHERE shared_form_no = 'NEVER'"); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestUnknownAndValidation(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("V", "M")
	g := h.group(lot, "a", "NORMAL")
	for i, patch := range []Object{{"counts": Object{"total_qty": -1}}, {"counts": Object{"total_qty": 1.2}}, {"paper_inspected_date": "2026-02-30"}, {"paper_inspected_at": "2026-11-03T00:00:00.001Z"}, {"paper_inspected_at": "2026-11-03T00:00:00"}, {"paper_inspected_date": "2026-11-03", "paper_inspected_at": "2026-11-03T00:00:00Z"}, {"lines": nil}, {"unknown": 1}} {
		status, m, _ := h.call("POST", "/submissions", fmt.Sprint("bad", i), Object{"content": patch})
		if status != 400 && status != 422 {
			t.Fatal(i, status, m)
		}
	}
	a := h.publish(h.sub(g, "a", Object{"counts": Object{"qualified_qty": 0, "defect_qty": 0}, "quantity_changes": []any{Object{"kind": "LOSS", "direction": "DECREASE", "quantity": nil, "reason": "破损待核对"}}}), "")
	h.publish(h.sub(g, "b", Object{"counts": Object{"total_qty": 0, "qualified_qty": 0, "defect_qty": 0}}), "")
	sum := h.get("/stats/summary?operation_group_id=" + g)
	total := obj(sum["total"])
	c := obj(obj(total["counts"])["total_qty"])
	if c["value"] != nil || num(c["unknown_count"]) != 1 || total["pass_rate"] != nil {
		t.Fatal(sum)
	}
	changes := h.get("/stats/quantity-changes?operation_group_id=" + g)
	if num(obj(changes["items"].([]any)[0])["unknown_count"]) != 1 {
		t.Fatal(changes)
	}
	s := obj(a["submission"])
	d := h.patch(str(s["id"]), Object{"expected_version": s["version"], "patch": Object{"counts": Object{}, "remark": "补记"}})
	if len(obj(obj(d["draft_revision"])["content"])["quantity_changes"].([]any)) != 1 {
		t.Fatal(d)
	}
	detached := h.write("/submissions", Object{"inspection_kind": "SAMPLING", "batch_no": "x"})
	ds := obj(detached["submission"])
	status, m, _ := h.call("PATCH", "/submissions/"+str(ds["id"])+"/draft", "mismatch", Object{"expected_version": ds["version"], "operation_group_id": g})
	if status != 422 || obj(m["details"])["reason"] != "inspection_kind_mismatch" {
		t.Fatal(status, m)
	}
	unassigned := h.write("/submissions", Object{})
	us := obj(unassigned["submission"])
	h.patch(str(us["id"]), Object{"expected_version": us["version"], "operation_group_id": g, "batch_no": "c"})
	for _, path := range []string{"/stats/summary?group_by=bogus", "/stats/summary?unexpected=1", "/submissions?page_size=0", "/submissions?state=bogus", "/product-lots?x=1"} {
		status, _, _ = h.call("GET", path, "", nil)
		if status != 400 {
			t.Fatal(path, status)
		}
	}
	dates := h.get("/stats/summary?operation_group_id=" + g + "&from=2026-11-03&to=2026-11-03")
	if num(dates["unknown_day_count"]) != 2 || num(obj(dates["total"])["submission_count"]) != 0 {
		t.Fatal(dates)
	}
}
func TestSamplingLoss(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("SAM", "M")
	g := h.group(lot, "a", "SAMPLING")
	d := h.publish(h.sub(g, nil, Object{"quantity_changes": []any{Object{"kind": "LOSS", "direction": "DECREASE", "quantity": 4, "reason": "原始证据保留"}, Object{"kind": "OTHER", "direction": "UNKNOWN", "reason": "核对"}}}), "")
	if len(obj(obj(d["current_revision"])["content"])["quantity_changes"].([]any)) != 2 {
		t.Fatal(d)
	}
	stats := h.get("/stats/quantity-changes?inspection_kind=SAMPLING")
	items := stats["items"].([]any)
	if len(items) != 1 || obj(items[0])["kind"] != "OTHER" {
		t.Fatal(stats)
	}
}
func TestConcurrency(t *testing.T) { h := newHarness(t); h.db.SetMaxOpenConns(4); competition(t, h) }
func competition(t *testing.T, h *harness) {
	lot := h.lot("RACE", "M")
	g := h.group(lot, "a", "NORMAL")
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i, b := range []any{nil, "a"} {
		wg.Add(1)
		go func(i int, b any) {
			defer wg.Done()
			status, _, _ := h.call("POST", "/submissions", fmt.Sprint("race", i), Object{"operation_group_id": g, "batch_no": b})
			statuses <- status
		}(i, b)
	}
	wg.Wait()
	close(statuses)
	success, conflict := 0, 0
	for status := range statuses {
		if status == 201 {
			success++
		}
		if status == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
func TestPostgres(t *testing.T) {
	if os.Getenv("LENS_TEST_POSTGRES_DSN") == "" {
		t.Skip("LENS_TEST_POSTGRES_DSN not set")
	}
	h := postgresHarness(t)
	exerciseLifecycle(t, h)
	competition(t, h)
}

func TestCatalogQueriesAndDefectCompleteness(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("CAT", "M")
	g := h.group(lot, "a", "NORMAL")
	h.publish(h.sub(g, "a", content(100, 95, 5)), "")
	for _, path := range []string{"/form-fields", "/form-templates", "/form-templates/a", "/reports/product-lots/" + lot, "/product-lots/" + lot, "/product-lots/" + lot + "/operation-groups", "/submissions?machine_no=unknown", "/stats/defects?operation_group_id=" + g, "/stats/quantity-changes?inspection_kind=ALL"} {
		h.get(path)
	}
	defects := h.get("/stats/defects?operation_group_id=" + g)
	items := defects["items"].([]any)
	if len(items) != 105 {
		t.Fatal(len(items))
	}
	first := obj(items[0])
	if num(first["known_sum"]) != 2 || num(first["unknown_count"]) != 0 {
		t.Fatal(first)
	}
	second := obj(items[1])
	if second["value"] != nil || num(second["unknown_count"]) != 1 {
		t.Fatal(second)
	}
	dates := h.get("/stats/summary?from=2026-11-03&to=2026-11-03&operation_group_id=" + g)
	if num(obj(dates["total"])["submission_count"]) != 1 {
		t.Fatal(dates)
	}
}
func TestConcurrentIdenticalRequests(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("SAME", "M")
	g := h.group(lot, "a", "NORMAL")
	var wg sync.WaitGroup
	responses := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, m, w := h.call("POST", "/submissions", "same-key", Object{"operation_group_id": g})
			if status != 201 {
				t.Errorf("%d %v", status, m)
			}
			responses <- w.Body.String()
		}()
	}
	wg.Wait()
	close(responses)
	a, b := <-responses, <-responses
	if a != b {
		t.Fatal("responses differ")
	}
	detail := h.get("/operation-groups/" + g)
	if num(detail["registered_count"]) != 1 {
		t.Fatal(detail)
	}
}

func TestSharedAccountsAndDictionary(t *testing.T) {
	h := newHarness(t)
	driver := "sqlite"
	if h.db.DriverName() == "pgx" {
		driver = "postgres"
	}
	us := users.NewStore(h.db, driver)
	u, e := us.Create(context.Background(), users.CreateInput{Username: "reused-account", Password: "test-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	authSvc := auth.NewService(h.db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, driver)
	login, e := authSvc.Login(context.Background(), "reused-account", "test-password", "", "")
	if e != nil {
		t.Fatal(e)
	}
	principal, e := authSvc.Authenticate(login.Token)
	if e != nil || principal.UserID != u.ID || !principal.IsAdmin {
		t.Fatal(principal, e)
	}
	dict := dictionary.NewStore(h.db, driver)
	op, e := dict.UpsertItem(context.Background(), &dictionary.Item{Category: dictionary.CategoryInspectionOp, Code: "NEW", Label: "新增工序", Active: true})
	if e != nil {
		t.Fatal(e)
	}
	lot := h.lot("REUSE", "M")
	h.group(lot, op.ID, "NORMAL")
}

func TestAdvisoryDifferencesDoNotBlockPublication(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("WARN", "M")
	g := h.group(lot, "a", "NORMAL")
	c := content(10, 9, 1)
	c["declared_counts"] = Object{"total_qty": 20}
	for _, field := range []string{"sample_project", "sample_model", "sample_spec", "sample_size", "sample_side"} {
		c[field] = "unmatched"
	}
	d := h.publish(h.sub(g, nil, c), "")
	rules := map[string]bool{}
	for _, v := range d["issues"].([]any) {
		rules[str(obj(v)["rule"])] = true
	}
	for _, rule := range []string{"declared_count_difference", "defect_overlap_possible", "sample_reference_unmatched"} {
		if !rules[rule] {
			t.Fatal(rule, d)
		}
	}
	if obj(d["submission"])["state"] != "PUBLISHED" {
		t.Fatal(d)
	}
}
