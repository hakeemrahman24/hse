-- Evidence files now live in folders with readable names, e.g.
--   2026/10/OBS-000123/OBS-000123_before_photo_01.jpg
-- so the database keeps a relative path, which does not fit in 64 characters.
--   mysql hse_register < migrations/011_evidence_paths.sql
-- Rows with an old random name keep working; "hse-register-api -organize-evidence" moves those files.

ALTER TABLE observation_files MODIFY stored_name VARCHAR(255) NOT NULL;
