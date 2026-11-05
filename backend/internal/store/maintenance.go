package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
)

type Inventory struct {
	Driver      string             `json:"driver"`
	Target      string             `json:"target"`
	Version     int                `json:"version"`
	Migrations  []AppliedMigration `json:"migrations"`
	Tables      []string           `json:"tables"`
	Counts      map[string]int     `json:"counts"`
	Conflicts   []string           `json:"conflicts"`
	DataDigest  string             `json:"data_digest"`
	Fingerprint string             `json:"fingerprint"`
}

func Inspect(ctx context.Context, db *sqlx.DB, driver string) (Inventory, error) {
	out := Inventory{Driver: driver, Counts: map[string]int{}, Conflicts: []string{}, Tables: []string{}}
	var e error
	out.Migrations, e = AppliedMigrations(ctx, db)
	if e != nil {
		return out, e
	}
	for _, m := range out.Migrations {
		out.Version = m.Version
	}
	for i, m := range out.Migrations {
		v, e := versionOf(m.Name)
		if e != nil || v != m.Version || m.Version != i+1 {
			out.Conflicts = append(out.Conflicts, "migration name/version mismatch")
		}
	}
	if driver == "sqlite" {
		var rows []struct {
			Seq  int    `db:"seq"`
			Name string `db:"name"`
			File string `db:"file"`
		}
		if e = db.SelectContext(ctx, &rows, `PRAGMA database_list`); e != nil {
			return out, e
		}
		for _, r := range rows {
			if r.Name == "main" {
				out.Target = r.File
			}
		}
		e = db.SelectContext(ctx, &out.Tables, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	} else {
		e = db.GetContext(ctx, &out.Target, `SELECT current_database() || '/' || current_schema() || '@' || COALESCE(inet_server_addr()::text,'local') || ':' || inet_server_port()::text`)
		if e == nil {
			e = db.SelectContext(ctx, &out.Tables, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() ORDER BY table_name`)
		}
	}
	if e != nil {
		return out, e
	}
	for _, name := range []string{"users", "dict_items", "units", "sample_drawings", "sample_parse_cache", "product_lots", "operation_groups", "inspection_submissions", "sessions", "v2_idempotency_requests"} {
		for _, t := range out.Tables {
			if t == name {
				var n int
				if e = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM `+name); e != nil {
					return out, e
				}
				out.Counts[name] = n
			}
		}
	}
	if out.Version == 6 {
		tx, e := db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: driver == "postgres"})
		if e != nil {
			return out, e
		}
		if e = preflightTx(ctx, tx); e != nil {
			out.Conflicts = append(out.Conflicts, e.Error())
		}
		tx.Rollback()
	}
	out.DataDigest, e = dataDigest(ctx, db, out.Tables)
	if e != nil {
		return out, e
	}
	config := ""
	if out.Version >= 6 {
		config, e = preservedDigest(ctx, db, out.Version >= 7)
		if e != nil {
			return out, e
		}
	}
	b, _ := json.Marshal(struct {
		Target  string
		Version int
		Counts  map[string]int
		Config  string
	}{out.Target, out.Version, out.Counts, config + out.DataDigest})
	out.Fingerprint = digest(b)
	return out, nil
}

//go:embed v6_shape.json
var v6ShapeJSON []byte

