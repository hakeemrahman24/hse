-- The portal now asks the submitter for a new employee's gender (MariaDB). SQLite gets the same
-- column from migrations/sqlite/017 (new databases) or mcusubmit.EnsureGenderColumn (older ones).
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Values are 'Male' or 'Female', the same spelling as Patient.Gender; NULL for documents sent before
-- this column existed (the staff form then leaves the gender for the reviewer to pick).
--
-- Run it BEFORE deploying the build that has the Gender field.

ALTER TABLE McuSubmission ADD COLUMN IF NOT EXISTS PatGender VARCHAR(10) NULL;
