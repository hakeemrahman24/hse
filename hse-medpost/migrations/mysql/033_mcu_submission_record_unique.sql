-- A checkup record (McuRecord) can close only one portal submission (MariaDB). Same as
-- migrations/sqlite/022_mcu_submission_record_unique.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- It fails if two submissions already point at the same record. Check first:
--   SELECT FK_McuRecordID, COUNT(*) FROM McuSubmission
--   WHERE FK_McuRecordID IS NOT NULL GROUP BY FK_McuRecordID HAVING COUNT(*) > 1;
-- (no rows expected: the app has refused a second link since the Close action was built).
--
-- Run it BEFORE deploying the build that handles the refusal.

CREATE UNIQUE INDEX IF NOT EXISTS uq_mcusub_mcurecord ON McuSubmission (FK_McuRecordID);
