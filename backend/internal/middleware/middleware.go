// Package middleware 提供 gin 中间件：认证、管理员校验、CORS、请求 ID。
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 上下文键
const (
	CtxUser     = "current_user"
	CtxUserID   = "current_user_id"
	CtxUserName = "current_user_name"
	CtxIsAdmin  = "current_is_admin"
)

// Authenticator 是中间件需要的认证能力（由 auth.Service 实现，便于测试替换）。
type Authenticator interface {
	// Authenticate 校验令牌，返回用户信息；令牌无效时返回错误。
	Authenticate(token string) (*Principal, error)
}

// Principal 是认证后的调用者身份。
type Principal struct {
	AccountType string `json:"account_type"`
	UserID      string
	Username    string
	DisplayName string
	IsAdmin     bool
}

// Auth 要求请求携带有效会话（Cookie 或 Authorization: Bearer）。
func Auth(a Authenticator, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := TokenFromRequest(c, cookieName)
		if token == "" {
			httpx.Fail(c, httpx.Unauthorized("缺少会话信息，请先登录"))
			c.Abort()
			return
		}
		p, err := a.Authenticate(token)
		if err != nil {
			httpx.Fail(c, err)
			c.Abort()
			return
		}
		c.Set(CtxUser, p)
		c.Set(CtxUserID, p.UserID)
		c.Set(CtxUserName, p.DisplayName)
		c.Set(CtxIsAdmin, p.IsAdmin)
		c.Next()
	}
}

// AdminOnly 要求调用者具备管理员标记（用于用户管理接口）。
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAdmin(c) {
			httpx.Fail(c, httpx.Forbidden("该操作仅管理员可执行"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// TokenFromRequest 依次从 Cookie、Authorization 头、X-Auth-Token 头提取令牌。
func TokenFromRequest(c *gin.Context, cookieName string) string {
	if cookieName != "" {
		if v, err := c.Cookie(cookieName); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if h := c.GetHeader("Authorization"); h != "" {
		if i := strings.IndexByte(h, ' '); i > 0 && strings.EqualFold(h[:i], "bearer") {
			return strings.TrimSpace(h[i+1:])
		}
	}
	return strings.TrimSpace(c.GetHeader("X-Auth-Token"))
}

// CurrentPrincipal 返回当前调用者；未认证时返回 nil。
func CurrentPrincipal(c *gin.Context) *Principal {
	if v, ok := c.Get(CtxUser); ok {
		if p, ok := v.(*Principal); ok {
			return p
		}
	}
	return nil
}

// CurrentUserID 返回当前调用者 ID（未认证时为空串）。
func CurrentUserID(c *gin.Context) string {
	if v, ok := c.Get(CtxUserID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CurrentUserName 返回当前调用者显示名。
func CurrentUserName(c *gin.Context) string {
	if v, ok := c.Get(CtxUserName); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// IsAdmin 报告当前调用者是否为管理员。
func IsAdmin(c *gin.Context) bool {
	if v, ok := c.Get(CtxIsAdmin); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// CORS 允许配置的来源跨域访问；origins 为空时不加 CORS 头。
func CORS(origins []string) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowAll || allowed[origin]) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			// 必须覆盖前端实际会发的自定义头，否则预检失败：
			// Authorization / X-Auth-Token（认证）、X-Request-Id（追踪）、
			// X-Idempotency-Key 与 Idempotency-Key（幂等写入）。
			h.Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, X-Auth-Token, X-Request-Id, "+
					"X-Idempotency-Key, Idempotency-Key")
			h.Set("Access-Control-Expose-Headers", "X-Request-Id, X-Idempotent-Replay")
			h.Set("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// RequestID 生成/透传请求 ID，便于日志关联。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = c.GetHeader("X-Request-ID")
		}
		if rid == "" {
			rid = newRequestID()
		}
		c.Set("request_id", rid)
		c.Writer.Header().Set("X-Request-Id", rid)
		c.Next()
	}
}
