// Package inspectionhttp 提供表单 / 笔 / 明细 / 历史的 HTTP 接口。
package inspectionhttp

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/inspection"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// Handler 聚合表单与工序记录接口。
type Handler struct {
	svc *inspection.Service
}

// New 构造 Handler。
func New(svc *inspection.Service) *Handler { return &Handler{svc: svc} }

// Register 注册全部接口（均需登录）。
func (h *Handler) Register(r *gin.RouterGroup) {
	r.GET("/forms", h.listForms)
	r.POST("/forms", h.saveForm)
	r.GET("/forms/:formNo", h.getForm)
	r.PATCH("/forms/:formNo", h.saveFormPatch)

	r.POST("/forms/:formNo/records", h.saveRecord)
	r.GET("/records/:id", h.getRecord)
	r.PATCH("/records/:id", h.saveRecordPatch)
	r.POST("/records/:id/void", h.voidRecord)
	r.GET("/records/:id/history", h.listHistory)
	r.GET("/records/:id/history/diff", h.diffHistory)
	r.GET("/forms/:formNo/sample/pending", h.pendingSampleByForm)
	r.GET("/audit-logs", h.listAudit)
}

// ------------------------------- 请求/响应结构 -------------------------------

// saveReq 是保存请求。form / record 是"字段补丁"：只提交本次要改的字段。
type saveReq struct {
	FormNo     string          `json:"form_no"`
	SequenceNo *int            `json:"sequence_no"`
	Mode       string          `json:"mode"`    // merge（默认）| replace
	Enforce    string          `json:"enforce"` // warn（默认）| strict
	Reason     string          `json:"reason"`
	Expected   *int            `json:"expected_save_count"`
	Form       json.RawMessage `json:"form"`
	Record     json.RawMessage `json:"record"`
	Lines      json.RawMessage `json:"lines"`

	// 批量：一次提交多笔（用于"次日一次性完整录入"）
	Records []json.RawMessage `json:"records"`
}

type recordPatch struct {
	Record json.RawMessage `json:"record"`
	Lines  json.RawMessage `json:"lines"`
}

func actorOf(c *gin.Context) inspection.Actor {
	return inspection.Actor{
		UserID:      middleware.CurrentUserID(c),
		DisplayName: middleware.CurrentUserName(c),
		IP:          c.ClientIP(),
		UserAgent:   c.GetHeader("User-Agent"),
	}
}

// ------------------------------- 表单 -------------------------------

