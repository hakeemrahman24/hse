package entrymcu

import (
	"strconv"
	"strings"
	"testing"
)

func TestImportRejectsPatientIDWithDifferentName(t *testing.T) {
	database := openExportTestDB(t)
	a := &API{DB: database}
	res, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Ana Example', '1990-01-02')`)
	if err != nil {
		t.Fatal(err)
	}
	patientID, _ := res.LastInsertId()
	id := strconv.FormatInt(patientID, 10)

	header := []string{"PatientID", "PatientName", "DateOfBirth", "CheckUpID", "ReviewDate"}
	get := cellGetter(headerIndex(header))

	mismatched := []string{id, "Other Person", "1990-01-02", "PAT-1", "2025-01-01"}
	if err := a.importOneRow(mismatched, get, importColumnSet{}); err == nil || !strings.Contains(err.Error(), "Ana Example") {
		t.Fatalf("mismatched name: err = %v, want rejection naming the stored patient", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM McuRecord WHERE CheckUpID = 'PAT-1'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected row was written: count=%d err=%v", count, err)
	}

	matching := []string{id, "Ana Example", "1990-01-02", "PAT-1", "2025-01-01"}
	if err := a.importOneRow(matching, get, importColumnSet{}); err != nil {
		t.Fatalf("matching name: %v", err)
	}
}
