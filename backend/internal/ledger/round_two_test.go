package ledger

import (
	"context"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
	"testing"
)

func TestG2ConcurrentReadViewPinsVersions(t *testing.T) {
	f := newFixture(t)
	writer := f.db
	if f.db.DriverName() == "sqlite" {
		var file string
		if e := f.db.Get(&file, `SELECT file FROM pragma_database_list WHERE name='main'`); e != nil {
			t.Fatal(e)
		}
		var e error
		writer, e = database.Open("sqlite", file, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
		defer writer.Close()
	}
	d := f.save(SaveInput{GroupID: &f.normal.ID, Patch: []byte(paper)})
	ctx := context.Background()
	_, e := store.ReadView(ctx, f.db, func(reader store.Reader, snapshot store.Snapshot) error {
		before, e := CurrentSet(ctx, reader, f.normal.ID)
		if e != nil {
			return e
		}
		ch := make(chan error, 1)
		go func() {
			_, _, e := Write(ctx, writer, Request{ActorID: "NORMAL", Key: "during-view", Method: "PATCH", Path: "internal", Raw: []byte("view")}, nil, func(r *Repository) (Mutation, error) {
				new, e := r.Save(SaveInput{ID: d.Submission.ID, ExpectedVersion: 1, Patch: []byte(`{"remark":"after snapshot"}`)})
				return Mutation{Details: []Detail{new}}, e
			})
			ch <- e
		}()
		if e := <-ch; e != nil {
			return e
		}
		after, e := CurrentSet(ctx, reader, f.normal.ID)
		if e != nil {
			return e
		}
		if len(before) != 1 || len(after) != 1 || before[0].RevisionID != after[0].RevisionID || after[0].RevisionID != d.Revision.ID {
			t.Fatal("snapshot drift")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	after, e := CurrentSet(ctx, f.db, f.normal.ID)
	if e != nil || after[0].RevisionID == d.Revision.ID {
		t.Fatal("writer did not commit", e)
	}
}
