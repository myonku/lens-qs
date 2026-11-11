package users

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/password"
	"github.com/myonku/lens-qs-api/internal/store"
)

func TestBootstrapAdminEmptyAndExisting(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	s := NewStore(db, driver)
	ctx := context.Background()
	if u, err := s.BootstrapAdmin(ctx, CreateInput{}); err != nil || u != nil {
		t.Fatal("disabled bootstrap", err)
	}
	if _, err := s.BootstrapAdmin(ctx, CreateInput{Username: "admin"}); err == nil {
		t.Fatal("incomplete credentials accepted")
	}
	u, err := s.BootstrapAdmin(ctx, CreateInput{Username: "admin", Password: "original-password", AccountType: "SAMPLING"})
	if err != nil || u == nil || !u.IsAdmin || u.AccountType != "SAMPLING" {
		t.Fatal("bootstrap", u, err)
	}
	if result, err := s.BootstrapAdmin(ctx, CreateInput{Username: "different", Password: "short", AccountType: "invalid"}); err != nil || result != nil {
		t.Fatal("nonempty database must ignore bootstrap", err)
	}
	after, _ := s.GetByID(ctx, u.ID)
	n, _ := s.Count(ctx)
	if n != 1 || !reflect.DeepEqual(u, after) {
		t.Fatal("restart changed existing user")
	}
}

func TestBootstrapAdminConcurrent(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	s := NewStore(db, driver)
	var wg sync.WaitGroup
	for _, username := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if _, err := s.BootstrapAdmin(context.Background(), CreateInput{Username: name, Password: "original-password"}); err != nil {
				t.Error(err)
			}
		}(username)
	}
	wg.Wait()
	if n, err := s.Count(context.Background()); err != nil || n != 1 {
		t.Fatal("concurrent bootstrap created multiple users", n, err)
	}
}

func TestConfigureAdminPreservesCredentialsAndRevokesTypeSessions(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	s := NewStore(db, driver)
	ctx := context.Background()
	u, err := s.Create(ctx, CreateInput{Username: "admin", Password: "original-password", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(db.Rebind(`INSERT INTO sessions(id,user_id,token_hash,created_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?)`), "bootstrap-session", u.ID, "bootstrap-token", "2026-11-06T00:00:00Z", "2026-11-06T00:00:00Z", "2030-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ConfigureAdmin(ctx, CreateInput{Username: "admin", Password: "ignored-password", AccountType: "SAMPLING", DisplayName: "抽检管理员"})
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != u.ID || after.Username != u.Username || after.PasswordHash != u.PasswordHash || after.AccountType != "SAMPLING" || after.DisplayName != "抽检管理员" {
		t.Fatal("profile synchronization changed credentials or missed profile")
	}
	if err := password.Verify("original-password", after.PasswordHash); err != nil {
		t.Fatal("original password invalid", err)
	}
	var sessions, events int
	db.Get(&sessions, `SELECT COUNT(*) FROM sessions WHERE revoked_at IS NULL`)
	if sessions != 0 {
		t.Fatal("old type session remains active")
	}
	db.Get(&events, `SELECT COUNT(*) FROM business_events`)
	if _, err := s.ConfigureAdmin(ctx, CreateInput{Username: "admin", AccountType: "SAMPLING"}); err != nil {
		t.Fatal(err)
	}
	var next int
	db.Get(&next, `SELECT COUNT(*) FROM business_events`)
	if next != events {
		t.Fatal("unchanged config wrote another event")
	}
	if _, err := s.ConfigureAdmin(ctx, CreateInput{Username: "renamed", Password: "new-password"}); err == nil {
		t.Fatal("nonempty database accepted another bootstrap user")
	}
}
