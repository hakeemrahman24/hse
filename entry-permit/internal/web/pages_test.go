package web

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"entry-permit/internal/company"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/filestore"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/mail"
	"entry-permit/internal/malaria"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/position"
	"entry-permit/internal/ratelimit"
	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/settings"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/turnstile"
	"entry-permit/internal/waiverlog"
	"entry-permit/migrations"
)

type testEnv struct {
	srv       *httptest.Server
	store     *staff.Store
	clock     *movingClock
	db        *sql.DB
	workers   *fakeWorkers
	companies *fakeCompanies
	uploads   string
	mx        *mxStub
	mailer    *fakeMail
	app       *App
}

// mxStub stands in for the DNS lookup that checks an email domain can receive mail.
type mxStub struct {
	err     error
	badOnly string // when set, only this domain fails
	domains []string
}

func (m *mxStub) Check(domain string) error {
	m.domains = append(m.domains, domain)
	if m.badOnly != "" && domain != m.badOnly {
		return nil
	}
	return m.err
}

type movingClock struct{ now dates.Moment }

func (c *movingClock) Now() dates.Moment { return c.now }
func (c *movingClock) Today() dates.Day  { return c.now.Day() }

// fakeRegions stands in for the hse-medpost region API in tests.
type fakeRegions struct{ list []region.Regency }

func (f fakeRegions) Regencies(context.Context) ([]region.Regency, error) { return f.list, nil }

var testRegencies = []region.Regency{
	{Code: "11.01", Name: "Kabupaten Aceh Selatan", Province: "Aceh"},
	{Code: "96.01", Name: "Kabupaten Sorong", Province: "Papua Barat Daya"},
	{Code: "96.71", Name: "Kota Sorong", Province: "Papua Barat Daya"},
}

func newEnv(t *testing.T) *testEnv { return newEnvWith(t, fakeRegions{testRegencies}) }

func newEnvWith(t *testing.T, src region.Source) *testEnv {
	t.Helper()
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	clock := &movingClock{now: "2026-11-06 08:00:00"}
	pol := policy.Default()
	workers := &fakeWorkers{}
	companies := &fakeCompanies{}
	cfgSvc := settings.New(d, clock)
	if err := cfgSvc.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := staff.NewStore(d, clock, pol).WithPolicy(cfgSvc.Policy).WithCost(bcrypt.MinCost)
	lists := reflist.NewStore(d, clock)
	posStore := &position.Store{DB: d}
	if err := posStore.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	mx := &mxStub{}
	mailer := &fakeMail{}
	app, err := NewApp("../../web", App{Positions: posStore, Uploads: filestore.Store{Dir: uploads}, MXCheck: mx.Check, Store: store, Clock: clock, Pol: pol, PolicyFn: cfgSvc.Policy, Settings: cfgSvc, Officers: gateofficer.New(d, clock).WithIterations(5), Workers: workers, Permits: &permitlog.Store{DB: d}, Waivers: &waiverlog.Store{DB: d}, Tickets: &ticketlog.Store{DB: d}, Reviews: &reviewlog.Store{DB: d}, Companies: &company.Store{DB: d, Clock: clock}, CompanySource: companies, Turnstile: turnstile.DevVerifier{}, TurnstileDev: true, Limit: ratelimit.New(), Secret: "test-secret", BaseURL: "https://permit.example", Outbox: &mail.Outbox{DB: d}, Mailer: mailer, DB: d, SQL: d, Lists: lists,
		Malaria: &malaria.Service{Lists: lists, Regions: src}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, store: store, clock: clock, db: d, workers: workers, companies: companies, uploads: uploads, mx: mx, mailer: mailer, app: app}
}

func (e *testEnv) addUser(t *testing.T, username, name string, roles ...staff.Role) {
	t.Helper()
	if _, err := e.store.Create(context.Background(), username, name, "secret-mail@example.com", "1234", roles); err != nil {
		t.Fatal(err)
	}
}

// client does not follow redirects, so tests can assert on them, and keeps cookies by hand.
func noFollow() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *testEnv) login(t *testing.T, username, pin string) *http.Response {
	t.Helper()
	// "dev-pass" is the token the development security check accepts (the page fills it in itself).
	resp, err := noFollow().PostForm(e.srv.URL+"/login", url.Values{"username": {username}, "pin": {pin}, turnstileField: {"dev-pass"}})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// postForm posts without following redirects and returns the response.
func (e *testEnv) postForm(t *testing.T, path string, cookies []*http.Cookie, v url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// postBody posts and returns the status and the body.
func (e *testEnv) postBody(t *testing.T, path string, cookies []*http.Cookie, v url.Values) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *testEnv) get(t *testing.T, path string, cookies []*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, sb.String()
}

func TestHomeHasRequestAndSignInLinks(t *testing.T) {
	e := newEnv(t)
	resp, body := e.get(t, "/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d", resp.StatusCode)
	}
	for _, want := range []string{`href="/request"`, `href="/my/signin"`} {
		if !strings.Contains(body, want) {
			t.Errorf("home page lacks %q", want)
		}
	}
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("security headers missing")
	}
	if r, _ := e.get(t, "/nope", nil); r.StatusCode != 404 {
		t.Errorf("unknown path: %d", r.StatusCode)
	}
}

