package sofi

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
	"hse-medpost/internal/turnstile"
)

// newBareTestAPI is the API with no SOFI window scheduled, so /sofi is closed.
func newBareTestAPI(t *testing.T) *API {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"004_company.sql", "005_patient.sql", "006_entrymcu.sql", "015_sofi.sql", "018_sofi_window.sql", "019_sofi_period_note.sql", "024_sofi_preview.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := EnsureWindowColumn(database); err != nil {
		t.Fatal(err)
	}
	seed(t, database)
	api, err := New(database, "test-secret", filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	api.SetTurnstile(turnstile.DevVerifier{}, "", true)
	return api
}

// newTestAPI is newBareTestAPI plus one window that is open for all practical time, so the older
// tests (sign-in, submit, lists) run as if /sofi were open. The window tests use the bare one.
func newTestAPI(t *testing.T) *API {
	t.Helper()
	api := newBareTestAPI(t)
	_, err := api.DB.Exec(`INSERT INTO SofiWindow (Label, OpensAt, ClosesAt, CreatedAt, CreatedBy)
		VALUES ('test', '2000-01-01T00:00:00', '2999-01-01T00:00:00', '2000-01-01T00:00:00', 'test')`)
	if err != nil {
		t.Fatal(err)
	}
	return api
}

// passCheck does what the browser does after the Cloudflare tick: returns the check cookie.
func passCheck(t *testing.T, api *API) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/sofi/api/check", strings.NewReader(`{"token":"dev-pass"}`))
	rec := httptest.NewRecorder()
	api.Check(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == CheckCookieName {
			return c
		}
	}
	t.Fatalf("check did not set a cookie: %d %s", rec.Code, rec.Body.String())
	return nil
}

func seed(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Andi Pratama', '1990-05-14')`,
		`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Andini Putri', '1988-01-02')`,
		`INSERT INTO Patient (Name) VALUES ('Andre No Birthdate')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

type pick struct {
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Company string `json:"company"`
}

func searchNames(t *testing.T, api *API, q string) []pick {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sofi/api/names?q="+q, nil)
	req.AddCookie(passCheck(t, api))
	api.Names(rec, req)
	var out []pick
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func signIn(t *testing.T, api *API, ref, dob string) *httptest.ResponseRecorder {
	body := `{"ref":"` + ref + `","dob":"` + dob + `"}`
	req := httptest.NewRequest(http.MethodPost, "/sofi/api/signin", strings.NewReader(body))
	req.AddCookie(passCheck(t, api))
	rec := httptest.NewRecorder()
	api.SignIn(rec, req)
	return rec
}

func TestNamesAndSignInAreClosedUntilTheCloudflareCheckPasses(t *testing.T) {
	api := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.Names(rec, httptest.NewRequest(http.MethodGet, "/sofi/api/names?q=andi", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("names without check: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	api.SignIn(rec, httptest.NewRequest(http.MethodPost, "/sofi/api/signin", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("signin without check: %d", rec.Code)
	}
}

func TestFailedCloudflareCheckSetsNoCookie(t *testing.T) {
	api := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.Check(rec, httptest.NewRequest(http.MethodPost, "/sofi/api/check", strings.NewReader(`{"token":"wrong"}`)))
	if rec.Code != http.StatusBadRequest || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("status %d cookies %v", rec.Code, rec.Result().Cookies())
	}
}

func TestSignInPageIsClosedWithoutAVerifier(t *testing.T) {
	api := newTestAPI(t)
	api.SetTurnstile(nil, "", false)
	rec := httptest.NewRecorder()
	api.Check(rec, httptest.NewRequest(http.MethodPost, "/sofi/api/check", strings.NewReader(`{"token":"dev-pass"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("check passed with no verifier: %d", rec.Code)
	}
}

func TestNamesNeedsThreeLetters(t *testing.T) {
	api := newTestAPI(t)
	if got := searchNames(t, api, "an"); len(got) != 0 {
		t.Fatalf("2 letters returned %d names", len(got))
	}
}

func TestNamesMatchesPatientsWithABirthdateOnly(t *testing.T) {
	api := newTestAPI(t)
	got := searchNames(t, api, "and")
	if len(got) != 2 {
		t.Fatalf("got %d names, want 2 (a patient without a birthdate cannot sign in): %+v", len(got), got)
	}
	for _, p := range got {
		if p.Ref == "" || p.Label == "" {
			t.Fatalf("incomplete pick %+v", p)
		}
	}
}

func TestNamesTreatsLikeWildcardsAsText(t *testing.T) {
	api := newTestAPI(t)
	if got := searchNames(t, api, "%25%25%25"); len(got) != 0 {
		t.Fatalf("a literal percent sign matched %d names", len(got))
	}
}

func TestSignInWithRightBirthdateSetsASofiCookie(t *testing.T) {
	api := newTestAPI(t)
	got := searchNames(t, api, "andi%20p")
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	rec := signIn(t, api, got[0].Ref, "1990-05-14")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("no HttpOnly %s cookie: %+v", CookieName, rec.Result().Cookies())
	}
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(cookie)
	if s := api.session(req); s == nil || s.Name != "Andi Pratama" {
		t.Fatalf("session = %+v", s)
	}
}

func TestSignInWithWrongBirthdateFailsGenerically(t *testing.T) {
	api := newTestAPI(t)
	got := searchNames(t, api, "andi%20p")
	rec := signIn(t, api, got[0].Ref, "1990-05-15")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Andi") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("leaked a name or set a cookie: %s", rec.Body.String())
	}
}

func TestSignInRejectsATamperedRef(t *testing.T) {
	api := newTestAPI(t)
	if rec := signIn(t, api, "not-a-ref", "1990-05-14"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSignInLocksAfterRepeatedFailures(t *testing.T) {
	api := newTestAPI(t)
	got := searchNames(t, api, "andi%20p")
	for i := 0; i < failMax; i++ {
		signIn(t, api, got[0].Ref, "1990-05-15")
	}
	if rec := signIn(t, api, got[0].Ref, "1990-05-14"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("right date still accepted while locked: %d", rec.Code)
	}
}

// A staff session token must not open the SOFI pages.
func TestStaffTokenIsNotASofiSession(t *testing.T) {
	api := newTestAPI(t)
	staffToken, err := auth.NewStore("test-secret").Issue(auth.Session{UserID: 1, StaffID: 1, Role: "admin", Name: "Staff"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: staffToken})
	if s := api.session(req); s != nil {
		t.Fatalf("staff token accepted as SOFI session: %+v", s)
	}
}

func TestStartRedirectsToSignInWithoutASession(t *testing.T) {
	api := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.Start(rec, httptest.NewRequest(http.MethodGet, "/sofi/start", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/sofi" {
		t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
}

// The staff server parses every web/templates/*.html with plain template functions. A SOFI page
// that needs "tr" must not sit there, or the whole server fails to start.
func TestStaffTemplateGlobDoesNotPickUpSofiPages(t *testing.T) {
	if _, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html")); err != nil {
		t.Fatalf("staff templates no longer parse: %v", err)
	}
}
