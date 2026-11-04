-- v2 additive schema. All dates and instants intentionally remain TEXT.
CREATE TABLE product_lots (
 id TEXT PRIMARY KEY, shared_form_no TEXT NOT NULL, sample_identity TEXT NOT NULL,
 sample_identity_key TEXT NOT NULL, sample_raw TEXT, sample_project TEXT, sample_model TEXT,
 sample_spec TEXT, sample_size TEXT, sample_side TEXT, version INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(shared_form_no, sample_identity_key)
);
CREATE TABLE operation_groups (
 id TEXT PRIMARY KEY, product_lot_id TEXT NOT NULL REFERENCES product_lots(id),
 operation_id TEXT NOT NULL REFERENCES dict_items(id), operation_code TEXT NOT NULL,
 inspection_kind TEXT NOT NULL CHECK(inspection_kind IN ('NORMAL','SAMPLING')),
 template_version TEXT NOT NULL, reporting_state TEXT NOT NULL CHECK(reporting_state IN ('OPEN','CLOSED')),
 batch_mode TEXT NOT NULL CHECK(batch_mode IN ('EMPTY','WHOLE','SPLIT')), version INTEGER NOT NULL,
 UNIQUE(product_lot_id,operation_id,inspection_kind),
 UNIQUE(id,inspection_kind)
);
CREATE TABLE inspection_submissions (
 id TEXT PRIMARY KEY, operation_group_id TEXT REFERENCES operation_groups(id), batch_no TEXT,
 inspection_kind TEXT NOT NULL CHECK(inspection_kind IN ('NORMAL','SAMPLING')),
 state TEXT NOT NULL CHECK(state IN ('DRAFT','PUBLISHED','CANCELLED')), version INTEGER NOT NULL,
 current_revision_id TEXT, draft_revision_id TEXT, created_by TEXT NOT NULL REFERENCES users(id),
 created_at TEXT NOT NULL, cancelled_by TEXT REFERENCES users(id), cancelled_at TEXT, cancel_reason TEXT,
 UNIQUE(id,operation_group_id),
 FOREIGN KEY(operation_group_id,inspection_kind) REFERENCES operation_groups(id,inspection_kind)
);
CREATE INDEX ix_v2_submissions_group ON inspection_submissions(operation_group_id,state);
CREATE TABLE submission_slots (
 operation_group_id TEXT NOT NULL REFERENCES operation_groups(id), batch_key TEXT NOT NULL,
 submission_id TEXT NOT NULL UNIQUE REFERENCES inspection_submissions(id), PRIMARY KEY(operation_group_id,batch_key),
 FOREIGN KEY(submission_id,operation_group_id) REFERENCES inspection_submissions(id,operation_group_id)
);
CREATE TABLE submission_revisions (
 id TEXT PRIMARY KEY, submission_id TEXT NOT NULL REFERENCES inspection_submissions(id), revision_no INTEGER NOT NULL,
 base_revision_id TEXT REFERENCES submission_revisions(id), template_version TEXT NOT NULL, inspection_kind TEXT NOT NULL,
 content TEXT NOT NULL, paper_inspected_date TEXT, paper_inspected_at TEXT, paper_inspected_day TEXT,
 paper_submitted_date TEXT, paper_submitted_at TEXT, paper_submitted_day TEXT,
 total_qty BIGINT CHECK(total_qty >= 0), qualified_qty BIGINT CHECK(qualified_qty >= 0), defect_qty BIGINT CHECK(defect_qty >= 0),
 machine_no TEXT, paper_inspector TEXT, saved_by TEXT NOT NULL REFERENCES users(id), saved_at TEXT NOT NULL,
 published_by TEXT REFERENCES users(id), published_at TEXT, reason TEXT,
 UNIQUE(submission_id,revision_no),
 UNIQUE(id,submission_id)
);
CREATE INDEX ix_v2_revision_day ON submission_revisions(paper_inspected_day);
CREATE INDEX ix_v2_revision_machine ON submission_revisions(machine_no,paper_inspector);
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
CREATE TABLE v2_idempotency_requests (
 actor_id TEXT NOT NULL REFERENCES users(id), idempotency_key TEXT NOT NULL, fingerprint TEXT NOT NULL,
 response_status INTEGER NOT NULL, response_body TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL,
 PRIMARY KEY(actor_id,idempotency_key)
);
CREATE TABLE v2_write_lock (id INTEGER PRIMARY KEY, version INTEGER NOT NULL);
INSERT INTO v2_write_lock(id,version) VALUES(1,0);
CREATE TABLE legacy_migration_candidates (
 source_id TEXT PRIMARY KEY REFERENCES forms(id), source_fingerprint TEXT NOT NULL,
 candidate TEXT NOT NULL, review_status TEXT NOT NULL DEFAULT 'PENDING', submission_id TEXT UNIQUE REFERENCES inspection_submissions(id),
 reviewed_by TEXT REFERENCES users(id), reviewed_at TEXT, reason TEXT
);

ALTER TABLE inspection_submissions ADD CONSTRAINT fk_v2_current_revision FOREIGN KEY(current_revision_id,id) REFERENCES submission_revisions(id,submission_id);
ALTER TABLE inspection_submissions ADD CONSTRAINT fk_v2_draft_revision FOREIGN KEY(draft_revision_id,id) REFERENCES submission_revisions(id,submission_id);
