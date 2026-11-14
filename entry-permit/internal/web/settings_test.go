package web

import (
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

func TestSettingsPageGuardsAndContent(t *testing.T) {
	e := newEnv(t)
	cookies, _ := e.adminSession(t)
	r, body := e.get(t, "/staff/admin/settings", cookies)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{"Onshore weeks on site", "Staff login", "Positions whose certificate never expires", `value="8"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	if r, _ := e.get(t, "/staff/admin/settings", nil); r.StatusCode != 303 {
		t.Errorf("anonymous: %d", r.StatusCode)
	}
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	if r, _ := e.get(t, "/staff/admin/settings", e.login(t, "ayu", "1234").Cookies()); r.StatusCode != 403 {
		t.Errorf("doctor: %d", r.StatusCode)
	}
}

func TestSettingsSaveResetAndRejects(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	save := func(key, val, action string) string {
		r := e.post(t, "/staff/admin/settings", cookies, url.Values{"_csrf": {tok}, "key": {key}, "value": {val}, "action": {action}})
		return r.Header.Get("Location")
	}
	if loc := save("onshore_weeks", "10", "save"); !strings.Contains(loc, "ok=Saved") {
		t.Fatalf("save: %s", loc)
	}
	if _, body := e.get(t, "/staff/admin/settings", cookies); !strings.Contains(body, `value="10"`) || !strings.Contains(body, "Default: 8") {
		t.Fatal("saved value or default hint not shown")
	}
	if loc := save("onshore_weeks", "0", "save"); !strings.Contains(loc, "err=") {
		t.Fatalf("out-of-range accepted: %s", loc)
	}
	if loc := save("nope", "1", "save"); !strings.Contains(loc, "err=") {
		t.Fatalf("unknown key accepted: %s", loc)
	}
	if loc := save("onshore_weeks", "", "reset"); !strings.Contains(loc, "ok=") {
		t.Fatalf("reset: %s", loc)
	}
	if _, body := e.get(t, "/staff/admin/audit", cookies); !strings.Contains(body, "setting:onshore_weeks") {
		t.Fatal("audit log lacks the setting change")
	}
	// Missing or wrong CSRF token, and an unknown action, change nothing.
	if r := e.post(t, "/staff/admin/settings", cookies, url.Values{"key": {"onshore_weeks"}, "value": {"12"}, "action": {"save"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/settings", cookies, url.Values{"_csrf": {tok}, "key": {"onshore_weeks"}, "action": {"zap"}}); r.StatusCode != 400 {
		t.Errorf("bad action: %d", r.StatusCode)
	}
}

// A Settings edit must reach the login rules at once, without a restart.
func TestSettingsChangeLoginLockoutLive(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	e.post(t, "/staff/admin/settings", cookies, url.Values{"_csrf": {tok}, "key": {"staff_login_max_tries"}, "value": {"1"}, "action": {"save"}})
	e.login(t, "ayu", "9999") // one wrong PIN now locks the account
	if r := e.login(t, "ayu", "1234"); r.StatusCode == 303 && r.Header.Get("Location") == "/" {
		t.Fatal("account not locked after 1 wrong PIN with max tries = 1")
	}
}

// The optional form fields are one table: a Show box and a Mandatory box per field.
func TestSettingsPageHasTheShowAndMandatoryTable(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	_, body := e.get(t, "/staff/admin/settings", cookies)
	for _, want := range []string{"Optional fields on the work-permit form", `id="fr-bpjs_kes-shown"`, `id="fr-bpjs_tk-required"`,
		`id="fr-contract-shown"`, `id="fr-certificate-required"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	if strings.Contains(body, "Show the BPJS Kesehatan box") {
		t.Error("the BPJS switch is still listed as a separate on/off row")
	}
	post := func(v url.Values) string {
		v.Set("_csrf", tok)
		loc := e.post(t, "/staff/admin/settings", cookies, v).Header.Get("Location")
		e.clock.now = e.clock.now.AddMinutes(2) // past the one-minute policy cache
		return loc
	}
	// Show and Mandatory ticked.
	if loc := post(url.Values{"action": {"save-field"}, "field": {"bpjs_tk"}, "shown": {"1"}, "required": {"1"}}); !strings.Contains(loc, "ok=Saved") {
		t.Fatalf("save field: %s", loc)
	}
	if p := e.livePolicy(); !p.BpjsTkShown || !p.BpjsTkRequired {
		t.Fatalf("show and mandatory not saved: %+v", p)
	}
	// Nothing ticked: hidden completely.
	post(url.Values{"action": {"save-field"}, "field": {"bpjs_tk"}})
	if p := e.livePolicy(); p.BpjsTkShown || p.BpjsTkRequired {
		t.Fatalf("hidden not saved: %+v", p)
	}
	if loc := post(url.Values{"action": {"save-field"}, "field": {"nonsense"}, "shown": {"1"}}); !strings.Contains(loc, "err=") {
		t.Fatalf("unknown field accepted: %s", loc)
	}
	post(url.Values{"action": {"reset-field"}, "field": {"bpjs_tk"}})
	if p := e.livePolicy(); !p.BpjsTkShown || p.BpjsTkRequired {
		t.Fatalf("reset not applied: %+v", p)
	}
}

func (e *testEnv) livePolicy() policy.Policy { return e.app.policy() }
