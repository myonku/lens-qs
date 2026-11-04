package ledger

import "testing"

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
