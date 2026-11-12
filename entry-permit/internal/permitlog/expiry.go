package permitlog

import (
	"context"
	"fmt"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
)

// ExpiryCandidate is a permit the daily expiry job looks at.
type ExpiryCandidate struct {
	ID, LEP, Status string
	ValidUntil      dates.Day
}

// ExpiryCandidates lists the permits the expiry rule applies to: Approved ones and
// those already Expired or in the grace period (so the grace state can move on to
// Expired, and the legacy "Expired but still within grace" case is repaired).
// Resigned, Rejected, Renewed and waiting permits are never touched.
func (s Store) ExpiryCandidates(ctx context.Context) ([]ExpiryCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT request_id, lep_no, status, lep_valid_until FROM permit
WHERE lep_no <> '' AND status IN (?, ?, ?) ORDER BY request_id`,
		permit.StatusApproved, permit.StatusExpiredGrace, permit.StatusExpired)
	if err != nil {
		return nil, fmt.Errorf("read expiry candidates: %w", err)
	}
	defer rows.Close()
	var out []ExpiryCandidate
	for rows.Next() {
		var c ExpiryCandidate
		var valid string
		if err := rows.Scan(&c.ID, &c.LEP, &c.Status, &valid); err != nil {
			return nil, fmt.Errorf("scan expiry candidate: %w", err)
		}
		c.ValidUntil = dates.Day(valid)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApplyExpiry moves one permit to its new status and, when lockTickets is set (the
// permit has just left Approved), locks every ticket of that permit that is still open,
// all in one transaction. It returns false, changing nothing, when the permit is no
// longer in the status the job read: someone resigned or renewed it in the meantime.
func (s Store) ApplyExpiry(ctx context.Context, c ExpiryCandidate, newStatus string, lockTickets bool) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin expiry of %s: %w", c.ID, err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	res, err := tx.ExecContext(ctx, `UPDATE permit SET status = ? WHERE request_id = ? AND status = ?`, newStatus, c.ID, c.Status)
	if err != nil {
		return false, fmt.Errorf("expire %s: %w", c.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("expire %s: %w", c.ID, err)
	} else if n == 0 {
		return false, nil
	}
	if lockTickets {
		if _, err := tx.ExecContext(ctx, `UPDATE ticket SET lep_locked = 1
			WHERE lep_no = ? AND lep_locked = 0 AND closed_at = '' AND status IN ('Approved', 'PendingDoctor')`, c.LEP); err != nil {
			return false, fmt.Errorf("lock tickets of %s: %w", c.LEP, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit expiry of %s: %w", c.ID, err)
	}
	return true, nil
}
