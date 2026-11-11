ALTER TABLE users ADD COLUMN operation_id TEXT REFERENCES dict_items(id);
DELETE FROM v2_idempotency_requests WHERE actor_id IN (SELECT id FROM users WHERE is_admin=0);
