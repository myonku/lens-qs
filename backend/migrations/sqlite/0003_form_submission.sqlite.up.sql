-- 0003_form_submission.sqlite.up.sql
-- 模型重构：录入场景是「每道工序填自己那张表单」，因此把
--   forms(一张纸质表单 + 多笔工序记录) + form_records + record_lines + record_history
-- 改为
--   forms(一次表单提交) + form_lines(明细行，含全部不良缺陷列) + form_history
--
-- 本项目处于开发阶段，无生产数据，故直接重建相关表。

DROP TABLE IF EXISTS record_history;
DROP TABLE IF EXISTS record_lines;
DROP TABLE IF EXISTS form_records;
DROP TABLE IF EXISTS form_history;
DROP TABLE IF EXISTS form_lines;
DROP TABLE IF EXISTS forms;

-- ============================ 表单提交（一次提交 = 某工序填的那张表单）============================

CREATE TABLE forms (
  id              TEXT PRIMARY KEY,
  form_no         TEXT NOT NULL,

  -- 表单第一列：检验工序（也是"找到自己工序所在表单"的依据）
  operation_code  TEXT NOT NULL DEFAULT '',
  operation_text  TEXT,

  -- 样品信息：原始串是事实来源；结构化字段用于查询与汇总
  sample_raw      TEXT NOT NULL DEFAULT '',
  sample_project  TEXT,
  sample_model    TEXT,
  sample_spec     TEXT,
  sample_size     TEXT,
  sample_side     TEXT,

  -- 单位（字典 id 或原文）
  unit_id         TEXT,
  unit_text       TEXT,

  -- 系统留痕（与纸面人名无关）
  submitted_by    TEXT,                       -- 实际录入的系统用户
  submitted_at    TEXT,
  last_saved_by   TEXT,
  last_saved_at   TEXT,
  save_count      INTEGER NOT NULL DEFAULT 1,

  -- 表头栏目汇总（有明细时由服务端按明细重算）
  total_qty       INTEGER NOT NULL DEFAULT 0,
  qualified_qty   INTEGER NOT NULL DEFAULT 0,
  defect_qty      INTEGER NOT NULL DEFAULT 0,
  pass_rate       REAL,

  -- 完成度
  is_complete     INTEGER NOT NULL DEFAULT 0,
  missing_fields  TEXT,

  state           TEXT NOT NULL DEFAULT 'ACTIVE',
  voided_at       TEXT,
  void_reason     TEXT,
  remark          TEXT,

  created_at      TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_forms_form_no ON forms (form_no);
CREATE INDEX ix_forms_state_created ON forms (state, created_at);
CREATE INDEX ix_forms_operation ON forms (operation_code, submitted_at);
CREATE INDEX ix_forms_sample_dim ON forms (sample_spec, sample_size, sample_side, sample_model);
CREATE INDEX ix_forms_project ON forms (sample_project);
CREATE INDEX ix_forms_complete ON forms (state, is_complete);
CREATE INDEX ix_forms_unit ON forms (unit_id);
CREATE INDEX ix_forms_submitted_by ON forms (submitted_by);

-- ============================ 明细行 ============================
-- 合计 3 列 + 不良缺陷明细（30 列 × 凹面/凸面/双面）+ 备注。
-- 列清单的唯一事实来源是 internal/formfields（新增缺陷列时同步加一次迁移即可）。

CREATE TABLE form_lines (
  id                      TEXT PRIMARY KEY,
  form_id                 TEXT NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  seq                     INTEGER NOT NULL DEFAULT 1,

  -- 检验总数 / 合格品 / 不合格品
  total_qty               INTEGER NOT NULL DEFAULT 0,
  qualified_qty           INTEGER NOT NULL DEFAULT 0,
  defect_qty              INTEGER NOT NULL DEFAULT 0,

-- 不良缺陷明细（35 列 × 凹面/凸面/双面 = 105 个子列）
  -- 1. 点伤
  prick_concave INTEGER NOT NULL DEFAULT 0,
  prick_convex INTEGER NOT NULL DEFAULT 0,
  prick_both INTEGER NOT NULL DEFAULT 0,
  -- 2. 划伤
  scratch_concave INTEGER NOT NULL DEFAULT 0,
  scratch_convex INTEGER NOT NULL DEFAULT 0,
  scratch_both INTEGER NOT NULL DEFAULT 0,
  -- 3. 法兰边点伤
  flange_prick_concave INTEGER NOT NULL DEFAULT 0,
  flange_prick_convex INTEGER NOT NULL DEFAULT 0,
  flange_prick_both INTEGER NOT NULL DEFAULT 0,
  -- 4. 法兰边划伤
  flange_scratch_concave INTEGER NOT NULL DEFAULT 0,
  flange_scratch_convex INTEGER NOT NULL DEFAULT 0,
  flange_scratch_both INTEGER NOT NULL DEFAULT 0,
  -- 5. 崩边
  collapse_edge_concave INTEGER NOT NULL DEFAULT 0,
  collapse_edge_convex INTEGER NOT NULL DEFAULT 0,
  collapse_edge_both INTEGER NOT NULL DEFAULT 0,
  -- 6. 涂层瑕疵
  coating_particle_concave INTEGER NOT NULL DEFAULT 0,
  coating_particle_convex INTEGER NOT NULL DEFAULT 0,
  coating_particle_both INTEGER NOT NULL DEFAULT 0,
  -- 7. 亮点
  bright_spot_concave INTEGER NOT NULL DEFAULT 0,
  bright_spot_convex INTEGER NOT NULL DEFAULT 0,
  bright_spot_both INTEGER NOT NULL DEFAULT 0,
  -- 8. 裂
  crack_concave INTEGER NOT NULL DEFAULT 0,
  crack_convex INTEGER NOT NULL DEFAULT 0,
  crack_both INTEGER NOT NULL DEFAULT 0,
  -- 9. 溢胶
  overflow_glue_concave INTEGER NOT NULL DEFAULT 0,
  overflow_glue_convex INTEGER NOT NULL DEFAULT 0,
  overflow_glue_both INTEGER NOT NULL DEFAULT 0,
  -- 10. 变色不良颗粒
  discolor_particle_concave INTEGER NOT NULL DEFAULT 0,
  discolor_particle_convex INTEGER NOT NULL DEFAULT 0,
  discolor_particle_both INTEGER NOT NULL DEFAULT 0,
  -- 11. 保护不良颗粒
  protective_particle_concave INTEGER NOT NULL DEFAULT 0,
  protective_particle_convex INTEGER NOT NULL DEFAULT 0,
  protective_particle_both INTEGER NOT NULL DEFAULT 0,
  -- 12. 旋涂颗粒
  spin_particle_concave INTEGER NOT NULL DEFAULT 0,
  spin_particle_convex INTEGER NOT NULL DEFAULT 0,
  spin_particle_both INTEGER NOT NULL DEFAULT 0,
  -- 13. 纤维
  fiber_concave INTEGER NOT NULL DEFAULT 0,
  fiber_convex INTEGER NOT NULL DEFAULT 0,
  fiber_both INTEGER NOT NULL DEFAULT 0,
  -- 14. 法兰边颗粒
  flange_particle_concave INTEGER NOT NULL DEFAULT 0,
  flange_particle_convex INTEGER NOT NULL DEFAULT 0,
  flange_particle_both INTEGER NOT NULL DEFAULT 0,
  -- 15. 加硬气泡
  hardening_bubble_concave INTEGER NOT NULL DEFAULT 0,
  hardening_bubble_convex INTEGER NOT NULL DEFAULT 0,
  hardening_bubble_both INTEGER NOT NULL DEFAULT 0,
  -- 16. 发丝裂
  hairline_crack_concave INTEGER NOT NULL DEFAULT 0,
  hairline_crack_convex INTEGER NOT NULL DEFAULT 0,
  hairline_crack_both INTEGER NOT NULL DEFAULT 0,
  -- 17. 脏污
  dirt_concave INTEGER NOT NULL DEFAULT 0,
  dirt_convex INTEGER NOT NULL DEFAULT 0,
  dirt_both INTEGER NOT NULL DEFAULT 0,
  -- 18. 旋痕
  spin_mark_concave INTEGER NOT NULL DEFAULT 0,
  spin_mark_convex INTEGER NOT NULL DEFAULT 0,
  spin_mark_both INTEGER NOT NULL DEFAULT 0,
  -- 19. 凸点
  raised_point_concave INTEGER NOT NULL DEFAULT 0,
  raised_point_convex INTEGER NOT NULL DEFAULT 0,
  raised_point_both INTEGER NOT NULL DEFAULT 0,
  -- 20. 凹坑
  recess_concave INTEGER NOT NULL DEFAULT 0,
  recess_convex INTEGER NOT NULL DEFAULT 0,
  recess_both INTEGER NOT NULL DEFAULT 0,
  -- 21. 气泡
  bubble_concave INTEGER NOT NULL DEFAULT 0,
  bubble_convex INTEGER NOT NULL DEFAULT 0,
  bubble_both INTEGER NOT NULL DEFAULT 0,
  -- 22. 面形
  shape_concave INTEGER NOT NULL DEFAULT 0,
  shape_convex INTEGER NOT NULL DEFAULT 0,
  shape_both INTEGER NOT NULL DEFAULT 0,
  -- 23. 水印
  watermark_concave INTEGER NOT NULL DEFAULT 0,
  watermark_convex INTEGER NOT NULL DEFAULT 0,
  watermark_both INTEGER NOT NULL DEFAULT 0,
  -- 24. 加硬点
  hard_spot_concave INTEGER NOT NULL DEFAULT 0,
  hard_spot_convex INTEGER NOT NULL DEFAULT 0,
  hard_spot_both INTEGER NOT NULL DEFAULT 0,
  -- 25. 法兰边圆点
  flange_round_point_concave INTEGER NOT NULL DEFAULT 0,
  flange_round_point_convex INTEGER NOT NULL DEFAULT 0,
  flange_round_point_both INTEGER NOT NULL DEFAULT 0,
  -- 26. 严重法兰边伤
  severe_flange_damage_concave INTEGER NOT NULL DEFAULT 0,
  severe_flange_damage_convex INTEGER NOT NULL DEFAULT 0,
  severe_flange_damage_both INTEGER NOT NULL DEFAULT 0,
  -- 27. 垃圾
  garbage_concave INTEGER NOT NULL DEFAULT 0,
  garbage_convex INTEGER NOT NULL DEFAULT 0,
  garbage_both INTEGER NOT NULL DEFAULT 0,
  -- 28. 法兰边伤
  flange_damage_concave INTEGER NOT NULL DEFAULT 0,
  flange_damage_convex INTEGER NOT NULL DEFAULT 0,
  flange_damage_both INTEGER NOT NULL DEFAULT 0,
  -- 29. 法兰边印痕
  flange_imprint_concave INTEGER NOT NULL DEFAULT 0,
  flange_imprint_convex INTEGER NOT NULL DEFAULT 0,
  flange_imprint_both INTEGER NOT NULL DEFAULT 0,
  -- 30. 压痕/法兰边压痕
  press_imprint_concave INTEGER NOT NULL DEFAULT 0,
  press_imprint_convex INTEGER NOT NULL DEFAULT 0,
  press_imprint_both INTEGER NOT NULL DEFAULT 0,
  -- 31. 流痕
  flow_mark_concave INTEGER NOT NULL DEFAULT 0,
  flow_mark_convex INTEGER NOT NULL DEFAULT 0,
  flow_mark_both INTEGER NOT NULL DEFAULT 0,
  -- 32. 镜内褶皱
  inner_fold_concave INTEGER NOT NULL DEFAULT 0,
  inner_fold_convex INTEGER NOT NULL DEFAULT 0,
  inner_fold_both INTEGER NOT NULL DEFAULT 0,
  -- 33. 褶皱/法兰边褶皱
  flange_fold_concave INTEGER NOT NULL DEFAULT 0,
  flange_fold_convex INTEGER NOT NULL DEFAULT 0,
  flange_fold_both INTEGER NOT NULL DEFAULT 0,
  -- 34. 内挂流/挂流
  inner_flow_glue_concave INTEGER NOT NULL DEFAULT 0,
  inner_flow_glue_convex INTEGER NOT NULL DEFAULT 0,
  inner_flow_glue_both INTEGER NOT NULL DEFAULT 0,
  -- 35. 法兰边凹痕
  flange_recess_concave INTEGER NOT NULL DEFAULT 0,
  flange_recess_convex INTEGER NOT NULL DEFAULT 0,
  flange_recess_both INTEGER NOT NULL DEFAULT 0,

  remark                  TEXT,
  created_at              TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at              TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX ux_form_lines_form_seq ON form_lines (form_id, seq);
CREATE INDEX ix_form_lines_form ON form_lines (form_id);

-- ============================ 历史快照（每次保存前的内容，追加写）============================

CREATE TABLE form_history (
  id                  TEXT PRIMARY KEY,
  form_id             TEXT NOT NULL REFERENCES forms (id) ON DELETE CASCADE,
  save_count          INTEGER NOT NULL,          -- 该快照对应的保存次数（被取代的那一版）
  submission_snapshot TEXT,                      -- 保存前的提交（JSON）
  lines_snapshot      TEXT,                      -- 保存前的明细（JSON 数组）
  saved_by            TEXT,
  saved_at            TEXT,
  changed_by          TEXT,
  changed_at          TEXT NOT NULL DEFAULT (datetime('now')),
  reason              TEXT
);
CREATE INDEX ix_form_history_form ON form_history (form_id, save_count);
CREATE INDEX ix_form_history_changed ON form_history (changed_at);
