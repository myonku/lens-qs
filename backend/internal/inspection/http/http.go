// Package inspectionhttp 提供检验表单（提交）相关的 HTTP 接口。
//
// 端点分组：
//   - /form-templates            工序表单目录与列布局（前端"找到自己工序的表单"）
//   - /forms                     表单提交的列表 / 新建 / 取详情 / 更新 / 作废 / 历史 / 差异
//   - /form-fields               字段目录（顶层栏目 + 不良缺陷明细）
//   - /audit-logs                操作日志
package inspectionhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/inspection"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/operations"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// Handler 聚合检验表单接口。
type Handler struct {
	svc *inspection.Service
}

// New 构造 Handler。
func New(svc *inspection.Service) *Handler { return &Handler{svc: svc} }

// Register 注册全部接口（均需登录）。
func (h *Handler) Register(r *gin.RouterGroup) {
	// 工序表单目录与字段目录
	r.GET("/form-templates", h.listTemplates)
	r.GET("/form-templates/:code", h.getTemplate)
	r.GET("/form-fields", h.listFields)

	// 表单提交
	r.GET("/forms", h.listForms)
	r.POST("/forms", h.createForm)
	r.POST("/forms/batch", h.createBatch)
	r.GET("/forms/:id", h.getForm)
	r.PATCH("/forms/:id", h.updateForm)
	r.POST("/forms/:id/void", h.voidForm)
	r.GET("/forms/:id/history", h.listHistory)
	r.GET("/forms/:id/history/diff", h.diffHistory)

	// 操作日志
	r.GET("/audit-logs", h.listAudit)
}

// ------------------------------- 请求结构 -------------------------------

// saveReq 是保存请求：form 是字段补丁（只提交本次要改的字段）。
type saveReq struct {
	FormNo      string          `json:"form_no"`
	Operation   string          `json:"operation_code"`
	Mode        string          `json:"mode"`    // merge（默认）| replace
	Enforce     string          `json:"enforce"` // warn（默认）| strict
	Reason      string          `json:"reason"`
	Expected    *int            `json:"expected_save_count"`
	ClientReqID string          `json:"client_request_id"`
	Form        json.RawMessage `json:"form"`

	// 兼容写法：把表头栏目与 lines 直接放在顶层（不写 form 包裹）
	Flat json.RawMessage `json:"-"`

	// 批量：一次提交多张表单（整批原子）
	Forms []json.RawMessage `json:"forms"`
}

func actorOf(c *gin.Context) inspection.Actor {
	return inspection.Actor{
		UserID:      middleware.CurrentUserID(c),
		DisplayName: middleware.CurrentUserName(c),
		IP:          c.ClientIP(),
		UserAgent:   c.GetHeader("User-Agent"),
	}
}

// ------------------------------- 工序表单与字段目录 -------------------------------

func (h *Handler) listTemplates(c *gin.Context) {
	list := make([]gin.H, 0, len(operations.Defaults))
	for _, t := range operations.Defaults {
		list = append(list, gin.H{
			"operation":      t,
			"layout":         inspection.LayoutFor(t.Code),
			"field_count":    len(formfields.HeaderColumns) + len(formfields.DefectFieldNames()),
		})
	}
	httpx.OK(c, gin.H{
		"items": list,
		"total": len(list),
		"note": "每个工序对应一张表单；不同工序所填内容不同，前端可按工序渲染对应列。" +
			"未登记的工序按通用模板处理（开放全部列，不校验必填）",
	})
}

func (h *Handler) getTemplate(c *gin.Context) {
	code := c.Param("code")
	httpx.OK(c, gin.H{
		"operation": inspection.TemplateFor(code),
		"layout":    inspection.LayoutFor(code),
	})
}

func (h *Handler) listFields(c *gin.Context) {
	layout := formfields.BuildLayout()
	httpx.OK(c, gin.H{
		"header_columns": layout.Header,
		"surfaces":       layout.Surfaces,
		"surface_labels": layout.SurfaceLabels,
		"defect_group":   layout.DefectGroup,
		"defect_columns": layout.DefectColumns,
		"stats": gin.H{
			"header_count": len(layout.Header),
			"defect_count": len(formfields.DefectGroups),
			"total_columns": len(layout.Header) + len(layout.DefectColumns),
		},
		"note": "不良缺陷明细下的每一项都包含凹面/凸面/双面三个子列",
	})
}

// ------------------------------- 表单提交 -------------------------------

