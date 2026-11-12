package waiverlog

import (
	"context"
	"testing"

	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
)

// visit stores a waiver in a status, with its visit ending on `to`, and one ticket.
func visit(t *testing.T, s Store, id, status, to, ticketStatus string) {
	t.Helper()
	w := sample(id)
	w.Status, w.VisitTo = status, to
	tk := ticketlog.NewTicket{ID: "TKT-" + id[4:], LEP: id, Work: false, Status: ticketStatus,
		Arrival: "2026-11-10", Departure: to, Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-06 08:00:00"}
	if err := s.CreateWithTicket(context.Background(), w, tk); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, s Store, q string, arg string) string {
	t.Helper()
	var v string
	if err := s.DB.QueryRow(q, arg).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEndedVisitsAreOpenWaiversWhoseVisitEndedBeforeToday(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	visit(t, s, "WVR-00001", waiver.StatusApproved, "2026-11-09", "Approved")           // ended
	visit(t, s, "WVR-00002", waiver.StatusPendingDoctor, "2026-11-09", "PendingDoctor") // ended, never approved
	visit(t, s, "WVR-00003", waiver.StatusApproved, "2026-11-10", "Approved")           // ends today: not yet
	visit(t, s, "WVR-00004", waiver.StatusCompleted, "2026-11-01", "Closed")            // already done
	visit(t, s, "WVR-00005", waiver.StatusRejected, "2026-11-01", "Rejected")           // never open

	got, err := s.EndedVisits(ctx, "2026-11-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "WVR-00001" || got[1].ID != "WVR-00002" {
		t.Fatalf("ended visits = %+v", got)
	}
	if got[0].Status != "Approved" || got[0].To != "2026-11-09" || len(got[0].Tickets) != 1 || got[0].Tickets[0].ID != "TKT-00001" || got[0].Tickets[0].Status != "Approved" {
		t.Errorf("first = %+v", got[0])
	}
}

func TestApplyVisitEndCompletesTheWaiverAndClosesItsTicket(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	visit(t, s, "WVR-00001", waiver.StatusApproved, "2026-11-09", "Approved")
	v := Visit{ID: "WVR-00001", Status: "Approved", Tickets: []VisitTicket{{ID: "TKT-00001", Status: "Approved"}}}
	ok, err := s.ApplyVisitEnd(ctx, v, "Completed", []TicketChange{{ID: "TKT-00001", From: "Approved", To: "Closed", Reason: "Planned departure passed"}}, "2026-11-10 06:00:00")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := status(t, s, `SELECT status FROM waiver WHERE request_id = ?`, "WVR-00001"); got != "Completed" {
		t.Errorf("waiver status %q", got)
	}
	var st, at, reason string
	_ = s.DB.QueryRow(`SELECT status, closed_at, closed_reason FROM ticket WHERE ticket_id = 'TKT-00001'`).Scan(&st, &at, &reason)
	if st != "Closed" || at != "2026-11-10 06:00:00" || reason != "Planned departure passed" {
		t.Errorf("ticket %q %q %q", st, at, reason)
	}
}

func TestApplyVisitEndCancelsAWaiverThatWasNeverApproved(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	visit(t, s, "WVR-00002", waiver.StatusPendingDoctor, "2026-11-09", "PendingDoctor")
	v := Visit{ID: "WVR-00002", Status: "PendingDoctor", Tickets: []VisitTicket{{ID: "TKT-00002", Status: "PendingDoctor"}}}
	ok, err := s.ApplyVisitEnd(ctx, v, "Cancelled", []TicketChange{{ID: "TKT-00002", From: "PendingDoctor", To: "Cancelled", Reason: "Visit window ended before approval"}}, "2026-11-10 06:00:00")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := status(t, s, `SELECT status FROM waiver WHERE request_id = ?`, "WVR-00002"); got != "Cancelled" {
		t.Errorf("waiver status %q", got)
	}
	if got := status(t, s, `SELECT closed_reason FROM ticket WHERE ticket_id = ?`, "TKT-00002"); got != "Visit window ended before approval" {
		t.Errorf("ticket reason %q", got)
	}
}

func TestApplyVisitEndDoesNothingWhenTheWaiverChangedUnderIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	visit(t, s, "WVR-00001", waiver.StatusApproved, "2026-11-09", "Approved")
	if _, err := s.DB.Exec(`UPDATE waiver SET status = 'Cancelled' WHERE request_id = 'WVR-00001'`); err != nil {
		t.Fatal(err)
	}
	v := Visit{ID: "WVR-00001", Status: "Approved"}
	ok, err := s.ApplyVisitEnd(ctx, v, "Completed", []TicketChange{{ID: "TKT-00001", From: "Approved", To: "Closed", Reason: "x"}}, "2026-11-10 06:00:00")
	if err != nil || ok {
		t.Fatalf("a stale update was applied: ok=%v err=%v", ok, err)
	}
	if got := status(t, s, `SELECT status FROM ticket WHERE ticket_id = ?`, "TKT-00001"); got != "Approved" {
		t.Errorf("the ticket was closed although the waiver update was refused: %q", got)
	}
}

func TestApplyVisitEndLeavesATicketTheGateAlreadyClosed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	visit(t, s, "WVR-00001", waiver.StatusApproved, "2026-11-09", "Approved")
	if _, err := s.DB.Exec(`UPDATE ticket SET status = 'Closed', closed_at = '2026-11-09 17:00:00', closed_reason = 'Selesai awal' WHERE ticket_id = 'TKT-00001'`); err != nil {
		t.Fatal(err)
	}
	v := Visit{ID: "WVR-00001", Status: "Approved"}
	ok, err := s.ApplyVisitEnd(ctx, v, "Completed", []TicketChange{{ID: "TKT-00001", From: "Approved", To: "Closed", Reason: "Planned departure passed"}}, "2026-11-10 06:00:00")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := status(t, s, `SELECT closed_reason FROM ticket WHERE ticket_id = ?`, "TKT-00001"); got != "Selesai awal" {
		t.Errorf("the gate's own closing was overwritten: %q", got)
	}
}

func TestADatabaseErrorIsReportedNotSwallowedByTheDepartureReads(t *testing.T) {
	s := newStore(t)
	s.DB.Close()
	if _, err := s.EndedVisits(context.Background(), "2026-11-10"); err == nil {
		t.Error("EndedVisits hid a database error")
	}
	if _, err := s.ApplyVisitEnd(context.Background(), Visit{ID: "X"}, "Completed", nil, "x"); err == nil {
		t.Error("ApplyVisitEnd hid a database error")
	}
}
