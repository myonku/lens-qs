package store

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/database"
)

func legacyFixture(t *testing.T) (*database.DB, string) {
	t.Helper()
	db, driver := FixtureDB(t, false)
	if _, e := migrate(context.Background(), db, driver, 6, false); e != nil {
		t.Fatal(e)
	}
	statements := []string{`INSERT INTO units(id,code,name,active,created_at,updated_at) VALUES('org','ORG','一厂',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO users(id,username,display_name,unit_id,password_hash,account_type,is_admin,created_at,updated_at) VALUES('admin','admin','管理员','org','old-PHC-hash','NORMAL',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO dict_items(id,category,code,label,sort_no,active,meta,created_at,updated_at) VALUES('oldop','process','A','人工工序',10,0,'{"aliases":["AA"]}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO sample_parse_cache(id,raw_key,raw_sample,parsed,verified_by,verified_at,created_at,updated_at) VALUES('manual','key',' 原文 ','{"is_rx":null}','admin','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO sample_parse_cache(id,raw_key,raw_sample,parsed,created_at,updated_at) VALUES('auto','auto','auto','{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO audit_logs(action,entity,entity_id,created_at) VALUES('login','auth','admin','2026-01-01T00:00:00Z')`}
	for _, q := range statements {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return db, driver
}
func TestG1EmptyAndLegacyPreservation(t *testing.T) {
	ctx := context.Background()
	db, driver := legacyFixture(t)
	before, e := Inspect(ctx, db, driver)
	if e != nil || len(before.Conflicts) > 0 {
		t.Fatal(before, e)
	}
	if strings.Contains(string(mustJSON(before)), "old-PHC-hash") {
		t.Fatal("hash leaked")
	}
	if e = Rebuild(ctx, db, driver, "wrong"); e == nil {
		t.Fatal("target mismatch accepted")
	}
	if e = Rebuild(ctx, db, driver, before.Fingerprint); e != nil {
		t.Fatal(e)
	}
	if e = CheckReady(ctx, db, driver); e != nil {
		t.Fatal(e)
	}
	var department, hash string
	if e = db.QueryRow(`SELECT department,password_hash FROM users WHERE id='admin'`).Scan(&department, &hash); e != nil || department != "一厂" || hash != "old-PHC-hash" {
		t.Fatal(department, e)
	}
	var n int
	for _, q := range []string{`SELECT COUNT(*) FROM sample_parse_cache WHERE verified_by IS NOT NULL AND needs_review=1`, `SELECT COUNT(*) FROM preserved_organization_units WHERE id='org'`, `SELECT COUNT(*) FROM dict_items WHERE id='oldop' AND category='inspection_operation' AND active=0`, `SELECT COUNT(*) FROM audit_logs WHERE entity='auth'`, `SELECT COUNT(*) FROM maintenance_receipts`} {
		if e = db.Get(&n, q); e != nil || n != 1 {
			t.Fatal(q, n, e)
		}
	}
	inv, e := Inspect(ctx, db, driver)
	if e != nil {
		t.Fatal(e)
	}
	if e = Rebuild(ctx, db, driver, inv.Fingerprint); e != nil {
		t.Fatal(e)
	}
	if _, e = migrate(ctx, db, driver, 6, false); e != nil {
		t.Fatal(e)
	}
	empty, emptyDriver := FixtureDB(t, true)
	var schemas []string
	if e = empty.Select(&schemas, `SELECT definition FROM field_schemas`); e != nil || len(schemas) != 1 {
		t.Fatal(e)
	}
	if e = CheckReady(ctx, empty, emptyDriver); e != nil {
		t.Fatal(e)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestG1PreflightAndFailedRebuildRollback(t *testing.T) {
	ctx := context.Background()
	db, driver := legacyFixture(t)
	if _, e := db.Exec(`UPDATE users SET unit_id='missing'`); e != nil {
		t.Fatal(e)
	}
	inv, e := Inspect(ctx, db, driver)
	if e != nil || len(inv.Conflicts) == 0 {
		t.Fatal(inv, e)
	}
	if e = Rebuild(ctx, db, driver, inv.Fingerprint); e == nil {
		t.Fatal("bad department accepted")
	}
	var n int
	db.Get(&n, `SELECT COUNT(*) FROM schema_migrations`)
	if n != 6 {
		t.Fatal("partial schema")
	}
	db.Exec(`UPDATE users SET unit_id='org'`)
	db.Exec(`INSERT INTO dict_items(id,category,code,label,active,created_at,updated_at) VALUES('newop','inspection_operation','A','different',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	inv, e = Inspect(ctx, db, driver)
	if e != nil || len(inv.Conflicts) == 0 || !strings.Contains(inv.Conflicts[0], "oldop") {
		t.Fatal(inv, e)
	}
	db.Exec(`DELETE FROM dict_items WHERE id='newop'`)
	// A late DDL failure must restore earlier deletes and account-column changes.
	if driver == "sqlite" {
		if _, e = db.Exec(`CREATE TRIGGER injected_failure BEFORE UPDATE OF department ON users BEGIN SELECT RAISE(ABORT,'injected failure'); END`); e != nil {
			t.Fatal(e)
		}
	} else {
		if _, e = db.Exec(`CREATE FUNCTION injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`CREATE TRIGGER injected_failure BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION injected_failure()`); e != nil {
			t.Fatal(e)
		}
	}
	inv, e = Inspect(ctx, db, driver)
	if e != nil {
		t.Fatal(e)
	}
	if e = Rebuild(ctx, db, driver, inv.Fingerprint); e == nil {
		t.Fatal("injected DDL failure accepted")
	}
	var hash string
	if e = db.Get(&hash, `SELECT password_hash FROM users WHERE unit_id='org'`); e != nil || hash != "old-PHC-hash" {
		t.Fatal("preservation rollback", e)
	}
	if e = db.Get(&n, `SELECT COUNT(*) FROM sample_parse_cache`); e != nil || n != 2 {
		t.Fatal("cache deletion not rolled back", e)
	}
}
func TestG1MigrationIntegrityAndReadOnlyStatus(t *testing.T) {
	ctx := context.Background()
	db, driver := FixtureDB(t, false)
	inv, e := Inspect(ctx, db, driver)
	if e != nil || len(inv.Tables) != 0 {
		t.Fatal(inv, e)
	}
	if e = Initialize(ctx, db, driver); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE migration_checksums SET digest='tampered' WHERE version=1`); e != nil {
		t.Fatal(e)
	}
	if e = CheckReady(ctx, db, driver); e == nil {
		t.Fatal("checksum accepted")
	}
	if _, e = migrate(ctx, db, driver, 6, false); e == nil {
		t.Fatal("migration accepted checksum")
	}
}

func TestG1SQLiteBackupRestore(t *testing.T) {
	db, driver := legacyFixture(t)
	if driver != "sqlite" {
		t.Skip("SQLite backup; PostgreSQL pg_dump restore is verified separately")
	}
	inv, e := Inspect(context.Background(), db, driver)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := BackupSQLite(context.Background(), inv.Target, t.TempDir()+"/backup.db")
	if e != nil {
		t.Fatal(e)
	}
	if receipt.SourceDigest != receipt.RestoredDigest {
		t.Fatal("restore digest")
	}
	restored, e := database.Open("sqlite", receipt.BackupPath, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	preview, e := Inspect(context.Background(), restored, "sqlite")
	if e != nil {
		t.Fatal(e)
	}
	if e = Rebuild(context.Background(), restored, "sqlite", preview.Fingerprint); e != nil {
		t.Fatal(e)
	}
	sourceAfter, e := Inspect(context.Background(), db, driver)
	if e != nil || inv.Fingerprint != sourceAfter.Fingerprint {
		t.Fatal("source changed", e)
	}
}

func TestG1PostgresBackupRestore(t *testing.T) {
	container := os.Getenv("LENS_TEST_PG_DOCKER_CONTAINER")
	if os.Getenv("LENS_TEST_POSTGRES_DSN") == "" || container == "" {
		t.Skip("requires explicit disposable PostgreSQL Docker fixture")
	}
	db, driver := legacyFixture(t)
	ctx := context.Background()
	source, e := Inspect(ctx, db, driver)
	if e != nil {
		t.Fatal(e)
	}
	var schema, sourceDB string
	db.Get(&schema, `SELECT current_schema()`)
	db.Get(&sourceDB, `SELECT current_database()`)
	if !strings.HasPrefix(schema, "g1_") {
		t.Fatal("unsafe backup schema")
	}
	restoreDB := "restore_" + schema
	run := func(args ...string) {
		t.Helper()
		out, e := exec.Command("docker", append([]string{"exec", container}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatal(string(out), e)
		}
	}
	run("pg_dump", "-U", "postgres", "-d", sourceDB, "--schema="+schema, "--format=custom", "--file=/tmp/"+schema+".dump")
	run("createdb", "-U", "postgres", restoreDB)
	t.Cleanup(func() {
		exec.Command("docker", "exec", container, "dropdb", "-U", "postgres", "--force", restoreDB).Run()
	})
	run("pg_restore", "-U", "postgres", "-d", restoreDB, "--exit-on-error", "/tmp/"+schema+".dump")
	u, e := url.Parse(os.Getenv("LENS_TEST_POSTGRES_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + restoreDB
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	restore, e := database.Open("postgres", u.String(), 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer restore.Close()
	copy, e := Inspect(ctx, restore, "postgres")
	if e != nil || copy.DataDigest != source.DataDigest {
		t.Fatal("restored digest differs", e)
	}
	if e = Rebuild(ctx, restore, "postgres", copy.Fingerprint); e != nil {
		t.Fatal(e)
	}
	sourceAfter, e := Inspect(ctx, db, "postgres")
	if e != nil || sourceAfter.Fingerprint != source.Fingerprint {
		t.Fatal("source changed", e)
	}
}

func TestG1EmptyAndLegacyHaveIdenticalSchema(t *testing.T) {
	old, driver := legacyFixture(t)
	inv, e := Inspect(context.Background(), old, driver)
	if e != nil {
		t.Fatal(e)
	}
	if e = Rebuild(context.Background(), old, driver, inv.Fingerprint); e != nil {
		t.Fatal(e)
	}
	empty, _ := FixtureDB(t, true)
	signature := func(db *database.DB) string {
		t.Helper()
		var queries []string
		if driver == "sqlite" {
			queries = []string{`SELECT type,name,tbl_name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`}
		} else {
			queries = []string{`SELECT table_name,column_name,data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position`, `SELECT c.conname,pg_get_constraintdef(c.oid) AS definition FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname=current_schema() ORDER BY c.conname`}
		}
		var schema string
		if driver == "postgres" {
			db.Get(&schema, `SELECT current_schema()`)
		}
		parts := []string{}
		for _, q := range queries {
			rows, e := db.Queryx(q)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				v := map[string]any{}
				if e = rows.MapScan(v); e != nil {
					t.Fatal(e)
				}
				for k, x := range v {
					if b, ok := x.([]byte); ok {
						v[k] = string(b)
					}
				}
				raw, _ := json.Marshal(v)
				str := string(raw)
				if schema != "" {
					str = strings.ReplaceAll(str, schema+".", "")
				}
				parts = append(parts, str)
			}
			rows.Close()
		}
		return strings.Join(parts, "\n")
	}
	if a, b := signature(old), signature(empty); a != b {
		t.Fatal("empty/legacy final schemas differ")
	}
	for _, db := range []*database.DB{old, empty} {
		var n int
		if e = db.Get(&n, `SELECT COUNT(*) FROM units WHERE id='unit-piece' AND code='PIECE' AND name='片'`); e != nil || n != 1 {
			t.Fatal("unit seed differs", e)
		}
		var version string
		if e = db.Get(&version, `SELECT version FROM field_schemas`); e != nil || version != "2" {
			t.Fatal(e)
		}
	}
}
