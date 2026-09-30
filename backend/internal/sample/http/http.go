// Package samplehttp 提供样品信息解析接口。
package samplehttp

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/inspection"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/sample"
)

// Handler 聚合样品信息接口。
type Handler struct {
	svc     *sample.Service
	inspSvc *inspection.Service
}

// New 构造 Handler。
func New(svc *sample.Service, insp *inspection.Service) *Handler {
	return &Handler{svc: svc, inspSvc: insp}
}

// Register 注册接口。
func (h *Handler) Register(r *gin.RouterGroup) {
	r.POST("/sample/parse", h.parse)
	r.POST("/sample/confirm", h.confirm)
	r.GET("/sample/cache", h.cacheInfo)
	r.GET("/sample/pending", h.pending)
}

type parseReq struct {
	Raw string `json:"sample_raw"`
}

func (h *Handler) parse(c *gin.Context) {
	var req parseReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.svc.Parse(c.Request.Context(), req.Raw)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

type confirmReq struct {
	Raw     string `json:"sample_raw" binding:"required"`
	Project string `json:"project"`
	Model   string `json:"model"`
	Spec    string `json:"spec"`
	Size    string `json:"size"`
	Side    string `json:"side"`
	Class   string `json:"class"`
	IsRx    bool   `json:"is_rx"`
}

func (h *Handler) confirm(c *gin.Context) {
	var req confirmReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	p, err := h.svc.Confirm(c.Request.Context(), req.Raw, sample.Parsed{
		Raw:     req.Raw,
		Project: req.Project,
		Model:   req.Model,
		Spec:    req.Spec,
		Size:    req.Size,
		Side:    req.Side,
		Class:   req.Class,
		IsRx:    req.IsRx,
	}, middleware.CurrentUserID(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"parsed": p, "note": "已记录人工确认结果；原始串未被改写"})
}

func (h *Handler) cacheInfo(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("sample_raw"))
	if raw == "" {
		httpx.Fail(c, httpx.BadRequest("缺少 sample_raw 参数"))
		return
	}
	res, err := h.svc.Parse(c.Request.Context(), raw)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"normalized_key": sample.NormalizeKey(raw), "from_cache": res.FromCache,
		"tokens": res.Parsed.Tokens, "unknown_tokens": res.Parsed.Unknown})
}

// pending 列出需要人工核对的表单（解析不匹配或结构化字段缺失）。
func (h *Handler) pending(c *gin.Context) {
	forms, total, err := h.inspSvc.Store().ListForms(c.Request.Context(), inspection.FormFilter{
		State:       "ACTIVE",
		NeedsReview: true,
		Limit:       100,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if forms == nil {
		forms = []inspection.Form{}
	}
	httpx.OK(c, gin.H{"items": forms, "total": total,
		"note": "这些表单的样品信息未能与参考清单匹配；不影响数据可用性，仅供人工核对"})
}
