-- Corrective action tracking (MariaDB). Same shape as migrations/sqlite/028_corrective_action.sql; see
-- plan/corrective-action-plan.md.
--
-- Not loaded by cmd/server: run it by hand after a mysqldump backup. Safe to run twice (IF NOT EXISTS).
-- Needs HealthProcedureTable, ActivityTable and MedpostStaffTable, which already exist.
--
-- Dates are VARCHAR(10) 'YYYY-MM-DD' and timestamps VARCHAR(19) 'YYYY-MM-DDTHH:MM:SS' (UTC), never
-- DATE/DATETIME, to avoid timezone shifts. PicName/PicEmployeeID are a text copy of the picked employee,
-- not a foreign key.

CREATE TABLE IF NOT EXISTS CorrectiveActionTable (
    ActionID        INT AUTO_INCREMENT PRIMARY KEY,
    FK_ProcID       INT NOT NULL,
    FK_ActivityID   INT NULL,
    InspectionDate  VARCHAR(10) NOT NULL,
    Location        VARCHAR(255) NOT NULL DEFAULT '',
    Finding         TEXT NOT NULL,
    Recommendation  TEXT NOT NULL,
    PicName         VARCHAR(255) NOT NULL,
    PicEmployeeID   VARCHAR(100) NOT NULL DEFAULT '',
    DueDate         VARCHAR(10) NULL,
    Severity        VARCHAR(10) NOT NULL DEFAULT 'Minor',
    Status          VARCHAR(12) NOT NULL DEFAULT 'Open',
    ClosingDate     VARCHAR(10) NULL,
    ActionTaken     TEXT NOT NULL,
    FK_CreatedBy    INT NOT NULL,
    CreatedAt       VARCHAR(19) NOT NULL,
    FK_ClosedBy     INT NULL,
    ClosedAt        VARCHAR(19) NULL,
    CONSTRAINT chk_ca_severity CHECK (Severity IN ('Minor', 'Major', 'Critical')),
    CONSTRAINT chk_ca_status CHECK (Status IN ('Open', 'In Progress', 'Closed')),
    CONSTRAINT fk_ca_proc FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable (ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT fk_ca_activity FOREIGN KEY (FK_ActivityID) REFERENCES ActivityTable (ActivityID)
        ON DELETE SET NULL,
    CONSTRAINT fk_ca_created FOREIGN KEY (FK_CreatedBy) REFERENCES MedpostStaffTable (StaffID),
    CONSTRAINT fk_ca_closed FOREIGN KEY (FK_ClosedBy) REFERENCES MedpostStaffTable (StaffID),
    INDEX idx_corrective_action_proc (FK_ProcID, InspectionDate),
    INDEX idx_corrective_action_status (Status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS CorrectiveActionPhotoTable (
    PhotoID      INT AUTO_INCREMENT PRIMARY KEY,
    FK_ActionID  INT NOT NULL,
    Phase        VARCHAR(6) NOT NULL,
    FileLink     VARCHAR(500) NOT NULL,
    SortOrder    INT NOT NULL DEFAULT 1,
    UploadedBy   INT NULL,
    UploadedAt   VARCHAR(19) NOT NULL,
    CONSTRAINT chk_cap_phase CHECK (Phase IN ('Before', 'After')),
    CONSTRAINT fk_cap_action FOREIGN KEY (FK_ActionID) REFERENCES CorrectiveActionTable (ActionID)
        ON DELETE CASCADE,
    CONSTRAINT fk_cap_staff FOREIGN KEY (UploadedBy) REFERENCES MedpostStaffTable (StaffID),
    INDEX idx_corrective_photo_action (FK_ActionID, Phase)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS CorrectiveActionFollowUpTable (
    FollowUpID     INT AUTO_INCREMENT PRIMARY KEY,
    FK_ActionID    INT NOT NULL,
    Description    TEXT NOT NULL,
    PicName        VARCHAR(255) NOT NULL,
    PicEmployeeID  VARCHAR(100) NOT NULL DEFAULT '',
    DueDate        VARCHAR(10) NULL,
    Status         VARCHAR(4) NOT NULL DEFAULT 'Open',
    DoneDate       VARCHAR(10) NULL,
    ActionTaken    TEXT NOT NULL,
    FK_CreatedBy   INT NOT NULL,
    CreatedAt      VARCHAR(19) NOT NULL,
    FK_DoneBy      INT NULL,
    DoneAt         VARCHAR(19) NULL,
    CONSTRAINT chk_caf_status CHECK (Status IN ('Open', 'Done')),
    CONSTRAINT fk_caf_action FOREIGN KEY (FK_ActionID) REFERENCES CorrectiveActionTable (ActionID)
        ON DELETE CASCADE,
    CONSTRAINT fk_caf_created FOREIGN KEY (FK_CreatedBy) REFERENCES MedpostStaffTable (StaffID),
    CONSTRAINT fk_caf_done FOREIGN KEY (FK_DoneBy) REFERENCES MedpostStaffTable (StaffID),
    INDEX idx_corrective_followup_action (FK_ActionID, Status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS CorrectiveActionHistoryTable (
    HistoryID    INT AUTO_INCREMENT PRIMARY KEY,
    FK_ActionID  INT NOT NULL,
    FK_StaffID   INT NULL,
    ChangedAt    VARCHAR(19) NOT NULL,
    Field        VARCHAR(40) NOT NULL,
    OldValue     TEXT NOT NULL,
    NewValue     TEXT NOT NULL,
    CONSTRAINT fk_cah_action FOREIGN KEY (FK_ActionID) REFERENCES CorrectiveActionTable (ActionID)
        ON DELETE CASCADE,
    CONSTRAINT fk_cah_staff FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable (StaffID),
    INDEX idx_corrective_history_action (FK_ActionID, HistoryID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
