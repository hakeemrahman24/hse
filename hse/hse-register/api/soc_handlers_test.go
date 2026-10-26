package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// memorySink keeps submissions in memory for the tests, so no database is needed.
type memorySink struct {
	mu       sync.Mutex
	next     int64
	rows     []socSubmission
	photos   [][]photoRecord
	verified map[int]bool // row index -> verified by HSE
}

func (m *memorySink) verification(i int) string {
	if m.verified[i] {
		return verifVerified
	}
	return verifNotYet
}

func (m *memorySink) GetOwn(ctx context.Context, id int64, employeeID string) (*socPost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := int(id) - 1
	if i < 0 || i >= len(m.rows) || m.rows[i].EmployeeID != employeeID {
		return nil, nil
	}
	r := m.rows[i]
	ids, _ := m.photoIDs()
	post := &socPost{ID: id, Date: r.FindingDate, Category: r.Category, Description: r.Description, TopicID: r.TopicID, HazardID: r.HazardID,
		Area: r.Area, ResponsibleCompany: r.ResponsibleCompany, Status: r.Status, Verification: m.verification(i), ActionTaken: r.ActionTaken}
	for n, p := range m.photos[i] {
		if p.Kind == "after" {
			post.AfterIDs = append(post.AfterIDs, ids[i][n])
		} else {
			post.PhotoIDs = append(post.PhotoIDs, ids[i][n])
		}
	}
	return post, nil
}

func (m *memorySink) AddAfterOwn(ctx context.Context, id int64, employeeID string, add []photoRecord, remarks *string, closeIt bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := int(id) - 1
	if i < 0 || i >= len(m.rows) || m.rows[i].EmployeeID != employeeID || !socOwnerCanAddAfter(m.rows[i].Status) {
		return errNotEditable
	}
	have := 0
	for _, p := range m.photos[i] {
		if p.Kind == "after" {
			have++
		}
	}
	if err := checkPhotoCount(have, len(add)); err != nil {
		return err
	}
	if closeIt && closeBlocked(m.rows[i].Category, statusClosed, have+len(add)) {
		return errNeedsAfter
	}
	m.photos[i] = append(m.photos[i], add...)
	row := m.rows[i]
	if remarks != nil {
		row.ActionTaken = *remarks
	}
	if closeIt {
		row.Status = statusClosed
	}
	m.rows[i] = row
	return nil
}

func (m *memorySink) UpdateOwn(ctx context.Context, id int64, employeeID string, sub socSubmission, remove []int64, add []photoRecord) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := int(id) - 1
	if i < 0 || i >= len(m.rows) || m.rows[i].EmployeeID != employeeID || !socOwnerCanEdit(m.rows[i].Status, m.verification(i)) {
		return nil, errNotEditable
	}
	ids, byID := m.photoIDs()
	drop := map[string]bool{}
	var removed []string
	for _, pid := range remove {
		if p, ok := byID[pid]; ok && p.row == i {
			drop[p.name] = true
			removed = append(removed, p.name)
		}
	}
	var kept []photoRecord
	for _, p := range m.photos[i] {
		if !drop[p.StoredName] {
			kept = append(kept, p)
		}
	}
	_ = ids
	if err := checkPhotoCount(len(kept), len(add)); err != nil {
		return nil, err
	}
	m.photos[i] = append(kept, add...)
	row := m.rows[i]
	row.FindingDate, row.Category, row.Description, row.TopicID, row.HazardID = sub.FindingDate, sub.Category, sub.Description, sub.TopicID, sub.HazardID
	row.Area, row.ResponsibleCompany = sub.Area, sub.ResponsibleCompany
	m.rows[i] = row
	return removed, nil
}

// photoID gives each photo a stable id: 1, 2, 3... in the order they were saved.
func (m *memorySink) photoIDs() (ids [][]int64, byID map[int64]struct {
	row  int
	name string
}) {
	byID = map[int64]struct {
		row  int
		name string
	}{}
	var n int64
	for i, ps := range m.photos {
		var rowIDs []int64
		for _, p := range ps {
			n++
			rowIDs = append(rowIDs, n)
			byID[n] = struct {
				row  int
				name string
			}{i, p.StoredName}
		}
		ids = append(ids, rowIDs)
	}
	return ids, byID
}

func (m *memorySink) OwnPhoto(ctx context.Context, photoID int64, employeeID string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, byID := m.photoIDs()
	p, ok := byID[photoID]
	if !ok || m.rows[p.row].EmployeeID != employeeID {
		return "", false, nil
	}
	return p.name, true, nil
}

func (m *memorySink) Save(ctx context.Context, s socSubmission, photos []photoRecord) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	m.rows = append(m.rows, s)
	m.photos = append(m.photos, photos)
	return m.next, nil
}

func (m *memorySink) ListByEmployee(ctx context.Context, employeeID string) ([]socListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []socListItem
	ids, _ := m.photoIDs()
	for i, r := range m.rows {
		if r.EmployeeID == employeeID {
			out = append(out, socListItem{ID: int64(i + 1), Ref: socRef(int64(i + 1)), Date: r.FindingDate, Category: r.Category, Status: r.Status,
				Verification: m.verification(i), Photos: ids[i], Editable: socOwnerCanEdit(r.Status, m.verification(i)), CanClose: socOwnerCanAddAfter(r.Status)})
		}
	}
	return out, nil
}

type socClient struct {
	t       *testing.T
	mux     *http.ServeMux
	cookies map[string]*http.Cookie
}

func newSOCTest(t *testing.T) (*socClient, *socHandlers, *memorySink) {
	t.Helper()
	loadSOCPages("../web/templates")
	sink := &memorySink{}
	h := newSOCHandlers(NewSessionStore("test-secret"), devTurnstile{}, newDevDirectory(), sink)
	h.devStubs = true
	h.evidenceDir = filepath.Join(t.TempDir(), "evidence")
	h.ref = newStaticRef(testSnap())
	h.companies = newCompanyCache(devCompanies{})
	// The older tests read English text, so the client chooses English; the language tests choose their own.
	return &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{cookieSOCLang: {Name: cookieSOCLang, Value: langEN}}}, h, sink
}

