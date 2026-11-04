// Package server 组装 HTTP 路由。
package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/auth"
	authhttp "github.com/myonku/lens-qs-api/internal/auth/http"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	dictionaryhttp "github.com/myonku/lens-qs-api/internal/dictionary/http"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/sample"
	samplehttp "github.com/myonku/lens-qs-api/internal/sample/http"
	"github.com/myonku/lens-qs-api/internal/users"
)

// Deps 是构建路由所需的依赖。
type Deps struct {
	Ledger    *ledger.Service
	Cfg       *config.Config
	Auth      *auth.Service
	UserStore *users.Store
	Dict      *dictionary.Store
	Sample    *sample.Service
}

// Build 组装并返回 gin 引擎。
func Build(d Deps) *gin.Engine {
	if d.Cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), httpx.Recovery(), httpx.ErrorHandler(), middleware.RequestID())
	r.Use(middleware.CORS(d.Cfg.CORSOrigins))

	// 健康检查（无需登录）
	r.GET("/healthz", func(c *gin.Context) {
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
	authed := api.Group("", middleware.Auth(d.Auth, d.Cfg.CookieName))
	authH.RegisterAuthed(authed)
	if d.Ledger != nil {
		d.Ledger.Register(authed)
	}
	dictionaryhttp.New(d.Dict).Register(authed)
	samplehttp.New(d.Sample).Register(authed)
	return r
}
