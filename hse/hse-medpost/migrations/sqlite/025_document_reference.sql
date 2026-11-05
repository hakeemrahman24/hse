-- Reference tab on the medical record page (plan/emr-plan.md section 11).
--
-- DocumentTable (001_sqlite.sql) stays the one library of documents: a title, a link (FormLink) or an
-- uploaded file (FileLink), notes, and the program. It cannot say "this is version 3 of that form",
-- cannot tell that two uploads are the same bytes, and cannot say which record-page section shows a
-- document. These two tables add exactly that and nothing else, so the activity log's Documents page
-- keeps working untouched.

-- One row per uploaded file of a document. Replacing a file adds a row (VersionNo + 1) and keeps the
-- old one, so a printed form can be tied back to the version in use on a given date.
-- DocumentTable.FileLink keeps pointing at the CURRENT version (the app updates it with the insert),
-- which is why every existing reader of FileLink needs no change.
CREATE TABLE IF NOT EXISTS DocumentVersion (
    VersionID     INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_DocumentID INTEGER NOT NULL,
    VersionNo     INTEGER NOT NULL,
    FileLink      TEXT NOT NULL,
    OriginalName  TEXT NOT NULL DEFAULT '',
    -- SHA-256 of the stored bytes, hex. NULL only for files uploaded before this table existed; the
    -- app fills those in at startup. Several NULLs may coexist in a UNIQUE index (SQLite and
    -- MariaDB both allow that), so unhashed old rows do not collide with each other.
    FileHash      TEXT,
    FK_StaffID    INTEGER,
    UploadDate    TEXT NOT NULL CHECK (length(UploadDate) = 10),
    FOREIGN KEY (FK_DocumentID) REFERENCES DocumentTable(DocumentID)
        ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE SET NULL
);

-- Same document + same number can exist once: two admins replacing a file at the same moment cannot
-- both become "version 2".
CREATE UNIQUE INDEX IF NOT EXISTS idx_documentversion_doc_no ON DocumentVersion (FK_DocumentID, VersionNo);
-- Same bytes can exist once in the whole library: this is what makes "reject an exact duplicate"
-- safe against two uploads racing past a check-then-insert in the application.
CREATE UNIQUE INDEX IF NOT EXISTS idx_documentversion_hash ON DocumentVersion (FileHash);

-- Which documents show in which section's Reference tab. A table instead of a Section column on
-- DocumentTable so one SOP can serve two sections, and removing it from one tab deletes only this row.
-- Section is the key the record page already uses (data-sub: alcohol, fatigue, malaria; data-section:
-- outpatient, inpatient, wellness). It has no CHECK on purpose: the allowed list lives in Go, so a
-- new section does not need a schema change.
CREATE TABLE IF NOT EXISTS DocumentSection (
    FK_DocumentID INTEGER NOT NULL,
    Section       TEXT NOT NULL,
    SortOrder     INTEGER NOT NULL DEFAULT 0,
    FK_StaffID    INTEGER,
    AddedDate     TEXT NOT NULL CHECK (length(AddedDate) = 10),
    PRIMARY KEY (FK_DocumentID, Section),
    FOREIGN KEY (FK_DocumentID) REFERENCES DocumentTable(DocumentID)
        ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_documentsection_section ON DocumentSection (Section, SortOrder);

-- Version 1 for every document that already has an uploaded file. Link-only documents have no file
-- and get no version. The upload date of old files was never stored, so today's date stands in for
-- it. NOT EXISTS makes this safe to run at every startup (cmd/server runs all SQLite migrations each
-- time): a document that already has any version is skipped.
INSERT INTO DocumentVersion (FK_DocumentID, VersionNo, FileLink, OriginalName, UploadDate)
SELECT d.DocumentID, 1, d.FileLink, '', date('now')
FROM DocumentTable d
WHERE d.FileLink IS NOT NULL AND d.FileLink <> ''
  AND NOT EXISTS (SELECT 1 FROM DocumentVersion v WHERE v.FK_DocumentID = d.DocumentID);
