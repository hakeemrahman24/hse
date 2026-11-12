// Package ticketlog stores and reads working schedule tickets (TKT-).
//
// WHY separate from package ticket: ticket holds the pure rotation rules and
// never touches a database. This is the small database side, the same split as
// permit and permitlog.
package ticketlog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/ticket"
)

// Store reads and writes the ticket table.
type Store struct{ DB *sql.DB }

// NewTicket is one ticket to store. For a waiver ticket Work is false.
type NewTicket struct {
	ID, LEP, Status                             string
	Work                                        bool
	Arrival, Departure, Location, Pattern       string
	RotationNo                                  int
	FatigueDue                                  string
	GapDays                                     int
	ShortBreak, RotationExtension, FatigueAgree bool
	DoctorReview, DoctorNote                    string
	McuResult                                   string
	MedAnswer, MedDetails, MedPhoto             string
	RequesterEmail, SubmittedAt                 string

	// State a ticket already has when it is imported from the old system. A new ticket leaves
	// these empty: it has not started, closed, been locked or been mailed.
	RecordStart, ClosedAt, ClosedReason string
	LepLocked, ReminderNotified         bool
	FatigueFlag                         bool
	WeeksOnSite                         int
	WeekN1SentAt, WeekN0LockedAt        string
}

// Exists reports whether a ticket id is taken (the id generator retries on true).
func (s Store) Exists(ctx context.Context, id string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket WHERE ticket_id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check ticket id: %w", err)
	}
	return n > 0, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// columns is the one place that pairs each column with its value, so the
// INSERT can never get out of step with the list (see permitlog.Create).
func (n NewTicket) columns() []struct {
	name string
	val  any
} {
	return []struct {
		name string
		val  any
	}{
		{"ticket_id", n.ID}, {"lep_no", strings.ToUpper(n.LEP)}, {"is_work", b2i(n.Work)}, {"status", n.Status},
		{"arrival", n.Arrival}, {"departure", n.Departure}, {"location", n.Location}, {"pattern", n.Pattern},
		{"rotation_no", n.RotationNo}, {"fatigue_due", n.FatigueDue}, {"gap_days", n.GapDays},
		{"short_break", b2i(n.ShortBreak)}, {"rotation_extension", b2i(n.RotationExtension)},
		{"fatigue_consent", b2i(n.FatigueAgree)}, {"doctor_review", n.DoctorReview}, {"doctor_note", n.DoctorNote},
		{"mcu_result", n.McuResult}, {"med_answer", n.MedAnswer}, {"med_details", n.MedDetails}, {"med_photo", n.MedPhoto},
		{"requester_email", n.RequesterEmail}, {"submitted_at", n.SubmittedAt},
		{"record_start", n.RecordStart}, {"closed_at", n.ClosedAt}, {"closed_reason", n.ClosedReason},
		{"lep_locked", b2i(n.LepLocked)}, {"weeks_on_site", n.WeeksOnSite}, {"reminder_notified", b2i(n.ReminderNotified)},
		{"fatigue_flag", b2i(n.FatigueFlag)}, {"week_n1_sent_at", n.WeekN1SentAt}, {"week_n0_locked_at", n.WeekN0LockedAt},
	}
}

// Create stores a ticket. The primary key is the last guard against two
// requests drawing the same id at once.
func (s Store) Create(ctx context.Context, n NewTicket) error {
	return Insert(ctx, s.DB, n)
}

// Execer is what Insert needs: a *sql.DB or a *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Insert writes a ticket through db or an open transaction, so a caller (the
// waiver submit) can store a ticket and its waiver together or not at all.
func Insert(ctx context.Context, db Execer, n NewTicket) error {
	cols := n.columns()
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		names[i], marks[i], args[i] = c.name, "?", c.val
	}
	q := "INSERT INTO ticket (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

// ForLEP returns every ticket of a permit (LEP-... or WVR-..., any case),
// oldest arrival first, in the shape the rotation rules take.
func (s Store) ForLEP(ctx context.Context, lep string) ([]ticket.Ticket, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT ticket_id, lep_no, is_work, status, arrival, departure, location, pattern,
       record_start, closed_at, closed_reason
FROM ticket WHERE lep_no = ? ORDER BY arrival, ticket_id`, strings.ToUpper(strings.TrimSpace(lep)))
	if err != nil {
		return nil, fmt.Errorf("read tickets: %w", err)
	}
	defer rows.Close()
	var out []ticket.Ticket
	for rows.Next() {
		var t ticket.Ticket
		var work int
		var status, arrival, departure, start, closed string
		if err := rows.Scan(&t.ID, &t.LEP, &work, &status, &arrival, &departure, &t.Location, &t.Pattern,
			&start, &closed, &t.ClosedReason); err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}
		t.Work, t.Status = work == 1, ticket.NormalizeStatus(status)
		t.Arrival, t.Departure = dates.Day(arrival), dates.Day(departure)
		t.RecordStart, t.ClosedAt = dates.Moment(start), dates.Moment(closed)
		out = append(out, t)
	}
	return out, rows.Err()
}
