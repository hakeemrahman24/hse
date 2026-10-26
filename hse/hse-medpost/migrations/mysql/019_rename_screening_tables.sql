-- Renames the two screening result tables (MariaDB/MySQL):
--
--   DrugAlcoholSubmission -> DrugAlcoholScreening
--   FatigueSubmission     -> FatigueScreening
--
-- "Screening" says what a row is, one screening test, and not how it arrived:
-- the same table takes an offline form uploaded from a worksite and, later, an
-- entry made in the clinic. See plan/emr-plan.md section 2.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database,
-- BEFORE deploying a build that uses the new names. Take a mysqldump first.
-- Not safe to repeat: the second run fails because the old names are gone.
--
-- Run it on a restored copy first. ActivityTable.FK_SubmissionID keeps its name
-- and its constraint (fk_activity_submission) and should follow the parent
-- table to its new name; the check below proves it before you rely on it.
--
-- Rollback (nothing else has changed yet):
--   RENAME TABLE DrugAlcoholScreening TO DrugAlcoholSubmission,
--                FatigueScreening TO FatigueSubmission;

RENAME TABLE DrugAlcoholSubmission TO DrugAlcoholScreening,
             FatigueSubmission TO FatigueScreening;

-- Check: expect fk_activity_submission -> DrugAlcoholScreening.
SELECT CONSTRAINT_NAME, TABLE_NAME, REFERENCED_TABLE_NAME
FROM information_schema.REFERENTIAL_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE()
  AND REFERENCED_TABLE_NAME IN ('DrugAlcoholScreening', 'FatigueScreening');