func (h *Handler) saveForm(c *gin.Context) {
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	if strings.TrimSpace(req.FormNo) == "" {
		httpx.Fail(c, httpx.BadRequest("表单号（form_no）不能为空"))
		return
	}
	patch, err := inspection.ParsePatch(req.Form)
	if err != nil {
		httpx.Fail(c, httpx.BadRequest(err.Error()))
		return
	}
	// 允许在 form 补丁中直接给出 form_no，两者都可
	if _, ok := patch["form_no"]; !ok {
		patch["form_no"], _ = json.Marshal(req.FormNo)
	}
	res, err := h.svc.SaveForm(c.Request.Context(), actorOf(c), inspection.SaveInput{
		FormNo:    req.FormNo,
		FormPatch: patch,
		Enforce:   inspection.ParseEnforce(req.Enforce),
		Reason:    req.Reason,
		Raw:       rawOf(req),
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, res)
}

func (h *Handler) saveFormPatch(c *gin.Context) {
	formNo := c.Param("formNo")
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	patch, err := inspection.ParsePatch(req.Form)
	if err != nil {
		httpx.Fail(c, httpx.BadRequest(err.Error()))
		return
	}
	res, err := h.svc.SaveForm(c.Request.Context(), actorOf(c), inspection.SaveInput{
		FormNo:    formNo,
		FormPatch: patch,
		Enforce:   inspection.ParseEnforce(req.Enforce),
		Reason:    req.Reason,
		Raw:       rawOf(req),
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) getForm(c *gin.Context) {
	st := h.svc.Store()
	form, err := st.GetFormByNo(c.Request.Context(), c.Param("formNo"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if form == nil {
		httpx.Fail(c, httpx.NotFound("表单不存在: "+c.Param("formNo")))
		return
	}
	records, err := st.ListRecords(c.Request.Context(), form.ID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	for i := range records {
		lines, err := st.ListLines(c.Request.Context(), records[i].ID)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		records[i].Lines = lines
		if records[i].Lines == nil {
			records[i].Lines = []inspection.RecordLine{}
		}
	}
	form.Records = records
	if form.Records == nil {
		form.Records = []inspection.FormRecord{}
	}
	httpx.OK(c, form)
}

func (h *Handler) listForms(c *gin.Context) {
	page, size := pageParams(c)
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		state = "ACTIVE"
	}
	forms, total, err := h.svc.Store().ListForms(c.Request.Context(), inspection.FormFilter{
		Keyword:     strings.TrimSpace(c.Query("keyword")),
		State:       state,
		NeedsReview: c.Query("needs_review") == "true",
		From:        c.Query("from"),
		To:          c.Query("to"),
		// 样品维度与单位筛选（与 FormFilter 字段对齐，避免"传了没生效"）
		Project: c.Query("sample_project"),
		Model:   c.Query("sample_model"),
		Spec:    c.Query("sample_spec"),
		Size:    c.Query("sample_size"),
		Side:    c.Query("sample_side"),
		Unit:    c.Query("unit"),
		Offset:  (page - 1) * size,
		Limit:   size,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, httpx.NewPage(forms, total, page, size))
}

// ------------------------------- 工序记录 -------------------------------

func (h *Handler) saveRecord(c *gin.Context) {
	formNo := c.Param("formNo")
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	req.FormNo = formNo

	// 批量模式：一次提交多笔
	if len(req.Records) > 0 {
		h.saveMany(c, req)
		return
	}

	res, err := h.doSave(c, req, "")
	if err != nil {
		httpx.Fail(c, mapConstraintError(err, formNo))
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) saveRecordPatch(c *gin.Context) {
	recordID := c.Param("id")
	var req saveReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	// 通过记录 ID 定位表单
	rec, err := h.svc.Store().GetRecord(c.Request.Context(), recordID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if rec == nil {
		httpx.Fail(c, httpx.NotFound("工序记录不存在"))
		return
	}
	form, err := h.svc.Store().GetFormByID(c.Request.Context(), rec.FormID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if form == nil {
		httpx.Fail(c, httpx.NotFound("表单不存在"))
		return
	}
	req.FormNo = form.FormNo

	res, err := h.doSave(c, req, recordID)
	if err != nil {
		httpx.Fail(c, mapConstraintError(err, req.FormNo))
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) saveMany(c *gin.Context, req saveReq) {
	results := make([]*inspection.SaveResult, 0, len(req.Records))
	for _, raw := range req.Records {
		var rp recordPatch
		if err := json.Unmarshal(raw, &rp); err != nil {
			// 兼容：直接把该元素当作 record 补丁
			rp = recordPatch{Record: raw}
		}
		sub := saveReq{
			FormNo:  req.FormNo,
			Mode:    req.Mode,
			Enforce: req.Enforce,
			Reason:  req.Reason,
			Form:    req.Form,
			Record:  rp.Record,
			Lines:   rp.Lines,
		}
		res, err := h.doSave(c, sub, "")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		results = append(results, res)
	}
	httpx.OK(c, gin.H{"results": results, "count": len(results)})
}

func (h *Handler) doSave(c *gin.Context, req saveReq, recordID string) (*inspection.SaveResult, error) {
	formPatch, err := inspection.ParsePatch(req.Form)
	if err != nil {
		return nil, httpx.BadRequest("form 补丁格式不正确: " + err.Error())
	}
	recPatch, err := inspection.ParsePatch(req.Record)
	if err != nil {
		return nil, httpx.BadRequest("record 补丁格式不正确: " + err.Error())
	}

	in := inspection.SaveInput{
		FormNo:      req.FormNo,
		RecordID:    recordID,
		SequenceNo:  req.SequenceNo,
		FormPatch:   formPatch,
		RecordPatch: recPatch,
		Enforce:     inspection.ParseEnforce(req.Enforce),
		SaveMode:    strings.ToLower(strings.TrimSpace(req.Mode)),
		Reason:      req.Reason,
		Expected:    req.Expected,
		Raw:         rawOf(req),
	}
	if in.SaveMode == "" {
		in.SaveMode = "merge"
	}

	if len(req.Lines) > 0 && !isJSONNull(req.Lines) {
		lines, err := decodeLines(req.Lines)
		if err != nil {
			return nil, httpx.BadRequest("lines 格式不正确: " + err.Error())
		}
		in.Lines = lines
		in.LinesGiven = true
	}

	return h.svc.SaveRecord(c.Request.Context(), actorOf(c), in)
}

func (h *Handler) getRecord(c *gin.Context) {
	st := h.svc.Store()
	rec, err := st.GetRecord(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if rec == nil {
		httpx.Fail(c, httpx.NotFound("工序记录不存在"))
		return
	}
	lines, err := st.ListLines(c.Request.Context(), rec.ID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	rec.Lines = lines
	if rec.Lines == nil {
		rec.Lines = []inspection.RecordLine{}
	}
	form, err := st.GetFormByID(c.Request.Context(), rec.FormID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"record": rec, "form": form})
}

func (h *Handler) voidRecord(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	rec, err := h.svc.VoidRecord(c.Request.Context(), actorOf(c), c.Param("id"), req.Reason)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, rec)
}

func (h *Handler) listHistory(c *gin.Context) {
	list, err := h.svc.Store().ListHistory(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list, "total": len(list)})
}

// diffHistory 返回两个版本之间的字段级差异。
// 用法：?from=<history_id>&to=<history_id|current>
func (h *Handler) diffHistory(c *gin.Context) {
	recordID := c.Param("id")
	fromID := c.Query("from")
	toID := c.Query("to")

	st := h.svc.Store()
	fromEntry, err := st.GetHistoryEntry(c.Request.Context(), fromID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if fromEntry == nil {
		httpx.Fail(c, httpx.NotFound("历史快照不存在: "+fromID))
		return
	}

	fromRecord, fromLines, err := decodeSnapshot(fromEntry)
	if err != nil {
		httpx.Fail(c, err)
		return
	}

	var toRecord *inspection.FormRecord
	var toLines []inspection.RecordLine
	if toID == "" || toID == "current" {
		toRecord, err = st.GetRecord(c.Request.Context(), recordID)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if toRecord == nil {
			httpx.Fail(c, httpx.NotFound("工序记录不存在"))
			return
		}
		toLines, err = st.ListLines(c.Request.Context(), toRecord.ID)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	} else {
		toEntry, err := st.GetHistoryEntry(c.Request.Context(), toID)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if toEntry == nil {
			httpx.Fail(c, httpx.NotFound("历史快照不存在: "+toID))
			return
		}
		toRecord, toLines, err = decodeSnapshot(toEntry)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
	}

	diff := inspection.DiffRecords(fromRecord, fromLines, toRecord, toLines)
	httpx.OK(c, gin.H{
		"from_save_count": fromEntry.SaveCount,
		"to_save_count":   toRecord.SaveCount,
		"changes":         diff,
		"total":           len(diff),
	})
}

func (h *Handler) pendingSampleByForm(c *gin.Context) {
	form, err := h.svc.Store().GetFormByNo(c.Request.Context(), c.Param("formNo"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if form == nil {
		httpx.Fail(c, httpx.NotFound("表单不存在"))
		return
	}
	issues := inspection.ValidateForm(*form)
	httpx.OK(c, gin.H{"form_no": form.FormNo, "needs_review": form.NeedsReview, "issues": issues})
}

func (h *Handler) listAudit(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := parseInt(v); err == nil {
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

// mapConstraintError 把数据库唯一约束冲突映射为 409（V-7），
// 而不是让它以 500 内部错误返回——前端需要能区分"重号"与"服务异常"。
func mapConstraintError(err error, formNo string) error {
	if err == nil {
		return nil
	}
	var he *httpx.Error
	if errors.As(err, &he) {
		return err // 已经是业务错误（参数/校验/冲突等），原样返回
	}
	msg := strings.ToLower(err.Error())
	hit := strings.Contains(msg, "unique") ||
		strings.Contains(msg, "constraint failed") ||
		strings.Contains(msg, "duplicate key")
	if !hit {
		return err
	}
	switch {
	case strings.Contains(msg, "form_no"):
		return httpx.Conflict("表单号已存在: "+formNo+"（V-7：表单号唯一）")
	case strings.Contains(msg, "sequence_no"):
		return httpx.Conflict("该表单下已存在相同序号的工序记录（V-7：(form_no, sequence_no) 唯一）")
	case strings.Contains(msg, "record_lines") || strings.Contains(msg, "seq"):
		return httpx.Conflict("明细行序号重复（V-7：(record_id, seq) 唯一）")
	default:
		return httpx.Conflict("唯一性冲突（V-7）", map[string]any{"detail": err.Error()})
	}
}

// decodeLines 解析明细数组；字段缺省时按 0 处理（最大灵活性：缺字段不算错）。
func decodeLines(raw json.RawMessage) ([]inspection.RecordLine, error) {
	var list []inspection.RecordLine
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Seq <= 0 {
			list[i].Seq = i + 1
		}
	}
	return list, nil
}

func decodeSnapshot(e *inspection.HistoryEntry) (*inspection.FormRecord, []inspection.RecordLine, error) {
	var rec inspection.FormRecord
	if e.RecordSnapshot != nil && *e.RecordSnapshot != "" {
		if err := json.Unmarshal([]byte(*e.RecordSnapshot), &rec); err != nil {
			return nil, nil, httpx.BadRequest("历史快照解析失败: " + err.Error())
		}
	}
	lines := []inspection.RecordLine{}
	if e.LinesSnapshot != nil && *e.LinesSnapshot != "" {
		if err := json.Unmarshal([]byte(*e.LinesSnapshot), &lines); err != nil {
			return nil, nil, httpx.BadRequest("历史明细解析失败: " + err.Error())
		}
	}
	return &rec, lines, nil
}

func rawOf(req saveReq) json.RawMessage {
	// 操作日志里的 after 快照：记录本次请求内容（不含 lines 时也没关系）
	b, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	return b
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func pageParams(c *gin.Context) (page, size int) {
	page, size = 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	return page, size
}

func parseInt(s string) (int, error) {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0, httpx.BadRequest("不是合法数字")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// 保证 inspection 包被引用（DiffRecords 在 diff.go 中实现）。
var _ = inspection.DiffRecords
