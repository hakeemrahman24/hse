package main

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readSource returns the text of the files matching a glob.
func readSource(t *testing.T, glob string, skip func(name string) bool) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(glob)
	if err != nil || len(paths) == 0 {
		t.Fatalf("no files for %s: %v", glob, err)
	}
	out := map[string]string{}
	for _, p := range paths {
		if skip != nil && skip(p) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = string(b)
	}
	return out
}

func unescapeGo(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n").Replace(s)
}

// Every text a SOC page or answer can show must have an Indonesian and a Chinese entry, so a new
// sentence cannot ship half translated. The texts are collected from the templates, from the tr
// calls in the Go code, and from the lists whose values are shown through tr.
func TestEverySOCTextIsTranslated(t *testing.T) {
	keys := map[string]string{} // text -> where it was found

	tmpl := regexp.MustCompile(`\b(?:tr|heading) \$?\.Lang "((?:[^"\\]|\\.)*)"`)
	for name, src := range readSource(t, "../web/templates/soc_*.html", nil) {
		for _, m := range tmpl.FindAllStringSubmatch(src, -1) {
			keys[unescapeGo(m[1])] = name
		}
	}
	goCall := regexp.MustCompile(`\btrf?\((?:langOf\(r\)|lang), "((?:[^"\\]|\\.)*)"`)
	for name, src := range readSource(t, "soc_*.go", func(n string) bool { return strings.HasSuffix(n, "_test.go") || strings.HasSuffix(n, "soc_i18n.go") }) {
		for _, m := range goCall.FindAllStringSubmatch(src, -1) {
			keys[unescapeGo(m[1])] = name
		}
	}
	// Messages written by the validation and shown through translateErrors.
	errText := regexp.MustCompile(`errs\["[a-z_]+"\] = "((?:[^"\\]|\\.)*)"`)
	textCall := regexp.MustCompile(`text\("[a-z_]+", [A-Za-z.]+, "((?:[^"\\]|\\.)*)", (?:true|false)\)`)
	for _, f := range []string{"soc_validate.go", "observation_rules.go", "soc_wizard.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range textCall.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "" {
				keys[m[1]] = f // the messages given to the wizard's text() helper
			}
		}
		for _, m := range errText.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(f, "observation_rules") && m[1] != "Choose a topic from the list." && m[1] != "Choose a hazard from the list." {
				continue // the staff messages; only these two are shared with the SOC form
			}
			keys[m[1]] = f
		}
	}
	keys[msgNotFound] = "msgNotFound"
	keys["Maximum %d characters."] = "validation"
	for _, c := range socCategories {
		keys[c.Label] = "socCategories"
	}
	for _, s := range []string{"Closed and verified", "Closed, waiting for HSE verification", "In progress", "Verified", "Received", "Hazard", "Control", "None", "Other"} {
		keys[s] = "stage / group names"
	}
	for _, c := range controlClassOrder {
		keys[c] = "controlClassOrder"
	}
	f, err := os.Open("../migrations/seed/ref_hazard_seed.csv")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows[1:] {
		keys[r[0]] = "hazard classes in the seed"
	}

	if len(keys) < 80 {
		t.Fatalf("the scan found only %d texts: is a pattern out of date?", len(keys))
	}
	for en, where := range keys {
		entry, ok := socText[en]
		switch {
		case !ok:
			t.Errorf("no translation for %q (%s)", en, where)
		case entry.id == "":
			t.Errorf("no Indonesian for %q (%s)", en, where)
		}
	}
}

func TestTranslationsKeepTheirPlaceholders(t *testing.T) {
	for en, tr := range socText {
		if strings.Count(en, "%d") != strings.Count(tr.id, "%d") {
			t.Errorf("%q and %q must have the same number of %%d", en, tr.id)
		}
	}
}

func TestLanguageIsChosenFromTheCookieThenTheBrowser(t *testing.T) {
	req := func(cookie, accept string) *http.Request {
		r := httptest.NewRequest("GET", "/soc", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieSOCLang, Value: cookie})
		}
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		return r
	}
	for _, c := range []struct{ cookie, accept, want string }{
		{"", "", langID},
		{"en", "", langEN},
		{"bogus", "", langID},
		{"", "zh-CN,zh;q=0.9", langID}, // not offered: the default
		{"zh", "", langID},             // an old cookie of a language that is gone is ignored
		{"", "en-US,en;q=0.9", langEN},
		{"", "id-ID,id;q=0.9", langID},
		{"", "ms-MY", langID},
		{"", "fr-FR,fr", langID},
		{"id", "en-US", langID}, // the visitor's own choice beats the browser's
	} {
		if got := langOf(req(c.cookie, c.accept)); got != c.want {
			t.Errorf("cookie %q, Accept-Language %q: got %s, want %s", c.cookie, c.accept, got, c.want)
		}
	}
}

