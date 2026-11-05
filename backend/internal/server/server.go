// Package server 组装 HTTP 路由。
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/myonku/lens-qs-api/internal/auth"
	authhttp "github.com/myonku/lens-qs-api/internal/auth/http"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	dictionaryhttp "github.com/myonku/lens-qs-api/internal/dictionary/http"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/sample"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

// Deps 是构建路由所需的依赖。
type Deps struct {
	Cfg       *config.Config
	Auth      *auth.Service
	UserStore *users.Store
	Dict      *dictionary.Store
}

// Build 组装并返回 gin 引擎。
func Build(d Deps) *gin.Engine {
	if d.Cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.NoRoute(func(c *gin.Context) { httpx.Fail(c, httpx.NotFound("未定义路径")) })
	r.Use(gin.Logger(), httpx.Recovery(), httpx.ErrorHandler(), middleware.RequestID())
	r.Use(middleware.CORS(d.Cfg.CORSOrigins))
	r.Use(func(c *gin.Context) {
		if c.FullPath() == "" {
			c.Next()
			return
		}
		var allowed []string
		// New business handlers own their complete endpoint-specific whitelist.
		path := c.FullPath()
		for _, prefix := range []string{"/api/v2/product-lots", "/api/v2/operation-groups", "/api/v2/submissions", "/api/v2/stats/", "/api/v2/reports/", "/api/v2/audit-logs", "/api/v2/sample/", "/api/v2/dict/drawings"} {
			if strings.HasPrefix(path, prefix) {
				c.Next()
				return
			}
		}
		switch c.FullPath() {
		case "/api/v2/users":
			if c.Request.Method == "GET" {
				allowed = []string{"page", "page_size", "keyword"}
			}
		case "/api/v2/dict/items":
			if c.Request.Method == "GET" {
				allowed = []string{"category", "include_inactive"}
			}
		}
		if e := httpx.Query(c, allowed...); e != nil {
			httpx.Fail(c, e)
			c.Abort()
			return
		}
		c.Next()
	})

	// 健康检查（无需登录）
	r.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := d.UserStore.DB().PingContext(ctx); err != nil {
			c.JSON(503, gin.H{"status": "unavailable", "time": time.Now().UTC().Format(time.RFC3339)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
	})
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name":    "lens-qs-api",
			"version": "2.0.0",
			"docs": gin.H{
				"requirements": "docs/requirements.md",
				"design":       "docs/design.md",
				"features":     "docs/features.md",
				"api":          "docs/api.md",
			},
		})
	})

	api := r.Group("/api/v2")
	authH := authhttp.New(d.Auth, d.UserStore, d.Cfg.CookieName, d.Cfg.CookieSecure)
	authH.RegisterPublic(api)
	authed := api.Group("", middleware.Auth(d.Auth, d.Cfg.CookieName, d.Cfg.CORSOrigins...))
	authH.RegisterAuthed(authed)
	dictionaryhttp.New(d.Dict).Register(authed)
	authed.GET("/form-fields", func(c *gin.Context) {
		if e := httpx.Query(c); e != nil {
			httpx.Fail(c, e)
			return
		}
		catalog, e := store.ReadCatalog(c.Request.Context(), d.UserStore.DB(), "2")
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		type surface struct {
			Code  string `json:"code"`
			Label string `json:"label"`
		}
		type defect struct {
			Code  string `json:"code"`
			Label string `json:"label"`
			Order int    `json:"order"`
		}
		surfaces := []surface{}
		for _, code := range catalog.Surfaces {
			surfaces = append(surfaces, surface{code, formfields.SurfaceLabels[code]})
		}
		defects := []defect{}
		for i, d := range catalog.DefectCategories {
			defects = append(defects, defect{d.Code, d.Label, i + 1})
		}
		httpx.OK(c, struct {
			formfields.FieldCatalog
			Surfaces []surface `json:"surfaces"`
			Defects  []defect  `json:"defect_categories"`
		}{catalog, surfaces, defects})
	})
	templates := func(c *gin.Context) {
		if e := httpx.Query(c); e != nil {
			httpx.Fail(c, e)
			return
		}
		items, e := store.Templates(c.Request.Context(), d.UserStore.DB(), c.Param("id"))
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		if c.Param("id") != "" {
			httpx.OK(c, items[0])
		} else {
			httpx.OK(c, gin.H{"items": items, "total": len(items)})
		}
	}
	authed.GET("/form-templates", templates)
	authed.GET("/form-templates/:id", templates)
	ledger.Register(authed, d.UserStore.DB())
	dictionary.RegisterDrawings(authed, d.UserStore.DB())
	sample.Register(authed, d.UserStore.DB())
	return r
}
