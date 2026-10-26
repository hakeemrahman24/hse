-- Renames the WorkPermit table to match the struct rename (WorkPermit ->
-- WorkPermitRegister) and the project's own name. Preserves existing rows.
--   mysql hse_register < migrations/004_rename_permits_table.sql

RENAME TABLE permits_workpermit TO permits_workpermitregister;
