-- Moves blood type from Patient to McuRecord (MariaDB/MySQL).
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first.
--
-- Equivalent to entrymcu.EnsureBloodTypeColumn for SQLite. Each patient's
-- existing Patient.BloodType is copied onto their most recent checkup only.
-- Patient.BloodType is left in place; drop it in a later migration after the
-- new code has run in production.

ALTER TABLE McuRecord ADD COLUMN BloodType VARCHAR(10) NULL;

UPDATE McuRecord m
JOIN (
  SELECT McuRecordID, FK_PatientID,
         ROW_NUMBER() OVER (PARTITION BY FK_PatientID
                            ORDER BY ReviewDate DESC, Timestamp DESC, McuRecordID DESC) AS rn
  FROM McuRecord
) latest ON latest.McuRecordID = m.McuRecordID AND latest.rn = 1
JOIN Patient p ON p.PatientID = m.FK_PatientID
SET m.BloodType = p.BloodType
WHERE TRIM(COALESCE(p.BloodType, '')) <> '';

-- Check: should equal the number of patients that had a blood type before.
-- SELECT COUNT(DISTINCT FK_PatientID) FROM McuRecord WHERE BloodType IS NOT NULL;
