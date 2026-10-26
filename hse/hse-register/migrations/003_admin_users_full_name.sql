-- Adds a display name to admin_users, separate from the login username.
--   mysql hse_register < migrations/003_admin_users_full_name.sql

ALTER TABLE admin_users
  ADD COLUMN full_name VARCHAR(255) NOT NULL DEFAULT '' AFTER username;

UPDATE admin_users SET full_name = 'Administrator' WHERE username = 'admin';
