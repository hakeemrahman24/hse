package entrymcu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowseLatestReturnsVisitWorkPosition(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Company (Name, Abbreviation) VALUES ('Acme', 'ACM')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, WorkPosition) VALUES ('Ani Test', 'Nurse')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, WorkPosition, FK_CompanyID, ResultCriteria, ResultValidUntil, CheckUpStatus) VALUES
		(1, '2026-01-01T00:00:00', '2026-01-01', 'MedC-old', 'Welder', 1, 'Fit', '2027-01-01', 'Close'),
		(1, '2026-06-01T00:00:00', '2026-06-01', 'MedC-new', 'Medic', 1, 'FitWithNote', '2027-06-01', 'Close')`); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database}
	rec := httptest.NewRecorder()
	api.GetMcuRecordsBrowse(rec, httptest.NewRequest(http.MethodGet, "/api/mcu-records/browse?latestOnly=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var rows []ReportRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("latest rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.CheckUpID != "MedC-new" || got.WorkPosition != "Medic" || got.PatientName != "Ani Test" || got.CompanyName != "Acme" {
		t.Fatalf("latest row = %+v", got)
	}
	if got.ResultCriteria == nil || *got.ResultCriteria != "FitWithNote" {
		t.Fatalf("result = %v", got.ResultCriteria)
	}
	if got.HcValidityStatus == "" {
		t.Fatal("expected a medical validity standing")
	}
}
