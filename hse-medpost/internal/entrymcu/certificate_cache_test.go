package entrymcu

import (
	"os"
	"path/filepath"
	"testing"

	"hse-medpost/internal/storage"
)

func TestClearCachedCertificatesRemovesSavedFilesOnly(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Ani Test', '1990-01-02')`); err != nil {
		t.Fatal(err)
	}
	upload := t.TempDir()
	certDir := storage.McuCertificateDir(upload)
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(certDir, "cached.pdf")
	if err := os.WriteFile(saved, []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(upload, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(otherDir, "keep.pdf")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, HealthCertificateFileName) VALUES (1, '2026-01-01T00:00:00', '2026-01-01', 'MedC1', ?)`, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, HealthCertificateFileName) VALUES (1, '2026-01-02T00:00:00', '2026-01-02', 'MedC2', ?)`, other); err != nil {
		t.Fatal(err)
	}

	removed, err := (&API{DB: database}).clearCachedCertificates(upload)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Fatal("cached certificate still on disk")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("file outside the certificate folder was removed")
	}
	var left int
	if err := database.QueryRow(`SELECT COUNT(*) FROM McuRecord WHERE HealthCertificateFileName IS NOT NULL`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("stored paths left = %d", left)
	}
}
