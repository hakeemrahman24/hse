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

func openRegionsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, stmt := range []string{
		`CREATE TABLE Region (Code TEXT PRIMARY KEY, Name TEXT NOT NULL, Level INTEGER NOT NULL, ParentCode TEXT)`,
		`INSERT INTO Region VALUES ('96', 'Papua Barat Daya', 1, NULL)`,
		`INSERT INTO Region VALUES ('11', 'Aceh', 1, NULL)`,
		`INSERT INTO Region VALUES ('96.71', 'Kota Sorong', 2, '96')`,
		`INSERT INTO Region VALUES ('96.01', 'Kabupaten Sorong', 2, '96')`,
		`INSERT INTO Region VALUES ('11.01', 'Kabupaten Aceh Selatan', 2, '11')`,
		`INSERT INTO Region VALUES ('11.01.01', 'Bakongan', 3, '11.01')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func getRegions(t *testing.T, api *API, token string) (int, []Region) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/regions", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.Regions(rec, req)
	var rows []Region
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, rows
}

func TestRegionsNeedsTheToken(t *testing.T) {
	api := &API{DB: openRegionsTestDB(t), Token: "secret", Now: fixedNow}
	if code, _ := getRegions(t, api, ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := getRegions(t, api, "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	unset := &API{DB: openRegionsTestDB(t), Now: fixedNow}
	if code, _ := getRegions(t, unset, "anything"); code != http.StatusServiceUnavailable {
		t.Errorf("not configured: %d", code)
	}
}

func TestRegionsListsRegenciesWithProvinceOnly(t *testing.T) {
	api := &API{DB: openRegionsTestDB(t), Token: "secret", Now: fixedNow}
	code, rows := getRegions(t, api, "secret")
	if code != http.StatusOK || len(rows) != 3 {
		t.Fatalf("%d %+v", code, rows)
	}
	// Ordered by province then name; districts (level 3) and provinces are not listed.
	want := []struct{ code, name, province string }{
		{"11.01", "Kabupaten Aceh Selatan", "Aceh"},
		{"96.01", "Kabupaten Sorong", "Papua Barat Daya"},
		{"96.71", "Kota Sorong", "Papua Barat Daya"},
	}
	for i, w := range want {
		if rows[i].Code != w.code || rows[i].Name != w.name || rows[i].Province != w.province {
			t.Errorf("row %d: %+v want %+v", i, rows[i], w)
		}
		// This database has no endemicity tables (migration 031/047 not run): the regency list
		// must still be served, with every regency "unknown", never "free".
		if rows[i].Endemicity != (Endemicity{Status: "unknown"}) {
			t.Errorf("row %d endemicity without the tables: %+v", i, rows[i].Endemicity)
		}
	}
}

func TestRegionsCarryTheAppliedEndemicity(t *testing.T) {
	database := openRegionsTestDB(t)
	sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", "031_data_sync.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, sqlText); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO MalariaEndemicity VALUES ('96.01', 'Kabupaten Sorong', 'Papua Barat Daya', 'Endemis Tinggi I', 2025, 1141, 124207, '2026-11-13 11:31:55')`,
		`INSERT INTO MalariaEndemicity VALUES ('11.01', 'Kabupaten Aceh Selatan', 'Aceh', 'Eliminasi', 2024, 0, 100000, '')`,
		`UPDATE DataSyncSetting SET AppliedAt = '2026-11-14T08:00:00' WHERE Source = 'endemicity'`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	api := &API{DB: database, Token: "secret", Now: fixedNow}
	_, rows := getRegions(t, api, "secret")
	byCode := map[string]Endemicity{}
	for _, r := range rows {
		byCode[r.Code] = r.Endemicity
	}
	if got := byCode["96.01"]; got != (Endemicity{Status: "high", Label: "Endemis Tinggi I", DataYear: 2025, Per1000: 9.19, AppliedAt: "2026-11-14"}) {
		t.Errorf("endemic regency: %+v", got)
	}
	if got := byCode["11.01"]; got != (Endemicity{Status: "free", Label: "Eliminasi", Free: true, DataYear: 2024, AppliedAt: "2026-11-14"}) {
		t.Errorf("free regency: %+v", got)
	}
	if got := byCode["96.71"]; got != (Endemicity{Status: "unknown"}) {
		t.Errorf("a regency not on the list must be unknown, not free: %+v", got)
	}
}

func TestRegionsReportsDatabaseError(t *testing.T) {
	database := openRegionsTestDB(t)
	database.Close()
	api := &API{DB: database, Token: "secret", Now: fixedNow}
	if code, _ := getRegions(t, api, "secret"); code != http.StatusInternalServerError {
		t.Errorf("closed db: %d", code)
	}
}
