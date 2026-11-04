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
INSERT INTO v2_release_state(id,history_started_at) SELECT 1,strftime('%Y-%m-%dT%H:%M:%SZ','now');
CREATE INDEX ix_v2_audit_replay ON audit_logs(entity,created_at,id);
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','product_lot',id,json_object('id',id,'shared_form_no',shared_form_no,'sample_identity',sample_identity,'sample_identity_key',sample_identity_key,'sample_raw',sample_raw,'sample_project',sample_project,'sample_model',sample_model,'sample_spec',sample_spec,'sample_size',sample_size,'sample_side',sample_side,'version',version,'created_at',created_at,'updated_at',updated_at),(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM product_lots t;
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','operation_group',id,json_object('id',id,'product_lot_id',product_lot_id,'operation_id',operation_id,'operation_code',operation_code,'inspection_kind',inspection_kind,'template_version',template_version,'reporting_state',reporting_state,'batch_mode',batch_mode,'version',version),(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM operation_groups t;
INSERT INTO audit_logs(action,entity,entity_id,"after",created_at) SELECT 'v2_release_baseline','inspection_submission',id,json_object('id',id,'operation_group_id',operation_group_id,'batch_no',batch_no,'inspection_kind',inspection_kind,'state',state,'version',version,'current_revision_id',current_revision_id,'draft_revision_id',draft_revision_id,'created_by',created_by,'created_at',created_at,'cancelled_by',cancelled_by,'cancelled_at',cancelled_at,'cancel_reason',cancel_reason),(SELECT history_started_at FROM v2_release_state WHERE id = 1) FROM inspection_submissions t;
