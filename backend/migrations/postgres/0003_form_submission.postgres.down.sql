-- 0003_form_submission.postgres.down.sql
-- 回滚：恢复「表单 + 多笔工序记录」的旧结构（见 0001_init）。
-- 注意：由于该结构在 0003 中被删除，回滚会丢失 0003 之后录入的数据。

DROP TABLE IF EXISTS form_history;
DROP TABLE IF EXISTS form_lines;
DROP TABLE IF EXISTS forms;

CREATE TABLE forms (
  id                       text PRIMARY KEY,
  form_no                  text NOT NULL,
  unit_id                  text,
  unit_text                text,
  sample_raw               text NOT NULL DEFAULT '',
  sample_project           text,
  sample_model             text,
  sample_spec              text,
  sample_size              text,
  sample_side              text,
  sample_class             text,
  sample_is_rx             integer,
  sample_drawing_id        text,
  sample_unmatched_reason  text,
  sample_parsed_by         text NOT NULL DEFAULT 'auto',
  sample_confidence        double precision NOT NULL DEFAULT 0,
  sample_verified          integer NOT NULL DEFAULT 0,
  needs_review             integer NOT NULL DEFAULT 0,
  unknown_tokens           text,
  state                    text NOT NULL DEFAULT 'ACTIVE',
  voided_at                text,
  void_reason              text,
  remark                   text,
  created_by               text,
  created_at               text NOT NULL DEFAULT (now()),
  updated_by               text,
  updated_at               text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_forms_form_no ON forms (form_no);
CREATE INDEX ix_forms_state_created ON forms (state, created_at);
CREATE INDEX ix_forms_sample_dim ON forms (sample_spec, sample_size, sample_side, sample_model);
CREATE INDEX ix_forms_project ON forms (sample_project);
CREATE INDEX ix_forms_review ON forms (needs_review, state);
CREATE INDEX ix_forms_unit ON forms (unit_id);

CREATE TABLE form_records (
  id               text PRIMARY KEY,
  form_id          text NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  sequence_no      integer NOT NULL,
  process_code     text,
  process_text     text,
  submitted_by     text,
  submitted_at     text,
  inspected_by     text,
  inspected_at     text,
  machine_operator text,
  handover_by      text,
  recorded_by      text,
  recorded_at      text,
  save_count       integer NOT NULL DEFAULT 1,
  last_saved_by    text,
  last_saved_at    text,
  is_complete      integer NOT NULL DEFAULT 0,
  missing_fields   text,
  remark           text,
  total_qty        integer NOT NULL DEFAULT 0,
  qualified_qty    integer NOT NULL DEFAULT 0,
  defect_qty       integer NOT NULL DEFAULT 0,
  pass_rate        double precision,
  state            text NOT NULL DEFAULT 'ACTIVE',
  voided_at        text,
  void_reason      text,
  created_at       text NOT NULL DEFAULT (now()),
  updated_at       text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_form_records_form_seq ON form_records (form_id, sequence_no);
CREATE INDEX ix_form_records_form ON form_records (form_id);
CREATE INDEX ix_form_records_process ON form_records (process_code, inspected_at);
CREATE INDEX ix_form_records_inspector ON form_records (inspected_by, inspected_at);
CREATE INDEX ix_form_records_complete ON form_records (state, is_complete);

CREATE TABLE record_lines (
  id                text PRIMARY KEY,
  record_id         text NOT NULL REFERENCES form_records (id) ON DELETE CASCADE,
  seq               integer NOT NULL DEFAULT 1,
  total_qty         integer NOT NULL DEFAULT 0,
  qualified_qty     integer NOT NULL DEFAULT 0,
  defect_qty        integer NOT NULL DEFAULT 0,
  spot_concave      integer NOT NULL DEFAULT 0,
  spot_convex       integer NOT NULL DEFAULT 0,
  spot_both         integer NOT NULL DEFAULT 0,
  scratch_concave   integer NOT NULL DEFAULT 0,
  scratch_convex    integer NOT NULL DEFAULT 0,
  scratch_both      integer NOT NULL DEFAULT 0,
  dirt_concave      integer NOT NULL DEFAULT 0,
  dirt_convex       integer NOT NULL DEFAULT 0,
  dirt_both         integer NOT NULL DEFAULT 0,
  hard_spot_concave integer NOT NULL DEFAULT 0,
  hard_spot_convex  integer NOT NULL DEFAULT 0,
  hard_spot_both    integer NOT NULL DEFAULT 0,
  debris_concave    integer NOT NULL DEFAULT 0,
  debris_convex     integer NOT NULL DEFAULT 0,
  debris_both       integer NOT NULL DEFAULT 0,
  process_code      text,
  process_text      text,
  remark            text,
  created_at        text NOT NULL DEFAULT (now()),
  updated_at        text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_record_lines_record_seq ON record_lines (record_id, seq);
CREATE INDEX ix_record_lines_record ON record_lines (record_id);

CREATE TABLE record_history (
  id              text PRIMARY KEY,
  record_id       text NOT NULL REFERENCES form_records (id) ON DELETE CASCADE,
  form_id         text NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  save_count      integer NOT NULL,
  form_snapshot   text,
  record_snapshot text,
  lines_snapshot  text,
  saved_by        text,
  saved_at        text,
  changed_by      text,
  changed_at      text NOT NULL DEFAULT (now()),
  reason          text
);
CREATE INDEX ix_record_history_record ON record_history (record_id, save_count);
CREATE INDEX ix_record_history_form ON record_history (form_id, changed_at);
