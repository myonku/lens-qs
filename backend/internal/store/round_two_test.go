package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestG2UpgradePreservesDataAndRollback(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "rollback"}[duplicate], func(t *testing.T) {
			db, driver := FixtureDB(t, false)
			ctx := context.Background()
			if _, e := migrate(ctx, db, driver, 7, true); e != nil {
				t.Fatal(e)
			}
			statements := []string{`INSERT INTO users(id,username,password_hash,created_at,updated_at) VALUES('u','original','unchanged-hash','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`, `INSERT INTO sample_drawings(id,seq_no,drawing_no,is_rx,created_at,updated_at) VALUES('d1','1','D1',0,'2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`, `INSERT INTO sample_parse_cache(id,raw_key,raw_sample,parsed,verified_by,verified_at,created_at,updated_at) VALUES('m','key','raw','{"is_rx":null}','u','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`}
			if duplicate {
				statements = append(statements, `INSERT INTO sample_drawings(id,seq_no,drawing_no,is_rx,created_at,updated_at) VALUES('d2','1','D2',1,'2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`)
			}
			for _, s := range statements {
				if _, e := db.Exec(s); e != nil {
					t.Fatal(e)
				}
			}
			inv, e := Inspect(ctx, db, driver)
			if e != nil {
				t.Fatal(e)
			}
			if e = Upgrade(ctx, db, driver, "wrong"); e == nil {
				t.Fatal("wrong target accepted")
			}
			e = Upgrade(ctx, db, driver, inv.Fingerprint)
			if duplicate {
				if e == nil {
					t.Fatal("duplicate sequence accepted")
				}
				after, err := Inspect(ctx, db, driver)
				if err != nil || after.Fingerprint != inv.Fingerprint {
					t.Fatal("partial upgrade", err)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = CheckReady(ctx, db, driver); e != nil {
				t.Fatal(e)
			}
			var hash, raw string
			if e = db.Get(&hash, `SELECT password_hash FROM users WHERE id='u'`); e != nil || hash != "unchanged-hash" {
				t.Fatal("account changed", e)
			}
			if e = db.Get(&raw, `SELECT parsed FROM sample_parse_cache WHERE id='m'`); e != nil || raw != `{"is_rx":null}` {
				t.Fatal("manual mapping changed", e)
			}
			if _, e = db.Exec(`INSERT INTO sample_drawings(id,drawing_no,is_rx,created_at,updated_at) VALUES('unknown','unknown',NULL,'2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`); e != nil {
				t.Fatal("unknown Rx unsupported", e)
			}
			after, e := Inspect(ctx, db, driver)
			if e != nil {
				t.Fatal(e)
			}
			if e = Upgrade(ctx, db, driver, after.Fingerprint); e != nil {
				t.Fatal("upgrade retry", e)
			}
			if driver == "sqlite" {
				var filename string
				db.Get(&filename, `SELECT file FROM pragma_database_list WHERE name='main'`)
				receipt, e := BackupSQLite(ctx, filename, filepath.Join(t.TempDir(), "v8.db"))
				if e != nil || receipt.SourceDigest != receipt.RestoredDigest {
					t.Fatal("v8 backup restore", e)
				}
			}
		})
	}
}