// The old "staff dropdown lists names only" test is replaced by signin_test.go
// (TestLoginPageIsTheNameStepAndListsNoStaffNames): the page no longer lists any staff name.

func TestLoginLandsOnTheSharedLandingPage(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "Ann Lee", staff.Stage1, staff.Doctor)
	resp := e.login(t, "ann", "1234")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: %+v", cookies)
	}
	// /staff sends the user straight to the highest-priority page: stage1.
	r, _ := e.get(t, "/staff", cookies)
	if r.StatusCode != 303 || r.Header.Get("Location") != "/staff/stage1" {
		t.Fatalf("landing: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	_, page := e.get(t, "/staff/stage1", cookies)
	if !strings.Contains(page, "Stage 1 review") || !strings.Contains(page, "Doctor review") || !strings.Contains(page, "/staff/dashboard") {
		t.Error("page should show own pages and a dashboard link")
	}
	// Dashboard is open to every staff member; a role they lack is refused.
	if r, _ := e.get(t, "/staff/dashboard", cookies); r.StatusCode != 200 {
		t.Errorf("dashboard: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/staff/admin", cookies); r.StatusCode != 403 {
		t.Errorf("admin page: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/staff/unknown", cookies); r.StatusCode != 404 {
		t.Errorf("unknown page: %d", r.StatusCode)
	}
	// The login page sends an already signed-in user on.
	if r, _ := e.get(t, "/login", cookies); r.StatusCode != 303 {
		t.Errorf("login while signed in: %d", r.StatusCode)
	}
}

func TestViewerLandsOnDashboard(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "viewer", "Viewer", staff.Dashboard)
	cookies := e.login(t, "viewer", "1234").Cookies()
	if r, _ := e.get(t, "/staff", cookies); r.Header.Get("Location") != "/staff/dashboard" {
		t.Errorf("%s", r.Header.Get("Location"))
	}
}

func TestNotLoggedInAndNoRole(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/staff", "/staff/admin", "/staff/dashboard"} {
		if r, _ := e.get(t, p, nil); r.StatusCode != 303 || r.Header.Get("Location") != "/login" {
			t.Errorf("%s: %d %s", p, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// Roles are required at creation, so make a role-less account by deleting its roles.
	e.addUser(t, "ann", "Ann", staff.Doctor)
	cookies := e.login(t, "ann", "1234").Cookies()
	if _, err := e.db.Exec(`DELETE FROM staff_user_role`); err != nil {
		t.Fatal(err)
	}
	if r, body := e.get(t, "/staff", cookies); r.StatusCode != 403 || !strings.Contains(body, "no role") {
		t.Errorf("no role: %d", r.StatusCode)
	}
}

func TestBadLoginIsGenericAndLocks(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "Ann", staff.Doctor)
	for i := 1; i <= 4; i++ {
		resp := e.login(t, "ann", "0000")
		if resp.StatusCode != 401 {
			t.Fatalf("try %d: %d", i, resp.StatusCode)
		}
	}
	resp := e.login(t, "ann", "0000")
	if resp.StatusCode != 429 {
		t.Fatalf("5th try: %d", resp.StatusCode)
	}
	if r := e.login(t, "ann", "1234"); r.StatusCode != 429 {
		t.Errorf("right PIN while locked: %d", r.StatusCode)
	}
	// Unknown names get the same message as a wrong PIN.
	unknown, _ := noFollow().PostForm(e.srv.URL+"/login", url.Values{"username": {"ghost"}, "pin": {"1234"}, turnstileField: {"dev-pass"}})
	if unknown.StatusCode != 401 {
		t.Errorf("unknown: %d", unknown.StatusCode)
	}
	if r := e.login(t, "", ""); r.StatusCode != 401 {
		t.Errorf("empty form: %d", r.StatusCode)
	}
}

func TestExpiredSessionAndLogout(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "Ann", staff.Doctor)
	cookies := e.login(t, "ann", "1234").Cookies()
	e.clock.now = "2026-11-06 20:00:00" // exactly 12 hours later
	if r, _ := e.get(t, "/staff/doctor", cookies); r.StatusCode != 303 {
		t.Errorf("expired session still works: %d", r.StatusCode)
	}
	e.clock.now = "2026-11-06 09:00:00"
	if r, _ := e.get(t, "/staff/doctor", cookies); r.StatusCode != 200 {
		t.Errorf("valid session: %d", r.StatusCode)
	}
	resp, err := noFollow().Post(e.srv.URL+"/logout", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 303 || len(resp.Cookies()) != 1 || resp.Cookies()[0].MaxAge >= 0 {
		t.Errorf("logout: %d %+v", resp.StatusCode, resp.Cookies())
	}
	// A deactivated account loses access even with a valid cookie.
	if err := e.store.SetActive(context.Background(), "ann", false); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.get(t, "/staff/doctor", cookies); r.StatusCode != 303 {
		t.Errorf("deactivated user still in: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/staff/doctor", []*http.Cookie{{Name: sessionCookie, Value: "forged.value"}}); r.StatusCode != 303 {
		t.Errorf("forged cookie: %d", r.StatusCode)
	}
}

func TestCrossOriginPostRefused(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/login", strings.NewReader("username=a&pin=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Errorf("cross-origin: %d", resp.StatusCode)
	}
	req.Header.Set("Origin", "not a url \x7f")
	if resp, _ := noFollow().Do(req); resp != nil && resp.StatusCode == 200 {
		t.Error("garbage origin accepted")
	}
}

func TestStaticAndHealthPages(t *testing.T) {
	e := newEnv(t)
	if r, body := e.get(t, "/static/combobox.js", nil); r.StatusCode != 200 || !strings.Contains(body, "data-combobox") {
		t.Errorf("static: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/healthz", nil); r.StatusCode != 200 {
		t.Errorf("healthz: %d", r.StatusCode)
	}
}

func TestNewAppMissingTemplates(t *testing.T) {
	if _, err := NewApp(t.TempDir(), App{}); err == nil {
		t.Error("missing templates should fail")
	}
}

// setMcuURL sets the medical checkup portal address the way production does: through the live policy
// (the mcu_portal_url Setting), which wins over the environment fallback.
func (e *testEnv) setMcuURL(u string) {
	base, fixed := e.app.PolicyFn, e.app.Pol // captured now: policy() would call the new func again
	e.app.PolicyFn = func() policy.Policy {
		p := fixed
		if base != nil {
			p = base()
		}
		p.McuPortalURL = u
		return p
	}
}
