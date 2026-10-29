package mcusubmit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const (
	testUser = "hr.budi"
	testPass = "a-long-passphrase-1"
	testMail = "hr@example.com"
	newPass  = "another-long-phrase-2"
)

// signedIn onboards one submitter through the real flow and returns the session cookie.
func signedIn(t *testing.T) (*env, int64, *http.Cookie) {
	t.Helper()
	e := newEnv(t)
	id := e.onboard(testMail, testUser, testPass)
	rec, c := e.signIn(testUser, testPass)
	if c == nil {
		t.Fatalf("sign-in failed: %d", rec.Code)
	}
	return e, id, c
}

func (e *env) mailsTo(to string) []sentMail {
	e.mail.mu.Lock()
	defer e.mail.mu.Unlock()
	var out []sentMail
	for _, m := range e.mail.sent {
		if m.To == to {
			out = append(out, m)
		}
	}
	return out
}

func tokenIn(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`token=([0-9a-f]{64})`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token link in:\n%s", body)
	}
	return m[1]
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

// ---- forgot and reset password ----------------------------------------------------------------

func TestForgotPasswordGivesTheSameReplyAndMailsOnlyAnActiveAccount(t *testing.T) {
	e, _, _ := signedIn(t)
	before := len(e.mailsTo(testMail))
	known := e.post("/mcu-submit/forgot", url.Values{"email": {testMail}})
	unknown := e.post("/mcu-submit/forgot", url.Values{"email": {"nobody@example.com"}})
	if known.Code != http.StatusOK || known.Body.String() != unknown.Body.String() {
		t.Fatalf("known %d, unknown %d; want identical replies", known.Code, unknown.Code)
	}
	mails := e.mailsTo(testMail)
	if len(mails) != before+1 || !strings.Contains(mails[len(mails)-1].Body, "/mcu-submit/reset?token=") {
		t.Fatalf("the active account did not get a reset link")
	}
	if len(e.mailsTo("nobody@example.com")) != 0 {
		t.Fatal("an unknown address was mailed")
	}
}

