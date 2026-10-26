-- Makes a Sub-header "audit-grade" without a separate document table:
--
-- SubHeaderTable.AuditLocked: when true, that sub-header's entries can be
-- created but never edited or deleted (enforced in Go, see subHeaderLocked
-- in internal/activity-log/subheaders.go). Any program can lock any of its
-- sub-headers — this is not specific to Drug & Alcohol, though that's the
-- first use (consent+result, positive/refusal, reasonable suspicion,
-- monthly randomization, arrival/ballot draw).
--
-- ActivityTable.FK_SubmissionID: optionally ties a sub-header entry to the
-- exact DrugAlcoholSubmission row it documents, instead of a human matching
-- donor name/date between the two.
--
-- EvidenceTable.FileHash: SHA-256 of the uploaded content at upload time,
-- for tamper detection. Populated for sub-header entry uploads; NULL for
-- older/regular-activity evidence.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database.
-- Take a mysqldump backup first. The column adds are harmless to repeat
-- (IF NOT EXISTS); the ADD CONSTRAINT below is not (MariaDB doesn't support
-- IF NOT EXISTS on ADD CONSTRAINT) — if rerun, skip it once it exists,
-- checked via: SELECT * FROM information_schema.TABLE_CONSTRAINTS WHERE
-- CONSTRAINT_NAME = 'fk_activity_submission'.

ALTER TABLE SubHeaderTable
    ADD COLUMN IF NOT EXISTS AuditLocked TINYINT(1) NOT NULL DEFAULT 0;

ALTER TABLE ActivityTable
    ADD COLUMN IF NOT EXISTS FK_SubmissionID INTEGER NULL;

ALTER TABLE ActivityTable
    ADD CONSTRAINT fk_activity_submission
        FOREIGN KEY (FK_SubmissionID) REFERENCES DrugAlcoholSubmission(SubmissionID);

ALTER TABLE EvidenceTable
    ADD COLUMN IF NOT EXISTS FileHash TEXT NULL;
