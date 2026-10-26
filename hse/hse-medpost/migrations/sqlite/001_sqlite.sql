-- SQLite-compatible schema for local development.
-- Production NAS uses plan/sql schema (MariaDB).
-- Calendar dates are TEXT 'YYYY-MM-DD' (never DATE/DATETIME) to avoid timezone shifts.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS HealthProcedureTable (
    ProcID          INTEGER PRIMARY KEY AUTOINCREMENT,
    HealthProcedure TEXT NOT NULL,
    Status          TEXT NOT NULL DEFAULT 'Active'
        CHECK (Status IN ('Active', 'Archived'))
);

CREATE TABLE IF NOT EXISTS MedpostStaffTable (
    StaffID   INTEGER PRIMARY KEY AUTOINCREMENT,
    StaffName TEXT NOT NULL,
    Position  TEXT,
    Status    TEXT NOT NULL DEFAULT 'Active'
        CHECK (Status IN ('Active', 'NotActive'))
);

CREATE TABLE IF NOT EXISTS SupervisorTable (
    SupervisorID INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ProcID    INTEGER NOT NULL,
    FK_StaffID   INTEGER NOT NULL,
    StartDate    TEXT CHECK (StartDate IS NULL OR length(StartDate) = 10),
    EndDate      TEXT CHECK (EndDate IS NULL OR length(EndDate) = 10),
    FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable(ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE RESTRICT
);

-- FK_SubHeaderID: NULL for a normal top-level activity (most programs don't
-- use sub-headers at all); set when this activity actually belongs to one of
-- the program's admin-defined sub-headers (SubHeaderTable, created below in
-- 003_subheaders.sql) instead of being a duplicate parallel table for that
-- case. ON DELETE CASCADE matches the old SubHeaderEntryTable's behavior:
-- deleting a sub-header removes its entries (and, via EvidenceTable's own
-- cascade from ActivityID, their evidence) rather than orphaning them.
CREATE TABLE IF NOT EXISTS ActivityTable (
    ActivityID      INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ProcID       INTEGER NOT NULL,
    FK_StaffID      INTEGER NOT NULL,
    FK_SubHeaderID  INTEGER,
    ActivityType    TEXT NOT NULL
        CHECK (ActivityType IN ('Monitoring/Inspection', 'Socialization')),
    ActivityTitle   TEXT NOT NULL,
    ActivitySummary TEXT,
    Location        TEXT,
    ActivityDate    TEXT CHECK (ActivityDate IS NULL OR length(ActivityDate) = 10),
    Notes           TEXT,
    FormLink        TEXT,
    FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable(ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    FOREIGN KEY (FK_SubHeaderID) REFERENCES SubHeaderTable(SubHeaderID)
        ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS EvidenceTable (
    EvidenceID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_IDActivity INTEGER NOT NULL,
    FK_StaffID    INTEGER REFERENCES MedpostStaffTable(StaffID),
    EvidenceType  TEXT NOT NULL
        CHECK (EvidenceType IN ('AttendanceList', 'ReportAttachment', 'Photos')),
    FileLink      TEXT,
    UploadDate    TEXT CHECK (UploadDate IS NULL OR length(UploadDate) = 10),
    FOREIGN KEY (FK_IDActivity) REFERENCES ActivityTable(ActivityID)
        ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS DocumentTable (
    DocumentID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ProcID     INTEGER,
    DocumentsType TEXT NOT NULL
        CHECK (DocumentsType IN ('Procedure', 'SocializationMaterial', 'Forms', 'Template')),
    DocumentTitle TEXT NOT NULL,
    FormLink      TEXT,
    FileLink      TEXT,
    Notes         TEXT,
    FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable(ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    CHECK (
        (DocumentsType = 'Template' AND FK_ProcID IS NULL)
        OR (DocumentsType <> 'Template' AND FK_ProcID IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS AppUserTable (
    UserID       INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_StaffID   INTEGER NOT NULL UNIQUE,
    Username     TEXT NOT NULL UNIQUE,
    PasswordHash TEXT NOT NULL,
    Role         TEXT NOT NULL DEFAULT 'staff'
        CHECK (Role IN ('admin', 'manager', 'staff')),
    FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE RESTRICT
);
