package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"hse-medpost/internal/dates"
)

// The reviewer digest: one mail listing every document still waiting for review, sent every 24, 36
// or 48 hours (owner decision, 2026-10-29; it replaces "one mail per batch" in the plan). The list
// of readers is a plain table managed by the portal admin, because reviewers have no portal login.

// maxBounceReason is the size of Submitter.EmailBounceReason (VARCHAR(100)); MariaDB refuses a longer value.
const maxBounceReason = 100

// DigestIntervals are the only choices the settings page offers. Anything else is refused in Go so
// the migration stays valid on both SQLite and MariaDB.
var DigestIntervals = []int{24, 36, 48}

// digestMaxLines keeps one mail readable and small; the rest is summarised as "and N more".
const digestMaxLines = 100

var (
	ErrBadInterval       = errors.New("mcusubmit: the interval must be 24, 36 or 48 hours")
	errNoDigestRecipient = errors.New("mcusubmit: the digest reached nobody")
)

// Reviewer is one address on the digest list.
type Reviewer struct {
	ID        int64
	Email     string
	Name      string
	Active    bool
	BouncedAt string
	CreatedAt string
}

// DigestConfig is the single settings row.
type DigestConfig struct {
	IntervalHours int
	LastSentAt    string // empty until the first digest has gone out
}

