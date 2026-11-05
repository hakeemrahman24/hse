-- DocumentVersion and DocumentSection (MariaDB). Same shape as migrations/sqlite/025_document_reference.sql;
-- read that file for why these tables exist and why each constraint is there.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS, and the backfill skips documents that already have a version).
--
-- Before running: confirm the key types the foreign keys point at, because a mismatch makes
-- CREATE TABLE fail with "Can't create table ... (errno: 150)":
--     SHOW CREATE TABLE DocumentTable;      -- DocumentID should be INT (not BIGINT, not UNSIGNED)
--     SHOW CREATE TABLE MedpostStaffTable;  -- StaffID likewise
-- If either differs, change FK_DocumentID / FK_StaffID below to the same type.
--
-- Run it BEFORE deploying the build with the Reference tab. Without it the tab shows an error and
-- nothing else breaks.

CREATE TABLE IF NOT EXISTS DocumentVersion (
    VersionID     INT AUTO_INCREMENT PRIMARY KEY,
    FK_DocumentID INT NOT NULL,
    VersionNo     INT NOT NULL,
    -- VARCHAR(1024): the stored path is long (program/documents/type/dated-name).
    FileLink      VARCHAR(1024) NOT NULL,
    OriginalName  VARCHAR(255) NOT NULL DEFAULT '',
    -- SHA-256 hex is 64 characters. NULL only for files uploaded before this table existed;
    -- a UNIQUE index allows several NULLs in MariaDB.
    FileHash      CHAR(64) NULL,
    FK_StaffID    INT NULL,
    UploadDate    CHAR(10) NOT NULL,
    UNIQUE KEY idx_documentversion_doc_no (FK_DocumentID, VersionNo),
    UNIQUE KEY idx_documentversion_hash (FileHash),
    CONSTRAINT fk_documentversion_doc FOREIGN KEY (FK_DocumentID) REFERENCES DocumentTable(DocumentID)
        ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT fk_documentversion_staff FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS DocumentSection (
    FK_DocumentID INT NOT NULL,
    Section       VARCHAR(40) NOT NULL,
    SortOrder     INT NOT NULL DEFAULT 0,
    FK_StaffID    INT NULL,
    AddedDate     CHAR(10) NOT NULL,
    PRIMARY KEY (FK_DocumentID, Section),
    KEY idx_documentsection_section (Section, SortOrder),
    CONSTRAINT fk_documentsection_doc FOREIGN KEY (FK_DocumentID) REFERENCES DocumentTable(DocumentID)
        ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT fk_documentsection_staff FOREIGN KEY (FK_StaffID) REFERENCES MedpostStaffTable(StaffID)
        ON UPDATE CASCADE ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Version 1 for every document that already has an uploaded file; link-only documents get none.
-- The upload date of old files was never stored, so today's date stands in for it.
INSERT INTO DocumentVersion (FK_DocumentID, VersionNo, FileLink, OriginalName, UploadDate)
SELECT d.DocumentID, 1, d.FileLink, '', DATE_FORMAT(CURDATE(), '%Y-%m-%d')
FROM DocumentTable d
WHERE d.FileLink IS NOT NULL AND d.FileLink <> ''
  AND NOT EXISTS (SELECT 1 FROM DocumentVersion v WHERE v.FK_DocumentID = d.DocumentID);
