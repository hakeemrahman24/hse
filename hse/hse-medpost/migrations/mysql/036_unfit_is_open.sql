-- Unfit is an open result (MariaDB). Same as migrations/sqlite/023_unfit_is_open.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice: the second run finds nothing to change.
--
-- Only Fit and Fit with note close a checkup. New and edited records already save that way; this
-- brings the Unfit records saved earlier into line. To see what it will change first:
--   SELECT ResultCriteria, CheckUpStatus, IsTerminal, COUNT(*) FROM McuRecord
--   WHERE ResultCriteria = 'Unfit' GROUP BY 1, 2, 3;
--
-- Run it BEFORE or right after deploying the build with the new rule; either order is fine.

UPDATE McuRecord SET CheckUpStatus = 'Open', IsTerminal = 0
WHERE ResultCriteria = 'Unfit' AND (CheckUpStatus IS NULL OR CheckUpStatus <> 'Open' OR IsTerminal <> 0);
