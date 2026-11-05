package main

import (
	"context"
	"testing"

	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/store"
)

func TestG1SeedOnlyFillsMissing(t *testing.T) {
	db, driver := store.FixtureDB(t, true)
	st := dictionary.NewStore(db, driver)
	ctx := context.Background()
	if e := seedDict(ctx, st); e != nil {
		t.Fatal(e)
	}
	var id string
	if e := db.Get(&id, `SELECT id FROM dict_items WHERE category='sample_project' AND code='M1'`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(db.Rebind(`UPDATE dict_items SET label='人工标签',active=0,meta='{"aliases":["manual"]}' WHERE id=?`), id); e != nil {
		t.Fatal(e)
	}
	before, e := store.Inspect(ctx, db, driver)
	if e != nil {
		t.Fatal(e)
	}
	if e = seedDict(ctx, st); e != nil {
		t.Fatal(e)
	}
	after, e := store.Inspect(ctx, db, driver)
	if e != nil || before.DataDigest != after.DataDigest {
		t.Fatal("seed overwrote manual configuration", e)
	}
	d := dictionary.Drawing{DrawingNo: "human", Active: false}
	if _, e := st.InitializeDrawings(ctx, []dictionary.Drawing{d}); e != nil {
		t.Fatal(e)
	}
	d.Active = true
	if n, e := st.InitializeDrawings(ctx, []dictionary.Drawing{d}); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var active bool
	if e := db.Get(&active, `SELECT active FROM sample_drawings WHERE drawing_no='human'`); e != nil || active {
		t.Fatal("drawing overwritten", e)
	}
}
