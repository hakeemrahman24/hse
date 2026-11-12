package jobs

import (
	"context"
	"fmt"
	"sort"

	"entry-permit/internal/dashboard"
	"entry-permit/internal/dates"
	"entry-permit/internal/mail"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

// digestAnchor is a Monday. Weeks are counted from it, so "every second week" is the same
// weeks every year, no matter when the service was started or restarted.
const digestAnchor dates.Day = "2026-01-05"

// digestDue says whether the digest goes out on this day: it is the chosen weekday
// (1 Monday to 7 Sunday) of a week that is a whole number of DigestWeeks from the anchor.
// A weeks setting under 1 is treated as "never" rather than dividing by zero.
func digestDue(pol policy.Policy, day dates.Day) bool {
	since := dates.DaysBetween(digestAnchor, day)
	if since < 0 || pol.DigestWeeks < 1 {
		return false
	}
	// the anchor is a Monday, so the weekday follows from the day count
	return since%7+1 == pol.DigestWeekday && (since/7)%pol.DigestWeeks == 0
}

// AdminDigest is the periodic permit digest (rules doc section 7). On its weekday, every
// DigestWeeks weeks, it mails the admins: how many work permits are registered and active,
// which end within the window, and which are past their end but still in grace. Only work
// permits count; visitors are left out. The counts come from the dashboard queries, so the
// mail and the dashboard always agree.
//
// On any other day it does nothing, and the run log still records that it ran.
func (r *Runner) AdminDigest(ctx context.Context) (Outcome, error) {
	var out Outcome
	pol, today := r.Policy(), r.Clock.Today()
	if !digestDue(pol, today) {
		return out, nil
	}
	to, err := r.Staff.EmailsForRole(ctx, staff.Admin)
	if err != nil || len(to) == 0 {
		out.Errors++
		r.logf("jobs: admin digest skipped: no admin mailbox (%v)", err)
		return out, nil
	}
	registered, err := r.Dash.Registered(ctx)
	if err != nil {
		return out, fmt.Errorf("admin digest: %w", err)
	}
	active, err := r.Dash.Active(ctx, string(today))
	if err != nil {
		return out, fmt.Errorf("admin digest: %w", err)
	}
	n := mail.DigestNotice{To: to, WindowDays: pol.DigestExpiryDays}
	var expiring, grace []dated
	for _, row := range registered {
		if row.Kind != dashboard.KindWork {
			continue
		}
		n.Registered++
		switch d, kind, ok := digestItem(row, pol, today); {
		case !ok:
		case kind == groupExpiring:
			expiring = append(expiring, d)
		default:
			grace = append(grace, d)
		}
	}
	for _, row := range active {
		if row.Kind == dashboard.KindWork {
			n.Active++
		}
	}
	n.Expiring, n.Grace = inEndOrder(expiring), inEndOrder(grace)
	msg, err := mail.ComposeDigest(n)
	if err != nil {
		out.Errors++
		r.logf("jobs: admin digest not composed: %v", err)
		return out, nil
	}
	if _, err := r.Outbox.Enqueue(ctx, msg, r.Clock.Now()); err != nil {
		return out, fmt.Errorf("admin digest: %w", err)
	}
	out.Changed = 1
	r.drain(ctx)
	return out, nil
}

const (
	groupExpiring = "expiring"
	groupGrace    = "grace"
)

// dated is a digest line with its valid-until day in sortable form (the line itself
// carries the day written for people, which does not sort).
type dated struct {
	iso  string
	item mail.DigestItem
}

// inEndOrder lists the nearest end first.
func inEndOrder(list []dated) []mail.DigestItem {
	sort.SliceStable(list, func(i, j int) bool { return list[i].iso < list[j].iso })
	out := make([]mail.DigestItem, len(list))
	for i, d := range list {
		out[i] = d.item
	}
	return out
}

// digestItem decides whether a registered permit belongs in the digest, and in which
// group. Approved permits ending within the window are "expiring" (urgent at
// DigestUrgentDays or fewer left); permits in the grace period are "grace", with the days of
// grace left worked out as LepExpiryGraceDays minus the days past (rules doc 7: 14 + days
// past, where days past counts down from zero). Anything else, or a permit with no readable
// end day, is left out.
func digestItem(row dashboard.Row, pol policy.Policy, today dates.Day) (dated, string, bool) {
	if _, err := dates.ParseDay(row.ValidUntil); err != nil {
		return dated{}, "", false
	}
	end := dates.Day(row.ValidUntil)
	item := mail.DigestItem{ID: row.ID, Name: row.Name, Company: row.Company, Position: row.Position, ValidUntil: longDate(end)}
	switch row.Status {
	case permit.StatusApproved:
		left := dates.DaysBetween(today, end)
		if left < 0 || left > pol.DigestExpiryDays {
			return dated{}, "", false
		}
		item.Left, item.Urgent = fmt.Sprintf("%dd", left), left <= pol.DigestUrgentDays
		return dated{row.ValidUntil, item}, groupExpiring, true
	case permit.StatusExpiredGrace:
		left := pol.LepExpiryGraceDays - dates.DaysBetween(end, today)
		item.Left, item.Urgent = fmt.Sprintf("%dd left", left), left <= pol.DigestGraceUrgentDays
		return dated{row.ValidUntil, item}, groupGrace, true
	}
	return dated{}, "", false
}
