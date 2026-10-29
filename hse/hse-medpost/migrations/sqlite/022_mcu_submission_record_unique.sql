-- A checkup record (McuRecord) can close only one portal submission. Until now only a read before
-- the write stopped two staff members from linking the same record in the same instant; this
-- makes the database refuse the second one. Many rows may have no record (NULL), and both SQLite
-- and MariaDB allow several NULLs in a unique index.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mcusub_mcurecord ON McuSubmission(FK_McuRecordID);
