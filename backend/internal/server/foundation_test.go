package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/password"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

func TestG1SharedHTTPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, driver := store.FixtureDB(t, true)
	us := users.NewStore(db, driver)
	a, e := us.Create(context.Background(), users.CreateInput{Username: "admin", Password: "good-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	svc := auth.NewService(db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour, MaxLoginFail: 5, LockDuration: time.Minute}, driver)
	dict := dictionary.NewStore(db, driver)
	r := Build(Deps{Cfg: &config.Config{CookieName: "sid", CORSOrigins: []string{"https://allowed.example"}}, UserStore: us, Auth: svc, Dict: dict})
	call := func(method, path, body, token, cookie, origin string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "sid", Value: cookie})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var out map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(w.Code, w.Body, e)
		}
		return w.Code, out
	}
	status, out := call("POST", "/api/v2/auth/login", `{"username":"admin","password":"good-password"}`, "", "", "")
	if status != 200 {
		t.Fatal(status, out)
	}
	token := out["data"].(map[string]any)["token"].(string)
	for _, tc := range []struct {
		method, path, body, token, cookie, origin string
		status                                    int
	}{
		{"GET", "/api/v2/auth/me", "", token, "different", "", 400}, {"GET", "/api/v2/auth/me", "", token, token, "", 200},
		{"PATCH", "/api/v2/users/" + a.ID, `{"display_name":"changed"}`, "", token, "", 403}, {"PATCH", "/api/v2/users/" + a.ID, `{"display_name":"changed"}`, "", token, "https://allowed.example", 200},
		{"GET", "/api/v2/units", "", token, "", "", 200}, {"POST", "/api/v2/units", `{"code":"ORG"}`, token, "", "", 404},
		{"GET", "/api/v2/users?page=0", "", token, "", "", 400}, {"GET", "/api/v2/users?page=1&page=2", "", token, "", "", 400}, {"GET", "/api/v2/auth/me?unknown=x", "", token, "", "", 400}, {"GET", "/api/v2/auth/me", `{}`, token, "", "", 400},
		{"PATCH", "/api/v2/users/" + a.ID, `{"department":"一厂","employee_no":"  "}`, token, "", "", 200}, {"PATCH", "/api/v2/users/" + a.ID, `{"department":null,"employee_no":null}`, token, "", "", 200},
		{"PATCH", "/api/v2/users/" + a.ID, `{"is_admin":null}`, token, "", "", 400}, {"PATCH", "/api/v2/users/" + a.ID, `{}`, token, "", "", 400}, {"PATCH", "/api/v2/users/" + a.ID, `{"is_admin":false}`, token, "", "", 409},
		{"POST", "/api/v2/users", `{"username":"bad","password":"short"}`, token, "", "", 422}, {"POST", "/api/v2/users", `{"username":"bad","username":"other","password":"good-password"}`, token, "", "", 400},
		{"POST", "/api/v2/dict/items", `{"category":"inspection_operation","code":"A","label":"工序A","meta":{"aliases":["AA"]}}`, token, "", "", 200},
		{"POST", "/api/v2/dict/items", `{"category":"process","code":"A"}`, token, "", "", 422},
		{"POST", "/api/v2/dict/items", `{"category":"sample_project","code":"M1","meta":[]}`, token, "", "", 400},
		{"GET", "/api/v2/dict/items?include_inactive=1", "", token, "", "", 400},
		{"GET", "/api/v2/form-fields", "", token, "", "", 200}, {"GET", "/api/v2/form-templates", "", token, "", "", 200},
		{"POST", "/api/v2/auth/change-password", `{"old_password":"wrong","new_password":"new-password"}`, token, "", "", 401},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			s, m := call(tc.method, tc.path, tc.body, tc.token, tc.cookie, tc.origin)
			if s != tc.status {
				t.Fatal(s, m)
			}
		})
	}
	u, e := us.GetByID(context.Background(), a.ID)
	if e != nil || u.Department != nil || u.EmployeeNo != nil {
		t.Fatal(u, e)
	}
	// All retired and not-yet-accepted business routes remain unregistered.
	for _, path := range []string{"product-lots", "operation-groups", "submissions", "submissions/x/draft", "submissions/x/publish", "stats/summary", "stats/defects", "stats/workload", "reports/operations", "audit-logs", "sample/parse", "sample/cache", "dict/drawings", "dict/drawings/import"} {
		for _, method := range []string{"GET", "POST", "PATCH"} {
			s, m := call(method, "/api/v2/"+path, "", token, "", "")
			if s != 404 {
				t.Fatal(method, path, s, m)
			}
		}
	}
	// Read-only cache behavior cannot accidentally be reintroduced through an alias.
	before := 0
	db.Get(&before, `SELECT COUNT(*) FROM sample_parse_cache`)
	call("GET", "/api/v2/sample/cache?raw=x", "", token, "", "")
	after := 0
	db.Get(&after, `SELECT COUNT(*) FROM sample_parse_cache`)
	if before != after {
		t.Fatal("cache GET wrote")
	}
	// Account type updates revoke every session and successful domain retry entry.
	b, e := us.Create(context.Background(), users.CreateInput{Username: "normal", Password: "normal-password"})
	if e != nil {
		t.Fatal(e)
	}
	status, out = call("POST", "/api/v2/auth/login", `{"username":"normal","password":"normal-password"}`, "", "", "")
	if status != 200 {
		t.Fatal(out)
	}
	oldToken := out["data"].(map[string]any)["token"].(string)
	if _, e = db.Exec(db.Rebind(`INSERT INTO v2_idempotency_requests VALUES(?,?,?,?,?,?,?)`), b.ID, "old", "fp", 200, "{}", "2026-11-05T00:00:00Z", "2030-01-01T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	status, out = call("PATCH", "/api/v2/users/"+b.ID, `{"account_type":"SAMPLING"}`, token, "", "")
	if status != 200 {
		t.Fatal(out)
	}
	if status, _ = call("GET", "/api/v2/auth/me", "", oldToken, "", ""); status != 401 {
		t.Fatal("session survived type update")
	}
	var n int
	db.Get(&n, db.Rebind(`SELECT COUNT(*) FROM v2_idempotency_requests WHERE actor_id=?`), b.ID)
	if n != 0 {
		t.Fatal("old retry survived type update")
	}
	// Authentication failures use the same 401 wording for unknown/disabled/locked.
	for _, name := range []string{"missing", "normal"} {
		s, m := call("POST", "/api/v2/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong-password"}`, name), "", "", "")
		if s != 401 || m["message"] != "登录名或密码不正确" {
			t.Fatal(s, m)
		}
	}
	// Old PHC hashes remain valid even when the old password is shorter than policy.
	oldHash, e := password.Hash("old")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(db.Rebind(`UPDATE users SET password_hash=? WHERE id=?`), oldHash, b.ID); e != nil {
		t.Fatal(e)
	}
	s, m := call("POST", "/api/v2/auth/login", `{"username":"normal","password":"old"}`, "", "", "")
	if s != 200 {
		t.Fatal(s, m)
	}
	// Logs never contain plaintext credentials or PHC hashes.
	var logs []string
	db.Select(&logs, `SELECT COALESCE(before_json,'') || COALESCE(after_json,'') FROM business_events`)
	for _, log := range logs {
		if strings.Contains(log, "good-password") || strings.Contains(log, "$argon2") {
			t.Fatal("secret in audit")
		}
	}
}
