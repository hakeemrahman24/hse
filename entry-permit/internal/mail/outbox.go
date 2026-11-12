package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
)

// Outbox statuses.
const (
	StatusPending = "Pending"
	StatusSent    = "Sent"
	StatusFailed  = "Failed"
)

// drainBatch caps one drain run, so a long backlog cannot hold the scheduler.
const drainBatch = 50

// maxErrorLen fits the last_error column.
const maxErrorLen = 300

// Retry is the retry rule, from Settings (outbox_max_attempts, outbox_retry_minutes).
type Retry struct{ MaxAttempts, BaseMinutes int }

// delay grows with each attempt: 10, 20, 30 ... minutes for a base of 10.
func (r Retry) delay(attempts int) int { return attempts * r.BaseMinutes }

// Outbox stores every mail before it is sent.
type Outbox struct{ DB *sql.DB }

// Result counts what one drain did.
type Result struct{ Sent, Retried, Failed int }

// FailedRow is a mail that gave up, for the admin page.
type FailedRow struct {
	ID                                              int64
	Kind, SourceKey, To, Subject, LastError, Queued string
	Attempts                                        int
}

// Execer is what EnqueueIn needs: a *sql.DB or an open *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Enqueue stores a message as Pending and due now. It refuses a bad recipient
// here, before anything is stored, so the drain never meets a row it can never send.
func (o Outbox) Enqueue(ctx context.Context, m Message, now dates.Moment) (int64, error) {
	return o.EnqueueIn(ctx, o.DB, m, now)
}

