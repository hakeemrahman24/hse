-- Drops Django's bookkeeping tables and the four empty usersettings stub
-- tables, now that Django has been removed from this project. Keeps
-- permits_workpermit and observations_observation (and their data) as-is --
-- only Django's own machinery and never-used stubs are dropped.
--
-- Run by hand, once, AFTER migrations/001_admin_users.sql and after
-- confirming the new Go admin works, since this is irreversible:
--   mysql hse_register < migrations/002_drop_django_tables.sql

DROP TABLE IF EXISTS usersettings_notification;
DROP TABLE IF EXISTS usersettings_userpreference;
DROP TABLE IF EXISTS usersettings_userrole;
DROP TABLE IF EXISTS usersettings_role;
DROP TABLE IF EXISTS django_admin_log;
DROP TABLE IF EXISTS django_session;
DROP TABLE IF EXISTS django_migrations;
DROP TABLE IF EXISTS auth_user_user_permissions;
DROP TABLE IF EXISTS auth_user_groups;
DROP TABLE IF EXISTS auth_user;
DROP TABLE IF EXISTS auth_group_permissions;
DROP TABLE IF EXISTS auth_permission;
DROP TABLE IF EXISTS auth_group;
DROP TABLE IF EXISTS django_content_type;
