-- PDF documents attached to a finding (a signed report, a work order, a purchase receipt). Separate from
-- CorrectiveActionPhotoTable because a document is not a Before/After picture and has its own limits.
-- FileName is the name the person uploaded, kept only to show and to name the download; the stored file
-- has a generated name. The count limit is enforced in code (internal/correctiveaction).
CREATE TABLE IF NOT EXISTS CorrectiveActionDocumentTable (
    DocumentID   INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_ActionID  INTEGER NOT NULL REFERENCES CorrectiveActionTable(ActionID) ON DELETE CASCADE,
    FileName     TEXT NOT NULL,
    FileLink     TEXT NOT NULL,
    UploadedBy   INTEGER REFERENCES MedpostStaffTable(StaffID),
    UploadedAt   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_corrective_document_action ON CorrectiveActionDocumentTable (FK_ActionID);
