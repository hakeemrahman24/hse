package entrymcu

import (
	"database/sql"
	"testing"

	"hse-medpost/internal/db"
)

func patientCompany(t *testing.T, database *sql.DB, patientID int64) sql.NullInt64 {
	t.Helper()
	var c sql.NullInt64
	if err := database.QueryRow(`SELECT FK_CompanyID FROM Patient WHERE PatientID = ?`, patientID).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func seedCompanyPatient(t *testing.T) *sql.DB {
	t.Helper()
	database := openExportTestDB(t)
	for _, q := range []string{
		`INSERT INTO Company (Name) VALUES ('Alpha'), ('Beta'), ('Gamma')`,
		`INSERT INTO Patient (Name, DateOfBirth, Gender) VALUES ('Ani Test', '1990-01-02', 'F')`,
		`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, FK_CompanyID) VALUES
			(1, '2024-01-01T00:00:00', '2024-01-01', 'OLD1', 1),
			(1, '2025-01-01T00:00:00', '2025-01-01', 'NEW1', 2),
			(1, '2026-01-01T00:00:00', '2026-01-01', 'NOCO', NULL)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestSyncPatientFromLatestVisitUsesLatestVisitWithCompany(t *testing.T) {
	database := seedCompanyPatient(t)
	if err := db.SyncPatientFromLatestVisit(database, 1); err != nil {
		t.Fatal(err)
	}
	if c := patientCompany(t, database, 1); !c.Valid || c.Int64 != 2 {
		t.Fatalf("company = %+v, want 2 (latest visit that has a company)", c)
	}
}

func TestSyncPatientFromLatestVisitFollowsEditsAndDeletes(t *testing.T) {
	database := seedCompanyPatient(t)
	if _, err := database.Exec(`UPDATE McuRecord SET FK_CompanyID = 3 WHERE CheckUpID = 'NEW1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncPatientFromLatestVisit(database, 1); err != nil {
		t.Fatal(err)
	}
	if c := patientCompany(t, database, 1); c.Int64 != 3 {
		t.Fatalf("after edit company = %+v, want 3", c)
	}
	if _, err := database.Exec(`DELETE FROM McuRecord WHERE CheckUpID = 'NEW1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncPatientFromLatestVisit(database, 1); err != nil {
		t.Fatal(err)
	}
	if c := patientCompany(t, database, 1); c.Int64 != 1 {
		t.Fatalf("after delete company = %+v, want 1", c)
	}
}

func TestSyncAllPatientsFromLatestVisitKeepsValueWhenNoVisitHasCompany(t *testing.T) {
	database := openExportTestDB(t)
	for _, q := range []string{
		`INSERT INTO Company (Name) VALUES ('Alpha')`,
		`INSERT INTO Patient (Name, DateOfBirth, Gender, FK_CompanyID) VALUES ('Budi Test', '1991-02-03', 'M', 1)`,
		`INSERT INTO Patient (Name, DateOfBirth, Gender) VALUES ('Citra Test', '1992-03-04', 'F')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SyncAllPatientsFromLatestVisit(database); err != nil {
		t.Fatal(err)
	}
	if c := patientCompany(t, database, 1); c.Int64 != 1 {
		t.Fatalf("visitless patient lost company: %+v", c)
	}
	if c := patientCompany(t, database, 2); c.Valid {
		t.Fatalf("patient with no company gained one: %+v", c)
	}
}

func patientPosition(t *testing.T, database *sql.DB, patientID int64) string {
	t.Helper()
	var p sql.NullString
	if err := database.QueryRow(`SELECT WorkPosition FROM Patient WHERE PatientID = ?`, patientID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p.String
}

func TestSyncPatientFromLatestVisitUsesLatestNonEmptyPosition(t *testing.T) {
	database := openExportTestDB(t)
	for _, q := range []string{
		`INSERT INTO Patient (Name, DateOfBirth, Gender, WorkPosition) VALUES ('Ani Test', '1990-01-02', 'F', 'Old Stored')`,
		`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, WorkPosition) VALUES
			(1, '2024-01-01T00:00:00', '2024-01-01', 'OLD1', 'Welder'),
			(1, '2025-01-01T00:00:00', '2025-01-01', 'NEW1', 'Foreman'),
			(1, '2026-01-01T00:00:00', '2026-01-01', 'BLNK', '  ')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SyncPatientFromLatestVisit(database, 1); err != nil {
		t.Fatal(err)
	}
	if got := patientPosition(t, database, 1); got != "Foreman" {
		t.Fatalf("position = %q, want Foreman (latest non-blank visit)", got)
	}
}

func TestSyncAllPatientsKeepsPositionWhenNoVisitHasOne(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, WorkPosition) VALUES ('Budi Test', '1991-02-03', 'M', 'Engineer')`); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncAllPatientsFromLatestVisit(database); err != nil {
		t.Fatal(err)
	}
	if got := patientPosition(t, database, 1); got != "Engineer" {
		t.Fatalf("visitless patient position = %q, want Engineer", got)
	}
}

// Height and blood type are measured at a checkup, but the patient row keeps
// the newest recorded value of each so a later checkup can start from it.
func TestSyncPatientFromLatestVisitCopiesHeightAndBloodType(t *testing.T) {
	database := openExportTestDB(t)
	for _, q := range []string{
		`INSERT INTO Patient (Name, DateOfBirth, Gender) VALUES ('Budi Test', '1990-01-02', 'M')`,
		`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, HeightCm, BloodType) VALUES
			(1, '2024-01-01T00:00:00', '2024-01-01', 'OLD1', 170.5, 'A'),
			(1, '2025-01-01T00:00:00', '2025-01-01', 'NEW1', 171.0, 'B'),
			(1, '2026-01-01T00:00:00', '2026-01-01', 'BLNK', NULL, '')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SyncPatientFromLatestVisit(database, 1); err != nil {
		t.Fatal(err)
	}
	var height sql.NullFloat64
	var blood sql.NullString
	if err := database.QueryRow(`SELECT HeightCm, BloodType FROM Patient WHERE PatientID = 1`).Scan(&height, &blood); err != nil {
		t.Fatal(err)
	}
	if !height.Valid || height.Float64 != 171.0 {
		t.Fatalf("height = %+v, want 171 (newest checkup that has one, blank 2026 skipped)", height)
	}
	if blood.String != "B" {
		t.Fatalf("blood type = %q, want B", blood.String)
	}
}
