-- NationalityTable + Patient (see plan §2 "New: Patient"). Patient is the
-- shared identity extracted out of McuRecord's old inlined demographics —
-- entrymcu, emr, and screening all reference it via FK_PatientID instead of
-- re-entering name/DOB/company per record.
CREATE TABLE IF NOT EXISTS NationalityTable (
    NationalityID INTEGER PRIMARY KEY AUTOINCREMENT,
    Name          TEXT NOT NULL UNIQUE,
    Status        TEXT NOT NULL DEFAULT 'Active'
);

CREATE TABLE IF NOT EXISTS Patient (
    PatientID        INTEGER PRIMARY KEY AUTOINCREMENT,
    Name             TEXT NOT NULL,
    Gender           TEXT NULL,
    DateOfBirth      TEXT NULL,
    FK_NationalityID INTEGER NULL REFERENCES NationalityTable(NationalityID),
    BloodType        TEXT NULL,
    EmployeeID       TEXT NULL,
    FK_CompanyID     INTEGER NULL REFERENCES Company(CompanyID),
    WorkPosition     TEXT NULL
);
