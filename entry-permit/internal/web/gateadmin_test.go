package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/staff"
	"entry-permit/internal/worker"
)

// gateAdminSession signs in a Gate admin and returns cookies plus a CSRF token.
func (e *testEnv) gateAdminSession(t *testing.T) ([]*http.Cookie, string) {
	t.Helper()
	e.addUser(t, "gia", "Gia", staff.GateAdmin)
	cookies := e.login(t, "gia", "1234").Cookies()
	_, body := e.get(t, "/staff/admin/gate-officers", cookies)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf token on the officers page")
	}
	return cookies, m[1]
}

func TestGateAdminLandsOnLimitedOverview(t *testing.T) {
	e := newEnv(t)
	cookies, _ := e.gateAdminSession(t)
	// Login sends a Gate admin to /staff/gate-admin, which shows the overview.
	if r, _ := e.get(t, "/staff/gate-admin", cookies); r.StatusCode != 303 || r.Header.Get("Location") != "/staff/admin" {
		t.Fatalf("gate-admin redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	_, body := e.get(t, "/staff/admin", cookies)
	for _, want := range []string{"Gate administration", "Gate officers", "Gate names", "Purpose area"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	for _, not := range []string{"Nationality", "Notification emails", "Audit log", "Settings", "Users"} {
		if strings.Contains(body, not) {
			t.Errorf("Gate admin overview shows %q", not)
		}
	}
}

func TestGateAdminLimits(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.gateAdminSession(t)
	for _, p := range []string{"/staff/admin/users", "/staff/admin/settings", "/staff/admin/audit", "/staff/admin/malaria",
		"/staff/admin/lists/nationality", "/staff/admin/lists/role_email", "/staff/admin/lists/checkout_reason"} {
		if r, _ := e.get(t, p, cookies); r.StatusCode != 403 {
			t.Errorf("Gate admin GET %s: %d, want 403", p, r.StatusCode)
		}
	}
	// Posting to a list outside the gate scope is refused even with a valid token.
	if r := e.post(t, "/staff/admin/lists/nationality", cookies, url.Values{"_csrf": {tok}, "v0": {"Nowhere"}}); r.StatusCode != 403 {
		t.Errorf("POST nationality: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/users/gia", cookies, url.Values{"_csrf": {tok}, "action": {"reset_pin"}}); r.StatusCode != 403 {
		t.Errorf("POST users: %d", r.StatusCode)
	}
	// The two gate lists work.
	for _, list := range []string{"gate_name", "purpose_area"} {
		if r, _ := e.get(t, "/staff/admin/lists/"+list, cookies); r.StatusCode != 200 {
			t.Errorf("Gate admin GET %s: %d", list, r.StatusCode)
		}
		loc := e.post(t, "/staff/admin/lists/"+list, cookies, url.Values{"_csrf": {tok}, "v0": {"North " + list}}).Header.Get("Location")
		if !strings.Contains(loc, "ok=") {
			t.Errorf("add to %s: %s", list, loc)
		}
	}
	// An unknown list is 404 for a signed-in admin of either kind.
	if r, _ := e.get(t, "/staff/admin/lists/nope", cookies); r.StatusCode != 404 {
		t.Errorf("unknown list: %d", r.StatusCode)
	}
}

func TestOtherRolesCannotUseOfficerPages(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	doc := e.login(t, "ayu", "1234").Cookies()
	if r, _ := e.get(t, "/staff/admin/gate-officers", doc); r.StatusCode != 403 {
		t.Errorf("doctor GET: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/gate-officers", doc, url.Values{"name": {"Budi Santoso"}, "username": {"budi"}, "pin": {"abcd12"}}); r.StatusCode != 403 {
		t.Errorf("doctor POST: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/staff/admin/gate-officers", nil); r.StatusCode != 303 {
		t.Errorf("anonymous: %d", r.StatusCode)
	}
}

func TestOfficerAddCodeArchiveRemove(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{{Name: "Budi Santoso"}, {Name: "Sari Wulan"}}
	cookies, tok := e.gateAdminSession(t)
	act := func(path string, v url.Values) string {
		v.Set("_csrf", tok)
		return e.post(t, path, cookies, v).Header.Get("Location")
	}
	if loc := act("/staff/admin/gate-officers", url.Values{"name": {"Budi Santoso"}, "username": {"Budi.S"}, "pin": {"abcd12"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("add: %s", loc)
	}
	// A name that is not an eligible worker is refused, however it is typed.
	for _, name := range []string{"Someone Else", "Budi", ""} {
		loc := act("/staff/admin/gate-officers", url.Values{"name": {name}, "username": {"other"}, "pin": {"abcd12"}})
		if !strings.Contains(loc, "err=") || !strings.Contains(loc, "Pick") {
			t.Fatalf("free-text name %q accepted: %s", name, loc)
		}
	}
	if loc := act("/staff/admin/gate-officers", url.Values{"name": {"Sari Wulan"}, "username": {"budi.s"}, "pin": {"abcd12"}}); !strings.Contains(loc, "err=") {
		t.Fatalf("duplicate username accepted: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers", url.Values{"name": {"Sari Wulan"}, "username": {"sari"}, "pin": {"abcd12"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("a PIN shared with another officer was refused: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers", url.Values{"name": {"Sari Wulan"}, "username": {"sari2"}, "pin": {"abc"}}); !strings.Contains(loc, "err=") {
		t.Fatalf("3-character PIN accepted: %s", loc)
	}
	_, body := e.get(t, "/staff/admin/gate-officers", cookies)
	if !strings.Contains(body, "Budi Santoso") || !strings.Contains(body, `value="budi.s"`) || strings.Contains(body, "abcd12") {
		t.Fatal("list must show the name and username and never the PIN")
	}
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"username"}, "username": {"budi.santoso"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("set username: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"username"}, "username": {"sari"}}); !strings.Contains(loc, "err=") {
		t.Fatalf("took another officer's username: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"pin"}, "pin": {"zz99zz"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("set PIN: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"archive"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("archive: %s", loc)
	}
	if _, body := e.get(t, "/staff/admin/gate-officers", cookies); strings.Contains(body, "Budi Santoso") {
		t.Fatal("archived officer still in the default list")
	}
	if _, body := e.get(t, "/staff/admin/gate-officers?archived=1", cookies); !strings.Contains(body, "Budi Santoso") {
		t.Fatal("archived officer missing with 'show archived'")
	}
	// Remove needs the confirm tick box.
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"remove"}}); !strings.Contains(loc, "err=") {
		t.Fatalf("removed without confirm: %s", loc)
	}
	if loc := act("/staff/admin/gate-officers/1", url.Values{"action": {"remove"}, "confirm": {"yes"}}); !strings.Contains(loc, "ok=") {
		t.Fatalf("remove: %s", loc)
	}
	if _, body := e.get(t, "/staff/admin/gate-officers?archived=1", cookies); strings.Contains(body, "Budi Santoso") {
		t.Fatal("removed officer still listed")
	}
	if r := e.post(t, "/staff/admin/gate-officers/1", cookies, url.Values{"action": {"archive"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/gate-officers/1", cookies, url.Values{"_csrf": {tok}, "action": {"zap"}}); r.StatusCode != 400 {
		t.Errorf("bad action: %d", r.StatusCode)
	}
}

func TestOfficerAddFailsClosedWithoutEmployeeList(t *testing.T) {
	e := newEnv(t)
	e.workers.err = errors.New("connection refused 127.0.0.1:8113")
	cookies, tok := e.gateAdminSession(t)
	loc := e.post(t, "/staff/admin/gate-officers", cookies, url.Values{"_csrf": {tok}, "name": {"Budi Santoso"}, "username": {"budi"}, "pin": {"abcd12"}}).Header.Get("Location")
	if !strings.Contains(loc, "err=") || strings.Contains(loc, "127.0.0.1") {
		t.Fatalf("source down: %s", loc)
	}
	if _, body := e.get(t, "/staff/admin/gate-officers", cookies); strings.Contains(body, "Budi Santoso") {
		t.Fatal("officer added without checking eligibility")
	}
}

func TestEmployeeNamesIsForAdminsOnly(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{{Name: "Budi Santoso"}}
	if r, _ := e.get(t, "/staff/admin/employee-names?q=budi", nil); r.StatusCode != 403 {
		t.Errorf("anonymous: %d", r.StatusCode)
	}
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	if r, _ := e.get(t, "/staff/admin/employee-names?q=budi", e.login(t, "ayu", "1234").Cookies()); r.StatusCode != 403 {
		t.Errorf("doctor: %d", r.StatusCode)
	}
	cookies, _ := e.gateAdminSession(t)
	r, body := e.get(t, "/staff/admin/employee-names?q=budi", cookies)
	if r.StatusCode != 200 || !strings.Contains(body, "Budi Santoso") {
		t.Errorf("gate admin: %d %s", r.StatusCode, body)
	}
	if _, short := e.get(t, "/staff/admin/employee-names?q=bu", cookies); strings.Contains(short, "Budi") {
		t.Error("a 2-letter query returned names")
	}
}

func TestFullAdminStillSeesEverything(t *testing.T) {
	e := newEnv(t)
	cookies, _ := e.adminSession(t)
	_, body := e.get(t, "/staff/admin", cookies)
	for _, want := range []string{"Administration", "Gate officers", "Nationality", "Audit log", "Settings", "Users"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin overview lacks %q", want)
		}
	}
	if r, _ := e.get(t, "/staff/admin/gate-officers", cookies); r.StatusCode != 200 {
		t.Errorf("admin officers: %d", r.StatusCode)
	}
}
