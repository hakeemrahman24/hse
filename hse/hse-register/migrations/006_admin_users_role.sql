-- Adds a role to the web accounts: 'user' sees the three module cards, 'admin' also sees Settings.
--   mysql hse_register < migrations/006_admin_users_role.sql
--
-- WHY every existing row becomes 'admin': before this migration every account could manage
-- users, so demoting them silently would lock the current people out of Settings.
-- New accounts default to 'user' (least privilege).

ALTER TABLE admin_users
  ADD COLUMN role ENUM('user','admin') NOT NULL DEFAULT 'user' AFTER full_name;

UPDATE admin_users SET role = 'admin';
