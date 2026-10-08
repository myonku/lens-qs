// Package dictionaryhttp 提供字典 / 单位 / 参考部件号的 HTTP 接口。
package dictionaryhttp

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// Handler 聚合字典接口。
type Handler struct {
	store *dictionary.Store
}

// New 构造 Handler。
func New(store *dictionary.Store) *Handler { return &Handler{store: store} }

// Register 注册接口。
func (h *Handler) Register(r *gin.RouterGroup) {
	r.GET("/dict/items", h.listItems)
	r.POST("/dict/items", h.upsertItem)
	r.PATCH("/dict/items/:id", h.updateItem)
	r.GET("/dict/categories", h.categories)

	r.GET("/units", h.listUnits)
	r.POST("/units", h.upsertUnit)

	r.GET("/dict/drawings", h.listDrawings)
	r.POST("/dict/drawings/import", h.importDrawings)
}

func (h *Handler) listItems(c *gin.Context) {
	items, err := h.store.ListItems(c.Request.Context(), c.Query("category"), c.Query("include_inactive") == "true")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if items == nil {
		items = []dictionary.Item{}
	}
	httpx.OK(c, gin.H{"items": items, "total": len(items)})
}

type itemReq struct {
	Category string          `json:"category" binding:"required"`
	Code     string          `json:"code" binding:"required"`
	Label    string          `json:"label"`
	SortNo   int             `json:"sort_no"`
	Active   *bool           `json:"active"`
	Meta     json.RawMessage `json:"meta"`
}

func (h *Handler) upsertItem(c *gin.Context) {
	var req itemReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var meta *string
	if len(req.Meta) > 0 && string(req.Meta) != "null" {
		s := string(req.Meta)
		meta = &s
	}
	it, err := h.store.UpsertItem(c.Request.Context(), &dictionary.Item{
		Category: req.Category,
		Code:     req.Code,
		Label:    req.Label,
		SortNo:   req.SortNo,
		Active:   active,
		Meta:     meta,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, it)
}

type updateItemReq struct {
	Label  *string          `json:"label"`
	SortNo *int             `json:"sort_no"`
	Active *bool            `json:"active"`
	Meta   *json.RawMessage `json:"meta"`
}

func (h *Handler) updateItem(c *gin.Context) {
	var req updateItemReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	var meta *string
	if req.Meta != nil && string(*req.Meta) != "null" {
		s := string(*req.Meta)
		meta = &s
	}
	it, err := h.store.UpdateItem(c.Request.Context(), c.Param("id"), req.Label, req.SortNo, req.Active, meta)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, it)
}

func (h *Handler) categories(c *gin.Context) {
	httpx.OK(c, gin.H{"categories": []string{
		dictionary.CategoryProcess,
		dictionary.CategoryInspectionOp,
		dictionary.CategorySampleProject,
		dictionary.CategorySampleModel,
		dictionary.CategorySampleSpec,
		dictionary.CategorySampleSize,
		dictionary.CategorySampleSide,
		dictionary.CategorySampleClass,
		dictionary.CategoryUnit,
	}})
}

func (h *Handler) listUnits(c *gin.Context) {
	units, err := h.store.ListUnits(c.Request.Context(), c.Query("include_inactive") == "true")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if units == nil {
		units = []dictionary.Unit{}
	}
	httpx.OK(c, gin.H{"items": units, "total": len(units)})
}

type unitReq struct {
	Code   string `json:"code" binding:"required"`
	Name   string `json:"name"`
	SortNo int    `json:"sort_no"`
	Active *bool  `json:"active"`
}

func (h *Handler) upsertUnit(c *gin.Context) {
	var req unitReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	u, err := h.store.UpsertUnit(c.Request.Context(), &dictionary.Unit{
		Code: req.Code, Name: req.Name, SortNo: req.SortNo, Active: active,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, u)
}

func (h *Handler) listDrawings(c *gin.Context) {
	list, err := h.store.ListDrawings(c.Request.Context(), dictionary.DrawingFilter{
		Project: c.Query("project"),
		Model:   c.Query("model"),
		Spec:    c.Query("spec"),
		Size:    c.Query("size"),
		Side:    c.Query("side"),
		Keyword: c.Query("keyword"),
		Limit:   500,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if list == nil {
		list = []dictionary.Drawing{}
	}
	httpx.OK(c, gin.H{"items": list, "total": len(list),
		"note": "参考清单仅用于辅助选择；输入与清单不一致不影响保存"})
}

type drawingCSVReq struct {
	// 支持两种导入方式：结构化数组，或制表符/逗号分隔的整行文本
	Items []dictionary.Drawing `json:"items"`
	Rows  []string             `json:"rows"`
}

func (h *Handler) importDrawings(c *gin.Context) {
	var req drawingCSVReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	list := req.Items
	for _, row := range req.Rows {
		if d, ok := parseDrawingRow(row); ok {
			list = append(list, d)
		}
	}
	if len(list) == 0 {
		httpx.Fail(c, httpx.BadRequest("没有可导入的数据（items 或 rows 至少提供一个）"))
		return
	}
	inserted, updated, err := h.store.UpsertDrawings(c.Request.Context(), list)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"inserted": inserted, "updated": updated, "total": len(list)})
}

// parseDrawingRow 解析形如 "05\tS1 - MD - R - VID2 - MONOC & BINOC" 的一行。
func parseDrawingRow(row string) (dictionary.Drawing, bool) {
	row = strings.TrimSpace(row)
	if row == "" {
		return dictionary.Drawing{}, false
	}
	fields := strings.SplitN(row, "\t", 2)
	if len(fields) == 1 {
		fields = strings.SplitN(row, ",", 2)
	}
	seq := strings.TrimSpace(fields[0])
	drawing := ""
	if len(fields) == 2 {
		drawing = strings.TrimSpace(fields[1])
	} else {
		seq, drawing = "", seq
	}
	if drawing == "" {
		return dictionary.Drawing{}, false
	}
	d := dictionary.Drawing{DrawingNo: drawing, Active: true}
	if seq != "" {
		d.SeqNo = &seq
	}
	// 依据绘图号的书写顺序（规格-大小-左右-型号-结构类别）尽力拆解
	upper := strings.ToUpper(drawing)
	for _, tok := range strings.FieldsFunc(upper, func(r rune) bool {
		return r == '-' || r == ' ' || r == '_'
	}) {
		switch tok {
		case "S1", "S2":
			v := tok
			d.SpecCode = &v
		case "MD", "SM", "LG":
			v := tok
			d.SizeCode = &v
		case "L", "R":
			v := tok
			d.SideCode = &v
		case "VID1", "VID2":
			v := tok
			d.ModelCode = &v
		case "RX":
			d.IsRx = true
		case "MONOC":
			v := "MONOC"
			d.ClassCode = &v
		case "BINOC":
			v := "BINOC"
			d.ClassCode = &v
		}
	}
	if strings.Contains(upper, "MONOC") && strings.Contains(upper, "BINOC") {
		v := "MONOC_AND_BINOC"
		d.ClassCode = &v
	}
	return d, true
}

// 说明：旧的 defectColumns 处理器已删除。
// 它返回的是**旧模型**硬编码的 5 类缺陷（spot_* / debris_* 等），与现行的
// 35 列不良缺陷明细不一致，留着会误导前端。
// 列布局请使用 GET /api/form-fields 与 GET /api/form-templates（唯一事实来源：internal/formfields）。