func (c *socClient) do(method, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	return rec
}

func (c *socClient) json(method, path, body string) *httptest.ResponseRecorder {
	return c.do(method, path, "application/json", body)
}

func (c *socClient) passCheck() {
	c.t.Helper()
	if rec := c.json("POST", "/soc/api/check", `{"token":"dev-pass"}`); rec.Code != 200 {
		c.t.Fatalf("check failed: %d %s", rec.Code, rec.Body)
	}
}

type nameEntry struct{ Ref, Label, Company string }

func (c *socClient) search(q string) []nameEntry {
	c.t.Helper()
	rec := c.json("GET", "/soc/api/names?q="+url.QueryEscape(q), "")
	if rec.Code != 200 {
		c.t.Fatalf("search %q: %d %s", q, rec.Code, rec.Body)
	}
	var out []nameEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		c.t.Fatal(err)
	}
	return out
}

func (c *socClient) signIn(ref, dob string) *httptest.ResponseRecorder {
	return c.json("POST", "/soc/api/signin", `{"ref":"`+ref+`","dob":"`+dob+`"}`)
}

// signedIn returns a client signed in as Budi Santoso (E001).
func signedIn(t *testing.T) (*socClient, *memorySink) {
	c, _, sink := newSOCTest(t)
	c.passCheck()
	rec := c.signIn(c.search("budi")[0].Ref, "1990-05-14")
	if rec.Code != 200 {
		t.Fatalf("sign-in failed: %d %s", rec.Code, rec.Body)
	}
	return c, sink
}

func TestNamesNeedTheCheckFirst(t *testing.T) {
	c, _, _ := newSOCTest(t)
	if rec := c.json("GET", "/soc/api/names?q=budi", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 before the check", rec.Code)
	}
	if rec := c.json("POST", "/soc/api/check", `{"token":"wrong"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad token: got %d, want 400", rec.Code)
	}
	if rec := c.json("GET", "/soc/api/names?q=budi", ""); rec.Code != http.StatusForbidden {
		t.Fatal("a failed check must not open the search")
	}
}

func TestNameSearchRules(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.passCheck()
	if got := c.search("bu"); len(got) != 0 {
		t.Fatalf("2 letters must return nothing, got %v", got)
	}
	rec := c.json("GET", "/soc/api/names?q=bud", "")
	body := rec.Body.String()
	for _, secret := range []string{"E001", "Operator", "1990", "dob"} {
		if strings.Contains(body, secret) {
			t.Errorf("search result leaks %q: %s", secret, body)
		}
	}
	var got []nameEntry
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0].Label != "Budi Santoso" || got[0].Company != "SALAWATI MOTORINDO" || got[0].Ref == "" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestEveryNameShowsItsCompany(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.passCheck()
	got := c.search("andi")
	if len(got) != 2 {
		t.Fatalf("want 2 Andi Wijaya entries, got %v", got)
	}
	if got[0].Company == got[1].Company || got[0].Company == "" {
		t.Fatalf("every entry shows its company so identical names can be told apart: %v", got)
	}
}

func TestSigninWrongThenRightDOB(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.passCheck()
	ref := c.search("budi")[0].Ref
	rec := c.signIn(ref, "1990-05-15")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), msgNotFound) {
		t.Fatalf("wrong DOB: got %d %s", rec.Code, rec.Body)
	}
	if _, ok := c.cookies[cookieSOCSession]; ok {
		t.Fatal("no session on a wrong date of birth")
	}
	rec = c.signIn(ref, "1990-05-14")
	if rec.Code != 200 {
		t.Fatalf("right DOB: got %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "1990") {
		t.Error("the date of birth must never be echoed back")
	}
	if _, ok := c.cookies[cookieSOCSession]; !ok {
		t.Fatal("expected a SOC session cookie")
	}
}

func TestFiveFailuresLockTheEmployee(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.passCheck()
	ref := c.search("budi")[0].Ref
	for i := 0; i < socFailMax; i++ {
		c.signIn(ref, "1999-01-01")
	}
	rec := c.signIn(ref, "1990-05-14") // now correct, but locked
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), msgNotFound) {
		t.Fatalf("locked sign-in must look like any other failure, got %d %s", rec.Code, rec.Body)
	}
}

func TestReferenceBelongsToOneVisitor(t *testing.T) {
	a, h, _ := newSOCTest(t)
	a.passCheck()
	ref := a.search("budi")[0].Ref
	b := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	b.passCheck() // a different visitor with their own check cookie
	if rec := b.signIn(ref, "1990-05-14"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a reference from another visitor must not work, got %d", rec.Code)
	}
}

func TestSigninNeedsTheCheck(t *testing.T) {
	c, _, _ := newSOCTest(t)
	if rec := c.signIn("x", "1990-05-14"); rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 without the check", rec.Code)
	}
}

func TestFormNeedsSignin(t *testing.T) {
	c, _, _ := newSOCTest(t)
	for _, path := range []string{"/soc/form", "/soc/thanks?ref=SOC-000001", "/soc/mine"} {
		rec := c.do("GET", path, "", "")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/soc" {
			t.Errorf("%s: got %d %q, want redirect to /soc", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", "description=x"); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST without session: got %d", rec.Code)
	}
}

func TestFormPrefillsFromSession(t *testing.T) {
	c, _ := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	for _, want := range []string{"Budi Santoso", "Operator", "SALAWATI MOTORINDO", "Sign out", "Working at Height", `<option value="Camp">`, `/soc/api/suggest`, `/soc/static/combo.js?v=`, `data-combo-strict`} {
		if !strings.Contains(body, want) {
			t.Errorf("form page is missing %q", want)
		}
	}
}

// The form uses the staff template layout, and the reporter's name, position and company are
// read-only and carry no name attribute, so nothing typed there can reach the server.
func TestFormUsesStaffLayoutWithReadOnlyReporter(t *testing.T) {
	c, _ := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	for _, want := range []string{`class="add-activity"`, `class="section-label"`, `class="settings-grid"`, `id="reporter_name" value="Budi Santoso" readonly`, `id="reporter_position" value="Operator" readonly`, `id="reporter_company" value="SALAWATI MOTORINDO" readonly`} {
		if !strings.Contains(body, want) {
			t.Errorf("form page is missing %q", want)
		}
	}
	for _, bad := range []string{`name="observer_name"`, `name="employee_id"`, `name="designation"`, `name="company"`} {
		if strings.Contains(body, bad) {
			t.Errorf("form page must not post %s", bad)
		}
	}
}

// The reporter picks a Topic; the Hazard class is shown read-only and follows the topic, and the
// hazard category travels in a hidden field. There is no second dropdown that looks like Topic.
func TestSOCFormShowsTopicAndHazardClassNotAHazardDropdown(t *testing.T) {
	c, _ := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	for _, want := range []string{`id="hazard_class"`, `readonly`, `data-class="Safety"`, `data-hazard="1"`, `type="hidden" name="hazard_id" id="hazard_id"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form page is missing %q", want)
		}
	}
	if strings.Contains(body, `<select name="hazard_id"`) {
		t.Error("the hazard category must not be a dropdown on the SOC form")
	}
}

