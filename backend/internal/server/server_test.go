package server

import (
	"context"
	"encoding/json"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
	"strings"
	"time"

	"net/http/httptest"
	"testing"
)

func TestAuthenticatedV2Routes(t *testing.T) {
	db := store.NewTestDB(t)
	us := users.NewStore(db, "sqlite")
	_, err := us.Create(context.Background(), users.CreateInput{Username: "integration", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, "sqlite")
	cfg := &config.Config{CookieName: "sid"}
	r := Build(Deps{Cfg: cfg, Auth: authSvc, UserStore: us, Ledger: ledger.New(db)})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/product-lots", nil))
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v2/auth/login", strings.NewReader(`{"username":"integration","password":"integration-password"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var login struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &login); err != nil || login.Data.Token == "" {
		t.Fatal(w.Body, err)
	}
	for _, tc := range []struct {
		path, key string
		status    int
	}{{"/api/v2/product-lots", "", 400}, {"/api/v2/product-lots", "create", 201}, {"/api/forms", "legacy", 404}} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest("POST", tc.path, strings.NewReader(`{"shared_form_no":"AUTH-001","sample_identity":"M"}`))
		req.Header.Set("Authorization", "Bearer "+login.Data.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Idempotency-Key", tc.key)
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body)
		}
	}
}

func TestSamplingLoginAndAdminTypeManagement(t *testing.T) {
	db := store.NewTestDB(t)
	us := users.NewStore(db, "sqlite")
	_, e := us.Create(context.Background(), users.CreateInput{Username: "admin", Password: "test-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	as := auth.NewService(db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, "sqlite")
	r := Build(Deps{Cfg: &config.Config{CookieName: "sid"}, Auth: as, UserStore: us, Ledger: ledger.New(db)})
	call := func(method, path, body, token, key string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("X-Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		m := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	login := func(name string) (string, map[string]any) {
		status, m := call("POST", "/api/v2/auth/login", `{"username":"`+name+`","password":"test-password"}`, "", "")
		if status != 200 {
			t.Fatal(status, m)
		}
		d := m["data"].(map[string]any)
		return d["token"].(string), d
	}
	admin, _ := login("admin")
	status, m := call("POST", "/api/v2/users", `{"username":"sampling","password":"test-password","account_type":"SAMPLING"}`, admin, "")
	if status != 201 {
		t.Fatal(status, m)
	}
	uid := m["data"].(map[string]any)["id"].(string)
	token, d := login("sampling")
	if d["account_type"] != "SAMPLING" || d["submission_inspection_kind"] != "SAMPLING" {
		t.Fatal(d)
	}
	status, m = call("GET", "/api/v2/auth/me", "", token, "")
	if status != 200 || m["data"].(map[string]any)["submission_inspection_kind"] != "SAMPLING" {
		t.Fatal(status, m)
	}
	status, m = call("POST", "/api/v2/submissions", `{}`, token, "default")
	if status != 201 || m["data"].(map[string]any)["submission"].(map[string]any)["inspection_kind"] != "SAMPLING" {
		t.Fatal(status, m)
	}
	status, m = call("POST", "/api/v2/submissions", `{"inspection_kind":"NORMAL"}`, token, "opposite")
	if status != 403 {
		t.Fatal(status, m)
	}
	status, m = call("POST", "/api/v2/users", `{"username":"denied","password":"test-password"}`, token, "")
	if status != 403 {
		t.Fatal(status, m)
	}
	status, m = call("PATCH", "/api/v2/users/"+uid, `{"account_type":"NORMAL"}`, admin, "")
	if status != 200 {
		t.Fatal(status, m)
	}
	status, m = call("GET", "/api/v2/auth/me", "", token, "")
	if status != 401 {
		t.Fatal(status, m)
	}
	_, d = login("sampling")
	if d["account_type"] != "NORMAL" {
		t.Fatal(d)
	}
	for _, path := range []string{"/api/auth/login", "/api/forms", "/api/stats/summary", "/api/form-fields", "/api/sample/pending", "/api/v2/forms"} {
		status, _ = call("GET", path, "", admin, "")
		if status != 404 {
			t.Fatal(path, status)
		}
	}
	for _, body := range []string{`{"username":"invalid","password":"x","account_type":"OTHER"}`, `{"username":"invalid","password":"x","legacy_role":"SAMPLING"}`} {
		status, m = call("POST", "/api/v2/users", body, admin, "")
		if status != 400 {
			t.Fatal(status, m)
		}
	}
}
