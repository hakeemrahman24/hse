-- PDF documents attached to a finding. See migrations/sqlite/030_corrective_action_document.sql.
CREATE TABLE IF NOT EXISTS CorrectiveActionDocumentTable (
    DocumentID   INT AUTO_INCREMENT PRIMARY KEY,
    FK_ActionID  INT NOT NULL,
    FileName     VARCHAR(255) NOT NULL,
    FileLink     VARCHAR(500) NOT NULL,
    UploadedBy   INT NULL,
    UploadedAt   VARCHAR(19) NOT NULL,
    CONSTRAINT fk_cad_action FOREIGN KEY (FK_ActionID) REFERENCES CorrectiveActionTable (ActionID)
        ON DELETE CASCADE,
    CONSTRAINT fk_cad_staff FOREIGN KEY (UploadedBy) REFERENCES MedpostStaffTable (StaffID),
    INDEX idx_corrective_document_action (FK_ActionID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
