package permitlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
)

// ErrNoPermit: no permit has that request id.
var ErrNoPermit = errors.New("there is no permit with that request id")

// PhotoTargets lists the request ids of a person's permits that are still in use (waiting for
// review, Approved, or in the grace period), oldest first. A photo the person changes goes to
// all of them, because each permit keeps its own copy of the photo; Rejected, Expired, Resigned
// and Renewed permits keep the photo they were decided with.
//
// Matching is the same as Latest: the medpost patient id, or the personal id when there is one.
// A blank personal id never matches, so it cannot pick up every imported permit.
func (s Store) PhotoTargets(ctx context.Context, patientID int64, personalID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT request_id FROM permit
WHERE (patient_id = ? OR (? <> '' AND personal_id = ?))
  AND status IN ('PendingStage1', 'PendingFinal', 'Approved', 'Expired in grace period')
ORDER BY created_at, request_id`, patientID, personalID, personalID)
	if err != nil {
		return nil, fmt.Errorf("read photo targets: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read photo target: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetIDPhoto stores the new photo path on a permit and returns the path it replaced (for the
// audit log). The read and the write share one transaction, so the logged old value is the one
// that was really replaced.
func (s Store) SetIDPhoto(ctx context.Context, requestID, path string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("set photo: %w", err)
	}
	defer tx.Rollback()
	var old string
	if err := tx.QueryRowContext(ctx, `SELECT id_photo FROM permit WHERE request_id = ?`, requestID).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNoPermit
		}
		return "", fmt.Errorf("read photo: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE permit SET id_photo = ? WHERE request_id = ?`, path, requestID); err != nil {
		return "", fmt.Errorf("set photo: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("set photo: %w", err)
	}
	return old, nil
}

// PersonPermit is one of a worker's permits as the worker sees it: the LEP number (blank while
// the permit waits for review), the status and the last valid day. No request id, so nothing on
// the page can be used to look a request up.
type PersonPermit struct {
	LEP, Status string
	ValidUntil  string
	IDPhoto     string // stored path of the permit's photo, blank when none; never shown as text
}

// ForPerson lists a worker's permits, newest first, at most limit. Matching is the same as
// Latest and PhotoTargets.
func (s Store) ForPerson(ctx context.Context, patientID int64, personalID string, limit int) ([]PersonPermit, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT lep_no, status, lep_valid_until, id_photo FROM permit
WHERE patient_id = ? OR (? <> '' AND personal_id = ?)
ORDER BY created_at DESC, request_id DESC
LIMIT ?`, patientID, personalID, personalID, limit)
	if err != nil {
		return nil, fmt.Errorf("read permits of a person: %w", err)
	}
	defer rows.Close()
	var out []PersonPermit
	for rows.Next() {
		var p PersonPermit
		if err := rows.Scan(&p.LEP, &p.Status, &p.ValidUntil, &p.IDPhoto); err != nil {
			return nil, fmt.Errorf("read permit of a person: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OwnedCard is a card plus the stored path of the worker's photo, for the worker's own download.
type OwnedCard struct {
	CardInfo
	IDPhoto string
}

// OwnedCard finds a permit by LEP number only if it belongs to the person (same matching as
// Latest). Someone else's number, an unknown number and a blank number all give ok = false, so
// the caller cannot tell them apart.
func (s Store) OwnedCard(ctx context.Context, patientID int64, personalID, lep string) (OwnedCard, bool, error) {
	lep = strings.ToUpper(strings.TrimSpace(lep))
	if lep == "" {
		return OwnedCard{}, false, nil
	}
	var c OwnedCard
	var valid string
	err := s.DB.QueryRowContext(ctx, `
SELECT lep_no, status, worker_name, company, entity, parents, work_position, lep_valid_until, id_photo
FROM permit WHERE lep_no = ? AND (patient_id = ? OR (? <> '' AND personal_id = ?))`, lep, patientID, personalID, personalID).
		Scan(&c.LEP, &c.Status, &c.WorkerName, &c.Company, &c.Entity, &c.Parents, &c.Position, &valid, &c.IDPhoto)
	if errors.Is(err, sql.ErrNoRows) {
		return OwnedCard{}, false, nil
	}
	if err != nil {
		return OwnedCard{}, false, fmt.Errorf("read own card: %w", err)
	}
	c.ValidUntil = dates.Day(valid)
	return c, true, nil
}
