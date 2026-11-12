package waiverlog

import (
	"context"
	"fmt"

	"entry-permit/internal/dates"
	"entry-permit/internal/waiver"
)

// VisitTicket is one open ticket of a waiver.
type VisitTicket struct{ ID, Status string }

// Visit is a waiver whose visit has ended but which is still open.
type Visit struct {
	ID, Status string
	To         dates.Day
	Tickets    []VisitTicket
}

// TicketChange is one ticket move the job decided on.
type TicketChange struct{ ID, From, To, Reason string }

// EndedVisits lists the waivers still open (approved or waiting for a review) whose
// to-date is before today, each with its open tickets. A visit that ends today is not
// over yet. Both reads finish before it returns, so no cursor stays open.
func (s Store) EndedVisits(ctx context.Context, today dates.Day) ([]Visit, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT request_id, status, visit_to FROM waiver
		WHERE status IN (?, ?, ?, ?) AND visit_to < ? ORDER BY request_id`,
		waiver.StatusApproved, waiver.StatusPendingDoctor, waiver.StatusPendingStage1, waiver.StatusPendingSiteManager, string(today))
	if err != nil {
		return nil, fmt.Errorf("read ended visits: %w", err)
	}
	var out []Visit
	for rows.Next() {
		var v Visit
		var to string
		if err := rows.Scan(&v.ID, &v.Status, &to); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan ended visit: %w", err)
		}
		v.To = dates.Day(to)
		out = append(out, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ended visits: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}

	trows, err := s.DB.QueryContext(ctx, `SELECT lep_no, ticket_id, status FROM ticket
		WHERE is_work = 0 AND closed_at = '' AND status IN (?, ?) ORDER BY ticket_id`,
		waiver.TicketApproved, waiver.TicketPendingDoctor)
	if err != nil {
		return nil, fmt.Errorf("read waiver tickets: %w", err)
	}
	defer trows.Close()
	byWaiver := map[string][]VisitTicket{}
	for trows.Next() {
		var id string
		var t VisitTicket
		if err := trows.Scan(&id, &t.ID, &t.Status); err != nil {
			return nil, fmt.Errorf("scan waiver ticket: %w", err)
		}
		byWaiver[id] = append(byWaiver[id], t)
	}
	if err := trows.Err(); err != nil {
		return nil, fmt.Errorf("read waiver tickets: %w", err)
	}
	for i := range out {
		out[i].Tickets = byWaiver[out[i].ID]
	}
	return out, nil
}

// ApplyVisitEnd moves one waiver and its tickets in one transaction. It returns false,
// changing nothing, when the waiver is no longer in the status the job read. A ticket
// that is no longer in its old status (the gate sync closed it, with its own reason) is
// left exactly as it is: the gate's closing wins.
func (s Store) ApplyVisitEnd(ctx context.Context, v Visit, newStatus string, changes []TicketChange, now dates.Moment) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin end of visit %s: %w", v.ID, err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	res, err := tx.ExecContext(ctx, `UPDATE waiver SET status = ? WHERE request_id = ? AND status = ?`, newStatus, v.ID, v.Status)
	if err != nil {
		return false, fmt.Errorf("update waiver %s: %w", v.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("update waiver %s: %w", v.ID, err)
	} else if n == 0 {
		return false, nil
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE ticket SET status = ?, closed_at = ?, closed_reason = ? WHERE ticket_id = ? AND status = ?`,
			c.To, string(now), c.Reason, c.ID, c.From); err != nil {
			return false, fmt.Errorf("update ticket %s: %w", c.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit end of visit %s: %w", v.ID, err)
	}
	return true, nil
}
