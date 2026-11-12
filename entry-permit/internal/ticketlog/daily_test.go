package ticketlog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"entry-permit/internal/ticket"
)

func addPermit(t *testing.T, s Store, lep, status, nationality string) {
	t.Helper()
	_, err := s.DB.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, nationality, requester_name, requester_email,
		work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES ('WEP-' || ?, ?, ?, 1, 'Budi Santoso', 'PT Maju', ?, 'Rina', 'rina@example.com', 'Onshore', '2027-03-31', '2027-03-17', '2026-06-01 08:00:00')`,
		lep[4:], lep, status, nationality)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDailyTicketsAreTheApprovedWorkTicketsWithTheirPermit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addPermit(t, s, "LEP-11111", "Approved", "Indonesia")
	addPermit(t, s, "LEP-22222", "Expired in grace period", "Australia")
	for _, n := range []NewTicket{sample("TKT-AAAAA", "LEP-11111"), sample("TKT-BBBBB", "LEP-22222")} {
		if err := s.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	closed := sample("TKT-CCCCC", "LEP-11111")
	closed.Status = "Closed"
	pending := sample("TKT-DDDDD", "LEP-11111")
	pending.Status = "PendingDoctor"
	waiver := sample("TKT-EEEEE", "WVR-33333")
	waiver.Work = false
	for _, n := range []NewTicket{closed, pending, waiver} {
		if err := s.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.DailyTickets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want the 2 approved work tickets", len(rows))
	}
	a, b := rows[0], rows[1]
	if a.Ticket.ID != "TKT-AAAAA" || a.PermitStatus != "Approved" || a.PermitValidTo != "2027-03-17" || a.Nationality != "Indonesia" ||
		a.Name != "Budi Santoso" || a.Company != "PT Maju" || a.Requester != "rina@example.com" {
		t.Errorf("first row = %+v", a)
	}
	if b.Ticket.ID != "TKT-BBBBB" || b.PermitStatus != "Expired in grace period" || b.Nationality != "Australia" {
		t.Errorf("second row = %+v", b)
	}
	if a.Ticket.Departure != "2027-01-25" || !a.Ticket.Work || a.Ticket.Status != ticket.StatusApproved {
		t.Errorf("the ticket is not in rule shape: %+v", a.Ticket)
	}
}

func TestApplyDailyWritesTheJobsFieldsAndRunsTheExtraWorkInTheSameTransaction(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addPermit(t, s, "LEP-11111", "Approved", "Indonesia")
	if err := s.Create(ctx, sample("TKT-AAAAA", "LEP-11111")); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.DailyTickets(ctx)
	before := rows[0].Ticket
	after := before
	after.LepLocked, after.WeeksOnSite, after.ReminderNotified, after.FatigueFlag = true, 9, true, true
	after.WeekN1SentAt, after.WeekN0LockedAt = "2027-01-20 06:00:00", "2027-01-27 06:00:00"

	ran := false
	ok, err := s.ApplyDaily(ctx, before, after, func(tx *sql.Tx) error {
		ran = true
		_, e := tx.ExecContext(ctx, `INSERT INTO email_outbox (queued_at, status, kind, to_addrs, subject, text_body, html_body, next_attempt)
			VALUES ('2027-01-20 06:00:00', 'Pending', 'ticket-reminder', 'rina@example.com', 's', 't', 'h', '2027-01-20 06:00:00')`)
		return e
	})
	if err != nil || !ok || !ran {
		t.Fatalf("ApplyDaily: ok=%v ran=%v err=%v", ok, ran, err)
	}
	got, _ := s.DailyTickets(ctx)
	g := got[0].Ticket
	if !g.LepLocked || g.WeeksOnSite != 9 || !g.ReminderNotified || !g.FatigueFlag || g.WeekN1SentAt != "2027-01-20 06:00:00" || g.WeekN0LockedAt != "2027-01-27 06:00:00" {
		t.Errorf("stored = %+v", g)
	}
	var mails int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM email_outbox`).Scan(&mails)
	if mails != 1 {
		t.Errorf("the extra work left %d mails", mails)
	}
}

func TestApplyDailyCanCloseATicket(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addPermit(t, s, "LEP-11111", "Approved", "Indonesia")
	_ = s.Create(ctx, sample("TKT-AAAAA", "LEP-11111"))
	rows, _ := s.DailyTickets(ctx)
	after := rows[0].Ticket
	after.Status, after.ClosedAt, after.ClosedReason = ticket.StatusClosed, "2027-01-26 06:00:00", ticket.ClosedPlannedPast
	if ok, err := s.ApplyDaily(ctx, rows[0].Ticket, after, nil); err != nil || !ok {
		t.Fatalf("close: ok=%v err=%v", ok, err)
	}
	var status, at, reason string
	_ = s.DB.QueryRow(`SELECT status, closed_at, closed_reason FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&status, &at, &reason)
	if status != "Closed" || at != "2027-01-26 06:00:00" || reason != ticket.ClosedPlannedPast {
		t.Errorf("stored %q %q %q", status, at, reason)
	}
}

func TestApplyDailyDoesNothingWhenTheTicketChangedUnderIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addPermit(t, s, "LEP-11111", "Approved", "Indonesia")
	_ = s.Create(ctx, sample("TKT-AAAAA", "LEP-11111"))
	rows, _ := s.DailyTickets(ctx)
	before := rows[0].Ticket
	// the gate sync closes the ticket after the job read it
	if _, err := s.DB.Exec(`UPDATE ticket SET status = 'Closed' WHERE ticket_id = 'TKT-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	after := before
	after.ReminderNotified = true
	ran := false
	ok, err := s.ApplyDaily(ctx, before, after, func(*sql.Tx) error { ran = true; return nil })
	if err != nil || ok || ran {
		t.Fatalf("a stale write was applied: ok=%v ran=%v err=%v", ok, ran, err)
	}
	// and a second run of the same update (a flag the other run already set) is refused too
	_, _ = s.DB.Exec(`UPDATE ticket SET status = 'Approved', reminder_notified = 1 WHERE ticket_id = 'TKT-AAAAA'`)
	if ok, _ := s.ApplyDaily(ctx, before, after, func(*sql.Tx) error { ran = true; return nil }); ok || ran {
		t.Error("a reminder flag set by someone else was overwritten, so its mail would be queued twice")
	}
}

func TestApplyDailyRollsEverythingBackWhenTheExtraWorkFails(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addPermit(t, s, "LEP-11111", "Approved", "Indonesia")
	_ = s.Create(ctx, sample("TKT-AAAAA", "LEP-11111"))
	rows, _ := s.DailyTickets(ctx)
	after := rows[0].Ticket
	after.ReminderNotified = true
	ok, err := s.ApplyDaily(ctx, rows[0].Ticket, after, func(*sql.Tx) error { return errors.New("mail table gone") })
	if err == nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got, _ := s.DailyTickets(ctx)
	if got[0].Ticket.ReminderNotified {
		t.Error("the reminder flag stayed set although its mail was never queued")
	}
}

func TestADatabaseErrorIsReportedNotSwallowed(t *testing.T) {
	s := newStore(t)
	s.DB.Close()
	if _, err := s.DailyTickets(context.Background()); err == nil {
		t.Error("DailyTickets hid a database error")
	}
	if _, err := s.ApplyDaily(context.Background(), ticket.Ticket{}, ticket.Ticket{}, nil); err == nil {
		t.Error("ApplyDaily hid a database error")
	}
}
