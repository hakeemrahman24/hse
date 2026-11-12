package dashboard

import (
	"context"
	"database/sql"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/migrations"
)

const today = "2026-11-10"

func newStore(t *testing.T) (Store, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return Store{DB: d}, d
}

func exec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func addPermit(t *testing.T, d *sql.DB, id, lep, status, name string) {
	t.Helper()
	exec(t, d, `INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, requester_name, requester_email,
		work_location, mcu_valid_until, lep_valid_until, created_at, work_position, entity, parents)
		VALUES (?, ?, ?, 1, ?, 'PT Maju', 'Req', 'r@x.id', 'Onshore', '2027-01-31', '2027-01-17', '2026-06-01 08:00:00', 'Welder', 'Contractor', 'PT LNG (1)')`,
		id, lep, status, name)
}

func addWaiver(t *testing.T, d *sql.DB, id, status, name string) {
	t.Helper()
	exec(t, d, `INSERT INTO waiver (request_id, status, applicant_type, company_id, company, visitor_name, gender, dob, nationality, nik, position,
		department, visit_location, purpose, visit_from, visit_to, days, requester_name, requester_email, phone, risk, precaution, prevent, declared_at, created_at)
		VALUES (?, ?, 'VisitorCompany', 1, 'Visit Co', ?, 'M', '1980-01-01', 'Indonesia', '123', 'Auditor', 'HSE', 'Onshore', 'Audit',
		'2026-11-10', '2026-11-12', 3, 'Req', 'r@x.id', '0812', 'r', 'p', 'v', '2026-06-01 08:00:00', '2026-06-01 08:00:00')`, id, status, name)
}

func addTicket(t *testing.T, d *sql.DB, id, lep, status string, work int, arrival, departure, closedAt string) {
	t.Helper()
	exec(t, d, `INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, rotation_no, closed_at, submitted_at)
		VALUES (?, ?, ?, ?, ?, ?, 'Onshore', 1, ?, '2026-11-01 08:00:00')`, id, lep, work, status, arrival, departure, closedAt)
}

func addInside(t *testing.T, d *sql.DB, lep string) {
	t.Helper()
	exec(t, d, `INSERT INTO gate_state (lep_no, status, last_action, last_action_at, officer, gate, first_checkin, last_checkout, reason, worker_updated_at, received_at)
		VALUES (?, 'INSIDE', 'checkin', '2026-11-10 07:00:00', 'Sari', 'Gate 1', '2026-11-10 07:00:00', '', '', '2026-11-10T00:00:00.000Z', '2026-11-10 07:05:00')`, lep)
}

