package sample

import (
	"context"
	"github.com/myonku/lens-qs-api/internal/store"
	"sync"
	"testing"
)

func TestG2ManualCacheWinsConcurrentParse(t *testing.T) {
	db, _ := store.FixtureDB(t, true)
	_, e := db.Exec(`INSERT INTO users(id,username,password_hash,created_at,updated_at) VALUES('u','u','hash','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
	if e != nil {
		t.Fatal(e)
	}
	ctx := store.WithActor(context.Background(), "u")
	raw := "M2 S1 VID2 MD R"
	project := "MANUAL"
	errors := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var confirmation *ParsedResult
			if i == 7 {
				confirmation = &ParsedResult{Project: &project}
			}
			_, e := ParseAndCache(ctx, db, &raw, confirmation)
			errors <- e
		}(i)
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	result, e := ParseAndCache(ctx, db, &raw, nil)
	if e != nil || !result.Verified || result.Parsed.Project == nil || *result.Parsed.Project != project {
		t.Fatal("manual confirmation overwritten", result, e)
	}
	if _, e = db.Exec(`UPDATE shared_catalog_state SET version=version+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	result, e = ParseAndCache(ctx, db, &raw, nil)
	if e != nil || !result.Verified || !result.NeedsReview || *result.Parsed.Project != project {
		t.Fatal("manual cache did not survive catalog invalidation", e)
	}
	other := "M1 S2 VID1"
	first, e := ParseAndCache(ctx, db, &other, nil)
	if e != nil || first.FromCache {
		t.Fatal(e)
	}
	second, e := ParseAndCache(ctx, db, &other, nil)
	if e != nil || !second.FromCache {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE shared_catalog_state SET version=version+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	third, e := ParseAndCache(ctx, db, &other, nil)
	if e != nil || third.FromCache {
		t.Fatal("stale automatic cache served", e)
	}
}
