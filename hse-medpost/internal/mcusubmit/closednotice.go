package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
)

// The "your checkups are done" mail. Staff review documents one at a time all day, so a mail per
// document would bury a busy submitter. Instead a job that runs every 12 hours (owner, 2026-11-10;
// it was once a day at 08:00 Jakarta) sends each submitter ONE mail listing every reviewed or
// rejected document they have not been told about yet, found by an empty CertNotifiedAt. Nobody
// presses a button: a result the reviewer saves is picked up by the next run. It names the worker
// but never the result, an ID number or a birth date: the mail travels over the open internet, the
// portal is behind a login.

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

// EnsureNotifyRequestedColumn adds McuSubmission.NotifyRequestedAt to SQLite databases; MariaDB gets
// it from migrations/mysql/043, run by hand. It is set when the reviewer tells the submitter about an
// IncompleteResult, which leaves the document Reviewed instead of Closed.
func EnsureNotifyRequestedColumn(database *sql.DB) error {
	_, err := addSubmissionColumn(database, "NotifyRequestedAt", "TEXT NULL")
	return err
}

type closedLine struct {
	id                        int64
	worker, position, company string
	rejected                  bool // a rejected document, told as "needs a corrected upload"
}

type closedTarget struct {
	submitterID int64
	companyID   int64 // the uploader's company: its other accounts get a copy
	email       string
	bounced     bool // the uploader's mailbox is bouncing: the copy to the company's other account is the mail
	lines       []closedLine
}

// counts splits a target's lines into completed checkups and rejected documents.
func (t closedTarget) counts() (done, rejected int) {
	for _, l := range t.lines {
		if l.rejected {
			rejected++
		} else {
			done++
		}
	}
	return done, rejected
}