func (h *Handler) listForms(c *gin.Context) {
	page, size := pageParams(c)
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		state = "ACTIVE"
	}
	list, total, err := h.svc.Store().ListSubmissions(c.Request.Context(), inspection.FormFilter{
		Keyword:       strings.TrimSpace(c.Query("keyword")),
		State:         state,
		OperationCode: c.Query("operation_code"),
		SampleProject: c.Query("sample_project"),
		SampleModel:   c.Query("sample_model"),
		SampleSpec:    c.Query("sample_spec"),
		SampleSize:    c.Query("sample_size"),
		SampleSide:    c.Query("sample_side"),
		Unit:          c.Query("unit"),
		From:          c.Query("from"),
		To:            c.Query("to"),
		Incomplete:    c.Query("incomplete") == "true",
		Offset:        (page - 1) * size,
		Limit:         size,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, httpx.NewPage(list, total, page, size))
}

// createForm 新建/更新一次提交（表单号相同即更新）。
func (h *Handler) createForm(c *gin.Context) {
	h.save(c, "")
}

// updateForm 按提交 ID 更新。
func (h *Handler) updateForm(c *gin.Context) {
	h.save(c, c.Param("id"))
}

func (h *Handler) save(c *gin.Context, submissionID string) {
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	body, _ := json.Marshal(req)

	in, err := h.buildInput(req, submissionID, body)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.svc.Save(c.Request.Context(), actorOf(c), *in)
	if err != nil {
		httpx.Fail(c, mapConstraintError(err, req.FormNo))
		return
	}
	if submissionID == "" {
		httpx.Created(c, res)
		return
	}
	httpx.OK(c, res)
}

// createBatch 批量提交多张表单（整批原子）。
// `forms` 的元素结构与单张提交完全一致（form_no / operation_code / form / enforce ...）。
func (h *Handler) createBatch(c *gin.Context) {
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if len(req.Forms) == 0 {
		httpx.Fail(c, httpx.BadRequest("forms 不能为空"))
		return
	}
	ins := make([]inspection.SaveInput, 0, len(req.Forms))
	for i, raw := range req.Forms {
		// 逐张解析：元素即一个 saveReq
		var el saveReq
		if err := json.Unmarshal(raw, &el); err != nil {
			httpx.Fail(c, httpx.BadRequest(fmt.Sprintf("第 %d 张表单格式不正确: %s", i+1, err.Error())))
			return
		}
		// 顶层未给的字段由批量外壳提供
		if el.Enforce == "" {
			el.Enforce = req.Enforce
		}
		if el.Mode == "" {
			el.Mode = req.Mode
		}
		if el.Reason == "" {
			el.Reason = req.Reason
		}
		in, err := h.buildInput(el, "", raw)
		if err != nil {
			httpx.Fail(c, httpx.BadRequest(fmt.Sprintf("第 %d 张表单: %s", i+1, err.Error())))
			return
		}
		in.Expected = nil // 批量语义：逐张新建，不用乐观锁
		ins = append(ins, *in)
	}
	results, err := h.svc.SaveBatch(c.Request.Context(), actorOf(c), ins)
	if err != nil {
		httpx.Fail(c, mapConstraintError(err, ""))
		return
	}
	httpx.OK(c, gin.H{"results": results, "count": len(results), "atomic": true})
}

func (h *Handler) buildInput(req saveReq, submissionID string, raw []byte) (*inspection.SaveInput, error) {
	rawForm := req.Form
	if len(rawForm) == 0 || isJSONNull(rawForm) {
		// 兼容：允许把栏目直接写在顶层（除控制字段外）
		rawForm = json.RawMessage(raw)
	}
	patch, unknown, err := inspection.ParseFormPatch(rawForm)
	if err != nil {
		return nil, httpx.BadRequest(err.Error())
	}
	if len(unknown) > 0 {
		return nil, httpx.BadRequest("存在无法识别的字段: "+strings.Join(unknown, ", "),
			map[string]any{"unknown_fields": unknown,
				"hint": "列清单见 GET /api/form-fields"})
	}

	in := &inspection.SaveInput{
		FormNo:       strings.TrimSpace(req.FormNo),
		SubmissionID: submissionID,
		Operation:    strings.TrimSpace(req.Operation),
		Patch:        patch,
		Enforce:      inspection.ParseEnforce(req.Enforce),
		Reason:       req.Reason,
		Expected:     req.Expected,
		Raw:          raw,
	}
	// lines 出现在补丁里即整笔替换
	in.LinesGiven = patch.Lines != nil
	if in.Operation == "" {
		if patch.Operation.IsSet && !patch.Operation.Cleared {
			in.Operation = strings.TrimSpace(patch.Operation.Value)
		} else if patch.OperationAlt.IsSet && !patch.OperationAlt.Cleared {
			in.Operation = strings.TrimSpace(patch.OperationAlt.Value)
		}
	}
	return in, nil
}

