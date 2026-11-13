-- Patient.HeightCm: the newest height recorded on any of the patient's
-- checkups, kept on the patient so the next checkup can start from it. Same
-- derived-copy idea as Patient.BloodType (which already exists). SQLite gets
-- the column from entrymcu.EnsurePatientHeightColumn at startup.
-- Run by hand after a mysqldump. The app fills both columns from the latest
-- checkup at startup (db.SyncAllPatientsFromLatestVisit), so no backfill here.
ALTER TABLE Patient ADD COLUMN HeightCm DOUBLE NULL;