// A topic chosen without a hazard still saves the hazard category the topic maps to.
func TestSOCSubmitDerivesTheHazardCategoryFromTheTopic(t *testing.T) {
	c, sink := signedIn(t)
	if rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(map[string]string{"topic_id": "1"})); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
	if sink.rows[0].TopicID != 1 || sink.rows[0].HazardID != 1 {
		t.Fatalf("want topic 1 and hazard 1, got %+v", sink.rows[0])
	}
}

func TestSigninPageHasTheSearchableNameCombo(t *testing.T) {
	c, _, _ := newSOCTest(t)
	body := c.do("GET", "/soc", "", "").Body.String()
	for _, want := range []string{`<body class="soc-theme">`, `class="add-activity"`, `role="combobox"`, `class="combo-list"`, `id="dob-input"`, `/soc/static/soc-signin.js?v=`} {
		if !strings.Contains(body, want) {
			t.Errorf("sign-in page is missing %q", want)
		}
	}
}

func formBody(extra map[string]string) string {
	v := url.Values{
		"date": {"2026-01-05"}, "category": {"unsafe_condition"},
		"description": {"Kabel berserakan"}, "area": {"Camp"}, "responsible_company": {"STARWAY ENGINEERING INDONESIA"},
	}
	for k, x := range extra {
		v.Set(k, x)
	}
	return v.Encode()
}

func TestSubmitSavesWithSessionIdentity(t *testing.T) {
	c, sink := signedIn(t)
	// A visitor trying to file under someone else's name by adding fields must have no effect.
	rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(map[string]string{"observer_name": "Evil", "employee_id": "E999"}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/soc/thanks?ref=SOC-000001" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(sink.rows) != 1 {
		t.Fatalf("want 1 saved row, got %d", len(sink.rows))
	}
	got := sink.rows[0]
	if got.ReporterName != "Budi Santoso" || got.EmployeeID != "E001" || got.Designation != "Operator" {
		t.Fatalf("reporter must come from the session, got %+v", got)
	}
	if got.Status != statusOpen || got.Category != catUnsafeCondition || got.Company != "SALAWATI MOTORINDO" {
		t.Fatalf("bad saved row: %+v", got)
	}
	if got.TopicID != 0 || got.HazardID != 0 || got.TopicPick != topicPickNone || got.ResponsibleCompany != "STARWAY ENGINEERING INDONESIA" {
		t.Fatalf("no topic is picked for the reporter and the responsible company is kept: %+v", got)
	}
	thanks := c.do("GET", "/soc/thanks?ref=SOC-000001", "", "")
	if thanks.Code != 200 || !strings.Contains(thanks.Body.String(), "SOC-000001") {
		t.Fatalf("thank-you page: %d", thanks.Code)
	}
	mine := c.do("GET", "/soc/mine", "", "").Body.String()
	if !strings.Contains(mine, "SOC-000001") || !strings.Contains(mine, "Received") {
		t.Fatalf("my submissions should list the report as Received: %s", mine)
	}
}

func TestSubmitInvalidShowsErrorsAndSavesNothing(t *testing.T) {
	c, sink := signedIn(t)
	rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(map[string]string{"description": " ", "date": "2999-01-01"}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Describe your finding") || !strings.Contains(rec.Body.String(), "cannot be in the future") {
		t.Fatalf("errors not shown: %s", rec.Body)
	}
	if len(sink.rows) != 0 {
		t.Fatal("nothing may be saved when validation fails")
	}
}

func TestThanksRejectsOddReferences(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("GET", "/soc/thanks?ref="+url.QueryEscape("<script>alert(1)</script>"), "", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d, want redirect", rec.Code)
	}
}

func TestSignoutClearsTheSession(t *testing.T) {
	c, _ := signedIn(t)
	c.do("POST", "/soc/signout", "", "")
	if rec := c.do("GET", "/soc/form", "", ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("after sign-out the form must be closed, got %d", rec.Code)
	}
}

func TestPublicListenerHasOnlySOCRoutes(t *testing.T) {
	c, _, _ := newSOCTest(t)
	for _, path := range []string{"/start", "/observations", "/users", "/admin/settings", "/workpermitregister", "/api/observations", "/health", "/"} {
		if rec := c.do("GET", path, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s must not exist on the public listener, got %d", path, rec.Code)
		}
	}
}

