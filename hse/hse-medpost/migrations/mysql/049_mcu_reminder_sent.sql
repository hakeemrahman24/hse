-- Expiry reminders are sent once per worker, per checkup and per stage (owner, 2026-11-13). See
-- migrations/sqlite/032 for what each column means.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs by
-- hand, once, against the production database. Take a mysqldump backup first. Run it BEFORE deploying
-- the build that reads it; until it exists the daily reminder job logs an error and mails nothing.
-- Safe to run twice (IF NOT EXISTS). NOT TESTED against a real MariaDB server.
CREATE TABLE IF NOT EXISTS McuReminderSent (
    FK_PatientID INT NOT NULL,
    ValidUntil   VARCHAR(10) NOT NULL,
    Stage        VARCHAR(20) NOT NULL,
    SentAt       VARCHAR(32) NOT NULL,
    PRIMARY KEY (FK_PatientID, ValidUntil, Stage),
    CONSTRAINT fk_reminder_patient FOREIGN KEY (FK_PatientID) REFERENCES Patient(PatientID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
