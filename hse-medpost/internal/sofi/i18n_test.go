package sofi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLanguageDefaultsToIndonesianAndFollowsTheCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/sofi", nil)
	if got := langOf(req); got != LangID {
		t.Fatalf("default = %q", got)
	}
	req.AddCookie(&http.Cookie{Name: langCookie, Value: LangEN})
	if got := langOf(req); got != LangEN {
		t.Fatalf("cookie en = %q", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/sofi", nil)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: "fr"})
	if got := langOf(req); got != LangID {
		t.Fatalf("unknown language = %q", got)
	}
}

func TestSetLangStoresTheChoiceAndOnlyGoesToSofiPages(t *testing.T) {
	api := newTestAPI(t)
	cases := []struct{ query, location string }{
		{"l=en&next=/sofi/start", "/sofi/start"},
		{"l=id&next=/sofi", "/sofi"},
		{"l=en&next=https://evil.example/", "/sofi"},
		{"l=en&next=//evil.example", "/sofi"},
		{"l=en&next=/emr", "/sofi"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		api.SetLang(rec, httptest.NewRequest(http.MethodGet, "/sofi/lang?"+c.query, nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != c.location {
			t.Fatalf("%s: %d -> %q, want %q", c.query, rec.Code, rec.Header().Get("Location"), c.location)
		}
	}
	rec := httptest.NewRecorder()
	api.SetLang(rec, httptest.NewRequest(http.MethodGet, "/sofi/lang?l=en&next=/sofi", nil))
	var got string
	for _, c := range rec.Result().Cookies() {
		if c.Name == langCookie {
			got = c.Value
		}
	}
	if got != LangEN {
		t.Fatalf("cookie = %q", got)
	}
	rec = httptest.NewRecorder()
	api.SetLang(rec, httptest.NewRequest(http.MethodGet, "/sofi/lang?l=xx&next=/sofi", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == langCookie {
			t.Fatalf("an unknown language set a cookie: %q", c.Value)
		}
	}
}

func TestEveryPublicMessageHasAnIndonesianText(t *testing.T) {
	for _, msg := range publicMessages {
		if id := tr(LangID, msg); id == msg || strings.TrimSpace(id) == "" {
			t.Fatalf("no Indonesian text for %q", msg)
		}
		if tr(LangEN, msg) != msg {
			t.Fatalf("English text changed for %q", msg)
		}
	}
}

func TestItemTextFollowsTheLanguage(t *testing.T) {
	first := Items[0]
	if first.Text(LangEN) != "Overworked" {
		t.Fatalf("en = %q", first.Text(LangEN))
	}
	if first.Text(LangID) != "Terlalu banyak aktivitas yang dikerjakan" {
		t.Fatalf("id = %q", first.Text(LangID))
	}
}

func TestPagesShowOnlyTheChosenLanguage(t *testing.T) {
	api := newTestAPI(t)
	for lang, want := range map[string]string{LangEN: "Your name", LangID: "Nama Anda"} {
		req := httptest.NewRequest(http.MethodGet, "/sofi", nil)
		req.AddCookie(&http.Cookie{Name: langCookie, Value: lang})
		rec := httptest.NewRecorder()
		api.SignInPage(rec, req)
		body := rec.Body.String()
		other := map[string]string{LangEN: "Nama Anda", LangID: "Your name"}[lang]
		if !strings.Contains(body, want) || strings.Contains(body, other) {
			t.Fatalf("%s page: has %q = %v, has %q = %v", lang, want, strings.Contains(body, want), other, strings.Contains(body, other))
		}
	}
}

func TestSignedInFormShowsTheHeaderFieldsAndTwentyItemsInOneLanguage(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(c)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: LangEN})
	rec := httptest.NewRecorder()
	api.Start(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"Andi Pratama", "14/05/1990", "Weeks on site", "MCU review status", "Overworked", "Yawning"} {
		if !strings.Contains(body, want) {
			t.Fatalf("form lacks %q", want)
		}
	}
	if strings.Contains(body, "Terlalu banyak aktivitas") {
		t.Fatal("English form shows the Indonesian words")
	}
	if n := strings.Count(body, `name="q`); n != ItemCount*7 {
		t.Fatalf("%d answer buttons, want %d (20 items x 7 points)", n, ItemCount*7)
	}
}

func TestEveryPublicMessageAndWordHasAChineseText(t *testing.T) {
	for _, msg := range publicMessages {
		if zh := tr(LangZH, msg); zh == msg || strings.TrimSpace(zh) == "" {
			t.Fatalf("no Chinese text for %q", msg)
		}
	}
	for _, it := range Items {
		if strings.TrimSpace(it.Text(LangZH)) == "" {
			t.Fatalf("word %d has no Chinese text", it.Number)
		}
	}
	for _, d := range Dimensions {
		if strings.TrimSpace(d.Name(LangZH)) == "" {
			t.Fatalf("dimension %s has no Chinese name", d.Key)
		}
	}
	for key := range zhMessages {
		if _, ok := idText[key]; !ok {
			t.Fatalf("Chinese text for %q, which is not a public message", key)
		}
	}
}

func TestChineseCanBeChosenAndShowsOnlyChinese(t *testing.T) {
	api := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.SetLang(rec, httptest.NewRequest(http.MethodGet, "/sofi/lang?l=zh&next=/sofi/start", nil))
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == langCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != LangZH {
		t.Fatalf("cookie = %v", cookie)
	}

	c := signedInCookie(t, api)
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(c)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	api.Start(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`lang="zh"`, "疲劳筛查", "工作过度", "频繁打哈欠", "中文"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Chinese form lacks %q", want)
		}
	}
	for _, other := range []string{"Overworked", "Kerja berlebihan", "Weeks on site"} {
		if strings.Contains(body, other) {
			t.Fatalf("Chinese form still shows %q", other)
		}
	}
}

func TestSubmitButtonStartsDisabledUntilEverythingIsAnswered(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	rec := get(api.Start, "/sofi/start", c)
	body := rec.Body.String()
	if !strings.Contains(body, `class="btn-primary" disabled>`) || !strings.Contains(body, "0 / 20") {
		t.Fatal("the Submit button should start disabled with a 0 / 20 counter")
	}
}
