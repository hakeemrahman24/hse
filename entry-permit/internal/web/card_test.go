package web

import (
	"context"
	"entry-permit/internal/dates"
	"entry-permit/internal/ticket"
	"entry-permit/internal/ticketlog"
	"net/http"
	"os"
	"strings"
	"testing"

	"entry-permit/internal/staff"
)

// cardPermit is an Approved permit with the fields the card shows.
func (e *testEnv) cardPermit(t *testing.T) {
	t.Helper()
	e.approvedPermit(t, testLEP, "Fit")
	if _, err := e.db.Exec(`UPDATE permit SET entity = 'Subcontractor', work_position = 'Driver LV',
		parents = 'PT LNG (2); PT Karya (1)' WHERE lep_no = ?`, testLEP); err != nil {
		t.Fatal(err)
	}
}

func TestCardPageShowsAMaskedCardWithAQRCode(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	r, body := e.get(t, "/c/"+testLEP, nil)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{"Budi S.", "PT Maju", "PT Karya", "Driver LV", testLEP, "30 Jun 2027", "Valid", "<svg", "card-contractor"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Privacy: no full surname, no birth date, no requester e-mail, no second-level parent.
	for _, bad := range []string{"Santoso", "05/03/1988", "1988-03-05", "rina@example.com", "PT LNG", "WEP-"} {
		if strings.Contains(body, bad) {
			t.Errorf("public card must not show %q", bad)
		}
	}
	if !strings.Contains(body, `href="/ticket?lep=`+testLEP+`"`) {
		t.Error("the card links to the working schedule page")
	}
}

func TestCardPageHasItsOwnOpenGraphTags(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	_, body := e.get(t, "/c/"+testLEP, nil)
	for _, want := range []string{
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="Entry Permit ` + testLEP + `">`,
		`<meta property="og:url" content="https://permit.example/c/` + testLEP + `">`,
		`content="Budi S. · PT Maju · Valid until 30 Jun 2027"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(body, "Santoso") {
		t.Error("link previews must not carry the full name")
	}
	// Ordinary pages carry no card tags.
	_, home := e.get(t, "/", nil)
	if strings.Contains(home, "og:title") {
		t.Error("only the card page has og tags")
	}
}

func TestCardPageStatesExpiredAndReplaced(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	e.clock.now = "2027-07-01 08:00:00" // the day after the validity ends
	_, body := e.get(t, "/c/"+testLEP, nil)
	if !strings.Contains(body, "Expired") || strings.Contains(body, `class="state state-valid"`) {
		t.Error("a card past its date reads Expired")
	}

	e.clock.now = "2026-11-06 08:00:00"
	if _, err := e.db.Exec(`UPDATE permit SET status = 'Renewed by LEP-ZZZZZ' WHERE lep_no = ?`, testLEP); err != nil {
		t.Fatal(err)
	}
	_, body = e.get(t, "/c/"+testLEP, nil)
	if !strings.Contains(body, "Replaced by a newer permit") {
		t.Error("a renewed card says it was replaced")
	}
}

func TestCardPageGivesTheSameNotFoundForEveryUnknownNumber(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	for _, path := range []string{"/c/LEP-ZZZZZ", "/c/nonsense", "/c/lep-abcde%20", "/c/WVR-ABCDE", "/c/LEP-ABCDEF"} {
		r, body := e.get(t, path, nil)
		if r.StatusCode != 404 {
			t.Errorf("%s: status %d", path, r.StatusCode)
		}
		if strings.Contains(body, "Budi") {
			t.Errorf("%s leaked a name", path)
		}
	}
}

func TestCardPageIsNeverCached(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	r, _ := e.get(t, "/c/"+testLEP, nil)
	if cc := r.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control %q: a card must not be cached, validity changes daily", cc)
	}
}

func TestOldQRAddressesStillWork(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	cases := []struct{ path, want string }{
		{"/?page=ticket&lep=" + testLEP, "/ticket?lep=" + testLEP},
		{"/?page=ticket&lep=lep-abcde", "/ticket?lep=" + testLEP}, // old links were typed in any case
		{"/?page=card&id=" + testLEP, "/c/" + testLEP},
	}
	for _, c := range cases {
		r, _ := e.get(t, c.path, nil)
		if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != c.want {
			t.Errorf("%s: %d -> %q, want %q", c.path, r.StatusCode, r.Header.Get("Location"), c.want)
		}
	}
}

func TestOldQRAddressesNeverRedirectToAnythingButOurOwnPages(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{
		"/?page=ticket&lep=https://evil.example", "/?page=ticket&lep=//evil.example", "/?page=ticket&lep=",
		"/?page=card&id=../../x", "/?page=other&lep=" + testLEP, "/?page=ticket",
	} {
		r, _ := e.get(t, path, nil)
		if r.StatusCode != 200 {
			t.Errorf("%s: status %d, a bad address just shows the home page", path, r.StatusCode)
		}
	}
}

func TestSignedInGateOfficerGoesToTheGatePageFromAScan(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	officer := e.reviewer(t, "gina", staff.GateOfficer)
	for _, path := range []string{"/?page=ticket&lep=" + testLEP, "/c/" + testLEP} {
		r, _ := e.get(t, path, officer)
		if loc := r.Header.Get("Location"); r.StatusCode != http.StatusSeeOther || loc != "/staff/gate?lep="+testLEP {
			t.Errorf("%s: %d -> %q", path, r.StatusCode, loc)
		}
	}
	// Another role gets the ordinary public flow.
	doctor := e.reviewer(t, "dina", staff.Doctor)
	r, _ := e.get(t, "/?page=ticket&lep="+testLEP, doctor)
	if r.Header.Get("Location") != "/ticket?lep="+testLEP {
		t.Errorf("a non-gate user was sent to %q", r.Header.Get("Location"))
	}
}

// The card page follows the legacy card (owner request 2026-11-08): a portrait ID card with a
// logo header, a 3x4 photo frame, labelled details and the QR code, in the palette the old system
// derived from one primary colour per theme (blue company, orange contractor).
func TestCardPageUsesTheLegacyCardLayoutAndColours(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	_, body := e.get(t, "/c/"+testLEP, nil)
	for _, want := range []string{
		`class="lepc card-contractor"`, `lepc-header`, `/static/logo-white.png`, `lepc-photo-ph`,
		"Name:", "Company:", "Position:", "Entry Permit:", "Valid Until:", "(PT Karya)", "<svg", "/static/cardfit.js",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	raw, err := os.ReadFile("../../web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{
		".lepc.card-company", ".lepc.card-contractor", // one palette per theme
		"#e0ebfa", "#003687", "#387ce1", // company: light background, dark label, header
		"#fcebe2", "#913707", "#ef7d41", // contractor
	} {
		if !strings.Contains(css, want) {
			t.Errorf("the stylesheet lacks the card colour %q", want)
		}
	}
}

// photoOfBudi gives Budi's permit a photo, as the worker does from their own page.
func (e *testEnv) photoOfBudi(t *testing.T) {
	t.Helper()
	if code, _ := e.photoPost(t, e.photoToken(t), bigJPEG(t)); code != 200 {
		t.Fatalf("photo setup failed: %d", code)
	}
}

// The ID photo is personal data, so only the permit's own worker (signed in) and signed-in staff see it.
func TestCardPhotoIsShownToTheOwnerAndStaffOnly(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	e.photoOfBudi(t)
	photo := "/c/" + testLEP + "/photo"

	// The public sees no picture and cannot fetch one.
	_, body := e.get(t, "/c/"+testLEP, nil)
	if strings.Contains(body, photo) {
		t.Error("the public card carries the photo address")
	}
	if r, _ := e.get(t, photo, nil); r.StatusCode != 404 {
		t.Errorf("public photo: status %d, want 404", r.StatusCode)
	}

	// The owner sees the photo and their full name.
	owner := e.signedInWorker(t)
	_, body = e.get(t, "/c/"+testLEP, owner)
	if !strings.Contains(body, `src="`+photo+`"`) || !strings.Contains(body, "Budi Santoso") {
		t.Error("the owner's card lacks the photo or the full name")
	}
	r, _ := e.get(t, photo, owner)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Errorf("owner photo: status %d, type %q, cache %q", r.StatusCode, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"))
	}

	// Another worker's sign-in sees neither.
	other := []*http.Cookie{{Name: workerCookie, Value: signPhoto("test-secret", photoClaims{
		PatientID: 99, Name: "Other Person", Expires: e.app.Clock.Now().AddHours(1)})}}
	_, body = e.get(t, "/c/"+testLEP, other)
	if strings.Contains(body, photo) || strings.Contains(body, "Santoso") {
		t.Error("another worker sees the photo or the full name")
	}
	if r, _ := e.get(t, photo, other); r.StatusCode != 404 {
		t.Errorf("other worker photo: status %d, want 404", r.StatusCode)
	}

	// Signed-in staff see the photo, with the name still shortened.
	doctor := e.reviewer(t, "dina", staff.Doctor)
	_, body = e.get(t, "/c/"+testLEP, doctor)
	if !strings.Contains(body, `src="`+photo+`"`) || strings.Contains(body, "Santoso") {
		t.Error("staff card lacks the photo, or shows the full name")
	}
	if r, _ := e.get(t, photo, doctor); r.StatusCode != 200 {
		t.Errorf("staff photo: status %d", r.StatusCode)
	}
}

func TestCardPhotoNeedsARealPermitWithAPhoto(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	owner := e.signedInWorker(t)
	for _, path := range []string{"/c/" + testLEP + "/photo", "/c/LEP-ZZZZZ/photo", "/c/nonsense/photo"} {
		if r, _ := e.get(t, path, owner); r.StatusCode != 404 {
			t.Errorf("%s: status %d, want 404 (no photo yet, unknown, or bad shape)", path, r.StatusCode)
		}
	}
	// Without a photo the owner's card shows the placeholder, not a broken picture.
	_, body := e.get(t, "/c/"+testLEP, owner)
	if strings.Contains(body, `class="lepc-photo-img"`) || !strings.Contains(body, "lepc-photo-ph") {
		t.Error("a card with no photo should show the placeholder")
	}
}

func TestCardPageSaysWhetherThereIsAWorkingTicketToTheOwnerAndStaffOnly(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	owner := e.signedInWorker(t)
	doctor := e.reviewer(t, "dina", staff.Doctor)

	// The public never learns whether a person has a schedule.
	_, body := e.get(t, "/c/"+testLEP, nil)
	if strings.Contains(strings.ToLower(body), "working ticket") {
		t.Error("the public card carries the working ticket marker")
	}
	// The owner and staff see "No working ticket yet" while there is none.
	for name, who := range map[string][]*http.Cookie{"owner": owner, "staff": doctor} {
		if _, body := e.get(t, "/c/"+testLEP, who); !strings.Contains(body, "<b>No valid working schedule</b>") {
			t.Errorf("%s: no No-valid-working-schedule marker inside the card", name)
		}
	}
	// With an approved ticket they see its dates instead.
	if err := (ticketlog.Store{DB: e.db}).Create(context.Background(), ticketlog.NewTicket{ID: "TKT-CARD1", LEP: testLEP, Work: true, Status: "Approved",
		Arrival: "2026-11-20", Departure: "2026-11-27", Location: "Onshore", Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-13 08:00:00"}); err != nil {
		t.Fatal(err)
	}
	_, body = e.get(t, "/c/"+testLEP, owner)
	if !strings.Contains(body, "<b>Working ticket</b><span>Starts in 14 days</span>") || strings.Contains(body, "No valid working schedule") {
		t.Error("the owner's card does not show the approved ticket")
	}
}

func TestAWorkingTicketThatHasDepartedIsNotAValidSchedule(t *testing.T) {
	e := newEnv(t) // the test clock says 2026-11-06
	e.cardPermit(t)
	owner := e.signedInWorker(t)
	if err := (ticketlog.Store{DB: e.db}).Create(context.Background(), ticketlog.NewTicket{ID: "TKT-OLD01", LEP: testLEP, Work: true, Status: "Approved",
		Arrival: "2026-10-20", Departure: "2026-10-27", Location: "Onshore", Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-10-15 08:00:00"}); err != nil {
		t.Fatal(err)
	}
	_, body := e.get(t, "/c/"+testLEP, owner)
	if !strings.Contains(body, "<b>No valid working schedule</b><span>Ended 27 Oct</span>") || strings.Contains(body, "<b>Working ticket</b>") {
		t.Error("a departed ticket must read as no valid schedule, with the day it ended")
	}
	// A ticket that has not departed yet (today is its departure day) is valid again.
	if err := (ticketlog.Store{DB: e.db}).Create(context.Background(), ticketlog.NewTicket{ID: "TKT-NEW01", LEP: testLEP, Work: true, Status: "Approved",
		Arrival: "2026-11-01", Departure: "2026-11-06", Location: "Onshore", Pattern: "Normal", RotationNo: 2, SubmittedAt: "2026-10-30 08:00:00"}); err != nil {
		t.Fatal(err)
	}
	_, body = e.get(t, "/c/"+testLEP, owner)
	if !strings.Contains(body, "<b>Working ticket</b><span>Ends today</span>") {
		t.Error("a ticket departing today is still valid, and says so")
	}
}

func TestWhenPhraseSaysHowTheValidScheduleStandsToday(t *testing.T) {
	today := dates.Day("2026-11-06")
	for _, c := range []struct{ arr, dep, want string }{
		{"2026-11-20", "2026-11-27", "Starts in 14 days"},
		{"2026-11-07", "2026-11-14", "Starts tomorrow"},
		{"2026-10-30", "2026-11-06", "Ends today"},
		{"2026-10-30", "2026-11-07", "Ends tomorrow"},
		{"2026-10-30", "2026-11-09", "Ends in 3 days"},
		{"2026-10-30", "2026-11-10", ""}, // more than three days left: the dates tell the rest
	} {
		if got := whenPhrase(ticket.Ticket{Arrival: dates.Day(c.arr), Departure: dates.Day(c.dep)}, today); got != c.want {
			t.Errorf("%s to %s: %q, want %q", c.arr, c.dep, got, c.want)
		}
	}
	if got := scheduleLine(ticket.Ticket{Arrival: "2026-10-30", Departure: "2026-11-20"}, today); got != "30 Oct to 20 Nov" {
		t.Errorf("under way with time left shows the dates: %q", got)
	}
}
