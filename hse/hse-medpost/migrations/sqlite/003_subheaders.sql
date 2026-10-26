-- Admin-managed sub-categories under a program. Their entries used to live
-- in a parallel SubHeaderEntryTable/EntryEvidenceTable pair that duplicated
-- ActivityTable/EvidenceTable's exact shape (date/title/description/staff +
-- photo/document attachments) — folded into ActivityTable/EvidenceTable
-- instead via the nullable FK_SubHeaderID/FormLink columns added in
-- 001_sqlite.sql. A sub-header entry is just an activity with FK_SubHeaderID
-- set, not a separate table family. See internal/activity-log/migrate.go's
-- EnsureSubHeaderMerge for the one-time upgrade of pre-existing databases
-- that still have the old tables with data in them.

CREATE TABLE IF NOT EXISTS SubHeaderTable (
    SubHeaderID   INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ProcID     INTEGER NOT NULL,
    SubHeaderName TEXT NOT NULL,
    Status        TEXT NOT NULL DEFAULT 'Active'
        CHECK (Status IN ('Active', 'Archived')),
    FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable(ProcID)
        ON UPDATE CASCADE ON DELETE RESTRICT
);
