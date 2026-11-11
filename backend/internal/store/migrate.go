package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/migrations"
)

const LatestSchemaVersion = 9

// Initialize is the only empty-database entry; known old databases must use
// Rebuild with an inventory fingerprint and verified backup at the CLI boundary.
func Initialize(ctx context.Context, db *sqlx.DB, driver string) error {
	rows, err := AppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if len(rows) == LatestSchemaVersion {
		return CheckReady(ctx, db, driver)
	}
	if len(rows) > 0 {
		return fmt.Errorf("initialize requires an empty database")
	}
	var n int
	q := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`
	if driver == "postgres" {
		q = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema()`
	}
	if err = db.GetContext(ctx, &n, q); err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("initialize refuses unrecognized tables")
	}
	_, err = migrate(ctx, db, driver, LatestSchemaVersion, true)
	return err
}
func migrate(ctx context.Context, db *sqlx.DB, driver string, limit int, explicit bool, expectedData ...string) ([]string, error) {
	if driver != "sqlite" && driver != "postgres" {
		return nil, fmt.Errorf("unsupported driver")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if driver == "postgres" {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(74192711)`); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	// This first write also serializes SQLite maintenance across instances.
	if _, err = tx.ExecContext(ctx, `UPDATE schema_migrations SET name=name WHERE version=1`); err != nil {
		return nil, err
	}
	if len(expectedData) > 0 {
		var tables []string
		q := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
		if driver == "postgres" {
			q = `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() ORDER BY table_name`
		}
		if err = tx.SelectContext(ctx, &tables, q); err != nil {
			return nil, err
		}
		actual, e := dataDigest(ctx, tx, tables)
		if e != nil {
			return nil, e
		}
		if actual != expectedData[0] {
			return nil, fmt.Errorf("target changed after inventory")
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migration_checksums(version INTEGER PRIMARY KEY, digest TEXT NOT NULL, provenance TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	var done []AppliedMigration
	if err = tx.SelectContext(ctx, &done, `SELECT version,name,applied_at FROM schema_migrations ORDER BY version`); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(migrations.FS, driver)
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	var versions []int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		v, e2 := versionOf(e.Name())
		if e2 != nil {
			return nil, e2
		}
		if names[v] != "" {
			return nil, fmt.Errorf("duplicate migration version")
		}
		names[v] = e.Name()
		versions = append(versions, v)
	}
	sort.Ints(versions)
	applied := map[int]bool{}
	for i, m := range done {
		if m.Version != i+1 || names[m.Version] != m.Name {
			return nil, fmt.Errorf("unknown, missing or renamed migration at version %d", m.Version)
		}
		raw, _ := migrations.FS.ReadFile(path.Join(driver, m.Name))
		h := migrationDigest(raw)
		var stored string
		err = tx.GetContext(ctx, &stored, tx.Rebind(`SELECT digest FROM migration_checksums WHERE version=?`), m.Version)
		if err == sql.ErrNoRows {
			// A legacy receipt proves names/shape only, never past file integrity.
			if m.Version > 6 {
				return nil, fmt.Errorf("missing checksum for new migration")
			}
			if _, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO migration_checksums VALUES(?,?,?)`), m.Version, h, "legacy-name-and-shape-baseline"); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else if !migrationDigestMatches(raw, stored) {
			return nil, fmt.Errorf("migration checksum mismatch: %d", m.Version)
		}
		applied[m.Version] = true
	}
	if len(done) > 0 && len(done) < 6 {
		return nil, fmt.Errorf("legacy versions before 0006 require a separate preservation analysis")
	}
	if applied[limit] {
		return nil, tx.Commit()
	}
	if limit >= 7 && !explicit {
		return nil, fmt.Errorf("explicit rebuild required")
	}
	if applied[6] && !applied[7] && limit >= 7 {
		if err = preflightTx(ctx, tx); err != nil {
			return nil, err
		}
	}
	before := ""
	if limit >= 7 {
		if applied[6] && !applied[7] {
			before, err = preservedDigest(ctx, tx, false)
			if err != nil {
				return nil, err
			}
		}
	}
	var out []string
	for _, v := range versions {
		if v > limit || applied[v] {
			continue
		}
		raw, _ := migrations.FS.ReadFile(path.Join(driver, names[v]))
		for _, stmt := range splitStatements(string(raw)) {
			if _, err = tx.ExecContext(ctx, stmt); err != nil {
				return nil, fmt.Errorf("migration %s: %w", names[v], err)
			}
		}
		if _, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO schema_migrations VALUES(?,?,?)`), v, names[v], nowText()); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO migration_checksums VALUES(?,?,?)`), v, migrationDigest(raw), "applied-and-verified"); err != nil {
			return nil, err
		}
		out = append(out, names[v])
	}
	if limit >= 7 && !applied[7] {
		definition, _ := json.Marshal(formfields.Catalog())
		layout, _ := json.Marshal(formfields.Layout())
		if _, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO field_schemas VALUES(?,?)`), "2", string(definition)); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO form_templates VALUES(?,?,?)`), "general-1", "2", string(layout)); err != nil {
			return nil, err
		}

	}
	if before != "" {
		if err = receipt(ctx, tx, before); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Migration files may be checked out with LF or CRLF. New receipts use LF;
// historical byte-exact receipts remain valid without rewriting the database.
// Do not normalize general data/backup digests or any other SQL whitespace.
func migrationDigest(raw []byte) string {
	return digest(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
}

func migrationDigestMatches(raw []byte, stored string) bool {
	lf := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	return stored == digest(raw) || stored == digest(lf) ||
		stored == digest(bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n")))
}

func CheckReady(ctx context.Context, db *sqlx.DB, driver string) error {
	rows, err := AppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if len(rows) != LatestSchemaVersion {
		return fmt.Errorf("expected schema %04d; use explicit initialization/rebuild/upgrade", LatestSchemaVersion)
	}
	for i, m := range rows {
		if m.Version != i+1 {
			return fmt.Errorf("noncontiguous migration version")
		}
		v, e := versionOf(m.Name)
		if e != nil || v != m.Version {
			return fmt.Errorf("migration name/version mismatch")
		}
		raw, e := migrations.FS.ReadFile(driver + "/" + m.Name)
		if e != nil {
			return e
		}
		var h string
		if e = db.GetContext(ctx, &h, db.Rebind(`SELECT digest FROM migration_checksums WHERE version=?`), m.Version); e != nil {
			return e
		}
		if !migrationDigestMatches(raw, h) {
			return fmt.Errorf("migration integrity mismatch: %s", m.Name)
		}
	}
	var n int
	if err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM write_gate JOIN rebuild_state ON write_gate.id=rebuild_state.id WHERE write_gate.id=1`); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("missing foundation singleton")
	}
	if err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM shared_catalog_state WHERE id=1`); err != nil || n != 1 {
		return fmt.Errorf("missing shared catalog version")
	}
	for _, query := range []string{`SELECT paper_unit_text FROM submission_revisions LIMIT 0`, `SELECT is_rx FROM sample_drawings LIMIT 0`} {
		rows, e := db.QueryxContext(ctx, query)
		if e != nil {
			return fmt.Errorf("incompatible business shape")
		}
		rows.Close()
	}
	for _, q := range []string{`SELECT department,account_type,operation_id FROM users LIMIT 0`, `SELECT state,current_revision_id,last_saved_by FROM inspection_submissions LIMIT 0`, `SELECT content_json,content_schema_version,saved_actor_name FROM submission_revisions LIMIT 0`, `SELECT operation_label,created_by FROM operation_groups LIMIT 0`, `SELECT transaction_id,actor_name FROM business_events LIMIT 0`, `SELECT kind,direction,quantity,reason FROM quantity_changes LIMIT 0`, `SELECT operation_group_id,batch_key,submission_id FROM submission_slots LIMIT 0`, `SELECT fingerprint,response_status,response_body,expires_at FROM v2_idempotency_requests LIMIT 0`, `SELECT parser_version,dictionary_version,needs_review FROM sample_parse_cache LIMIT 0`, `SELECT code,name FROM units LIMIT 0`, `SELECT category,code,meta FROM dict_items LIMIT 0`, `SELECT schema_version,layout FROM form_templates LIMIT 0`} {
		rows, e := db.QueryxContext(ctx, q)
		if e != nil {
			return fmt.Errorf("incompatible foundation shape")
		}
		rows.Close()
	}
	cols := []string{"revision_id", "seq", "total_qty", "qualified_qty", "defect_qty", "remark"}
	for _, code := range formfields.DefectCodes() {
		for _, surface := range formfields.Surfaces {
			cols = append(cols, code+"_"+surface)
		}
	}
	lineRows, e := db.QueryxContext(ctx, `SELECT `+strings.Join(cols, ",")+` FROM revision_lines LIMIT 0`)
	if e != nil {
		return fmt.Errorf("incompatible revision line shape")
	}
	lineRows.Close()
	catalog, e := ReadCatalog(ctx, db, "2")
	if e != nil {
		return e
	}
	actual, _ := json.Marshal(catalog)
	expected, _ := json.Marshal(formfields.Catalog())
	if string(actual) != string(expected) {
		return fmt.Errorf("immutable field schema mismatch")
	}
	return nil
}

func versionOf(filename string) (int, error) {
	i := strings.IndexByte(filename, '_')
	if i <= 0 {
		return 0, fmt.Errorf("迁移文件名不符合 <版本号>_<描述> 约定: %s", filename)
	}
	v, err := strconv.Atoi(filename[:i])
	if err != nil {
		return 0, fmt.Errorf("迁移文件版本号不是数字: %s", filename)
	}
	return v, nil
}

// splitStatements 按分号切分 SQL，跳过注释行；够用即可（本项目迁移不含存储过程）。
func splitStatements(sqlText string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			if stmt != "" {
				out = append(out, stmt)
			}
			cur.Reset()
		}
	}
	if stmt := strings.TrimSpace(cur.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