// AddReviewer puts an address on the list. The address is lower-cased, so a duplicate in any case
// is ErrEmailTaken.
func (s *Store) AddReviewer(ctx context.Context, email, name string) (int64, error) {
	addr, err := ParseEmail(email)
	if err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if len(name) > 150 {
		return 0, errors.New("the name is too long")
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO McuReviewerEmail (Email, Name, Active, CreatedAt) VALUES (?, ?, 1, ?)`, addr, name, s.stamp())
	if isUniqueViolation(err) {
		return 0, ErrEmailTaken
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Reviewers lists everybody on the digest list, oldest first.
func (s *Store) Reviewers(ctx context.Context) ([]Reviewer, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ReviewerEmailID, Email, Name, Active, COALESCE(EmailBouncedAt, ''), CreatedAt
		 FROM McuReviewerEmail ORDER BY ReviewerEmailID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reviewer
	for rows.Next() {
		var r Reviewer
		var active int
		if err := rows.Scan(&r.ID, &r.Email, &r.Name, &active, &r.BouncedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Active = active == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetReviewerActive pauses or resumes one address without deleting it.
func (s *Store) SetReviewerActive(ctx context.Context, id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	return s.oneRow(ctx, `UPDATE McuReviewerEmail SET Active = ? WHERE ReviewerEmailID = ?`, v, id)
}

// DeleteReviewer removes an address; a second delete is ErrNotFound.
func (s *Store) DeleteReviewer(ctx context.Context, id int64) error {
	return s.oneRow(ctx, `DELETE FROM McuReviewerEmail WHERE ReviewerEmailID = ?`, id)
}

// oneRow runs a write that must touch the row asked for; no row means ErrNotFound.
func (s *Store) oneRow(ctx context.Context, query string, args ...any) error {
	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DigestSettings reads the interval and when the last digest went out.
func (s *Store) DigestSettings(ctx context.Context) (DigestConfig, error) {
	var c DigestConfig
	err := s.DB.QueryRowContext(ctx,
		`SELECT IntervalHours, COALESCE(LastSentAt, '') FROM McuDigestSetting WHERE McuDigestSettingID = 1`).
		Scan(&c.IntervalHours, &c.LastSentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DigestConfig{IntervalHours: DigestIntervals[0]}, nil
	}
	return c, err
}

// SaveDigestInterval stores 24, 36 or 48 and nothing else.
func (s *Store) SaveDigestInterval(ctx context.Context, hours int) error {
	ok := false
	for _, h := range DigestIntervals {
		ok = ok || h == hours
	}
	if !ok {
		return ErrBadInterval
	}
	return s.oneRow(ctx, `UPDATE McuDigestSetting SET IntervalHours = ? WHERE McuDigestSettingID = 1`, hours)
}

type digestLine struct{ worker, position, company, submitter, sentAt string }

// waitingForReview returns the sent, not yet reviewed documents, oldest first. It holds no NIK and
// no birth date: the mail travels over the open internet, the staff page is behind a login.
func (s *Store) waitingForReview(ctx context.Context) ([]digestLine, error) {
	return s.digestLines(ctx, SubPending)
}

// reviewedNotNotified returns the Reviewed documents: a checkup with a result is saved but the
// reviewer has not pressed Notify submitter, so the submitter has not been told. Without this line
// a Reviewed document could sit forever, because notifying is optional.
func (s *Store) reviewedNotNotified(ctx context.Context) ([]digestLine, error) {
	return s.digestLines(ctx, SubReviewed)
}

func (s *Store) digestLines(ctx context.Context, status string) ([]digestLine, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT s.PatName, s.PatPosition, s.PatCompanyText, u.Name, COALESCE(b.SentAt, '')
		 FROM McuSubmission s
		 JOIN McuBatch b ON b.BatchID = s.FK_BatchID
		 JOIN Submitter u ON u.SubmitterID = s.FK_SubmitterID
		 WHERE s.Status = ?
		 ORDER BY b.SentAt, s.SubmissionID`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []digestLine
	for rows.Next() {
		var l digestLine
		if err := rows.Scan(&l.worker, &l.position, &l.company, &l.submitter, &l.sentAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) activeReviewerEmails(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT Email FROM McuReviewerEmail WHERE Active = 1 AND EmailBouncedAt IS NULL ORDER BY ReviewerEmailID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// digestDue is true on the first run and once the chosen interval has passed since the last send.
func (s *Store) digestDue(c DigestConfig) bool {
	if c.LastSentAt == "" {
		return true
	}
	last, err := time.ParseInLocation(dates.TimestampLayout, c.LastSentAt, time.UTC)
	if err != nil {
		return true // an unreadable clock must not silence the reviewers for good
	}
	return !s.now().Before(last.Add(time.Duration(c.IntervalHours) * time.Hour))
}

// RunDigest sends the digest when it is due and something is waiting. It returns how many
// reviewers got it. The clock (LastSentAt) moves only when at least one mail went out, so a mail
// outage is retried on the next hourly run instead of waiting another 24 to 48 hours; an empty
// list never starts the clock, so the first document sent after a quiet spell is mailed promptly.
func (s *Store) RunDigest(ctx context.Context, m Mailer, staffURL string) (int, error) {
	cfg, err := s.DigestSettings(ctx)
	if err != nil {
		return 0, err
	}
	if !s.digestDue(cfg) {
		return 0, nil
	}
	lines, err := s.waitingForReview(ctx)
	if err != nil {
		return 0, err
	}
	reviewed, err := s.reviewedNotNotified(ctx)
	if err != nil || len(lines)+len(reviewed) == 0 {
		return 0, err
	}
	to, err := s.activeReviewerEmails(ctx)
	if err != nil || len(to) == 0 {
		return 0, err
	}
	subject := fmt.Sprintf("Medical checkups: %d waiting for review, %d reviewed and not notified", len(lines), len(reviewed))
	body := digestBody(lines, reviewed, staffURL)
	sent := 0
	for _, addr := range to {
		if err := m.Send(ctx, addr, subject, body); err != nil {
			log.Printf("mcu digest: mail to a reviewer failed: %v", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		return 0, errNoDigestRecipient
	}
	if err := s.oneRow(ctx, `UPDATE McuDigestSetting SET LastSentAt = ? WHERE McuDigestSettingID = 1`, s.stamp()); err != nil {
		return sent, err
	}
	_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySystem, Action: "DigestSent",
		Detail: fmt.Sprintf("%d waiting, %d reviewed not notified, %d mails", len(lines), len(reviewed), sent)})
	return sent, nil
}

func digestBody(waiting, reviewed []digestLine, staffURL string) string {
	var b strings.Builder
	if len(waiting) > 0 {
		fmt.Fprintf(&b, "%d medical checkup documents are waiting for review.\n\n", len(waiting))
		writeDigestList(&b, waiting)
	}
	if len(reviewed) > 0 {
		if len(waiting) > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d reviewed documents have not been notified to their submitter (press Notify submitter on the Submitted tab).\n\n", len(reviewed))
		writeDigestList(&b, reviewed)
	}
	if staffURL != "" {
		fmt.Fprintf(&b, "\nOpen them here (sign in required): %s\n", staffURL)
	}
	b.WriteString("\nThis list holds no ID numbers, birth dates or results on purpose.\n")
	return b.String()
}

func writeDigestList(b *strings.Builder, lines []digestLine) {
	shown := lines
	if len(shown) > digestMaxLines {
		shown = shown[:digestMaxLines]
	}
	for _, l := range shown {
		fmt.Fprintf(b, "- %s | %s | %s | sent by %s | %s\n", oneLine(l.worker), oneLine(l.position), oneLine(l.company), oneLine(l.submitter), l.sentAt)
	}
	if more := len(lines) - len(shown); more > 0 {
		fmt.Fprintf(b, "and %d more.\n", more)
	}
}

// oneLine keeps a stored value from breaking the list layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// MarkEmailBounced records a hard bounce for an address, on a submitter and on the reviewer list,
// so no further mail is sent there. It returns how many accounts or list entries were flagged; 0
// is normal (unknown address) and is not an error. The Brevo webhook calls this.
func (s *Store) MarkEmailBounced(ctx context.Context, email, reason string) (int, error) {
	addr := normalizeEmail(email)
	if addr == "" {
		return 0, nil
	}
	now := s.stamp()
	total := 0
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuReviewerEmail SET EmailBouncedAt = ? WHERE Email = ? AND EmailBouncedAt IS NULL`, now, addr)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	total += int(n)

	var id int64
	err = s.DB.QueryRowContext(ctx,
		`SELECT SubmitterID FROM Submitter WHERE Email = ? AND EmailBouncedAt IS NULL AND IsBuiltIn = 0`, addr).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return total, err
	}
	if err == nil {
		if len(reason) > maxBounceReason {
			reason = reason[:maxBounceReason]
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET EmailBouncedAt = ?, EmailBounceReason = ? WHERE SubmitterID = ?`, now, reason, id); err != nil {
			return total, err
		}
		total++
		_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmitter, EntityID: id, Action: "EmailBounced", Detail: reason})
	}
	return total, nil
}
