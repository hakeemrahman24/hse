// Package jobs runs the scheduled work: the daily permit expiry check, the daily
// departure check (tickets and finished visits), and the scheduler that runs them once
// a day at the hours set in Settings.
//
// The decisions themselves are pure rules in packages permit, ticket and waiver, each
// with its own tests. This package only reads the data, applies those rules, writes the
// result, and carries out the side effects they name (queue a mail, ask the gate sync to
// run). A record that cannot be handled is logged and skipped, so one bad row never
// stops the rest of the run.
package jobs

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"

	"entry-permit/internal/dashboard"
	"entry-permit/internal/dates"
	"entry-permit/internal/mail"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiverlog"
)

// Outcome says what one run did. Errors counts records that were skipped.
type Outcome struct{ Changed, Errors int }

// Recipients says who holds a role, for the mails that go to staff.
type Recipients interface {
	EmailsForRole(ctx context.Context, role staff.Role) ([]string, error)
}

// Runner carries out the jobs.
type Runner struct {
	Tickets ticketlog.Store
	Permits permitlog.Store
	Waivers waiverlog.Store
	Reviews reviewlog.Store // who is waiting at each review stage
	Dash    dashboard.Store // the registered and active counts, the same as the dashboard shows
	Staff   Recipients      // staff mailboxes by role
	Outbox  mail.Outbox
	Mailer  mail.Mailer // nil: mail waits in the outbox until a drain sends it
	Clock   dates.Clock
	Policy  func() policy.Policy
	BaseURL string // the public address, for the link in the reminder mail

	// DrainTick overrides how often DrainLoop sends queued mail. Zero means the Settings
	// value; only tests set it.
	DrainTick time.Duration

	// GateKick asks the gate sync to run now, so a lock or a deactivation reaches the
	// gate in seconds, not at the next five-minute tick. It may be nil.
	GateKick func()

	Logf func(format string, args ...any) // nil: the standard logger
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// kick asks for one gate sync, if the gate is configured.
func (r *Runner) kick() {
	if r.GateKick != nil {
		r.GateKick()
	}
}

// drain sends what the job queued, right away. A mail server that is down is not a
// job failure: the mails stay Pending and the outbox retries them.
func (r *Runner) drain(ctx context.Context) {
	if r.Mailer == nil {
		return
	}
	pol := r.Policy()
	retry := mail.Retry{MaxAttempts: pol.OutboxMaxAttempts, BaseMinutes: pol.OutboxRetryMinutes}
	if _, err := r.Outbox.Drain(ctx, r.Mailer, r.Clock.Now(), retry); err != nil {
		r.logf("jobs: sending queued mail: %v", err)
	}
}

// ticketLink is the working-schedule page for a permit number.
func (r *Runner) ticketLink(lep string) string {
	return strings.TrimRight(r.BaseURL, "/") + "/ticket?lep=" + url.QueryEscape(lep)
}

// longDate writes a day the way the mails do ("15 Nov 2026"); an unreadable day is
// returned as it is.
func longDate(d dates.Day) string {
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil {
		return string(d)
	}
	return t.Format("2 Jan 2006")
}
