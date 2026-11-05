-- Controlled rebuild: only the explicit maintenance runner applies this file.
DELETE FROM submission_slots;
UPDATE inspection_submissions SET current_revision_id = NULL, draft_revision_id = NULL;
DELETE FROM quantity_changes;
DELETE FROM revision_lines;
DELETE FROM submission_revisions;
DROP TABLE submission_slots;
DROP TABLE quantity_changes;
DROP TABLE revision_lines;
ALTER TABLE inspection_submissions DROP CONSTRAINT fk_v2_current_revision;
ALTER TABLE inspection_submissions DROP CONSTRAINT fk_v2_draft_revision;
DROP TABLE submission_revisions;
DROP TABLE inspection_submissions;
DROP TABLE operation_groups;
DROP TABLE product_lots;
DROP TABLE v2_release_state;
DELETE FROM audit_logs WHERE entity IN ('product_lot','operation_group','inspection_submission');
DELETE FROM sessions;
DELETE FROM v2_idempotency_requests;
DELETE FROM sample_parse_cache WHERE verified_by IS NULL;
ALTER TABLE users ADD COLUMN department TEXT;
UPDATE users SET department = (SELECT name FROM units WHERE units.id = users.unit_id);
ALTER TABLE users DROP COLUMN unit_id;
ALTER TABLE units RENAME TO preserved_organization_units;
CREATE TABLE units (
 id TEXT PRIMARY KEY CHECK(id = 'unit-piece'), code TEXT NOT NULL CHECK(code = 'PIECE'),
 name TEXT NOT NULL CHECK(name = '片'), sort_no INTEGER NOT NULL DEFAULT 0,
 active INTEGER NOT NULL DEFAULT 1 CHECK(active = 1), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO units VALUES('unit-piece','PIECE','片',0,1,'2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');
CREATE TABLE dictionary_id_mappings (source_id TEXT PRIMARY KEY, target_id TEXT NOT NULL);
INSERT INTO dictionary_id_mappings SELECT a.id,b.id FROM dict_items a JOIN dict_items b ON a.code=b.code AND b.category='inspection_operation' WHERE a.category='process';
DELETE FROM dict_items WHERE id IN (SELECT source_id FROM dictionary_id_mappings);
UPDATE dict_items SET category='inspection_operation' WHERE category='process';
ALTER TABLE sample_parse_cache ADD COLUMN parser_version TEXT NOT NULL DEFAULT 'legacy-unverified';
ALTER TABLE sample_parse_cache ADD COLUMN dictionary_version TEXT NOT NULL DEFAULT 'legacy-unverified';
ALTER TABLE sample_parse_cache ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 1;
CREATE TABLE field_schemas (version TEXT PRIMARY KEY, definition TEXT NOT NULL);
CREATE TABLE form_templates (version TEXT PRIMARY KEY, schema_version TEXT NOT NULL REFERENCES field_schemas(version), layout TEXT NOT NULL);
CREATE TABLE product_lots (
 id TEXT PRIMARY KEY, shared_form_no TEXT NOT NULL, sample_identity TEXT NOT NULL, sample_identity_key TEXT NOT NULL,
 sample_raw TEXT, sample_project TEXT, sample_model TEXT, sample_spec TEXT, sample_size TEXT, sample_side TEXT,
 version INTEGER NOT NULL CHECK(version > 0), created_by TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL,
 UNIQUE(shared_form_no,sample_identity_key)
);
CREATE TABLE operation_groups (
 id TEXT PRIMARY KEY, product_lot_id TEXT NOT NULL REFERENCES product_lots(id), operation_id TEXT NOT NULL REFERENCES dict_items(id),
 operation_code TEXT NOT NULL, operation_label TEXT NOT NULL, inspection_kind TEXT NOT NULL CHECK(inspection_kind IN ('NORMAL','SAMPLING')),
 template_version TEXT NOT NULL REFERENCES form_templates(version), reporting_state TEXT NOT NULL CHECK(reporting_state IN ('OPEN','CLOSED')),
 batch_mode TEXT NOT NULL CHECK(batch_mode IN ('EMPTY','WHOLE','SPLIT')), version INTEGER NOT NULL CHECK(version > 0),
 created_by TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL,
 UNIQUE(product_lot_id,operation_id,inspection_kind), UNIQUE(id,inspection_kind)
);
CREATE TABLE inspection_submissions (
 id TEXT PRIMARY KEY, operation_group_id TEXT REFERENCES operation_groups(id), batch_no TEXT,
 scope TEXT NOT NULL CHECK(scope IN ('WHOLE','SPLIT')), inspection_kind TEXT NOT NULL CHECK(inspection_kind IN ('NORMAL','SAMPLING')),
 state TEXT NOT NULL CHECK(state IN ('ACTIVE','CANCELLED')), version INTEGER NOT NULL CHECK(version > 0),
 current_revision_id TEXT NOT NULL, created_by TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL,
 last_saved_by TEXT NOT NULL REFERENCES users(id), last_saved_at TEXT NOT NULL,
 cancelled_by TEXT REFERENCES users(id), cancelled_at TEXT, cancel_reason TEXT,
 CHECK((scope='WHOLE' AND batch_no IS NULL) OR (scope='SPLIT' AND batch_no IS NOT NULL AND batch_no <> '')),
 UNIQUE(id,operation_group_id),
 FOREIGN KEY(operation_group_id,inspection_kind) REFERENCES operation_groups(id,inspection_kind)
);
CREATE TABLE submission_revisions (
 id TEXT PRIMARY KEY, submission_id TEXT NOT NULL REFERENCES inspection_submissions(id) DEFERRABLE INITIALLY DEFERRED,
 revision_no INTEGER NOT NULL CHECK(revision_no > 0), base_revision_id TEXT,
 template_version TEXT NOT NULL REFERENCES form_templates(version), content_schema_version TEXT NOT NULL REFERENCES field_schemas(version),
 content_json TEXT NOT NULL, saved_by TEXT NOT NULL REFERENCES users(id), saved_actor_name TEXT NOT NULL, saved_at TEXT NOT NULL, reason TEXT,
 total_qty BIGINT CHECK(total_qty BETWEEN 0 AND 9007199254740991), qualified_qty BIGINT CHECK(qualified_qty BETWEEN 0 AND 9007199254740991), defect_qty BIGINT CHECK(defect_qty BETWEEN 0 AND 9007199254740991),
 paper_inspected_day TEXT, paper_submitted_day TEXT, machine_no TEXT, paper_inspector TEXT, paper_submitter TEXT,
 paper_machine_operator TEXT, paper_handover_by TEXT, sample_raw TEXT, sample_project TEXT, sample_model TEXT, sample_spec TEXT, sample_size TEXT, sample_side TEXT,
 is_complete INTEGER NOT NULL, missing_fields TEXT NOT NULL,
 UNIQUE(id,submission_id), UNIQUE(submission_id,revision_no),
 FOREIGN KEY(base_revision_id,submission_id) REFERENCES submission_revisions(id,submission_id)
);
CREATE TABLE submission_slots (
 operation_group_id TEXT NOT NULL REFERENCES operation_groups(id), batch_key TEXT NOT NULL,
 submission_id TEXT NOT NULL UNIQUE REFERENCES inspection_submissions(id), PRIMARY KEY(operation_group_id,batch_key),
 FOREIGN KEY(submission_id,operation_group_id) REFERENCES inspection_submissions(id,operation_group_id)
);
CREATE TABLE revision_lines (
 revision_id TEXT NOT NULL REFERENCES submission_revisions(id), seq INTEGER NOT NULL,
 total_qty BIGINT CHECK(total_qty >= 0), qualified_qty BIGINT CHECK(qualified_qty >= 0), defect_qty BIGINT CHECK(defect_qty >= 0),
 prick_concave BIGINT CHECK(prick_concave >= 0),
 prick_convex BIGINT CHECK(prick_convex >= 0),
 prick_both BIGINT CHECK(prick_both >= 0),
 scratch_concave BIGINT CHECK(scratch_concave >= 0),
 scratch_convex BIGINT CHECK(scratch_convex >= 0),
 scratch_both BIGINT CHECK(scratch_both >= 0),
 flange_prick_concave BIGINT CHECK(flange_prick_concave >= 0),
 flange_prick_convex BIGINT CHECK(flange_prick_convex >= 0),
 flange_prick_both BIGINT CHECK(flange_prick_both >= 0),
 flange_scratch_concave BIGINT CHECK(flange_scratch_concave >= 0),
 flange_scratch_convex BIGINT CHECK(flange_scratch_convex >= 0),
 flange_scratch_both BIGINT CHECK(flange_scratch_both >= 0),
 collapse_edge_concave BIGINT CHECK(collapse_edge_concave >= 0),
 collapse_edge_convex BIGINT CHECK(collapse_edge_convex >= 0),
 collapse_edge_both BIGINT CHECK(collapse_edge_both >= 0),
 coating_particle_concave BIGINT CHECK(coating_particle_concave >= 0),
 coating_particle_convex BIGINT CHECK(coating_particle_convex >= 0),
 coating_particle_both BIGINT CHECK(coating_particle_both >= 0),
 bright_spot_concave BIGINT CHECK(bright_spot_concave >= 0),
 bright_spot_convex BIGINT CHECK(bright_spot_convex >= 0),
 bright_spot_both BIGINT CHECK(bright_spot_both >= 0),
 crack_concave BIGINT CHECK(crack_concave >= 0),
 crack_convex BIGINT CHECK(crack_convex >= 0),
 crack_both BIGINT CHECK(crack_both >= 0),
 overflow_glue_concave BIGINT CHECK(overflow_glue_concave >= 0),
 overflow_glue_convex BIGINT CHECK(overflow_glue_convex >= 0),
 overflow_glue_both BIGINT CHECK(overflow_glue_both >= 0),
 discolor_particle_concave BIGINT CHECK(discolor_particle_concave >= 0),
 discolor_particle_convex BIGINT CHECK(discolor_particle_convex >= 0),
 discolor_particle_both BIGINT CHECK(discolor_particle_both >= 0),
 protective_particle_concave BIGINT CHECK(protective_particle_concave >= 0),
 protective_particle_convex BIGINT CHECK(protective_particle_convex >= 0),
 protective_particle_both BIGINT CHECK(protective_particle_both >= 0),
 spin_particle_concave BIGINT CHECK(spin_particle_concave >= 0),
 spin_particle_convex BIGINT CHECK(spin_particle_convex >= 0),
 spin_particle_both BIGINT CHECK(spin_particle_both >= 0),
 fiber_concave BIGINT CHECK(fiber_concave >= 0),
 fiber_convex BIGINT CHECK(fiber_convex >= 0),
 fiber_both BIGINT CHECK(fiber_both >= 0),
 flange_particle_concave BIGINT CHECK(flange_particle_concave >= 0),
 flange_particle_convex BIGINT CHECK(flange_particle_convex >= 0),
 flange_particle_both BIGINT CHECK(flange_particle_both >= 0),
 hardening_bubble_concave BIGINT CHECK(hardening_bubble_concave >= 0),
 hardening_bubble_convex BIGINT CHECK(hardening_bubble_convex >= 0),
 hardening_bubble_both BIGINT CHECK(hardening_bubble_both >= 0),
 hairline_crack_concave BIGINT CHECK(hairline_crack_concave >= 0),
 hairline_crack_convex BIGINT CHECK(hairline_crack_convex >= 0),
 hairline_crack_both BIGINT CHECK(hairline_crack_both >= 0),
 dirt_concave BIGINT CHECK(dirt_concave >= 0),
 dirt_convex BIGINT CHECK(dirt_convex >= 0),
 dirt_both BIGINT CHECK(dirt_both >= 0),
 spin_mark_concave BIGINT CHECK(spin_mark_concave >= 0),
 spin_mark_convex BIGINT CHECK(spin_mark_convex >= 0),
 spin_mark_both BIGINT CHECK(spin_mark_both >= 0),
 raised_point_concave BIGINT CHECK(raised_point_concave >= 0),
 raised_point_convex BIGINT CHECK(raised_point_convex >= 0),
 raised_point_both BIGINT CHECK(raised_point_both >= 0),
 recess_concave BIGINT CHECK(recess_concave >= 0),
 recess_convex BIGINT CHECK(recess_convex >= 0),
 recess_both BIGINT CHECK(recess_both >= 0),
 bubble_concave BIGINT CHECK(bubble_concave >= 0),
 bubble_convex BIGINT CHECK(bubble_convex >= 0),
 bubble_both BIGINT CHECK(bubble_both >= 0),
 shape_concave BIGINT CHECK(shape_concave >= 0),
 shape_convex BIGINT CHECK(shape_convex >= 0),
 shape_both BIGINT CHECK(shape_both >= 0),
 watermark_concave BIGINT CHECK(watermark_concave >= 0),
 watermark_convex BIGINT CHECK(watermark_convex >= 0),
 watermark_both BIGINT CHECK(watermark_both >= 0),
 hard_spot_concave BIGINT CHECK(hard_spot_concave >= 0),
 hard_spot_convex BIGINT CHECK(hard_spot_convex >= 0),
 hard_spot_both BIGINT CHECK(hard_spot_both >= 0),
 flange_round_point_concave BIGINT CHECK(flange_round_point_concave >= 0),
 flange_round_point_convex BIGINT CHECK(flange_round_point_convex >= 0),
 flange_round_point_both BIGINT CHECK(flange_round_point_both >= 0),
 severe_flange_damage_concave BIGINT CHECK(severe_flange_damage_concave >= 0),
 severe_flange_damage_convex BIGINT CHECK(severe_flange_damage_convex >= 0),
 severe_flange_damage_both BIGINT CHECK(severe_flange_damage_both >= 0),
 garbage_concave BIGINT CHECK(garbage_concave >= 0),
 garbage_convex BIGINT CHECK(garbage_convex >= 0),
 garbage_both BIGINT CHECK(garbage_both >= 0),
 flange_damage_concave BIGINT CHECK(flange_damage_concave >= 0),
 flange_damage_convex BIGINT CHECK(flange_damage_convex >= 0),
 flange_damage_both BIGINT CHECK(flange_damage_both >= 0),
 flange_imprint_concave BIGINT CHECK(flange_imprint_concave >= 0),
 flange_imprint_convex BIGINT CHECK(flange_imprint_convex >= 0),
 flange_imprint_both BIGINT CHECK(flange_imprint_both >= 0),
 press_imprint_concave BIGINT CHECK(press_imprint_concave >= 0),
 press_imprint_convex BIGINT CHECK(press_imprint_convex >= 0),
 press_imprint_both BIGINT CHECK(press_imprint_both >= 0),
 flow_mark_concave BIGINT CHECK(flow_mark_concave >= 0),
 flow_mark_convex BIGINT CHECK(flow_mark_convex >= 0),
 flow_mark_both BIGINT CHECK(flow_mark_both >= 0),
 inner_fold_concave BIGINT CHECK(inner_fold_concave >= 0),
 inner_fold_convex BIGINT CHECK(inner_fold_convex >= 0),
 inner_fold_both BIGINT CHECK(inner_fold_both >= 0),
 flange_fold_concave BIGINT CHECK(flange_fold_concave >= 0),
 flange_fold_convex BIGINT CHECK(flange_fold_convex >= 0),
 flange_fold_both BIGINT CHECK(flange_fold_both >= 0),
 inner_flow_glue_concave BIGINT CHECK(inner_flow_glue_concave >= 0),
 inner_flow_glue_convex BIGINT CHECK(inner_flow_glue_convex >= 0),
 inner_flow_glue_both BIGINT CHECK(inner_flow_glue_both >= 0),
 flange_recess_concave BIGINT CHECK(flange_recess_concave >= 0),
 flange_recess_convex BIGINT CHECK(flange_recess_convex >= 0),
 flange_recess_both BIGINT CHECK(flange_recess_both >= 0),
 remark TEXT, PRIMARY KEY(revision_id,seq)
);
CREATE TABLE quantity_changes (
 id TEXT PRIMARY KEY, revision_id TEXT NOT NULL REFERENCES submission_revisions(id),
 kind TEXT NOT NULL, direction TEXT NOT NULL, quantity BIGINT CHECK(quantity >= 0), reason TEXT NOT NULL,
 occurred_date TEXT, occurred_at TEXT
);
CREATE INDEX ix_v2_changes_revision ON quantity_changes(revision_id,kind,direction);

CREATE INDEX ix_current_group ON inspection_submissions(operation_group_id,state);
CREATE INDEX ix_revision_day ON submission_revisions(paper_inspected_day);
CREATE INDEX ix_revision_people ON submission_revisions(paper_inspector,saved_by);
CREATE TABLE write_gate (id INTEGER PRIMARY KEY CHECK(id=1), last_event_time TEXT NOT NULL);
INSERT INTO write_gate VALUES(1,'0001-01-01T00:00:00Z');
DROP TABLE v2_write_lock;
CREATE TABLE event_transactions (id TEXT PRIMARY KEY, domain INTEGER NOT NULL, created_at TEXT NOT NULL, last_event_id BIGINT);
CREATE TABLE business_events (
 event_id BIGSERIAL PRIMARY KEY, transaction_id TEXT NOT NULL REFERENCES event_transactions(id),
 actor_id TEXT NOT NULL, actor_name TEXT NOT NULL, action TEXT NOT NULL, entity TEXT NOT NULL, entity_id TEXT NOT NULL,
 before_json TEXT, after_json TEXT, reason TEXT, created_at TEXT NOT NULL
);
CREATE INDEX ix_event_entity ON business_events(entity,entity_id,event_id);
CREATE TABLE rebuild_state (id INTEGER PRIMARY KEY, history_started_at TEXT);
INSERT INTO rebuild_state VALUES(1,NULL);
CREATE TABLE maintenance_receipts (id TEXT PRIMARY KEY, source_version INTEGER NOT NULL, preserved_digest TEXT NOT NULL, created_at TEXT NOT NULL);

ALTER TABLE inspection_submissions ADD CONSTRAINT current_revision_owner FOREIGN KEY(current_revision_id,id) REFERENCES submission_revisions(id,submission_id) DEFERRABLE INITIALLY DEFERRED;
