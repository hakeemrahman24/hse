-- Batches and submissions of the medical checkup portal. Design: plan/mcu-submission-plan.md
-- sections 3, 5 and 7. Accounts are in 016_mcu_submission.sql.
--
-- A submission is one PDF about one worker. It is a Draft inside an open batch until the
-- submitter presses Send, which sets McuBatch.SentAt and moves every document to Pending.
-- Status and ScanStatus values are checked in Go (internal/mcusubmit), not with CHECK/ENUM, so
-- the file stays valid on both SQLite and MariaDB. Timestamps are UTC, YYYY-MM-DDTHH:MM:SS.
--
-- Patient is never written by a submitter: FK_PatientID is set on a match or by staff on approval,
-- and Pat* columns keep what the submitter typed.

CREATE TABLE IF NOT EXISTS McuBatch (
    BatchID             INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_SubmitterID      INTEGER NOT NULL REFERENCES Submitter(SubmitterID),
    CreatedAt           TEXT NOT NULL,
    SentAt              TEXT NULL,                        -- NULL = still a draft; set only by Send
    ReviewersNotifiedAt TEXT NULL,
    SubmitterNotifiedAt TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcubatch_submitter ON McuBatch(FK_SubmitterID);

CREATE TABLE IF NOT EXISTS McuSubmission (
    SubmissionID        INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_SubmitterID      INTEGER NOT NULL REFERENCES Submitter(SubmitterID),
    FK_BatchID          INTEGER NOT NULL REFERENCES McuBatch(BatchID),
    FK_PatientID        INTEGER NULL REFERENCES Patient(PatientID),
    PatName             TEXT NOT NULL,
    PatDateOfBirth      TEXT NOT NULL,
    PatPosition         TEXT NOT NULL DEFAULT '',
    PatCompanyText      TEXT NOT NULL DEFAULT '',
    PatNationality      TEXT NOT NULL DEFAULT '',
    PatNik              TEXT NULL,
    PatPassportNo       TEXT NULL,
    FK_CompanyID        INTEGER NULL REFERENCES Company(CompanyID),
    IsNewEmployee       INTEGER NOT NULL DEFAULT 0,
    FilePath            TEXT NULL,                        -- NULL once deleted (withdrawn or expired)
    FileSha256          TEXT NULL,
    FileBytes           INTEGER NULL,
    FileExpiresAt       TEXT NULL,
    ScanStatus          TEXT NOT NULL DEFAULT 'Scanning', -- Scanning | Clean | ScanError (Infected rows are deleted)
    Status              TEXT NOT NULL DEFAULT 'Draft',    -- Draft | Review | Pending | Reviewed | Closed | Rejected | Withdrawn (Review = uploaded, not yet confirmed by the submitter)
    RejectReason        TEXT NULL,
    WithdrawnAt         TEXT NULL,
    SubmittedAt         TEXT NOT NULL,
    ReviewedAt          TEXT NULL,
    FK_ReviewerStaffID  INTEGER NULL REFERENCES MedpostStaffTable(StaffID),
    FK_McuRecordID      INTEGER NULL REFERENCES McuRecord(McuRecordID)
);
CREATE INDEX IF NOT EXISTS idx_mcusub_submitter ON McuSubmission(FK_SubmitterID, SubmissionID);
CREATE INDEX IF NOT EXISTS idx_mcusub_batch ON McuSubmission(FK_BatchID);
CREATE INDEX IF NOT EXISTS idx_mcusub_status ON McuSubmission(Status);