func TestClientIPTrustsCloudflareHeaderOnlyWhenAsked(t *testing.T) {
	_, h, _ := newSOCTest(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Cf-Connecting-Ip", "198.51.100.9")
	if got := h.clientIP(req); got != "127.0.0.1" {
		t.Fatalf("header must be ignored when not trusted, got %q", got)
	}
	h.trustCF = true
	if got := h.clientIP(req); got != "198.51.100.9" {
		t.Fatalf("header must be used when trusted, got %q", got)
	}
}

// ---- photos ----

// postWithPhotos sends the SOC form as multipart with the given photo files.
func postWithPhotos(c *socClient, files map[string][]byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	vals, _ := url.ParseQuery(formBody(nil))
	for k, v := range vals {
		_ = mw.WriteField(k, v[0])
	}
	i := 0
	for name, data := range files {
		part, _ := mw.CreateFormFile("photos", name)
		_, _ = part.Write(data)
		i++
	}
	_ = mw.Close()
	return c.do("POST", "/soc/form", mw.FormDataContentType(), body.String())
}

// postWithAfter sends the new-report form with After photos and optionally the close tick box.
func postWithAfter(t *testing.T, c *socClient, extra map[string]string, afterFiles int, closeIt bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	vals, _ := url.ParseQuery(formBody(extra))
	for k, v := range vals {
		_ = mw.WriteField(k, v[0])
	}
	if closeIt {
		_ = mw.WriteField("close", "1")
	}
	for i := 0; i < afterFiles; i++ {
		part, _ := mw.CreateFormFile("after_photos", "after.jpg")
		_, _ = part.Write(encodeJPEG(t, noisyImage(200, 150), 85))
	}
	_ = mw.Close()
	return c.do("POST", "/soc/form", mw.FormDataContentType(), body.String())
}

func TestReporterCanCloseOnSubmitWithAnAfterPhoto(t *testing.T) {
	c, sink := signedIn(t)
	rec := postWithAfter(t, c, nil, 1, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if len(sink.rows) != 1 || sink.rows[0].Status != statusClosed {
		t.Fatalf("the post must be saved closed, got %+v", sink.rows)
	}
	if len(sink.photos[0]) != 1 || sink.photos[0][0].Kind != "after" {
		t.Fatalf("want one After photo record, got %v", sink.photos)
	}
}

func TestAfterPhotosWithoutCloseKeepThePostOpen(t *testing.T) {
	c, sink := signedIn(t)
	if rec := postWithAfter(t, c, nil, 2, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
	if sink.rows[0].Status != statusOpen || len(sink.photos[0]) != 2 {
		t.Fatalf("want open with 2 After photos, got %+v %v", sink.rows[0], sink.photos[0])
	}
}

func TestCloseOnSubmitNeedsAnAfterPhotoUnlessPositive(t *testing.T) {
	c, sink := signedIn(t)
	rec := postWithAfter(t, c, nil, 0, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "After photo") || len(sink.rows) != 0 {
		t.Fatalf("closing without an After photo must be refused and save nothing: %d %s", rec.Code, rec.Body)
	}
	if rec := postWithAfter(t, c, map[string]string{"category": "positive_finding"}, 0, true); rec.Code != http.StatusSeeOther || sink.rows[0].Status != statusClosed {
		t.Fatalf("a Positive Finding closes without After photos: %d %+v", rec.Code, sink.rows)
	}
}

func evidenceFiles(t *testing.T, h *socHandlers) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(h.evidenceDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return entries
}

func signedInWithHandlers(t *testing.T) (*socClient, *socHandlers, *memorySink) {
	c, h, sink := newSOCTest(t)
	c.passCheck()
	if rec := c.signIn(c.search("budi")[0].Ref, "1990-05-14"); rec.Code != 200 {
		t.Fatalf("sign-in failed: %d", rec.Code)
	}
	return c, h, sink
}

func TestSubmitSavesUpToFourCompressedPhotos(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	big := encodePNG(t, noisyImage(1200, 900)) // ~3 MB of noise, far over 500 KB
	files := map[string][]byte{"a.png": big, "b.png": big, "c.jpg": encodeJPEG(t, noisyImage(300, 200), 85), "d.png": big}
	rec := postWithPhotos(c, files)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("4 photos must be accepted, got %d: %s", rec.Code, rec.Body)
	}
	if len(sink.photos) != 1 || len(sink.photos[0]) != 4 {
		t.Fatalf("want 4 photo records, got %v", sink.photos)
	}
	entries := evidenceFiles(t, h)
	if len(entries) != 4 {
		t.Fatalf("want 4 files on disk, got %d", len(entries))
	}
	for _, e := range entries {
		info, _ := e.Info()
		if info.Size() > photoMaxBytes {
			t.Errorf("%s is %d bytes, limit %d", e.Name(), info.Size(), photoMaxBytes)
		}
	}
	for _, p := range sink.photos[0] {
		if p.Kind != "before" || p.Size > photoMaxBytes {
			t.Errorf("bad record %+v", p)
		}
	}
}

func TestSubmitRejectsFivePhotosAndSavesNothing(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	small := encodeJPEG(t, noisyImage(40, 40), 80)
	files := map[string][]byte{}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		files[n+".jpg"] = small
	}
	rec := postWithPhotos(c, files)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "at most 4") {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if len(sink.rows) != 0 || len(evidenceFiles(t, h)) != 0 {
		t.Fatal("nothing may be saved, not the post and not any photo, when there are 5 photos")
	}
}

func TestSubmitRejectsNonImageAndCleansUp(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	good := encodeJPEG(t, noisyImage(40, 40), 80)
	rec := postWithPhotos(c, map[string][]byte{"ok.jpg": good, "evil.jpg": []byte("<script>alert(1)</script>")})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", rec.Code)
	}
	if len(sink.rows) != 0 {
		t.Fatal("the post must not be saved when a photo is refused")
	}
	if n := len(evidenceFiles(t, h)); n != 0 {
		t.Fatalf("the good photo must be removed again when another photo fails, %d file(s) left", n)
	}
}

