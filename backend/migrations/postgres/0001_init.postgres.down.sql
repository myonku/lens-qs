-- 0001_init.postgres.down.sql
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS record_history;
DROP TABLE IF EXISTS record_lines;
DROP TABLE IF EXISTS form_records;
DROP TABLE IF EXISTS forms;
DROP TABLE IF EXISTS sample_parse_cache;
DROP TABLE IF EXISTS sample_drawings;
DROP TABLE IF EXISTS dict_items;
DROP TABLE IF EXISTS units;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
