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

	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestSingleBusinessWriteDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, selected := range []string{"legacy", "v2", ""} {
		r := gin.New()
		for _, domain := range []string{"legacy", "v2"} {
			prefix := "/api"
			if domain == "v2" {
				prefix = "/api/v2"
			}
			g := r.Group(prefix, businessWriteGate(selected, domain))
			for _, method := range []string{"GET", "POST", "PATCH"} {
				g.Handle(method, "/forms", func(c *gin.Context) { c.Status(204) })
			}
			if domain == "legacy" {
				g.POST("/dict/items", func(c *gin.Context) { c.Status(204) })
			}
		}
		for _, path := range []string{"/api/forms", "/api/v2/forms", "/api/dict/items"} {
			for _, method := range []string{"GET", "POST"} {
				if path == "/api/dict/items" && method == "GET" {
					continue
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				want := 204
				active := selected
				if active == "" {
					active = "v2"
				}
				if method == "POST" && ((path == "/api/forms" && active != "legacy") || (path == "/api/v2/forms" && active != "v2")) {
					want = 409
				}
				if w.Code != want {
					t.Fatalf("%s %s %s: %d want %d", selected, method, path, w.Code, want)
				}
			}
		}
	}
}

func TestAuthenticatedV2Routes(t *testing.T) {
	db := store.NewTestDB(t)
	us := users.NewStore(db, "sqlite")
	_, err := us.Create(context.Background(), users.CreateInput{Username: "integration", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, "sqlite")
	cfg := &config.Config{BusinessWriteDomain: "v2", CookieName: "sid"}
	r := Build(Deps{Cfg: cfg, Auth: authSvc, UserStore: us, Ledger: ledger.New(db)})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v2/product-lots", nil))
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"integration","password":"integration-password"}`))
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
	}{{"/api/v2/product-lots", "", 400}, {"/api/v2/product-lots", "create", 201}, {"/api/forms", "legacy", 409}} {
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
