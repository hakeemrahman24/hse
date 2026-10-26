-- Second rename pass: drops the old Django app-label prefix ("permits_")
-- now that the whole structure (Go struct, handlers, routes, templates) is
-- WorkPermitRegister-named throughout, not just the struct. Preserves rows.
--   mysql hse_register < migrations/005_rename_table_drop_prefix.sql

RENAME TABLE permits_workpermitregister TO workpermitregister;
