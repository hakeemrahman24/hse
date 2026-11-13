package web

import (
	"context"
	"errors"
	"net/url"
	"os"
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

// The home page is the single landing page (owner decision 2026-11-13): two doors, Medical checkup
// submission and Layar Entry Permit, with staff sign in and settings as small links.
func TestHomeIsTheSingleLandingPage(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("https://medpost.example.com/mcu-submit")
	r, body := e.get(t, "/", nil)
	for _, want := range []string{
		"Medical checkup (MCU) <span>submission</span>",
		`href="https://medpost.example.com/mcu-submit/signin"`,
		`href="https://medpost.example.com/mcu-submit/register"`,
		"Layar <span>Entry Permit</span>",
		`href="/request"`, "Request a permit",
		`href="/my/signin"`, "Check my permit",
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

func TestHomeHidesMedicalDoorLinksWhenPortalIsNotConfigured(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("") // an admin cleared the Setting
	_, body := e.get(t, "/", nil)
	if strings.Contains(body, "/mcu-submit") || strings.Contains(body, "/register\"") {
		t.Errorf("no portal address set, so the medical door must not link anywhere")
	}
	if !strings.Contains(body, "Medical checkup (MCU) <span>submission</span>") || !strings.Contains(body, "Ask the medical team for the portal address") {
		t.Errorf("the door itself should still be explained")
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
		{[]staff.Role{staff.Dashboard}, "/"},
	} {
		if got := a.loginTarget(staff.User{Roles: c.roles}, "review"); got != c.want {
			t.Errorf("roles %v: got %q, want %q", c.roles, got, c.want)
		}
	}
	if got := a.loginTarget(staff.User{Roles: []staff.Role{staff.Admin}}, "admin"); got != "/staff/admin" {
		t.Errorf("admin tile opened %q", got)
	}
	if got := a.loginTarget(staff.User{Roles: []staff.Role{staff.Doctor}}, "admin"); got != "/" {
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
	if loc := post("ayu", "final"); loc != "/" { // no final role: the shared landing page
		t.Errorf("user without the role: %q", loc)
	}
	if loc := post("ayu", "dashboard"); loc != "/staff/dashboard" { // everyone may open the dashboard
		t.Errorf("dashboard: %q", loc)
	}
	if loc := post("ayu", "//evil.example"); loc != "/" {
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

// Staff use the same landing page as everyone (owner decision 2026-11-13). Signed out, they see only
// the small Staff sign in link. Signed in, a third door lists their own pages and a sign out button,
// and the top bar carries their menu.
func TestSignedInStaffSeeTheirOwnDoorOnTheLandingPage(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ann", "Ann Lee", staff.Stage1, staff.Doctor)
	_, out := e.get(t, "/", nil)
	if strings.Contains(out, "Your work") || strings.Contains(out, "/staff/stage1") {
		t.Fatalf("signed out landing page must not show staff pages")
	}
	if !strings.Contains(out, `href="/login"`) {
		t.Errorf("signed out landing page keeps the Staff sign in link")
	}
	cookies := e.login(t, "ann", "1234").Cookies()
	_, body := e.get(t, "/", cookies)
	for _, want := range []string{"Ann Lee", "Your work", `href="/staff/stage1"`, `href="/staff/doctor"`, `href="/staff/dashboard"`, `action="/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("signed in landing page lacks %q", want)
		}
	}
	if strings.Contains(body, `href="/staff/admin"`) || strings.Contains(body, `href="/staff/final"`) {
		t.Errorf("landing page must list only the user's own roles")
	}
	if strings.Contains(body, `href="/login"`) {
		t.Errorf("signed in staff do not need the Staff sign in link")
	}
	if !strings.Contains(body, "Request a permit") || !strings.Contains(body, "Medical checkup (MCU) <span>submission</span>") {
		t.Errorf("staff still see the two public doors")
	}
}

// The permit door comes first (workers are the main visitors), and each door has one clear primary
// action. The MCU is spelled out and the page carries a short Indonesian line.
func TestLandingLeadsWithThePermitDoorAndExplainsMCU(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("https://medpost.example.com/mcu-submit")
	_, body := e.get(t, "/", nil)
	permit, mcu := strings.Index(body, `id="h-door-lep"`), strings.Index(body, `id="h-door-mcu"`)
	if permit < 0 || mcu < 0 || permit > mcu {
		t.Fatalf("permit door must come before the medical checkup door (permit %d, mcu %d)", permit, mcu)
	}
	for _, want := range []string{"Request a permit", "Check my permit", "medical checkup (MCU)", `lang="id"`, "Fit with note"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing page lacks %q", want)
		}
	}
	if strings.Contains(body, "later step") {
		t.Errorf("no internal build talk on the landing page")
	}
}

func TestDonePagesPromiseOnlyWhatTheSystemDoes(t *testing.T) {
	for _, name := range []string{"permit.html", "waiver.html"} {
		b, err := os.ReadFile("../../web/templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "later step") {
			t.Errorf("%s still talks about a mail service built in a later step", name)
		}
	}
}

// The chooser says what each permit needs before the requester starts, and where to get an MCU.
func TestRequestChooserExplainsWhatEachPermitNeeds(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("https://medpost.example.com/mcu-submit")
	_, body := e.get(t, "/request", nil)
	for _, want := range []string{`href="/permit"`, `href="/waiver"`, "Work Entry Permit", "Visitor Entry Permit", "valid medical checkup (MCU)", "appointment letter", `href="https://medpost.example.com/mcu-submit"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/request lacks %q", want)
		}
	}
}

// The long visitor form gets a jump list of its sections and keeps its send button in reach.
func TestWaiverFormHasSectionNavAndStickySubmit(t *testing.T) {
	e := newEnv(t)
	_, body := e.get(t, "/waiver", nil)
	if strings.Contains(body, "not available right now") {
		t.Skip("visitor form is not available in this test environment")
	}
	for _, want := range []string{`aria-label="Sections of this form"`, `href="#sec-1"`, `href="#sec-8"`, `id="sec-1"`, `id="sec-8"`, `class="form-actions"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/waiver lacks %q", want)
		}
	}
}

func TestMedicalLinksOpenInANewTabAndNeverDoubleTheSigninPath(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("https://medpost.example.com/mcu-submit/signin") // the address as it is kept in Settings
	_, body := e.get(t, "/", nil)
	for _, want := range []string{
		`href="https://medpost.example.com/mcu-submit/signin" target="_blank" rel="noopener">Sign in`,
		`href="https://medpost.example.com/mcu-submit/register" target="_blank" rel="noopener">Request an account`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if strings.Contains(body, "/signin/signin") {
		t.Errorf("the sign-in path must not be added twice")
	}
}

func TestMedicalCheckupIsNotInTheSideMenu(t *testing.T) {
	e := newEnv(t)
	e.setMcuURL("https://medpost.example.com/mcu-submit/signin")
	_, body := e.get(t, "/", nil)
	i, j := strings.Index(body, `<aside class="side"`), strings.Index(body, "</aside>")
	if i < 0 || j < i {
		t.Fatal("no side menu")
	}
	if menu := body[i:j]; strings.Contains(menu, "mcu-submit") || strings.Contains(menu, "Medical checkup") {
		t.Error("the medical checkup portal is in the side menu; it belongs on the home page only")
	}
	if !strings.Contains(body[j:], "mcu-submit/register") {
		t.Error("the home page lost its medical checkup door")
	}
}
