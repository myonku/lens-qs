-- 0002_idempotency.postgres.up.sql
-- 幂等请求表：同一系统用户在 TTL 内用相同幂等键重复提交时，直接返回首次结果，
-- 避免弱网重试造成重复写入（重复的历史快照与操作日志）。
--
-- 幂等键优先级：请求头 X-Idempotency-Key > 请求体 client_request_id（或 clientRequestId）。

CREATE TABLE idempotency_requests (
  id                text PRIMARY KEY,
  user_id           text NOT NULL,          -- 幂等键的作用域：按系统用户隔离
  idempotency_key   text NOT NULL,          -- 幂等键（客户端生成，建议 UUID）
  request_method    text NOT NULL,
  request_path      text NOT NULL,
  request_hash      text NOT NULL,          -- 请求体指纹（SHA-256），同键不同体视为误用
  status_code       integer NOT NULL,       -- 首次响应的 HTTP 状态
  response_headers  text,
  response_body     text,                   -- 首次响应的响应体（原样重放）
  created_at        text NOT NULL,
  expires_at        text NOT NULL
);
CREATE UNIQUE INDEX ux_idempotency_user_key ON idempotency_requests (user_id, idempotency_key);
CREATE INDEX ix_idempotency_expires ON idempotency_requests (expires_at);
