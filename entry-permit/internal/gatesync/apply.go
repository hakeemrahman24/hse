package gatesync

import (
	"context"
	"database/sql"
	"fmt"

	"entry-permit/internal/dates"
)

const maxClosedReason = 60 // the ticket column

// applier writes pulled gate rows into the Pi database. Every method works inside one
// transaction and is safe to repeat: the gate hands a row out again until it is acked,
// so a lost ack must change nothing the second time.
type applier struct {
	db    *sql.DB
	clock dates.Clock
}

func (a applier) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// logRows stores scans the Pi has not seen, keyed by the Worker's row id (UNIQUE),
// and returns how many were new.
func (a applier) logRows(ctx context.Context, rows []LogRow) (int, error) {
	added := 0
	now := string(a.clock.Now())
	err := a.tx(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gate_log WHERE worker_id = ?`, r.ID).Scan(&n); err != nil {
				return fmt.Errorf("check scan %d: %w", r.ID, err)
			}
			if n > 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO gate_log (worker_id, at, lep_no, worker_name, company, action, reason, officer, session_id, day, gate, received_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				r.ID, jakartaFromISO(r.Timestamp), r.Lep, r.Name, r.Company, r.Action, r.Reason, r.GateOfficer, r.SessionID, r.DateOnly, r.GateName, now); err != nil {
				return fmt.Errorf("store scan %d: %w", r.ID, err)
			}
			added++
		}
		return nil
	})
	return added, err
}

// stateRows keeps where each worker is. The newest Worker update wins, so a late
// or repeated older row can never put someone back outside.
func (a applier) stateRows(ctx context.Context, rows []StateRow) (int, error) {
	written := 0
	now := string(a.clock.Now())
	err := a.tx(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			var have string
			err := tx.QueryRowContext(ctx, `SELECT worker_updated_at FROM gate_state WHERE lep_no = ?`, r.Lep).Scan(&have)
			switch {
			case err == sql.ErrNoRows:
				if _, err := tx.ExecContext(ctx, `INSERT INTO gate_state (lep_no, status, last_action, last_action_at, officer, gate, first_checkin, last_checkout, reason, worker_updated_at, received_at)
					VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
					r.Lep, r.CurrentStatus, r.LastAction, jakartaFromISO(r.LastActionTime), r.LastGateOfficer, r.GateName,
					jakartaFromISO(r.FirstCheckin), jakartaFromISO(r.LastCheckout), r.Reason, r.UpdatedAt, now); err != nil {
					return fmt.Errorf("store state %s: %w", r.Lep, err)
				}
			case err != nil:
				return fmt.Errorf("read state %s: %w", r.Lep, err)
			case r.UpdatedAt >= have: // same-format ISO times compare as text
				if _, err := tx.ExecContext(ctx, `UPDATE gate_state SET status = ?, last_action = ?, last_action_at = ?, officer = ?, gate = ?,
					first_checkin = ?, last_checkout = ?, reason = ?, worker_updated_at = ?, received_at = ? WHERE lep_no = ?`,
					r.CurrentStatus, r.LastAction, jakartaFromISO(r.LastActionTime), r.LastGateOfficer, r.GateName,
					jakartaFromISO(r.FirstCheckin), jakartaFromISO(r.LastCheckout), r.Reason, r.UpdatedAt, now, r.Lep); err != nil {
					return fmt.Errorf("update state %s: %w", r.Lep, err)
				}
			default:
				continue // older than what the Pi already has
			}
			written++
		}
		return nil
	})
	return written, err
}

// ticketRows applies the two things the gate owns on a ticket: the first check-in
// (record_start) and the closing. It never touches the plan (dates, rotation, lock),
// which the Pi owns. A ticket the Pi does not know is counted as skipped.
func (a applier) ticketRows(ctx context.Context, rows []TicketRow) (applied, skipped int, err error) {
	err = a.tx(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			var status, recordStart, closedAt, closedReason string
			e := tx.QueryRowContext(ctx, `SELECT status, record_start, closed_at, closed_reason FROM ticket WHERE ticket_id = ?`, r.TicketID).
				Scan(&status, &recordStart, &closedAt, &closedReason)
			if e == sql.ErrNoRows {
				skipped++
				continue
			}
			if e != nil {
				return fmt.Errorf("read ticket %s: %w", r.TicketID, e)
			}
			changed := false
			if start := jakartaFromISO(r.RecordStart); start != "" && recordStart == "" {
				if _, e := tx.ExecContext(ctx, `UPDATE ticket SET record_start = ? WHERE ticket_id = ?`, start, r.TicketID); e != nil {
					return fmt.Errorf("start ticket %s: %w", r.TicketID, e)
				}
				changed = true
			}
			// The gate's closing wins, even over a close the Pi made itself (tickets doc 5).
			if r.Status == "COMPLETED" && (status == "Approved" || status == "Closed") {
				at, reason := jakartaFromISO(r.ClosedAt), clipRunes(r.CloseReason, maxClosedReason)
				if status != "Closed" || closedAt != at || closedReason != reason {
					if _, e := tx.ExecContext(ctx, `UPDATE ticket SET status = 'Closed', closed_at = ?, closed_reason = ? WHERE ticket_id = ?`, at, reason, r.TicketID); e != nil {
						return fmt.Errorf("close ticket %s: %w", r.TicketID, e)
					}
					changed = true
				}
			}
			if changed {
				applied++
			}
		}
		return nil
	})
	return applied, skipped, err
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
