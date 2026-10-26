-- Answers from the public SOFI fatigue questionnaire (/sofi). One row per completed inventory.
--
-- The person comes from Patient (FK_PatientID). Position, company, age and the MCU review status
-- are copied at submit time, so the record keeps what the form showed even if the patient is
-- edited later. Answers is a JSON array of 20 integers 0-6 in item order; the five dimension
-- scores, the total and the overall level are worked out on the server (internal/sofi/scoring.go)
-- and stored so the list can be sorted and filtered without re-scoring.
-- SubmittedAt is UTC, YYYY-MM-DDTHH:MM:SS.
CREATE TABLE IF NOT EXISTS SofiScreening (
    SofiID          INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_PatientID    INTEGER NOT NULL REFERENCES Patient(PatientID),
    SubmittedAt     TEXT NOT NULL,
    WeeksOnSite     INTEGER NOT NULL,
    AgeYears        INTEGER NULL,
    WorkPosition    TEXT NOT NULL DEFAULT '',
    Company         TEXT NOT NULL DEFAULT '',
    McuReviewStatus TEXT NOT NULL DEFAULT '',
    Answers         TEXT NOT NULL,
    ScoreEnergy     INTEGER NOT NULL,
    ScoreExertion   INTEGER NOT NULL,
    ScoreDiscomfort INTEGER NOT NULL,
    ScoreMotivation INTEGER NOT NULL,
    ScoreSleepiness INTEGER NOT NULL,
    ScoreTotal      INTEGER NOT NULL,
    ResultLevel     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sofi_patient ON SofiScreening(FK_PatientID);
CREATE INDEX IF NOT EXISTS idx_sofi_submitted ON SofiScreening(SubmittedAt);
