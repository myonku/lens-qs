package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

func TestG2BusinessBackupRestoreReport(t *testing.T) {
	if os.Getenv("LENS_TEST_POSTGRES_DSN") != "" && os.Getenv("LENS_TEST_PG_DOCKER_CONTAINER") == "" {
		t.Skip("PostgreSQL restore requires disposable Docker fixture")
	}
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	d := f.save(g.ID, "a", record)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"patch":{"counts":{"qualified_qty":90,"defect_qty":10}}}`, 200)
	f.call("POST", "/submissions/"+d.Submission.ID+"/cancel", `{"expected_version":2,"reason":"保留取消证据"}`, 200)
	report := f.call("GET", "/reports/product-lots/"+lot.ID, "", 200)
	var snapshot struct {
		At      string `json:"as_of"`
		Through int64  `json:"through_event_id"`
	}
	decode(t, report.Data, &snapshot)
	path := fmt.Sprintf("/reports/product-lots/%s?as_of=%s&through_event_id=%d", lot.ID, url.QueryEscape(snapshot.At), snapshot.Through)
	var restored *sqlx.DB
	driver := "sqlite"
	if f.db.DriverName() == "sqlite" {
		var file string
		if e := f.db.Get(&file, `SELECT file FROM pragma_database_list WHERE name='main'`); e != nil {
			t.Fatal(e)
		}
		backup := filepath.Join(t.TempDir(), "restored.db")
		if _, e := store.BackupSQLite(context.Background(), file, backup); e != nil {
			t.Fatal(e)
		}
		var e error
		restored, e = database.Open("sqlite", backup, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
	} else {
		driver = "postgres"
		container := os.Getenv("LENS_TEST_PG_DOCKER_CONTAINER")
		var schema, dbName string
		if e := f.db.Get(&schema, `SELECT current_schema()`); e != nil {
			t.Fatal(e)
		}
		if !strings.HasPrefix(schema, "g1_") {
			t.Fatal("unsafe restore source")
		}
		if e := f.db.Get(&dbName, `SELECT current_database()`); e != nil {
			t.Fatal(e)
		}
		restoreName := "restore_g2_" + schema
		run := func(args ...string) {
			t.Helper()
			out, e := exec.Command("docker", append([]string{"exec", container}, args...)...).CombinedOutput()
			if e != nil {
				t.Fatal("restore command", string(out), e)
			}
		}
		dump := "/tmp/" + schema + "-g2.dump"
		run("pg_dump", "-U", "postgres", "-d", dbName, "--schema="+schema, "--format=custom", "--file="+dump)
		run("createdb", "-U", "postgres", restoreName)
		t.Cleanup(func() {
			exec.Command("docker", "exec", container, "dropdb", "-U", "postgres", "--force", restoreName).Run()
		})
		run("pg_restore", "-U", "postgres", "-d", restoreName, "--exit-on-error", dump)
		u, e := url.Parse(os.Getenv("LENS_TEST_POSTGRES_DSN"))
		if e != nil {
			t.Fatal(e)
		}
		u.Path = "/" + restoreName
		v := u.Query()
		v.Set("search_path", schema)
		u.RawQuery = v.Encode()
		restored, e = database.Open("postgres", u.String(), 4, 2)
		if e != nil {
			t.Fatal(e)
		}
	}
	defer restored.Close()
	if e := store.CheckReady(context.Background(), restored, driver); e != nil {
		t.Fatal(e)
	}
	us := users.NewStore(restored, driver)
	svc := auth.NewService(restored, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, driver)
	copy := *f
	copy.db = restored
	copy.r = Build(Deps{Cfg: &config.Config{CookieName: "sid"}, UserStore: us, Auth: svc, Dict: dictionary.NewStore(restored, driver)})
	after := copy.call("GET", path, "", 200)
	if string(after.Data) != string(report.Data) {
		t.Fatal("restored historical report differs")
	}
	copy.call("GET", "/submissions/"+d.Submission.ID+"/revisions", "", 200)
	copy.call("GET", "/audit-logs?entity=submission", "", 200)
	var revs int
	if e := restored.Get(&revs, restored.Rebind(`SELECT COUNT(*) FROM submission_revisions WHERE submission_id=?`), d.Submission.ID); e != nil || revs != 2 {
		t.Fatal("restored versions missing", e)
	}
}
