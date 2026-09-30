-- 0001_init.sqlite.up.sql
-- 镜片表面质量检验记录系统 —— 初始表结构（SQLite / 开发与单机部署）
-- 设计要点（对应 docs/requirements.md v0.4 §1.4 最大灵活性）：
--   * 除 form_no 唯一之外，几乎所有业务字段都可空
--   * 工序同时存在于「笔」与「行」（不收敛未知语义）

CREATE TABLE users (
  id                TEXT PRIMARY KEY,
  username          TEXT NOT NULL,
  display_name      TEXT NOT NULL DEFAULT '',
  employee_no       TEXT,
  unit_id           TEXT,
  password_hash     TEXT NOT NULL,
  is_admin          INTEGER NOT NULL DEFAULT 0,
  must_change_pwd   INTEGER NOT NULL DEFAULT 0,
  status            TEXT NOT NULL DEFAULT 'active',
  failed_attempts   INTEGER NOT NULL DEFAULT 0,
  locked_until      TEXT,
  last_login_at     TEXT,
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_users_username ON users (username);
CREATE UNIQUE INDEX ux_users_employee_no ON users (employee_no) WHERE employee_no IS NOT NULL;
CREATE INDEX ix_users_status ON users (status);

CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL,
  ip           TEXT,
  user_agent   TEXT,
  created_at   TEXT NOT NULL DEFAULT (datetime('now')),
  last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at   TEXT NOT NULL,
  revoked_at   TEXT
);
CREATE UNIQUE INDEX ux_sessions_token_hash ON sessions (token_hash);
CREATE INDEX ix_sessions_user ON sessions (user_id);
CREATE INDEX ix_sessions_expiry ON sessions (expires_at);

CREATE TABLE units (
  id         TEXT PRIMARY KEY,
  code       TEXT NOT NULL,
  name       TEXT NOT NULL,
  sort_no    INTEGER NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_units_code ON units (code);

CREATE TABLE dict_items (
  id         TEXT PRIMARY KEY,
  category   TEXT NOT NULL,
  code       TEXT NOT NULL,
  label      TEXT NOT NULL DEFAULT '',
  sort_no    INTEGER NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  meta       TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_dict_items_category_code ON dict_items (category, code);
CREATE INDEX ix_dict_items_category ON dict_items (category, active, sort_no);

-- 参考部件号清单：仅供参考，系统不因此拒绝任何输入（B-6 / B-8）
CREATE TABLE sample_drawings (
  id          TEXT PRIMARY KEY,
  seq_no      TEXT,
  drawing_no  TEXT NOT NULL,
  project_code TEXT,
  model_code  TEXT,
  spec_code   TEXT,
  size_code   TEXT,
  side_code   TEXT,
  class_code  TEXT,
  is_rx       INTEGER NOT NULL DEFAULT 0,
  active      INTEGER NOT NULL DEFAULT 1,
  note        TEXT,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_sample_drawings_drawing_no ON sample_drawings (drawing_no);
CREATE INDEX ix_sample_drawings_dim ON sample_drawings (spec_code, size_code, side_code, model_code);

-- 原始串 → 结构化结果的缓存（提升重复录入速度并保持一致性）
CREATE TABLE sample_parse_cache (
  id          TEXT PRIMARY KEY,
  raw_key     TEXT NOT NULL,
  raw_sample  TEXT NOT NULL,
  parsed      TEXT,
  drawing_id  TEXT,
  confidence  REAL NOT NULL DEFAULT 0,
  hit_count   INTEGER NOT NULL DEFAULT 0,
  verified_by TEXT,
  verified_at TEXT,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_sample_parse_cache_raw_key ON sample_parse_cache (raw_key);

-- ============================ 表单 / 笔 / 行 ============================

CREATE TABLE forms (
  id                 TEXT PRIMARY KEY,
  form_no            TEXT NOT NULL,
  unit_id            TEXT,
  unit_text          TEXT,
  sample_raw         TEXT NOT NULL DEFAULT '',
  sample_project     TEXT,
  sample_model       TEXT,
  sample_spec        TEXT,
  sample_size        TEXT,
  sample_side        TEXT,
  sample_class       TEXT,
  sample_is_rx       INTEGER,
  sample_drawing_id  TEXT,
  sample_unmatched_reason TEXT,
  sample_parsed_by   TEXT NOT NULL DEFAULT 'auto',
  sample_confidence  REAL NOT NULL DEFAULT 0,
  sample_verified    INTEGER NOT NULL DEFAULT 0,
  needs_review       INTEGER NOT NULL DEFAULT 0,
  unknown_tokens     TEXT,
  state              TEXT NOT NULL DEFAULT 'ACTIVE',
  voided_at          TEXT,
  void_reason        TEXT,
  remark             TEXT,
  created_by         TEXT,
  created_at         TEXT NOT NULL DEFAULT (datetime('now')),
  updated_by         TEXT,
  updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
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

-- 每次保存前的完整快照（追加写，不可改不可删）——支撑历史追溯
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

CREATE TABLE audit_logs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_id   TEXT,
  actor_name TEXT,
  action     TEXT NOT NULL,
  entity     TEXT NOT NULL,
  entity_id  TEXT,
  before     TEXT,
  after      TEXT,
  ip         TEXT,
  user_agent TEXT,
  detail     TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX ix_audit_logs_entity ON audit_logs (entity, entity_id, created_at);
CREATE INDEX ix_audit_logs_actor ON audit_logs (actor_id, created_at);
CREATE INDEX ix_audit_logs_created ON audit_logs (created_at);
