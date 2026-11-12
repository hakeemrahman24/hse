package jobs

import (
	"context"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/mail"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/staff"
)

// staleStage is one review stage the reminder looks at.
type staleStage struct {
	key, label string
	role       staff.Role
	page       string
}

// The order the mails are sent in, and where each stage's reviewers sign in.
var staleStages = []staleStage{
	{"stage1", "Stage 1 Reviewer", staff.Stage1, "/staff/stage1"},
	{"doctor", "Doctor", staff.Doctor, "/staff/doctor"},
	{"final", "Final Approver", staff.Final, "/staff/final"},
}

// StaleReminder is the daily reviewer reminder (rules doc section 7). For each review
// stage it mails the people who hold that role when at least one request has waited the
// limit (Settings: approver_reminder_hours, default 24) or more. The mail lists every
// request waiting at that stage, longest wait first, so the reviewer sees the whole queue.
//
// Decided 2026-11-06: nothing is sent while every request is still under the limit (the
// old system mailed every day while anything was pending). It repeats daily while a late
// request remains. A stage with late requests but no mailbox is counted as skipped, so the
// run shows "partial" and an admin knows a role has nobody to tell.
func (r *Runner) StaleReminder(ctx context.Context) (Outcome, error) {
	var out Outcome
	waiting, err := r.Reviews.Waiting(ctx)
	if err != nil {
		return out, fmt.Errorf("stale reminder: %w", err)
	}
	pol, now := r.Policy(), r.Clock.Now()
	for _, st := range staleStages {
		items, late := r.staleItems(waiting, st.key, now, pol.ApproverReminderHours, &out)
		if late == 0 {
			continue // nothing has waited long enough: no mail
		}
		to, err := r.Staff.EmailsForRole(ctx, st.role)
		if err != nil || len(to) == 0 {
			out.Errors++
			r.logf("jobs: stale reminder for %s skipped: no mailbox (%v)", st.label, err)
			continue
		}
		msg, err := mail.ComposeStale(mail.StaleNotice{
			To: to, Stage: st.label, Items: items, ThresholdHours: pol.ApproverReminderHours,
			Link: strings.TrimRight(r.BaseURL, "/") + st.page,
		})
		if err != nil {
			out.Errors++
			r.logf("jobs: stale reminder for %s skipped: %v", st.label, err)
			continue
		}
		if _, err := r.Outbox.Enqueue(ctx, msg, now); err != nil {
			out.Errors++
			r.logf("jobs: stale reminder for %s not queued: %v", st.label, err)
			continue
		}
		out.Changed++
	}
	r.drain(ctx)
	return out, nil
}

// staleItems turns one stage's waiting requests into mail rows and counts the late ones.
// A request whose time cannot be read is skipped and counted, never shown with a made-up wait.
func (r *Runner) staleItems(all []reviewlog.Waiting, stage string, now dates.Moment, limit int, out *Outcome) ([]mail.StaleItem, int) {
	var items []mail.StaleItem
	late := 0
	for _, w := range all {
		if w.Stage != stage {
			continue
		}
		hours, ok := dates.HoursBetween(dates.Moment(w.Since), now)
		if !ok {
			out.Errors++
			r.logf("jobs: stale reminder: %s has an unreadable time %q", w.ID, w.Since)
			continue
		}
		if hours >= limit {
			late++
		}
		items = append(items, mail.StaleItem{ID: w.ID, Name: w.Name, Company: w.Company, Position: w.Position, Hours: hours})
	}
	return items, late
}
