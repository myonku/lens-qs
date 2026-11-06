package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/myonku/lens-qs-api/migrations"
)

func TestMigrationChecksumLineEndings(t *testing.T) {
	lf := []byte("-- migration\nSELECT 1;\nSELECT 2;\n")
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	for _, raw := range [][]byte{lf, crlf} {
		if migrationDigest(raw) != digest(lf) {
			t.Fatal("new receipts must use LF")
		}
		for _, historical := range [][]byte{lf, crlf} {
			if !migrationDigestMatches(raw, digest(historical)) {
				t.Fatal("historical line endings rejected")
			}
		}
		for _, changed := range [][]byte{
			[]byte("-- migration\nSELECT 1;\nSELECT 3;\n"),
			[]byte("-- migration\nSELECT  1;\nSELECT 2;\n"),
		} {
			if migrationDigestMatches(raw, digest(changed)) {
				t.Fatal("SQL change accepted")
			}
		}
	}
}

func TestHistoricalMigrationChecksumsRemainUnchanged(t *testing.T) {
	db, driver := FixtureDB(t, true)
	ctx := context.Background()
	rows, err := AppliedMigrations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range rows {
		raw, err := migrations.FS.ReadFile(driver + "/" + m.Name)
		if err != nil {
			t.Fatal(err)
		}
		lf := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		for _, historical := range [][]byte{lf, bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))} {
			stored := digest(historical)
			if _, err := db.Exec(db.Rebind(`UPDATE migration_checksums SET digest=? WHERE version=?`), stored, m.Version); err != nil {
				t.Fatal(err)
			}
			before, err := Inspect(ctx, db, driver)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckReady(ctx, db, driver); err != nil {
				t.Fatal(m.Name, err)
			}
			if _, err := migrate(ctx, db, driver, 8, true); err != nil {
				t.Fatal(m.Name, err)
			}
			after, err := Inspect(ctx, db, driver)
			if err != nil || before.DataDigest != after.DataDigest {
				t.Fatal("checksum validation changed database contents", err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE migration_checksums SET digest='tampered' WHERE version=7`); err != nil {
		t.Fatal(err)
	}
	if err := CheckReady(ctx, db, driver); err == nil {
		t.Fatal("startup accepted tampered checksum")
	}
	if _, err := migrate(ctx, db, driver, 8, true); err == nil {
		t.Fatal("maintenance accepted tampered checksum")
	}
}
