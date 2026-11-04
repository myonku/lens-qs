package ledger

import (
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/migrations"
	"strings"

	"context"
	"testing"
)

func TestLegacyReviewMigration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, e := h.db.Exec(`INSERT INTO forms(id,form_no,operation_code,sample_raw,total_qty,qualified_qty,defect_qty,paper_inspected_at) VALUES('legacy-one','OLD-001','a','原始型号 原文',10,9,1,'2026.11.3')`)
	if e != nil {
		t.Fatal(e)
	}
	inventory, e := New(h.db).Inventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if num(obj(obj(inventory)["counts"])["forms"]) != 1 {
		t.Fatal(inventory)
	}
	svc := New(h.db)
	if _, _, _, e = svc.Stage(ctx, "actor", "stage"); e != nil {
		t.Fatal(e)
	}
	var n int
	h.db.Get(&n, "SELECT COUNT(*) FROM submission_slots")
	if n != 0 {
		t.Fatal("stage reserved slot")
	}
	var candidate, fingerprint string
	if e = h.db.QueryRow("SELECT candidate,source_fingerprint FROM legacy_migration_candidates WHERE source_id = 'legacy-one'").Scan(&candidate, &fingerprint); e != nil {
		t.Fatal(e)
	}
	review := Object{"source_id": "legacy-one", "source_fingerprint": fingerprint, "shared_form_no": "OLD-001", "sample_identity": "人工确认完整身份", "operation_id": "a", "inspection_kind": "NORMAL", "batch_no": nil, "counts_confirmed": true, "reason": "已核对纸单与零值"}
	body := []byte(stringify(Object{"items": []any{review}}))
	first, _, _, e := svc.ImportReviewed(ctx, "actor", "review", body)
	if e != nil {
		t.Fatal(e)
	}
	response, _ := decode(first)
	d := obj(obj(response["data"])["items"].([]any)[0])
	sub := obj(d["submission"])
	if sub["state"] != "DRAFT" || sub["current_revision_id"] != nil {
		t.Fatal(d)
	}
	c := obj(obj(d["draft_revision"])["content"])
	if c["paper_inspected_date"] != "2026-11-03" || c["paper_inspected_raw"] != "2026.11.3" {
		t.Fatal(c)
	}
	if _, _, _, e = svc.ImportReviewed(ctx, "actor", "review-again", body); e != nil {
		t.Fatal(e)
	}
	h.db.Get(&n, "SELECT COUNT(*) FROM inspection_submissions")
	if n != 1 {
		t.Fatal("duplicate import", n)
	}
	var hash string
	h.db.Get(&hash, "SELECT password_hash FROM users WHERE id = 'actor'")
	if hash != "unchanged" {
		t.Fatal("account changed")
	}
	h.db.Get(&n, "SELECT COUNT(*) FROM forms")
	if n != 1 {
		t.Fatal("source removed")
	}
	if _, e = h.db.Exec("UPDATE forms SET remark = 'source changed' WHERE id = 'legacy-one'"); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = svc.ImportReviewed(ctx, "actor", "stale-review", body); e == nil {
		t.Fatal("accepted stale review")
	}
}
func TestDateClassification(t *testing.T) {
	for raw, want := range map[string]string{"2026-11-03": "date", "2026.11.3": "date", "2026/11/3": "date", "2026-11-03T00:30:00+08:00": "instant", "2026-02-30": "invalid", "2026.2.30": "invalid", "03/04/26": "ambiguous", "2026-11-03T00:00:00.1Z": "ambiguous", "": "missing"} {
		if got := ClassifyLegacyDate(raw); got["classification"] != want || got["raw"] != raw {
			t.Fatal(raw, got)
		}
	}
}
func TestResultStoreFailureRollsBackBusiness(t *testing.T) {
	h := newHarness(t)
	if h.db.DriverName() == "pgx" {
		_, e := h.db.Exec("ALTER TABLE v2_idempotency_requests ADD CONSTRAINT reject_result CHECK(idempotency_key <> 'fail-result')")
		if e != nil {
			t.Fatal(e)
		}
	} else {
		_, e := h.db.Exec(`CREATE TRIGGER reject_result BEFORE INSERT ON v2_idempotency_requests WHEN NEW.idempotency_key = 'fail-result' BEGIN SELECT RAISE(ABORT, 'result failed'); END`)
		if e != nil {
			t.Fatal(e)
		}
	}
	status, m, _ := h.call("POST", "/product-lots", "fail-result", Object{"shared_form_no": "ROLLBACK", "sample_identity": "M"})
	if status != 503 {
		t.Fatal(status, m)
	}
	for _, table := range []string{"product_lots", "audit_logs", "v2_idempotency_requests"} {
		var n int
		if e := h.db.Get(&n, "SELECT COUNT(*) FROM "+table); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
func TestPatchArraysAndVersionBoundaries(t *testing.T) {
	h := newHarness(t)
	lot := h.lot("PATCH", "M")
	g := h.group(lot, "a", "NORMAL")
	d := h.publish(h.sub(g, nil, content(100, 96, 4)), "")
	sub := obj(d["submission"])
	key := str(sub["id"])
	patch := Object{"expected_version": sub["version"], "patch": Object{"paper_inspected_date": "2026-11-04"}}
	status, m, _ := h.call("PATCH", "/submissions/"+key+"/draft", "bad-date-switch", patch)
	if status != 422 {
		t.Fatal(status, m)
	}
	d = h.patch(key, Object{"expected_version": sub["version"], "patch": Object{"paper_inspected_at": nil, "paper_inspected_date": "2026-11-04", "lines": []any{Object{"seq": 1}}, "counts": nil}})
	c := obj(obj(d["draft_revision"])["content"])
	if obj(c["counts"])["total_qty"] != nil || obj(obj(obj(c["lines"].([]any)[0])["defects"])["prick"])["concave"] != nil {
		t.Fatal(c)
	}
	ds := obj(d["submission"])
	status, m, _ = h.call("POST", "/submissions/"+key+"/publish", "revision-no-reason", Object{"expected_version": ds["version"], "draft_revision_id": ds["draft_revision_id"], "reason": ""})
	if status != 422 {
		t.Fatal(status, m)
	}
	status, m, _ = h.call("PATCH", "/submissions/"+key+"/draft", "stale-version", Object{"expected_version": sub["version"], "patch": Object{"remark": "stale"}})
	requireReason(t, status, m, "version_conflict")
	status, m, _ = h.call("PATCH", "/submissions/"+key+"/draft", "identity", Object{"expected_version": ds["version"], "operation_group_id": g})
	if status != 400 {
		t.Fatal(status, m)
	}
	// An existing revision draft does not prevent closing, and publication still works after close.
	group := h.get("/operation-groups/" + g)
	h.write("/operation-groups/"+g+"/close", Object{"expected_version": group["version"], "reason": "结束"})
	h.publish(d, "数量重新核对")
}

func TestAdditiveMigrationPreservesLegacy(t *testing.T) {
	h := newHarness(t)
	_, e := h.db.Exec("INSERT INTO forms(id,form_no,sample_raw) VALUES('preserved','preserved-no','unchanged evidence')")
	if e != nil {
		t.Fatal(e)
	}
	driver := "sqlite"
	if h.db.DriverName() == "pgx" {
		driver = "postgres"
	}
	raw, e := migrations.FS.ReadFile(driver + "/0005_v2." + driver + ".down.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, stmt := range strings.Split(string(raw), ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, e = h.db.Exec(stmt); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = h.db.Exec("DELETE FROM schema_migrations WHERE version = 5"); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Migrate(context.Background(), h.db, driver); e != nil {
		t.Fatal(e)
	}
	var got string
	h.db.Get(&got, "SELECT sample_raw FROM forms WHERE id = 'preserved'")
	if got != "unchanged evidence" {
		t.Fatal(got)
	}
	h.db.Get(&got, "SELECT password_hash FROM users WHERE id = 'actor'")
	if got != "unchanged" {
		t.Fatal("account changed")
	}
	if driver == "postgres" {
		var n int
		if e = h.db.Get(&n, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'submission_revisions' AND column_name IN ('paper_inspected_date','paper_inspected_at','paper_inspected_day','saved_at') AND data_type = 'text'`); e != nil || n != 4 {
			t.Fatal(n, e)
		}
	} else {
		rows, e := h.db.Query("PRAGMA table_info(submission_revisions)")
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		for rows.Next() {
			var seq, notnull, pk int
			var name, typ string
			var def any
			if e = rows.Scan(&seq, &name, &typ, &notnull, &def, &pk); e != nil {
				t.Fatal(e)
			}
			if contains([]string{"paper_inspected_date", "paper_inspected_at", "paper_inspected_day", "saved_at"}, name) && typ != "TEXT" {
				t.Fatal(name, typ)
			}
		}
	}
}
