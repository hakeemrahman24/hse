package ticket

import (
	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Effect names a side effect the caller must carry out (send mail, push to
// the gate). The rules only decide; they never send anything themselves.
type Effect string

const (
	EffectGateSync Effect = "gate_sync"    // LepLocked changed
	EffectReminder Effect = "reminder"     // next-rotation reminder mail
	EffectWeekN1   Effect = "week_n1_mail" // one week before the cap
	EffectWeekN0   Effect = "week_n0_mail" // cap reached
)

// ClosedPlannedPast is the close reason written by the daily job.
const ClosedPlannedPast = "Planned departure passed"

const (
	statusExpired      = "Expired"
	statusExpiredGrace = "Expired in grace period"
)

// DailyInput is what the departure job knows about one ticket's permit.
type DailyInput struct {
	Today         dates.Day
	Now           dates.Moment
	PermitValidTo dates.Day
	PermitStatus  string
	Wni           bool
}

// DailyResult is the updated ticket plus the effects to carry out.
type DailyResult struct {
	Ticket  Ticket
	Effects []Effect
}

func permitExpired(in DailyInput) bool {
	if in.PermitStatus == statusExpired || in.PermitStatus == statusExpiredGrace {
		return true
	}
	return in.PermitValidTo != "" && in.Today > in.PermitValidTo
}

// DailyCheck ports computeDepartureCheckRowUpdate_. Only approved work
// tickets are processed. Re-running on the same day changes nothing more,
// because each step is guarded by a "done" field on the ticket.
func DailyCheck(p policy.Policy, t Ticket, in DailyInput) DailyResult {
	res := DailyResult{Ticket: t}
	if !t.Work || t.Status != StatusApproved {
		return res
	}
	cur := &res.Ticket

	// 1. Expiry lock.
	if !cur.LepLocked && permitExpired(in) {
		cur.LepLocked = true
		res.Effects = append(res.Effects, EffectGateSync)
	}
	// 2. Auto-close; nothing else happens to a closed ticket.
	if in.Today > cur.Departure {
		cur.Status, cur.ClosedAt, cur.ClosedReason = StatusClosed, in.Now, ClosedPlannedPast
		return res
	}
	// 3. A locked LEP gets no reminders.
	if cur.LepLocked {
		return res
	}
	remind(p, cur, in, &res)
	if cur.RecordStart == "" {
		return res
	}
	weeklyChecks(p, cur, in, &res)
	return res
}

// remind is step 4: one reminder, 7 days ahead (2 for short tickets).
func remind(p policy.Policy, t *Ticket, in DailyInput, res *DailyResult) {
	lead := p.ReminderDays
	if t.Days() < p.ShortTicketDays {
		lead = p.ShortLeadDays
	}
	left := dates.DaysBetween(in.Today, t.Departure)
	if left >= 0 && left <= lead && !t.ReminderNotified {
		t.ReminderNotified = true
		res.Effects = append(res.Effects, EffectReminder)
	}
}

// weeklyChecks is steps 6 to 9: weeks on site, fatigue flag, N-1 and N0.
func weeklyChecks(p policy.Policy, t *Ticket, in DailyInput, res *DailyResult) {
	t.WeeksOnSite = WeeksOnSite(t.RecordStart, in.Today)
	maxWeeks := p.WorkWeeks(t.Location, in.Wni)

	if t.WeeksOnSite >= p.FatigueWeek(maxWeeks) && !t.FatigueFlag {
		t.FatigueFlag = true
	}
	if t.WeeksOnSite >= maxWeeks-1 && t.WeekN1SentAt == "" {
		t.WeekN1SentAt = in.Now
		res.Effects = append(res.Effects, EffectWeekN1)
	}
	if t.WeeksOnSite >= maxWeeks && t.WeekN0LockedAt == "" {
		t.WeekN0LockedAt = in.Now
		t.FatigueFlag = true
		res.Effects = append(res.Effects, EffectWeekN0)
		// WHY a switch: legacy only notified at the cap; the owner added an
		// optional gate block, off by default.
		if p.BlockGateAtCap && !t.LepLocked {
			t.LepLocked = true
			res.Effects = append(res.Effects, EffectGateSync)
		}
	}
}
