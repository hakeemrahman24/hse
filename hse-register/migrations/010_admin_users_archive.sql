-- Lets an administrator archive an account (no sign-in, nothing deleted, can be restored) and
-- reset a password to the default (the person must choose a new one at the next sign-in).
--   mysql hse_register < migrations/010_admin_users_archive.sql

ALTER TABLE admin_users
  ADD COLUMN archived_at DATETIME NULL,
  ADD COLUMN must_change_password TINYINT(1) NOT NULL DEFAULT 0;
