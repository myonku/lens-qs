-- 0004_paper_fields.postgres.down.sql
-- 回滚：移除纸质表头栏目。

DROP INDEX IF EXISTS ix_forms_paper_inspector;
DROP INDEX IF EXISTS ix_forms_paper_submitter;
DROP INDEX IF EXISTS ix_forms_paper_unit;

ALTER TABLE forms DROP COLUMN IF EXISTS paper_unit_id;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_unit_text;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_submitter;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_submitted_at;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_inspector;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_inspected_at;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_machine_operator;
ALTER TABLE forms DROP COLUMN IF EXISTS paper_handover_by;
ALTER TABLE forms DROP COLUMN IF EXISTS created_by;

-- 恢复 0003 里的 unit_id / unit_text
ALTER TABLE forms ADD COLUMN IF NOT EXISTS unit_id text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS unit_text text;
CREATE INDEX IF NOT EXISTS ix_forms_unit ON forms (unit_id);
