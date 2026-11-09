package mcusubmit

import (
	"context"
	"fmt"
	"time"
)

// Why a worker cannot be submitted right now (owner, 2026-11-06: block both, with the renewal
// window open). Both cases would only create a second record for the same checkup.
const (
	BlockWaiting = "waiting" // a document for this worker is already on its way to a result
	BlockValid   = "valid"   // the worker's current checkup is valid for longer than the renewal window
)

// Block is the answer of WorkerBlock. The zero value means "go ahead".
type Block struct {
	Kind  string // "", BlockWaiting or BlockValid
	Last  string // BlockValid: the day the current checkup was done (YYYY-MM-DD)
	Until string // BlockValid: the day it lapses
	From  string // BlockValid: the first day a new one is accepted
}

// Message is the sentence shown to the submitter: it names the last checkup, when it lapses and the
// first day a new one is accepted, so nobody has to ask why. The pattern in lang_portal.go matches
// the valid wording, so keep the two in step.
func (b Block) Message() string {
	switch b.Kind {
	case BlockWaiting:
		return "This worker already has a document waiting for the medical team. Wait for its result before sending another."
	case BlockValid:
		return fmt.Sprintf("This worker already has a valid medical checkup (done on %s, valid until %s). A new checkup is accepted only more than 180 days after the last one, from %s.", b.Last, b.Until, b.From)
	}
	return ""
}

// minDaysBetweenCheckups is how long after a checkup the next one for the same worker is accepted
// (owner, 2026-11-06: only when the new checkup date is more than 180 days after the previous).
// The portal cannot read the date off the PDF, so the day of the upload stands in for the new
// checkup date, which is when the checkup was in practice done.
const minDaysBetweenCheckups = 180

// WorkerBlock decides whether submitterID may send a document for patientID now.
//
// Waiting: a document for the patient is Pending or Reviewed (sent, not closed yet), or is the
// submitter's own Draft or Review (already in their open batch). Another company's unsent draft
// does not count: it is invisible to this submitter and may never be sent.
//
// Valid: the patient has a final Fit or Fit-with-note record that is not archived, is still valid
// today, and is 180 days old or less (its checkup date, else its review date). These are the
// records the staff app would issue a certificate for; unfit or temporarily unfit results never
// block, because those workers must be checked again. A checkup that lapses before the 180 days
// are over (a short project) stops blocking on the day after it lapses, since a lapsed checkup
// is no longer valid. When several records block, the latest day wins.
//
// Dates are YYYY-MM-DD text in both databases, so comparing them as strings is a date comparison.
func (s *Store) WorkerBlock(ctx context.Context, submitterID, patientID int64) (Block, error) {
	var waiting int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM McuSubmission
		 WHERE FK_PatientID = ? AND (Status IN (?, ?) OR (FK_SubmitterID = ? AND Status IN (?, ?)))`,
		patientID, SubPending, SubReviewed, submitterID, SubDraft, SubReview).Scan(&waiting); err != nil {
		return Block{}, err
	}
	if waiting > 0 {
		return Block{Kind: BlockWaiting}, nil
	}

	const day = "2006-01-02"
	today := s.clinicToday().Format(day)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ResultValidUntil, COALESCE(NULLIF(DateCheckUp, ''), ReviewDate) FROM McuRecord
		 WHERE FK_PatientID = ? AND IsTerminal = 1 AND ResultCriteria IN ('Fit', 'FitWithNote')
		   AND ResultValidUntil IS NOT NULL AND ResultValidUntil >= ?
		   AND (CheckUpStatus IS NULL OR CheckUpStatus <> 'Archived / Exit')`,
		patientID, today)
	if err != nil {
		return Block{}, err
	}
	defer rows.Close()
	var out Block
	for rows.Next() {
		var until, done string
		if err := rows.Scan(&until, &done); err != nil {
			return Block{}, err
		}
		doneAt, err1 := time.Parse(day, done)
		untilAt, err2 := time.Parse(day, until)
		if err1 != nil || err2 != nil {
			continue // a record with an unreadable date cannot be reasoned about: do not block on it
		}
		from := doneAt.AddDate(0, 0, minDaysBetweenCheckups+1) // "more than 180 days": day 181 is the first
		if lapse := untilAt.AddDate(0, 0, 1); lapse.Before(from) {
			from = lapse
		}
		if f := from.Format(day); f > today && f > out.From {
			out = Block{Kind: BlockValid, Last: done, Until: until, From: f}
		}
	}
	return out, rows.Err()
}
