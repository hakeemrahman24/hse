-- Results uploaded from the offline Drug & Alcohol Consent form (MariaDB/MySQL).
-- Same shape as migrations/011_screening.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first.

CREATE TABLE IF NOT EXISTS DrugAlcoholSubmission (
    SubmissionID   INT AUTO_INCREMENT PRIMARY KEY,
    ClientID       VARCHAR(64) NOT NULL UNIQUE,
    FK_StaffID     INT NOT NULL,
    FormVersion    VARCHAR(16) NOT NULL,
    CreatedAt      VARCHAR(32) NOT NULL,
    SubmittedAt    VARCHAR(32) NOT NULL,
    DonorName      VARCHAR(500) NOT NULL DEFAULT '',
    DonorNik       VARCHAR(500) NOT NULL DEFAULT '',
    DonorCompany   VARCHAR(500) NOT NULL DEFAULT '',
    DonorPosition  VARCHAR(500) NOT NULL DEFAULT '',
    ConsentAt      VARCHAR(500) NOT NULL DEFAULT '',
    TestType       VARCHAR(500) NOT NULL DEFAULT '',
    Breath1Reading VARCHAR(500) NOT NULL DEFAULT '',
    Breath1Result  VARCHAR(500) NOT NULL DEFAULT '',
    Breath2Reading VARCHAR(500) NOT NULL DEFAULT '',
    Breath2Result  VARCHAR(500) NOT NULL DEFAULT '',
    FinalResult    VARCHAR(500) NOT NULL DEFAULT '',
    Remarks        VARCHAR(500) NOT NULL DEFAULT '',
    RawJSON        LONGTEXT NOT NULL,
    CONSTRAINT fk_drugalc_staff FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
);
