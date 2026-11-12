package reviewlog

import (
	"context"
	"fmt"

	"entry-permit/internal/permit"
	"entry-permit/internal/waiver"
)

// Waiting is one request that is waiting for a decision at a review stage.
type Waiting struct {
	Stage                   string // stage1, doctor or final
	ID                      string // WEP- or WVR- request id
	Name, Company, Position string
	Since                   string // the moment it reached this stage, "yyyy-mm-dd hh:mm:ss"
}

// Waiting lists every request waiting at a stage, with the moment it got there. The
// reminder job turns Since into "hours waiting".
//
// WHY the moment is different per stage: the clock for a reviewer starts when the
// request reached them, not when it was first submitted. A work permit reaches Final
// when Stage 1 approved it; a waiver reaches Stage 1 when the Doctor approved it, and
// Final when Stage 1 did. If the earlier decision was not recorded (data from before
// the review log existed), the submit time is used, so the request still shows.
func (s Store) Waiting(ctx context.Context) ([]Waiting, error) {
	const since = `COALESCE((SELECT at FROM review_step WHERE subject_id = %s AND stage = '%s'), %s)`
	q := `SELECT 'stage1', request_id, worker_name, company, work_position, created_at FROM permit WHERE status = ?
		UNION ALL
		SELECT 'final', request_id, worker_name, company, work_position, ` + fmt.Sprintf(since, "request_id", "stage1", "created_at") + ` FROM permit WHERE status = ?
		UNION ALL
		SELECT 'doctor', request_id, visitor_name, company, position, created_at FROM waiver WHERE status = ?
		UNION ALL
		SELECT 'stage1', request_id, visitor_name, company, position, ` + fmt.Sprintf(since, "request_id", "doctor", "created_at") + ` FROM waiver WHERE status = ?
		UNION ALL
		SELECT 'final', request_id, visitor_name, company, position, ` + fmt.Sprintf(since, "request_id", "stage1", "created_at") + ` FROM waiver WHERE status = ?
		ORDER BY 1, 6, 2`
	rows, err := s.DB.QueryContext(ctx, q, permit.StatusPendingStage1, permit.StatusPendingFinal,
		waiver.StatusPendingDoctor, waiver.StatusPendingStage1, waiver.StatusPendingSiteManager)
	if err != nil {
		return nil, fmt.Errorf("list waiting requests: %w", err)
	}
	defer rows.Close()
	var out []Waiting
	for rows.Next() {
		var w Waiting
		if err := rows.Scan(&w.Stage, &w.ID, &w.Name, &w.Company, &w.Position, &w.Since); err != nil {
			return nil, fmt.Errorf("read waiting request: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
