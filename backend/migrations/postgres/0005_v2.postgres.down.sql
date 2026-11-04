ALTER TABLE inspection_submissions DROP CONSTRAINT fk_v2_current_revision;
ALTER TABLE inspection_submissions DROP CONSTRAINT fk_v2_draft_revision;
-- Remove circular pointer references before dropping version tables.
UPDATE inspection_submissions SET current_revision_id = NULL, draft_revision_id = NULL;
DROP TABLE legacy_migration_candidates;
DROP TABLE v2_write_lock;
DROP TABLE v2_idempotency_requests;
DROP TABLE quantity_changes;
DROP TABLE revision_lines;
DROP TABLE submission_revisions;
DROP TABLE submission_slots;
DROP TABLE inspection_submissions;
DROP TABLE operation_groups;
DROP TABLE product_lots;
