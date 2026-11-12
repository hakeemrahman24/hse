package ticketlog

import (
	"context"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/internal/ticket"
	"entry-permit/migrations"
)

func newStore(t *testing.T) Store {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	return Store{DB: d}
}

func sample(id, lep string) NewTicket {
	return NewTicket{
		ID: id, LEP: lep, Work: true, Status: string(ticket.StatusApproved),
		Arrival: "2026-12-01", Departure: "2027-01-25", Location: "Onshore", Pattern: ticket.PatternNormal,
		RotationNo: 1, FatigueDue: "2027-01-11", DoctorReview: "Approved", DoctorNote: "_",
		McuResult: "Fit", RequesterEmail: "rina@example.com", SubmittedAt: "2026-11-06 08:00:00",
	}
}

func TestCreateThenExists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if ok, err := s.Exists(ctx, "TKT-AAAAA"); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	if err := s.Create(ctx, sample("TKT-AAAAA", "LEP-11111")); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Exists(ctx, "TKT-AAAAA"); err != nil || !ok {
		t.Fatalf("after: %v %v", ok, err)
	}
	if err := s.Create(ctx, sample("TKT-AAAAA", "LEP-11111")); err == nil {
		t.Fatal("a second ticket with the same id must fail")
	}
}

func TestForLEPReturnsThatPermitsTicketsOldestFirstAsRuleInput(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	later := sample("TKT-BBBBB", "LEP-11111")
	later.Arrival, later.Departure, later.RotationNo = "2027-02-01", "2027-03-20", 2
	later.Pattern = ticket.PatternConsecutive
	other := sample("TKT-CCCCC", "LEP-22222")
	wvr := sample("TKT-DDDDD", "WVR-33333")
	wvr.Work = false
	for _, n := range []NewTicket{later, sample("TKT-AAAAA", "LEP-11111"), other, wvr} {
		if err := s.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ForLEP(ctx, "lep-11111") // the number may be typed in any case
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "TKT-AAAAA" || got[1].ID != "TKT-BBBBB" {
		t.Fatalf("got %+v", got)
	}
	a := got[0]
	if !a.Work || a.Status != ticket.StatusApproved || a.Arrival != "2026-12-01" || a.Departure != "2027-01-25" ||
		a.Location != "Onshore" || a.Pattern != "Normal" || a.LEP != "LEP-11111" {
		t.Errorf("fields did not round-trip: %+v", a)
	}
	if got[1].Pattern != ticket.PatternConsecutive {
		t.Errorf("pattern %q", got[1].Pattern)
	}
	w, err := s.ForLEP(ctx, "WVR-33333")
	if err != nil || len(w) != 1 || w[0].Work {
		t.Errorf("waiver ticket: %+v %v", w, err)
	}
	// The rules must accept what the store returns: two approved tickets count.
	if ticket.RotationNo(got) != 3 {
		t.Errorf("rotation number %d, want 3", ticket.RotationNo(got))
	}
}

func TestInsertKeepsTheStateOfAnImportedTicket(t *testing.T) {
	// A ticket from the old system may already have started, closed, been locked or been mailed;
	// the importer must be able to store all of that, and a new ticket must still start clean.
	s := newStore(t)
	ctx := context.Background()
	n := sample("TKT-OLD01", "LEP-AAAAA")
	n.Status = "Closed"
	n.RecordStart, n.ClosedAt, n.ClosedReason = "2026-10-01 00:00:00", "2026-10-30 00:00:00", "Planned departure passed"
	n.LepLocked, n.WeeksOnSite, n.ReminderNotified, n.FatigueFlag = true, 4, true, true
	n.WeekN1SentAt, n.WeekN0LockedAt = "2026-10-20 00:00:00", "2026-10-27 00:00:00"
	if err := Insert(ctx, s.DB, n); err != nil {
		t.Fatal(err)
	}
	var start, closed, reason, n1, n0 string
	var locked, weeks, reminded, fatigue int
	err := s.DB.QueryRow(`SELECT record_start, closed_at, closed_reason, lep_locked, weeks_on_site, reminder_notified, fatigue_flag, week_n1_sent_at, week_n0_locked_at
		FROM ticket WHERE ticket_id = 'TKT-OLD01'`).Scan(&start, &closed, &reason, &locked, &weeks, &reminded, &fatigue, &n1, &n0)
	if err != nil {
		t.Fatal(err)
	}
	if start != "2026-10-01 00:00:00" || closed != "2026-10-30 00:00:00" || reason != "Planned departure passed" ||
		locked != 1 || weeks != 4 || reminded != 1 || fatigue != 1 || n1 != "2026-10-20 00:00:00" || n0 != "2026-10-27 00:00:00" {
		t.Errorf("stored %q %q %q %d %d %d %d %q %q", start, closed, reason, locked, weeks, reminded, fatigue, n1, n0)
	}
	if err := Insert(ctx, s.DB, sample("TKT-NEW01", "LEP-BBBBB")); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT record_start, lep_locked, weeks_on_site, reminder_notified FROM ticket WHERE ticket_id = 'TKT-NEW01'`).Scan(&start, &locked, &weeks, &reminded); err != nil || start != "" || locked+weeks+reminded != 0 {
		t.Errorf("a new ticket did not start clean: %q %d %d %d err %v", start, locked, weeks, reminded, err)
	}
}