func TestLanguageLinkSetsTheCookieAndOnlyReturnsToSOCPages(t *testing.T) {
	c, _, _ := newSOCTest(t)
	rec := c.do("GET", "/soc/lang?l=en&next=/soc/form", "", "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/soc/form" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == cookieSOCLang {
			cookie = ck
		}
	}
	if cookie == nil || cookie.Value != langEN || cookie.Path != "/soc" || !cookie.HttpOnly || cookie.MaxAge <= 0 {
		t.Fatalf("language cookie wrong: %+v", cookie)
	}
	for _, next := range []string{"//evil.example", "https://evil.example", "/staff", "/soc/../admin", "/soc\\evil", "", "javascript:alert(1)"} {
		rec := c.do("GET", "/soc/lang?l=id&next="+next, "", "")
		if loc := rec.Header().Get("Location"); loc != "/soc" {
			t.Errorf("next=%q must fall back to /soc, got %q", next, loc)
		}
	}
	if rec := c.do("GET", "/soc/lang?l=klingon&next=/soc", "", ""); len(rec.Result().Cookies()) != 0 {
		t.Error("an unknown language must not set a cookie")
	}
}

func TestSigninPageShowsOneLanguageAtATime(t *testing.T) {
	c, _, _ := newSOCTest(t)
	for _, w := range []struct {
		lang string
		want []string
		not  []string
	}{
		{langID, []string{`<html lang="id"`, "Observasi Keselamatan", "Tanggal lahir", `data-lang="id"`}, []string{"Date of birth"}},
		{langEN, []string{`<html lang="en"`, "Safety Observation", "Date of birth"}, []string{"Tanggal lahir"}},
	} {
		c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: w.lang}
		body := c.do("GET", "/soc", "", "").Body.String()
		for _, s := range w.want {
			if !strings.Contains(body, s) {
				t.Errorf("%s: sign-in page is missing %q", w.lang, s)
			}
		}
		for _, s := range w.not {
			if strings.Contains(body, s) {
				t.Errorf("%s: sign-in page must not show %q (one language at a time)", w.lang, s)
			}
		}
		// The switcher offers all three, marks the current one, and returns to this page.
		for _, s := range []string{`href="/soc/lang?l=id&amp;next=%2fsoc"`, `href="/soc/lang?l=en&amp;next=%2fsoc"`, ">ID<", ">EN<"} {
			if !strings.Contains(body, s) {
				t.Errorf("%s: switcher is missing %q", w.lang, s)
			}
		}
		if strings.Count(body, `aria-current="true"`) != 1 {
			t.Errorf("%s: exactly one language must be marked current", w.lang)
		}
	}
}

func TestFormPageAndErrorsFollowTheLanguage(t *testing.T) {
	c, _ := signedIn(t)
	c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: langID}
	body := c.do("GET", "/soc/form", "", "").Body.String()
	for _, s := range []string{"Temuan <span>Observasi</span>", "Tanggal temuan", "Tindakan Tidak Aman", "Setelah perbaikan (opsional)", "APD", "Tutup temuan ini sekarang", `data-none="Tidak ada"`, "Pilih dari daftar", "Lainnya / tidak ada yang cocok", "Ketuk topik yang cocok"} {
		if !strings.Contains(body, s) {
			t.Errorf("Indonesian form is missing %q", s)
		}
	}
	if strings.Contains(body, "Date of finding") || strings.Contains(body, "Unsafe Action") {
		t.Error("the Indonesian form must not show the English labels")
	}
	// Errors are shown in the same language.
	post := "date=2999-01-01&category=unsafe_condition&area=Camp"
	rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", post)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Tanggal tidak boleh di masa depan.") || !strings.Contains(rec.Body.String(), "Jelaskan temuan Anda.") {
		t.Fatalf("errors must be in Indonesian: %d", rec.Code)
	}
	c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: langEN}
	rec = c.do("POST", "/soc/form", "application/x-www-form-urlencoded", post)
	if !strings.Contains(rec.Body.String(), "The date cannot be in the future.") || !strings.Contains(rec.Body.String(), "Describe your finding.") {
		t.Fatal("errors must be in English")
	}
	if body := c.do("GET", "/soc/form", "", "").Body.String(); strings.Contains(body, "Tanggal temuan") || !strings.Contains(body, "Date of finding") {
		t.Error("the English form must show only English")
	}
}

