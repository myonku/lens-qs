-- Formal v2 cutover. Legacy inspection test data is intentionally discarded.
ALTER TABLE users ADD COLUMN account_type TEXT NOT NULL DEFAULT 'NORMAL' CHECK(account_type IN ('NORMAL','SAMPLING'));
ALTER TABLE submission_revisions ADD COLUMN saved_reason TEXT;
UPDATE submission_revisions SET saved_reason = reason;
DROP TABLE legacy_migration_candidates;
DROP TABLE form_history;
DROP TABLE form_lines;
DROP TABLE forms;
DROP TABLE idempotency_requests;
DELETE FROM audit_logs WHERE entity NOT IN ('product_lot','operation_group','inspection_submission');
CREATE TABLE v2_release_state (id INTEGER PRIMARY KEY, history_started_at TEXT NOT NULL);
INSERT INTO v2_release_state(id,history_started_at) SELECT 1,to_char(CURRENT_TIMESTAMP AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"');
CREATE INDEX ix_v2_audit_replay ON audit_logs(entity,created_at,id);
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','product_lot',id,row_to_json(t)::text,(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM product_lots t;
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','operation_group',id,row_to_json(t)::text,(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM operation_groups t;
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','inspection_submission',id,row_to_json(t)::text,(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM inspection_submissions t;