type failingSink struct{ memorySink }

func (f *failingSink) Save(ctx context.Context, s socSubmission, p []photoRecord) (int64, error) {
	return 0, errors.New("database is down")
}

func TestPhotosAreRemovedWhenThePostCannotBeSaved(t *testing.T) {
	c, h, _ := signedInWithHandlers(t)
	h.sink = &failingSink{}
	rec := postWithPhotos(c, map[string][]byte{"a.jpg": encodeJPEG(t, noisyImage(40, 40), 80)})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	if n := len(evidenceFiles(t, h)); n != 0 {
		t.Fatalf("photos must not be left behind for a post that was not saved, %d left", n)
	}
}

// ---- thumbnails for the visitor ----

func TestOwnerSeesThumbnailsOnThanksAndMine(t *testing.T) {
	c, _, _ := signedInWithHandlers(t)
	rec := postWithPhotos(c, map[string][]byte{"a.jpg": encodeJPEG(t, noisyImage(60, 40), 80)})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	thanks := c.do("GET", rec.Header().Get("Location"), "", "").Body.String()
	if !strings.Contains(thanks, `src="/soc/photo/1"`) {
		t.Fatalf("thank-you page should show the photo just sent: %s", thanks)
	}
	if mine := c.do("GET", "/soc/mine", "", "").Body.String(); !strings.Contains(mine, `src="/soc/photo/1"`) {
		t.Fatalf("my submissions should show thumbnails: %s", mine)
	}
	img := c.do("GET", "/soc/photo/1", "", "")
	if img.Code != 200 || img.Header().Get("Content-Type") != "image/jpeg" || !bytes.HasPrefix(img.Body.Bytes(), []byte{0xFF, 0xD8}) {
		t.Fatalf("photo route: %d %s", img.Code, img.Header().Get("Content-Type"))
	}
	if img.Header().Get("Cache-Control") == "" || !strings.Contains(img.Header().Get("Cache-Control"), "private") {
		t.Error("photos must be cacheable only privately")
	}
}

func TestSomeoneElseCannotSeeMyPhotos(t *testing.T) {
	budi, h, _ := signedInWithHandlers(t)
	postWithPhotos(budi, map[string][]byte{"a.jpg": encodeJPEG(t, noisyImage(60, 40), 80)})

	siti := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	siti.passCheck()
	if rec := siti.signIn(siti.search("siti")[0].Ref, "1988-11-02"); rec.Code != 200 {
		t.Fatalf("second sign-in: %d", rec.Code)
	}
	if rec := siti.do("GET", "/soc/photo/1", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("another employee must get 404 for my photo, got %d", rec.Code)
	}
	if strings.Contains(siti.do("GET", "/soc/mine", "", "").Body.String(), "/soc/photo/") {
		t.Fatal("another employee's list must not show my photos")
	}
	signedOut := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	if rec := signedOut.do("GET", "/soc/photo/1", "", ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("without a session the photo route must redirect, got %d", rec.Code)
	}
	if rec := budi.do("GET", "/soc/photo/abc", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a bad id must be 404, got %d", rec.Code)
	}
}

// ---- topic and hazard suggestion ----

