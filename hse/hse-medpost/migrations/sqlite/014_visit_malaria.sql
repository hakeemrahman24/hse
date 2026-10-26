-- Visit and its malaria screening part (see plan/emr-plan.md §4.2).
--
-- Visit has only the columns the malaria form needs today; the rest of the
-- planned columns (site, parent visit, closed-at, update audit) are added
-- by the migration of the feature that first uses them.
--
-- SlideInitial is 3 letters made from the patient's name plus a digit (BSU1),
-- written on the blood slide. It is unique per TestDate except that one patient
-- keeps the same code for all of that day's tests; Go checks this (MariaDB has no partial
-- unique index, and the same patient may repeat).
--
-- MalariaScreening copies the patient's age, company, position and the region
-- names at test time, so the record keeps what was true that day.

CREATE TABLE IF NOT EXISTS Visit (
    VisitID       INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_PatientID  INTEGER NOT NULL REFERENCES Patient(PatientID),
    FK_CompanyID  INTEGER NULL REFERENCES Company(CompanyID),
    FK_StaffID    INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID),
    VisitCategory TEXT NOT NULL CHECK (VisitCategory IN ('Outpatient', 'Screening', 'Wellness')),
    VisitAt       TEXT NOT NULL,
    ReportDate    TEXT NOT NULL,
    Status        TEXT NOT NULL DEFAULT 'Open' CHECK (Status IN ('Open', 'Closed', 'Amended')),
    CreatedAt     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_visit_patient ON Visit (FK_PatientID);

-- Why the test was done. 1 is the default; 2 is a patient with symptoms.
-- Whether it happened inside an outpatient visit is Visit.VisitCategory, not this.
CREATE TABLE IF NOT EXISTS MalariaPurposeTable (
    PurposeID   INTEGER PRIMARY KEY,
    PurposeName TEXT NOT NULL UNIQUE
);
INSERT OR IGNORE INTO MalariaPurposeTable (PurposeID, PurposeName) VALUES (1, 'On arrival'), (2, 'Symptomatic');

CREATE TABLE IF NOT EXISTS MalariaScreening (
    MalariaID       INTEGER PRIMARY KEY,
    FK_VisitID      INTEGER NOT NULL REFERENCES Visit(VisitID),
    FK_PurposeID    INTEGER NOT NULL DEFAULT 1 REFERENCES MalariaPurposeTable(PurposeID),
    TestDate        TEXT NOT NULL,
    SlideInitial    TEXT NOT NULL,
    AgeYears        INTEGER NULL,
    WeightKg        REAL NOT NULL CHECK (WeightKg > 0 AND WeightKg <= 150),
    HeadOfHousehold TEXT NOT NULL,
    HeadIsPatient   INTEGER NOT NULL DEFAULT 0,
    RegencyCode     TEXT NOT NULL,
    RegencyName     TEXT NOT NULL,
    DistrictCode    TEXT NOT NULL,
    DistrictName    TEXT NOT NULL,
    VillageCode     TEXT NOT NULL,
    VillageName     TEXT NOT NULL,
    WorkPosition    TEXT NOT NULL DEFAULT '',
    Company         TEXT NOT NULL DEFAULT '',
    ExamType        TEXT NOT NULL CHECK (ExamType IN ('RDT', 'BloodSmear')),
    Result          TEXT NULL CHECK (Result IN ('Negative', 'Pv', 'Pf', 'Po', 'Mixed'))
);

-- A visit can hold more than one test (an RDT, then a smear), so FK_VisitID is not unique.
CREATE INDEX IF NOT EXISTS idx_malaria_visit ON MalariaScreening (FK_VisitID);
CREATE INDEX IF NOT EXISTS idx_malaria_testdate ON MalariaScreening (TestDate);
CREATE INDEX IF NOT EXISTS idx_malaria_slide ON MalariaScreening (TestDate, SlideInitial);
