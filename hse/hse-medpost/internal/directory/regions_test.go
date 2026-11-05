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
	want := []Region{
		{"11.01", "Kabupaten Aceh Selatan", "Aceh"},
		{"96.01", "Kabupaten Sorong", "Papua Barat Daya"},
		{"96.71", "Kota Sorong", "Papua Barat Daya"},
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: %+v want %+v", i, rows[i], want[i])
		}
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
