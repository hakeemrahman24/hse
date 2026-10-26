package directory

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

func openActivitiesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"001_sqlite.sql", "003_subheaders.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO HealthProcedureTable (ProcID, HealthProcedure) VALUES (1, 'HealthScreening')`,
		`INSERT INTO MedpostStaffTable (StaffID, StaffName) VALUES (1, 'Nurse Secret')`,
		`INSERT INTO ActivityTable (ActivityID, FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivitySummary, Location, ActivityDate, Notes)
		   VALUES (1, 1, 1, 'Socialization', 'Toolbox talk', 'Hand hygiene', 'Camp', '2026-10-01', 'internal note')`,
		`INSERT INTO ActivityTable (ActivityID, FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate)
		   VALUES (2, 1, 1, 'Monitoring/Inspection', 'Kitchen check', '2026-10-05')`,
		`INSERT INTO ActivityTable (ActivityID, FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate)
		   VALUES (3, 1, 1, 'Monitoring/Inspection', 'Old one', '2026-01-02')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func getActivities(t *testing.T, api *API, query, token string) (int, []Activity, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/activities"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.Activities(rec, req)
	var rows []Activity
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, rows, rec.Body.String()
}

func TestActivitiesNeedsTheToken(t *testing.T) {
	api := &API{DB: openActivitiesTestDB(t), Token: "secret", Now: fixedNow}
	if code, _, _ := getActivities(t, api, "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", code)
	}
	if code, _, _ := getActivities(t, api, "", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", code)
	}
	unset := &API{DB: openActivitiesTestDB(t), Now: fixedNow}
	if code, _, _ := getActivities(t, unset, "", "anything"); code != http.StatusServiceUnavailable {
		t.Errorf("no token configured: status %d, want 503", code)
	}
}

func TestActivitiesListsNewestFirstWithoutPrivateFields(t *testing.T) {
	api := &API{DB: openActivitiesTestDB(t), Token: "secret", Now: fixedNow}
	code, rows, body := getActivities(t, api, "", "secret")
	if code != http.StatusOK || len(rows) != 3 {
		t.Fatalf("status %d rows %d: %s", code, len(rows), body)
	}
	if rows[0].Title != "Kitchen check" || rows[1].Title != "Toolbox talk" || rows[2].Title != "Old one" {
		t.Errorf("order = %v", []string{rows[0].Title, rows[1].Title, rows[2].Title})
	}
	if rows[1].Program != "Health Screening" || rows[1].Summary != "Hand hygiene" || rows[1].Location != "Camp" || rows[1].Date != "2026-10-01" {
		t.Errorf("row = %+v", rows[1])
	}
	for _, secret := range []string{"Nurse Secret", "internal note"} {
		if bytesContains(body, secret) {
			t.Errorf("the answer leaks %q", secret)
		}
	}
}

func TestActivitiesFiltersAndLimits(t *testing.T) {
	api := &API{DB: openActivitiesTestDB(t), Token: "secret", Now: fixedNow}
	if _, rows, _ := getActivities(t, api, "?from=2026-10-01&to=2026-10-04", "secret"); len(rows) != 1 || rows[0].Title != "Toolbox talk" {
		t.Errorf("date range rows = %+v", rows)
	}
	if _, rows, _ := getActivities(t, api, "?limit=1", "secret"); len(rows) != 1 {
		t.Errorf("limit rows = %d, want 1", len(rows))
	}
	for _, bad := range []string{"?from=yesterday", "?to=2026-13-99x", "?from=2026-10-05&to=2026-10-01", "?limit=0", "?limit=abc"} {
		if code, _, _ := getActivities(t, api, bad, "secret"); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
}

func TestActivitiesLeavesOutSubHeaderEntries(t *testing.T) {
	database := openActivitiesTestDB(t)
	if _, err := database.Exec(`INSERT INTO SubHeaderTable (SubHeaderID, FK_ProcID, SubHeaderName) VALUES (1, 1, 'Sub')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, FK_SubHeaderID, ActivityType, ActivityTitle, ActivityDate)
		VALUES (1, 1, 1, 'Socialization', 'Inside a sub-header', '2026-10-06')`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database, Token: "secret", Now: fixedNow}
	if _, rows, _ := getActivities(t, api, "", "secret"); len(rows) != 3 {
		t.Errorf("rows = %d, want 3 (sub-header entries excluded)", len(rows))
	}
}