func TestSuggestNeedsSignin(t *testing.T) {
	c, _, _ := newSOCTest(t)
	if rec := c.json("POST", "/soc/api/suggest", `{"text":"kabel berserakan"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 without a sign-in", rec.Code)
	}
}

func TestSuggestAnswersForASignedInEmployee(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.json("POST", "/soc/api/suggest", `{"text":"kabel berserakan di camp"}`)
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Suggestions []suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Suggestions) != 1 || got.Suggestions[0].TopicID != 2 || got.Suggestions[0].HazardID != 2 || got.Suggestions[0].Topic != "Housekeeping" {
		t.Errorf("suggestions = %+v", got.Suggestions)
	}
	if rec := c.json("POST", "/soc/api/suggest", `{"text":"zzz"}`); !strings.Contains(rec.Body.String(), `"suggestions":[]`) {
		t.Errorf("no match should give an empty list, not null: %s", rec.Body)
	}
	if rec := c.json("POST", "/soc/api/suggest", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: got %d", rec.Code)
	}
}

func TestSuggestOffersHazardTopicsOnly(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.json("POST", "/soc/api/suggest", `{"text":"pekerja tidak menggunakan helm"}`) // helm is a PPE (control) keyword
	if !strings.Contains(rec.Body.String(), `"suggestions":[]`) {
		t.Errorf("a control topic must not be offered on the SOC form: %s", rec.Body)
	}
}

func TestSuggestIsRateLimitedPerEmployee(t *testing.T) {
	c, _ := signedIn(t)
	var last int
	for i := 0; i <= socSuggestMax; i++ {
		last = c.json("POST", "/soc/api/suggest", `{"text":"kabel"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("request %d got %d, want 429", socSuggestMax+1, last)
	}
}

func TestFormShowsStaleListsErrorWhenListsAreMissing(t *testing.T) {
	c, h := func() (*socClient, *socHandlers) { c, h, _ := signedInWithHandlers(t); return c, h }()
	h.ref = &refData{load: func(context.Context) (*refSnapshot, error) { return nil, errors.New("db down") }, ttl: refCacheTTL}
	if rec := c.do("GET", "/soc/form", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503 when the lookup lists cannot be loaded", rec.Code)
	}
}

// ---- employee edits their own post ----

// postEdit sends the edit form as multipart: fields, ids of photos to remove, and new photo files.
func postEdit(c *socClient, id int, fields map[string]string, remove []string, files map[string][]byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	merged := map[string]string{}
	vals, _ := url.ParseQuery(formBody(nil))
	for k, v := range vals {
		merged[k] = v[0]
	}
	for k, v := range fields { // overrides replace the default value instead of adding a second one
		merged[k] = v
	}
	for k, v := range merged {
		_ = mw.WriteField(k, v)
	}
	for _, r := range remove {
		_ = mw.WriteField("remove_photo", r)
	}
	for name, data := range files {
		part, _ := mw.CreateFormFile("photos", name)
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	return c.do("POST", "/soc/edit/"+strconv.Itoa(id), mw.FormDataContentType(), body.String())
}

func submitOne(t *testing.T, c *socClient, files map[string][]byte) {
	t.Helper()
	if rec := postWithPhotos(c, files); rec.Code != http.StatusSeeOther {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
}

func TestMineShowsEditLinkOnlyWhileEditable(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	if mine := c.do("GET", "/soc/mine", "", "").Body.String(); !strings.Contains(mine, `href="/soc/edit/1"`) {
		t.Fatalf("an open post must offer Edit: %s", mine)
	}
	sink.verified = map[int]bool{0: true}
	if mine := c.do("GET", "/soc/mine", "", "").Body.String(); strings.Contains(mine, "/soc/edit/") || !strings.Contains(mine, "Verified") {
		t.Fatalf("a verified post must not offer Edit and must say Verified: %s", mine)
	}
}

func TestEditPageIsPrefilledAndOwnedByTheEmployee(t *testing.T) {
	budi, h, _ := signedInWithHandlers(t)
	budi.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	page := budi.do("GET", "/soc/edit/1", "", "")
	if page.Code != 200 {
		t.Fatalf("got %d", page.Code)
	}
	for _, want := range []string{"Edit your report", "SOC-000001", "Kabel berserakan", `action="/soc/edit/1"`, "Save changes"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("edit page is missing %q", want)
		}
	}

	siti := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	siti.passCheck()
	siti.signIn(siti.search("siti")[0].Ref, "1988-11-02")
	if rec := siti.do("GET", "/soc/edit/1", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("another employee must get 404, got %d", rec.Code)
	}
	if rec := postEdit(siti, 1, map[string]string{"description": "hacked"}, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("another employee's edit must get 404, got %d", rec.Code)
	}
	out := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	if rec := out.do("GET", "/soc/edit/1", "", ""); rec.Code != http.StatusSeeOther {
		t.Errorf("signed out must be redirected, got %d", rec.Code)
	}
	if rec := budi.do("GET", "/soc/edit/abc", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("bad id must be 404, got %d", rec.Code)
	}
}

func TestEditSavesChangesAndKeepsIdentityAndStatus(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	rec := postEdit(c, 1, map[string]string{"description": "Kabel dan tangga rusak", "area": "PCC / TCF", "observer_name": "Evil", "status": "closed"}, nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/soc/mine?saved=SOC-000001" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	got := sink.rows[0]
	if got.Description != "Kabel dan tangga rusak" || got.Area != "PCC / TCF" {
		t.Errorf("changes not saved: %+v", got)
	}
	if got.ReporterName != "Budi Santoso" || got.EmployeeID != "E001" || got.Status != statusOpen {
		t.Errorf("identity and status must not change: %+v", got)
	}
	if mine := c.do("GET", "/soc/mine?saved=SOC-000001", "", "").Body.String(); !strings.Contains(mine, "Changes saved") {
		t.Error("the list should confirm the save")
	}
}

func TestEditIsRefusedAfterVerificationOrClosing(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))

	sink.verified = map[int]bool{0: true}
	if rec := c.do("GET", "/soc/edit/1", "", ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "no longer be edited") {
		t.Errorf("verified: got %d", rec.Code)
	}
	if rec := postEdit(c, 1, map[string]string{"description": "late change"}, nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("verified: POST got %d", rec.Code)
	}
	if sink.rows[0].Description != "Kabel berserakan" {
		t.Error("a verified post must not change")
	}

	sink.verified = nil
	sink.rows[0].Status = statusClosed
	if rec := postEdit(c, 1, map[string]string{"description": "late change"}, nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("closed: POST got %d", rec.Code)
	}
	if sink.rows[0].Description != "Kabel berserakan" {
		t.Error("a closed post must not change")
	}
}

func TestEditValidationErrorKeepsThePost(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	rec := postEdit(c, 1, map[string]string{"description": " ", "area": "Mars"}, nil, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Describe your finding") {
		t.Fatalf("got %d", rec.Code)
	}
	if sink.rows[0].Description != "Kabel berserakan" {
		t.Error("a failed edit must not change the post")
	}
}

func TestEditAddsAndRemovesPhotos(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	small := encodeJPEG(t, noisyImage(40, 40), 80)
	submitOne(t, c, map[string][]byte{"a.jpg": small, "b.jpg": small})
	if len(sink.photos[0]) != 2 {
		t.Fatalf("setup: %v", sink.photos)
	}
	removedName := sink.photos[0][0].StoredName

	rec := postEdit(c, 1, nil, []string{"1"}, map[string][]byte{"c.jpg": small, "d.jpg": small})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if len(sink.photos[0]) != 3 {
		t.Errorf("want 3 photos (2 - 1 + 2), got %d", len(sink.photos[0]))
	}
	for _, p := range sink.photos[0] {
		if p.StoredName == removedName {
			t.Error("the removed photo is still on the post")
		}
	}
	if _, err := os.Stat(filepath.Join(h.evidenceDir, removedName)); !os.IsNotExist(err) {
		t.Errorf("the removed photo file must be deleted from disk: %v", err)
	}
	if n := len(evidenceFiles(t, h)); n != 3 {
		t.Errorf("want 3 files on disk, got %d", n)
	}

	// 3 now: adding 2 more would make 5.
	rec = postEdit(c, 1, nil, nil, map[string][]byte{"e.jpg": small, "f.jpg": small})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "at most 4") {
		t.Errorf("5 photos must be refused, got %d: %s", rec.Code, photoErrLine(rec.Body.String()))
	}
	if n := len(evidenceFiles(t, h)); n != 3 {
		t.Errorf("a refused edit must leave no new files behind, got %d", n)
	}
}

