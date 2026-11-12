package importer

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func waiverTicketRow(change map[int]string) []string {
	base := map[int]string{1: "TKT-WVR01", 2: "Waiver", 3: "WVR-AAAAA", 44: "", 26: "_", 28: "_", 37: "", 36: "", 40: "", 19: "_", 41: ""}
	for i, v := range change {
		base[i] = v
	}
	return ticketRow(base)
}

func fullBook() Book {
	return Book{
		Permits: sheet(sheetRow(nil)),
		Waivers: sheet(waiverRow(nil)),
		Tickets: sheet(ticketRow(nil), waiverTicketRow(nil)),
	}
}

func runAll(t *testing.T, d *sql.DB, b Book, dry bool) []SheetReport {
	t.Helper()
	reps, err := RunAll(context.Background(), d, b, Options{HeaderRows: 4, DryRun: dry, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 3 {
		t.Fatalf("%d sheet reports", len(reps))
	}
	return reps
}

func addCompany(t *testing.T, d *sql.DB, id int, name string) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO company (company_id, name, synced_at) VALUES (?, ?, '2026-11-06 08:00:00')`, id, name); err != nil {
		t.Fatal(err)
	}
}

func TestRunAllImportsPermitsWaiversAndTicketsTogether(t *testing.T) {
	d := newDB(t)
	addCompany(t, d, 77, "Dinas Kerja")
	reps := runAll(t, d, fullBook(), false)
	for i, name := range []string{"Work permits", "Waivers", "Tickets"} {
		if reps[i].Name != name {
			t.Errorf("section %d is %q, want %q", i, reps[i].Name, name)
		}
	}
	if reps[0].Inserted != 1 || reps[1].Inserted != 1 || reps[2].Inserted != 2 {
		t.Fatalf("reports %+v", reps)
	}
	var companyID int
	var email string
	if err := d.QueryRow(`SELECT company_id FROM waiver WHERE request_id = 'WVR-AAAAA'`).Scan(&companyID); err != nil || companyID != 77 {
		t.Errorf("company id %d err %v: the waiver must point at the company in the mirror", companyID, err)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM review_step WHERE subject_id = 'WVR-AAAAA'`); n != 3 {
		t.Errorf("%d waiver review steps", n)
	}
	// a ticket has no address of its own in the sheet: it takes its permit's or waiver's
	if err := d.QueryRow(`SELECT requester_email FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&email); err != nil || email != "rina@example.com" {
		t.Errorf("work ticket email %q err %v", email, err)
	}
	if err := d.QueryRow(`SELECT requester_email FROM ticket WHERE ticket_id = 'TKT-WVR01'`).Scan(&email); err != nil || email != "tamu@example.com" {
		t.Errorf("waiver ticket email %q err %v", email, err)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM import_row`); n != 4 {
		t.Errorf("%d import records, want 4", n)
	}
}

func TestRunAllDryRunWritesNothingAndTheSecondRealRunChangesNothing(t *testing.T) {
	d := newDB(t)
	dry := runAll(t, d, fullBook(), true)
	if dry[2].Inserted != 2 {
		t.Fatalf("dry %+v", dry)
	}
	if n := count(t, d, `SELECT (SELECT COUNT(*) FROM permit) + (SELECT COUNT(*) FROM waiver) + (SELECT COUNT(*) FROM ticket) + (SELECT COUNT(*) FROM import_row)`); n != 0 {
		t.Fatalf("a dry run wrote %d rows", n)
	}
	runAll(t, d, fullBook(), false)
	again := runAll(t, d, fullBook(), false)
	if again[0].Unchanged != 1 || again[1].Unchanged != 1 || again[2].Unchanged != 2 || again[2].Inserted != 0 {
		t.Errorf("second run %+v", again)
	}
}

func TestATicketWhoseWaiverOrPermitWasNotImportedIsRefusedWithThatReason(t *testing.T) {
	d := newDB(t)
	b := fullBook()
	b.Tickets = sheet(ticketRow(nil), ticketRow(map[int]string{1: "TKT-LOST1", 3: "LEP-ZZZZZ"}))
	reps := runAll(t, d, b, false)
	if reps[2].Inserted != 1 || len(reps[2].Rejected) != 1 {
		t.Fatalf("tickets %+v", reps[2])
	}
	if r := reps[2].Rejected[0]; r.ID != "TKT-LOST1" || !strings.Contains(r.Msg, "LEP-ZZZZZ") || !strings.Contains(r.Msg, "not imported") {
		t.Errorf("reject %+v", r)
	}
	// a permit that fails its own checks takes its tickets with it
	d2 := newDB(t)
	b2 := fullBook()
	b2.Permits = sheet(sheetRow(map[int]string{20: "Mars"}))
	reps = runAll(t, d2, b2, false)
	if len(reps[0].Rejected) != 1 || reps[2].Inserted != 1 || len(reps[2].Rejected) != 1 {
		t.Errorf("permit rejected: %+v", reps)
	}
}

