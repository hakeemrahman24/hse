package mcusubmit

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

var indonesian = &http.Cookie{Name: langCookie, Value: "id"}

func TestPagesAreEnglishUntilALanguageIsChosen(t *testing.T) {
	e := newEnv(t)
	rec := e.get("/mcu-submit/signin")
	body := rec.Body.String()
	if !strings.Contains(body, `lang="en"`) || !strings.Contains(body, "Forgot your password?") {
		t.Fatalf("default page is not English:\n%s", body)
	}
	if strings.Contains(body, "@@") {
		t.Fatal("a placeholder leaked into the page")
	}
}

func TestChoosingIndonesianSetsACookieAndTranslatesThePages(t *testing.T) {
	e := newEnv(t)
	rec := e.post("/mcu-submit/lang", url.Values{"lang": {"id"}, "next": {"/mcu-submit/register?x=1"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/mcu-submit/register?x=1" {
		t.Fatalf("choose = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	var set *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == langCookie {
			set = c
		}
	}
	if set == nil || set.Value != "id" || set.Path != basePath || !set.HttpOnly || set.SameSite != http.SameSiteLaxMode {
		t.Fatalf("language cookie = %+v", set)
	}

	for path, want := range map[string]string{
		"/mcu-submit/signin":   "Lupa kata sandi Anda?",
		"/mcu-submit/register": "Ajukan akun",
		"/mcu-submit":          "Masuk",
		"/mcu-submit/forgot":   "Kirim tautan",
	} {
		body := e.do(http.MethodGet, path, nil, []*http.Cookie{indonesian}, nil).Body.String()
		if !strings.Contains(body, want) || !strings.Contains(body, `lang="id"`) {
			t.Errorf("%s lacks %q or lang=id:\n%s", path, want, body)
		}
	}
}

func TestTranslatedErrorMessagesAndInputValuesStayIntact(t *testing.T) {
	e := newEnv(t)
	rec := e.do(http.MethodPost, "/mcu-submit/signin",
		url.Values{"username": {"Sign in"}, "password": {"wrong-password-1"}}, []*http.Cookie{indonesian}, nil)
	body := rec.Body.String()
	if !strings.Contains(body, "Username atau password salah") {
		t.Errorf("the wrong-password message was not translated:\n%s", body)
	}
	if !strings.Contains(body, `value="sign in"`) && !strings.Contains(body, `value="Sign in"`) {
		t.Errorf("what the person typed was changed:\n%s", body)
	}
}

func TestLanguageChoiceRefusesBadValuesAndOffSiteRedirects(t *testing.T) {
	e := newEnv(t)
	if rec := e.post("/mcu-submit/lang", url.Values{"lang": {"fr"}, "next": {"/mcu-submit"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown language = %d, want 400", rec.Code)
	}
	for _, next := range []string{"//evil.example/x", "https://evil.example", "/elsewhere", "/mcu-submit/../x", "\\\\evil", "", "/mcu-submitter"} {
		rec := e.post("/mcu-submit/lang", url.Values{"lang": {"en"}, "next": {next}})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/mcu-submit" {
			t.Errorf("next %q -> %d %q, want a redirect to /mcu-submit", next, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestTheSwitcherCarriesTheCurrentPageSoTheChoiceComesBack(t *testing.T) {
	e := newEnv(t)
	body := e.get("/mcu-submit/verify?token=abc").Body.String()
	if !strings.Contains(body, `name="next" value="/mcu-submit/verify?token=abc"`) {
		t.Fatalf("switcher does not carry the current page:\n%s", body)
	}
	hostile := e.get("/mcu-submit/signin?x=%22%3E%3Cscript%3E").Body.String()
	if strings.Contains(hostile, `"><script>`) {
		t.Fatal("the current address was written into the page without escaping")
	}
}
