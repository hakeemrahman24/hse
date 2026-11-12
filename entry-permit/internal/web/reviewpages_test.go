package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/filestore"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
)

// reviewPermit inserts a work permit in the given status.
func (e *testEnv) reviewPermit(t *testing.T, id, status string, patient int64, personal, lep, mcu string) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, personal_id, worker_name, company, nationality,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, id_doc, created_at)
		VALUES (?, ?, ?, ?, ?, 'Budi Santoso', 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore', ?, '', ?, '2026-11-06 08:00:00')`,
		id, lep, status, patient, personal, mcu, id+"/id_doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
}

// reviewWaiver inserts a PendingDoctor waiver with its ticket.
func (e *testEnv) reviewWaiver(t *testing.T, id string) {
	t.Helper()
	w := waiverlog.NewWaiver{RequestID: id, Status: waiver.StatusPendingDoctor, ApplicantType: waiver.TypeVisitorCompany, CompanyID: 1,
		Company: "PT Tamu", Name: "Tamu Satu", Gender: "Male", DOB: "1990-04-02", Nationality: "Indonesia", NIK: "3201020304900001",
		Position: "Auditor", Department: "HSE", VisitLocation: "Site A", Purpose: "Audit", VisitFrom: "2026-11-10", VisitTo: "2026-11-12",
		Days: 3, RequesterName: "R", RequesterEmail: "r@x.id", Phone: "081234567890", Risk: "Heavy equipment", Precaution: "p", Prevent: "v",
		DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00"}
	tk := ticketlog.NewTicket{ID: "TKT-" + id[4:], LEP: id, Status: "PendingDoctor", Arrival: "2026-11-10", Departure: "2026-11-12",
		Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-06 08:00:00"}
	if err := (waiverlog.Store{DB: e.db}).CreateWithTicket(context.Background(), w, tk); err != nil {
		t.Fatal(err)
	}
}

// reviewer adds a staff member with the given roles, signs them in, and returns the cookies.
func (e *testEnv) reviewer(t *testing.T, username string, roles ...staff.Role) []*http.Cookie {
	t.Helper()
	e.addUser(t, username, strings.ToUpper(username[:1])+username[1:], roles...)
	return e.login(t, username, "1234").Cookies()
}

// reviewToken opens a role page and returns its CSRF token.
func (e *testEnv) reviewToken(t *testing.T, page string, cookies []*http.Cookie) string {
	t.Helper()
	r, body := e.get(t, "/staff/"+page, cookies)
	m := csrfRe.FindStringSubmatch(body)
	if r.StatusCode != 200 || m == nil {
		t.Fatalf("%s page: %d, no CSRF token", page, r.StatusCode)
	}
	return m[1]
}

// review posts one decision and returns the status, the redirect and the body.
func (e *testEnv) review(t *testing.T, cookies []*http.Cookie, tok, id, stage, action, note string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/staff/review", strings.NewReader(url.Values{
		"_csrf": {tok}, "subject": {id}, "stage": {stage}, "action": {action}, "note": {note}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(b)
}

func (e *testEnv) statusOf(t *testing.T, table, key, id string) string {
	t.Helper()
	var s string
	if err := e.db.QueryRow(`SELECT status FROM `+table+` WHERE `+key+` = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEachRolePageListsOnlyItsOwnQueue(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-STAGE", "PendingStage1", 1, "", "", "2027-01-31")
	e.reviewPermit(t, "WEP-FINAL", "PendingFinal", 2, "", "", "2027-01-31")
	e.reviewWaiver(t, "WVR-DOCTR")
	doctor := e.reviewer(t, "ayu", staff.Doctor)
	s1 := e.reviewer(t, "sari", staff.Stage1)
	final := e.reviewer(t, "manager", staff.Final)
	cases := []struct {
		name    string
		cookies []*http.Cookie
		page    string
		has     []string
		hasNot  []string
	}{
		{"doctor", doctor, "doctor", []string{"WVR-DOCTR", "Tamu Satu", "Heavy equipment"}, []string{"WEP-STAGE", "WEP-FINAL"}},
		{"stage1", s1, "stage1", []string{"WEP-STAGE", "Budi Santoso"}, []string{"WEP-FINAL", "WVR-DOCTR"}},
		{"final", final, "final", []string{"WEP-FINAL"}, []string{"WEP-STAGE", "WVR-DOCTR"}},
	}
	for _, c := range cases {
		r, body := e.get(t, "/staff/"+c.page, c.cookies)
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", c.name, r.StatusCode)
		}
		for _, want := range c.has {
			if !strings.Contains(body, want) {
				t.Errorf("%s page lacks %q", c.name, want)
			}
		}
		for _, no := range c.hasNot {
			if strings.Contains(body, no) {
				t.Errorf("%s page shows %q from another queue", c.name, no)
			}
		}
	}
}

func TestEachReviewerOpensOnlyTheirOwnQueue(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-STAGE", "PendingStage1", 1, "", "", "2027-01-31")
	e.reviewPermit(t, "WEP-FINAL", "PendingFinal", 2, "", "", "2027-01-31")
	e.reviewWaiver(t, "WVR-DOCTR")
	s1 := e.reviewer(t, "sari", staff.Stage1)
	doctor := e.reviewer(t, "ayu", staff.Doctor)
	final := e.reviewer(t, "manager", staff.Final)
	cases := []struct {
		who     string
		cookies []*http.Cookie
		page    string
		want    int
	}{
		{"stage1 on stage1", s1, "stage1", 200},
		{"doctor on doctor", doctor, "doctor", 200},
		{"final on final", final, "final", 200},
		{"stage1 on final", s1, "final", 403},
		{"stage1 on doctor", s1, "doctor", 403},
		{"doctor on stage1", doctor, "stage1", 403},
		{"final on stage1", final, "stage1", 403},
		{"gate officer on final", e.reviewer(t, "gita", staff.GateOfficer), "final", 403},
		{"gate admin on stage1", e.reviewer(t, "gani", staff.GateAdmin), "stage1", 403},
		{"dashboard-only on doctor", e.reviewer(t, "dewi", staff.Dashboard), "doctor", 403},
		{"admin on final", e.reviewer(t, "adi", staff.Admin), "final", 403},
		{"signed out on final", nil, "final", 303},
	}
	for _, c := range cases {
		if r, _ := e.get(t, "/staff/"+c.page, c.cookies); r.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.who, r.StatusCode, c.want)
		}
	}
	// The menu lists only the person's own pages (and the dashboard), not the other queues.
	_, body := e.get(t, "/staff/stage1", s1)
	for _, href := range []string{`href="/staff/doctor"`, `href="/staff/final"`} {
		if strings.Contains(body, href) {
			t.Errorf("stage 1 menu offers %s", href)
		}
	}
}

func TestEveryReviewPageShowsWhichRolesHaveApprovalsWaiting(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-STAGE", "PendingStage1", 1, "", "", "2027-01-31")
	e.reviewPermit(t, "WEP-FINAL", "PendingFinal", 2, "", "", "2027-01-31")
	e.reviewWaiver(t, "WVR-DOCTR")
	for page, cookies := range map[string][]*http.Cookie{
		"stage1": e.reviewer(t, "sari", staff.Stage1), "doctor": e.reviewer(t, "ayu", staff.Doctor), "final": e.reviewer(t, "manager", staff.Final)} {
		_, body := e.get(t, "/staff/"+page, cookies)
		want := map[string]string{"stage1": "Stage 1: 1", "doctor": "Doctor: 1", "final": "Final: 1"}
		own := map[string]string{"stage1": "Stage 1 (you): 1", "doctor": "Doctor (you): 1", "final": "Final (you): 1"}
		for p, text := range want {
			if p == page {
				text = own[p]
			}
			if !strings.Contains(body, text) {
				t.Errorf("%s page lacks %q", page, text)
			}
		}
		if !strings.Contains(body, "Waiting for approval") {
			t.Errorf("%s page has no notice", page)
		}
	}
}

func TestThePendingNoticeListsOnlyRolesWithSomethingWaiting(t *testing.T) {
	e := newEnv(t)
	final := e.reviewer(t, "manager", staff.Final)
	_, body := e.get(t, "/staff/final", final)
	if !strings.Contains(body, "No approvals are waiting") {
		t.Error("an empty system should say nothing is waiting")
	}
	e.reviewPermit(t, "WEP-STAGE", "PendingStage1", 1, "", "", "2027-01-31")
	_, body = e.get(t, "/staff/final", final)
	if !strings.Contains(body, "Stage 1: 1") {
		t.Error("the waiting Stage 1 request is not announced to the Final reviewer")
	}
	for _, no := range []string{"Doctor: ", "Final (you)", "Final: "} {
		if strings.Contains(body, no) {
			t.Errorf("a role with nothing waiting is listed: %q", no)
		}
	}
	// The notice carries counts only: no names, ids or documents of the other queue.
	for _, leak := range []string{"WEP-STAGE", "Budi Santoso"} {
		if strings.Contains(body, leak) {
			t.Errorf("the other queue leaks %q", leak)
		}
	}
}

func TestStage1ApproveMovesThePermitOnAndRecordsWhoDidIt(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-AAAAA", "PendingStage1", 1, "", "", "2027-01-31")
	c := e.reviewer(t, "sari", staff.Stage1)
	tok := e.reviewToken(t, "stage1", c)
	code, loc, _ := e.review(t, c, tok, "WEP-AAAAA", "stage1", "approve", "documents match")
	if code != 303 || !strings.HasPrefix(loc, "/staff/stage1") || !strings.Contains(loc, "ok=") {
		t.Fatalf("approve: %d %q", code, loc)
	}
	if got := e.statusOf(t, "permit", "request_id", "WEP-AAAAA"); got != "PendingFinal" {
		t.Errorf("status %q", got)
	}
	var reviewer, note, at string
	if err := e.db.QueryRow(`SELECT reviewer, note, at FROM review_step WHERE subject_id = 'WEP-AAAAA' AND stage = 'stage1'`).Scan(&reviewer, &note, &at); err != nil {
		t.Fatal(err)
	}
	if reviewer != "Sari" || note != "documents match" || at != string(e.clock.now) {
		t.Errorf("recorded %q %q %q", reviewer, note, at)
	}
}

func TestFinalApproveIssuesTheLEPAndRenewsTheEarlierPermit(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-OLD01", "Approved", 1, "", "LEP-OLDOL", "2026-12-31")
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	c := e.reviewer(t, "manager", staff.Final)
	tok := e.reviewToken(t, "final", c)
	code, loc, _ := e.review(t, c, tok, "WEP-NEW01", "final", "approve", "")
	if code != 303 || !strings.Contains(loc, "LEP-") {
		t.Fatalf("final approve: %d %q", code, loc)
	}
	var lep, valid string
	if err := e.db.QueryRow(`SELECT lep_no, lep_valid_until FROM permit WHERE request_id = 'WEP-NEW01'`).Scan(&lep, &valid); err != nil || !strings.HasPrefix(lep, "LEP-") || valid != "2027-01-17" {
		t.Errorf("LEP %q valid %q %v", lep, valid, err)
	}
	if got := e.statusOf(t, "permit", "request_id", "WEP-OLD01"); got != "Renewed by "+lep {
		t.Errorf("earlier permit is %q", got)
	}
	// The same click again (or a second reviewer) must not issue another LEP.
	code, _, body := e.review(t, c, tok, "WEP-NEW01", "final", "approve", "")
	if code != 409 || !strings.Contains(body, "already actioned") {
		t.Errorf("second action: %d", code)
	}
}

func TestRejectNeedsANoteAndShowsWhyItWasRefused(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-AAAAA", "PendingStage1", 1, "", "", "2027-01-31")
	c := e.reviewer(t, "sari", staff.Stage1)
	tok := e.reviewToken(t, "stage1", c)
	code, _, body := e.review(t, c, tok, "WEP-AAAAA", "stage1", "reject", "no")
	if code != 400 || !strings.Contains(alertText(body), "longer note") || e.statusOf(t, "permit", "request_id", "WEP-AAAAA") != "PendingStage1" {
		t.Fatalf("short note: %d %q", code, alertText(body))
	}
	if code, _, _ := e.review(t, c, tok, "WEP-AAAAA", "stage1", "reject", "the visa copy is unreadable"); code != 303 {
		t.Fatalf("reject with a note: %d", code)
	}
	if got := e.statusOf(t, "permit", "request_id", "WEP-AAAAA"); got != "Rejected" {
		t.Errorf("status %q", got)
	}
}

func TestWaiverGoesDoctorThenStage1ThenFinalOverTheWeb(t *testing.T) {
	e := newEnv(t)
	e.reviewWaiver(t, "WVR-AAAAA")
	steps := []struct {
		user  string
		role  staff.Role
		stage string
	}{{"ayu", staff.Doctor, "doctor"}, {"sari", staff.Stage1, "stage1"}, {"manager", staff.Final, "final"}}
	for _, s := range steps {
		c := e.reviewer(t, s.user, s.role)
		tok := e.reviewToken(t, s.stage, c)
		if code, loc, _ := e.review(t, c, tok, "WVR-AAAAA", s.stage, "approve", "fine"); code != 303 {
			t.Fatalf("%s: %d %q", s.stage, code, loc)
		}
	}
	if got := e.statusOf(t, "waiver", "request_id", "WVR-AAAAA"); got != "Approved" {
		t.Errorf("waiver %q", got)
	}
	if got := e.statusOf(t, "ticket", "lep_no", "WVR-AAAAA"); got != "Approved" {
		t.Errorf("ticket %q", got)
	}
}

func TestReviewRefusesWhatItShould(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-AAAAA", "PendingStage1", 1, "", "", "2027-01-31")
	s1 := e.reviewer(t, "sari", staff.Stage1)
	tok := e.reviewToken(t, "stage1", s1)
	if code, _, _ := e.review(t, s1, "", "WEP-AAAAA", "stage1", "approve", ""); code != 403 {
		t.Errorf("no CSRF token: %d", code)
	}
	if code, _, _ := e.review(t, s1, "0000", "WEP-AAAAA", "stage1", "approve", ""); code != 403 {
		t.Errorf("wrong CSRF token: %d", code)
	}
	if code, _, _ := e.review(t, nil, tok, "WEP-AAAAA", "stage1", "approve", ""); code != 303 {
		t.Errorf("signed out: %d, want a redirect to the login", code)
	}
	// A Stage 1 reviewer cannot act at the Final stage, even with a valid token.
	if code, _, _ := e.review(t, s1, tok, "WEP-AAAAA", "final", "approve", ""); code != 403 {
		t.Errorf("a Stage 1 reviewer acting as Final: %d", code)
	}
	if code, _, _ := e.review(t, s1, tok, "WEP-AAAAA", "nonsense", "approve", ""); code != 400 {
		t.Errorf("unknown stage: %d", code)
	}
	if code, _, _ := e.review(t, s1, tok, "WEP-AAAAA", "stage1", "maybe", ""); code != 400 {
		t.Errorf("unknown action: %d", code)
	}
	if code, _, _ := e.review(t, s1, tok, "WEP-ZZZZZ", "stage1", "approve", ""); code != 404 {
		t.Errorf("unknown request: %d", code)
	}
	if got := e.statusOf(t, "permit", "request_id", "WEP-AAAAA"); got != "PendingStage1" {
		t.Errorf("a refused action changed the status to %q", got)
	}
}

func TestReviewersCanOpenUploadedFilesAndNobodyElseCan(t *testing.T) {
	e := newEnv(t)
	rel, err := (filestore.Store{Dir: e.uploads}).Save("WEP-AAAAA", "id_doc", pdfBytes, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	doctor := e.reviewer(t, "ayu", staff.Doctor)
	r, body := e.get(t, "/staff/file?path="+url.QueryEscape(rel), doctor)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(body, "%PDF-") {
		t.Fatalf("reviewer: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Errorf("headers %v", r.Header)
	}
	if r, _ := e.get(t, "/staff/file?path="+url.QueryEscape(rel), nil); r.StatusCode != 303 {
		t.Errorf("signed out: %d", r.StatusCode)
	}
	viewer := e.reviewer(t, "dewi", staff.Dashboard)
	if r, _ := e.get(t, "/staff/file?path="+url.QueryEscape(rel), viewer); r.StatusCode != 403 {
		t.Errorf("dashboard-only user: %d", r.StatusCode)
	}
	for _, bad := range []string{"../../etc/passwd", "WEP-AAAAA/../../x.pdf", "WEP-ZZZZZ/id_doc.pdf", ""} {
		if r, _ := e.get(t, "/staff/file?path="+url.QueryEscape(bad), doctor); r.StatusCode != 404 {
			t.Errorf("%q: %d, want 404", bad, r.StatusCode)
		}
	}
}
