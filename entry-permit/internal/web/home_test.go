package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/staff"
	"entry-permit/internal/worker"
)

// fakeWorkers is an in-memory worker.Source.
type fakeWorkers struct {
	people      []worker.Person
	employees   map[string]worker.Employee
	err         error
	finds       int
	expiredAsks int // calls to PickIncludingExpired
}

func (f *fakeWorkers) Names(_ context.Context, q string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len([]rune(strings.TrimSpace(q))) < worker.MinQuery {
		return nil, nil
	}
	var out []string
	for _, p := range f.people {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(strings.TrimSpace(q))) {
			out = append(out, p.Name)
		}
	}
	return out, nil
}

func (f *fakeWorkers) Eligible(_ context.Context, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for _, p := range f.people {
		if strings.EqualFold(p.Name, strings.Join(strings.Fields(name), " ")) {
			return true, nil
		}
	}
	return false, nil
}

// PickIncludingExpired: the fake never filters by expiry, so it answers like Pick, and counts the
// call so tests can see which page asked for expired workers.
func (f *fakeWorkers) PickIncludingExpired(ctx context.Context, name, dob string) (worker.Employee, bool, error) {
	f.expiredAsks++
	return f.Pick(ctx, name, dob)
}

// Pick turns a Person into a full record. An entry in employees (keyed by
// name) replaces the defaults, for tests that need a foreign worker or a patient id.
func (f *fakeWorkers) Pick(_ context.Context, name, dob string) (worker.Employee, bool, error) {
	f.finds++
	if f.err != nil {
		return worker.Employee{}, false, f.err
	}
	for i, p := range f.people {
		if strings.EqualFold(p.Name, name) && p.DOB == dob {
			if e, ok := f.employees[p.Name]; ok {
				return e, true, nil
			}
			return worker.Employee{
				PatientID: int64(i + 1), Name: p.Name, DOB: p.DOB, Nationality: "Indonesia", Company: p.Company,
				Entity: "Contractor", Position: p.Position, McuResult: "Fit", McuValidUntil: p.McuValidUntil,
			}, true, nil
		}
	}
	return worker.Employee{}, false, nil
}

var budi = worker.Person{Name: "Budi Santoso", DOB: "1988-03-05", Company: "PT Maju", Position: "Welder", McuValidUntil: "2027-01-31"}

func (e *testEnv) lookup(t *testing.T, name, dob, token string) (int, string) {
	t.Helper()
	v := url.Values{"name": {name}, "dob": {dob}, "cf-turnstile-response": {token}}
	return e.postBody(t, "/my", nil, v)
}

// The home page is five tiles (owner decision 2026-11-12). Staff tiles go through /login?next=...
func TestHomeShowsTheThreeTilesAndSettingsLink(t *testing.T) {
	e := newEnv(t)
	r, body := e.get(t, "/", nil)
	for _, want := range []string{
		`href="/request"`, "Request Entry Permit",
		`href="/my/signin"`, "My Entry Permit",
		`href="/login"`, "Staff sign in",
		`href="/login?next=admin"`, "Settings",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if strings.Contains(body, "HR Personnel") || strings.Contains(body, `action="/signin"`) {
		t.Errorf("home must not carry the sign-in form any more")
	}
	if csp := r.Header.Get("Content-Security-Policy"); strings.Contains(csp, "cloudflare") {
		t.Errorf("home has no widget, so it keeps the strict policy: %q", csp)
	}
}

func TestReviewTileOpensTheUsersOwnReviewPage(t *testing.T) {
	a := &App{}
	for _, c := range []struct {
		roles []staff.Role
		want  string
	}{
		{[]staff.Role{staff.Doctor}, "/staff/doctor"},
		{[]staff.Role{staff.Stage1, staff.Doctor}, "/staff/stage1"},
		{[]staff.Role{staff.Final}, "/staff/final"},
		{[]staff.Role{staff.Dashboard}, "/staff"},
	} {
		if got := a.loginTarget(staff.User{Roles: c.roles}, "review"); got != c.want {
			t.Errorf("roles %v: got %q, want %q", c.roles, got, c.want)
		}
	}
	if got := a.loginTarget(staff.User{Roles: []staff.Role{staff.Admin}}, "admin"); got != "/staff/admin" {
		t.Errorf("admin tile opened %q", got)
	}
	if got := a.loginTarget(staff.User{Roles: []staff.Role{staff.Doctor}}, "admin"); got != "/staff" {
		t.Errorf("non-admin must not reach admin, got %q", got)
	}
}

func TestCSPAllowsTurnstileOnlyOnTheWorkerPages(t *testing.T) {
	e := newEnv(t)
	// Only the worker pages carry the widget; the staff sign in has none.
	my, _ := e.get(t, "/my/signin", nil)
	if csp := my.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "https://challenges.cloudflare.com") {
		t.Errorf("my sign in CSP = %q", csp)
	}
	// Every page without the widget keeps the strict policy.
	health, _ := e.get(t, "/healthz", nil)
	if csp := health.Header.Get("Content-Security-Policy"); strings.Contains(csp, "cloudflare") {
		t.Errorf("other pages must stay strict: %q", csp)
	}
}

