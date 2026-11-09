package patient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

// The employee list carries each person's latest MCU date and the date it is valid until, so a
// reader of the list does not have to open every history. "Latest" is by checkup date (else review
// date), not by when the row was typed in.
func TestListPatientsAddsTheLatestMcuDateAndValidUntil(t *testing.T) {
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"004_company.sql", "005_patient.sql", "006_entrymcu.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Columns the app adds at startup rather than in 005/006.
	for _, q := range []string{
		`ALTER TABLE Patient ADD COLUMN PersonalID TEXT NULL`,
		`ALTER TABLE McuRecord ADD COLUMN BloodType TEXT NULL`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	insertWorker := func(name string) int64 {
		res, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, WorkPosition) VALUES (?, '1990-04-12', 'Welder')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	addRecord := func(patientID int64, checkUpID, done, review, until string) {
		var validUntil any
		if until != "" {
			validUntil = until
		}
		if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, DateCheckUp, CheckUpID, ResultValidUntil, WorkPosition)
			VALUES (?, '2026-01-01T00:00:00', ?, ?, ?, ?, 'Welder')`, patientID, review, done, checkUpID, validUntil); err != nil {
			t.Fatal(err)
		}
	}
	withHistory, open := insertWorker("A Has History"), insertWorker("B Open Checkup")
	insertWorker("C No Checkup")
	// Entered newest-first on purpose: the latest by date must win, not the last row inserted.
	addRecord(withHistory, "MedC-NEW", "2026-06-01", "2026-06-03", "2027-06-01")
	addRecord(withHistory, "MedC-OLD", "2025-06-01", "2025-06-03", "2026-06-01")
	addRecord(open, "MedC-OPEN", "", "2026-08-15", "") // no checkup date: the review date stands in; still open

	rec := httptest.NewRecorder()
	(&API{DB: database}).ListPatients(rec, httptest.NewRequest(http.MethodGet, "/api/patients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got []Patient
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"A Has History":  {"2026-06-01", "2027-06-01"},
		"B Open Checkup": {"2026-08-15", ""},
		"C No Checkup":   {"", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("%d patients, want %d", len(got), len(want))
	}
	for _, p := range got {
		if w := want[p.Name]; p.MCUDate != w[0] || p.MCUValidUntil != w[1] {
			t.Errorf("%s: mcuDate %q mcuValidUntil %q, want %q %q", p.Name, p.MCUDate, p.MCUValidUntil, w[0], w[1])
		}
	}
	if raw := rec.Body.String(); !contains(raw, `"mcuDate"`) || !contains(raw, `"mcuValidUntil"`) {
		t.Errorf("the JSON keys are not mcuDate and mcuValidUntil: %s", raw)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