func verifyLegacyShape(ctx context.Context, tx *sqlx.Tx) error {
	var expected map[string][]string
	if e := json.Unmarshal(v6ShapeJSON, &expected); e != nil {
		return e
	}
	var tables []string
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if tx.DriverName() == "pgx" {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() ORDER BY table_name`
	}
	if e := tx.SelectContext(ctx, &tables, query); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, table := range tables {
		if table == "migration_checksums" {
			continue
		}
		want, ok := expected[table]
		if !ok {
			return fmt.Errorf("unrecognized v6 table: %s", table)
		}
		seen[table] = true
		var columns []string
		if tx.DriverName() == "pgx" {
			if e := tx.SelectContext(ctx, &columns, tx.Rebind(`SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY column_name`), table); e != nil {
				return e
			}
		} else {
			rows, e := tx.QueryxContext(ctx, `PRAGMA table_info("`+table+`")`)
			if e != nil {
				return e
			}
			for rows.Next() {
				v := map[string]any{}
				if e = rows.MapScan(v); e != nil {
					rows.Close()
					return e
				}
				columns = append(columns, v["name"].(string))
			}
			rows.Close()
			sort.Strings(columns)
		}
		if strings.Join(want, ",") != strings.Join(columns, ",") {
			return fmt.Errorf("unrecognized v6 columns: %s", table)
		}
	}
	for table := range expected {
		if !seen[table] {
			return fmt.Errorf("missing v6 table: %s", table)
		}
	}
	return nil
}

func preflightTx(ctx context.Context, tx *sqlx.Tx) error {
	if e := verifyLegacyShape(ctx, tx); e != nil {
		return e
	}
	// Known v6 shape probes: a renamed or manually damaged database is rejected.
	for _, q := range []string{`SELECT unit_id,account_type,password_hash FROM users LIMIT 0`, `SELECT current_revision_id,draft_revision_id FROM inspection_submissions LIMIT 0`, `SELECT published_at,saved_reason FROM submission_revisions LIMIT 0`, `SELECT history_started_at FROM v2_release_state WHERE id=1`} {
		r, e := tx.QueryxContext(ctx, q)
		if e != nil {
			return fmt.Errorf("unrecognized v6 shape")
		}
		r.Close()
	}
	var n int
	if e := tx.GetContext(ctx, &n, `SELECT COUNT(*) FROM users u WHERE unit_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM units x WHERE x.id=u.unit_id)`); e != nil {
		return e
	}
	if n > 0 {
		return fmt.Errorf("unresolved department references: %d", n)
	}
	var conflicts []string
	q := `SELECT a.id FROM dict_items a JOIN dict_items b ON a.code=b.code AND b.category='inspection_operation' WHERE a.category='process' AND (a.label<>b.label OR a.active<>b.active OR a.sort_no<>b.sort_no OR COALESCE(a.meta,'null')<>COALESCE(b.meta,'null')) ORDER BY a.id`
	if e := tx.SelectContext(ctx, &conflicts, q); e != nil {
		return e
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("conflicting operation configuration IDs: %s", strings.Join(conflicts, ","))
	}
	var metas []struct {
		ID   string  `db:"id"`
		Meta *string `db:"meta"`
	}
	if e := tx.SelectContext(ctx, &metas, `SELECT id,meta FROM dict_items WHERE meta IS NOT NULL`); e != nil {
		return e
	}
	for _, m := range metas {
		var v map[string]any
		if e := json.Unmarshal([]byte(*m.Meta), &v); e != nil {
			return fmt.Errorf("invalid metadata ID: %s", m.ID)
		}
	}
	var caches []struct {
		ID     string  `db:"id"`
		Parsed *string `db:"parsed"`
	}
	if e := tx.SelectContext(ctx, &caches, `SELECT id,parsed FROM sample_parse_cache WHERE verified_by IS NOT NULL`); e != nil {
		return e
	}
	for _, c := range caches {
		if c.Parsed == nil || !json.Valid([]byte(*c.Parsed)) {
			return fmt.Errorf("invalid manual confirmation ID: %s", c.ID)
		}
	}
	return nil
}
func preservedDigest(ctx context.Context, q sqlx.QueryerContext, rebuilt bool) (string, error) {
	unitTable := "units"
	department := `(SELECT name FROM units WHERE units.id=users.unit_id)`
	if rebuilt {
		unitTable = "preserved_organization_units"
		department = "department"
	}
	queries := []string{`SELECT id,username,password_hash,display_name,employee_no,is_admin,account_type,status,created_at,` + department + ` AS department FROM users ORDER BY id`,
		`SELECT id,seq_no,drawing_no,project_code,model_code,spec_code,size_code,side_code,class_code,is_rx,active,note,created_at,updated_at FROM sample_drawings ORDER BY id`,
		`SELECT id,raw_key,raw_sample,parsed,drawing_id,confidence,verified_by,verified_at,created_at,updated_at FROM sample_parse_cache WHERE verified_by IS NOT NULL ORDER BY id`,
		`SELECT * FROM ` + unitTable + ` ORDER BY id`,
		`SELECT CASE WHEN category='process' THEN 'inspection_operation' ELSE category END AS category,code,label,sort_no,active,meta FROM dict_items WHERE category<>'process' OR NOT EXISTS(SELECT 1 FROM dict_items b WHERE b.category='inspection_operation' AND b.code=dict_items.code) ORDER BY 1,code`}
	all := []any{}
	for _, query := range queries {
		rows, e := q.QueryxContext(ctx, query)
		if e != nil {
			return "", e
		}
		items := []map[string]any{}
		for rows.Next() {
			v := map[string]any{}
			if e = rows.MapScan(v); e != nil {
				rows.Close()
				return "", e
			}
			for k, x := range v {
				if b, ok := x.([]byte); ok {
					v[k] = string(b)
				}
			}
			items = append(items, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		all = append(all, items)
	}
	b, _ := json.Marshal(all)
	return digest(b), nil
}
func Rebuild(ctx context.Context, db *sqlx.DB, driver, expected string) error {
	inv, e := Inspect(ctx, db, driver)
	if e != nil {
		return e
	}
	if expected == "" || inv.Fingerprint != expected {
		return fmt.Errorf("target fingerprint mismatch")
	}
	if inv.Version == 8 {
		return CheckReady(ctx, db, driver)
	}
	if inv.Version != 6 || len(inv.Conflicts) > 0 {
		return fmt.Errorf("rebuild requires known v6 without conflicts: %v", inv.Conflicts)
	}
	_, e = migrate(ctx, db, driver, 8, true, inv.DataDigest)
	return e
}
func ResetInspection(ctx context.Context, db *sqlx.DB) error {
	w, e := BeginWrite(ctx, db, "", false, nil)
	if e != nil {
		return e
	}
	defer w.Close()
	// Deferred cyclic references are checked at commit, not disabled.
	for _, q := range []string{`DELETE FROM submission_slots`, `DELETE FROM quantity_changes`, `DELETE FROM revision_lines`, `DELETE FROM submission_revisions`, `DELETE FROM inspection_submissions`, `DELETE FROM operation_groups`, `DELETE FROM product_lots`, `DELETE FROM business_events WHERE transaction_id IN (SELECT id FROM event_transactions WHERE domain=1)`, `DELETE FROM event_transactions WHERE domain=1`, `DELETE FROM sessions`, `DELETE FROM v2_idempotency_requests`, `DELETE FROM sample_parse_cache WHERE verified_by IS NULL`} {
		if _, e = w.ExecContext(w.Context, q); e != nil {
			return e
		}
	}
	if _, e = w.ExecContext(w.Context, `UPDATE rebuild_state SET history_started_at=NULL WHERE id=1`); e != nil {
		return e
	}
	return w.Commit()
}
func receipt(ctx context.Context, tx *sqlx.Tx, before string) error {
	after, e := preservedDigest(ctx, tx, true)
	if e != nil {
		return e
	}
	if before != after {
		return fmt.Errorf("account/configuration preservation mismatch; rolled back")
	}
	_, e = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO maintenance_receipts VALUES(?,?,?,?)`), id.New(), 6, after, nowText())
	return e
}