func TestWorkerNamesNeedsFourCharacters(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi, {Name: "Budi Hartono"}}
	for q, want := range map[string]string{"bud": `{"names":[]}`, "budi": `{"names":["Budi Santoso","Budi Hartono"]}`} {
		r, body := e.get(t, "/api/worker-names?q="+q, nil)
		if r.StatusCode != 200 || strings.TrimSpace(body) != want {
			t.Errorf("q=%q -> %d %s", q, r.StatusCode, body)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("q=%q not no-store", q)
		}
	}
	e.workers.err = errors.New("medpost down")
	if r, body := e.get(t, "/api/worker-names?q=budi", nil); r.StatusCode != 502 || strings.Contains(body, "medpost") {
		t.Errorf("source error: %d %s", r.StatusCode, body)
	}
}

func TestWorkerNamesIsRateLimited(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	last := 0
	for i := 0; i < 40; i++ {
		r, _ := e.get(t, "/api/worker-names?q=budi", nil)
		last = r.StatusCode
	}
	if last != 429 {
		t.Fatalf("40 searches in a row ended with %d, want 429", last)
	}
}

// A permit that came from the spreadsheet has no medpost patient id. The first time its worker is
// confirmed by name and birth date, the page links it and shows it (owner decision 2026-11-08).
func TestAnImportedPermitShowsOnMyPageAfterTheNameAndBirthDateMatch(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	if _, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, dob, company,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES ('WEP-IMP01', 'LEP-IMP01', 'Approved', 0, 'budi santoso', '1988-03-05', 'PT Maju', 'Rina', 'rina@example.com',
		'Onshore', '2027-01-31', '2027-01-17', '2026-06-01 08:00:00')`); err != nil {
		t.Fatal(err)
	}
	code, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	if code != 200 || !strings.Contains(body, "LEP-IMP01") {
		t.Fatalf("status %d; the imported permit is not on the page", code)
	}
	// Linked for good: the permit now belongs to patient 1, so the permit form's duplicate gate sees it too.
	var patient int64
	if err := e.db.QueryRow(`SELECT patient_id FROM permit WHERE request_id = 'WEP-IMP01'`).Scan(&patient); err != nil || patient != 1 {
		t.Errorf("patient id = %d, %v; want 1", patient, err)
	}
}

func TestLookupHappyPath(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	code, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"Budi Santoso", "PT Maju", "Welder", "2027-01-31"} {
		if !strings.Contains(body, want) {
			t.Errorf("result lacks %q", want)
		}
	}
	if strings.Contains(body, "1988") {
		t.Error("the date of birth is echoed back on the result page")
	}
}

func TestLookupRejections(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	cases := []struct {
		name, nm, dob, token string
		status               int
		msg                  string
	}{
		{"no token", "Budi Santoso", "05/03/1988", "", 400, "security check"},
		{"bad token", "Budi Santoso", "05/03/1988", "forged", 400, "security check"},
		{"short name", "Bud", "05/03/1988", "dev-pass", 400, "at least 4"},
		{"bad date format", "Budi Santoso", "1988-03-05", "dev-pass", 400, "dd/mm/yyyy"},
		{"impossible date", "Budi Santoso", "31/04/1988", "dev-pass", 400, "dd/mm/yyyy"},
		{"wrong dob", "Budi Santoso", "06/03/1988", "dev-pass", 401, "could not find"},
		{"unknown name", "Nobody Here", "05/03/1988", "dev-pass", 401, "could not find"},
	}
	for _, c := range cases {
		code, body := e.lookup(t, c.nm, c.dob, c.token)
		if code != c.status || !strings.Contains(body, c.msg) {
			t.Errorf("%s: %d, body lacks %q", c.name, code, c.msg)
		}
		if strings.Contains(body, "PT Maju") {
			t.Errorf("%s: details leaked", c.name)
		}
	}
	// The same message for a wrong DOB and an unknown name: nothing to enumerate. A fresh
	// environment, because the cases above already used up this address's 5 tries.
	e = newEnv(t)
	e.workers.people = []worker.Person{budi}
	_, a := e.lookup(t, "Budi Santoso", "06/03/1988", "dev-pass")
	_, b := e.lookup(t, "Nobody Here", "05/03/1988", "dev-pass")
	if alertText(a) != alertText(b) || alertText(a) == "" {
		t.Error("wrong DOB and unknown name give different messages")
	}
}

func alertText(body string) string {
	i := strings.Index(body, `role="alert"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</p>")
	return body[i : i+j]
}

func TestLookupLocksAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	// 5 wrong tries are answered normally; the 6th is locked for 15 minutes.
	for _, d := range []string{"01/01/1990", "02/01/1990", "03/01/1990", "04/01/1990", "05/01/1990"} {
		if code, _ := e.lookup(t, "Budi Santoso", d, "dev-pass"); code != 401 {
			t.Fatalf("try with %s: %d, want 401", d, code)
		}
	}
	if code, body := e.lookup(t, "Budi Santoso", "06/01/1990", "dev-pass"); code != 429 || !strings.Contains(body, "15 minutes") {
		t.Fatalf("after 5 bad tries got %d, want 429 with 15 minutes", code)
	}
	before := e.workers.finds
	// Even the right answer is refused while locked, and medpost is not asked.
	if code, _ := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass"); code != 429 {
		t.Fatalf("correct DOB while locked: %d", code)
	}
	if e.workers.finds != before {
		t.Fatal("medpost was queried while locked")
	}
}

func TestLookupFailsClosedWhenUnavailable(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	e.workers.err = errors.New("connection refused to 127.0.0.1:8113")
	code, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	if code != 503 || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("source down: %d (internal detail leaked: %v)", code, strings.Contains(body, "127.0.0.1"))
	}
}

func TestLoginNextGoesToTheRequestedPageOrLanding(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	e.addUser(t, "fina", "Fina", staff.Final, staff.Doctor)
	_, page := e.get(t, "/login?next=final", nil)
	if !strings.Contains(page, `name="next" value="final"`) {
		t.Fatal("login page does not carry next")
	}
	if _, page := e.get(t, "/login?next=https://evil.example", nil); strings.Contains(page, "evil") {
		t.Fatal("login page echoes an unknown next")
	}
	post := func(user, next string) string {
		r := e.postForm(t, "/login", nil, url.Values{"username": {user}, "pin": {"1234"}, "next": {next}, turnstileField: {"dev-pass"}})
		return r.Header.Get("Location")
	}
	if loc := post("fina", "doctor"); loc != "/staff/doctor" {
		t.Errorf("multi-role user asking for doctor: %q", loc)
	}
	if loc := post("ayu", "final"); loc != "/staff" { // no final role: ordinary landing
		t.Errorf("user without the role: %q", loc)
	}
	if loc := post("ayu", "dashboard"); loc != "/staff/dashboard" { // everyone may open the dashboard
		t.Errorf("dashboard: %q", loc)
	}
	if loc := post("ayu", "//evil.example"); loc != "/staff" {
		t.Errorf("hostile next: %q", loc)
	}
}

func TestMySignInHasNameSearchDOBAndTurnstile(t *testing.T) {
	e := newEnv(t)
	r, body := e.get(t, "/my/signin", nil)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{`data-namesearch="/api/worker-names"`, `name="dob"`, "cf-turnstile-response", `action="/my"`} {
		if !strings.Contains(body, want) {
			t.Errorf("my sign in lacks %q", want)
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "https://challenges.cloudflare.com") {
		t.Error("my sign in needs the Turnstile CSP")
	}
}

func TestRequestPageOffersWorkAndVisitorPermitWithoutSignIn(t *testing.T) {
	e := newEnv(t)
	r, body := e.get(t, "/request", nil)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{"Work Entry Permit", "Visitor Entry Permit", `href="/permit"`, `href="/waiver"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request page lacks %q", want)
		}
	}
}
