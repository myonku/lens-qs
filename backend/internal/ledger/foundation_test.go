package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/store"
)

type fixture struct {
	db               *sqlx.DB
	t                *testing.T
	n                int
	lot              ProductLot
	normal, sampling Group
}

func newFixture(t *testing.T) *fixture {
	db, _ := store.FixtureDB(t, true)
	f := &fixture{db: db, t: t}
	for _, kind := range []string{"NORMAL", "SAMPLING"} {
		_, e := db.Exec(db.Rebind(`INSERT INTO users(id,username,display_name,password_hash,account_type,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`), kind, kind, "原姓名", "test-hash", kind, "2026-11-05T00:00:00Z", "2026-11-05T00:00:00Z")
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e := db.Exec(`INSERT INTO dict_items(id,category,code,label,active,created_at,updated_at) VALUES('op','inspection_operation','B','后序',1,'2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
	if e != nil {
		t.Fatal(e)
	}
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		l, e := r.CreateLot(" QS-001 ", " Model  X ", EmptyContent())
		f.lot = l
		return Mutation{Lot: &l}, e
	})
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		g, e := r.CreateGroup(f.lot.ID, "op")
		f.normal = g
		return Mutation{Group: &g}, e
	})
	f.run("SAMPLING", func(r *Repository) (Mutation, error) {
		g, e := r.CreateGroup(f.lot.ID, "op")
		f.sampling = g
		return Mutation{Group: &g}, e
	})
	return f
}
func (f *fixture) run(actor string, fn func(*Repository) (Mutation, error)) Mutation {
	f.t.Helper()
	f.n++
	m, _, e := Write(context.Background(), f.db, Request{ActorID: actor, Key: fmt.Sprint(f.n), Method: "POST", Path: "internal", Raw: []byte(fmt.Sprint(f.n))}, nil, fn)
	if e != nil {
		f.t.Fatal(e)
	}
	return m
}
func (f *fixture) save(in SaveInput) Detail {
	f.t.Helper()
	return f.run("NORMAL", func(r *Repository) (Mutation, error) { d, e := r.Save(in); return Mutation{Details: []Detail{d}}, e }).Details[0]
}
func count(t *testing.T, db *sqlx.DB, table string) int {
	t.Helper()
	var n int
	if e := db.Get(&n, `SELECT COUNT(*) FROM `+table); e != nil {
		t.Fatal(e)
	}
	return n
}

const paper = `{"paper_unit_text":"片","sample_raw":"  原始 型号  ","paper_inspector":"纸面甲","counts":{"total_qty":100,"qualified_qty":96,"defect_qty":4},"lines":[{"seq":1,"total_qty":90,"qualified_qty":85,"defect_qty":5}]}`

func TestG1IndependentGroupsHeaderAndLoss(t *testing.T) {
	f := newFixture(t)
	if f.normal.LotID != f.sampling.LotID || f.lot.SampleIdentity != "Model  X" {
		t.Fatal("identity semantics")
	}
	d := f.save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(paper)})
	if *d.Revision.Content.Counts.Qualified != 96 || *d.Derived.PassRate != 96 || *d.Derived.Differences["total_qty"].Difference != 10 || *d.Revision.Content.SampleRaw != "  原始 型号  " {
		t.Fatal(d)
	}
	set, e := CurrentSet(context.Background(), f.db, f.normal.ID)
	if e != nil || len(set) != 1 {
		t.Fatal(set, e)
	}
	newer := f.save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 1, Patch: []byte(`{"lines":[{"seq":2,"total_qty":7}]}`)})
	if *newer.Revision.Content.Counts.Total != 100 || newer.Revision.No != 2 {
		t.Fatal(newer)
	}
	var selected Content
	json.Unmarshal([]byte(set[0].ContentJSON), &selected)
	if *selected.Lines[0].Total != 90 {
		t.Fatal("fixed selection mutated")
	}
	before := count(t, f.db, "submission_revisions")
	_, _, e = Write(context.Background(), f.db, Request{ActorID: "SAMPLING", Key: "loss", Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
		_, e := r.Save(SaveInput{GroupID: &f.sampling.ID, Patch: []byte(`{"quantity_changes":[{"kind":"LOSS","direction":"DECREASE","reason":"原因"}]}`)})
		return Mutation{}, e
	})
	if e == nil || count(t, f.db, "submission_revisions") != before {
		t.Fatal("sampling LOSS wrote")
	}
	// The database itself rejects a current pointer to another submission's revision.
	other := f.save(SaveInput{Patch: []byte(`{"remark":"ungrouped"}`)})
	tx, _ := f.db.Beginx()
	_, e = tx.Exec(tx.Rebind(`UPDATE inspection_submissions SET current_revision_id=? WHERE id=?`), other.Revision.ID, d.Submission.ID)
	if e == nil {
		e = tx.Commit()
	} else {
		tx.Rollback()
	}
	if e == nil {
		t.Fatal("cross-submission pointer accepted")
	}
}
func TestG1BindCloseCorrectCancelAndReuse(t *testing.T) {
	f := newFixture(t)
	d := f.save(SaveInput{Patch: []byte(`{"remark":"first"}`)})
	f.save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 1, Patch: []byte(`{"sample_raw":"second"}`)})
	if count(t, f.db, "submission_slots") != 0 {
		t.Fatal("ungrouped occupancy")
	}
	d = f.save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 2, GroupID: &f.normal.ID, Patch: []byte(`{}`)})
	if count(t, f.db, "submission_slots") != 1 {
		t.Fatal("binding")
	}
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		g, e := r.GroupState(f.normal.ID, 2, true, "结束")
		return Mutation{Group: &g}, e
	})
	d = f.save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 3, Patch: []byte(`{"counts":{"total_qty":0}}`)})
	versions := count(t, f.db, "submission_revisions")
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		_, e := r.Cancel(d.Submission.ID, 4, "录错")
		return Mutation{}, e
	})
	if count(t, f.db, "submission_slots") != 0 || count(t, f.db, "submission_revisions") != versions {
		t.Fatal("cancel generated revision or kept occupancy")
	}
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		g, e := r.GroupState(f.normal.ID, 4, false, "继续")
		return Mutation{Group: &g}, e
	})
	f.save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(`{}`)})
}
func TestG1AtomicRetryBoundaryClockAndRollback(t *testing.T) {
	f := newFixture(t)
	req := Request{ActorID: "NORMAL", Key: "retry", Method: "POST", Path: "/submissions", Raw: []byte(paper)}
	clock := func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	fn := func(r *Repository) (Mutation, error) {
		d, e := r.Save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(paper)})
		return Mutation{Details: []Detail{d}}, e
	}
	m, replay, e := Write(context.Background(), f.db, req, clock, fn)
	if e != nil || replay {
		t.Fatal(e)
	}
	before := count(t, f.db, "submission_revisions")
	_, replay, e = Write(context.Background(), f.db, req, clock, func(*Repository) (Mutation, error) { t.Fatal("retry executed"); return Mutation{}, nil })
	if e != nil || !replay || count(t, f.db, "submission_revisions") != before {
		t.Fatal("idempotency", e)
	}
	var ids []int64
	f.db.Select(&ids, `SELECT event_id FROM business_events WHERE entity_id='`+f.normal.ID+`' ORDER BY event_id`)
	last := ids[len(ids)-1]
	if e = store.Boundary(context.Background(), f.db, last, "2030-01-01T00:00:00Z"); e == nil {
		t.Fatal("intermediate event accepted")
	}
	var boundary int64
	f.db.Get(&boundary, `SELECT MAX(last_event_id) FROM event_transactions WHERE domain=1`)
	if e = store.Boundary(context.Background(), f.db, boundary, "2030-01-01T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	req.Key = "rollback"
	req.Raw = []byte("failure")
	events := count(t, f.db, "business_events")
	_, _, e = Write(context.Background(), f.db, req, nil, func(r *Repository) (Mutation, error) {
		_, e := r.Save(SaveInput{ID: m.Details[0].Submission.ID, ExpectedVersion: 1, Patch: []byte(`{"remark":"rolled back"}`)})
		if e != nil {
			return Mutation{}, e
		}
		return Mutation{}, errors.New("injected failure after versions/events")
	})
	if e == nil || count(t, f.db, "submission_revisions") != before || count(t, f.db, "business_events") != events {
		t.Fatal("partial write")
	}
	req.Key = "clock-back"
	_, _, e = Write(context.Background(), f.db, req, nil, func(r *Repository) (Mutation, error) {
		d, e := r.Save(SaveInput{ID: m.Details[0].Submission.ID, ExpectedVersion: 1, Patch: []byte(`{"remark":"clock back"}`)})
		return Mutation{Details: []Detail{d}}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	var lastTime string
	f.db.Get(&lastTime, `SELECT last_event_time FROM write_gate WHERE id=1`)
	if lastTime != "2030-01-01T00:00:00Z" {
		t.Fatal(lastTime)
	}
}
func TestG1ConcurrentOccupancyAndBatchRollback(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, e := Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: fmt.Sprint("race", i), Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
				_, e := r.Save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(`{}`)})
				return Mutation{}, e
			})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 || count(t, f.db, "submission_slots") != 1 {
		t.Fatal("race", success)
	}
	before := count(t, f.db, "inspection_submissions")
	_, _, e := Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: "batch", Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
		if _, e := r.Save(SaveInput{Patch: []byte(`{}`)}); e != nil {
			return Mutation{}, e
		}
		_, e := r.Save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(`{}`)})
		return Mutation{}, e
	})
	if e == nil || count(t, f.db, "inspection_submissions") != before {
		t.Fatal("batch partial write")
	}
}

func TestG1SnapshotEvidenceAndMaintenanceReset(t *testing.T) {
	f := newFixture(t)
	d := f.save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(paper)})
	snapshot, e := store.ReadView(context.Background(), f.db, func(reader store.Reader, snapshot store.Snapshot) error {
		var current string
		if e := reader.GetContext(context.Background(), &current, reader.Rebind(`SELECT current_revision_id FROM inspection_submissions WHERE id=?`), d.Submission.ID); e != nil {
			return e
		}
		if current != d.Revision.ID {
			t.Fatal("wrong pinned revision")
		}
		return nil
	})
	if e != nil || snapshot.AsofEventID == 0 {
		t.Fatal(snapshot, e)
	}
	if _, e = f.db.Exec(`UPDATE users SET display_name='后来改名' WHERE id='NORMAL'`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE dict_items SET label='后来工序名' WHERE id='op'`); e != nil {
		t.Fatal(e)
	}
	var name, label string
	f.db.Get(&name, f.db.Rebind(`SELECT saved_actor_name FROM submission_revisions WHERE id=?`), d.Revision.ID)
	f.db.Get(&label, f.db.Rebind(`SELECT operation_label FROM operation_groups WHERE id=?`), f.normal.ID)
	if name != "原姓名" || label != "后序" {
		t.Fatal("historical evidence changed")
	}
	if _, e = f.db.Exec(`INSERT INTO sample_parse_cache(id,raw_key,raw_sample,verified_by,parsed,created_at,updated_at) VALUES('manual','manual','raw','NORMAL','{}','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	if e = store.ResetInspection(context.Background(), f.db); e != nil {
		t.Fatal(e)
	}
	if count(t, f.db, "inspection_submissions") != 0 || count(t, f.db, "users") != 2 || count(t, f.db, "sample_parse_cache") != 1 {
		t.Fatal("reset preservation")
	}
}
func TestG1AuthorizationBeforeRetryAndVersionConflict(t *testing.T) {
	f := newFixture(t)
	d := f.save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(paper)})
	before := count(t, f.db, "submission_revisions")
	for _, tc := range []struct {
		actor   string
		version int64
	}{{"SAMPLING", 1}, {"NORMAL", 8}} {
		_, _, e := Write(context.Background(), f.db, Request{ActorID: tc.actor, Key: tc.actor + "bad", Method: "PATCH", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
			_, e := r.Save(SaveInput{ID: d.Submission.ID, ExpectedVersion: tc.version, Patch: []byte(`{"remark":"wrong"}`)})
			return Mutation{}, e
		})
		if e == nil || count(t, f.db, "submission_revisions") != before {
			t.Fatal("forbidden write")
		}
	}
	// Same-body successful retry must still reject a now-disabled account.
	req := Request{ActorID: "NORMAL", Key: "auth-retry", Method: "POST", Path: "internal"}
	_, _, e := Write(context.Background(), f.db, req, nil, func(r *Repository) (Mutation, error) {
		_, e := r.Save(SaveInput{Patch: []byte(`{}`)})
		return Mutation{}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	f.db.Exec(`UPDATE users SET status='disabled' WHERE id='NORMAL'`)
	if _, _, e = Write(context.Background(), f.db, req, nil, func(*Repository) (Mutation, error) {
		t.Fatal("disabled account reached callback")
		return Mutation{}, nil
	}); e == nil {
		t.Fatal("disabled retry replayed")
	}
}

func TestG1IdentityAndSlotMatrix(t *testing.T) {
	f := newFixture(t)
	// Exact trim-only identity distinguishes case and internal spaces.
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		l, e := r.CreateLot("QS-001", "model  X", EmptyContent())
		if e == nil && l.ID == f.lot.ID {
			t.Fatal("case merged")
		}
		return Mutation{Lot: &l}, e
	})
	_, _, e := Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: "duplicate-lot", Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
		_, e := r.CreateLot("QS-001", "Model  X", EmptyContent())
		return Mutation{}, e
	})
	if e == nil {
		t.Fatal("duplicate identity replaced")
	}
	batch := " c "
	first := f.save(SaveInput{GroupID: &f.normal.ID, BatchNo: &batch, Patch: []byte(`{}`)})
	if first.Submission.BatchNo == nil || *first.Submission.BatchNo != "c" {
		t.Fatal("batch trim")
	}
	for i, in := range []SaveInput{{GroupID: &f.normal.ID, BatchNo: ptr("c"), Patch: []byte(`{}`)}, {GroupID: &f.normal.ID, Patch: []byte(`{}`)}} {
		_, _, e = Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: fmt.Sprint("slot-conflict", i), Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) { _, e := r.Save(in); return Mutation{}, e })
		if e == nil {
			t.Fatal("duplicate/scope conflict accepted")
		}
	}
	f.run("SAMPLING", func(r *Repository) (Mutation, error) {
		d, e := r.Save(SaveInput{GroupID: &f.sampling.ID, BatchNo: ptr("c"), Patch: []byte(`{}`)})
		return Mutation{Details: []Detail{d}}, e
	})
	f.run("NORMAL", func(r *Repository) (Mutation, error) {
		g, e := r.GroupState(f.normal.ID, 2, true, "结束")
		return Mutation{Group: &g}, e
	})
	unassigned := f.save(SaveInput{Patch: []byte(`{}`)})
	for i, in := range []SaveInput{{GroupID: &f.normal.ID, BatchNo: ptr("new"), Patch: []byte(`{}`)}, {ID: unassigned.Submission.ID, ExpectedVersion: 1, GroupID: &f.normal.ID, BatchNo: ptr("new"), BatchSet: true, Patch: []byte(`{}`)}} {
		_, _, e = Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: fmt.Sprint("closed", i), Method: "POST", Path: "internal"}, nil, func(r *Repository) (Mutation, error) { _, e := r.Save(in); return Mutation{}, e })
		if e == nil {
			t.Fatal("closed group accepted new/bind")
		}
	}
}

func TestG1WhitespaceEmptyPatchRejected(t *testing.T) {
	f := newFixture(t)
	d := f.save(SaveInput{Patch: []byte(`{}`)})
	_, _, e := Write(context.Background(), f.db, Request{ActorID: "NORMAL", Key: "empty", Method: "PATCH", Path: "internal"}, nil, func(r *Repository) (Mutation, error) {
		_, e := r.Save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 1, Patch: []byte("  { \n }  ")})
		return Mutation{}, e
	})
	if e == nil {
		t.Fatal("whitespace empty patch accepted")
	}
}
