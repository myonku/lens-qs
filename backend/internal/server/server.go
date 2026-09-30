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
	"github.com/myonku/lens-qs-api/internal/idempotency"
	idempotencyhttp "github.com/myonku/lens-qs-api/internal/idempotency/http"
	"github.com/myonku/lens-qs-api/internal/inspection"
	inspectionhttp "github.com/myonku/lens-qs-api/internal/inspection/http"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/sample"
	samplehttp "github.com/myonku/lens-qs-api/internal/sample/http"
	"github.com/myonku/lens-qs-api/internal/stats"
	statshttp "github.com/myonku/lens-qs-api/internal/stats/http"
	"github.com/myonku/lens-qs-api/internal/users"
)

// Deps 是构建路由所需的依赖。
type Deps struct {
	Cfg         *config.Config
	Auth        *auth.Service
	UserStore   *users.Store
	Dict        *dictionary.Store
	Sample      *sample.Service
	Inspection  *inspection.Service
	Stats       *stats.Service
	Idempotency *idempotency.Store // 为 nil 时关闭幂等
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
			"version": "0.1.0",
			"docs": gin.H{
				"requirements": "docs/requirements.md",
				"design":       "docs/design.md",
				"features":     "docs/features.md",
				"api":          "docs/api.md",
			},
		})
	})

	api := r.Group("/api")

	// 公开接口
	authH := authhttp.New(d.Auth, d.UserStore, d.Cfg.CookieName, d.Cfg.CookieSecure)
	authH.RegisterPublic(api)

	// 需要登录的接口（认证是唯一硬边界；权限只有"普通用户 / 管理员"两级）
	authed := api.Group("", middleware.Auth(d.Auth, d.Cfg.CookieName))
	// 幂等：写接口在 TTL 内用相同幂等键重复提交时回放首次响应，避免弱网重试重复写入
	authed.Use(idempotencyhttp.Middleware(d.Idempotency))
	authH.RegisterAuthed(authed)
	dictionaryhttp.New(d.Dict).Register(authed)
	inspectionhttp.New(d.Inspection).Register(authed)
	samplehttp.New(d.Sample, d.Inspection).Register(authed)
	statshttp.New(d.Stats).Register(authed)

	return r
}
