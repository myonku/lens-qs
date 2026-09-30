package idempotencyhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/idempotency"
	"github.com/myonku/lens-qs-api/internal/middleware"
	"github.com/myonku/lens-qs-api/internal/store"
)

// newTestEngine 组装一个最小引擎：认证身份由中间件直接注入（不跑真实登录）。
func newTestEngine(t *testing.T, handler func(c *gin.Context)) (*gin.Engine, *idempotency.Store, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := store.NewTestDB(t)
	st := idempotency.NewStore(db, "sqlite", time.Hour)

	sideEffectCalls := 0
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		// 模拟已登录用户
		c.Set(middleware.CtxUserID, "user-1")
		c.Next()
	})
	api.Use(Middleware(st))
	api.POST("/forms/:formNo/records", func(c *gin.Context) {
		sideEffectCalls++
		handler(c)
	})
	return r, st, &sideEffectCalls
}

func do(t *testing.T, r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/forms/M-1/records", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 相同幂等键重复提交：第二次不回放副作用，直接回放首次响应。
func TestMiddlewareReplaysFirstResponse(t *testing.T) {
	r, _, calls := newTestEngine(t, func(c *gin.Context) {
		var in map[string]any
		_ = json.NewDecoder(c.Request.Body).Decode(&in)
		c.JSON(http.StatusOK, gin.H{"code": "ok", "data": gin.H{"call": in["note"]}})
	})

	body := `{"note":"first","client_request_id":"k-1"}`

	w1 := do(t, r, "", body) // 键来自请求体
	if w1.Code != http.StatusOK {
		t.Fatalf("首次请求应 200，得到 %d", w1.Code)
	}
	if *calls != 1 {
		t.Fatalf("首次请求应执行 1 次副作用，得到 %d", *calls)
	}
	if w1.Header().Get(ReplayHeader) == "true" {
		t.Error("首次请求不应带回放标记")
	}

	w2 := do(t, r, "", body)
	if w2.Code != http.StatusOK {
		t.Fatalf("重复请求应 200（回放），得到 %d", w2.Code)
	}
	if *calls != 1 {
		t.Fatalf("重复请求不应再执行副作用，副作用次数=%d", *calls)
	}
	if w2.Header().Get(ReplayHeader) != "true" {
		t.Error("回放响应应带 X-Idempotent-Replay: true")
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatalf("回放内容应与首次一致:\n首次 %s\n回放 %s", w1.Body.String(), w2.Body.String())
	}
}

// 请求头优先于请求体。
func TestMiddlewareHeaderKeyTakesPrecedence(t *testing.T) {
	r, _, calls := newTestEngine(t, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "ok"})
	})

	body := `{"note":"x","client_request_id":"body-key"}`
	if w := do(t, r, "header-key", body); w.Code != http.StatusOK {
		t.Fatalf("首次请求失败: %d", w.Code)
	}
	// 换一个请求体键但沿用同一个请求头键 → 应命中（请求体指纹不同 → 409）
	w := do(t, r, "header-key", `{"note":"y","client_request_id":"other"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("同键不同体应返回 409，得到 %d（body=%s）", w.Code, w.Body.String())
	}
	if *calls != 1 {
		t.Fatalf("冲突请求不应执行副作用，次数=%d", *calls)
	}
}

// 没有幂等键时中间件完全透明。
func TestMiddlewareNoKeyPassesThrough(t *testing.T) {
	r, _, calls := newTestEngine(t, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "ok"})
	})
	for i := 0; i < 3; i++ {
		if w := do(t, r, "", `{"note":"no-key"}`); w.Code != http.StatusOK {
			t.Fatalf("无幂等键应正常处理，得到 %d", w.Code)
		}
	}
	if *calls != 3 {
		t.Fatalf("无幂等键时每次都应变执行，次数=%d", *calls)
	}
}

// 失败响应不缓存：同一个键可以重试直到成功。
func TestMiddlewareDoesNotCacheFailures(t *testing.T) {
	attempt := 0
	r, _, calls := newTestEngine(t, func(c *gin.Context) {
		attempt++
		if attempt == 1 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"code": "validation_failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": "ok"})
	})

	body := `{"client_request_id":"retry-1"}`
	if w := do(t, r, "", body); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("首次应 422，得到 %d", w.Code)
	}
	w := do(t, r, "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("失败后同键重试应再次执行并成功，得到 %d", w.Code)
	}
	if *calls != 2 {
		t.Fatalf("失败不缓存，第二次应再次执行，次数=%d", *calls)
	}
}

// 请求体必须能被后续 handler 正常读取（中间件读过后要还原）。
func TestMiddlewareRestoresRequestBody(t *testing.T) {
	var seen string
	r, _, _ := newTestEngine(t, func(c *gin.Context) {
		var in struct {
			Record struct {
				ProcessCode string `json:"process_code"`
			} `json:"record"`
		}
		if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		seen = in.Record.ProcessCode
		c.JSON(http.StatusOK, gin.H{"code": "ok"})
	})

	body := `{"record":{"process_code":"一次加硬"},"client_request_id":"k-restore"}`
	w := do(t, r, "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("请求失败: %d %s", w.Code, w.Body.String())
	}
	if seen != "一次加硬" {
		t.Fatalf("handler 未读到请求体，得到 %q", seen)
	}
}

// 请求体过大（超过上限）时不应把请求体吃掉导致后文解析失败。
func TestMiddlewareLargeBodyIsBounded(t *testing.T) {
	r, _, _ := newTestEngine(t, func(c *gin.Context) {
		// 即使中间件限制了读取，handler 仍应看到 gin 的错误处理路径（这里是 400）
		var in map[string]any
		if err := json.NewDecoder(c.Request.Body).Decode(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": "ok"})
	})

	big := make([]byte, 5<<20) // 5MB > 4MB 上限
	for i := range big {
		big[i] = 'a'
	}
	body := `{"client_request_id":"k-big","note":"` + string(big) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/forms/M-1/records", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// 只要求不 panic、不 500；具体状态码由业务决定
	if w.Code >= http.StatusInternalServerError {
		t.Fatalf("超大请求体不应导致 500，得到 %d", w.Code)
	}
}
