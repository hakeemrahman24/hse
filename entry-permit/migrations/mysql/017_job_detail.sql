-- 017 job run detail (MariaDB). Keep in step with migrations/sqlite/017_job_detail.sql.
-- Run by hand after a mysqldump.
ALTER TABLE job_run ADD COLUMN detail VARCHAR(200) NOT NULL DEFAULT '';
