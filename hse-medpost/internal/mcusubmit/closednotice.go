package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
)

// The "your checkups are done" mail. Staff close documents one at a time all day, so a mail per
// document would bury a busy submitter. Instead the daily job (08:00 Jakarta, with the expiry
// reminders) sends each submitter ONE mail listing everything that closed since their last one
// (owner, 2026-10-29: "inside one day"). It names the worker but never the result, an ID number or
// a birth date: the mail travels over the open internet, the portal is behind a login.

// closedNoticeMaxLines keeps one mail readable; the rest is summarised as "and N more".
const closedNoticeMaxLines = 100

// EnsureNoticeColumn adds McuSubmission.CertNotifiedAt to an older SQLite database. New databases
// get it from migrations/sqlite/017; MariaDB gets it from migrations/mysql/035, run by hand. When
// the column is new, documents that are already Closed are marked as told, so switching this on
// does not mail everybody about the past.
func EnsureNoticeColumn(database *sql.DB) error {
	added, err := addSubmissionColumn(database, "CertNotifiedAt", "TEXT NULL")
	if err != nil || !added {
		return err
	}
	_, err = database.Exec(`UPDATE McuSubmission SET CertNotifiedAt = COALESCE(ReviewedAt, SubmittedAt) WHERE Status = ?`, SubClosed)
	return err
}

type closedLine struct {
	id                        int64
	worker, position, company string
}

type closedTarget struct {
	submitterID int64
	email       string
	lines       []closedLine
}

// dueClosedNotices groups the documents closed and not yet announced by submitter. Only accounts
// that can still receive mail are included: Active, not the built-in admin, address not bounced,
// not erased. The rest stay unmarked, so a re-enabled account is told later.
func (s *Store) dueClosedNotices(ctx context.Context) ([]closedTarget, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT u.SubmitterID, u.Email, s.SubmissionID, s.PatName, s.PatPosition, s.PatCompanyText
		 FROM McuSubmission s
		 JOIN Submitter u ON u.SubmitterID = s.FK_SubmitterID
		 WHERE s.Status = ? AND s.FK_McuRecordID IS NOT NULL AND s.CertNotifiedAt IS NULL
		   AND u.Status = ? AND u.IsBuiltIn = 0 AND u.EmailBouncedAt IS NULL AND u.ErasedAt IS NULL
		 ORDER BY u.SubmitterID, s.SubmissionID`, SubClosed, StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []closedTarget
	for rows.Next() {
		var id int64
		var email string
		var l closedLine
		if err := rows.Scan(&id, &email, &l.id, &l.worker, &l.position, &l.company); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].submitterID != id {
			out = append(out, closedTarget{submitterID: id, email: email})
		}
		out[len(out)-1].lines = append(out[len(out)-1].lines, l)
	}
	return out, rows.Err()
}

// RunClosedNotices sends one mail per submitter about the documents closed since their last one,
// and returns how many mails and documents that was. A document is marked as told only after its
// mail was accepted, so a mail outage is retried on the next daily run. It is safe to run any
// number of times: a second run finds nothing to say.
func (s *Store) RunClosedNotices(ctx context.Context, m Mailer, portalURL string) (mails, documents int, err error) {
	due, err := s.dueClosedNotices(ctx)
	if err != nil {
		return 0, 0, err
	}
	var failures []error
	for _, t := range due {
		if err := m.Send(ctx, t.email, fmt.Sprintf("Medical checkups completed: %d", len(t.lines)), closedNoticeBody(portalURL, t.lines)); err != nil {
			log.Printf("mcu closed notice: mail to submitter %d failed: %v", t.submitterID, err)
			failures = append(failures, err)
			continue
		}
		for _, l := range t.lines {
			if _, err := s.DB.ExecContext(ctx, `UPDATE McuSubmission SET CertNotifiedAt = ? WHERE SubmissionID = ? AND CertNotifiedAt IS NULL`, s.stamp(), l.id); err != nil {
				failures = append(failures, err)
			}
		}
		// Count only, never a name (plan section 3).
		_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmitter, EntityID: t.submitterID,
			Action: "ClosedNoticeSent", Detail: fmt.Sprintf("%d documents", len(t.lines))})
		mails++
		documents += len(t.lines)
	}
	return mails, documents, errors.Join(failures...)
}

func closedNoticeBody(portalURL string, lines []closedLine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of the medical checkups you submitted have been completed by the medical post:\n\n", len(lines))
	shown := lines
	if len(shown) > closedNoticeMaxLines {
		shown = shown[:closedNoticeMaxLines]
	}
	for _, l := range shown {
		fmt.Fprintf(&b, "- %s | %s | %s\n", oneLine(l.worker), oneLine(l.position), oneLine(l.company))
	}
	if more := len(lines) - len(shown); more > 0 {
		fmt.Fprintf(&b, "\nand %d more.\n", more)
	}
	fmt.Fprintf(&b, "\nSign in to see them and download the health certificates that are available: %s/documents\n", portalURL+basePath)
	b.WriteString("\nThis mail holds no results, ID numbers or birth dates on purpose.\n")
	return b.String()
}
