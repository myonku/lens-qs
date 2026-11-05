-- Preserve reference rows and make unknown Rx representable.
ALTER TABLE sample_drawings RENAME TO sample_drawings_v7;
CREATE TABLE sample_drawings (
 id TEXT PRIMARY KEY, seq_no TEXT, drawing_no TEXT NOT NULL,
 project_code TEXT, model_code TEXT, spec_code TEXT, size_code TEXT, side_code TEXT, class_code TEXT,
 is_rx INTEGER CHECK(is_rx IN (0,1)), active INTEGER NOT NULL DEFAULT 1, note TEXT,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO sample_drawings SELECT * FROM sample_drawings_v7;
DROP TABLE sample_drawings_v7;
CREATE UNIQUE INDEX ux_sample_drawings_drawing_no ON sample_drawings(drawing_no);
CREATE UNIQUE INDEX ux_sample_drawings_seq_no ON sample_drawings(seq_no);
CREATE INDEX ix_sample_drawings_dim ON sample_drawings(spec_code,size_code,side_code,model_code);
ALTER TABLE submission_revisions ADD COLUMN paper_unit_text TEXT;
UPDATE submission_revisions SET paper_unit_text='片' WHERE content_json LIKE '%"paper_unit_text":"片"%';
CREATE INDEX ix_revision_sample ON submission_revisions(sample_project,sample_model,sample_spec,sample_size,sample_side);
CREATE INDEX ix_revision_machine ON submission_revisions(machine_no);
CREATE INDEX ix_revision_submitter ON submission_revisions(paper_submitter);
CREATE INDEX ix_revision_operator ON submission_revisions(paper_machine_operator);
CREATE INDEX ix_revision_handover ON submission_revisions(paper_handover_by);
CREATE INDEX ix_submission_created ON inspection_submissions(created_at,id);
CREATE INDEX ix_submission_creator ON inspection_submissions(created_by);
CREATE INDEX ix_submission_saved ON inspection_submissions(last_saved_by);
CREATE INDEX ix_event_time ON business_events(created_at,event_id);
CREATE INDEX ix_event_actor ON business_events(actor_id,created_at);
CREATE TABLE shared_catalog_state (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL);
INSERT INTO shared_catalog_state VALUES(1,1);
