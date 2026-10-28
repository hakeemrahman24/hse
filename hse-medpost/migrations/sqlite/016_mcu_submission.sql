-- Medical checkup submission portal (/mcu-submit). Design: plan/mcu-submission-plan.md section 3.
--
-- Submitters are outside people (company HR, contractors' clinics). They are NOT staff: no row here
-- is ever in AppUserTable, and Patient is never written by a submitter.
-- Status and Role values are checked in Go (internal/mcusubmit), not with CHECK/ENUM, so this file
-- stays valid on both SQLite and MariaDB.
-- All timestamps are UTC, YYYY-MM-DDTHH:MM:SS.

CREATE TABLE IF NOT EXISTS Submitter (
    SubmitterID          INTEGER PRIMARY KEY AUTOINCREMENT,
    Email                TEXT NOT NULL UNIQUE,            -- lower-cased
    Username             TEXT NULL UNIQUE,                -- chosen after approval
    PasswordHash         TEXT NULL,
    Name                 TEXT NOT NULL,
    FK_CompanyID         INTEGER NULL REFERENCES Company(CompanyID), -- NULL only for the built-in admin
    PositionText         TEXT NOT NULL DEFAULT '',
    Role                 TEXT NOT NULL DEFAULT 'Submitter', -- Submitter | Admin
    IsBuiltIn            INTEGER NOT NULL DEFAULT 0,      -- 1 only for the hidden portal admin
    Status               TEXT NOT NULL DEFAULT 'Requested', -- Requested | Approved | Active | Rejected | Disabled
    SessionVersion       INTEGER NOT NULL DEFAULT 1,      -- bumped to end every older session
    RemindersEnabled     INTEGER NOT NULL DEFAULT 1,
    LastReminderAt       TEXT NULL,
    EmailVerifiedAt      TEXT NULL,
    EmailBouncedAt       TEXT NULL,
    EmailBounceReason    TEXT NULL,
    ErasedAt             TEXT NULL,
    ApprovedAt           TEXT NULL,
    FK_ApprovedBySubmitterID INTEGER NULL REFERENCES Submitter(SubmitterID),
    RejectReason         TEXT NULL,
    FailedLogins         INTEGER NOT NULL DEFAULT 0,
    LockedUntil          TEXT NULL,
    CreatedAt            TEXT NOT NULL,
    LastLoginAt          TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_submitter_status ON Submitter(Status);

-- One-time links, stored hashed (sha256 hex) so a database leak does not leak working links.
CREATE TABLE IF NOT EXISTS SubmitterToken (
    TokenHash      TEXT PRIMARY KEY,
    FK_SubmitterID INTEGER NOT NULL REFERENCES Submitter(SubmitterID),
    Purpose        TEXT NOT NULL,                         -- Verify | Setup | Reset | EmailChange
    NewEmail       TEXT NULL,                             -- EmailChange only
    ExpiresAt      TEXT NOT NULL,
    UsedAt         TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_submittertoken_submitter ON SubmitterToken(FK_SubmitterID);

-- Append-only audit trail for the portal. No foreign key on EntityID so an event outlives its row.
CREATE TABLE IF NOT EXISTS McuSubmissionEvent (
    EventID    INTEGER PRIMARY KEY AUTOINCREMENT,
    At         TEXT NOT NULL,
    ActorType  TEXT NOT NULL,                             -- Submitter | Staff | System
    ActorID    INTEGER NULL,
    EntityType TEXT NOT NULL,                             -- Submitter | Batch | Submission
    EntityID   INTEGER NOT NULL,
    Action     TEXT NOT NULL,
    Detail     TEXT NULL,                                 -- short reason; never a name, NIK or birth date
    IpHash     TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcuevent_entity ON McuSubmissionEvent(EntityType, EntityID);

-- Single row (id 1): expiring-checkup reminder settings, edited by the portal admin.
CREATE TABLE IF NOT EXISTS McuPortalSetting (
    McuPortalSettingID     INTEGER PRIMARY KEY,
    RemindersEnabled       INTEGER NOT NULL DEFAULT 0,
    ReminderDaysBefore     INTEGER NOT NULL DEFAULT 60,
    ReminderRepeatDays     INTEGER NOT NULL DEFAULT 30,
    UpdatedAt              TEXT NULL,
    FK_UpdatedBySubmitterID INTEGER NULL REFERENCES Submitter(SubmitterID)
);
INSERT OR IGNORE INTO McuPortalSetting (McuPortalSettingID) VALUES (1);
