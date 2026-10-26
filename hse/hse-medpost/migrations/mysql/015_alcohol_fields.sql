-- Columns for the Drug & Alcohol form fields that the first release stored
-- only in RawJSON. Same list as drugAlcoholAddedColumns in
-- internal/screening/record.go.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first. Re-running it is harmless (IF NOT EXISTS).
-- TEXT, not VARCHAR(500): 25 VARCHAR(500) columns exceed MariaDB's 65535-byte row limit.

ALTER TABLE DrugAlcoholSubmission
    ADD COLUMN IF NOT EXISTS DonorSignName            TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS WitnessName              TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS MedNone                  TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med1Name                 TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med1Dose                 TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med1Reason               TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med1Prescribed           TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med2Name                 TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med2Dose                 TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med2Reason               TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Med2Prescribed           TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS BreathDevice             TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS BreathCalibrationUntil   TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Breath1Time              TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Breath1MouthpieceSealed  TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Breath2Time              TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS Breath2MouthpieceSealed  TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS SpecimenTempOk           TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS SpecimenColourOk         TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS SpecimenTempValue        TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS PanelNegative            TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS PanelNonnegative         TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS CollectorName            TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS SecurityName             TEXT NOT NULL,
    ADD COLUMN IF NOT EXISTS AckName                  TEXT NOT NULL;