// EnqueueIn is Enqueue through an open transaction (or the database).
//
// WHY: the daily jobs set a "reminder sent" flag on a ticket and queue its mail. In one
// transaction the two happen together or not at all, so a crash between them can neither
// lose the reminder nor send it twice.
func (o Outbox) EnqueueIn(ctx context.Context, ex Execer, m Message, now dates.Moment) (int64, error) {
	if err := cleanEach(m.To); err != nil || len(m.To) == 0 {
		return 0, fmt.Errorf("enqueue %s: bad recipient", m.Kind)
	}
	if err := cleanEach(m.CC); err != nil {
		return 0, fmt.Errorf("enqueue %s: bad cc", m.Kind)
	}
	res, err := ex.ExecContext(ctx, `
INSERT INTO email_outbox (queued_at, status, kind, source_key, to_addrs, cc_addrs, subject, text_body, html_body, attempts, next_attempt)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		string(now), StatusPending, m.Kind, m.SourceKey, strings.Join(m.To, ","), strings.Join(m.CC, ","),
		m.Subject, m.Text, m.HTML, string(now))
	if err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", m.Kind, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", m.Kind, err)
	}
	return id, nil
}

// outcome is what one attempt did to a row.
type outcome int

const (
	skipped outcome = iota // not Pending, or another sender took it
	sent
	retrying
	gaveUp
)

// Deliver tries one row now. It returns the send error (the row is kept and
// retried later), and nil when the row was sent or was not Pending any more.
func (o Outbox) Deliver(ctx context.Context, mailer Mailer, id int64, now dates.Moment, r Retry) error {
	_, err := o.attempt(ctx, mailer, id, now, r)
	return err
}

// Drain tries every Pending row that is due.
func (o Outbox) Drain(ctx context.Context, mailer Mailer, now dates.Moment, r Retry) (Result, error) {
	// Collect the ids first and close the cursor, so a send never runs inside an open query.
	rows, err := o.DB.QueryContext(ctx, `SELECT id FROM email_outbox WHERE status = ? AND next_attempt <= ? ORDER BY id LIMIT ?`,
		StatusPending, string(now), drainBatch)
	if err != nil {
		return Result{}, fmt.Errorf("list due mail: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Result{}, fmt.Errorf("read due mail: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Result{}, fmt.Errorf("list due mail: %w", err)
	}
	rows.Close()

	var res Result
	for _, id := range ids {
		out, _ := o.attempt(ctx, mailer, id, now, r) // a send error is already recorded on the row
		switch out {
		case sent:
			res.Sent++
		case retrying:
			res.Retried++
		case gaveUp:
			res.Failed++
		}
	}
	return res, nil
}

func (o Outbox) attempt(ctx context.Context, mailer Mailer, id int64, now dates.Moment, r Retry) (outcome, error) {
	var m Message
	var to, cc, status string
	var attempts int
	err := o.DB.QueryRowContext(ctx, `SELECT status, kind, source_key, to_addrs, cc_addrs, subject, text_body, html_body, attempts FROM email_outbox WHERE id = ?`, id).
		Scan(&status, &m.Kind, &m.SourceKey, &to, &cc, &m.Subject, &m.Text, &m.HTML, &attempts)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != StatusPending) {
		return skipped, nil
	}
	if err != nil {
		return skipped, fmt.Errorf("read mail %d: %w", id, err)
	}
	m.To = splitList(to)
	m.CC = splitList(cc)

	// Claim the row: count the attempt and push the next one out BEFORE sending.
	// WHY: if two senders race, only one UPDATE matches "attempts = <what I read>";
	// and if the process dies mid-send the row is simply retried later.
	next := now.AddMinutes(r.delay(attempts + 1))
	res, err := o.DB.ExecContext(ctx, `UPDATE email_outbox SET attempts = attempts + 1, next_attempt = ? WHERE id = ? AND status = ? AND attempts = ?`,
		string(next), id, StatusPending, attempts)
	if err != nil {
		return skipped, fmt.Errorf("claim mail %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return skipped, nil
	}

	if sendErr := mailer.Send(ctx, m); sendErr != nil {
		msg := clipError(sendErr.Error())
		out, newStatus := retrying, StatusPending
		if attempts+1 >= r.MaxAttempts {
			out, newStatus = gaveUp, StatusFailed
		}
		if _, err := o.DB.ExecContext(ctx, `UPDATE email_outbox SET status = ?, last_error = ? WHERE id = ?`, newStatus, msg, id); err != nil {
			return out, fmt.Errorf("record mail failure %d: %w (send error: %v)", id, err, sendErr)
		}
		return out, fmt.Errorf("send mail %d: %w", id, sendErr)
	}
	if _, err := o.DB.ExecContext(ctx, `UPDATE email_outbox SET status = ?, sent_at = ?, last_error = '' WHERE id = ?`, StatusSent, string(now), id); err != nil {
		// The mail left but the row says Pending: it would be sent twice. Say so loudly.
		return sent, fmt.Errorf("mail %d was sent but could not be marked sent: %w", id, err)
	}
	return sent, nil
}

// Failed lists mails that gave up, newest first.
func (o Outbox) Failed(ctx context.Context) ([]FailedRow, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id, kind, source_key, to_addrs, subject, last_error, queued_at, attempts FROM email_outbox WHERE status = ? ORDER BY id DESC`, StatusFailed)
	if err != nil {
		return nil, fmt.Errorf("list failed mail: %w", err)
	}
	defer rows.Close()
	var out []FailedRow
	for rows.Next() {
		var f FailedRow
		if err := rows.Scan(&f.ID, &f.Kind, &f.SourceKey, &f.To, &f.Subject, &f.LastError, &f.Queued, &f.Attempts); err != nil {
			return nil, fmt.Errorf("read failed mail: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Count is how many mails are in each state.
type Count struct{ Pending, Sent, Failed int }

// Counts tells the status page how big the backlog is.
func (o Outbox) Counts(ctx context.Context) (Count, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM email_outbox GROUP BY status`)
	if err != nil {
		return Count{}, fmt.Errorf("count mail: %w", err)
	}
	defer rows.Close()
	var c Count
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return Count{}, fmt.Errorf("count mail: %w", err)
		}
		switch status {
		case StatusPending:
			c.Pending = n
		case StatusSent:
			c.Sent = n
		case StatusFailed:
			c.Failed = n
		}
	}
	return c, rows.Err()
}

// PendingRow is a mail still waiting to be sent, for the admin page.
type PendingRow struct {
	ID                                         int64
	Kind, To, Subject, Queued, Next, LastError string
	Attempts                                   int
}

// Pending lists mails still waiting, oldest first (the one that has waited longest is on
// top), at most limit of them.
func (o Outbox) Pending(ctx context.Context, limit int) ([]PendingRow, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id, kind, to_addrs, subject, queued_at, next_attempt, last_error, attempts
		FROM email_outbox WHERE status = ? ORDER BY id LIMIT ?`, StatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending mail: %w", err)
	}
	defer rows.Close()
	var out []PendingRow
	for rows.Next() {
		var p PendingRow
		if err := rows.Scan(&p.ID, &p.Kind, &p.To, &p.Subject, &p.Queued, &p.Next, &p.LastError, &p.Attempts); err != nil {
			return nil, fmt.Errorf("read pending mail: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Requeue gives a Failed mail a fresh start (the admin's retry button).
func (o Outbox) Requeue(ctx context.Context, id int64, now dates.Moment) error {
	res, err := o.DB.ExecContext(ctx, `UPDATE email_outbox SET status = ?, attempts = 0, next_attempt = ?, last_error = '' WHERE id = ? AND status = ?`,
		StatusPending, string(now), id, StatusFailed)
	if err != nil {
		return fmt.Errorf("requeue mail %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("no failed e-mail with id %d", id)
	}
	return nil
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func clipError(s string) string {
	if r := []rune(s); len(r) > maxErrorLen {
		return string(r[:maxErrorLen])
	}
	return s
}
