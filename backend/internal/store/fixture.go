package store

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
)

// FixtureDB isolates every case by temporary SQLite file or PostgreSQL schema.
// An explicitly configured PostgreSQL test DSN is never used as a reset target.
func FixtureDB(t *testing.T, initialize bool) (*sqlx.DB, string) {
	t.Helper()
	driver := "sqlite"
	dsn := filepath.Join(t.TempDir(), "foundation.db")
	if v := os.Getenv("LENS_TEST_POSTGRES_DSN"); v != "" {
		driver = "postgres"
		admin, e := database.Open(driver, v, 2, 1)
		if e != nil {
			t.Fatal(e)
		}
		schema := "g1_" + strings.ReplaceAll(id.New(), "-", "")
		if _, e = admin.Exec(`CREATE SCHEMA ` + schema); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
		u, e := url.Parse(v)
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	db, e := database.Open(driver, dsn, 8, 4)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if initialize {
		if e = Initialize(context.Background(), db, driver); e != nil {
			t.Fatal(e)
		}
	}
	return db, driver
}
