-- Accounts the admin adds by hand (owner, 2026-11-13) get the default password and must replace
-- it at the first sign-in. This flag is what the portal checks on every request. SQLite gets the column
-- from mcusubmit.EnsureMustChangePasswordColumn at startup.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs by
-- hand, once, against the production database. Take a mysqldump backup first. Run it BEFORE deploying
-- the build that reads the column: until it exists, every portal page logs an error and a password
-- change fails. Running it twice fails on the second ALTER (the column already exists), which is safe.
-- NOT TESTED against a real MariaDB server.

ALTER TABLE Submitter ADD COLUMN MustChangePassword TINYINT NOT NULL DEFAULT 0;
