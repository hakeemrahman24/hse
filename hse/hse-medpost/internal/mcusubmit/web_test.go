package mcusubmit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"hse-medpost/internal/turnstile"
)

// fakeMailer keeps every message so a test can read the link a person would have been sent.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
	fail error
}

type sentMail struct{ To, Subject, Body string }

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, sentMail{to, subject, body})
	return nil
}

func (f *fakeMailer) last(t *testing.T) sentMail {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no mail was sent")
	}
	return f.sent[len(f.sent)-1]
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type env struct {
	t         *testing.T
	api       *API
	store     *Store
	mux       *http.ServeMux
	mail      *fakeMailer
	companyID int64
	adminID   int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s, _, co := newTestStore(t)
	// The Archived column is added to Company by a later startup step in the real app.
	if _, err := s.DB.Exec(`ALTER TABLE Company ADD COLUMN Archived INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	api, err := New(s, "test-secret", "../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailer{}
	api.SetTurnstile(turnstile.DevVerifier{}, "", true)
	api.SetMailer(mail, "https://portal.example")
	api.MailDomainOK = func(context.Context, string) bool { return true }
	api.limits.RegisterPerIP, api.limits.RegisterPerEmail, api.limits.SignInPerIP = 1000, 1000, 1000
	mux := http.NewServeMux()
	api.Register(mux)
	adminID := mustAdmin(t, s)
	return &env{t: t, api: api, store: s, mux: mux, mail: mail, companyID: co, adminID: adminID}
}

func (e *env) do(method, path string, form url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	body := strings.NewReader("")
	if form != nil {
		form.Set("cf-turnstile-response", "dev-pass")
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return e.do(http.MethodGet, path, nil, cookies, nil)
}

func (e *env) post(path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, path, form, cookies, nil)
}

var tokenInMail = regexp.MustCompile(`token=([0-9a-f]{64})`)

func tokenFrom(t *testing.T, m sentMail) string {
	t.Helper()
	match := tokenInMail.FindStringSubmatch(m.Body)
	if match == nil {
		t.Fatalf("no token link in mail:\n%s", m.Body)
	}
	return match[1]
}

func (e *env) registerForm(email string) url.Values {
	return url.Values{
		"name": {"Budi Santoso"}, "email": {email}, "position": {"HR Officer"},
		"company_id":     {itoa(e.companyID)},
		"romif_accepted": {"1"}, "romif_name": {"Budi Santoso"},
	}
}

// signIn posts the sign-in form and returns the response and the session cookie, if one was set.
func (e *env) signIn(username, password string) (*httptest.ResponseRecorder, *http.Cookie) {
	rec := e.post("/mcu-submit/signin", url.Values{"username": {username}, "password": {password}})
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return rec, c
		}
	}
	return rec, nil
}

// onboard walks a person through the whole real flow: register, open the email link, admin
// approval, the setup link, and chooses a username and password. It returns the submitter's id.
func (e *env) onboard(email, username, password string) int64 {
	e.t.Helper()
	if rec := e.post("/mcu-submit/register", e.registerForm(email)); rec.Code != http.StatusOK {
		e.t.Fatalf("register = %d\n%s", rec.Code, rec.Body)
	}
	verify := tokenFrom(e.t, e.mail.last(e.t))
	if rec := e.post("/mcu-submit/verify", url.Values{"token": {verify}}); rec.Code != http.StatusOK {
		e.t.Fatalf("verify = %d\n%s", rec.Code, rec.Body)
	}
	u, err := e.store.ByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.Approve(context.Background(), u.ID, e.adminID); err != nil {
		e.t.Fatal(err)
	}
	setup, err := e.store.IssueToken(context.Background(), u.ID, PurposeSetup, "", setupTTL)
	if err != nil {
		e.t.Fatal(err)
	}
	rec := e.post("/mcu-submit/setup", url.Values{"token": {setup}, "username": {username}, "password": {password}, "confirm": {password}})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("setup = %d\n%s", rec.Code, rec.Body)
	}
	return u.ID
}

func TestRegisterCreatesARequestAndMailsAVerifyLink(t *testing.T) {
	e := newEnv(t)
	rec := e.post("/mcu-submit/register", e.registerForm("Budi@Example.com"))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("register = %d\n%s", rec.Code, rec.Body)
	}
	u, err := e.store.ByEmail(context.Background(), "budi@example.com")
	if err != nil || u.Status != StatusRequested || u.Name != "Budi Santoso" || u.EmailVerifiedAt != "" {
		t.Fatalf("stored submitter = %+v, %v", u, err)
	}
	m := e.mail.last(t)
	if m.To != "budi@example.com" || !strings.Contains(m.Body, "https://portal.example/mcu-submit/verify?token=") {
		t.Fatalf("verify mail = %+v", m)
	}
}

// The reply must not depend on whether the address is already registered, or the form tells
// anyone who asks which emails have accounts.
func TestRegisterGivesTheSameReplyForAnExistingEmail(t *testing.T) {
	e := newEnv(t)
	first := e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	again := e.post("/mcu-submit/register", e.registerForm("budi@example.com"))

	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Fatalf("replies differ:\nfirst %d %q\nagain %d %q", first.Code, first.Body, again.Code, again.Body)
	}
	e.onboard("other@example.com", "other.o", "pelican-orbit-77")
	known := e.post("/mcu-submit/register", e.registerForm("other@example.com"))
	if known.Code != first.Code || known.Body.String() != first.Body.String() {
		t.Fatalf("a verified account gets a different reply:\n%d %q", known.Code, known.Body)
	}
	if m := e.mail.last(t); !strings.Contains(strings.ToLower(m.Subject), "already") {
		t.Fatalf("an existing account should be mailed a notice, got %+v", m)
	}
	var n int
	_ = e.store.DB.QueryRow(`SELECT COUNT(*) FROM Submitter WHERE Email = 'budi@example.com'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d rows for one email", n)
	}
}

