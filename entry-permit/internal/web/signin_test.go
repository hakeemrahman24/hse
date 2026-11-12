package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/staff"
	"entry-permit/internal/worker"
)

// signInEnv has one staff member and two workers, one of whom shares a name start with the staff member.
func signInEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.addUser(t, "annisa", "Annisa Putri", staff.Doctor)
	e.addUser(t, "gone", "Annika Gone", staff.Stage1)
	if err := e.store.SetActive(context.Background(), "gone", false); err != nil {
		t.Fatal(err)
	}
	e.workers.people = []worker.Person{budi, {Name: "Annie Worker", DOB: "1990-01-01"}}
	return e
}

func TestStaffWhoAreAlsoWorkersGetAMyPermitLink(t *testing.T) {
	e := signInEnv(t)
	// Reviewers are site personnel, so their menu has My permit (it opens the date of birth step for
	// their own name). A viewer-only account, an admin account and a gate admin account are office
	// roles with no permit, so their menu does not.
	for name, c := range map[string]struct {
		cookies []*http.Cookie
		page    string
		want    bool
	}{
		"doctor":     {e.login(t, "annisa", "1234").Cookies(), "doctor", true},
		"stage1":     {e.reviewer(t, "sari", staff.Stage1), "stage1", true},
		"final":      {e.reviewer(t, "manager", staff.Final), "final", true},
		"gate":       {e.reviewer(t, "gita", staff.GateOfficer), "dashboard", true}, // gate officers are medpost workers too
		"viewer":     {e.reviewer(t, "dewi", staff.Dashboard), "dashboard", false},
		"admin":      {e.reviewer(t, "adi", staff.Admin), "dashboard", false},
		"gate admin": {e.reviewer(t, "gani", staff.GateAdmin), "dashboard", false},
	} {
		_, page := e.get(t, "/staff/"+c.page, c.cookies)
		if got := strings.Contains(page, `href="/my/signin"`); got != c.want {
			t.Errorf("%s: My permit link shown = %v, want %v", name, got, c.want)
		}
	}
}

func TestStaffLoginNeedsNoWidget(t *testing.T) {
	// WHY: staff sign in has no Turnstile check; the PIN lockout protects the accounts.
	e := signInEnv(t)
	if code := e.postForm(t, "/login", nil, url.Values{"username": {"annisa"}, "pin": {"1234"}}).StatusCode; code != 303 {
		t.Errorf("no Turnstile configured: %d, want 303", code)
	}
}

func TestWrongPINShowsTheSameStaffStepAgainWithTheMessage(t *testing.T) {
	e := signInEnv(t)
	code, body := e.postBody(t, "/login", nil, url.Values{"username": {"annisa"}, "pin": {"0000"}, "next": {"final"}, "cf-turnstile-response": {"dev-pass"}})
	if code != 401 {
		t.Fatalf("wrong PIN: %d", code)
	}
	for _, want := range []string{"Username or PIN is incorrect.", `name="pin"`, `value="annisa"`, `name="next" value="final"`} {
		if !strings.Contains(body, want) {
			t.Errorf("retry page lacks %q", want)
		}
	}
	// An unknown name gets the same message, on the first step.
	code, body = e.postBody(t, "/login", nil, url.Values{"username": {"ghost"}, "pin": {"1234"}, "cf-turnstile-response": {"dev-pass"}})
	if code != 401 || !strings.Contains(body, "Username or PIN is incorrect.") {
		t.Errorf("unknown name: %d", code)
	}
}

func TestLoginPageIsUsernameAndPINAndListsNoStaffNames(t *testing.T) {
	e := signInEnv(t)
	r, body := e.get(t, "/login?next=final", nil)
	if r.StatusCode != 200 {
		t.Fatalf("/login: %d", r.StatusCode)
	}
	for _, want := range []string{`action="/login"`, `name="username"`, `name="pin"`, `name="next" value="final"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/login lacks %q", want)
		}
	}
	for _, leak := range []string{"Annisa", "Annika", "<option", "secret-mail", "doctor"} {
		if strings.Contains(body, leak) {
			t.Errorf("/login leaks %q", leak)
		}
	}
	if strings.Contains(r.Header.Get("Content-Security-Policy"), "cloudflare") {
		t.Error("staff sign in has no widget, so it keeps the strict policy")
	}
}

func TestAFailedLookupShowsTheDateOfBirthStepAgainForThatName(t *testing.T) {
	e := signInEnv(t)
	code, body := e.lookup(t, "Budi Santoso", "01/01/1999", "dev-pass")
	if code != 401 {
		t.Fatalf("wrong date: %d", code)
	}
	for _, want := range []string{lookupNotFound[:40], `name="dob"`, "Budi Santoso", `action="/my"`} {
		if !strings.Contains(body, want) {
			t.Errorf("retry lacks %q", want)
		}
	}
}
