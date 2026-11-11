CREATE TABLE user_operations (
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  operation_id TEXT NOT NULL REFERENCES dict_items(id),
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, operation_id)
);
CREATE INDEX ix_user_operations_operation ON user_operations(operation_id);
INSERT INTO user_operations(user_id, operation_id)
SELECT id, operation_id FROM users WHERE operation_id IS NOT NULL;
DELETE FROM v2_idempotency_requests WHERE actor_id IN (SELECT id FROM users WHERE is_admin=0);
