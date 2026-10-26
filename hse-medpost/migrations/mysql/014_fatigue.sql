-- Results uploaded from the offline Fatigue Scoring form (MariaDB/MySQL).
-- Same shape as migrations/012_fatigue.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first.

CREATE TABLE IF NOT EXISTS FatigueSubmission (
    SubmissionID   INT AUTO_INCREMENT PRIMARY KEY,
    ClientID       VARCHAR(64) NOT NULL UNIQUE,
    FK_StaffID     INT NOT NULL,
    FormVersion    VARCHAR(16) NOT NULL,
    CreatedAt      VARCHAR(32) NOT NULL,
    SubmittedAt    VARCHAR(32) NOT NULL,
    Examiner       VARCHAR(500) NOT NULL DEFAULT '',
    WorkerName     VARCHAR(500) NOT NULL DEFAULT '',
    WorkerCompany  VARCHAR(500) NOT NULL DEFAULT '',
    WorkerPosition VARCHAR(500) NOT NULL DEFAULT '',
    ExamDate       VARCHAR(500) NOT NULL DEFAULT '',
    ExamTime       VARCHAR(500) NOT NULL DEFAULT '',
    BpSystolic     VARCHAR(500) NOT NULL DEFAULT '',
    BpDiastolic    VARCHAR(500) NOT NULL DEFAULT '',
    Pulse          VARCHAR(500) NOT NULL DEFAULT '',
    Score24h       VARCHAR(500) NOT NULL DEFAULT '',
    Score48h       VARCHAR(500) NOT NULL DEFAULT '',
    ScoreAwake     VARCHAR(500) NOT NULL DEFAULT '',
    ScoreTotal     VARCHAR(500) NOT NULL DEFAULT '',
    RiskLevel      VARCHAR(500) NOT NULL DEFAULT '',
    RiskPoints     VARCHAR(500) NOT NULL DEFAULT '',
    Remarks        VARCHAR(500) NOT NULL DEFAULT '',
    RawJSON        LONGTEXT NOT NULL,
    CONSTRAINT fk_fatigue_staff FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
);
