-- 0003_form_submission.sqlite.down.sql
-- 回滚：恢复「表单 + 多笔工序记录」的旧结构（见 0001_init）。
-- 注意：由于该结构在 0003 中被删除，回滚会丢失 0003 之后录入的数据。

DROP TABLE IF EXISTS form_history;
DROP TABLE IF EXISTS form_lines;
DROP TABLE IF EXISTS forms;

CREATE TABLE forms (
  id                       TEXT PRIMARY KEY,
  form_no                  TEXT NOT NULL,
  unit_id                  TEXT,
  unit_text                TEXT,
  sample_raw               TEXT NOT NULL DEFAULT '',
  sample_project           TEXT,
  sample_model             TEXT,
  sample_spec              TEXT,
  sample_size              TEXT,
  sample_side              TEXT,
  sample_class             TEXT,
  sample_is_rx             INTEGER,
  sample_drawing_id        TEXT,
  sample_unmatched_reason  TEXT,
  sample_parsed_by         TEXT NOT NULL DEFAULT 'auto',
  sample_confidence        REAL NOT NULL DEFAULT 0,
  sample_verified          INTEGER NOT NULL DEFAULT 0,
  needs_review             INTEGER NOT NULL DEFAULT 0,
  unknown_tokens           TEXT,
  state                    TEXT NOT NULL DEFAULT 'ACTIVE',
  voided_at                TEXT,
  void_reason              TEXT,
  remark                   TEXT,
  created_by               TEXT,
  created_at               TEXT NOT NULL DEFAULT (datetime('now')),
  updated_by               TEXT,
  updated_at               TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_forms_form_no ON forms (form_no);
CREATE INDEX ix_forms_state_created ON forms (state, created_at);
CREATE INDEX ix_forms_sample_dim ON forms (sample_spec, sample_size, sample_side, sample_model);
CREATE INDEX ix_forms_project ON forms (sample_project);
CREATE INDEX ix_forms_review ON forms (needs_review, state);
CREATE INDEX ix_forms_unit ON forms (unit_id);

CREATE TABLE form_records (
  id               TEXT PRIMARY KEY,
  form_id          TEXT NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  sequence_no      INTEGER NOT NULL,
  process_code     TEXT,
  process_text     TEXT,
  submitted_by     TEXT,
  submitted_at     TEXT,
  inspected_by     TEXT,
  inspected_at     TEXT,
  machine_operator TEXT,
  handover_by      TEXT,
  recorded_by      TEXT,
  recorded_at      TEXT,
  save_count       INTEGER NOT NULL DEFAULT 1,
  last_saved_by    TEXT,
  last_saved_at    TEXT,
  is_complete      INTEGER NOT NULL DEFAULT 0,
  missing_fields   TEXT,
  remark           TEXT,
  total_qty        INTEGER NOT NULL DEFAULT 0,
  qualified_qty    INTEGER NOT NULL DEFAULT 0,
  defect_qty       INTEGER NOT NULL DEFAULT 0,
  pass_rate        REAL,
  state            TEXT NOT NULL DEFAULT 'ACTIVE',
  voided_at        TEXT,
  void_reason      TEXT,
  created_at       TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_form_records_form_seq ON form_records (form_id, sequence_no);
CREATE INDEX ix_form_records_form ON form_records (form_id);
CREATE INDEX ix_form_records_process ON form_records (process_code, inspected_at);
CREATE INDEX ix_form_records_inspector ON form_records (inspected_by, inspected_at);
CREATE INDEX ix_form_records_complete ON form_records (state, is_complete);

CREATE TABLE record_lines (
  id                TEXT PRIMARY KEY,
  record_id         TEXT NOT NULL REFERENCES form_records (id) ON DELETE CASCADE,
  seq               INTEGER NOT NULL DEFAULT 1,
  total_qty         INTEGER NOT NULL DEFAULT 0,
  qualified_qty     INTEGER NOT NULL DEFAULT 0,
  defect_qty        INTEGER NOT NULL DEFAULT 0,
  spot_concave      INTEGER NOT NULL DEFAULT 0,
  spot_convex       INTEGER NOT NULL DEFAULT 0,
  spot_both         INTEGER NOT NULL DEFAULT 0,
  scratch_concave   INTEGER NOT NULL DEFAULT 0,
  scratch_convex    INTEGER NOT NULL DEFAULT 0,
  scratch_both      INTEGER NOT NULL DEFAULT 0,
  dirt_concave      INTEGER NOT NULL DEFAULT 0,
  dirt_convex       INTEGER NOT NULL DEFAULT 0,
  dirt_both         INTEGER NOT NULL DEFAULT 0,
  hard_spot_concave INTEGER NOT NULL DEFAULT 0,
  hard_spot_convex  INTEGER NOT NULL DEFAULT 0,
  hard_spot_both    INTEGER NOT NULL DEFAULT 0,
  debris_concave    INTEGER NOT NULL DEFAULT 0,
  debris_convex     INTEGER NOT NULL DEFAULT 0,
  debris_both       INTEGER NOT NULL DEFAULT 0,
  process_code      TEXT,
  process_text      TEXT,
  remark            TEXT,
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_record_lines_record_seq ON record_lines (record_id, seq);
CREATE INDEX ix_record_lines_record ON record_lines (record_id);

CREATE TABLE record_history (
  id              TEXT PRIMARY KEY,
  record_id       TEXT NOT NULL REFERENCES form_records (id) ON DELETE CASCADE,
  form_id         TEXT NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  save_count      INTEGER NOT NULL,
  form_snapshot   TEXT,
  record_snapshot TEXT,
  lines_snapshot  TEXT,
  saved_by        TEXT,
  saved_at        TEXT,
  changed_by      TEXT,
  changed_at      TEXT NOT NULL DEFAULT (datetime('now')),
  reason          TEXT
);
CREATE INDEX ix_record_history_record ON record_history (record_id, save_count);
CREATE INDEX ix_record_history_form ON record_history (form_id, changed_at);
