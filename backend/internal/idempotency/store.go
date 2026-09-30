// Package idempotency 实现"幂等请求"：同一系统用户在 TTL 内用相同幂等键重复提交时，
// 直接返回首次结果，避免弱网重试造成重复写入（重复的历史快照与操作日志）。
//
// 幂等键优先级：请求头 X-Idempotency-Key > 请求体 client_request_id（或 clientRequestId）。
// 作用域：按 (user_id, idempotency_key) 唯一，不同用户互不影响。
//
// 用法（见 middleware.Idempotency）：
//
//	h.Begin(ctx, key, method, path, body)  -> 命中则返回已存响应（应直接重放）
//	... 处理请求 ...
//	h.Complete(ctx, key, method, path, body, status, headers, respBody)  -> 记录首次响应
package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// 默认 TTL：24 小时内同键重复提交视为重试。
const DefaultTTL = 24 * time.Hour

// Record 是已记录的首次响应。
type Record struct {
	ID              string  `db:"id" json:"id"`
	UserID          string  `db:"user_id" json:"user_id"`
	IdempotencyKey  string  `db:"idempotency_key" json:"idempotency_key"`
	RequestMethod   string  `db:"request_method" json:"request_method"`
	RequestPath     string  `db:"request_path" json:"request_path"`
	RequestHash     string  `db:"request_hash" json:"request_hash"`
	StatusCode      int     `db:"status_code" json:"status_code"`
	ResponseHeaders *string `db:"response_headers" json:"response_headers,omitempty"`
	ResponseBody    *string `db:"response_body" json:"response_body,omitempty"`
	CreatedAt       string  `db:"created_at" json:"created_at"`
	ExpiresAt       string  `db:"expires_at" json:"expires_at"`
}

// Store 负责幂等记录的持久化。
type Store struct {
	db     *sqlx.DB
	driver string
	ttl    time.Duration
}

// NewStore 构造 Store。ttl <= 0 时使用 DefaultTTL。
func NewStore(db *sqlx.DB, driver string, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{db: db, driver: driver, ttl: ttl}
}

// Replay 表示一次命中：调用方应直接回放该响应，不再执行写操作。
type Replay struct {
	StatusCode int
	Body       []byte
	// SameRequest 为 false 表示：同键但请求体不同（客户端误用），调用方应返回 409。
	SameRequest bool
	Age         time.Duration
}

const columns = `id, user_id, idempotency_key, request_method, request_path, request_hash,
	status_code, response_headers, response_body, created_at, expires_at`

// Begin 检查幂等键是否已存在。
//   - 返回 (nil, nil)：首次请求，调用方继续正常处理；
//   - 返回 (replay, nil)：命中，调用方直接回放（SameRequest=false 时应返回 409）；
//   - 返回 (nil, err)：数据库错误（调用方可按普通错误处理，不阻断业务）。
func (s *Store) Begin(ctx context.Context, userID, key, method, path string, body []byte) (*Replay, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	// 先清理过期记录，再查（避免过期记录被当成命中）
	if _, err := s.cleanup(ctx); err != nil {
		// 清理失败不阻断：下面的查询本身也会用 expires_at 过滤
		_ = err
	}

	// 只认"未过期"的记录（双重保险：即使清理失败或被并发插入，也不会误回放）
	var rec Record
	err := s.db.GetContext(ctx, &rec, s.rebind(`SELECT `+columns+`
		FROM idempotency_requests
		WHERE user_id = ? AND idempotency_key = ? AND expires_at > ?`),
		userID, key, store.FormatTime(time.Now()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询幂等记录失败: %w", err)
	}

	age := time.Since(store.ParseTime(rec.CreatedAt))
	replay := &Replay{
		StatusCode:  rec.StatusCode,
		SameRequest: rec.RequestHash == HashBody(method, path, body),
		Age:         age,
	}
	if rec.ResponseBody != nil {
		replay.Body = []byte(*rec.ResponseBody)
	}
	return replay, nil
}

// Complete 记录首次响应。重复键（并发抢先）时忽略错误，由先写入者胜出。
func (s *Store) Complete(ctx context.Context, userID, key, method, path string, body []byte,
	status int, headers string, respBody []byte) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(userID) == "" {
		return nil
	}
	now := time.Now()
	rec := &Record{
		ID:             id.New(),
		UserID:         userID,
		IdempotencyKey: key,
		RequestMethod:  method,
		RequestPath:    path,
		RequestHash:    HashBody(method, path, body),
		StatusCode:     status,
		CreatedAt:      store.FormatTime(now),
		ExpiresAt:      store.FormatTime(now.Add(s.ttl)),
	}
	if headers != "" {
		rec.ResponseHeaders = &headers
	}
	if len(respBody) > 0 {
		bodyStr := string(respBody)
		rec.ResponseBody = &bodyStr
	}
	_, err := s.db.NamedExecContext(ctx, `INSERT INTO idempotency_requests (`+columns+`) VALUES (
		:id, :user_id, :idempotency_key, :request_method, :request_path, :request_hash,
		:status_code, :response_headers, :response_body, :created_at, :expires_at)`, rec)
	if err != nil {
		// 并发下另一个请求已写入：不是错误，先写入者胜出
		if strings.Contains(strings.ToLower(err.Error()), "unique") ||
			strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			return nil
		}
		return fmt.Errorf("写入幂等记录失败: %w", err)
	}
	return nil
}

// cleanup 删除过期记录。返回删除条数。
func (s *Store) cleanup(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM idempotency_requests WHERE expires_at < ?`),
		store.FormatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Cleanup 供运维/定时任务调用。
func (s *Store) Cleanup(ctx context.Context) (int64, error) { return s.cleanup(ctx) }

// TTL 返回当前 TTL。
func (s *Store) TTL() time.Duration { return s.ttl }

func (s *Store) rebind(q string) string { return s.db.Rebind(q) }

// HashBody 计算"方法 + 路径 + 请求体"的指纹，用于识别"同键不同体"的误用。
func HashBody(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
