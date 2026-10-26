-- Results uploaded from the offline Drug & Alcohol Consent form.
--
-- One row per test, keyed by the client-generated ClientID so a retried
-- upload never inserts a second copy. FK_StaffID records who uploaded it
-- (from the signed-in bearer token). RawJSON keeps the full client payload.
CREATE TABLE IF NOT EXISTS DrugAlcoholScreening (
    SubmissionID   INTEGER PRIMARY KEY AUTOINCREMENT,
    ClientID       TEXT NOT NULL UNIQUE,
    FK_StaffID     INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID),
    FormVersion    TEXT NOT NULL,
    CreatedAt      TEXT NOT NULL,
    SubmittedAt    TEXT NOT NULL,
    DonorName      TEXT NOT NULL DEFAULT '',
    DonorNik       TEXT NOT NULL DEFAULT '',
    DonorCompany   TEXT NOT NULL DEFAULT '',
    DonorPosition  TEXT NOT NULL DEFAULT '',
    ConsentAt      TEXT NOT NULL DEFAULT '',
    TestType       TEXT NOT NULL DEFAULT '',
    Breath1Reading TEXT NOT NULL DEFAULT '',
    Breath1Result  TEXT NOT NULL DEFAULT '',
    Breath2Reading TEXT NOT NULL DEFAULT '',
    Breath2Result  TEXT NOT NULL DEFAULT '',
    FinalResult    TEXT NOT NULL DEFAULT '',
    Remarks        TEXT NOT NULL DEFAULT '',
    RawJSON        TEXT NOT NULL
);
