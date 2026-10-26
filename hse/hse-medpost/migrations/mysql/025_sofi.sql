-- SofiScreening (MariaDB). Same shape as migrations/sqlite/015_sofi.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build that has the SOFI questionnaire (/sofi).

CREATE TABLE IF NOT EXISTS SofiScreening (
    SofiID          INT AUTO_INCREMENT PRIMARY KEY,
    FK_PatientID    INT NOT NULL,
    SubmittedAt     VARCHAR(32) NOT NULL,
    WeeksOnSite     INT NOT NULL,
    AgeYears        INT NULL,
    WorkPosition    VARCHAR(150) NOT NULL DEFAULT '',
    Company         VARCHAR(150) NOT NULL DEFAULT '',
    McuReviewStatus VARCHAR(40) NOT NULL DEFAULT '',
    Answers         VARCHAR(200) NOT NULL,
    ScoreEnergy     INT NOT NULL,
    ScoreExertion   INT NOT NULL,
    ScoreDiscomfort INT NOT NULL,
    ScoreMotivation INT NOT NULL,
    ScoreSleepiness INT NOT NULL,
    ScoreTotal      INT NOT NULL,
    ResultLevel     VARCHAR(16) NOT NULL,
    KEY idx_sofi_patient (FK_PatientID),
    KEY idx_sofi_submitted (SubmittedAt),
    CONSTRAINT fk_sofi_patient FOREIGN KEY (FK_PatientID) REFERENCES Patient(PatientID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
