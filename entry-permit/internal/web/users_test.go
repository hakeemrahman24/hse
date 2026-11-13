package web

import (
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/staff"
)

func TestUsersPageGuards(t *testing.T) {
	e := newEnv(t)
	cookies, _ := e.adminSession(t)
	r, body := e.get(t, "/staff/admin/users", cookies)
	if r.StatusCode != 200 || !strings.Contains(body, "123456") || !strings.Contains(body, "Reset PIN") {
		t.Fatalf("users page: %d", r.StatusCode)
	}
	if r, _ := e.get(t, "/staff/admin/users", nil); r.StatusCode != 303 {
		t.Errorf("anonymous: %d", r.StatusCode)
	}
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	doc := e.login(t, "ayu", "1234").Cookies()
	if r, _ := e.get(t, "/staff/admin/users", doc); r.StatusCode != 403 {
		t.Errorf("doctor GET: %d", r.StatusCode)
	}
	// A non-admin cannot reset anyone's PIN even with a valid-looking form.
	if r := e.post(t, "/staff/admin/users/arif", doc, url.Values{"action": {"reset_pin"}}); r.StatusCode != 403 {
		t.Errorf("doctor POST: %d", r.StatusCode)
	}
}

func TestAdminAddResetAndNoPINChoice(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	loc := e.post(t, "/staff/admin/users", cookies, url.Values{"_csrf": {tok}, "username": {"budi"}, "display": {"Budi"}, "role": {"doctor"}, "pin": {"9999"}}).Header.Get("Location")
	if !strings.Contains(loc, "ok=") {
		t.Fatalf("add: %s", loc)
	}
	// The posted "pin" is ignored: the default is the only PIN.
	if r := e.login(t, "budi", "123456"); r.Header.Get("Location") != "/" {
		t.Fatalf("default PIN login: %s", r.Header.Get("Location"))
	}
	if loc := e.post(t, "/staff/admin/users", cookies, url.Values{"_csrf": {tok}, "username": {"x y"}, "display": {"X"}, "role": {"doctor"}}).Header.Get("Location"); !strings.Contains(loc, "err=") {
		t.Fatalf("bad username accepted: %s", loc)
	}
	bud := e.login(t, "budi", "123456").Cookies()
	_, acc := e.get(t, "/account", bud)
	btok := csrfRe.FindStringSubmatch(acc)[1]
	e.post(t, "/account/pin", bud, url.Values{"_csrf": {btok}, "current": {"123456"}, "new": {"mine77"}, "again": {"mine77"}})
	if r := e.login(t, "budi", "123456"); r.Header.Get("Location") == "/" {
		t.Fatal("old PIN still works")
	}
	if loc := e.post(t, "/staff/admin/users/budi", cookies, url.Values{"_csrf": {tok}, "action": {"reset_pin"}, "pin": {"evil1"}}).Header.Get("Location"); !strings.Contains(loc, "ok=") {
		t.Fatalf("reset: %s", loc)
	}
	if r := e.login(t, "budi", "123456"); r.Header.Get("Location") != "/" {
		t.Fatal("reset did not restore the default PIN")
	}
	if r := e.login(t, "budi", "evil1"); r.Header.Get("Location") == "/" {
		t.Fatal("admin managed to choose a PIN")
	}
}

func TestAdminCannotRemoveLastAdmin(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	loc := e.post(t, "/staff/admin/users/arif", cookies, url.Values{"_csrf": {tok}, "action": {"off"}}).Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Fatalf("switched off the last admin: %s", loc)
	}
	if r := e.post(t, "/staff/admin/users/arif", cookies, url.Values{"_csrf": {tok}, "action": {"zap"}}); r.StatusCode != 400 {
		t.Errorf("bad action: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/users/arif", cookies, url.Values{"action": {"off"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
}

func TestAccountSelfService(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	if r, _ := e.get(t, "/account", nil); r.StatusCode != 303 {
		t.Fatalf("anonymous: %d", r.StatusCode)
	}
	c := e.login(t, "ayu", "1234").Cookies()
	_, body := e.get(t, "/account", c)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Change PIN") {
		t.Fatal("account page incomplete")
	}
	tok := m[1]
	if loc := e.post(t, "/account/profile", c, url.Values{"_csrf": {tok}, "username": {"ayu.d"}, "display": {"Ayu Dewi"}}).Header.Get("Location"); !strings.Contains(loc, "ok=") {
		t.Fatalf("profile: %s", loc)
	}
	// The session survives a rename (it is keyed by id), and the new name signs in.
	if r, _ := e.get(t, "/account", c); r.StatusCode != 200 {
		t.Fatalf("session lost after rename: %d", r.StatusCode)
	}
	if r := e.login(t, "ayu.d", "1234"); r.Header.Get("Location") != "/" {
		t.Fatal("new username does not sign in")
	}
	for name, v := range map[string]url.Values{
		"wrong current": {"current": {"nope"}, "new": {"abcd1"}, "again": {"abcd1"}},
		"mismatch":      {"current": {"1234"}, "new": {"abcd1"}, "again": {"abcd2"}},
		"too short":     {"current": {"1234"}, "new": {"ab"}, "again": {"ab"}},
	} {
		v.Set("_csrf", tok)
		if loc := e.post(t, "/account/pin", c, v).Header.Get("Location"); !strings.Contains(loc, "err=") {
			t.Errorf("%s accepted: %s", name, loc)
		}
	}
	if r := e.post(t, "/account/pin", c, url.Values{"current": {"1234"}, "new": {"abcd1"}, "again": {"abcd1"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
}
