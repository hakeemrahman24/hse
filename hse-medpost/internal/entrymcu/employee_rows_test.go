package entrymcu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// seedEmployees adds one patient with a checkup and one without.
func seedEmployees(t *testing.T, api *API) {
	t.Helper()
	seedNewPatientLookups(t, api)
	stmts := []string{
		`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_NationalityID, FK_CompanyID) VALUES ('Has Checkup', 'F', '1990-01-02', 1, 1)`,
		`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_NationalityID, FK_CompanyID) VALUES ('No Checkup', 'M', '1985-05-05', 1, 1)`,
		`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, FK_CompanyID, ResultCriteria, CheckUpStatus) VALUES (1, '2026-09-15T00:00:00', '2026-09-15', 'EMP1', 1, 'Fit', 'Close')`,
	}
	for _, stmt := range stmts {
		if _, err := api.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func browseRows(t *testing.T, api *API, query string) []ReportRow {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mcu-records/browse"+query, nil)
	api.GetMcuRecordsBrowse(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rows []ReportRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestEmployeeTabListsEveryPatientIncludingNoCheckup(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedEmployees(t, api)

	rows := browseRows(t, api, "?latestOnly=1")

	if len(rows) != 2 {
		t.Fatalf("employee rows = %d, want 2: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.PatientName == "No Checkup" && (r.McuRecordID != 0 || r.PatientID == 0) {
			t.Fatalf("patient without checkup has McuRecordID %d, PatientID %d", r.McuRecordID, r.PatientID)
		}
		if r.PatientName == "Has Checkup" && r.CheckUpID != "EMP1" {
			t.Fatalf("latest checkup = %q, want EMP1", r.CheckUpID)
		}
	}
}

func TestMcuDataTabStillListsOnlyCheckups(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedEmployees(t, api)

	rows := browseRows(t, api, "")

	if len(rows) != 1 || rows[0].PatientName != "Has Checkup" {
		t.Fatalf("MCU data rows = %+v, want only the checkup row", rows)
	}
}
