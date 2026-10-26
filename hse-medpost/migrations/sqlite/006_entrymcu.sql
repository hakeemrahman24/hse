-- CheckUpCriteriaTable, TaskCategoryTable, McuRecord (see plan §2
-- "entry-mcu module" and the finalized column mapping from the real 71-col
-- legacy sheet header). CheckUpStatus/ResultCriteria stay inline CHECK
-- constraints (fixed workflow states, not admin-editable lookups — see the
-- "Settings pattern clarified" progress log entry). TaskType/CheckUpCriteria
-- free text both become lookup FKs; RelationCode+RelationDepth collapse into
-- one FK_CompanyRelationID (CompanyRelation already owns Depth, no need to
-- duplicate it here); IsTerminal is the renamed + polarity-corrected legacy
-- "Terminal" column (TRUE/default = current record, FALSE = superseded by a
-- renewal or newer follow-up).
CREATE TABLE IF NOT EXISTS CheckUpCriteriaTable (
    CheckUpCriteriaID INTEGER PRIMARY KEY AUTOINCREMENT,
    Name   TEXT NOT NULL UNIQUE,
    Status TEXT NOT NULL DEFAULT 'Active'
);

CREATE TABLE IF NOT EXISTS TaskCategoryTable (
    TaskCategoryID INTEGER PRIMARY KEY AUTOINCREMENT,
    Name   TEXT NOT NULL UNIQUE,
    Status TEXT NOT NULL DEFAULT 'Active'
);

CREATE TABLE IF NOT EXISTS McuRecord (
    McuRecordID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_PatientID   INTEGER NOT NULL REFERENCES Patient(PatientID),
    Timestamp      TEXT NOT NULL,
    ReviewDate     TEXT NOT NULL,
    CheckUpID      TEXT NOT NULL UNIQUE,
    Reviewer       TEXT NULL,
    FK_CheckUpCriteriaID INTEGER NULL REFERENCES CheckUpCriteriaTable(CheckUpCriteriaID),
    FK_TaskCategoryID    INTEGER NULL REFERENCES TaskCategoryTable(TaskCategoryID),
    Age            INTEGER NULL,
    HeightCm       REAL NULL,
    WeightKg       REAL NULL,
    FK_CompanyID   INTEGER NULL REFERENCES Company(CompanyID),
    WorkPosition   TEXT NULL,
    DateCheckUp    TEXT NULL,
    IsShortProject INTEGER NOT NULL DEFAULT 0,
    ValidityPeriodMonths INTEGER NULL,
    BmiValue REAL NULL, BmiCriteria TEXT NULL, BmiNotes TEXT NULL,
    Smoking TEXT NULL,
    BpCriteria TEXT NULL, BpNotes TEXT NULL,
    VisionCriteria TEXT NULL, VisionNotes TEXT NULL,
    CbcCriteria TEXT NULL, CbcNotes TEXT NULL,
    LipidCriteria TEXT NULL, LipidNotes TEXT NULL,
    GlucoseCriteria TEXT NULL, GlucoseNotes TEXT NULL,
    LiverCriteria TEXT NULL, LiverNotes TEXT NULL,
    KidneyCriteria TEXT NULL, KidneyNotes TEXT NULL,
    UrinalysisCriteria TEXT NULL, UrinalysisNotes TEXT NULL,
    RadiologyResult TEXT NULL, RadiologyNotes TEXT NULL,
    EcgResult TEXT NULL,
    TreadmillResult TEXT NULL,
    EcgTreadmillNotes TEXT NULL,
    HerniaHemorrhoidCriteria TEXT NULL, HerniaHemorrhoidNotes TEXT NULL,
    SpirometryCriteria TEXT NULL, SpirometryNotes TEXT NULL,
    AudiometryCriteria TEXT NULL, AudiometryNotes TEXT NULL,
    HepBCriteria TEXT NULL, HepBNotes TEXT NULL,
    HepACriteria TEXT NULL, HepANotes TEXT NULL,
    RectalSwabCriteria TEXT NULL, RectalSwabNotes TEXT NULL,
    HivCriteria TEXT NULL,
    ResultCriteria TEXT NULL CHECK (ResultCriteria IN ('Fit','FitWithNote','Unfit','TemporaryUnfit','NeedFollowUp')),
    ResultValidUntil TEXT NULL,
    ResultNotes TEXT NULL,
    Recommendation TEXT NULL,
    CheckUpStatus TEXT NULL CHECK (CheckUpStatus IN ('Open','Close','Archived / Exit')),
    FK_ParentRecordID INTEGER NULL REFERENCES McuRecord(McuRecordID),
    HealthCertificateFileName TEXT NULL,
    HcValidityStatus TEXT NULL,
    ReviewYear INTEGER NULL,
    IsTerminal INTEGER NOT NULL DEFAULT 1,
    SubmissionDate TEXT NULL,
    DaysToReview INTEGER NULL,
    FK_CompanyRelationID INTEGER NULL REFERENCES CompanyRelation(CompanyRelationID)
);
CREATE INDEX IF NOT EXISTS idx_mcu_review_date ON McuRecord(ReviewDate);
