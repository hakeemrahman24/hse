-- Expiry reminders are sent once per worker, per checkup and per stage (owner, 2026-11-13): within 60
-- days, within 30 days, within 2 weeks, expired. One row says "this worker was already mailed about this
-- checkup at this stage". The checkup is identified by its end date (ValidUntil): a renewal has a new
-- end date, so its stages start again. MariaDB gets the same table from migrations/mysql/049.
CREATE TABLE IF NOT EXISTS McuReminderSent (
    FK_PatientID INTEGER NOT NULL REFERENCES Patient(PatientID),
    ValidUntil   TEXT NOT NULL,   -- YYYY-MM-DD, the checkup's ResultValidUntil
    Stage        TEXT NOT NULL,   -- Within 60 days | Within 30 days | Within 2 weeks | Expired
    SentAt       TEXT NOT NULL,
    PRIMARY KEY (FK_PatientID, ValidUntil, Stage)
);