func TestServerAnswersToTheSigninScriptFollowTheLanguage(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: langID}
	rec := c.json("GET", "/soc/api/names?q=budi", "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Silakan selesaikan pemeriksaan") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	c.passCheck()
	rec = c.json("POST", "/soc/api/signin", `{"ref":"nope","dob":"1990-05-14"}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Data tidak ditemukan") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: langEN}
	if rec := c.json("POST", "/soc/api/signin", `{"ref":"nope","dob":"1990-05-14"}`); !strings.Contains(rec.Body.String(), msgNotFound) {
		t.Fatalf("English answer: %s", rec.Body)
	}
}

func TestPhotoErrorsAreTranslatedAndNumbersKept(t *testing.T) {
	got := photoMessage(langID, checkPhotoCount(2, 3))
	if !strings.Contains(got, "Foto tidak dapat diterima") || !strings.Contains(got, "maksimal 4 foto") || !strings.Contains(got, "Maks. 4 foto") {
		t.Fatalf("Indonesian message: %s", got)
	}
	if got := photoMessage(langEN, checkPhotoCount(2, 3)); !strings.Contains(got, "Photo not accepted: a post can have at most 4 photos") {
		t.Fatalf("English message: %s", got)
	}
}

func TestHeadingKeepsTheAccentOnTheLastWordAndEscapes(t *testing.T) {
	if got := string(headingHTML(langEN, "Safety Observation")); got != "Safety <span>Observation</span>" {
		t.Fatal(got)
	}
	if got := string(headingHTML(langID, "Safety Observation")); got != "Observasi <span>Keselamatan</span>" {
		t.Fatal(got)
	}
	if got := string(headingHTML(langEN, "A <b> & C")); strings.Contains(got, "<b>") {
		t.Fatalf("not escaped: %s", got)
	}
}

// A template that fails half way only logs the error and sends a cut-off page. Every SOC page must
// come out complete, in every language, including the ones that loop over photos.
func TestEverySOCPageRendersCompletelyInEveryLanguage(t *testing.T) {
	c, h, _ := signedInWithHandlers(t)
	small := encodeJPEG(t, noisyImage(120, 90), 80)
	if rec := postWithPhotos(c, map[string][]byte{"a.jpg": small}); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup: %d", rec.Code)
	}
	_ = h
	for _, l := range socLangs {
		c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: l.Code}
		for _, path := range []string{"/soc", "/soc/form", "/soc/mine", "/soc/mine?saved=SOC-000001", "/soc/after/1", "/soc/edit/1", "/soc/thanks?ref=SOC-000001"} {
			rec := c.do("GET", path, "", "")
			body := rec.Body.String()
			if rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
				t.Errorf("%s %s: got %d", l.Code, path, rec.Code)
			}
			if rec.Code == http.StatusOK && !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
				t.Errorf("%s %s: the page is cut off (a template error?)", l.Code, path)
			}
		}
	}
}

// The date of birth is typed as dd/mm/yyyy (with a calendar button for the browser's date picker),
// not in the browser's own order, which is mm/dd/yyyy on many phones. The server still gets an ISO date.
func TestSigninDateOfBirthIsDayMonthYearWithAPicker(t *testing.T) {
	c, _, _ := newSOCTest(t)
	for _, l := range socLangs {
		c.cookies[cookieSOCLang] = &http.Cookie{Name: cookieSOCLang, Value: l.Code}
		body := c.do("GET", "/soc", "", "").Body.String()
		for _, want := range []string{`id="dob-input" type="text" inputmode="numeric"`, `placeholder="dd/mm/yyyy"`, `maxlength="10"`, `id="dob-pick"`, `id="dob-native" class="dob-native" type="date"`, `novalidate`, `data-msg-bad-date=`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: sign-in page is missing %q", l.Code, want)
			}
		}
		if strings.Contains(body, `id="dob-input" type="date"`) {
			t.Errorf("%s: the visible field must not be the browser's own date input", l.Code)
		}
	}
}
