package web

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
)

const (
	genericAccessError = "The permit number or date of birth is incorrect."
	testLEP            = "LEP-ABCDE"
)

// approvedPermit inserts an Approved work permit with a known LEP and birth date.
func (e *testEnv) approvedPermit(t *testing.T, lep, mcu string) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, nationality,
		requester_name, requester_email, work_location, mcu_result, mcu_valid_until, lep_valid_until, dob, created_at)
		VALUES (?, ?, 'Approved', 1, 'Budi Santoso', 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore',
		?, '2027-06-30', '2027-06-30', '1988-03-05', '2026-11-06 08:00:00')`, "WEP-"+lep[4:], lep, mcu)
	if err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) ticketAccess(t *testing.T, lep, dob string) (int, string) {
	t.Helper()
	return e.postBody(t, "/ticket", nil, url.Values{"lep": {lep}, "dob": {dob}, "cf-turnstile-response": {"dev-pass"}})
}

// openTicketPage gets past the access step and returns the signed token for proposing.
func (e *testEnv) openTicketPage(t *testing.T, lep string) string {
	t.Helper()
	code, body := e.ticketAccess(t, lep, "05031988")
	m := tokenRe.FindStringSubmatch(body)
	if code != 200 || m == nil {
		t.Fatalf("access: %d %s", code, alertText(body))
	}
	return m[1]
}

func (e *testEnv) propose(t *testing.T, token string, over map[string]string, files map[string][]byte) (int, string) {
	t.Helper()
	f := map[string]string{"token": token, "arrival": "01/12/2026", "departure": "25/01/2027"} // 56 days
	for k, v := range over {
		f[k] = v
	}
	return e.postMultipart(t, "/ticket/propose", f, files)
}

func (e *testEnv) ticketRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM ticket`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTicketPageAsksForPermitNumberAndBirthDate(t *testing.T) {
	e := newEnv(t)
	r, body := e.get(t, "/ticket?lep=LEP-ABCDE", nil)
	if r.StatusCode != 200 || strings.Contains(body, "Coming soon") {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{`action="/ticket"`, `name="lep"`, `value="LEP-ABCDE"`, `name="dob"`, `name="cf-turnstile-response"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "challenges.cloudflare.com") {
		t.Error("the Turnstile widget cannot load on /ticket")
	}
}

func TestTicketAccessWithTheRightBirthDateOpensThePermit(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	for _, dob := range []string{"05031988", "05/03/1988", " 05-03-1988 "} { // digits are what counts
		code, body := e.ticketAccess(t, "lep-abcde", dob)
		if code != 200 || !strings.Contains(body, "Budi Santoso") || !strings.Contains(body, `action="/ticket/propose"`) {
			t.Fatalf("dob %q: %d %s", dob, code, alertText(body))
		}
	}
	_, body := e.ticketAccess(t, testLEP, "05031988")
	for _, want := range []string{`name="arrival"`, `name="departure"`, `name="fatigue_consent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("proposal form lacks %q", want)
		}
	}
}

