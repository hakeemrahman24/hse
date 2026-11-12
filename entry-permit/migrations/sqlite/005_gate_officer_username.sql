-- 005 gate officer username (SQLite). Keep in step with migrations/mysql/005_gate_officer_username.sql.
-- The gate admin gives each officer a username. Uniqueness (case-insensitive) is
-- checked in internal/gateofficer, because MariaDB has no partial unique index
-- and rows made before this migration have an empty username.
ALTER TABLE gate_officer ADD COLUMN username VARCHAR(40) NOT NULL DEFAULT '';
