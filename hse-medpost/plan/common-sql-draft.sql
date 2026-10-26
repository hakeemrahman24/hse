-- Draft DDL for tables classified as "common" (shared across entry-mcu,
-- medical-activities, and emr) — staged here ahead of being merged into
-- ../plan/hse-medpost-plan.md §2, the authoritative schema.
--
-- Source of each block noted inline. Nothing here is edited yet — this is
-- a staging copy, not a rewrite.

-- =====================================================
-- From entry-mcu/plan/sql schema, unchanged.
-- =====================================================

CREATE TABLE IF NOT EXISTS EntityType (
    EntityTypeID   INT AUTO_INCREMENT PRIMARY KEY,
    LegacyCode     VARCHAR(16)  NULL,
    Name           VARCHAR(64)  NOT NULL UNIQUE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS Company (
    CompanyID      INT AUTO_INCREMENT PRIMARY KEY,
    LegacyCode     VARCHAR(16)  NULL UNIQUE,
    Name           VARCHAR(255) NOT NULL UNIQUE,
    Abbreviation   VARCHAR(32)  NULL,
    FK_EntityTypeID INT NULL,
    FOREIGN KEY (FK_EntityTypeID) REFERENCES EntityType(EntityTypeID)
        ON UPDATE CASCADE ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS CompanyRelation (
    CompanyRelationID INT AUTO_INCREMENT PRIMARY KEY,
    RelationCode   VARCHAR(16)  NOT NULL UNIQUE,
    FK_CompanyID   INT NOT NULL,
    FK_ParentID    INT NULL,
    Project        VARCHAR(64)  NULL,
    Depth          SMALLINT NULL,
    FOREIGN KEY (FK_CompanyID) REFERENCES Company(CompanyID)
        ON UPDATE CASCADE ON DELETE RESTRICT,
    FOREIGN KEY (FK_ParentID) REFERENCES Company(CompanyID)
        ON UPDATE CASCADE ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- =====================================================
-- New table — see ../plan/hse-medpost-plan.md §2 for the reasoning.
-- Extracted from entry-mcu's McuRecord inlined demographics.
-- =====================================================

CREATE TABLE Patient (
    PatientID     INT AUTO_INCREMENT PRIMARY KEY,
    Name          VARCHAR(255) NOT NULL,
    Gender        VARCHAR(16)  NULL,
    DateOfBirth   CHAR(10)     NULL,
    Nationality   VARCHAR(64)  NULL,
    BloodType     VARCHAR(8)   NULL,
    EmployeeID    VARCHAR(32)  NULL,   -- badge/NIK if one exists
    FK_CompanyID  INT NULL,
    WorkPosition  VARCHAR(128) NULL,
    INDEX idx_patient_name (Name),
    FOREIGN KEY (FK_CompanyID) REFERENCES Company(CompanyID)
        ON UPDATE CASCADE ON DELETE SET NULL
) ENGINE=InnoDB;

-- =====================================================
-- From medical-activities/plan/sql schema, unchanged — reclassified as
-- common because staff identity + login/role is a whole-app concern, not
-- medical-activities-specific data. See ../common/README.md.
-- =====================================================

CREATE TABLE MedpostStaffTable (
    StaffID   INT AUTO_INCREMENT PRIMARY KEY,
    StaffName VARCHAR(100) NOT NULL,
    Position  VARCHAR(100),
    Status    ENUM('Active', 'NotActive') NOT NULL DEFAULT 'Active'
) ENGINE=InnoDB;

CREATE TABLE PositionRoleTable (
    PositionID   INT AUTO_INCREMENT PRIMARY KEY,
    PositionName VARCHAR(100) NOT NULL,
    Role         ENUM('admin', 'manager', 'staff') NOT NULL DEFAULT 'staff',
    CONSTRAINT uq_position_name UNIQUE (PositionName)
) ENGINE=InnoDB;

INSERT INTO PositionRoleTable (PositionName, Role) VALUES
('Chief Medical Officer', 'admin'),
('Site Doctor', 'staff'),
('Medpost Nurse', 'staff'),
('Administrator', 'admin');

CREATE TABLE AppUserTable (
    UserID       INT AUTO_INCREMENT PRIMARY KEY,
    FK_StaffID   INT NOT NULL,
    Username     VARCHAR(100) NOT NULL,
    PasswordHash VARCHAR(255) NOT NULL,
    Role         ENUM('admin', 'manager', 'staff') NOT NULL DEFAULT 'staff',
    CONSTRAINT uq_appuser_staff UNIQUE (FK_StaffID),
    CONSTRAINT uq_appuser_username UNIQUE (Username),
    CONSTRAINT fk_appuser_staff
        FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE RESTRICT
) ENGINE=InnoDB;
