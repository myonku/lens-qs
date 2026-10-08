// Package statshttp 提供汇总统计接口。
package statshttp

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/stats"
)

// Handler 聚合统计接口。
type Handler struct {
	svc *stats.Service
}

// New 构造 Handler。
func New(svc *stats.Service) *Handler { return &Handler{svc: svc} }

// Register 注册接口。
func (h *Handler) Register(r *gin.RouterGroup) {
	r.GET("/stats/dimensions", h.dimensions)
	r.GET("/stats/summary", h.summary)
	r.GET("/stats/defects", h.defects)
	r.GET("/stats/workload", h.workload)
	r.GET("/stats/incomplete-forms", h.incomplete)
}

func (h *Handler) dimensions(c *gin.Context) {
	httpx.OK(c, gin.H{"dimensions": stats.SupportedDimensions()})
}

// filterFromQuery 从 query 读取筛选条件；默认只看未作废的（include_void=true 可含已作废）。
func filterFromQuery(c *gin.Context) stats.Filter {
	return stats.Filter{
		From:          c.Query("from"),
		To:            c.Query("to"),
		OperationCode: c.Query("operation_code"),
		Project:       c.Query("sample_project"),
		Model:         c.Query("sample_model"),
		Spec:          c.Query("sample_spec"),
		Size:          c.Query("sample_size"),
		Side:          c.Query("sample_side"),
		SubmittedBy:   c.Query("submitted_by"),
		Unit:          c.Query("unit"),
		OnlyActive:    c.Query("include_void") != "true",
	}
}

// summary 支持 ?group_by=sample_project,sample_size 或 ?group_by=sample_project&group_by=sample_size
func (h *Handler) summary(c *gin.Context) {
	var groupBy []string
	for _, raw := range c.QueryArray("group_by") {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				groupBy = append(groupBy, p)
			}
		}
	}
	res, err := h.svc.Summary(c.Request.Context(), filterFromQuery(c), groupBy)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) defects(c *gin.Context) {
	res, err := h.svc.DefectBreakdown(c.Request.Context(), filterFromQuery(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) workload(c *gin.Context) {
	field := c.DefaultQuery("by", "submitted_by")
	list, err := h.svc.WorkloadBy(c.Request.Context(), filterFromQuery(c), field)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"by": field, "items": list, "total": len(list)})
}

func (h *Handler) incomplete(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		n := 0
		for _, r := range v {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		if n > 0 {
			limit = n
		}
	}
	list, err := h.svc.IncompleteForms(c.Request.Context(), limit)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list, "total": len(list),
		"note": "这些笔尚未填完整（缺失表单既有栏目或计数明细），可供跟进"})
}
