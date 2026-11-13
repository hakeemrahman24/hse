package mcusubmit

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Admin adds an account; the person must replace the temporary password before anything else opens.
func TestAdminAddedAccountMustChangeItsTemporaryPassword(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signIn("admin", "pelican-orbit-77")
	form := url.Values{"name": {"Budi Santoso"}, "email": {"budi@example.com"}, "company": {itoa(e.companyID)}, "position": {"Welder"}, "username": {"budi.s"}}

	// Only the admin may add accounts.
	if rec := e.post("/mcu-submit/admin/create", form); rec.Code == http.StatusOK {
		t.Fatalf("anonymous create answered 200")
	}
	rec := e.post("/mcu-submit/admin/create", form, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}
	temp := DefaultPassword
	if !strings.Contains(rec.Body.String(), temp) {
		t.Fatalf("the page does not show the default password:\n%s", rec.Body)
	}
	mail := e.mail.last(t)
	if mail.To != "budi@example.com" || !strings.Contains(mail.Body, "/mcu-submit/signin?username=budi.s") || !strings.Contains(mail.Body, "budi.s") || !strings.Contains(mail.Body, "Welder") {
		t.Fatalf("welcome mail = %+v", mail)
	}
	if rec := e.post("/mcu-submit/admin/create", form, admin); rec.Code != http.StatusConflict {
		t.Errorf("same email again = %d, want 409", rec.Code)
	}
	if m := e.mail.last(t); m.To != "budi@example.com" || m.Subject != "You already have an account" {
		t.Errorf("the existing address was not told: %+v", m)
	}

	// Sign in with the temporary password: every page sends the person to choose a new one.
	_, cookie := e.signIn("budi.s", temp)
	if cookie == nil {
		t.Fatal("the temporary password does not sign in")
	}
	for _, path := range []string{"/mcu-submit/home", "/mcu-submit/documents", "/mcu-submit/employees"} {
		if got := e.get(path, cookie); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/mcu-submit/password" {
			t.Errorf("%s = %d to %q, want a redirect to the password page", path, got.Code, got.Header().Get("Location"))
		}
	}
	if got := e.get("/mcu-submit/password", cookie); got.Code != http.StatusOK {
		t.Fatalf("password page = %d", got.Code)
	}
	bad := func(password, confirm string) {
		t.Helper()
		if got := e.post("/mcu-submit/password", url.Values{"password": {password}, "confirm": {confirm}}, cookie); got.Code != http.StatusBadRequest {
			t.Errorf("password %q/%q = %d, want 400", password, confirm, got.Code)
		}
	}
	bad(temp, temp)                    // the same one again
	bad("brand-new-secret-9", "other") // not confirmed
	bad("123", "123")                  // too short

	done := e.post("/mcu-submit/password", url.Values{"password": {"brand-new-secret-9"}, "confirm": {"brand-new-secret-9"}}, cookie)
	if done.Code != http.StatusSeeOther {
		t.Fatalf("change = %d", done.Code)
	}
	u, _ := e.store.ByUsername(context.Background(), "budi.s")
	if must, _ := e.store.MustChangePassword(context.Background(), u.ID); must {
		t.Error("flag still set after the change")
	}
	// The old cookie is dead (session version bumped); the new password signs in and is no longer sent to /password.
	if _, again := e.signIn("budi.s", temp); again != nil {
		t.Error("the temporary password still signs in")
	}
	if _, fresh := e.signIn("budi.s", "brand-new-secret-9"); fresh == nil {
		t.Fatal("the new password does not sign in")
	}
}

// The welcome link opens the sign-in page with the username filled in.
func TestSignInPageFillsInTheUsernameFromTheWelcomeLink(t *testing.T) {
	e := newEnv(t)
	if body := e.get("/mcu-submit/signin?username=budi.s").Body.String(); !strings.Contains(body, `value="budi.s"`) {
		t.Errorf("username not filled in")
	}
	if body := e.get("/mcu-submit/signin?username=%3Cscript%3E").Body.String(); strings.Contains(body, "&lt;script") {
		t.Errorf("a bad username was echoed")
	}
}

// The name is written like a worker's name in the checkup form, the username in lower case, and an
// address whose domain cannot receive mail is refused.
func TestAdminCreateCleansNameAndUsernameAndChecksTheDomain(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signIn("admin", "pelican-orbit-77")
	form := url.Values{"name": {"  BUDI   santoso "}, "email": {"Budi@Example.com"}, "company": {itoa(e.companyID)}, "position": {"Welder"}, "username": {"Budi.S"}}
	rec := e.post("/mcu-submit/admin/create", form, admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Budi Santoso") {
		t.Fatalf("create = %d\n%s", rec.Code, rec.Body)
	}
	u, err := e.store.ByEmail(context.Background(), "budi@example.com")
	if err != nil || u.Name != "Budi Santoso" || u.Username != "budi.s" {
		t.Errorf("stored %+v, %v; want Title Case name, lower case username and email", u, err)
	}

	e.api.MailDomainOK = func(context.Context, string) bool { return false }
	form.Set("email", "siti@nonexistent-domain.test")
	form.Set("username", "siti.a")
	if rec := e.post("/mcu-submit/admin/create", form, admin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot receive mail") {
		t.Errorf("a dead domain = %d, want 400 with the domain message", rec.Code)
	}
	if _, err := e.store.ByEmail(context.Background(), "siti@nonexistent-domain.test"); err == nil {
		t.Error("an account was made for a dead domain")
	}
}

func TestAdminCreateRefusesBadInput(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signIn("admin", "pelican-orbit-77")
	base := func() url.Values {
		return url.Values{"name": {"Budi Santoso"}, "email": {"budi@example.com"}, "company": {itoa(e.companyID)}, "position": {"Welder"}, "username": {"budi.s"}}
	}
	for name, mutate := range map[string]func(url.Values){
		"no name":      func(v url.Values) { v.Set("name", "") },
		"bad email":    func(v url.Values) { v.Set("email", "not-an-email") },
		"bad username": func(v url.Values) { v.Set("username", "a b") },
		"no company":   func(v url.Values) { v.Set("company", "") },
	} {
		v := base()
		mutate(v)
		if rec := e.post("/mcu-submit/admin/create", v, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
}
