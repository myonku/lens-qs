// Package httpx 统一 HTTP 响应格式与错误处理。
//
// 约定（对应 docs/design.md §6）：
//
//	成功：{ "code": "ok",        "message": "",   "data": {...} }
//	失败：{ "code": "not_found", "message": "...", "details": [...] }
//
// HTTP 状态码与错误码语义一致，便于调用方按状态码处理。
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// 错误码（同时作为响应的 code 字段）。
const (
	CodeOK           = "ok"
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeValidation   = "validation_failed"
	CodeInternal     = "internal_error"
)

// 业务错误（服务层返回，由中间件统一转成 HTTP 响应）。
var (
	ErrUnauthorized = errors.New("未登录或会话已失效")
	ErrForbidden    = errors.New("无权执行该操作")
	ErrNotFound     = errors.New("资源不存在")
)

// Error 是带错误码与 HTTP 状态的可返回错误。
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

// Unwrap 支持 errors.Is/As 对底层错误判断。
func (e *Error) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	}
	return nil
}

// NewError 构造一个业务错误。
func NewError(status int, code, message string, details any) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: details}
}

// BadRequest / Unauthorized / Forbidden / NotFound / Conflict / Validation 是便捷构造器。
func BadRequest(msg string, details ...any) *Error {
	return NewError(http.StatusBadRequest, CodeBadRequest, msg, firstOrNil(details))
}

func Unauthorized(msg string) *Error {
	if msg == "" {
		msg = "未登录或会话已失效"
	}
	return NewError(http.StatusUnauthorized, CodeUnauthorized, msg, nil)
}

func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "无权执行该操作"
	}
	return NewError(http.StatusForbidden, CodeForbidden, msg, nil)
}

func NotFound(msg string) *Error {
	if msg == "" {
		msg = "资源不存在"
	}
	return NewError(http.StatusNotFound, CodeNotFound, msg, nil)
}

func Conflict(msg string, details ...any) *Error {
	return NewError(http.StatusConflict, CodeConflict, msg, firstOrNil(details))
}

func Validation(msg string, details any) *Error {
	return NewError(http.StatusUnprocessableEntity, CodeValidation, msg, details)
}

func firstOrNil(v []any) any {
	if len(v) == 0 {
		return nil
	}
	return v[0]
}

// Issue 是一条校验提示。level 为 "warning" 时不影响保存；为 "error" 时表示阻断。
type Issue struct {
	Rule    string `json:"rule"`            // 规则编号，如 V-1
	Level   string `json:"level"`           // warning | error
	Field   string `json:"field,omitempty"` // 相关字段
	Message string `json:"message"`         // 可读说明
}

// Warnings 过滤出 warning 级别的问题。
func Warnings(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, i := range issues {
		if i.Level == "warning" {
			out = append(out, i)
		}
	}
	return out
}

// Errors 过滤出 error 级别的问题。
func Errors(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, i := range issues {
		if i.Level == "error" {
			out = append(out, i)
		}
	}
	return out
}

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": CodeOK, "message": "", "data": data})
}

// Created 返回创建成功响应。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"code": CodeOK, "message": "", "data": data})
}

// Page 是分页响应的通用结构。
type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// NewPage 构造分页响应；items 为 nil 时返回空数组而非 null。
func NewPage[T any](items []T, total, page, pageSize int) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Page: page, PageSize: pageSize}
}

// Fail 按错误类型写出失败响应。
func Fail(c *gin.Context, err error) {
	var e *Error
	if errors.As(err, &e) {
		body := gin.H{"code": e.Code, "message": e.Message}
		if e.Details != nil {
			body["details"] = e.Details
		}
		c.JSON(e.Status, body)
		return
	}
	// 未识别的错误：不把内部细节暴露给调用方
	_ = c.Error(err)
	c.JSON(http.StatusInternalServerError, gin.H{
		"code":    CodeInternal,
		"message": "服务器内部错误",
	})
}

// BindJSON 解析请求体，失败时返回统一的 400 错误。
func BindJSON(c *gin.Context, dst any) error {
	contentType, _, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if e != nil || contentType != "application/json" {
		return BadRequest("Content-Type 必须为 application/json")
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return BadRequest("请求体读取失败或超过 4 MiB")
	}
	if err = DecodeStrict(raw, dst); err != nil {
		return err
	}
	if err = binding.Validator.ValidateStruct(dst); err != nil {
		return BadRequest("请求体格式不正确: " + err.Error())
	}
	return nil
}

// DecodeJSON 在非 gin 场景下解析 JSON（如测试）。
func DecodeJSON(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }
