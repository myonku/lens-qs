package ledger

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/migrations"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeFromV2DraftPreservesAccountsAndSeedsHistory(t *testing.T) {
	driver := "sqlite"
	var db *sqlx.DB
	var e error
	if os.Getenv("LENS_TEST_POSTGRES_DSN") != "" {
		driver = "postgres"
		db = postgresDatabase(t)
	} else {
		db, e = sqlx.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db")+"?_pragma=foreign_keys(1)")
		if e != nil {
			t.Fatal(e)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	entries, e := fs.ReadDir(migrations.FS, driver)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") || entry.Name()[:4] >= "0006" {
			continue
		}
		raw, e := migrations.FS.ReadFile(driver + "/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		lines := []string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		tx, e := db.Beginx()
		if e != nil {
			t.Fatal(e)
		}
		for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
			if strings.TrimSpace(statement) != "" {
				if _, e = tx.Exec(statement); e != nil {
					tx.Rollback()
					t.Fatal(entry.Name(), e)
				}
			}
		}
		if _, e = tx.Exec(tx.Rebind("INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)"), int(entry.Name()[3]-'0'), entry.Name(), now()); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	queries := []string{`INSERT INTO users(id,username,password_hash) VALUES('preserved','preserved','hash-preserved')`, `INSERT INTO dict_items(id,category,code,label) VALUES('a','inspection_operation','A','A')`, `INSERT INTO forms(id,form_no) VALUES('obsolete','obsolete')`, `INSERT INTO product_lots(id,shared_form_no,sample_identity,sample_identity_key,created_at,updated_at) VALUES('p','v2-lot','M','M','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`, `INSERT INTO operation_groups(id,product_lot_id,operation_id,operation_code,inspection_kind,template_version,reporting_state,batch_mode,version) VALUES('g','p','a','A','NORMAL','v2.1','OPEN','WHOLE',2)`, `INSERT INTO inspection_submissions(id,operation_group_id,inspection_kind,state,version,created_by,created_at) VALUES('s','g','NORMAL','DRAFT',1,'preserved','2026-11-01T00:00:00Z')`}
	for _, q := range queries {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	c, e := mergeContent(emptyContent(), Object{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(db.Rebind(`INSERT INTO submission_revisions(id,submission_id,revision_no,template_version,inspection_kind,content,saved_by,saved_at,reason) VALUES('r','s',1,'v2.1','NORMAL',?,'preserved','2026-11-01T00:00:00Z','saved before upgrade')`), stringify(c))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE inspection_submissions SET draft_revision_id = 'r' WHERE id = 's'"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO submission_slots(operation_group_id,batch_key,submission_id) VALUES('g','','s')"); e != nil {
		t.Fatal(e)
	}
	applied, e := store.Migrate(context.Background(), db, driver)
	if e != nil || len(applied) != 1 {
		t.Fatal(applied, e)
	}
	if a, e := store.Migrate(context.Background(), db, driver); e != nil || len(a) != 0 {
		t.Fatal(a, e)
	}
	var hash, kind string
	if e = db.QueryRow("SELECT password_hash,account_type FROM users WHERE id = 'preserved'").Scan(&hash, &kind); e != nil || hash != "hash-preserved" || kind != "NORMAL" {
		t.Fatal(hash, kind, e)
	}
	var n int
	if e = db.Get(&n, "SELECT COUNT(*) FROM submission_revisions"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	r := gin.New()
	New(db).Register(r.Group("/api/v2", func(c *gin.Context) { c.Set(middleware.CtxUserID, "preserved") }))
	h := &harness{t: t, db: db, r: r, actor: "preserved"}
	report := h.get("/reports/product-lots/p")
	groups := report["groups"].([]any)
	if len(groups) != 1 || num(obj(groups[0])["registered_count"]) != 1 {
		t.Fatal(report)
	}
	revision := obj(obj(obj(groups[0])["submissions"].([]any)[0])["draft_revision"])
	if revision["saved_reason"] != "saved before upgrade" {
		t.Fatal(revision)
	}
	if _, e = db.Exec("SELECT * FROM forms"); e == nil {
		t.Fatal("old forms retained")
	}
}
