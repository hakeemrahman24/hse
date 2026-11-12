-- 017 a short line saying what each job run did ("3 changed, 1 skipped", or why it failed),
-- so the admin status page can show more than a result word.
ALTER TABLE job_run ADD COLUMN detail VARCHAR(200) NOT NULL DEFAULT '';
