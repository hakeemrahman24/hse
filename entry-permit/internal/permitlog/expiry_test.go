package permitlog

import (
	"context"
	"database/sql"
	"testing"
)

// permitWithLEP inserts a permit with its own LEP number and valid-until day.
func permitWithLEP(t *testing.T, d *sql.DB, id, lep, status, validUntil string) {
	t.Helper()
	insert(t, d, id, status, 1, "", "2026-06-01 08:00:00")
	if _, err := d.Exec(`UPDATE permit SET lep_no = ?, lep_valid_until = ? WHERE request_id = ?`, lep, validUntil, id); err != nil {
		t.Fatal(err)
	}
}

func addTicket(t *testing.T, d *sql.DB, id, lep, status, closedAt string) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, rotation_no, closed_at, submitted_at)
		VALUES (?, ?, 1, ?, '2026-12-01', '2027-01-25', 'Onshore', 1, ?, '2026-11-01 08:00:00')`, id, lep, status, closedAt); err != nil {
		t.Fatal(err)
	}
}

func locked(t *testing.T, d *sql.DB, ticket string) bool {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT lep_locked FROM ticket WHERE ticket_id = ?`, ticket).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestExpiryCandidatesAreApprovedAndExpiredLikePermitsWithAnLEP(t *testing.T) {
	s, d := newStore(t)
	permitWithLEP(t, d, "WEP-00001", "LEP-AAAA1", "Approved", "2027-01-17")
	permitWithLEP(t, d, "WEP-00002", "LEP-AAAA2", "Expired in grace period", "2026-11-01")
	permitWithLEP(t, d, "WEP-00003", "LEP-AAAA3", "Expired", "2026-01-01")
	permitWithLEP(t, d, "WEP-00004", "LEP-AAAA4", "Resigned", "2027-01-17")
	permitWithLEP(t, d, "WEP-00005", "LEP-AAAA5", "Renewed by LEP-AAAA1", "2026-01-01")
	permitWithLEP(t, d, "WEP-00006", "", "PendingFinal", "")

	got, err := s.ExpiryCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	if got[0].LEP != "LEP-AAAA1" || got[0].Status != "Approved" || got[0].ValidUntil != "2027-01-17" || got[0].ID != "WEP-00001" {
		t.Errorf("first = %+v", got[0])
	}
}

func TestApplyExpiryChangesTheStatusAndLocksOnlyThatPermitsOpenTickets(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	permitWithLEP(t, d, "WEP-00001", "LEP-AAAA1", "Approved", "2026-11-01")
	permitWithLEP(t, d, "WEP-00002", "LEP-AAAA2", "Approved", "2027-06-01")
	addTicket(t, d, "TKT-00001", "LEP-AAAA1", "Approved", "")
	addTicket(t, d, "TKT-00002", "LEP-AAAA1", "PendingDoctor", "")
	addTicket(t, d, "TKT-00003", "LEP-AAAA1", "Closed", "2026-10-01 06:00:00")
	addTicket(t, d, "TKT-00004", "LEP-AAAA2", "Approved", "")

	c := ExpiryCandidate{ID: "WEP-00001", LEP: "LEP-AAAA1", Status: "Approved"}
	ok, err := s.ApplyExpiry(ctx, c, "Expired in grace period", true)
	if err != nil || !ok {
		t.Fatalf("ApplyExpiry: ok=%v err=%v", ok, err)
	}
	var status string
	_ = d.QueryRow(`SELECT status FROM permit WHERE request_id = 'WEP-00001'`).Scan(&status)
	if status != "Expired in grace period" {
		t.Errorf("status = %q", status)
	}
	if !locked(t, d, "TKT-00001") || !locked(t, d, "TKT-00002") {
		t.Error("the open tickets of the expired permit were not locked")
	}
	if locked(t, d, "TKT-00003") {
		t.Error("a closed ticket was locked")
	}
	if locked(t, d, "TKT-00004") {
		t.Error("another permit's ticket was locked")
	}
}

func TestApplyExpiryFromGraceToExpiredLeavesTicketsAlone(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	permitWithLEP(t, d, "WEP-00001", "LEP-AAAA1", "Expired in grace period", "2026-01-01")
	addTicket(t, d, "TKT-00001", "LEP-AAAA1", "Approved", "")
	ok, err := s.ApplyExpiry(ctx, ExpiryCandidate{ID: "WEP-00001", LEP: "LEP-AAAA1", Status: "Expired in grace period"}, "Expired", false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if locked(t, d, "TKT-00001") {
		t.Error("a ticket was locked by a change that does not lock")
	}
}

func TestApplyExpiryDoesNothingWhenThePermitChangedUnderIt(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	permitWithLEP(t, d, "WEP-00001", "LEP-AAAA1", "Approved", "2026-11-01")
	addTicket(t, d, "TKT-00001", "LEP-AAAA1", "Approved", "")
	// someone marks the permit Resigned after the job read it
	if _, err := d.Exec(`UPDATE permit SET status = 'Resigned' WHERE request_id = 'WEP-00001'`); err != nil {
		t.Fatal(err)
	}
	ok, err := s.ApplyExpiry(ctx, ExpiryCandidate{ID: "WEP-00001", LEP: "LEP-AAAA1", Status: "Approved"}, "Expired in grace period", true)
	if err != nil || ok {
		t.Fatalf("a stale expiry was applied: ok=%v err=%v", ok, err)
	}
	var status string
	_ = d.QueryRow(`SELECT status FROM permit WHERE request_id = 'WEP-00001'`).Scan(&status)
	if status != "Resigned" || locked(t, d, "TKT-00001") {
		t.Errorf("status %q, ticket locked %v", status, locked(t, d, "TKT-00001"))
	}
}

func TestADatabaseErrorIsReportedNotSwallowedByTheExpiryReads(t *testing.T) {
	s, d := newStore(t)
	d.Close()
	if _, err := s.ExpiryCandidates(context.Background()); err == nil {
		t.Error("ExpiryCandidates hid a database error")
	}
	if _, err := s.ApplyExpiry(context.Background(), ExpiryCandidate{ID: "X"}, "Expired", true); err == nil {
		t.Error("ApplyExpiry hid a database error")
	}
}
