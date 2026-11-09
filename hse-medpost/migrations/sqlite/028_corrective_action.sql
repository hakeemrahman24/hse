-- Corrective action tracking (plan/corrective-action-plan.md): findings from inspections across every
-- health program, each with before/after photos and an edit history.
--
-- Calendar dates are TEXT 'YYYY-MM-DD'; timestamps are TEXT 'YYYY-MM-DDTHH:MM:SS' in UTC (dates.NowStamp).
-- PicName / PicEmployeeID are a TEXT COPY of the employee picked from the Patient list, not a foreign key
-- (decided): a later rename or removal of the employee must not rewrite an old finding. The server fills
-- them from Patient itself, so only a listed employee can be stored.
-- "Overdue" and "days open" are not columns: both are computed from DueDate / dates and today's date.
CREATE TABLE IF NOT EXISTS CorrectiveActionTable (
    ActionID        INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ProcID       INTEGER NOT NULL REFERENCES HealthProcedureTable(ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    FK_ActivityID   INTEGER REFERENCES ActivityTable(ActivityID) ON DELETE SET NULL,
    InspectionDate  TEXT NOT NULL CHECK (length(InspectionDate) = 10),
    Location        TEXT NOT NULL DEFAULT '',
    Finding         TEXT NOT NULL,
    Recommendation  TEXT NOT NULL,
    PicName         TEXT NOT NULL,
    PicEmployeeID   TEXT NOT NULL DEFAULT '',
    DueDate         TEXT CHECK (DueDate IS NULL OR length(DueDate) = 10),
    Severity        TEXT NOT NULL DEFAULT 'Minor'
        CHECK (Severity IN ('Minor', 'Major', 'Critical')),
    Status          TEXT NOT NULL DEFAULT 'Open'
        CHECK (Status IN ('Open', 'In Progress', 'Closed')),
    ClosingDate     TEXT CHECK (ClosingDate IS NULL OR length(ClosingDate) = 10),
    ActionTaken     TEXT NOT NULL DEFAULT '',
    FK_CreatedBy    INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID),
    CreatedAt       TEXT NOT NULL,
    FK_ClosedBy     INTEGER REFERENCES MedpostStaffTable(StaffID),
    ClosedAt        TEXT
);

CREATE INDEX IF NOT EXISTS idx_corrective_action_proc ON CorrectiveActionTable (FK_ProcID, InspectionDate);
CREATE INDEX IF NOT EXISTS idx_corrective_action_status ON CorrectiveActionTable (Status);

-- Up to 3 photos per finding and phase; the limit is enforced in code (internal/correctiveaction) inside a
-- transaction. FileLink is the stored path, like EvidenceTable.FileLink.
CREATE TABLE IF NOT EXISTS CorrectiveActionPhotoTable (
    PhotoID      INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ActionID  INTEGER NOT NULL REFERENCES CorrectiveActionTable(ActionID) ON DELETE CASCADE,
    Phase        TEXT NOT NULL CHECK (Phase IN ('Before', 'After')),
    FileLink     TEXT NOT NULL,
    SortOrder    INTEGER NOT NULL DEFAULT 1,
    UploadedBy   INTEGER REFERENCES MedpostStaffTable(StaffID),
    UploadedAt   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_corrective_photo_action ON CorrectiveActionPhotoTable (FK_ActionID, Phase);

-- A finding can need several actions, and one may still be open while another is done, so follow-ups are
-- their own rows. Each has its own PIC (text copy of the picked employee, like the finding's) and due date.
-- A finding with open follow-ups cannot be closed; the rule lives in code (internal/correctiveaction).
CREATE TABLE IF NOT EXISTS CorrectiveActionFollowUpTable (
    FollowUpID     INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ActionID    INTEGER NOT NULL REFERENCES CorrectiveActionTable(ActionID) ON DELETE CASCADE,
    Description    TEXT NOT NULL,
    PicName        TEXT NOT NULL,
    PicEmployeeID  TEXT NOT NULL DEFAULT '',
    DueDate        TEXT CHECK (DueDate IS NULL OR length(DueDate) = 10),
    Status         TEXT NOT NULL DEFAULT 'Open' CHECK (Status IN ('Open', 'Done')),
    DoneDate       TEXT CHECK (DoneDate IS NULL OR length(DoneDate) = 10),
    ActionTaken    TEXT NOT NULL DEFAULT '',
    FK_CreatedBy   INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID),
    CreatedAt      TEXT NOT NULL,
    FK_DoneBy      INTEGER REFERENCES MedpostStaffTable(StaffID),
    DoneAt         TEXT
);

CREATE INDEX IF NOT EXISTS idx_corrective_followup_action ON CorrectiveActionFollowUpTable (FK_ActionID, Status);

-- Append-only: one row per changed field. Deleted together with its finding (an admin delete), which is
-- why the server also logs the deletion.
CREATE TABLE IF NOT EXISTS CorrectiveActionHistoryTable (
    HistoryID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ActionID  INTEGER NOT NULL REFERENCES CorrectiveActionTable(ActionID) ON DELETE CASCADE,
    FK_StaffID   INTEGER REFERENCES MedpostStaffTable(StaffID),
    ChangedAt    TEXT NOT NULL,
    Field        TEXT NOT NULL,
    OldValue     TEXT NOT NULL DEFAULT '',
    NewValue     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_corrective_history_action ON CorrectiveActionHistoryTable (FK_ActionID, HistoryID);