func dataDigest(ctx context.Context, q sqlx.QueryerContext, tables []string) (string, error) {
	all := []any{}
	for _, table := range tables {
		if strings.ContainsAny(table, `";`) {
			return "", fmt.Errorf("invalid table identifier")
		}
		query := `SELECT * FROM "` + table + `"`
		if driverOf(q) == "pgx" {
			query = `SELECT row_to_json(t)::text AS row_json FROM "` + table + `" t`
		}
		rows, e := q.QueryxContext(ctx, query)
		if e != nil {
			return "", e
		}
		values := []string{}
		for rows.Next() {
			v := map[string]any{}
			if e = rows.MapScan(v); e != nil {
				rows.Close()
				return "", e
			}
			for k, x := range v {
				if b, ok := x.([]byte); ok {
					v[k] = string(b)
				}
			}
			if raw, ok := v["row_json"].(string); ok && driverOf(q) == "pgx" {
				decoder := json.NewDecoder(strings.NewReader(raw))
				decoder.UseNumber()
				if e = decoder.Decode(&v); e != nil {
					rows.Close()
					return "", e
				}
			}
			raw, _ := json.Marshal(v)
			values = append(values, string(raw))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		sort.Strings(values)
		all = append(all, struct {
			Name string
			Rows []string
		}{table, values})
	}
	raw, _ := json.Marshal(all)
	return digest(raw), nil
}