func names(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPendingListsWhatEachStageStillHasToDecide(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "", "PendingFinal", "Final Fran")
	addPermit(t, d, "WEP-00002", "", "PendingStage1", "Stage Sam")
	addPermit(t, d, "WEP-00003", "LEP-DONE1", "Approved", "Done Dan")
	addWaiver(t, d, "WVR-00001", "PendingDoctor", "Doc Dee")
	addWaiver(t, d, "WVR-00002", "Approved", "Okay Olly")
	addPermit(t, d, "WEP-00004", "LEP-TKT01", "Approved", "Ticket Tia")
	addTicket(t, d, "TKT-00001", "LEP-TKT01", "PendingDoctor", 1, "2026-11-12", "2026-11-20", "")

	rows, err := s.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// order: Stage 1, Doctor, Final; the ticket waiting for the doctor shows the worker's name
	if got, want := names(rows), []string{"Stage Sam", "Doc Dee", "Ticket Tia", "Final Fran"}; !equal(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
	if rows[0].Status != "Waiting for Stage 1" || rows[2].Kind != KindTicket || rows[3].Status != "Waiting for Final" {
		t.Errorf("labels = %+v", rows)
	}
}

func TestRegisteredIsEveryIssuedLEPAndTheApprovedOrCompletedWaivers(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "LEP-AAAA1", "Approved", "Zed")
	addPermit(t, d, "WEP-00002", "LEP-AAAA2", "Resigned", "Amy")
	addPermit(t, d, "WEP-00003", "", "PendingFinal", "Not Yet")
	addPermit(t, d, "WEP-00004", "", "Rejected", "Rejected Rob")
	addWaiver(t, d, "WVR-00001", "Approved", "Visitor Vic")
	addWaiver(t, d, "WVR-00002", "Completed", "Visitor Val")
	addWaiver(t, d, "WVR-00003", "PendingStage1", "Visitor Pending")

	rows, err := s.Registered(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// work permits first by name, then visitors by name
	if got, want := names(rows), []string{"Amy", "Zed", "Visitor Val", "Visitor Vic"}; !equal(got, want) {
		t.Fatalf("registered = %v, want %v", got, want)
	}
	if rows[0].Status != "Resigned" || rows[0].Active || !rows[1].Active || rows[1].ID != "LEP-AAAA1" || rows[1].ValidUntil != "2027-01-17" {
		t.Errorf("rows = %+v", rows[:2])
	}
}

func TestActiveIsInsideTheGateOrInsideAnOpenTicketWindow(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "LEP-INSD1", "Approved", "Inside Ian") // at the gate, no ticket window today
	addPermit(t, d, "WEP-00002", "LEP-WIN01", "Approved", "Window Win") // today is inside the ticket
	addPermit(t, d, "WEP-00003", "LEP-FUT01", "Approved", "Future Fay") // ticket starts tomorrow: scheduled, not active
	addPermit(t, d, "WEP-00004", "LEP-CLS01", "Approved", "Closed Cleo")
	addPermit(t, d, "WEP-00005", "LEP-RES01", "Resigned", "Resigned Ray") // no longer approved
	addInside(t, d, "LEP-INSD1")
	addInside(t, d, "LEP-RES01")
	addTicket(t, d, "TKT-00001", "LEP-WIN01", "Approved", 1, "2026-11-08", "2026-11-20", "")
	addTicket(t, d, "TKT-00002", "LEP-FUT01", "Approved", 1, "2026-11-11", "2026-11-25", "")
	addTicket(t, d, "TKT-00003", "LEP-CLS01", "Approved", 1, "2026-11-08", "2026-11-20", "2026-11-09 10:00:00")
	addTicket(t, d, "TKT-00004", "LEP-CLS01", "Closed", 1, "2026-11-01", "2026-11-05", "2026-11-05 10:00:00")

	rows, err := s.Active(context.Background(), today)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(rows), []string{"Inside Ian", "Window Win"}; !equal(got, want) {
		t.Fatalf("active = %v, want %v", got, want)
	}
	if rows[0].Status != "Inside" || rows[1].Status != "In ticket window" || rows[1].Arrival != "2026-11-08" || rows[1].Departure != "2026-11-20" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestScheduledIsAnOpenTicketThatStartsLaterAndShowsTheEarliestOnce(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "LEP-FUT01", "Approved", "Future Fay")
	addPermit(t, d, "WEP-00002", "LEP-WIN01", "Approved", "Window Win")
	addPermit(t, d, "WEP-00003", "LEP-FUT02", "Approved", "Earlier Eve")
	addTicket(t, d, "TKT-00001", "LEP-FUT01", "Approved", 1, "2026-12-01", "2026-12-15", "")
	addTicket(t, d, "TKT-00002", "LEP-FUT01", "Approved", 1, "2026-11-20", "2026-11-28", "")
	addTicket(t, d, "TKT-00003", "LEP-WIN01", "Approved", 1, "2026-11-08", "2026-11-20", "")
	addTicket(t, d, "TKT-00004", "LEP-FUT02", "Approved", 1, "2026-11-11", "2026-11-15", "")
	addTicket(t, d, "TKT-00005", "WVR-00001", "Approved", 0, "2026-11-12", "2026-11-13", "") // a waiver ticket is not a work ticket

	rows, err := s.Scheduled(context.Background(), today)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(rows), []string{"Earlier Eve", "Future Fay"}; !equal(got, want) {
		t.Fatalf("scheduled = %v, want %v", got, want)
	}
	if rows[1].Arrival != "2026-11-20" || rows[1].ID != "LEP-FUT01" || rows[1].Status != "Scheduled" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestGateActivityIsNewestFirstAndCanBeLimitedToOnePermit(t *testing.T) {
	s, d := newStore(t)
	for i, a := range []struct{ id, lep, at, action string }{
		{"1", "LEP-AAAA1", "2026-11-10 07:00:00", "checkin"},
		{"2", "LEP-BBBB1", "2026-11-10 07:30:00", "checkin"},
		{"3", "LEP-AAAA1", "2026-11-10 16:00:00", "checkout"},
	} {
		exec(t, d, `INSERT INTO gate_log (worker_id, at, lep_no, worker_name, company, action, reason, officer, session_id, day, gate, received_at)
			VALUES (?, ?, ?, 'W', 'PT Maju', ?, '', 'Sari', 's', '2026-11-10', 'Gate 1', '2026-11-10 17:00:00')`, i+1, a.at, a.lep, a.action)
	}
	all, err := s.GateActivity(context.Background(), "", 50)
	if err != nil || len(all) != 3 || all[0].Action != "checkout" || all[2].Action != "checkin" {
		t.Fatalf("all = %+v err=%v", all, err)
	}
	one, err := s.GateActivity(context.Background(), "lep-aaaa1", 50) // any case
	if err != nil || len(one) != 2 {
		t.Fatalf("one = %+v err=%v", one, err)
	}
	capped, _ := s.GateActivity(context.Background(), "", 1)
	if len(capped) != 1 || capped[0].At != "2026-11-10 16:00:00" {
		t.Errorf("capped = %+v", capped)
	}
}

func TestInsideIsTheWorkersCurrentlyInsideWithTheirNames(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "LEP-INSD1", "Approved", "Inside Ian")
	addPermit(t, d, "WEP-00002", "LEP-OUT01", "Approved", "Outside Olga")
	addInside(t, d, "LEP-INSD1")
	exec(t, d, `INSERT INTO gate_state (lep_no, status, last_action, last_action_at, officer, gate, first_checkin, last_checkout, reason, worker_updated_at, received_at)
		VALUES ('LEP-OUT01', 'OUTSIDE', 'checkout', '2026-11-10 16:00:00', 'Sari', 'Gate 1', '', '', '', '2026-11-10T09:00:00.000Z', '2026-11-10 16:05:00')`)
	rows, err := s.Inside(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Name != "Inside Ian" || rows[0].Gate != "Gate 1" || rows[0].At != "2026-11-10 07:00:00" {
		t.Fatalf("inside = %+v err=%v", rows, err)
	}
}

func TestSearchMatchesNameCompanyAndIDAnyCaseAndIgnoresApostrophes(t *testing.T) {
	rows := []Row{
		{ID: "LEP-AAAA1", Name: "As'adul Haq", Company: "PT Maju"},
		{ID: "LEP-BBBB1", Name: "Budi", Company: "CV Sentosa"},
	}
	cases := map[string]int{"": 2, "asadul": 1, "AS'ADUL": 1, "sentosa": 1, "lep-bbbb": 1, "maju": 1, "nobody": 0, "  budi  ": 1}
	for q, want := range cases {
		if got := Filter(rows, q); len(got) != want {
			t.Errorf("Filter(%q) = %d rows, want %d", q, len(got), want)
		}
	}
}

func TestADatabaseErrorIsReportedNotSwallowed(t *testing.T) {
	s, d := newStore(t)
	d.Close()
	ctx := context.Background()
	if _, err := s.Pending(ctx); err == nil {
		t.Error("Pending hid a database error")
	}
	if _, err := s.Registered(ctx); err == nil {
		t.Error("Registered hid a database error")
	}
	if _, err := s.Active(ctx, today); err == nil {
		t.Error("Active hid a database error")
	}
	if _, err := s.Scheduled(ctx, today); err == nil {
		t.Error("Scheduled hid a database error")
	}
	if _, err := s.GateActivity(ctx, "", 10); err == nil {
		t.Error("GateActivity hid a database error")
	}
	if _, err := s.Inside(ctx); err == nil {
		t.Error("Inside hid a database error")
	}
}

func TestFindLooksUpAWorkPermitOrAWaiverByItsNumberInAnyCase(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-00001", "LEP-AAAA1", "Approved", "Budi")
	addWaiver(t, d, "WVR-00001", "Approved", "Visitor Vic")
	ctx := context.Background()

	p, ok, err := s.Find(ctx, " lep-aaaa1 ")
	if err != nil || !ok || p.Name != "Budi" || p.Status != "Approved" || p.ValidUntil != "2027-01-17" || p.Kind != KindWork {
		t.Fatalf("permit = %+v ok=%v err=%v", p, ok, err)
	}
	v, ok, err := s.Find(ctx, "WVR-00001")
	if err != nil || !ok || v.Name != "Visitor Vic" || v.ValidUntil != "2026-11-12" || v.Kind != KindVisitor {
		t.Fatalf("waiver = %+v ok=%v err=%v", v, ok, err)
	}
	for _, id := range []string{"", "LEP-ZZZZZ", "WEP-00001"} { // unknown, blank, and a request id (not a number the gate sees)
		if _, ok, err := s.Find(ctx, id); ok || err != nil {
			t.Errorf("Find(%q): ok=%v err=%v", id, ok, err)
		}
	}
	d.Close()
	if _, _, err := s.Find(ctx, "LEP-AAAA1"); err == nil {
		t.Error("Find hid a database error")
	}
}
