package entrymcu

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newPatientBody returns a valid new-patient request. Tests change one field at
// a time to check that a failure in either half leaves the database untouched.
func newPatientBody(patientName string) map[string]any {
	today := time.Now().Format("2006-01-02")
	return map[string]any{
		"patient": map[string]any{
			"name":          patientName,
			"gender":        "F",
			"dateOfBirth":   "1990-01-02",
			"nationalityId": 1,
			"companyId":     1,
			"workPosition":  "Nurse",
			"personalId":    "",
		},
		"checkup": map[string]any{
			"timestamp":         today + "T08:00:00",
			"reviewDate":        today,
			"checkUpId":         "NEWP1",
			"checkUpCriteriaId": 1,
			"workLocation":      "Site A",
			"companyId":         1,
			"workPosition":      "Nurse",
			"dateCheckUp":       today,
			"submissionDate":    today,
			"resultCriteria":    "Fit",
		},
	}
}

func postNewPatient(t *testing.T, api *API, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcu-records/new-patient", bytes.NewReader(raw))
	api.CreateNewPatientWithCheckup(rec, req)
	return rec
}

func countRows(t *testing.T, api *API, table string) int {
	t.Helper()
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedNewPatientLookups(t *testing.T, api *API) {
	t.Helper()
	if _, err := api.DB.Exec(`INSERT INTO NationalityTable (Name) VALUES ('Indonesia')`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.DB.Exec(`INSERT INTO Company (Name, Abbreviation) VALUES ('Acme', 'ACM')`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.DB.Exec(`INSERT INTO CheckUpCriteriaTable (Name) VALUES ('Annual')`); err != nil {
		t.Fatal(err)
	}
}

func TestNewPatientWithCheckupSavesBothRows(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedNewPatientLookups(t, api)

	rec := postNewPatient(t, api, newPatientBody("Ani Test"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, api, "Patient"); n != 1 {
		t.Fatalf("patients = %d, want 1", n)
	}
	if n := countRows(t, api, "McuRecord"); n != 1 {
		t.Fatalf("checkups = %d, want 1", n)
	}
}

func TestNewPatientWithCheckupWritesNothingWhenCheckupInvalid(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedNewPatientLookups(t, api)
	body := newPatientBody("Ani Test")
	delete(body["checkup"].(map[string]any), "submissionDate")

	rec := postNewPatient(t, api, body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, api, "Patient"); n != 0 {
		t.Fatalf("patient saved although checkup was invalid: %d rows", n)
	}
	if n := countRows(t, api, "McuRecord"); n != 0 {
		t.Fatalf("checkup saved although it was invalid: %d rows", n)
	}
}

func TestNewPatientWithCheckupRejectsDuplicateWithoutSaving(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedNewPatientLookups(t, api)
	if rec := postNewPatient(t, api, newPatientBody("Ani Test")); rec.Code != http.StatusCreated {
		t.Fatalf("first save status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := newPatientBody("ani test")
	body["checkup"].(map[string]any)["checkUpId"] = "NEWP2"
	rec := postNewPatient(t, api, body)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, api, "Patient"); n != 1 {
		t.Fatalf("patients = %d, want 1", n)
	}
	if n := countRows(t, api, "McuRecord"); n != 1 {
		t.Fatalf("checkups = %d, want 1 (the duplicate patient must not get a checkup)", n)
	}
}

func TestNewPatientWithoutCompanyTakesCompanyFromFirstVisit(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedNewPatientLookups(t, api)
	body := newPatientBody("Ani Test")
	delete(body["patient"].(map[string]any), "companyId")

	rec := postNewPatient(t, api, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if c := patientCompany(t, api.DB, 1); !c.Valid || c.Int64 != 1 {
		t.Fatalf("patient company = %+v, want 1 (from the first visit)", c)
	}
}

func TestNewPatientWithoutPositionTakesPositionFromFirstVisit(t *testing.T) {
	api := &API{DB: openExportTestDB(t)}
	seedNewPatientLookups(t, api)
	body := newPatientBody("Ani Test")
	delete(body["patient"].(map[string]any), "workPosition")
	body["checkup"].(map[string]any)["workPosition"] = "Rigger"

	rec := postNewPatient(t, api, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := patientPosition(t, api.DB, 1); got != "Rigger" {
		t.Fatalf("patient position = %q, want Rigger (from the first visit)", got)
	}
}
