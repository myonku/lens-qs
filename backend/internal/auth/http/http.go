// Package authhttp 提供认证与用户管理的 HTTP 接口。
package authhttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/users"
)

// Handler 聚合认证与用户接口。
type Handler struct {
	svc          *auth.Service
	users        *users.Store
	cookieName   string
	cookieSecure bool
}

// New 构造 Handler。
func New(svc *auth.Service, userStore *users.Store, cookieName string, cookieSecure bool) *Handler {
	return &Handler{svc: svc, users: userStore, cookieName: cookieName, cookieSecure: cookieSecure}
}

// RegisterPublic 注册无需登录的接口。
func (h *Handler) RegisterPublic(r *gin.RouterGroup) {
	r.POST("/auth/login", h.login)
}

// RegisterAuthed 注册需要登录的接口。
// 注意："谁能否做什么"的判定集中在 middleware 的 Auth / AdminOnly，两级（普通用户 / 管理员）。
func (h *Handler) RegisterAuthed(r *gin.RouterGroup) {
	r.POST("/auth/logout", h.logout)
	r.GET("/auth/me", h.me)
	r.POST("/auth/change-password", h.changePassword)

	admin := r.Group("/users", middleware.AdminOnly())
	admin.GET("", h.listUsers)
	admin.POST("", h.createUser)
	admin.PATCH("/:id", h.updateUser)
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Username, req.Password, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	h.setCookie(c, res.Token)
	httpx.OK(c, gin.H{
		"token":      res.Token,
		"expires_at": res.ExpiresAt,
		"user":       res.User,
		"note":       "浏览器可用 Cookie；其它调用方可使用 Authorization: Bearer <token>",
	})
}

func (h *Handler) logout(c *gin.Context) {
	token := middleware.TokenFromRequest(c, h.cookieName)
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		httpx.Fail(c, err)
		return
	}
	h.clearCookie(c)
	httpx.OK(c, gin.H{"logged_out": true})
}

func (h *Handler) me(c *gin.Context) {
	u, err := h.users.GetByID(c.Request.Context(), middleware.CurrentUserID(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if u == nil {
		httpx.Fail(c, httpx.NotFound("用户不存在"))
		return
	}
	httpx.OK(c, gin.H{"user": u, "is_admin": u.IsAdmin})
}

type changePwdReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

func (h *Handler) changePassword(c *gin.Context) {
	var req changePwdReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if err := h.svc.ChangePassword(c.Request.Context(), middleware.CurrentUserID(c), req.OldPassword, req.NewPassword); err != nil {
		httpx.Fail(c, err)
		return
	}
	h.clearCookie(c)
	httpx.OK(c, gin.H{"changed": true, "note": "密码已修改，原会话已全部失效，请重新登录"})
}

func (h *Handler) listUsers(c *gin.Context) {
	page, size := pageParams(c)
	list, total, err := h.users.List(c.Request.Context(), c.Query("keyword"), (page-1)*size, size)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, httpx.NewPage(list, total, page, size))
}

type createUserReq struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required"`
	DisplayName string `json:"display_name"`
	EmployeeNo  string `json:"employee_no"`
	UnitID      string `json:"unit_id"`
	IsAdmin     bool   `json:"is_admin"`
}

func (h *Handler) createUser(c *gin.Context) {
	var req createUserReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	u, err := h.users.Create(c.Request.Context(), users.CreateInput{
		Username:      req.Username,
		Password:      req.Password,
		DisplayName:   req.DisplayName,
		EmployeeNo:    optString(req.EmployeeNo),
		UnitID:        optString(req.UnitID),
		IsAdmin:       req.IsAdmin,
		MustChangePwd: false,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, u)
}

type updateUserReq struct {
	DisplayName *string `json:"display_name"`
	EmployeeNo  *string `json:"employee_no"`
	UnitID      *string `json:"unit_id"`
	IsAdmin     *bool   `json:"is_admin"`
	Status      *string `json:"status"`
	Password    *string `json:"password"`
}

func (h *Handler) updateUser(c *gin.Context) {
	var req updateUserReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	targetID := c.Param("id")
	if req.IsAdmin != nil && !*req.IsAdmin && targetID == middleware.CurrentUserID(c) {
		httpx.Fail(c, httpx.BadRequest("不能取消自己的管理员权限"))
		return
	}
	if req.Status != nil && strings.EqualFold(*req.Status, "disabled") && targetID == middleware.CurrentUserID(c) {
		httpx.Fail(c, httpx.BadRequest("不能停用自己的账号"))
		return
	}
	u, err := h.users.Update(c.Request.Context(), targetID, users.UpdateInput{
		DisplayName: req.DisplayName,
		EmployeeNo:  req.EmployeeNo,
		UnitID:      req.UnitID,
		IsAdmin:     req.IsAdmin,
		Status:      req.Status,
		Password:    req.Password,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if (req.Status != nil && strings.EqualFold(*req.Status, "disabled")) || req.Password != nil {
		_ = h.svc.RevokeAll(c.Request.Context(), u.ID)
	}
	httpx.OK(c, u)
}

func (h *Handler) setCookie(c *gin.Context, token string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, token, 12*3600, "/", "", h.cookieSecure, true)
}

func (h *Handler) clearCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.cookieName, "", -1, "/", "", h.cookieSecure, true)
}

func pageParams(c *gin.Context) (page, size int) {
	page, size = 1, 20
	if n, err := strconv.Atoi(c.Query("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(c.Query("page_size")); err == nil && n > 0 && n <= 200 {
		size = n
	}
	return page, size
}

func optString(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
