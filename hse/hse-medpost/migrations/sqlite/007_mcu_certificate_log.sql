-- Audit trail for health-certificate PDFs: who generated/downloaded which
-- record's certificate, and when. McuRecord.HealthCertificateFileName holds
-- the current saved file's path (server-managed once a certificate exists,
-- via internal/entrymcu/certificate.go's DownloadCertificate — no longer a
-- free-text field the checkup form lets staff type into); this table is the
-- append-only history behind that single current-path column.
-- FK_PatientID is a deliberate denormalization (already reachable via
-- FK_McuRecordID -> McuRecord.FK_PatientID): the user asked for "latest
-- certificate linked to patient" directly, so a patient's certificate
-- history doesn't need a join through McuRecord to query.
CREATE TABLE IF NOT EXISTS McuCertificateLog (
    McuCertificateLogID INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_McuRecordID INTEGER NOT NULL REFERENCES McuRecord(McuRecordID),
    FK_PatientID   INTEGER NOT NULL REFERENCES Patient(PatientID),
    FK_StaffID     INTEGER NULL REFERENCES MedpostStaffTable(StaffID),
    Action         TEXT NOT NULL, -- 'generated' | 'downloaded'
    FilePath       TEXT NOT NULL,
    CreatedAt      TEXT NOT NULL -- CHAR(19)
);
CREATE INDEX IF NOT EXISTS idx_mcu_cert_log_record ON McuCertificateLog(FK_McuRecordID);
CREATE INDEX IF NOT EXISTS idx_mcu_cert_log_patient ON McuCertificateLog(FK_PatientID);
