-- Batches and submissions of the medical checkup portal (MariaDB). Same shape as
-- migrations/sqlite/017_mcu_submission_batches.sql. Needs 026_mcu_submission.sql first.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build that has batch upload.

CREATE TABLE IF NOT EXISTS McuBatch (
    BatchID             INT AUTO_INCREMENT PRIMARY KEY,
    FK_SubmitterID      INT NOT NULL,
    CreatedAt           VARCHAR(32) NOT NULL,
    SentAt              VARCHAR(32) NULL,
    ReviewersNotifiedAt VARCHAR(32) NULL,
    SubmitterNotifiedAt VARCHAR(32) NULL,
    KEY idx_mcubatch_submitter (FK_SubmitterID),
    CONSTRAINT fk_mcubatch_submitter FOREIGN KEY (FK_SubmitterID) REFERENCES Submitter(SubmitterID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS McuSubmission (
    SubmissionID        INT AUTO_INCREMENT PRIMARY KEY,
    FK_SubmitterID      INT NOT NULL,
    FK_BatchID          INT NOT NULL,
    FK_PatientID        INT NULL,
    PatName             VARCHAR(150) NOT NULL,
    PatDateOfBirth      VARCHAR(10) NOT NULL,
    PatPosition         VARCHAR(128) NOT NULL DEFAULT '',
    PatCompanyText      VARCHAR(150) NOT NULL DEFAULT '',
    PatNationality      VARCHAR(60) NOT NULL DEFAULT '',
    PatNik              VARCHAR(16) NULL,
    PatPassportNo       VARCHAR(12) NULL,
    FK_CompanyID        INT NULL,
    IsNewEmployee       TINYINT(1) NOT NULL DEFAULT 0,
    FilePath            VARCHAR(255) NULL,
    FileSha256          CHAR(64) NULL,
    FileBytes           INT NULL,
    FileExpiresAt       VARCHAR(32) NULL,
    ScanStatus          VARCHAR(10) NOT NULL DEFAULT 'Scanning',
    Status              VARCHAR(10) NOT NULL DEFAULT 'Draft',
    RejectReason        VARCHAR(255) NULL,
    WithdrawnAt         VARCHAR(32) NULL,
    SubmittedAt         VARCHAR(32) NOT NULL,
    ReviewedAt          VARCHAR(32) NULL,
    FK_ReviewerStaffID  INT NULL,
    FK_McuRecordID      INT NULL,
    KEY idx_mcusub_submitter (FK_SubmitterID, SubmissionID),
    KEY idx_mcusub_batch (FK_BatchID),
    KEY idx_mcusub_status (Status),
    CONSTRAINT fk_mcusub_submitter FOREIGN KEY (FK_SubmitterID) REFERENCES Submitter(SubmitterID),
    CONSTRAINT fk_mcusub_batch FOREIGN KEY (FK_BatchID) REFERENCES McuBatch(BatchID),
    CONSTRAINT fk_mcusub_patient FOREIGN KEY (FK_PatientID) REFERENCES Patient(PatientID),
    CONSTRAINT fk_mcusub_company FOREIGN KEY (FK_CompanyID) REFERENCES Company(CompanyID),
    CONSTRAINT fk_mcusub_reviewer FOREIGN KEY (FK_ReviewerStaffID) REFERENCES MedpostStaffTable(StaffID),
    CONSTRAINT fk_mcusub_mcurecord FOREIGN KEY (FK_McuRecordID) REFERENCES McuRecord(McuRecordID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
