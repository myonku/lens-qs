-- 0004_paper_fields.postgres.up.sql
-- 恢复纸质表单原有的表头栏目（0003 重构时被误删）。
--
-- 关键原则：**纸质栏目与系统留痕并存，且不混用**
--   paper_*     纸质表单上手写的值（单位、送检人、送检时间、检验人、检验时间、操机员、交接员）
--   submitted_* 实际录入的系统用户与时间（追溯用，与纸面人名无关，B-17）
--   created_* / last_saved_*  系统留痕

ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_unit_id        text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_unit_text      text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_submitter      text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_submitted_at   text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_inspector      text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_inspected_at   text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_machine_operator text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS paper_handover_by    text;
ALTER TABLE forms ADD COLUMN IF NOT EXISTS created_by           text;

CREATE INDEX IF NOT EXISTS ix_forms_paper_inspector ON forms (paper_inspector);
CREATE INDEX IF NOT EXISTS ix_forms_paper_submitter ON forms (paper_submitter);
CREATE INDEX IF NOT EXISTS ix_forms_paper_unit      ON forms (paper_unit_id);

-- 清掉 0003 里的 unit_id / unit_text：单位已统一为 paper_unit_*，
-- 留着两套列名容易再次出现"纸面栏目与系统字段混用"的问题。
DROP INDEX IF EXISTS ix_forms_unit;
ALTER TABLE forms DROP COLUMN IF EXISTS unit_id;
ALTER TABLE forms DROP COLUMN IF EXISTS unit_text;
