-- Medical checkup submission portal tables (MariaDB). Same shape as migrations/sqlite/016_mcu_submission.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS / INSERT IGNORE).
--
-- Run it BEFORE deploying the build that has the submission portal (/mcu-submit).

CREATE TABLE IF NOT EXISTS Submitter (
    SubmitterID          INT AUTO_INCREMENT PRIMARY KEY,
    Email                VARCHAR(254) NOT NULL,
    Username             VARCHAR(40) NULL,
    PasswordHash         VARCHAR(255) NULL,
    Name                 VARCHAR(150) NOT NULL,
    FK_CompanyID         INT NULL,
    PositionText         VARCHAR(128) NOT NULL DEFAULT '',
    Role                 VARCHAR(12) NOT NULL DEFAULT 'Submitter',
    IsBuiltIn            TINYINT(1) NOT NULL DEFAULT 0,
    Status               VARCHAR(12) NOT NULL DEFAULT 'Requested',
    SessionVersion       INT NOT NULL DEFAULT 1,
    RemindersEnabled     TINYINT(1) NOT NULL DEFAULT 1,
    LastReminderAt       VARCHAR(32) NULL,
    EmailVerifiedAt      VARCHAR(32) NULL,
    EmailBouncedAt       VARCHAR(32) NULL,
    EmailBounceReason    VARCHAR(100) NULL,
    ErasedAt             VARCHAR(32) NULL,
    ApprovedAt           VARCHAR(32) NULL,
    FK_ApprovedBySubmitterID INT NULL,
    RejectReason         VARCHAR(255) NULL,
    FailedLogins         INT NOT NULL DEFAULT 0,
    LockedUntil          VARCHAR(32) NULL,
    CreatedAt            VARCHAR(32) NOT NULL,
    LastLoginAt          VARCHAR(32) NULL,
    UNIQUE KEY uq_submitter_email (Email),
    UNIQUE KEY uq_submitter_username (Username),
    KEY idx_submitter_status (Status),
    CONSTRAINT fk_submitter_company FOREIGN KEY (FK_CompanyID) REFERENCES Company(CompanyID),
    CONSTRAINT fk_submitter_approver FOREIGN KEY (FK_ApprovedBySubmitterID) REFERENCES Submitter(SubmitterID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS SubmitterToken (
    TokenHash      CHAR(64) NOT NULL PRIMARY KEY,
    FK_SubmitterID INT NOT NULL,
    Purpose        VARCHAR(12) NOT NULL,
    NewEmail       VARCHAR(254) NULL,
    ExpiresAt      VARCHAR(32) NOT NULL,
    UsedAt         VARCHAR(32) NULL,
    KEY idx_submittertoken_submitter (FK_SubmitterID),
    CONSTRAINT fk_submittertoken_submitter FOREIGN KEY (FK_SubmitterID) REFERENCES Submitter(SubmitterID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS McuSubmissionEvent (
    EventID    INT AUTO_INCREMENT PRIMARY KEY,
    At         VARCHAR(32) NOT NULL,
    ActorType  VARCHAR(10) NOT NULL,
    ActorID    INT NULL,
    EntityType VARCHAR(12) NOT NULL,
    EntityID   INT NOT NULL,
    Action     VARCHAR(30) NOT NULL,
    Detail     VARCHAR(255) NULL,
    IpHash     CHAR(64) NULL,
    KEY idx_mcuevent_entity (EntityType, EntityID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS McuPortalSetting (
    McuPortalSettingID      INT NOT NULL PRIMARY KEY,
    RemindersEnabled        TINYINT(1) NOT NULL DEFAULT 0,
    ReminderDaysBefore      INT NOT NULL DEFAULT 60,
    ReminderRepeatDays      INT NOT NULL DEFAULT 30,
    UpdatedAt               VARCHAR(32) NULL,
    FK_UpdatedBySubmitterID INT NULL,
    CONSTRAINT fk_mcuportalsetting_updater FOREIGN KEY (FK_UpdatedBySubmitterID) REFERENCES Submitter(SubmitterID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO McuPortalSetting (McuPortalSettingID) VALUES (1);
