package directory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hse-medpost/internal/company"
)

func getCompanies(t *testing.T, api *API, token string) (int, []Company) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/companies", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.Companies(rec, req)
	var out []Company
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad json: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestCompaniesListsEverythingWithParentsEntityAndGov(t *testing.T) {
	database := openDirectoryTestDB(t)
	if err := company.EnsureCompanyArchivedColumn(database); err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO EntityType (EntityTypeID, Name) VALUES (1, 'Contractor'), (2, 'Client'), (3, 'Government')`)
	mustExec(t, database, `INSERT INTO Company (CompanyID, Name, Abbreviation, FK_EntityTypeID, Archived) VALUES
		(1, 'PT LNG', 'LNG', 2, 0),
		(2, 'PT Karya', NULL, 1, 0),
		(3, 'PT Sub Karya', NULL, 1, 0),
		(4, 'Dinas Ketenagakerjaan', 'GOV', 3, 0),
		(5, 'PT Lama', NULL, NULL, 1)`)
	mustExec(t, database, `INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID, Depth) VALUES
		('R1', 2, 1, 1), ('R2', 3, 2, 1), ('R3', 3, 1, 2)`)
	api := &API{DB: database, Token: "tok", Now: fixedNow}
	if err := EnsureToken(database, "tok"); err != nil {
		t.Fatal(err)
	}

	code, got := getCompanies(t, api, "tok")
	if code != http.StatusOK || len(got) != 5 {
		t.Fatalf("status %d, %d companies", code, len(got))
	}
	by := map[string]Company{}
	for _, c := range got {
		by[c.Name] = c
	}
	if c := by["PT LNG"]; c.Entity != "Client" || c.Abbreviation != "LNG" || c.Gov || len(c.Parents) != 0 || c.Parents == nil {
		t.Errorf("top-level company = %+v", c)
	}
	// Nearest parent first (depth 1 before depth 2).
	if c := by["PT Sub Karya"]; len(c.Parents) != 2 || c.Parents[0].Name != "PT Karya" || c.Parents[0].Depth != 1 || c.Parents[1].Name != "PT LNG" || c.Parents[1].Depth != 2 {
		t.Errorf("parents = %+v", c.Parents)
	}
	if c := by["Dinas Ketenagakerjaan"]; !c.Gov || c.Entity != "Government" {
		t.Errorf("government office = %+v", c)
	}
	if c := by["PT Lama"]; !c.Archived || c.Entity != "" {
		t.Errorf("archived company must be listed and flagged: %+v", c)
	}
}

func TestCompaniesNeedsTheToken(t *testing.T) {
	database := openDirectoryTestDB(t)
	if err := company.EnsureCompanyArchivedColumn(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToken(database, "tok"); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database, Token: "tok", Now: fixedNow}
	for _, tok := range []string{"", "wrong"} {
		if code, _ := getCompanies(t, api, tok); code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", tok, code)
		}
	}
}

func TestIsGov(t *testing.T) {
	for _, c := range []struct {
		entity, abbr string
		want         bool
	}{{"Government", "", true}, {"government", "x", true}, {"Contractor", "GOV", true}, {"Contractor", "gov", true}, {"Contractor", "LNG", false}, {"", "", false}} {
		if got := isGov(c.entity, c.abbr); got != c.want {
			t.Errorf("isGov(%q,%q) = %v", c.entity, c.abbr, got)
		}
	}
}