// BackfillRejectNotices runs once, at the first start that knows about rejection mails: documents
// rejected before that are marked as told, so switching this on does not mail submitters about
// rejections they already saw in My documents. It is guarded by an event, so a restart never marks
// a newer rejection as told. (Owner, 2026-11-01: rejected documents go in the daily mail.)
func (s *Store) BackfillRejectNotices(ctx context.Context) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM McuSubmissionEvent WHERE Action = 'RejectNoticeBackfill'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET CertNotifiedAt = COALESCE(ReviewedAt, SubmittedAt) WHERE Status = ? AND CertNotifiedAt IS NULL`, SubRejected); err != nil {
		return err
	}
	return s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySystem, Action: "RejectNoticeBackfill"})
}

// dueClosedNotices groups the documents the submitter has not been told about yet, by submitter:
// every reviewed document with a result, whatever the result, and every rejected one, while
// CertNotifiedAt is empty. Nobody presses a button (owner, 2026-11-10): a result saved by the
// reviewer is picked up by the next run, which goes out every 12 hours. Only accounts that can
// still receive mail are included: Active, not the built-in admin, address not bounced, not
// erased. The rest stay unmarked, so a re-enabled account is told later.
func (s *Store) dueClosedNotices(ctx context.Context) ([]closedTarget, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT u.SubmitterID, COALESCE(u.FK_CompanyID, 0), u.Email, CASE WHEN u.EmailBouncedAt IS NULL THEN 0 ELSE 1 END, s.SubmissionID, s.PatName, s.PatPosition, s.PatCompanyText, CASE WHEN s.Status = ? THEN 1 ELSE 0 END
		 FROM McuSubmission s
		 JOIN Submitter u ON u.SubmitterID = s.FK_SubmitterID
		 WHERE ((s.Status IN (?, ?) AND s.FK_McuRecordID IS NOT NULL) OR s.Status = ?) AND s.CertNotifiedAt IS NULL
		   AND u.Status = ? AND u.IsBuiltIn = 0 AND u.ErasedAt IS NULL
		 ORDER BY u.SubmitterID, s.SubmissionID`, SubRejected, SubReviewed, SubClosed, SubRejected, StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []closedTarget
	for rows.Next() {
		var id, companyID int64
		var email string
		var l closedLine
		var rejected, bounced int
		if err := rows.Scan(&id, &companyID, &email, &bounced, &l.id, &l.worker, &l.position, &l.company, &rejected); err != nil {
			return nil, err
		}
		l.rejected = rejected == 1
		if n := len(out); n == 0 || out[n-1].submitterID != id {
			out = append(out, closedTarget{submitterID: id, companyID: companyID, email: email, bounced: bounced == 1})
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
	// The reviewers set in the portal admin's settings are copied on every notice (owner,
	// 2026-11-10), so they see what the submitter was told. Read once per run; a failure only
	// means no copies this time, never that a submitter goes untold.
	reviewers, err := s.activeReviewerEmails(ctx)
	if err != nil {
		log.Printf("mcu closed notice: reviewer copies: %v", err)
		reviewers = nil
	}
	for _, t := range due {
		// The uploader gets the mail, and so does the company's other account (owner, 2026-11-05:
		// a backup for when the first mailbox has trouble). When the uploader's mailbox is
		// bouncing, the other account's copy is the mail. The document counts as told once any
		// of them was accepted; the mail holds names and positions only, so a copy leaks nothing more.
		backups, err := s.BackupRecipients(ctx, t.submitterID, t.companyID)
		if err != nil {
			log.Printf("mcu closed notice: backup recipients of submitter %d: %v", t.submitterID, err)
		}
		var to []string
		if !t.bounced {
			to = append(to, t.email)
		}
		to = append(to, backups...)
		accepted := 0
		var mailErr error
		for _, addr := range to {
			if err := m.Send(ctx, addr, noticeSubject(t), closedNoticeBody(portalURL, t.lines)); err != nil {
				log.Printf("mcu closed notice: mail for submitter %d's documents failed: %v", t.submitterID, err)
				mailErr = err
				continue
			}
			accepted++
		}
		if accepted == 0 {
			if mailErr != nil {
				failures = append(failures, mailErr)
			}
			continue // nobody could be reached: the documents stay due and the next daily run tries again
		}
		s.copyToReviewers(ctx, m, reviewers, to, t, portalURL)
		for _, l := range t.lines {
			if err := s.markNotified(ctx, l.id); err != nil {
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

// copyToReviewers sends each reviewer a copy of a notice the submitter side already accepted. It is
// a separate mail per reviewer, not a Cc header: a Cc would show the reviewers' addresses to every
// submitter. The copy says who it went to, since the reviewer is not the addressee. A reviewer who
// is also one of the addressees is skipped, and a failed copy is only logged: the documents count
// as told because the submitter's mail was accepted, whatever happens to the copies.
func (s *Store) copyToReviewers(ctx context.Context, m Mailer, reviewers, addressed []string, t closedTarget, portalURL string) {
	already := make(map[string]bool, len(addressed))
	for _, a := range addressed {
		already[strings.ToLower(a)] = true
	}
	subject := "Copy: " + noticeSubject(t)
	body := "This is a copy for the reviewers of the mail sent to " + oneLine(t.email) + ".\n\n" + closedNoticeBody(portalURL, t.lines)
	for _, addr := range reviewers {
		if already[strings.ToLower(addr)] {
			continue
		}
		if err := m.Send(ctx, addr, subject, body); err != nil {
			log.Printf("mcu closed notice: reviewer copy failed: %v", err)
		}
	}
}

// markNotified records that the submitter was mailed about one document. A Reviewed document with a
// Fit or Fit with note result becomes Closed in the same statement: Closed is only for those two
// results (owner, 2026-11-10), and it is what opens the health certificate download. Every other
// result stays Reviewed, with CertNotifiedAt set, so it is never mailed twice. A document that
// is already marked is left alone, so two overlapping runs cannot undo each other.
func (s *Store) markNotified(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET
		   Status = CASE WHEN Status = ? AND EXISTS (
		     SELECT 1 FROM McuRecord r WHERE r.McuRecordID = McuSubmission.FK_McuRecordID AND r.ResultCriteria IN ('Fit', 'FitWithNote')
		   ) THEN ? ELSE Status END,
		   CertNotifiedAt = ?
		 WHERE SubmissionID = ? AND CertNotifiedAt IS NULL`,
		SubReviewed, SubClosed, s.stamp(), id)
	return err
}

func noticeSubject(t closedTarget) string {
	done, rejected := t.counts()
	switch {
	case rejected == 0:
		return fmt.Sprintf("Medical checkups completed: %d", done)
	case done == 0:
		return fmt.Sprintf("Medical checkup documents need a new upload: %d", rejected)
	}
	return fmt.Sprintf("Medical checkups: %d completed, %d need a new upload", done, rejected)
}

func closedNoticeBody(portalURL string, lines []closedLine) string {
	var done, rejected []closedLine
	for _, l := range lines {
		if l.rejected {
			rejected = append(rejected, l)
		} else {
			done = append(done, l)
		}
	}
	var b strings.Builder
	if len(done) > 0 {
		fmt.Fprintf(&b, "%d of the medical checkups you submitted have been completed by the medical team:\n\n", len(done))
		writeNoticeList(&b, done)
	}
	if len(rejected) > 0 {
		if len(done) > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d of your documents could not be used by the medical team (for example, the file could not be read). Please upload a corrected document for:\n\n", len(rejected))
		writeNoticeList(&b, rejected)
	}
	fmt.Fprintf(&b, "\nSign in to see the details and download the health certificates that are available: %s/documents\n", portalURL+basePath)
	b.WriteString("\nThis mail holds no results, ID numbers or birth dates on purpose.\n")
	return b.String()
}

func writeNoticeList(b *strings.Builder, lines []closedLine) {
	shown := lines
	if len(shown) > closedNoticeMaxLines {
		shown = shown[:closedNoticeMaxLines]
	}
	for _, l := range shown {
		fmt.Fprintf(b, "- %s | %s | %s\n", oneLine(l.worker), oneLine(l.position), oneLine(l.company))
	}
	if more := len(lines) - len(shown); more > 0 {
		fmt.Fprintf(b, "and %d more.\n", more)
	}
}
