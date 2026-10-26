-- HealthProcedureTable.TestLogTitle: an optional custom label for a
-- program's "Test log" tab button, overriding the default label for its
-- TestLogKind (e.g. "Alcohol test log") without changing which underlying
-- data (DrugAlcoholSubmission vs FatigueSubmission) the tab reads. NULL or
-- empty falls back to the kind's default label (see
-- internal/screening/testlog.go's KindLabel).
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first. Re-running it is harmless (IF NOT EXISTS).

ALTER TABLE HealthProcedureTable
    ADD COLUMN IF NOT EXISTS TestLogTitle TEXT;
