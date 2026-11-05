package dictionaryhttp

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

type Handler struct{ store *dictionary.Store }

func New(s *dictionary.Store) *Handler { return &Handler{s} }
func (h *Handler) Register(r *gin.RouterGroup) {
	r.GET("/dict/items", h.list)
	r.POST("/dict/items", h.upsert)
	r.PATCH("/dict/items/:id", h.patch)
	r.GET("/dict/categories", h.categories)
	r.GET("/units", h.units)
}
func (h *Handler) list(c *gin.Context) {
	if e := httpx.Query(c, "category", "include_inactive"); e != nil {
		httpx.Fail(c, e)
		return
	}
	v := c.Query("include_inactive")
	if v != "" && v != "true" && v != "false" {
		httpx.Fail(c, httpx.BadRequest("invalid include_inactive"))
		return
	}
	items, e := h.store.ListItems(c.Request.Context(), c.Query("category"), v == "true")
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	if items == nil {
		items = []dictionary.Item{}
	}
	httpx.OK(c, gin.H{"items": items, "total": len(items)})
}
func (h *Handler) categories(c *gin.Context) {
	if e := httpx.Query(c); e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, gin.H{"categories": dictionary.Categories()})
}
func (h *Handler) units(c *gin.Context) {
	if e := httpx.Query(c); e != nil {
		httpx.Fail(c, e)
		return
	}
	items, e := h.store.ListUnits(c.Request.Context(), false)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": len(items)})
}

type itemReq struct {
	Category string                 `json:"category" binding:"required"`
	Code     string                 `json:"code" binding:"required"`
	Label    httpx.Optional[string] `json:"label"`
	SortNo   httpx.Optional[int]    `json:"sort_no"`
	Active   httpx.Optional[bool]   `json:"active"`
	Meta     json.RawMessage        `json:"meta"`
}

func (h *Handler) upsert(c *gin.Context) {
	var req itemReq
	if e := httpx.BindJSON(c, &req); e != nil {
		httpx.Fail(c, e)
		return
	}
	active := true
	if req.Label.Set && req.Label.Value == nil || req.SortNo.Set && req.SortNo.Value == nil || req.Active.Set && req.Active.Value == nil {
		httpx.Fail(c, httpx.BadRequest("label/sort_no/active cannot be null"))
		return
	}
	label := ""
	sortNo := 0
	if req.Label.Value != nil {
		label = *req.Label.Value
	}
	if req.SortNo.Value != nil {
		sortNo = *req.SortNo.Value
	}
	if req.Active.Value != nil {
		active = *req.Active.Value
	}
	var meta *string
	if len(req.Meta) > 0 && string(req.Meta) != "null" {
		v := string(req.Meta)
		meta = &v
	}
	it, e := h.store.UpsertItem(c.Request.Context(), &dictionary.Item{Category: req.Category, Code: req.Code, Label: label, SortNo: sortNo, Active: active, Meta: meta})
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, it)
}

type patchReq struct {
	Label  httpx.Optional[string]          `json:"label"`
	SortNo httpx.Optional[int]             `json:"sort_no"`
	Active httpx.Optional[bool]            `json:"active"`
	Meta   httpx.Optional[json.RawMessage] `json:"meta"`
}

func (h *Handler) patch(c *gin.Context) {
	var req patchReq
	if e := httpx.BindJSON(c, &req); e != nil {
		httpx.Fail(c, e)
		return
	}
	if !req.Label.Set && !req.SortNo.Set && !req.Active.Set && !req.Meta.Set || req.Label.Set && req.Label.Value == nil || req.SortNo.Set && req.SortNo.Value == nil || req.Active.Set && req.Active.Value == nil {
		httpx.Fail(c, httpx.BadRequest("empty patch or invalid null"))
		return
	}
	it, e := h.store.UpdateItem(c.Request.Context(), c.Param("id"), req.Label.Value, req.SortNo.Value, req.Active.Value, req.Meta)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, it)
}
