package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"entry-permit/internal/reflist"
	"entry-permit/internal/staff"
)

var csrfRe = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

func (e *testEnv) post(t *testing.T, path string, cookies []*http.Cookie, v url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// adminSession creates an admin, signs in, and returns cookies plus the CSRF token of the page.
func (e *testEnv) adminSession(t *testing.T) ([]*http.Cookie, string) {
	t.Helper()
	e.addUser(t, "arif", "Arif", staff.Admin)
	cookies := e.login(t, "arif", "1234").Cookies()
	_, body := e.get(t, "/staff/admin/lists/nationality", cookies)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf token on the admin list page")
	}
	return cookies, m[1]
}

func TestAdminOverviewAndGuards(t *testing.T) {
	e := newEnv(t)
	cookies, _ := e.adminSession(t)
	_, body := e.get(t, "/staff/admin", cookies)
	for _, want := range []string{"Nationality", "Malaria endemicity", "Notification emails", "Audit log"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// Anonymous: sent to login. Non-admin: 403 on every admin page.
	for _, p := range []string{"/staff/admin", "/staff/admin/lists/nationality", "/staff/admin/audit"} {
		if r, _ := e.get(t, p, nil); r.StatusCode != 303 || r.Header.Get("Location") != "/login" {
			t.Errorf("anonymous %s: %d", p, r.StatusCode)
		}
	}
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	doc := e.login(t, "ayu", "1234").Cookies()
	for _, p := range []string{"/staff/admin", "/staff/admin/lists/nationality", "/staff/admin/audit"} {
		if r, _ := e.get(t, p, doc); r.StatusCode != 403 {
			t.Errorf("doctor on %s: %d", p, r.StatusCode)
		}
	}
	if r, _ := e.get(t, "/staff/admin/lists/nope", cookies); r.StatusCode != 404 {
		t.Errorf("unknown list: %d", r.StatusCode)
	}
}

func TestAdminAddEditArchiveRestoreAndAudit(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	list := "/staff/admin/lists/nationality"

	r := e.post(t, list, cookies, url.Values{"_csrf": {tok}, "v0": {"Indonesia"}})
	if r.StatusCode != 303 || !strings.Contains(r.Header.Get("Location"), "ok=Added") {
		t.Fatalf("add: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	_, body := e.get(t, list, cookies)
	if !strings.Contains(body, `value="Indonesia"`) {
		t.Fatal("added item not shown")
	}
	// Duplicate and invalid values come back as a readable error, not a crash.
	r = e.post(t, list, cookies, url.Values{"_csrf": {tok}, "v0": {"indonesia"}})
	if !strings.Contains(r.Header.Get("Location"), "err=") {
		t.Errorf("duplicate: %s", r.Header.Get("Location"))
	}
	_, body = e.get(t, r.Header.Get("Location"), cookies)
	if !strings.Contains(body, "already exists") {
		t.Error("duplicate message not shown")
	}
	r = e.post(t, list, cookies, url.Values{"_csrf": {tok}, "v0": {""}})
	if !strings.Contains(r.Header.Get("Location"), "err=") {
		t.Errorf("blank: %s", r.Header.Get("Location"))
	}

	item := list + "/1"
	steps := []struct {
		action, flash string
	}{{"save", "Saved."}, {"archive", "Archived."}, {"restore", "Restored."}}
	for _, s := range steps {
		r = e.post(t, item, cookies, url.Values{"_csrf": {tok}, "action": {s.action}, "v0": {"Indonesian"}})
		if r.StatusCode != 303 || !strings.Contains(r.Header.Get("Location"), "ok="+url.QueryEscape(s.flash)) {
			t.Errorf("%s: %d %s", s.action, r.StatusCode, r.Header.Get("Location"))
		}
	}
	if r = e.post(t, item, cookies, url.Values{"_csrf": {tok}, "action": {"explode"}}); r.StatusCode != 400 {
		t.Errorf("bad action: %d", r.StatusCode)
	}
	if r = e.post(t, list+"/999", cookies, url.Values{"_csrf": {tok}, "action": {"archive"}}); !strings.Contains(r.Header.Get("Location"), "err=") {
		t.Errorf("missing item: %s", r.Header.Get("Location"))
	}
	if r = e.post(t, list+"/abc", cookies, url.Values{"_csrf": {tok}, "action": {"save"}}); r.StatusCode != 404 {
		t.Errorf("bad id: %d", r.StatusCode)
	}

	// Archived items are hidden until asked for; search filters.
	e.post(t, item, cookies, url.Values{"_csrf": {tok}, "action": {"archive"}})
	if _, b := e.get(t, list, cookies); strings.Contains(b, `value="Indonesian"`) {
		t.Error("archived item shown by default")
	}
	if _, b := e.get(t, list+"?archived=1&q=indo", cookies); !strings.Contains(b, `value="Indonesian"`) || !strings.Contains(b, "Archived") {
		t.Error("archived search failed")
	}
	if _, b := e.get(t, list+"?archived=1&q=zzz", cookies); !strings.Contains(b, "No items yet") {
		t.Error("empty search should say so")
	}

	_, audit := e.get(t, "/staff/admin/audit", cookies)
	for _, want := range []string{"list.add", "list.update", "list.archive", "list.restore", "arif", "nationality#1"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit page lacks %q", want)
		}
	}
}

func TestAdminPostNeedsCSRFAndAdminRole(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	list := "/staff/admin/lists/nationality"
	for name, v := range map[string]url.Values{
		"missing token": {"v0": {"X"}},
		"wrong token":   {"_csrf": {"deadbeef"}, "v0": {"X"}},
	} {
		if r := e.post(t, list, cookies, v); r.StatusCode != 403 {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r := e.post(t, list+"/1", cookies, url.Values{"action": {"archive"}}); r.StatusCode != 403 {
		t.Errorf("item post without token: %d", r.StatusCode)
	}
	// A token from another session is not valid for this one.
	e.addUser(t, "romaryo", "Romaryo", staff.Admin)
	other := e.login(t, "romaryo", "1234").Cookies()
	if r := e.post(t, list, other, url.Values{"_csrf": {tok}, "v0": {"X"}}); r.StatusCode != 403 {
		t.Errorf("token reused across sessions: %d", r.StatusCode)
	}
	// A doctor cannot post even with a well-formed form.
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	doc := e.login(t, "ayu", "1234").Cookies()
	if r := e.post(t, list, doc, url.Values{"v0": {"X"}}); r.StatusCode != 403 {
		t.Errorf("doctor post: %d", r.StatusCode)
	}
	// Nothing was written by any of the refused requests.
	if _, b := e.get(t, list+"?archived=1", cookies); !strings.Contains(b, "No items yet") {
		t.Error("a refused request still wrote data")
	}
	// Anonymous POST goes to login.
	if r := e.post(t, list, nil, url.Values{"v0": {"X"}}); r.StatusCode != 303 {
		t.Errorf("anonymous post: %d", r.StatusCode)
	}
}

func TestAdminMultiColumnListAndEmailValidation(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	r := e.post(t, "/staff/admin/lists/role_email", cookies, url.Values{"_csrf": {tok}, "v0": {"doctor"}, "v1": {"Clinic@Example.com"}})
	if !strings.Contains(r.Header.Get("Location"), "ok=Added") {
		t.Fatalf("add email: %s", r.Header.Get("Location"))
	}
	if _, b := e.get(t, "/staff/admin/lists/role_email", cookies); !strings.Contains(b, `value="clinic@example.com"`) || !strings.Contains(b, "<select") {
		t.Error("email not stored lower-case or role dropdown missing")
	}
	r = e.post(t, "/staff/admin/lists/role_email", cookies, url.Values{"_csrf": {tok}, "v0": {"doctor"}, "v1": {"nope"}})
	if !strings.Contains(r.Header.Get("Location"), "err=") {
		t.Errorf("bad email: %s", r.Header.Get("Location"))
	}
	// A message in the query string is escaped, never run as markup.
	_, b := e.get(t, "/staff/admin/lists/nationality?err="+url.QueryEscape("<script>alert(1)</script>"), cookies)
	if strings.Contains(b, "<script>alert") {
		t.Error("flash message not escaped")
	}
	if _, b := e.get(t, "/staff/admin/lists/nationality?err="+strings.Repeat("a", 300), cookies); strings.Contains(b, strings.Repeat("a", 300)) {
		t.Error("oversized flash accepted")
	}
}

// ---- malaria page (regencies come from the medpost API) ----

func TestMalariaPageUsesAPIRegenciesAndAddsOnlyFromThem(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	page := "/staff/admin/malaria"

	// Nothing is listed yet: the default view says so and offers "show every regency".
	if _, b := e.get(t, page, cookies); !strings.Contains(b, "No regencies match") || !strings.Contains(b, "Show every regency") {
		t.Error("empty default view")
	}
	// All regencies come from the API, with a dropdown per row; free text is not offered.
	_, b := e.get(t, page+"?all=1", cookies)
	for _, want := range []string{"Kabupaten Aceh Selatan", "Kota Sorong", "Papua Barat Daya", "Not listed", "Tinggi"} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(b, `type="text" name="v0"`) {
		t.Error("malaria page must not offer free-text entry")
	}

	// Add a regency by choosing its level; the filter is kept in the redirect.
	r := e.post(t, page, cookies, url.Values{"_csrf": {tok}, "code": {"96.01"}, "endemicity": {"Tinggi"}, "f_province": {"Papua Barat Daya"}, "f_all": {"1"}})
	loc := r.Header.Get("Location")
	if r.StatusCode != 303 || !strings.Contains(loc, "ok=Saved") || !strings.Contains(loc, "province=Papua+Barat+Daya") || !strings.Contains(loc, "all=1") {
		t.Fatalf("set: %d %s", r.StatusCode, loc)
	}
	_, b = e.get(t, page, cookies) // default view now lists it
	if !strings.Contains(b, "Kabupaten Sorong") || strings.Contains(b, "Kabupaten Aceh Selatan") {
		t.Error("listed view wrong")
	}
	// Taking it off the list archives it; the audit log has both steps.
	e.post(t, page, cookies, url.Values{"_csrf": {tok}, "code": {"96.01"}, "endemicity": {""}})
	if _, b = e.get(t, page, cookies); strings.Contains(b, "Kabupaten Sorong") {
		t.Error("regency still listed after removal")
	}
	if _, a := e.get(t, "/staff/admin/audit", cookies); !strings.Contains(a, "list.add") || !strings.Contains(a, "list.archive") {
		t.Error("malaria changes not audited")
	}
	// Province filter and search.
	if _, b = e.get(t, page+"?all=1&province=Aceh", cookies); strings.Contains(b, "Kota Sorong") || !strings.Contains(b, "Aceh Selatan") {
		t.Error("province filter")
	}
	if _, b = e.get(t, page+"?all=1&q=kota", cookies); !strings.Contains(b, "Kota Sorong") || strings.Contains(b, "Aceh Selatan") {
		t.Error("text filter")
	}
}

func TestMalariaRefusesHandTypedRegionsAndBadLevels(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	page := "/staff/admin/malaria"
	for name, v := range map[string]url.Values{
		"unknown code": {"_csrf": {tok}, "code": {"99.99"}, "endemicity": {"Tinggi"}},
		"bad level":    {"_csrf": {tok}, "code": {"11.01"}, "endemicity": {"Parah"}},
	} {
		if r := e.post(t, page, cookies, v); !strings.Contains(r.Header.Get("Location"), "err=") {
			t.Errorf("%s: %s", name, r.Header.Get("Location"))
		}
	}
	if r := e.post(t, page, cookies, url.Values{"code": {"11.01"}, "endemicity": {"Tinggi"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	// The generic free-text editor is switched off for this list.
	if r, _ := e.get(t, "/staff/admin/lists/malaria", cookies); r.StatusCode != 303 || r.Header.Get("Location") != page {
		t.Errorf("generic page: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := e.post(t, "/staff/admin/lists/malaria", cookies, url.Values{"_csrf": {tok}, "v0": {"Tinggi"}, "v1": {"X"}, "v2": {"Y"}}); r.StatusCode != 404 {
		t.Errorf("generic add: %d", r.StatusCode)
	}
	if r := e.post(t, "/staff/admin/lists/malaria/1", cookies, url.Values{"_csrf": {tok}, "action": {"archive"}}); r.StatusCode != 404 {
		t.Errorf("generic item: %d", r.StatusCode)
	}
	// Doctors cannot open it; the overview links to the malaria page.
	e.addUser(t, "ayu", "Ayu", staff.Doctor)
	if r, _ := e.get(t, page, e.login(t, "ayu", "1234").Cookies()); r.StatusCode != 403 {
		t.Errorf("doctor: %d", r.StatusCode)
	}
	if _, b := e.get(t, "/staff/admin", cookies); !strings.Contains(b, `href="/staff/admin/malaria"`) {
		t.Error("overview does not link the malaria page")
	}
}

func TestMalariaPageWhenRegionListIsUnavailable(t *testing.T) {
	e := newEnvWith(t, nil)
	cookies, tok := e.adminSession(t)
	if _, b := e.get(t, "/staff/admin/malaria", cookies); !strings.Contains(b, "not available") || strings.Contains(b, "<tbody>") {
		t.Error("unavailable message missing")
	}
	r := e.post(t, "/staff/admin/malaria", cookies, url.Values{"_csrf": {tok}, "code": {"11.01"}, "endemicity": {"Tinggi"}})
	if !strings.Contains(r.Header.Get("Location"), "err=") {
		t.Errorf("save without source: %s", r.Header.Get("Location"))
	}
}

func TestMalariaOrphansAreShownRelinkedAndArchived(t *testing.T) {
	e := newEnv(t)
	cookies, tok := e.adminSession(t)
	page := "/staff/admin/malaria"
	// Two stored items that match nothing in the region list (as after a medpost change).
	def, _ := reflist.Find("malaria")
	lists := reflist.NewStore(e.db, e.clock)
	for _, name := range []string{"Kabupaten Hilang", "Kabupaten Lama"} {
		if _, err := lists.Add(context.Background(), "seed", def, []string{"Tinggi", name, "Aceh", ""}); err != nil {
			t.Fatal(err)
		}
	}
	_, ov := e.get(t, "/staff/admin", cookies)
	if !strings.Contains(ov, "2 to review") {
		t.Error("overview lacks the orphan count")
	}
	_, b := e.get(t, page, cookies)
	for _, want := range []string{"No longer in the region list", "Kabupaten Hilang", "Kabupaten Lama", "/static/combobox.js", `value="relink"`} {
		if !strings.Contains(b, want) {
			t.Errorf("orphan section lacks %q", want)
		}
	}
	items, _ := lists.Items(context.Background(), def, false)
	idHilang, idLama := items[0].ID, items[1].ID
	post := func(id int64, v url.Values) string {
		v.Set("_csrf", tok)
		return e.post(t, page+"/orphan/"+strconv.FormatInt(id, 10), cookies, v).Header.Get("Location")
	}
	if loc := post(idHilang, url.Values{"action": {"relink"}, "code": {"11.01"}}); !strings.Contains(loc, "ok=") {
		t.Errorf("relink: %s", loc)
	}
	if loc := post(idLama, url.Values{"action": {"archive"}}); !strings.Contains(loc, "ok=Archived") {
		t.Errorf("archive: %s", loc)
	}
	// Already handled, so a second try is refused with a message.
	if loc := post(idLama, url.Values{"action": {"archive"}}); !strings.Contains(loc, "err=") {
		t.Errorf("archive twice: %s", loc)
	}
	if loc := post(idHilang, url.Values{"action": {"relink"}, "code": {"99.99"}}); !strings.Contains(loc, "err=") {
		t.Errorf("relink non-orphan: %s", loc)
	}
	if r := e.post(t, page+"/orphan/x", cookies, url.Values{"_csrf": {tok}, "action": {"archive"}}); r.StatusCode != 404 {
		t.Errorf("bad id: %d", r.StatusCode)
	}
	if r := e.post(t, page+"/orphan/1", cookies, url.Values{"_csrf": {tok}, "action": {"zap"}}); r.StatusCode != 400 {
		t.Errorf("bad action: %d", r.StatusCode)
	}
	if r := e.post(t, page+"/orphan/1", cookies, url.Values{"action": {"archive"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	_, b = e.get(t, page+"?all=1&q=aceh", cookies)
	if strings.Contains(b, "No longer in the region list") || !strings.Contains(b, "Aceh Selatan") {
		t.Error("orphan section should be gone after handling both")
	}
	if _, ov = e.get(t, "/staff/admin", cookies); strings.Contains(ov, "to review") {
		t.Error("overview still shows a count")
	}
}