func TestATicketTheGateOrTheJobsChangedSinceTheImportIsNeverOverwritten(t *testing.T) {
	d := newDB(t)
	runAll(t, d, fullBook(), false)
	if _, err := d.Exec(`UPDATE ticket SET status = 'Closed', closed_at = '2026-11-09 08:00:00' WHERE ticket_id = 'TKT-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	b := fullBook()
	b.Tickets = sheet(ticketRow(map[int]string{10: "11/27/2026 0:00:00"}), waiverTicketRow(nil))
	reps := runAll(t, d, b, false)
	if reps[2].Replaced != 0 || len(reps[2].Rejected) != 1 || !strings.Contains(reps[2].Rejected[0].Msg, "changed in the new system") {
		t.Fatalf("tickets %+v", reps[2])
	}
	var status string
	_ = d.QueryRow(`SELECT status FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&status)
	if status != "Closed" {
		t.Errorf("status %q: the new system's closing was overwritten", status)
	}
}

func TestAChangedSheetTicketIsReplacedWhenNothingChangedInTheNewSystem(t *testing.T) {
	d := newDB(t)
	runAll(t, d, fullBook(), false)
	b := fullBook()
	b.Tickets = sheet(ticketRow(map[int]string{10: "11/27/2026 0:00:00"}), waiverTicketRow(nil))
	reps := runAll(t, d, b, false)
	if reps[2].Replaced != 1 || reps[2].Unchanged != 1 {
		t.Fatalf("tickets %+v", reps[2])
	}
	var dep string
	_ = d.QueryRow(`SELECT departure FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&dep)
	if dep != "2026-11-27" {
		t.Errorf("departure %q", dep)
	}
}

func TestAWaiverTheDailyJobCompletedSinceTheImportIsNeverOverwritten(t *testing.T) {
	d := newDB(t)
	runAll(t, d, fullBook(), false)
	if _, err := d.Exec(`UPDATE waiver SET status = 'Completed' WHERE request_id = 'WVR-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	b := fullBook()
	b.Waivers = sheet(waiverRow(map[int]string{18: "Changed purpose"}))
	reps := runAll(t, d, b, false)
	if reps[1].Replaced != 0 || len(reps[1].Rejected) != 1 || !strings.Contains(reps[1].Rejected[0].Msg, "changed in the new system") {
		t.Fatalf("waivers %+v", reps[1])
	}
}

func TestAWaiverWhoseCompanyIsNotInTheMirrorIsImportedWithCompanyZeroAndReported(t *testing.T) {
	d := newDB(t)
	reps := runAll(t, d, fullBook(), false)
	if reps[1].Inserted != 1 {
		t.Fatalf("waivers %+v", reps[1])
	}
	if g := reps[1].Warnings["company_id"]; g.Count != 1 {
		t.Errorf("warnings %+v: the missing company must be reported", reps[1].Warnings)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM waiver WHERE company_id = 0`); n != 1 {
		t.Errorf("%d waivers with company 0", n)
	}
}

func TestANativeTicketOrWaiverWithTheSameIdIsLeftAloneAndReported(t *testing.T) {
	d := newDB(t)
	if _, err := d.Exec(`INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, rotation_no, submitted_at)
		VALUES ('TKT-AAAAA', 'LEP-NATIV', 1, 'Approved', '2026-12-01', '2026-12-10', 'Onshore', 1, '2026-11-09 08:00:00')`); err != nil {
		t.Fatal(err)
	}
	reps := runAll(t, d, fullBook(), false)
	if len(reps[2].Rejected) != 1 || reps[2].Rejected[0].ID != "TKT-AAAAA" || !strings.Contains(reps[2].Rejected[0].Msg, "already exists") {
		t.Fatalf("tickets %+v", reps[2])
	}
	var lep string
	_ = d.QueryRow(`SELECT lep_no FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&lep)
	if lep != "LEP-NATIV" {
		t.Errorf("lep %q: the new system's ticket was overwritten", lep)
	}
}

func TestReportSectionsPrintWithTheirNames(t *testing.T) {
	var b strings.Builder
	WriteAll(&b, []SheetReport{{Name: "Work permits", Report: Report{Rows: 1, Inserted: 1}}, {Name: "Tickets", Report: Report{Rows: 2, Inserted: 2}}})
	out := b.String()
	for _, want := range []string{"== Work permits ==", "== Tickets ==", "Rows read: 1", "Rows read: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
