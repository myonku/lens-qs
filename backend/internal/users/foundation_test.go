package users

import (
	"context"
	"sync"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
)

func TestG1LastAdministratorAndMaintenanceAtomicity(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	st := NewStore(db, driver)
	a, e := st.Create(context.Background(), CreateInput{Username: "a", Password: "good-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	b, e := st.Create(context.Background(), CreateInput{Username: "b", Password: "good-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	disabled := "disabled"
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := st.Update(context.Background(), id, UpdateInput{Status: &disabled})
			errs <- e
		}(id)
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("last administrator race", success)
	}
	var n int
	if e = db.Get(&n, `SELECT COUNT(*) FROM users WHERE is_admin=1 AND status='active'`); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	var target string
	db.Get(&target, `SELECT id FROM users WHERE status='active'`)
	u, e := st.GetByID(context.Background(), target)
	if e != nil {
		t.Fatal(e)
	}
	beforeHash := u.PasswordHash
	if _, e = db.Exec(db.Rebind(`INSERT INTO sessions(id,user_id,token_hash,created_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?)`), "session", target, "token-hash", "2026-11-05T00:00:00Z", "2026-11-05T00:00:00Z", "2030-01-01T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(db.Rebind(`INSERT INTO v2_idempotency_requests VALUES(?,?,?,?,?,?,?)`), target, "retry", "fp", 200, "{}", "2026-11-05T00:00:00Z", "2030-01-01T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	// Audit storage failure occurs after account, revocation, and retry cleanup.
	if _, e = db.Exec(`DROP TABLE business_events`); e != nil {
		t.Fatal(e)
	}
	kind := "SAMPLING"
	pwd := "new-password"
	if _, e = st.Update(context.Background(), target, UpdateInput{AccountType: &kind, Password: &pwd, Department: httpx.Optional[string]{Set: true}}); e == nil {
		t.Fatal("audit failure accepted")
	}
	after, e := st.GetByID(context.Background(), target)
	if e != nil || after.AccountType != "NORMAL" || after.PasswordHash != beforeHash {
		t.Fatal("account partially changed", e)
	}
	if e = db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE revoked_at IS NULL`); e != nil || n != 1 {
		t.Fatal("revocation partially committed", e)
	}
	if e = db.Get(&n, `SELECT COUNT(*) FROM v2_idempotency_requests`); e != nil || n != 1 {
		t.Fatal("retry partially cleared", e)
	}
}