func TestEditCannotRemoveSomeoneElsesPhoto(t *testing.T) {
	budi, h, sink := signedInWithHandlers(t)
	small := encodeJPEG(t, noisyImage(40, 40), 80)
	budi.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil)) // post 1, no photos
	siti := &socClient{t: t, mux: h.routes(http.NotFoundHandler()), cookies: map[string]*http.Cookie{}}
	siti.passCheck()
	siti.signIn(siti.search("siti")[0].Ref, "1988-11-02")
	submitOne(t, siti, map[string][]byte{"s.jpg": small}) // post 2 with photo id 1

	rec := postEdit(budi, 1, nil, []string{"1"}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
	if len(sink.photos[1]) != 1 {
		t.Error("another employee's photo must not be removed through my edit")
	}
}

type failingUpdateSink struct{ *memorySink }

func (f failingUpdateSink) UpdateOwn(context.Context, int64, string, socSubmission, []int64, []photoRecord) ([]string, error) {
	return nil, errors.New("database is down")
}

func TestEditFailureRemovesNewFiles(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	h.sink = failingUpdateSink{sink}
	rec := postEdit(c, 1, nil, nil, map[string][]byte{"n.jpg": encodeJPEG(t, noisyImage(40, 40), 80)})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", rec.Code)
	}
	if n := len(evidenceFiles(t, h)); n != 0 {
		t.Errorf("new files must not be left behind, got %d", n)
	}
}

func postAfter(c *socClient, id int, closeIt bool, files map[string][]byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if closeIt {
		_ = mw.WriteField("close", "1")
	}
	for name, data := range files {
		part, _ := mw.CreateFormFile("photos", name)
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	return c.do("POST", "/soc/after/"+strconv.Itoa(id), mw.FormDataContentType(), body.String())
}

func postAfterRemarks(c *socClient, id int, remarks string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("action_taken", remarks)
	_ = mw.Close()
	return c.do("POST", "/soc/after/"+strconv.Itoa(id), mw.FormDataContentType(), body.String())
}

func afterPhoto(t *testing.T) map[string][]byte {
	return map[string][]byte{"after.jpg": encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 40, 30)), 80)}
}

func afterPhotoCount(sink *memorySink, row int) int {
	n := 0
	for _, p := range sink.photos[row] {
		if p.Kind == "after" {
			n++
		}
	}
	return n
}

func TestEmployeeAddsAfterPhotoAndClosesWaitingForVerification(t *testing.T) {
	c, h, sink := signedInWithHandlers(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	if rec := c.do("GET", "/soc/mine", "", ""); !strings.Contains(rec.Body.String(), `href="/soc/after/1"`) {
		t.Fatalf("an open post offers After photos and close: %s", rec.Body)
	}
	if rec := c.do("GET", "/soc/after/1", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Close finding") {
		t.Fatalf("after page: %d", rec.Code)
	}
	// Closing with no After photo is refused and nothing changes.
	if rec := postAfter(c, 1, true, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "At least one After photo") && !strings.Contains(rec.Body.String(), "at least one After photo") {
		t.Fatalf("close without photo: %d %s", rec.Code, rec.Body)
	}
	if sink.rows[0].Status == statusClosed {
		t.Fatal("must not close without an After photo")
	}
	// Saving a photo without closing keeps it open.
	if rec := postAfter(c, 1, false, afterPhoto(t)); rec.Code != http.StatusSeeOther || afterPhotoCount(sink, 0) != 1 || sink.rows[0].Status == statusClosed {
		t.Fatalf("save photo: %d after=%d status=%s", rec.Code, afterPhotoCount(sink, 0), sink.rows[0].Status)
	}
	// Close: done, still not verified.
	if rec := postAfter(c, 1, true, nil); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "closed=") {
		t.Fatalf("close: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if sink.rows[0].Status != statusClosed || sink.verification(0) != verifNotYet {
		t.Fatalf("closed by the employee, verification still waits: %s / %s", sink.rows[0].Status, sink.verification(0))
	}
	mine := c.do("GET", "/soc/mine", "", "").Body.String()
	if !strings.Contains(mine, "waiting for HSE verification") || strings.Contains(mine, "/soc/after/") || strings.Contains(mine, "/soc/edit/") {
		t.Errorf("a closed post waits for HSE and offers no more actions: %s", mine)
	}
	// Closed means closed.
	if rec := postAfter(c, 1, false, afterPhoto(t)); rec.Code != http.StatusForbidden || afterPhotoCount(sink, 0) != 1 {
		t.Errorf("a closed post takes no more photos: %d", rec.Code)
	}
	if rec := c.do("GET", "/soc/after/1", "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("after page of a closed post: %d", rec.Code)
	}
	if n := len(evidenceFiles(t, h)); n != 1 {
		t.Errorf("only the one saved photo should be on disk, found %d", n)
	}
}

func TestPositiveFindingClosesWithoutAfterPhotos(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(map[string]string{"category": "positive_finding"}))
	if rec := postAfter(c, 1, true, nil); rec.Code != http.StatusSeeOther || sink.rows[0].Status != statusClosed {
		t.Errorf("a positive finding needs no After photo: %d %s", rec.Code, sink.rows[0].Status)
	}
}

func TestEmployeeCannotAddAfterToSomeoneElsesPost(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	sink.rows[0].EmployeeID = "E999"
	if rec := c.do("GET", "/soc/after/1", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("page: %d", rec.Code)
	}
	if rec := postAfter(c, 1, true, afterPhoto(t)); rec.Code != http.StatusNotFound || sink.rows[0].Status == statusClosed {
		t.Errorf("post: %d", rec.Code)
	}
}

