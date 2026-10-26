package company

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"hse-medpost/internal/db"
)

func openArchiveTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, stmt := range []string{
		`CREATE TABLE EntityType (EntityTypeID INTEGER PRIMARY KEY, Name TEXT NOT NULL)`,
		`CREATE TABLE Company (CompanyID INTEGER PRIMARY KEY AUTOINCREMENT, Name TEXT NOT NULL UNIQUE, Abbreviation TEXT NULL, FK_EntityTypeID INTEGER NULL)`,
		`INSERT INTO EntityType (EntityTypeID, Name) VALUES (1, 'Client')`,
		`INSERT INTO Company (Name, Abbreviation, FK_EntityTypeID) VALUES ('Layar Nusantara Gas', 'LNG', 1), ('Starway Engineering Indonesia', 'SEI', NULL)`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func archiveTestRouter(a *API) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/companies", a.ListCompanies)
	mux.HandleFunc("PUT /api/companies/{id}", a.UpdateCompany)
	return mux
}

func listCompanyNames(t *testing.T, mux *http.ServeMux, query string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/companies"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rec.Code, rec.Body.String())
	}
	var out []Company
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(out))
	for _, c := range out {
		names = append(names, c.Name)
	}
	return names
}

func TestCompanyArchiveHidesFromDefaultListAndRestores(t *testing.T) {
	database := openArchiveTestDB(t)
	if err := EnsureCompanyArchivedColumn(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCompanyArchivedColumn(database); err != nil {
		t.Fatalf("second run should be a no-op: %v", err)
	}
	mux := archiveTestRouter(&API{DB: database})

	if got := listCompanyNames(t, mux, ""); len(got) != 2 {
		t.Fatalf("before archive: %v", got)
	}

	setArchived := func(id int64, archived bool) {
		body, _ := json.Marshal(map[string]bool{"archived": archived})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/companies/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("archived=%v status %d: %s", archived, rec.Code, rec.Body.String())
		}
	}

	setArchived(2, true)
	if got := listCompanyNames(t, mux, ""); len(got) != 1 || got[0] != "Layar Nusantara Gas" {
		t.Fatalf("archived company still in default list: %v", got)
	}
	if got := listCompanyNames(t, mux, "?includeArchived=1"); len(got) != 2 {
		t.Fatalf("includeArchived should show both: %v", got)
	}

	setArchived(2, false)
	if got := listCompanyNames(t, mux, ""); len(got) != 2 {
		t.Fatalf("restored company missing from default list: %v", got)
	}
}

func TestCompanyUpdateEditsNameAndAbbreviation(t *testing.T) {
	database := openArchiveTestDB(t)
	if err := EnsureCompanyArchivedColumn(database); err != nil {
		t.Fatal(err)
	}
	mux := archiveTestRouter(&API{DB: database})

	body := []byte(`{"name":"Starway Engineering Indonesia Group","abbreviation":"SEI-PT"}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/companies/2", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit status %d: %s", rec.Code, rec.Body.String())
	}
	var name, abbr string
	if err := database.QueryRow(`SELECT Name, Abbreviation FROM Company WHERE CompanyID = 2`).Scan(&name, &abbr); err != nil {
		t.Fatal(err)
	}
	if name != "Starway Engineering Indonesia Group" || abbr != "SEI-PT" {
		t.Fatalf("after edit name=%q abbr=%q", name, abbr)
	}
}
