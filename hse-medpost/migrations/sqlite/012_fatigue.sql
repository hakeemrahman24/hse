-- Results uploaded from the offline Fatigue Scoring form.
--
-- Same upload pattern as DrugAlcoholScreening (011_screening.sql): one row
-- per record, keyed by the client-generated ClientID so a retried upload
-- never inserts a second copy. FK_StaffID records who uploaded it.
-- Score and vital-sign values are stored as text because the form sends
-- them as a mix of numbers and blanks; RawJSON keeps the full payload.
CREATE TABLE IF NOT EXISTS FatigueScreening (
    SubmissionID   INTEGER PRIMARY KEY AUTOINCREMENT,
    ClientID       TEXT NOT NULL UNIQUE,
    FK_StaffID     INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID),
    FormVersion    TEXT NOT NULL,
    CreatedAt      TEXT NOT NULL,
    SubmittedAt    TEXT NOT NULL,
    Examiner       TEXT NOT NULL DEFAULT '',
    WorkerName     TEXT NOT NULL DEFAULT '',
    WorkerCompany  TEXT NOT NULL DEFAULT '',
    WorkerPosition TEXT NOT NULL DEFAULT '',
    ExamDate       TEXT NOT NULL DEFAULT '',
    ExamTime       TEXT NOT NULL DEFAULT '',
    BpSystolic     TEXT NOT NULL DEFAULT '',
    BpDiastolic    TEXT NOT NULL DEFAULT '',
    Pulse          TEXT NOT NULL DEFAULT '',
    Score24h       TEXT NOT NULL DEFAULT '',
    Score48h       TEXT NOT NULL DEFAULT '',
    ScoreAwake     TEXT NOT NULL DEFAULT '',
    ScoreTotal     TEXT NOT NULL DEFAULT '',
    RiskLevel      TEXT NOT NULL DEFAULT '',
    RiskPoints     TEXT NOT NULL DEFAULT '',
    Remarks        TEXT NOT NULL DEFAULT '',
    RawJSON        TEXT NOT NULL
);