func TestRegisterRefusesBadInput(t *testing.T) {
	e := newEnv(t)
	cases := map[string]func(v url.Values){
		"missing name":         func(v url.Values) { v.Set("name", "  123 ") },
		"bad email":            func(v url.Values) { v.Set("email", "not-an-email") },
		"missing position":     func(v url.Values) { v.Set("position", "") },
		"unknown company":      func(v url.Values) { v.Set("company_id", "9999") },
		"company not a number": func(v url.Values) { v.Set("company_id", "abc") },
		"romif not ticked":     func(v url.Values) { v.Del("romif_accepted") },
		"signed another name":  func(v url.Values) { v.Set("romif_name", "Somebody Else") },
		"no signature":         func(v url.Values) { v.Set("romif_name", "") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := e.registerForm("budi@example.com")
			mutate(v)
			rec := e.post("/mcu-submit/register", v)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body)
			}
		})
	}
	var n int
	_ = e.store.DB.QueryRow(`SELECT COUNT(*) FROM Submitter WHERE IsBuiltIn = 0`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d accounts were created from bad input", n)
	}
}

func TestRegisterRefusesAnArchivedCompanyAndADomainThatCannotReceiveMail(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.DB.Exec(`UPDATE Company SET Archived = 1 WHERE CompanyID = ?`, e.companyID); err != nil {
		t.Fatal(err)
	}
	if rec := e.post("/mcu-submit/register", e.registerForm("budi@example.com")); rec.Code != http.StatusBadRequest {
		t.Fatalf("archived company = %d, want 400", rec.Code)
	}
	if _, err := e.store.DB.Exec(`UPDATE Company SET Archived = 0 WHERE CompanyID = ?`, e.companyID); err != nil {
		t.Fatal(err)
	}
	e.api.MailDomainOK = func(context.Context, string) bool { return false }
	if rec := e.post("/mcu-submit/register", e.registerForm("budi@nowhere.invalid")); rec.Code != http.StatusBadRequest {
		t.Fatalf("a domain with no mail server = %d, want 400", rec.Code)
	}
}

func TestRegisterNeedsAPassedTurnstile(t *testing.T) {
	e := newEnv(t)
	form := e.registerForm("budi@example.com")
	req := httptest.NewRequest(http.MethodPost, "/mcu-submit/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded") // no cf-turnstile-response at all
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if _, err := e.store.ByEmail(context.Background(), "budi@example.com"); err == nil {
		t.Fatal("an account was created without the Cloudflare check")
	}
}

func TestRegisterIsRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	e.api.limits.RegisterPerIP = 2
	for i, email := range []string{"one@example.com", "two@example.com"} {
		if rec := e.post("/mcu-submit/register", e.registerForm(email)); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i+1, rec.Code)
		}
	}
	if rec := e.post("/mcu-submit/register", e.registerForm("third@example.com")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", rec.Code)
	}
}

func TestVerifyLinkShowsAButtonAndOnlyThePostUsesIt(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	token := tokenFrom(t, e.mail.last(t))

	page := e.get("/mcu-submit/verify?token=" + token)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="token"`) {
		t.Fatalf("GET verify = %d\n%s", page.Code, page.Body)
	}
	u, _ := e.store.ByEmail(context.Background(), "budi@example.com")
	if u.EmailVerifiedAt != "" {
		t.Fatal("opening the link must not verify the email by itself")
	}
	if rec := e.post("/mcu-submit/verify", url.Values{"token": {token}}); rec.Code != http.StatusOK {
		t.Fatalf("POST verify = %d", rec.Code)
	}
	u, _ = e.store.ByEmail(context.Background(), "budi@example.com")
	if u.EmailVerifiedAt == "" {
		t.Fatal("the email was not marked verified")
	}
	if rec := e.post("/mcu-submit/verify", url.Values{"token": {token}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("second use = %d, want 400", rec.Code)
	}
	if rec := e.get("/mcu-submit/verify?token=" + strings.Repeat("0", 64)); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown token page = %d, want 400", rec.Code)
	}
}

func TestSetupRefusesAWeakPasswordWithoutUsingUpTheLink(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	_ = e.post("/mcu-submit/verify", url.Values{"token": {tokenFrom(t, e.mail.last(t))}})
	u, _ := e.store.ByEmail(context.Background(), "budi@example.com")
	_ = e.store.Approve(context.Background(), u.ID, e.adminID)
	setup, _ := e.store.IssueToken(context.Background(), u.ID, PurposeSetup, "", setupTTL)

	weak := e.post("/mcu-submit/setup", url.Values{"token": {setup}, "username": {"budi.s"}, "password": {"short"}, "confirm": {"short"}})
	if weak.Code != http.StatusBadRequest {
		t.Fatalf("weak password = %d, want 400", weak.Code)
	}
	mismatch := e.post("/mcu-submit/setup", url.Values{"token": {setup}, "username": {"budi.s"}, "password": {"pelican-orbit-77"}, "confirm": {"pelican-orbit-78"}})
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("mismatched confirmation = %d, want 400", mismatch.Code)
	}
	good := e.post("/mcu-submit/setup", url.Values{"token": {setup}, "username": {"budi.s"}, "password": {"pelican-orbit-77"}, "confirm": {"pelican-orbit-77"}})
	if good.Code != http.StatusOK {
		t.Fatalf("the link was used up by earlier mistakes: %d\n%s", good.Code, good.Body)
	}
	if _, cookie := e.signIn("budi.s", "pelican-orbit-77"); cookie == nil {
		t.Fatal("cannot sign in after setup")
	}
}

func TestSetupRefusesAnUnapprovedAccount(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	u, _ := e.store.ByEmail(context.Background(), "budi@example.com")
	setup, _ := e.store.IssueToken(context.Background(), u.ID, PurposeSetup, "", setupTTL)

	rec := e.post("/mcu-submit/setup", url.Values{"token": {setup}, "username": {"budi.s"}, "password": {"pelican-orbit-77"}, "confirm": {"pelican-orbit-77"}})
	if rec.Code == http.StatusOK {
		t.Fatal("an account that was never approved finished setup")
	}
}

func TestSignInSetsAScopedSessionCookieAndHomeShowsTheName(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")

	rec, cookie := e.signIn("Budi.S", "pelican-orbit-77")
	if cookie == nil || rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/mcu-submit/home" {
		t.Fatalf("sign-in = %d %q, cookie %v", rec.Code, rec.Header().Get("Location"), cookie)
	}
	if !cookie.HttpOnly || cookie.Path != "/mcu-submit" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes = %+v", cookie)
	}
	home := e.get("/mcu-submit/home", cookie)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "Budi Santoso") {
		t.Fatalf("home = %d\n%s", home.Code, home.Body)
	}
}

func TestSignInFailureNeverSaysWhichPartWasWrong(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")

	wrongPassword, c1 := e.signIn("budi.s", "not-the-password-1")
	unknownUser, c2 := e.signIn("nobody.here", "not-the-password-1")
	if c1 != nil || c2 != nil {
		t.Fatal("a failed sign-in set a cookie")
	}
	if wrongPassword.Code != http.StatusUnauthorized || unknownUser.Code != http.StatusUnauthorized {
		t.Fatalf("codes = %d, %d; want 401, 401", wrongPassword.Code, unknownUser.Code)
	}
	// The form echoes the typed username back, so compare the error message, not the whole page.
	errorOf := func(body string) string {
		m := regexp.MustCompile(`(?s)<p class="mp-error".*?</p>`).FindString(body)
		if m == "" {
			t.Fatalf("no error message in the page:\n%s", body)
		}
		return m
	}
	if errorOf(wrongPassword.Body.String()) != errorOf(unknownUser.Body.String()) {
		t.Fatal("the two failures are worded differently, which reveals whether the username exists")
	}
}

func TestSignInIsRefusedUntilTheAccountIsActive(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	if _, cookie := e.signIn("budi@example.com", "anything-at-all-1"); cookie != nil {
		t.Fatal("an unapproved request signed in")
	}
	id := e.onboard("pending@example.com", "pending.p", "pelican-orbit-77")
	_ = e.store.Disable(context.Background(), id)
	if _, cookie := e.signIn("pending.p", "pelican-orbit-77"); cookie != nil {
		t.Fatal("a disabled account signed in")
	}
}

func TestFiveWrongPasswordsLockEvenTheRightPasswordOut(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	for i := 0; i < 5; i++ {
		e.signIn("budi.s", "wrong-password-"+strconv.Itoa(i))
	}
	rec, cookie := e.signIn("budi.s", "pelican-orbit-77")
	if cookie != nil || rec.Code != http.StatusUnauthorized {
		t.Fatalf("a locked account signed in: %d", rec.Code)
	}
}

func TestSignInIsRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	e.api.limits.SignInPerIP = 3
	for i := 0; i < 3; i++ {
		e.signIn("nobody.here", "whatever-password-1")
	}
	if rec, _ := e.signIn("nobody.here", "whatever-password-1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth attempt = %d, want 429", rec.Code)
	}
}

func TestHomeNeedsASession(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/mcu-submit/home")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/mcu-submit/signin" {
		t.Fatalf("home without a session = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	forged := &http.Cookie{Name: cookieName, Value: "eyJ1aWQiOjF9.bogus"}
	if rec := e.get("/mcu-submit/home", forged); rec.Code != http.StatusSeeOther {
		t.Fatalf("a forged cookie = %d, want a redirect to sign in", rec.Code)
	}
}

func TestDisablingAnAccountEndsItsOpenSession(t *testing.T) {
	e := newEnv(t)
	id := e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, cookie := e.signIn("budi.s", "pelican-orbit-77")
	if e.get("/mcu-submit/home", cookie).Code != http.StatusOK {
		t.Fatal("setup: the session should work")
	}
	if err := e.store.Disable(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if rec := e.get("/mcu-submit/home", cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("a disabled account's old cookie = %d, want a redirect to sign in", rec.Code)
	}
}

func TestAPasswordChangeEndsOlderSessions(t *testing.T) {
	e := newEnv(t)
	id := e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, cookie := e.signIn("budi.s", "pelican-orbit-77")
	hash, _ := HashPassword("another-pelican-88")
	_ = e.store.SetPassword(context.Background(), id, hash)

	if rec := e.get("/mcu-submit/home", cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("old cookie after a password change = %d, want a redirect", rec.Code)
	}
}

func TestSignOutClearsTheCookie(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, cookie := e.signIn("budi.s", "pelican-orbit-77")

	rec := e.post("/mcu-submit/signout", url.Values{}, cookie)
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && (c.MaxAge < 0 || c.Value == "") {
			cleared = true
		}
	}
	if !cleared || rec.Code != http.StatusSeeOther {
		t.Fatalf("signout = %d, cleared %v", rec.Code, cleared)
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, cookie := e.signIn("budi.s", "pelican-orbit-77")

	rec := e.do(http.MethodPost, "/mcu-submit/signout", url.Values{}, []*http.Cookie{cookie}, map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d, want 403", rec.Code)
	}
	if e.get("/mcu-submit/home", cookie).Code != http.StatusOK {
		t.Fatal("the refused sign-out still ended the session")
	}
	same := e.do(http.MethodPost, "/mcu-submit/signout", url.Values{}, []*http.Cookie{cookie}, map[string]string{"Origin": "http://example.com"})
	if same.Code == http.StatusForbidden {
		t.Fatal("a same-origin POST was refused")
	}
}

func TestPagesCarrySecurityHeaders(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/mcu-submit", "/mcu-submit/signin", "/mcu-submit/register"} {
		rec := e.get(path)
		h := rec.Header()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		// The links in mails carry a token in the query: it must not leak in a Referer header.
		if h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" ||
			h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s headers = %v", path, h)
		}
	}
}

func TestAdminPageIsOnlyForTheAdmin(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, member := e.signIn("budi.s", "pelican-orbit-77")

	if rec := e.get("/mcu-submit/admin", member); rec.Code != http.StatusNotFound {
		t.Fatalf("a submitter on /admin = %d, want 404", rec.Code)
	}
	if rec := e.get("/mcu-submit/admin"); rec.Code != http.StatusSeeOther {
		t.Fatalf("no session on /admin = %d, want a redirect", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/approve", url.Values{"id": {"1"}}, member); rec.Code != http.StatusNotFound {
		t.Fatalf("a submitter approving = %d, want 404", rec.Code)
	}
	_, admin := e.signIn("admin", "pelican-orbit-77")
	if admin == nil {
		t.Fatal("the built-in admin cannot sign in")
	}
	if rec := e.get("/mcu-submit/admin", admin); rec.Code != http.StatusOK {
		t.Fatalf("admin page = %d", rec.Code)
	}
}

func TestAdminApprovesAndMailsASetupLink(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	_ = e.post("/mcu-submit/verify", url.Values{"token": {tokenFrom(t, e.mail.last(t))}})
	u, _ := e.store.ByEmail(context.Background(), "budi@example.com")
	_, admin := e.signIn("admin", "pelican-orbit-77")

	page := e.get("/mcu-submit/admin", admin)
	if !strings.Contains(page.Body.String(), "Budi Santoso") || !strings.Contains(page.Body.String(), "PT Example") {
		t.Fatalf("the request is not listed:\n%s", page.Body)
	}
	rec := e.post("/mcu-submit/admin/approve", url.Values{"id": {itoa(u.ID)}}, admin)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve = %d\n%s", rec.Code, rec.Body)
	}
	got, _ := e.store.ByID(context.Background(), u.ID)
	if got.Status != StatusApproved {
		t.Fatalf("status = %s", got.Status)
	}
	m := e.mail.last(t)
	if m.To != "budi@example.com" || !strings.Contains(m.Body, "/mcu-submit/setup?token=") {
		t.Fatalf("approval mail = %+v", m)
	}
	events, _ := e.store.Events(context.Background(), EntitySubmitter, u.ID)
	found := false
	for _, ev := range events {
		if ev.Action == "Approved" && ev.ActorID == e.adminID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Approved event by the admin: %+v", events)
	}
}

func TestAdminRejectsWithAReasonAndTheSubmitterIsTold(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	_ = e.post("/mcu-submit/verify", url.Values{"token": {tokenFrom(t, e.mail.last(t))}})
	u, _ := e.store.ByEmail(context.Background(), "budi@example.com")
	_, admin := e.signIn("admin", "pelican-orbit-77")

	if rec := e.post("/mcu-submit/admin/reject", url.Values{"id": {itoa(u.ID)}, "reason": {""}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("reject without a reason = %d, want 400", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/reject", url.Values{"id": {itoa(u.ID)}, "reason": {"Company not recognised"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject = %d", rec.Code)
	}
	got, _ := e.store.ByID(context.Background(), u.ID)
	if got.Status != StatusRejected {
		t.Fatalf("status = %s", got.Status)
	}
	if m := e.mail.last(t); !strings.Contains(m.Body, "Company not recognised") {
		t.Fatalf("rejection mail = %+v", m)
	}
}

func TestTheBuiltInAdminCannotBeActedOnThroughTheAdminPage(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signIn("admin", "pelican-orbit-77")

	if rec := e.post("/mcu-submit/admin/disable", url.Values{"id": {itoa(e.adminID)}}, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("disabling the built-in admin = %d, want 404", rec.Code)
	}
	if page := e.get("/mcu-submit/admin", admin); strings.Contains(page.Body.String(), "admin@portal.invalid") {
		t.Fatal("the hidden admin is listed")
	}
}

func TestAMailFailureDoesNotRevealOrBreakRegistration(t *testing.T) {
	e := newEnv(t)
	e.mail.fail = context.DeadlineExceeded
	rec := e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("register with a failing mailer = %d\n%s", rec.Code, rec.Body)
	}
}

func TestPageOutputIsHTMLEscaped(t *testing.T) {
	e := newEnv(t)
	// The cleaner would already strip these, so write the raw value straight into the database.
	id := e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	if _, err := e.store.DB.Exec(`UPDATE Submitter SET Name = ? WHERE SubmitterID = ?`, `Budi <b>Santoso</b>`, id); err != nil {
		t.Fatal(err)
	}
	_, cookie := e.signIn("budi.s", "pelican-orbit-77")

	body := e.get("/mcu-submit/home", cookie).Body.String()
	if strings.Contains(body, "<b>Santoso") {
		t.Fatalf("a name was written into the page unescaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;b&gt;Santoso") {
		t.Fatalf("expected the escaped name in the page:\n%s", body)
	}
}

// An address that no route claims must answer with the portal's own page (styled, with the header),
// not the server's plain "404 page not found" text; signed in, it keeps the sidebar.
func TestUnknownPortalAddressGivesThePortalsOwnNotFoundPage(t *testing.T) {
	e := newEnv(t)

	rec := e.get("/mcu-submit/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown address = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Page not found") || !strings.Contains(body, "mcu-portal.css") || strings.Contains(body, "app-sidebar") {
		t.Fatalf("signed out: want the portal's page without a sidebar, got:\n%s", body)
	}

	_, admin := e.signIn("admin", "pelican-orbit-77")
	rec = e.get("/mcu-submit/nope/deeper", admin)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Page not found") || !strings.Contains(rec.Body.String(), "app-sidebar") {
		t.Fatalf("signed in: want 404 with the sidebar, got %d", rec.Code)
	}

	// A real address with the wrong method is still the router's 405 with an Allow header, not a 404.
	rec = e.post("/mcu-submit/home", url.Values{}, admin)
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "GET") {
		t.Fatalf("POST to a GET page = %d (Allow %q), want 405 allowing GET", rec.Code, rec.Header().Get("Allow"))
	}
	rec = e.get("/mcu-submit/signout", admin)
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Fatalf("GET to a POST route = %d (Allow %q), want 405 allowing POST", rec.Code, rec.Header().Get("Allow"))
	}

	// The real pages still answer as before.
	if rec := e.get("/mcu-submit/signin"); rec.Code != http.StatusOK {
		t.Fatalf("sign-in page = %d, want 200", rec.Code)
	}
	if rec := e.get("/mcu-submit"); rec.Code != http.StatusOK {
		t.Fatalf("landing = %d, want 200", rec.Code)
	}
}

func TestRegisterRecordsTheSignedROMIFInTheAuditTrail(t *testing.T) {
	e := newEnv(t)
	v := e.registerForm("budi@example.com")
	v.Set("romif_name", "budi  SANTOSO") // same person, sloppy typing
	if rec := e.post("/mcu-submit/register", v); rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	var detail string
	err := e.store.DB.QueryRow(`SELECT Detail FROM McuSubmissionEvent WHERE Action = 'RomifSigned'`).Scan(&detail)
	if err != nil {
		t.Fatalf("no RomifSigned event: %v", err)
	}
	if want := "ROMIF " + romifVersion + " en: Budi Santoso"; detail != want {
		t.Errorf("detail = %q, want %q", detail, want)
	}
}

// An account made before the ROMIF existed has no RomifSigned event. It is sent to the ROMIF page
// at its next sign-in, and after signing it goes on to Home.
func TestAccountWithoutAROMIFMustSignBeforeUsingThePortal(t *testing.T) {
	e, id, c := signedIn(t)
	if _, err := e.store.DB.Exec(`DELETE FROM McuSubmissionEvent WHERE Action = 'RomifSigned'`); err != nil {
		t.Fatal(err)
	}
	rec := e.get("/mcu-submit/home", c)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/mcu-submit/romif" {
		t.Fatalf("home = %d to %q, want a redirect to the ROMIF page", rec.Code, rec.Header().Get("Location"))
	}
	if page := e.get("/mcu-submit/romif", c); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Sign and continue") {
		t.Fatalf("romif page = %d", page.Code)
	}
	if rec := e.post("/mcu-submit/romif", url.Values{"romif_accepted": {"1"}, "romif_name": {"Someone Else"}}, c); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong name = %d, want 400", rec.Code)
	}
	if rec := e.post("/mcu-submit/romif", url.Values{"romif_name": {"Budi Santoso"}}, c); rec.Code != http.StatusBadRequest {
		t.Errorf("not ticked = %d, want 400", rec.Code)
	}
	if rec := e.post("/mcu-submit/romif", url.Values{"romif_accepted": {"1"}, "romif_name": {"budi santoso"}}, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("signing = %d, want a redirect", rec.Code)
	}
	if signed, _ := e.store.HasSignedRomif(context.Background(), id); !signed {
		t.Fatal("no RomifSigned event after signing")
	}
	if rec := e.get("/mcu-submit/home", c); rec.Code != http.StatusOK {
		t.Errorf("home after signing = %d", rec.Code)
	}
	if rec := e.get("/mcu-submit/romif", c); rec.Code != http.StatusSeeOther {
		t.Errorf("romif page once signed = %d, want a redirect home", rec.Code)
	}
}

// An account made before names were cleaned may hold characters a typed name can never contain
// (digits here). It must still be able to sign.
func TestOldAccountWithOddCharactersInItsNameCanStillSignTheROMIF(t *testing.T) {
	e, id, c := signedIn(t)
	if _, err := e.store.DB.Exec(`UPDATE Submitter SET Name = 'Budi 2 Santoso' WHERE SubmitterID = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB.Exec(`DELETE FROM McuSubmissionEvent WHERE Action = 'RomifSigned'`); err != nil {
		t.Fatal(err)
	}
	if rec := e.post("/mcu-submit/romif", url.Values{"romif_accepted": {"1"}, "romif_name": {"Budi Santoso"}}, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("signing = %d, want a redirect\n%s", rec.Code, rec.Body)
	}
}

