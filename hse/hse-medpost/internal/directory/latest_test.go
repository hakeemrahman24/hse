package directory

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func latestAPI(t *testing.T) *API {
	t.Helper()
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	mustExec(t, database, `INSERT INTO Patient (PatientID, Name, Gender, DateOfBirth, EmployeeID, FK_CompanyID) VALUES (5, 'Tanpa Checkup', 'Male', '1990-01-01', 'E-5', 2)`)
	return &API{DB: database, Token: "eligible", FullToken: "secret", Now: fixedNow}
}

func byCheckUpID(rows []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["checkUpId"].(string)] = r
	}
	return out
}

// One row per employee, the newest checkup whatever its result; people with no checkup are absent.
func TestLatestCheckupsIsOneRowPerEmployeeWhateverTheResult(t *testing.T) {
	api := latestAPI(t)
	rows := getJSONList(t, api, "/api/directory/latest-checkups", api.LatestCheckups)
	if len(rows) != 4 {
		t.Fatalf("%d rows, want 4 (Andi, Sari, Budi, Lama; not the worker with no checkup): %v", len(rows), rows)
	}
	got := byCheckUpID(rows)
	for _, id := range []string{"MedC-new", "MedC-note", "MedC-unfit", "MedC-expired"} {
		if got[id] == nil {
			t.Errorf("missing the latest checkup %s", id)
		}
	}
	if got["MedC-old"] != nil {
		t.Error("an older checkup of the same person must not appear")
	}
	if r := got["MedC-unfit"]; r["resultCriteria"] != "Unfit" {
		t.Errorf("unfit workers must be listed, got %v", r)
	}
	if r := got["MedC-expired"]; r["hcValidityStatus"] != "ReEvaluate / Overdue" {
		t.Errorf("overdue standing = %v", r["hcValidityStatus"])
	}
	for _, key := range []string{"dateCheckUp", "resultValidUntil", "reviewDate", "checkUpStatus", "recommendation", "company", "parents"} {
		if _, ok := got["MedC-new"][key]; !ok {
			t.Errorf("field %s is missing", key)
		}
	}
	for _, key := range []string{"personalId", "visionCriteria"} {
		if _, ok := got["MedC-new"][key]; ok {
			t.Errorf("%s must not be in a status feed", key)
		}
	}
}

// Unlike the employee search, a newer unfinished or Incomplete checkup IS the latest here.
func TestLatestCheckupsShowsANewerIncompleteOne(t *testing.T) {
	api := latestAPI(t)
	insertMCU(t, api.DB, 2, "MedC-inc", "2026-06-01", "IncompleteResult", "", "", "Open")
	got := byCheckUpID(getJSONList(t, api, "/api/directory/latest-checkups?patientId=2", api.LatestCheckups))
	if len(got) != 1 || got["MedC-inc"] == nil || got["MedC-inc"]["resultCriteria"] != "Incomplete" {
		t.Fatalf("latest of Sari = %v, want the Incomplete one", got)
	}
}

func TestLatestCheckupsFiltersApplyToTheLatestCheckup(t *testing.T) {
	api := latestAPI(t)
	cases := []struct {
		query string
		want  []string
	}{
		{"?result=Unfit", []string{"MedC-unfit"}},
		{"?result=Fit%20with%20note", []string{"MedC-note"}},
		{"?result=FitWithNote", []string{"MedC-note"}},
		{"?result=Fit", []string{"MedC-new", "MedC-expired"}},
		{"?q=santo", []string{"MedC-unfit"}},
		{"?employeeId=E-1", []string{"MedC-new"}},
		{"?from=2026-02-01&to=2026-03-31", []string{"MedC-note", "MedC-unfit"}},
		// MedC-old is Andi's OLD checkup: filtering by its date finds nobody, because it is not his latest.
		{"?from=2025-01-01&to=2025-12-31", nil},
	}
	for _, c := range cases {
		got := byCheckUpID(getJSONList(t, api, "/api/directory/latest-checkups"+c.query, api.LatestCheckups))
		if len(got) != len(c.want) {
			t.Errorf("%s: %d rows %v, want %v", c.query, len(got), got, c.want)
			continue
		}
		for _, id := range c.want {
			if got[id] == nil {
				t.Errorf("%s: missing %s", c.query, id)
			}
		}
	}
}

func TestLatestCheckupsNeedsTheAllCheckupsTokenAndValidFilters(t *testing.T) {
	api := latestAPI(t)
	for _, c := range []struct {
		path, token string
		want        int
	}{
		{"/api/directory/latest-checkups", "eligible", http.StatusUnauthorized},
		{"/api/directory/latest-checkups", "", http.StatusUnauthorized},
		{"/api/directory/latest-checkups", "secret", http.StatusOK},
		{"/api/directory/latest-checkups?from=01-02-2026", "secret", http.StatusBadRequest},
		{"/api/directory/latest-checkups?result=bogus", "secret", http.StatusBadRequest},
		{"/api/directory/latest-checkups?patientId=abc", "secret", http.StatusBadRequest},
		{"/api/directory/checkups?to=nope", "secret", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		if c.path[:len("/api/directory/checkups")] == "/api/directory/checkups" {
			api.Checkups(rec, req)
		} else {
			api.LatestCheckups(rec, req)
		}
		if rec.Code != c.want {
			t.Errorf("%s with %q: status %d, want %d", c.path, c.token, rec.Code, c.want)
		}
	}
}

// The existing route keeps returning everything without filters and narrows with them.
func TestCheckupsCanBeFiltered(t *testing.T) {
	api := latestAPI(t)
	if rows := getJSONList(t, api, "/api/directory/checkups", api.Checkups); len(rows) != 5 {
		t.Fatalf("no filter: %d rows, want all 5", len(rows))
	}
	cases := []struct {
		query string
		want  int
	}{
		{"?patientId=1", 2},
		{"?checkUpId=MedC-old", 1},
		{"?employeeId=E-3", 1},
		{"?result=Fit", 3},
		{"?from=2026-01-01", 3},
		{"?patientId=1&result=Fit&from=2026-01-01", 1},
	}
	for _, c := range cases {
		if rows := getJSONList(t, api, "/api/directory/checkups"+c.query, api.Checkups); len(rows) != c.want {
			t.Errorf("%s: %d rows, want %d", c.query, len(rows), c.want)
		}
	}
}