func TestForgotPasswordSendsNothingToADisabledAccount(t *testing.T) {
	e, id, _ := signedIn(t)
	if err := e.store.Disable(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	before := len(e.mailsTo(testMail))
	e.post("/mcu-submit/forgot", url.Values{"email": {testMail}})
	if len(e.mailsTo(testMail)) != before {
		t.Fatal("a disabled account was sent a reset link")
	}
}

func (e *env) resetToken(t *testing.T) string {
	t.Helper()
	e.post("/mcu-submit/forgot", url.Values{"email": {testMail}})
	mails := e.mailsTo(testMail)
	return tokenIn(t, mails[len(mails)-1].Body)
}

func TestResetLinkShowsAFormWithoutUsingItUp(t *testing.T) {
	e, _, _ := signedIn(t)
	token := e.resetToken(t)
	for i := 0; i < 2; i++ {
		if rec := e.get("/mcu-submit/reset?token=" + token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="password"`) {
			t.Fatalf("GET %d = %d", i, rec.Code)
		}
	}
	if rec := e.get("/mcu-submit/reset?token=" + strings.Repeat("0", 64)); rec.Code != http.StatusBadRequest {
		t.Errorf("a made-up token = %d, want 400", rec.Code)
	}
}

func TestResetChangesThePasswordSignsOutOldSessionsAndUsesTheLinkOnce(t *testing.T) {
	e, _, old := signedIn(t)
	token := e.resetToken(t)

	// A weak password or a typo must not use the link up.
	for _, f := range []url.Values{
		{"token": {token}, "password": {"short"}, "confirm": {"short"}},
		{"token": {token}, "password": {newPass}, "confirm": {newPass + "x"}},
		{"token": {token}, "password": {testMail}, "confirm": {testMail}}, // the account email
	} {
		if rec := e.post("/mcu-submit/reset", f); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad reset = %d, want 400", rec.Code)
		}
	}
	if rec := e.post("/mcu-submit/reset", url.Values{"token": {token}, "password": {newPass}, "confirm": {newPass}}); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d\n%s", rec.Code, rec.Body)
	}
	if rec := e.get("/mcu-submit/home", old); rec.Code != http.StatusSeeOther {
		t.Error("the session from before the reset still works")
	}
	if _, c := e.signIn(testUser, newPass); c == nil {
		t.Error("the new password does not sign in")
	}
	if _, c := e.signIn(testUser, testPass); c != nil {
		t.Error("the old password still signs in")
	}
	if rec := e.post("/mcu-submit/reset", url.Values{"token": {token}, "password": {newPass + "z"}, "confirm": {newPass + "z"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("reusing the link = %d, want 400", rec.Code)
	}
}

func TestResetClearsALockout(t *testing.T) {
	e, _, _ := signedIn(t)
	for i := 0; i < 5; i++ {
		e.signIn(testUser, "wrong-password-"+itoa(int64(i)))
	}
	if _, c := e.signIn(testUser, testPass); c != nil {
		t.Fatal("the account should be locked")
	}
	token := e.resetToken(t)
	e.post("/mcu-submit/reset", url.Values{"token": {token}, "password": {newPass}, "confirm": {newPass}})
	if _, c := e.signIn(testUser, newPass); c == nil {
		t.Fatal("a reset should clear the lock")
	}
}

func TestResetLinkOfAnAccountDisabledMeanwhileDoesNothing(t *testing.T) {
	e, id, _ := signedIn(t)
	token := e.resetToken(t)
	if err := e.store.Disable(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if rec := e.post("/mcu-submit/reset", url.Values{"token": {token}, "password": {newPass}, "confirm": {newPass}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
}

// ---- settings ---------------------------------------------------------------------------------

func TestSettingsNeedsASession(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/mcu-submit/settings"}, {"POST", "/mcu-submit/settings/password"},
		{"POST", "/mcu-submit/settings/email"}, {"POST", "/mcu-submit/settings/reminders"},
	} {
		if rec := e.do(c.method, c.path, nil, nil, nil); rec.Code != http.StatusSeeOther {
			t.Errorf("%s %s = %d, want a redirect to sign-in", c.method, c.path, rec.Code)
		}
	}
}

func TestChangePasswordNeedsTheCurrentOneAndKeepsOnlyThisSessionAlive(t *testing.T) {
	e, _, old := signedIn(t)
	_, other := e.signIn(testUser, testPass) // a second device

	for _, f := range []url.Values{
		{"current": {"wrong-current-pass"}, "password": {newPass}, "confirm": {newPass}},
		{"current": {testPass}, "password": {"short"}, "confirm": {"short"}},
		{"current": {testPass}, "password": {newPass}, "confirm": {"different-phrase-9"}},
	} {
		if rec := e.post("/mcu-submit/settings/password", f, old); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad change = %d, want 400 or 401", rec.Code)
		}
	}
	if _, c := e.signIn(testUser, testPass); c == nil {
		t.Fatal("a refused change altered the password")
	}
	rec := e.post("/mcu-submit/settings/password", url.Values{"current": {testPass}, "password": {newPass}, "confirm": {newPass}}, old)
	if rec.Code != http.StatusOK {
		t.Fatalf("change = %d\n%s", rec.Code, rec.Body)
	}
	fresh := sessionCookie(rec)
	if fresh == nil || e.get("/mcu-submit/home", fresh).Code != http.StatusOK {
		t.Error("the session that changed the password should stay signed in")
	}
	if e.get("/mcu-submit/home", other).Code != http.StatusSeeOther {
		t.Error("another session survived the password change")
	}
	if _, c := e.signIn(testUser, newPass); c == nil {
		t.Error("the new password does not work")
	}
}

func TestWrongCurrentPasswordCountsTowardsTheLockout(t *testing.T) {
	e, id, c := signedIn(t)
	for i := 0; i < 5; i++ {
		e.post("/mcu-submit/settings/password", url.Values{"current": {"wrong-current-" + itoa(int64(i))}, "password": {newPass}, "confirm": {newPass}}, c)
	}
	locked, err := e.store.IsLocked(context.Background(), id)
	if err != nil || !locked {
		t.Fatalf("locked = %v, %v; a stolen session must not be able to guess the current password", locked, err)
	}
}

func TestChangeEmailMailsTheNewAddressAndWarnsTheOldOneAndOnlyChangesAfterTheLink(t *testing.T) {
	e, id, c := signedIn(t)
	rec := e.post("/mcu-submit/settings/email", url.Values{"email": {"budi.new@example.com"}, "current": {testPass}}, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	to := e.mailsTo("budi.new@example.com")
	if len(to) != 1 || !strings.Contains(to[0].Body, "/mcu-submit/email-change?token=") {
		t.Fatalf("the new address did not get the link: %+v", to)
	}
	warn := e.mailsTo(testMail)
	if len(warn) == 0 || !strings.Contains(warn[len(warn)-1].Subject, "email") {
		t.Fatalf("the old address was not warned: %+v", warn)
	}
	if u, _ := e.store.ByID(context.Background(), id); u.Email != testMail {
		t.Fatalf("email changed before the link was opened: %s", u.Email)
	}
	token := tokenIn(t, to[0].Body)
	if rec := e.get("/mcu-submit/email-change?token=" + token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Confirm") {
		t.Fatalf("GET = %d", rec.Code)
	}
	if u, _ := e.store.ByID(context.Background(), id); u.Email != testMail {
		t.Fatal("opening the link changed the email: only the Confirm button may")
	}
	if rec := e.post("/mcu-submit/email-change", url.Values{"token": {token}}); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d\n%s", rec.Code, rec.Body)
	}
	if u, _ := e.store.ByID(context.Background(), id); u.Email != "budi.new@example.com" {
		t.Fatalf("email = %s after confirming", u.Email)
	}
	if rec := e.post("/mcu-submit/email-change", url.Values{"token": {token}}); rec.Code != http.StatusBadRequest {
		t.Errorf("reusing the link = %d, want 400", rec.Code)
	}
}

func TestChangeEmailRefusesAWrongPasswordABadAddressAndOneInUse(t *testing.T) {
	e, _, c := signedIn(t)
	if _, err := e.store.Register(context.Background(), "taken@example.com", "Someone", e.companyID, "HR"); err != nil {
		t.Fatal(err)
	}
	before := len(e.mail.sent)
	for name, f := range map[string]url.Values{
		"wrong password": {"email": {"new@example.com"}, "current": {"wrong-current-pass"}},
		"bad address":    {"email": {"not an email"}, "current": {testPass}},
		"already used":   {"email": {"taken@example.com"}, "current": {testPass}},
		"same address":   {"email": {testMail}, "current": {testPass}},
	} {
		if rec := e.post("/mcu-submit/settings/email", f, c); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 400 or 401", name, rec.Code)
		}
	}
	if len(e.mail.sent) != before {
		t.Fatal("a refused change sent mail")
	}
}

func TestEmailChangeLinkFailsIfSomeoneTookTheAddressMeanwhile(t *testing.T) {
	e, id, c := signedIn(t)
	e.post("/mcu-submit/settings/email", url.Values{"email": {"race@example.com"}, "current": {testPass}}, c)
	token := tokenIn(t, e.mailsTo("race@example.com")[0].Body)
	if _, err := e.store.Register(context.Background(), "race@example.com", "Other", e.companyID, "HR"); err != nil {
		t.Fatal(err)
	}
	if rec := e.post("/mcu-submit/email-change", url.Values{"token": {token}}); rec.Code != http.StatusConflict {
		t.Fatalf("= %d, want 409", rec.Code)
	}
	if u, _ := e.store.ByID(context.Background(), id); u.Email != testMail {
		t.Fatalf("email = %s, must be unchanged", u.Email)
	}
}

func TestRemindersCanBeSwitchedOffAndOn(t *testing.T) {
	e, id, c := signedIn(t)
	read := func() bool { u, _ := e.store.ByID(context.Background(), id); return u.RemindersEnabled }
	if !read() {
		t.Fatal("reminders should default to on")
	}
	e.post("/mcu-submit/settings/reminders", url.Values{}, c) // an unchecked box sends nothing
	if read() {
		t.Fatal("reminders still on after switching off")
	}
	e.post("/mcu-submit/settings/reminders", url.Values{"reminders": {"on"}}, c)
	if !read() {
		t.Fatal("reminders still off after switching on")
	}
}

func TestSettingsPageShowsTheCurrentEmailAndNeverAPasswordHash(t *testing.T) {
	e, _, c := signedIn(t)
	body := e.get("/mcu-submit/settings", c).Body.String()
	if !strings.Contains(body, testMail) || strings.Contains(body, "$2a$") || strings.Contains(body, "$2b$") {
		t.Fatal("settings page shows the wrong things")
	}
}