func (h *Handler) getForm(c *gin.Context) {
	sub, err := h.svc.Store().GetSubmission(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if sub == nil {
		// 兼容：允许用表单号查询
		sub, err = h.svc.Store().GetSubmissionByNo(c.Request.Context(), c.Param("id"))
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	if sub == nil {
		httpx.Fail(c, httpx.NotFound("表单提交不存在: "+c.Param("id")))
		return
	}
	httpx.OK(c, gin.H{
		"submission": sub,
		"template":   inspection.TemplateFor(sub.OperationCode),
		"layout":     inspection.LayoutFor(sub.OperationCode),
	})
}

func (h *Handler) voidForm(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	sub, err := h.svc.VoidForm(c.Request.Context(), actorOf(c), c.Param("id"), req.Reason)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, sub)
}

func (h *Handler) listHistory(c *gin.Context) {
	sub, err := h.svc.Store().GetSubmission(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if sub == nil {
		sub, err = h.svc.Store().GetSubmissionByNo(c.Request.Context(), c.Param("id"))
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	if sub == nil {
		httpx.Fail(c, httpx.NotFound("表单提交不存在"))
		return
	}
	items, err := h.svc.Store().ListHistory(c.Request.Context(), sub.ID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": len(items)})
}

// diffHistory 返回两个版本之间的字段级差异（?from=<history_id>&to=<history_id|current>）。
func (h *Handler) diffHistory(c *gin.Context) {
	id := c.Param("id")
	sub, err := h.svc.Store().GetSubmission(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if sub == nil {
		sub, err = h.svc.Store().GetSubmissionByNo(c.Request.Context(), id)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	if sub == nil {
		httpx.Fail(c, httpx.NotFound("表单提交不存在"))
		return
	}

	fromEntry, err := h.svc.Store().GetHistoryEntry(c.Request.Context(), c.Query("from"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if fromEntry == nil {
		httpx.Fail(c, httpx.NotFound("历史快照不存在: "+c.Query("from")))
		return
	}
	fromSub, fromLines, err := inspection.DecodeSnapshot(fromEntry)
	if err != nil {
		httpx.Fail(c, httpx.BadRequest(err.Error()))
		return
	}

	toQuery := c.Query("to")
	toSub := *sub
	toLines := sub.Lines
	if toQuery != "" && toQuery != "current" {
		toEntry, err := h.svc.Store().GetHistoryEntry(c.Request.Context(), toQuery)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if toEntry == nil {
			httpx.Fail(c, httpx.NotFound("历史快照不存在: "+toQuery))
			return
		}
		toSub, toLines, err = inspection.DecodeSnapshot(toEntry)
		if err != nil {
			httpx.Fail(c, httpx.BadRequest(err.Error()))
			return
		}
	}

	changes := inspection.DiffSubmissions(&fromSub, &toSub, fromLines, toLines)
	httpx.OK(c, gin.H{
		"from_save_count": fromEntry.SaveCount,
		"to_save_count":   toSub.SaveCount,
		"changes":         changes,
		"total":           len(changes),
	})
}

func (h *Handler) listAudit(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	list, err := h.svc.Store().ListAudit(c.Request.Context(), c.Query("entity"), c.Query("entity_id"), limit)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list, "total": len(list)})
}

// ------------------------------- 工具 -------------------------------

// mapConstraintError 把唯一约束冲突映射为 409（V-7），而不是 500。
func mapConstraintError(err error, formNo string) error {
	if err == nil {
		return nil
	}
	var he *httpx.Error
	if errors.As(err, &he) {
		return err // 已是业务错误，原样透传
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "unique") && !strings.Contains(msg, "constraint failed") &&
		!strings.Contains(msg, "duplicate key") {
		return err
	}
	switch {
	case strings.Contains(msg, "form_no"):
		return httpx.Conflict("表单号已存在: "+formNo+"（V-7：表单号唯一）")
	case strings.Contains(msg, "form_lines") || strings.Contains(msg, "seq"):
		return httpx.Conflict("明细行序号重复（V-7：(form_id, seq) 唯一）")
	default:
		return httpx.Conflict("唯一性冲突（V-7）", map[string]any{"detail": err.Error()})
	}
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
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
