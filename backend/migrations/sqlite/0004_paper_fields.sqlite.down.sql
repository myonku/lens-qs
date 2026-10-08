-- 0004_paper_fields.sqlite.down.sql
-- 回滚：SQLite 3.35+ 支持 DROP COLUMN；现代c 驱动自带较新版本的 SQLite。

-- 先删本迁移新建的索引，再删列（SQLite 不允许删被索引引用的列）
DROP INDEX IF EXISTS ix_forms_paper_inspector;
DROP INDEX IF EXISTS ix_forms_paper_submitter;
DROP INDEX IF EXISTS ix_forms_paper_unit;

ALTER TABLE forms DROP COLUMN paper_unit_id;
ALTER TABLE forms DROP COLUMN paper_unit_text;
ALTER TABLE forms DROP COLUMN paper_submitter;
ALTER TABLE forms DROP COLUMN paper_submitted_at;
ALTER TABLE forms DROP COLUMN paper_inspector;
ALTER TABLE forms DROP COLUMN paper_inspected_at;
ALTER TABLE forms DROP COLUMN paper_machine_operator;
ALTER TABLE forms DROP COLUMN paper_handover_by;
ALTER TABLE forms DROP COLUMN created_by;

-- 恢复 0003 里的 unit_id / unit_text（回滚到 0003 的状态）
ALTER TABLE forms ADD COLUMN unit_id text;
ALTER TABLE forms ADD COLUMN unit_text text;
CREATE INDEX IF NOT EXISTS ix_forms_unit ON forms (unit_id);
