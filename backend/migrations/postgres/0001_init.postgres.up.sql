-- 0001_init.postgres.up.sql
-- 镜片表面质量检验记录系统 —— 初始表结构（PostgreSQL / 生产）
-- 设计要点（对应 docs/requirements.md v0.4 §1.4 最大灵活性）：
--   * 除 form_no 唯一之外，几乎所有业务字段都可空
--   * 工序同时存在于「笔」与「行」（不收敛未知语义）

CREATE TABLE users (
  id                text PRIMARY KEY,
  username          text NOT NULL,
  display_name      text NOT NULL DEFAULT '',
  employee_no       text,
  unit_id           text,
  password_hash     text NOT NULL,
  is_admin          integer NOT NULL DEFAULT 0,
  must_change_pwd   integer NOT NULL DEFAULT 0,
  status            text NOT NULL DEFAULT 'active',
  failed_attempts   integer NOT NULL DEFAULT 0,
  locked_until      text,
  last_login_at     text,
  created_at        text NOT NULL DEFAULT (now()),
  updated_at        text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_users_username ON users (username);
CREATE UNIQUE INDEX ux_users_employee_no ON users (employee_no) WHERE employee_no IS NOT NULL;
CREATE INDEX ix_users_status ON users (status);

CREATE TABLE sessions (
  id           text PRIMARY KEY,
  user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash   text NOT NULL,
  ip           text,
  user_agent   text,
  created_at   text NOT NULL DEFAULT (now()),
  last_seen_at text NOT NULL DEFAULT (now()),
  expires_at   text NOT NULL,
  revoked_at   text
);
CREATE UNIQUE INDEX ux_sessions_token_hash ON sessions (token_hash);
CREATE INDEX ix_sessions_user ON sessions (user_id);
CREATE INDEX ix_sessions_expiry ON sessions (expires_at);

CREATE TABLE units (
  id         text PRIMARY KEY,
  code       text NOT NULL,
  name       text NOT NULL,
  sort_no    integer NOT NULL DEFAULT 0,
  active     integer NOT NULL DEFAULT 1,
  created_at text NOT NULL DEFAULT (now()),
  updated_at text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_units_code ON units (code);

CREATE TABLE dict_items (
  id         text PRIMARY KEY,
  category   text NOT NULL,
  code       text NOT NULL,
  label      text NOT NULL DEFAULT '',
  sort_no    integer NOT NULL DEFAULT 0,
  active     integer NOT NULL DEFAULT 1,
  meta       text,
  created_at text NOT NULL DEFAULT (now()),
  updated_at text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_dict_items_category_code ON dict_items (category, code);
CREATE INDEX ix_dict_items_category ON dict_items (category, active, sort_no);

-- 参考部件号清单：仅供参考，系统不因此拒绝任何输入（B-6 / B-8）
CREATE TABLE sample_drawings (
  id          text PRIMARY KEY,
  seq_no      text,
  drawing_no  text NOT NULL,
  project_code text,
  model_code  text,
  spec_code   text,
  size_code   text,
  side_code   text,
  class_code  text,
  is_rx       integer NOT NULL DEFAULT 0,
  active      integer NOT NULL DEFAULT 1,
  note        text,
  created_at  text NOT NULL DEFAULT (now()),
  updated_at  text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_sample_drawings_drawing_no ON sample_drawings (drawing_no);
CREATE INDEX ix_sample_drawings_dim ON sample_drawings (spec_code, size_code, side_code, model_code);

-- 原始串 → 结构化结果的缓存（提升重复录入速度并保持一致性）
CREATE TABLE sample_parse_cache (
  id          text PRIMARY KEY,
  raw_key     text NOT NULL,
  raw_sample  text NOT NULL,
  parsed      text,
  drawing_id  text,
  confidence  double precision NOT NULL DEFAULT 0,
  hit_count   integer NOT NULL DEFAULT 0,
  verified_by text,
  verified_at text,
  created_at  text NOT NULL DEFAULT (now()),
  updated_at  text NOT NULL DEFAULT (now())
);
CREATE UNIQUE INDEX ux_sample_parse_cache_raw_key ON sample_parse_cache (raw_key);

-- ============================ 表单 / 笔 / 行 ============================

CREATE TABLE forms (
  id                 text PRIMARY KEY,
  form_no            text NOT NULL,
  unit_id            text,
  unit_text          text,
  sample_raw         text NOT NULL DEFAULT '',
  sample_project     text,
  sample_model       text,
  sample_spec        text,
  sample_size        text,
  sample_side        text,
  sample_class       text,
  sample_is_rx       integer,
  sample_drawing_id  text,
  sample_unmatched_reason text,
  sample_parsed_by   text NOT NULL DEFAULT 'auto',
  sample_confidence  double precision NOT NULL DEFAULT 0,
  sample_verified    integer NOT NULL DEFAULT 0,
  needs_review       integer NOT NULL DEFAULT 0,
  unknown_tokens     text,
  state              text NOT NULL DEFAULT 'ACTIVE',
  voided_at          text,
  void_reason        text,
  remark             text,
  created_by         text,
  created_at         text NOT NULL DEFAULT (now()),
  updated_by         text,
  updated_at         text NOT NULL DEFAULT (now())
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

-- 每次保存前的完整快照（追加写，不可改不可删）——支撑历史追溯
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

CREATE TABLE audit_logs (
  id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  actor_id   text,
  actor_name text,
  action     text NOT NULL,
  entity     text NOT NULL,
  entity_id  text,
  before     text,
  after      text,
  ip         text,
  user_agent text,
  detail     text,
  created_at text NOT NULL DEFAULT (now())
);
CREATE INDEX ix_audit_logs_entity ON audit_logs (entity, entity_id, created_at);
CREATE INDEX ix_audit_logs_actor ON audit_logs (actor_id, created_at);
CREATE INDEX ix_audit_logs_created ON audit_logs (created_at);