func TestAdminArchiveRestoreEnableAndTheWarnedPermanentRemoval(t *testing.T) {
	e, id, member := signedIn(t)
	ctx := context.Background()
	sentDocIn(t, e.store, id, "Worker One", "Worker Two") // two documents that would go with the account
	_, admin := e.signIn("admin", "pelican-orbit-77")
	if admin == nil {
		t.Fatal("the built-in admin cannot sign in")
	}
	status := func() string {
		u, err := e.store.ByID(ctx, id)
		if err != nil {
			return "gone"
		}
		return u.Status
	}
	// Disable, Enable, Archive, Restore, Enable
	if rec := e.post("/mcu-submit/admin/disable", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusDisabled {
		t.Fatalf("disable = %d, %s", rec.Code, status())
	}
	if rec := e.post("/mcu-submit/admin/enable", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusActive {
		t.Fatalf("enable = %d, %s", rec.Code, status())
	}
	if rec := e.post("/mcu-submit/admin/archive", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusArchived {
		t.Fatalf("archive = %d, %s", rec.Code, status())
	}
	if rec, c := e.signIn(testUser, testPass); c != nil {
		t.Fatalf("an archived account signed in (%d)", rec.Code)
	}
	if page := e.get("/mcu-submit/admin", admin); strings.Contains(page.Body.String(), testMail) || !strings.Contains(page.Body.String(), "Show 1 archived accounts") {
		t.Error("the default list must hide an archived account and offer to show it")
	}
	if page := e.get("/mcu-submit/admin?archived=1", admin); !strings.Contains(page.Body.String(), testMail) || !strings.Contains(page.Body.String(), "Restore") {
		t.Error("the archived list must show the account with a Restore button")
	}
	if rec := e.post("/mcu-submit/admin/restore", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusDisabled {
		t.Fatalf("restore = %d, %s (it comes back Disabled, not Active)", rec.Code, status())
	}
	if rec := e.post("/mcu-submit/admin/enable", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusActive {
		t.Fatalf("enable after restore = %d, %s", rec.Code, status())
	}

	// The permanent removal is asked about first, with the counts.
	page := e.get("/mcu-submit/admin/delete?id="+itoa(id), admin)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "cascading database deletes") || !strings.Contains(body, "<strong>2</strong> submitted documents") {
		t.Fatalf("warning page = %d\n%s", page.Code, body)
	}
	if rec := e.post("/mcu-submit/admin/delete", url.Values{"id": {itoa(id)}}, admin); rec.Code != http.StatusSeeOther || status() != StatusActive {
		t.Fatalf("a post without the ticked box must not remove anything: %d, %s", rec.Code, status())
	}
	_, member = e.signIn(testUser, testPass) // archiving signed the old session out
	if rec := e.get("/mcu-submit/admin/delete?id="+itoa(id), member); rec.Code != http.StatusNotFound {
		t.Errorf("a submitter on the warning page = %d, want 404", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/delete", url.Values{"id": {itoa(id)}, "understood": {"1"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d", rec.Code)
	}
	if status() != "gone" {
		t.Error("the account is still there")
	}
	var n int
	_ = e.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmission WHERE FK_SubmitterID = ?`, id).Scan(&n)
	if n != 0 {
		t.Errorf("%d documents survived the account", n)
	}
	var detail string
	if err := e.store.DB.QueryRow(`SELECT Detail FROM McuSubmissionEvent WHERE Action = 'AccountRemoved'`).Scan(&detail); err != nil || strings.Contains(detail, testMail) || !strings.Contains(detail, "2 documents") {
		t.Errorf("audit detail %q, %v: want counts and no email", detail, err)
	}
	// the built-in admin is never removable, and nobody removes the account they are signed in with
	if rec := e.get("/mcu-submit/admin/delete?id="+itoa(e.adminID), admin); rec.Code != http.StatusNotFound {
		t.Errorf("warning page for the built-in admin = %d, want 404", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/delete", url.Values{"id": {itoa(e.adminID)}, "understood": {"1"}}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("removing the built-in admin = %d, want 404", rec.Code)
	}
}

// The ROMIF statement can be read again after signing: from a button on Home for the submitter, and
// the admin list shows who has signed and when.
func TestRomifStatementIsReadableAndShownInTheAdminList(t *testing.T) {
	e := newEnv(t)
	e.onboard("budi@example.com", "budi.s", "pelican-orbit-77")
	_, member := e.signIn("budi.s", "pelican-orbit-77")

	if home := e.get("/mcu-submit/home", member); !strings.Contains(home.Body.String(), "/mcu-submit/romif/statement") {
		t.Error("Home has no ROMIF statement button")
	}
	page := e.get("/mcu-submit/romif/statement", member)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Medical Team of the PT Layar Nusantara Gas") ||
		!strings.Contains(page.Body.String(), "You signed this statement on") {
		t.Errorf("statement page = %d\n%s", page.Code, page.Body)
	}
	if rec := e.get("/mcu-submit/romif/statement"); rec.Code != http.StatusSeeOther {
		t.Errorf("signed out = %d, want a redirect to sign in", rec.Code)
	}

	_, admin := e.signIn("admin", "pelican-orbit-77")
	list := e.get("/mcu-submit/admin", admin).Body.String()
	if !strings.Contains(list, "Signed 20") || strings.Contains(list, "Not signed") {
		t.Errorf("admin list should show the signature date:\n%s", list)
	}
	if rec := e.get("/mcu-submit/romif/statement", admin); rec.Code != http.StatusOK {
		t.Errorf("admin statement = %d", rec.Code)
	}
}
