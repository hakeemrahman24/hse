-- 005 gate officer username (MariaDB). Keep in step with migrations/sqlite/005_gate_officer_username.sql.
ALTER TABLE gate_officer ADD COLUMN username VARCHAR(40) NOT NULL DEFAULT '';
