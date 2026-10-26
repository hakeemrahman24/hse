package region

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", "013_region.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, sqlText); err != nil {
		t.Fatal(err)
	}
	rows := [][]any{
		{"11", "Aceh", 1, nil},
		{"94", "Papua", 1, nil},
		{"94.01", "Kab. Jayapura", 2, "94"},
		{"94.71", "Kota Jayapura", 2, "94"},
		{"94.01.05", "Wanggar", 3, "94.01"},
		{"94.01.05.2001", "Bakungan", 4, "94.01.05"},
		{"94.01.05.2002", "Aropi", 4, "94.01.05"},
	}
	for _, r := range rows {
		if _, err := database.Exec(`INSERT INTO Region (Code, Name, Level, ParentCode) VALUES (?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func get(t *testing.T, api *API, query string) (int, []item) {
	t.Helper()
	rec := httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions"+query, nil))
	var out []item
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func TestChildrenOfAParentAreSortedByName(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	code, got := get(t, api, "?parent=94.01.05")
	if code != http.StatusOK || len(got) != 2 || got[0].Name != "Aropi" || got[1].Code != "94.01.05.2001" {
		t.Fatalf("status %d got %+v", code, got)
	}
}

func TestLevelWithoutParentListsThatWholeLevel(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	_, regencies := get(t, api, "?level=2")
	if len(regencies) != 2 || regencies[0].Name != "Kab. Jayapura" {
		t.Fatalf("regencies = %+v", regencies)
	}
	_, provinces := get(t, api, "")
	if len(provinces) != 2 {
		t.Fatalf("default level should be provinces, got %+v", provinces)
	}
}

func TestEmptyResultIsAnEmptyListNotNull(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	rec := httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions?parent=00", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestBadInputIsRejected(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	for _, q := range []string{"?parent=x'%20OR%201=1", "?parent=94.01&level=2", "?level=9", "?level=abc"} {
		if code, _ := get(t, api, q); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
}

func TestSearchFindsAreasByNameWithTheirParents(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	rec := httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions?level=4&q=asa", nil))
	var got []map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(got) != 0 {
		t.Fatalf("no village matches 'asa' in the fixture, got %v", got)
	}

	rec = httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions?level=3&q=wang", nil))
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["code"] != "94.01.05" || got[0]["name"] != "Wanggar" ||
		got[0]["parentCode"] != "94.01" || got[0]["path"] != "Kab. Jayapura, Papua" {
		t.Fatalf("search result = %v", got)
	}
}

func TestSearchWordsMayComeFromTheParentsToo(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	count := func(query string) int {
		rec := httptest.NewRecorder()
		api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions?level=3&q="+query, nil))
		var got []item
		json.Unmarshal(rec.Body.Bytes(), &got)
		return len(got)
	}
	if n := count("wanggar%20jayapura"); n != 1 {
		t.Errorf("name + regency words: %d results, want 1", n)
	}
	if n := count("wanggar%20papua"); n != 1 {
		t.Errorf("name + province words: %d results, want 1", n)
	}
	if n := count("wanggar%20bandung"); n != 0 {
		t.Errorf("a word that matches nothing should exclude the row, got %d", n)
	}
	if n := count("%25"); n != 0 {
		t.Errorf("a literal %% must not act as a wildcard, got %d", n)
	}
	if n := count("a_a"); n != 0 {
		t.Errorf("a literal _ must not act as a wildcard, got %d", n)
	}
}

func TestSearchIsCappedByLimit(t *testing.T) {
	api := &API{DB: openTestDB(t)}
	rec := httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions?level=2&q=ja&limit=1", nil))
	var got []item
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 {
		t.Fatalf("limit=1 returned %d", len(got))
	}
	for _, q := range []string{"?level=2&q=ja&limit=0", "?level=2&q=ja&limit=abc", "?level=2&q=ja&limit=101", "?parent=94&q=ja"} {
		rec := httptest.NewRecorder()
		api.List(rec, httptest.NewRequest(http.MethodGet, "/api/regions"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rec.Code)
		}
	}
}
