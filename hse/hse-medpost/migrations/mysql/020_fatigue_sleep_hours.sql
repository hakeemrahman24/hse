-- Adds the raw sleep and wake hours to the fatigue results (MariaDB/MySQL).
--
-- The Fatigue Scoring form has always uploaded the hours it scores from
-- (sleep_24h = X, sleep_48h = Y, awake_hours = Z), but the table had no columns
-- for them, so they survived only inside RawJSON. Score24h, Score48h and
-- ScoreAwake are the POINTS computed from these hours, not the hours.
-- Same shape as the other result columns (VARCHAR(500), blank by default);
-- plan/type-conversion-plan.md converts them to proper types later, together
-- with the Score columns.
--
-- Run 019_rename_screening_tables.sql first: this alters FatigueScreening.
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump first. Safe to repeat (ADD COLUMN IF NOT EXISTS).
--
-- Rows uploaded before this runs keep blank hours here; the values are still in
-- their RawJSON if ever needed.

ALTER TABLE FatigueScreening
    ADD COLUMN IF NOT EXISTS Sleep24h   VARCHAR(500) NOT NULL DEFAULT '' AFTER Pulse,
    ADD COLUMN IF NOT EXISTS Sleep48h   VARCHAR(500) NOT NULL DEFAULT '' AFTER Sleep24h,
    ADD COLUMN IF NOT EXISTS AwakeHours VARCHAR(500) NOT NULL DEFAULT '' AFTER Sleep48h;
