package web

import (
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/worker"
)

func (e *testEnv) permitPost(t *testing.T, over map[string]string) (int, string) {
	t.Helper()
	v := url.Values{
		"requester_name": {"Rina Requester"}, "requester_email": {"rina@example.com"}, "work_location": {"Onshore"},
		"name": {"Budi Santoso"}, "dob": {"05/03/1988"}, "cf-turnstile-response": {"dev-pass"},
	}
	for k, val := range over {
		v.Set(k, val)
	}
	return e.postBody(t, "/permit", nil, v)
}

// seedPermit stores an earlier permit for patient 1 (Budi in the fake list).
func (e *testEnv) seedPermit(t *testing.T, id, status, lep, company, mcu, lepUntil string) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES (?, ?, ?, 1, 'Budi Santoso', ?, 'Rina', 'rina@example.com', 'Onshore', ?, ?, '2026-06-01 08:00:00')`,
		id, lep, status, company, mcu, lepUntil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPermitPageShowsTheForm(t *testing.T) {
	e := newEnv(t)
	r, body := e.get(t, "/permit", nil)
	for _, want := range []string{"Work Entry Permit", `name="requester_email"`, `name="work_location"`, "Onshore", "Offshore",
		`name="dob"`, `data-namesearch="/api/worker-names"`, "cf-turnstile-response"} {
		if !strings.Contains(body, want) {
			t.Errorf("permit page lacks %q", want)
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "https://challenges.cloudflare.com") {
		t.Error("the permit page needs the Turnstile CSP")
	}
}

func TestPermitStartHappyPath(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	code, body := e.permitPost(t, nil)
	if code != 200 {
		t.Fatalf("status %d: %s", code, alertText(body))
	}
	// MCU 2027-01-31 minus the 2-week grace = 2027-01-17, limited by the MCU.
	for _, want := range []string{"Budi Santoso", "PT Maju", "Welder", "2027-01-31", "2027-01-17", "MCU validity", "Onshore"} {
		if !strings.Contains(body, want) {
			t.Errorf("result lacks %q", want)
		}
	}
	if strings.Contains(body, "1988") {
		t.Error("the date of birth is echoed back")
	}
	if strings.Contains(body, "foreign national") {
		t.Error("an Indonesian worker got the foreign note")
	}
}

func TestPermitStartRejectsBadRequesterInputWithoutAskingMedpost(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	cases := map[string]map[string]string{
		"no name":         {"requester_name": " "},
		"bad email":       {"requester_email": "not-an-email"},
		"display address": {"requester_email": "Rina <rina@example.com>"},
		"two addresses":   {"requester_email": "a@example.com, b@example.com"},
		"no location":     {"work_location": ""},
		"odd location":    {"work_location": "Moon"},
	}
	for name, over := range cases {
		if code, _ := e.permitPost(t, over); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	if e.workers.finds != 0 {
		t.Errorf("medpost was asked %d times for a form that was already invalid", e.workers.finds)
	}
}

func TestPermitStartUsesTheSameProofAsMyLookup(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	if code, body := e.permitPost(t, map[string]string{"dob": "06/03/1988"}); code != 401 || strings.Contains(body, "PT Maju") {
		t.Errorf("wrong DOB: %d", code)
	}
	if code, _ := e.permitPost(t, map[string]string{"cf-turnstile-response": "forged"}); code != 400 {
		t.Errorf("bad token: %d", code)
	}
	if code, _ := e.permitPost(t, map[string]string{"dob": "1988-03-05"}); code != 400 {
		t.Errorf("bad date format: %d", code)
	}
}

func TestPermitStartDuplicateGate(t *testing.T) {
	cases := []struct {
		name                 string
		status, lep, co, mcu string
		lepUntil             string
		wantBlocked          bool
		want                 string
	}{
		{"approved, nothing changed", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-17", true, "Request WEP-OLD01, LEP LEP-AAAAA, valid until 2027-01-17"},
		{"still in review", "PendingStage1", "", "PT Maju", "2027-01-31", "", true, "request in progress (Request WEP-OLD01)"},
		{"approved, MCU renewed since", "Approved", "LEP-AAAAA", "PT Maju", "2026-12-01", "2026-11-17", false, "MCU has been renewed"},
		{"expired", "Expired", "LEP-AAAAA", "PT Maju", "2026-01-31", "2026-01-17", false, "had a permit before"},
		{"changed company", "Approved", "LEP-AAAAA", "PT Lain", "2027-01-31", "2027-01-17", false, "Step 2: details and documents"},
		{"already renewed", "Renewed by LEP-ZZZZZ", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-17", false, "Step 2: details and documents"},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.workers.people = []worker.Person{budi}
		e.seedPermit(t, "WEP-OLD01", c.status, c.lep, c.co, c.mcu, c.lepUntil)
		code, body := e.permitPost(t, nil)
		if code != 200 {
			t.Errorf("%s: status %d", c.name, code)
			continue
		}
		blocked := strings.Contains(body, `role="alert"`)
		if blocked != c.wantBlocked {
			t.Errorf("%s: blocked=%v want %v", c.name, blocked, c.wantBlocked)
		}
		if !strings.Contains(strings.ReplaceAll(body, "next step", "Next step"), c.want) {
			t.Errorf("%s: page lacks %q", c.name, c.want)
		}
	}
}

func TestPermitStartRechecksTheMCU(t *testing.T) {
	for name, emp := range map[string]worker.Employee{
		"expired": {PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "Indonesia", Company: "PT Maju", McuResult: "Fit", McuValidUntil: "2026-11-01"},
		"unfit":   {PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "Indonesia", Company: "PT Maju", McuResult: "Unfit", McuValidUntil: "2027-01-31"},
		"no date": {PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "Indonesia", Company: "PT Maju", McuResult: "Fit"},
	} {
		e := newEnv(t)
		e.workers.people = []worker.Person{budi}
		e.workers.employees = map[string]worker.Employee{"Budi Santoso": emp}
		code, body := e.permitPost(t, nil)
		if code != 200 || !strings.Contains(body, "no valid MCU") || strings.Contains(body, "could be valid until") {
			t.Errorf("%s: %d, alert=%q", name, code, alertText(body))
		}
	}
}

// A worker with no valid MCU is told what to do next, and the way to the medical checkup portal is
// one click away (design critique 2026-11-13: the old red message was a dead end).
func TestBlockedPermitSaysWhatToDoNextAndLinksThePortal(t *testing.T) {
	e := newEnv(t)
	e.app.McuPortalURL = "https://medpost.example.com/mcu-submit"
	e.workers.people = []worker.Person{budi}
	e.workers.employees = map[string]worker.Employee{"Budi Santoso": {PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "Indonesia", Company: "PT Maju", McuResult: "Fit", McuValidUntil: "2026-11-01"}}
	_, body := e.permitPost(t, nil)
	for _, want := range []string{"no valid MCU", "medical checkup portal", `href="https://medpost.example.com/mcu-submit"`} {
		if !strings.Contains(body, want) {
			t.Errorf("blocked page lacks %q", want)
		}
	}
}

func TestPermitStartForeignWorkerGetsTheVisaNote(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	e.workers.employees = map[string]worker.Employee{"Budi Santoso": {
		PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "China", Company: "PT Maju", Entity: "Contractor",
		Position: "Welder", McuResult: "Fit with note", McuValidUntil: "2027-01-31",
	}}
	code, body := e.permitPost(t, nil)
	if code != 200 || !strings.Contains(body, "foreign national") || !strings.Contains(body, "Fit with note") {
		t.Fatalf("foreign worker: %d", code)
	}
}

func TestPermitStartFailsClosedWhenMedpostIsDown(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	e.workers.err = errString("connection refused to 127.0.0.1:8113")
	code, body := e.permitPost(t, nil)
	if code != 503 || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("medpost down: %d", code)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
