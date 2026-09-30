// Package idempotencyhttp 提供幂等中间件：让弱网重试"安全地重复提交"。
//
// 行为：
//  1. 仅对写方法（POST / PATCH / PUT / DELETE）生效；
//  2. 幂等键取 请求头 X-Idempotency-Key，其次取请求体 client_request_id / clientRequestId；
//  3. 命中且请求体指纹一致 → 直接回放首次响应（并加响应头 X-Idempotent-Replay: true）；
//  4. 命中但请求体不同 → 409 conflict（避免"同键不同内容"造成的误用）；
//  5. 未命中 → 正常处理，处理完成后记录响应（状态码 + 响应体）。
//
// 开关：环境变量 IDEMPOTENCY_ENABLED（默认 true）、IDEMPOTENCY_TTL（默认 24h）。
package idempotencyhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/idempotency"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 幂等键的请求头候选（按顺序取第一个非空）。
// 注意：不把 X-Request-Id 当幂等键——本项目会为每个请求自动生成该头，用作幂等键会导致永不命中。
var headerKeys = []string{"X-Idempotency-Key", "Idempotency-Key"}

// 请求体内的幂等键字段候选。
var bodyKeys = []string{"client_request_id", "clientRequestId", "idempotency_key", "idempotencyKey"}

// 回放标记响应头。
const ReplayHeader = "X-Idempotent-Replay"

// Middleware 构造幂等中间件。store 为 nil 时直接放行（功能关闭）。
func Middleware(store *idempotency.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil || !isWriteMethod(c.Request.Method) {
			c.Next()
			return
		}
		userID := middleware.CurrentUserID(c)
		if userID == "" {
			c.Next()
			return
		}

		body := readBody(c)
		key := extractKey(c, body)
		if key == "" {
			c.Next()
			return
		}

		replay, err := store.Begin(c.Request.Context(), userID, key, c.Request.Method, c.FullPath(), body)
		if err != nil {
			// 幂等表故障不应阻断业务：记录错误后继续正常处理
			_ = c.Error(err)
			c.Next()
			return
		}
		if replay != nil {
			if !replay.SameRequest {
				httpx.Fail(c, httpx.Conflict(
					"幂等键已被使用且请求内容不同（idempotency key reused with different payload）",
					map[string]any{"idempotency_key": key}))
				c.Abort()
				return
			}
			c.Header(ReplayHeader, "true")
			writeReplay(c, replay)
			c.Abort()
			return
		}

		// 首次请求：缓冲响应以便记录
		buf := &bodyWriter{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = buf
		c.Next()

		status := buf.Status()
		if status >= http.StatusBadRequest {
			// 失败响应不缓存：让客户端可以用同一个键重试
			return
		}
		_ = store.Complete(c.Request.Context(), userID, key, c.Request.Method, c.FullPath(),
			body, status, "", buf.buf.Bytes())
	}
}

type bodyWriter struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) WriteString(s string) (int, error) {
	w.buf.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// Status 返回已写入的状态码（未写入时为 200，与 gin 默认一致）。
func (w *bodyWriter) Status() int {
	if s := w.ResponseWriter.Status(); s != 0 {
		return s
	}
	return http.StatusOK
}

func isWriteMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// readBody 读取请求体并原样写回，保证后续 handler 仍能解析。
func readBody(c *gin.Context) []byte {
	if c.Request.Body == nil {
		return nil
	}
	raw, err := readAllLimited(c)
	if err != nil {
		return nil
	}
	return raw
}

func readAllLimited(c *gin.Context) ([]byte, error) {
	const maxBody = 4 << 20 // 4MB，与业务请求量级匹配
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(http.MaxBytesReader(c.Writer, c.Request.Body, maxBody))
	if err != nil {
		return nil, err
	}
	raw := buf.Bytes()
	// 还原请求体，供后续 handler 正常读取
	c.Request.Body = &readCloser{Reader: bytes.NewReader(raw)}
	return raw, nil
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

// extractKey 依次从请求头、请求体提取幂等键。
func extractKey(c *gin.Context, body []byte) string {
	for _, h := range headerKeys {
		if v := strings.TrimSpace(c.GetHeader(h)); v != "" {
			return v
		}
	}
	if len(body) == 0 {
		return ""
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	for _, k := range bodyKeys {
		raw, ok := probe[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// writeReplay 回放首次响应。
func writeReplay(c *gin.Context, r *idempotency.Replay) {
	body := r.Body
	if len(body) == 0 {
		body = []byte(`{"code":"ok","message":"","data":null}`)
	}
	c.Data(r.StatusCode, "application/json; charset=utf-8", body)
}
