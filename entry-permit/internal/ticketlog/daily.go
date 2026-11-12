package ticketlog

import (
	"context"
	"database/sql"
	"fmt"

	"entry-permit/internal/dates"
	"entry-permit/internal/ticket"
)

// DailyRow is an Approved work ticket with what the daily departure job needs from
// its permit: the permit's state (for the expiry lock), the worker's nationality
// (for the rotation cap) and who to write to.
type DailyRow struct {
	Ticket        ticket.Ticket
	PermitStatus  string
	PermitValidTo dates.Day
	Nationality   string
	Name, Company string
	Requester     string // the e-mail address reminders go to
}

// DailyTickets reads every Approved work ticket together with its permit. The cursor
// is closed before it returns, so the job never holds a connection while it works.
// A ticket whose permit is missing is left out: there is nothing to check it against.
func (s Store) DailyTickets(ctx context.Context) ([]DailyRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT t.ticket_id, t.lep_no, t.status, t.arrival, t.departure, t.location, t.pattern,
       t.record_start, t.closed_at, t.closed_reason,
       t.lep_locked, t.weeks_on_site, t.reminder_notified, t.fatigue_flag, t.week_n1_sent_at, t.week_n0_locked_at,
       t.requester_email, p.status, p.lep_valid_until, p.nationality, p.worker_name, p.company
FROM ticket t JOIN permit p ON p.lep_no = t.lep_no
WHERE t.is_work = 1 AND t.status = ?
ORDER BY t.ticket_id`, string(ticket.StatusApproved))
	if err != nil {
		return nil, fmt.Errorf("read daily tickets: %w", err)
	}
	defer rows.Close()
	var out []DailyRow
	for rows.Next() {
		var r DailyRow
		var status, arrival, departure, start, closed, n1, n0, validTo string
		var locked, reminded, fatigue int
		t := &r.Ticket
		if err := rows.Scan(&t.ID, &t.LEP, &status, &arrival, &departure, &t.Location, &t.Pattern, &start, &closed, &t.ClosedReason,
			&locked, &t.WeeksOnSite, &reminded, &fatigue, &n1, &n0,
			&r.Requester, &r.PermitStatus, &validTo, &r.Nationality, &r.Name, &r.Company); err != nil {
			return nil, fmt.Errorf("scan daily ticket: %w", err)
		}
		t.Work, t.Status = true, ticket.NormalizeStatus(status)
		t.Arrival, t.Departure = dates.Day(arrival), dates.Day(departure)
		t.RecordStart, t.ClosedAt = dates.Moment(start), dates.Moment(closed)
		t.LepLocked, t.ReminderNotified, t.FatigueFlag = locked == 1, reminded == 1, fatigue == 1
		t.WeekN1SentAt, t.WeekN0LockedAt = dates.Moment(n1), dates.Moment(n0)
		r.PermitValidTo = dates.Day(validTo)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApplyDaily stores what the daily job decided for one ticket, and runs extra (queue
// the reminder mail) in the same transaction, so a flag is never set without its
// mail and a mail is never queued without its flag.
//
// WHY the update names every old value: it only applies if the ticket is still exactly
// as the job read it. If the gate sync closed it meanwhile, or another run already set
// the reminder flag, nothing is written and extra does not run, so a mail cannot be
// queued twice. It returns false then, and the caller moves on.
func (s Store) ApplyDaily(ctx context.Context, before, after ticket.Ticket, extra func(tx *sql.Tx) error) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin daily update: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	res, err := tx.ExecContext(ctx, `
UPDATE ticket SET status = ?, closed_at = ?, closed_reason = ?, lep_locked = ?, weeks_on_site = ?,
       reminder_notified = ?, fatigue_flag = ?, week_n1_sent_at = ?, week_n0_locked_at = ?
WHERE ticket_id = ? AND status = ? AND closed_at = ? AND lep_locked = ? AND weeks_on_site = ?
  AND reminder_notified = ? AND fatigue_flag = ? AND week_n1_sent_at = ? AND week_n0_locked_at = ?`,
		string(after.Status), string(after.ClosedAt), after.ClosedReason, b2i(after.LepLocked), after.WeeksOnSite,
		b2i(after.ReminderNotified), b2i(after.FatigueFlag), string(after.WeekN1SentAt), string(after.WeekN0LockedAt),
		before.ID, string(before.Status), string(before.ClosedAt), b2i(before.LepLocked), before.WeeksOnSite,
		b2i(before.ReminderNotified), b2i(before.FatigueFlag), string(before.WeekN1SentAt), string(before.WeekN0LockedAt))
	if err != nil {
		return false, fmt.Errorf("update ticket %s: %w", before.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("update ticket %s: %w", before.ID, err)
	} else if n == 0 {
		return false, nil
	}
	if extra != nil {
		if err := extra(tx); err != nil {
			return false, fmt.Errorf("ticket %s: %w", before.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit ticket %s: %w", before.ID, err)
	}
	return true, nil
}
