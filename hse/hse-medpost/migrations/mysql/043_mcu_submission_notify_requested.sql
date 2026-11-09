-- When the reviewer told the submitter about an IncompleteResult (MariaDB). The document then stays
-- Reviewed (open) instead of Closed, because the checkup itself is still open. SQLite gets the same
-- column from mcusubmit.EnsureNotifyRequestedColumn at startup.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build that reads the column.

ALTER TABLE McuSubmission ADD COLUMN IF NOT EXISTS NotifyRequestedAt VARCHAR(32) NULL;
