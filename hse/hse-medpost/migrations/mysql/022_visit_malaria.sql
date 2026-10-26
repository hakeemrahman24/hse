-- Visit and MalariaScreening (MariaDB). Same shape as migrations/sqlite/014_visit_malaria.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first. Safe to run twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build that has the malaria form.

CREATE TABLE IF NOT EXISTS Visit (
    VisitID       INT AUTO_INCREMENT PRIMARY KEY,
    FK_PatientID  INT NOT NULL,
    FK_CompanyID  INT NULL,
    FK_StaffID    INT NOT NULL,
    VisitCategory ENUM('Outpatient','Screening','Wellness') NOT NULL,
    VisitAt       VARCHAR(32) NOT NULL,
    ReportDate    VARCHAR(10) NOT NULL,
    Status        ENUM('Open','Closed','Amended') NOT NULL DEFAULT 'Open',
    CreatedAt     VARCHAR(32) NOT NULL,
    KEY idx_visit_patient (FK_PatientID),
    CONSTRAINT fk_visit_patient FOREIGN KEY (FK_PatientID) REFERENCES Patient(PatientID),
    CONSTRAINT fk_visit_company FOREIGN KEY (FK_CompanyID) REFERENCES Company(CompanyID),
    CONSTRAINT fk_visit_staff   FOREIGN KEY (FK_StaffID)   REFERENCES MedpostStaffTable(StaffID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS MalariaScreening (
    FK_VisitID      INT NOT NULL PRIMARY KEY,
    TestDate        VARCHAR(10) NOT NULL,
    SlideInitial    VARCHAR(4) NOT NULL,
    AgeYears        TINYINT UNSIGNED NULL,
    WeightKg        DECIMAL(4,1) NOT NULL,
    HeadOfHousehold VARCHAR(150) NOT NULL,
    HeadIsPatient   TINYINT(1) NOT NULL DEFAULT 0,
    RegencyCode     VARCHAR(13) NOT NULL,
    RegencyName     VARCHAR(100) NOT NULL,
    DistrictCode    VARCHAR(13) NOT NULL,
    DistrictName    VARCHAR(100) NOT NULL,
    VillageCode     VARCHAR(13) NOT NULL,
    VillageName     VARCHAR(100) NOT NULL,
    WorkPosition    VARCHAR(150) NOT NULL DEFAULT '',
    Company         VARCHAR(150) NOT NULL DEFAULT '',
    ExamType        ENUM('RDT','BloodSmear') NOT NULL,
    Result          ENUM('Negative','Pv','Pf','Po','Mixed') NULL,
    KEY idx_malaria_testdate (TestDate),
    KEY idx_malaria_slide (TestDate, SlideInitial),
    CONSTRAINT chk_malaria_weight CHECK (WeightKg > 0 AND WeightKg <= 150),
    CONSTRAINT fk_malaria_visit FOREIGN KEY (FK_VisitID) REFERENCES Visit(VisitID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
