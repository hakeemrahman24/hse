-- HealthProcedureTable.TestLogKind: which test log (if any) a program's
-- activity-log page shows, e.g. 'alcohol' or 'fatigue' (see the logSheets
-- keys in internal/screening/testlog.go). NULL means no test log tab.
-- Replaces the old TEST_LOG_BY_PROGRAM name lookup that used to live in
-- web/static/app.js.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first. Re-running it is harmless (IF NOT EXISTS;
-- the two UPDATEs below are also safe to repeat).

ALTER TABLE HealthProcedureTable
    ADD COLUMN IF NOT EXISTS TestLogKind TEXT;

-- Backfill the two programs the old TEST_LOG_BY_PROGRAM map (formerly in
-- web/static/app.js) used to grant a test log tab by name, so this
-- migration doesn't silently drop those tabs in production. Matches
-- internal/activity-log/migrate.go's EnsureTestLogKindColumn (SQLite side).
UPDATE HealthProcedureTable SET TestLogKind = 'alcohol' WHERE HealthProcedure = 'DrugAndAlcoholTesting';
UPDATE HealthProcedureTable SET TestLogKind = 'fatigue' WHERE HealthProcedure = 'FatigueManagementProgram';