func TestVerifiedPostCanStillBeClosedByTheEmployee(t *testing.T) {
	c, sink := signedIn(t)
	c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(nil))
	sink.verified = map[int]bool{0: true}
	if rec := postAfter(c, 1, true, afterPhoto(t)); rec.Code != http.StatusSeeOther || sink.rows[0].Status != statusClosed {
		t.Errorf("verified posts can still be closed by the employee: %d %s", rec.Code, sink.rows[0].Status)
	}
	if mine := c.do("GET", "/soc/mine", "", "").Body.String(); !strings.Contains(mine, "Closed and verified") {
		t.Errorf("stage text: %s", mine)
	}
}

func TestNewReportSavesTheRemarksFromTheAfterSection(t *testing.T) {
	c, sink := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	if !strings.Contains(body, `name="action_taken"`) {
		t.Fatal("the After section must have a remarks field")
	}
	rec := postWithAfter(t, c, map[string]string{"action_taken": "  Kabel dirapikan, disarankan rak kabel  "}, 1, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if got := sink.rows[0].ActionTaken; got != "Kabel dirapikan, disarankan rak kabel" {
		t.Fatalf("remarks not saved (trimmed): %q", got)
	}
}

func TestRemarksTooLongAreRefusedAndNothingIsSaved(t *testing.T) {
	c, sink := signedIn(t)
	rec := postWithAfter(t, c, map[string]string{"action_taken": strings.Repeat("x", maxObsText+1)}, 0, false)
	if rec.Code != http.StatusUnprocessableEntity || len(sink.rows) != 0 {
		t.Fatalf("got %d, rows %d", rec.Code, len(sink.rows))
	}
}

func TestAfterPageShowsAndSavesRemarks(t *testing.T) {
	c, sink := signedIn(t)
	if rec := postWithAfter(t, c, map[string]string{"action_taken": "Awal"}, 0, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup failed: %d", rec.Code)
	}
	if page := c.do("GET", "/soc/after/1", "", "").Body.String(); !strings.Contains(page, `name="action_taken"`) || !strings.Contains(page, ">Awal</textarea>") {
		t.Fatalf("the After page must show the saved remarks in a textarea")
	}
	if rec := postAfterRemarks(c, 1, "Diperbarui: pasang rak kabel"); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if got := sink.rows[0].ActionTaken; got != "Diperbarui: pasang rak kabel" || sink.rows[0].Status != statusOpen {
		t.Fatalf("remarks should update without closing: %q %s", got, sink.rows[0].Status)
	}
}

// PPE is a control topic: it has no hazard category. It is still offered and suggested (a worker
// without a helmet is a common report); the post is then saved with a topic and no hazard.
func TestPPEHelmetReportIsSuggestedWithoutAHazard(t *testing.T) {
	got := testSnap().Suggest("pekerja tidak menggunakan helm")
	if got.TopicID != 3 || got.Topic != "PPE" || got.HazardID != 0 {
		t.Fatalf("want PPE with no hazard, got %+v", got)
	}
}

func TestSOCFormOffersTopicsWithoutAHazardAndSavesThem(t *testing.T) {
	c, sink := signedIn(t)
	if body := c.do("GET", "/soc/form", "", "").Body.String(); !strings.Contains(body, `<option value="3" data-hazard="" data-class="">PPE</option>`) {
		t.Fatal("the PPE topic must be offered on the form")
	}
	if rec := c.do("POST", "/soc/form", "application/x-www-form-urlencoded", formBody(map[string]string{"topic_id": "3", "hazard_id": ""})); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if sink.rows[0].TopicID != 3 || sink.rows[0].HazardID != 0 {
		t.Fatalf("want topic 3 and no hazard, got %+v", sink.rows[0])
	}
}

func TestSOCTopicDropdownGroupsHazardAndControlTopics(t *testing.T) {
	c, _ := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	hz, ct, ppe := strings.Index(body, `<optgroup label="Hazard">`), strings.Index(body, `<optgroup label="Control · PPE">`), strings.Index(body, ">PPE</option>")
	if hz < 0 || ct < hz || ppe < ct {
		t.Fatalf("want the Hazard group, then the Control group holding PPE (got %d %d %d)", hz, ct, ppe)
	}
}

// photoErrLine returns the first photo error shown on a page, for failure messages.
func photoErrLine(body string) string {
	i := strings.Index(body, "Photo not accepted")
	if i < 0 {
		return "(no photo error on the page)"
	}
	return body[i:min(len(body), i+160)]
}

// v8: the form shows three suggested topics as buttons, the full list, and "none of these fit".
func TestSOCFormHasTheTopicPicker(t *testing.T) {
	c, _ := signedIn(t)
	body := c.do("GET", "/soc/form", "", "").Body.String()
	for _, want := range []string{`id="topic-chips"`, `id="topic-none"`, `/soc/static/soc-suggest.js?v=`, "attachTopicSuggest", `<option value="">`} {
		if !strings.Contains(body, want) {
			t.Errorf("form page is missing %q", want)
		}
	}
	if strings.Contains(body, "suggest.js?v=") && !strings.Contains(body, "soc-suggest.js?v=") {
		t.Error("the form still loads the single-suggestion script")
	}
}

// The reporter sees the Indonesian label of a topic, not its internal name.
func TestSOCFormShowsTheTopicLabel(t *testing.T) {
	snap := newSnapshot(
		[]refTopic{{ID: 1, Name: "Working at Height", HazardID: 1, Label: "Bekerja di ketinggian"}},
		[]refHazard{{ID: 1, Class: "Safety", Name: "Fall from Height"}}, []string{"Camp"}, nil)
	if g := snap.TopicGroups(); len(g) != 1 || g[0].Items[0].Label != "Bekerja di ketinggian" || g[0].Items[0].Name != "Working at Height" {
		t.Errorf("topic choice = %+v", g)
	}
	if got := (refTopic{Name: "Spill"}).DisplayLabel(); got != "Spill" {
		t.Errorf("a topic without a label falls back to its name, got %q", got)
	}
}