func TestTicketAccessFailuresAllSayTheSameThing(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	if _, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, requester_name, requester_email,
		work_location, dob, created_at) VALUES ('WEP-PEND1', 'LEP-PEND1', 'PendingFinal', 2, 'Wati', 'R', 'r@x.id', 'Onshore', '1990-01-01', '2026-11-06 08:00:00')`); err != nil {
		t.Fatal(err)
	}
	cases := map[string][2]string{
		"wrong birth date": {testLEP, "06031988"},
		"unknown permit":   {"LEP-ZZZZZ", "05031988"},
		"not approved":     {"LEP-PEND1", "01011990"},
		"bad number":       {"hello", "05031988"},
		"short birth date": {testLEP, "0503"},
		"not a real date":  {testLEP, "31021988"},
	}
	for name, c := range cases {
		code, body := e.ticketAccess(t, c[0], c[1])
		if code != 400 || !strings.Contains(alertText(body), genericAccessError) {
			t.Errorf("%s: %d %q", name, code, alertText(body))
		}
	}
}

func TestTicketAccessLocksAPermitAfterFiveWrongTriesAndAGoodEntryClearsThem(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	for i := 0; i < 4; i++ {
		e.ticketAccess(t, testLEP, "06031988")
	}
	if code, _ := e.ticketAccess(t, testLEP, "05031988"); code != 200 {
		t.Fatalf("a right entry after 4 wrong ones: %d", code)
	}
	for i := 0; i < 5; i++ { // the good entry cleared the count, so five more are needed to lock
		if code, _ := e.ticketAccess(t, testLEP, "06031988"); code != 400 {
			t.Fatalf("wrong try %d: %d", i+1, code)
		}
	}
	code, body := e.ticketAccess(t, testLEP, "05031988")
	if code != 429 || !strings.Contains(alertText(body), "Too many attempts") {
		t.Errorf("locked permit: %d %q", code, alertText(body))
	}
	// The lock belongs to the permit number: another permit is not affected.
	e.approvedPermit(t, "LEP-OTHER", "Fit")
	if code, _ := e.ticketAccess(t, "LEP-OTHER", "05031988"); code != 200 {
		t.Errorf("another permit while the first is locked: %d", code)
	}
}

func TestTicketAccessNeedsTurnstile(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	code, _ := e.postBody(t, "/ticket", nil, url.Values{"lep": {testLEP}, "dob": {"05031988"}})
	if code != 400 {
		t.Errorf("without the security check: %d", code)
	}
}

func TestProposeSavesAnApprovedTicketWithItsRotationAndFatigueDate(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	token := e.openTicketPage(t, testLEP)
	code, body := e.propose(t, token, nil, nil)
	if code != 200 {
		t.Fatalf("propose: %d %s", code, alertText(body))
	}
	id := regexp.MustCompile(`TKT-[A-Z0-9]{5}`).FindString(body)
	if id == "" {
		t.Fatalf("no ticket number shown: %s", body)
	}
	var status, lep, loc, pattern, due, review, mcu string
	var rot, work int
	if err := e.db.QueryRow(`SELECT lep_no, is_work, status, location, pattern, rotation_no, fatigue_due, doctor_review, mcu_result FROM ticket WHERE ticket_id = ?`, id).
		Scan(&lep, &work, &status, &loc, &pattern, &rot, &due, &review, &mcu); err != nil {
		t.Fatal(err)
	}
	// 6 weeks fatigue (cap 8 - 2) from 2026-12-01: 41 days on is 2027-01-11.
	if lep != testLEP || work != 1 || status != "Approved" || loc != "Onshore" || pattern != "Normal" || rot != 1 || due != "2027-01-11" || review != "Approved" || mcu != "Fit" {
		t.Errorf("stored %s %d %s %s %s %d %s %s %s", lep, work, status, loc, pattern, rot, due, review, mcu)
	}
	// The new ticket now shows on the page.
	if _, page := e.ticketAccess(t, testLEP, "05031988"); !strings.Contains(page, "2026-12-01") || !strings.Contains(page, "2027-01-25") {
		t.Error("the saved ticket is not listed")
	}
}

func TestProposeRefusalsSaveNothing(t *testing.T) {
	cases := map[string]map[string]string{
		"arrival in the past": {"arrival": "05/11/2026", "departure": "10/11/2026"},
		"departure first":     {"arrival": "10/12/2026", "departure": "01/12/2026"},
		"after the LEP ends":  {"arrival": "01/06/2027", "departure": "05/07/2027"},
		"no dates":            {"arrival": "", "departure": ""},
		"date format":         {"arrival": "2026-12-01"},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.approvedPermit(t, testLEP, "Fit")
			code, body := e.propose(t, e.openTicketPage(t, testLEP), over, nil)
			if code != 400 || alertText(body) == "" || e.ticketRows(t) != 0 {
				t.Errorf("%d %q rows %d", code, alertText(body), e.ticketRows(t))
			}
		})
	}
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Unfit")
	if code, body := e.propose(t, e.openTicketPage(t, testLEP), nil, nil); code != 400 || !strings.Contains(alertText(body), "Unfit") {
		t.Errorf("unfit MCU: %d %q", code, alertText(body))
	}
	if code, body := e.propose(t, "forged.token", nil, nil); code != 400 || !strings.Contains(alertText(body), "session") {
		t.Errorf("forged token: %d %q", code, alertText(body))
	}
}

func TestProposeTokenExpiresWithTheSession(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	token := e.openTicketPage(t, testLEP)
	e.clock.now = e.clock.now.AddMinutes(31) // the session lasts 30 minutes
	if code, body := e.propose(t, token, nil, nil); code != 400 || !strings.Contains(alertText(body), "session") {
		t.Errorf("expired session: %d %q", code, alertText(body))
	}
}

func TestProposeNextTicketWaitsForTheWindowThenAsksForFatigueConsent(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit")
	if code, body := e.propose(t, e.openTicketPage(t, testLEP), nil, nil); code != 200 {
		t.Fatalf("first ticket: %d %s", code, alertText(body))
	}
	next := map[string]string{"arrival": "26/01/2027", "departure": "04/02/2027"} // 10 days straight after 56
	code, body := e.propose(t, e.openTicketPage(t, testLEP), next, nil)
	if code != 400 || !strings.Contains(alertText(body), "closer") {
		t.Fatalf("before the window opens: %d %q", code, alertText(body))
	}
	e.clock.now = "2027-01-15 08:00:00" // 14 days before the first departure
	token := e.openTicketPage(t, testLEP)
	code, body = e.propose(t, token, next, nil)
	if code != 400 || !strings.Contains(alertText(body), "Fatigue survey consent") {
		t.Fatalf("an extension without consent: %d %q", code, alertText(body))
	}
	next["fatigue_consent"] = "on"
	if code, body = e.propose(t, token, next, nil); code != 200 {
		t.Fatalf("with consent: %d %s", code, alertText(body))
	}
	var pattern string
	var rot, consent int
	if err := e.db.QueryRow(`SELECT pattern, rotation_no, fatigue_consent FROM ticket WHERE arrival = '2027-01-26'`).Scan(&pattern, &rot, &consent); err != nil {
		t.Fatal(err)
	}
	if pattern != "ConsecutiveExtended" || rot != 2 || consent != 1 {
		t.Errorf("stored %s %d %d", pattern, rot, consent)
	}
}

func TestProposeFitWithNoteNeedsTheDeclarations(t *testing.T) {
	e := newEnv(t)
	e.approvedPermit(t, testLEP, "Fit with Note")
	token := e.openTicketPage(t, testLEP)
	if code, body := e.propose(t, token, nil, nil); code != 400 || !strings.Contains(alertText(body), "disclaimer") {
		t.Fatalf("no agreement: %d %q", code, alertText(body))
	}
	over := map[string]string{"mcu_note_agreement": "on"}
	if code, body := e.propose(t, token, over, nil); code != 400 || !strings.Contains(alertText(body), "medication") {
		t.Fatalf("no medication answer: %d %q", code, alertText(body))
	}
	over["medication_answer"] = "Y"
	over["medication_details"] = "Metformin 500 mg"
	if code, _ := e.propose(t, token, over, nil); code != 400 {
		t.Fatalf("medication without a photo: %d", code)
	}
	if e.ticketRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Fatal("a refused proposal left something behind")
	}
	if code, body := e.propose(t, token, over, map[string][]byte{"medicine_photo": jpgBytes}); code != 200 {
		t.Fatalf("complete declaration: %d %s", code, alertText(body))
	}
	var note, ans, photo string
	if err := e.db.QueryRow(`SELECT doctor_note, med_answer, med_photo FROM ticket`).Scan(&note, &ans, &photo); err != nil {
		t.Fatal(err)
	}
	if note != "MCU note self-declaration" || ans != "Y" || !strings.HasPrefix(photo, "TKT-") {
		t.Errorf("stored %q %q %q", note, ans, photo)
	}
}

func TestWaiverTicketIsShownButNeverProposed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	w := waiverlog.NewWaiver{RequestID: "WVR-ABCDE", Status: waiver.StatusApproved, ApplicantType: waiver.TypeVisitorCompany, CompanyID: 1,
		Company: "PT Tamu", Name: "Tamu Satu", Gender: "Male", DOB: "1990-04-02", Nationality: "Indonesia", NIK: "3201020304900001",
		Position: "Auditor", Department: "HSE", VisitLocation: "Site A", Purpose: "Audit", VisitFrom: "2026-11-10", VisitTo: "2026-11-12",
		Days: 3, RequesterName: "R", RequesterEmail: "r@x.id", Phone: "081234567890", Risk: "r", Precaution: "p", Prevent: "v",
		DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00"}
	if err := (waiverlog.Store{DB: e.db}).Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := (ticketlog.Store{DB: e.db}).Create(ctx, ticketlog.NewTicket{ID: "TKT-WVR01", LEP: "WVR-ABCDE", Work: false, Status: "Approved",
		Arrival: "2026-11-10", Departure: "2026-11-12", Location: "Onshore", Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-06 08:00:00"}); err != nil {
		t.Fatal(err)
	}
	code, body := e.ticketAccess(t, "WVR-ABCDE", "02041990")
	if code != 200 || !strings.Contains(body, "Tamu Satu") || !strings.Contains(body, "2026-11-10") {
		t.Fatalf("waiver access: %d %s", code, alertText(body))
	}
	if strings.Contains(body, `action="/ticket/propose"`) {
		t.Error("a waiver was offered the proposal form")
	}
	// A hand-made request with a genuine token for a waiver must still be refused.
	token := signAccess("test-secret", accessClaims{LEP: "WVR-ABCDE", Expires: e.clock.now.AddMinutes(30)})
	if code, body := e.propose(t, token, nil, nil); code != 400 || !strings.Contains(alertText(body), "read-only") {
		t.Errorf("proposing on a waiver: %d %q", code, alertText(body))
	}
}

func TestWeeksLabelShowsWholeAndPartWeeks(t *testing.T) {
	for days, want := range map[int]string{14: "2", 7: "1", 17: "2.4", 3: "0.4", 28: "4"} {
		if got := weeksLabel(days); got != want {
			t.Errorf("weeksLabel(%d) = %q, want %q", days, got, want)
		}
	}
}
