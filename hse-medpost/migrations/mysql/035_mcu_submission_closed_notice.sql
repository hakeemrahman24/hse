-- Remembers which Closed documents the daily "your medical checkups are completed" mail has
-- already covered (MariaDB). SQLite gets the same column from migrations/sqlite/017 (new databases)
-- or mcusubmit.EnsureNoticeColumn (older ones).
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- The UPDATE marks documents that are already Closed as told, so switching the mail on does not
-- announce the past. On a second run it only touches rows that are still unmarked.
--
-- Run it BEFORE deploying the build that sends the mail.

ALTER TABLE McuSubmission ADD COLUMN IF NOT EXISTS CertNotifiedAt VARCHAR(32) NULL;

UPDATE McuSubmission SET CertNotifiedAt = COALESCE(ReviewedAt, SubmittedAt)
WHERE Status = 'Closed' AND CertNotifiedAt IS NULL;
