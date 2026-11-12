// Package ticket holds the working-ticket rules: rotation counting, the
// consecutive-chain analysis, proposal checks and the daily departure job.
//
// WHY pure functions: every rule takes plain values (a Policy, tickets, a
// day) and returns a result, with no database or clock inside. That makes
// the rules testable with the golden cases from plan/business-rules-tickets.md
// and lets the HTTP layer and the job runner share the same code.
package ticket

import (
	"sort"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Status is a ticket's lifecycle state.
type Status string

const (
	StatusPendingDoctor Status = "PendingDoctor"
	StatusApproved      Status = "Approved"
	StatusRejected      Status = "Rejected"
	StatusClosed        Status = "Closed"
	StatusCancelled     Status = "Cancelled"
)

// Rotation pattern texts stored on the ticket.
const (
	PatternNormal              = "Normal"
	PatternConsecutive         = "Consecutive"
	PatternConsecutiveExtended = "ConsecutiveExtended"
	PatternShortBreak          = "ShortBreak"
	PatternShortBreakExtended  = "ShortBreakExtended"
	PatternExtended            = "Extended"
)

// NormalizeStatus maps stored text to a Status. The legacy value "active"
// counts as Approved; unknown text is returned unchanged.
func NormalizeStatus(s string) Status {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "approved":
		return StatusApproved
	case "pendingdoctor":
		return StatusPendingDoctor
	case "rejected":
		return StatusRejected
	case "closed":
		return StatusClosed
	case "cancelled", "canceled":
		return StatusCancelled
	}
	return Status(strings.TrimSpace(s))
}

// Ticket is one trip ticket. Functions return modified copies, never mutate.
type Ticket struct {
	ID, LEP      string
	Work         bool // false for waiver tickets
	Status       Status
	Arrival      dates.Day
	Departure    dates.Day
	ClosedAt     dates.Moment
	ClosedReason string
	Location     string // Onshore or Offshore, chosen per ticket
	Pattern      string // rotation pattern text

	RecordStart dates.Moment // set by gate check-in
	LepLocked   bool
	WeeksOnSite int

	ReminderNotified bool
	FatigueFlag      bool
	WeekN1SentAt     dates.Moment
	WeekN0LockedAt   dates.Moment
}

// End is when the ticket really ended: ClosedAt if set, else the plan.
func (t Ticket) End() dates.Day {
	if t.ClosedAt != "" {
		return t.ClosedAt.Day()
	}
	return t.Departure
}

// Days is the inclusive length of the planned ticket (same day = 1).
func (t Ticket) Days() int { return dates.InclusiveDays(t.Arrival, t.Departure) }

// IsExtendedPattern reports whether the pattern text records an extension.
// Legacy: blank, n, _, normal, consecutive and shortbreak are not extended;
// "y" is; otherwise a pattern containing "extended" is.
func IsExtendedPattern(pattern string) bool {
	v := strings.ToLower(strings.TrimSpace(pattern))
	switch v {
	case "", "n", "_", "normal", "consecutive", "shortbreak":
		return false
	case "y":
		return true
	}
	return strings.Contains(v, "extended")
}

// Counting returns the tickets that count toward rotation: work-entry tickets
// that are Approved or Closed. Pending, rejected and cancelled never count.
func Counting(tickets []Ticket) []Ticket {
	var out []Ticket
	for _, t := range tickets {
		if t.Work && (t.Status == StatusApproved || t.Status == StatusClosed) {
			out = append(out, t)
		}
	}
	return out
}

// RotationNo is the number the next ticket gets for this LEP.
func RotationNo(tickets []Ticket) int { return len(Counting(tickets)) + 1 }

// NormalizeLocation returns "Onshore" or "Offshore", or "" for anything else.
func NormalizeLocation(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "onshore":
		return "Onshore"
	case "offshore":
		return "Offshore"
	}
	return ""
}

// Chain describes the run of back-to-back tickets ending at the latest one.
type Chain struct {
	Days            int
	UsedExtension   bool
	ExceededOwnCaps bool
	Last            *Ticket
}

// chainGapBreak: a gap of more than 1 day between tickets ends the chain, so
// a gap of 0 (same day) or 1 (next day) keeps it going.
const chainGapBreak = 1

// ConsecutiveChain walks backwards from the latest counting ticket.
func ConsecutiveChain(p policy.Policy, tickets []Ticket, wni bool) Chain {
	var sorted []Ticket
	for _, t := range Counting(tickets) {
		if t.Arrival != "" && t.End() != "" {
			sorted = append(sorted, t)
		}
	}
	if len(sorted) == 0 {
		return Chain{}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Arrival < sorted[j].Arrival })

	var c Chain
	for i := len(sorted) - 1; i >= 0; i-- {
		t := sorted[i]
		span := dates.InclusiveDays(t.Arrival, t.End())
		c.Days += span
		if IsExtendedPattern(t.Pattern) {
			c.UsedExtension = true
		}
		if span > p.WorkWeeks(t.Location, wni)*7 {
			c.ExceededOwnCaps = true
		}
		if i > 0 && dates.DaysBetween(sorted[i-1].End(), t.Arrival) > chainGapBreak {
			break
		}
	}
	last := sorted[len(sorted)-1]
	c.Last = &last
	return c
}

// Analysis is the result of checking a proposed ticket against the policy.
type Analysis struct {
	Location                 string
	GapDays                  int
	HasPrevious              bool
	ArrivalBeforePreviousEnd bool
	ChainDaysBefore          int
	ChainUsedExtension       bool
	ProposedDays             int
	NormalCapDays            int
	MaxConsecutiveDays       int
	AllowedProposalDays      int
	ProjectedChainDays       int
	ExceedsAllowance         bool
	RequiredRestDays         int
	ShortBreak               bool
	RotationExtension        bool // "past duty limit"
	Consecutive              bool
	Pattern                  string
}

// PatternFromFlags builds the pattern text.
func PatternFromFlags(consecutive, shortBreak, extended bool) string {
	switch {
	case consecutive && extended:
		return PatternConsecutiveExtended
	case consecutive:
		return PatternConsecutive
	case shortBreak && extended:
		return PatternShortBreakExtended
	case shortBreak:
		return PatternShortBreak
	case extended:
		return PatternExtended
	}
	return PatternNormal
}

// Analyze ports analyzeTicketProposalPolicy_. location must already be
// normalised (Onshore or Offshore). Nothing here blocks a proposal for going
// over a cap; callers use the flags to require fatigue consent.
func Analyze(p policy.Policy, tickets []Ticket, arrival, departure dates.Day, location string, wni bool) Analysis {
	normalCap := p.WorkWeeks(location, wni) * 7
	maxConsecutive := normalCap + p.ExtensionDays
	chain := ConsecutiveChain(p, tickets, wni)

	a := Analysis{
		Location:           location,
		NormalCapDays:      normalCap,
		MaxConsecutiveDays: maxConsecutive,
		ProposedDays:       dates.InclusiveDays(arrival, departure),
		HasPrevious:        chain.Last != nil,
	}
	a.ChainUsedExtension = chain.UsedExtension || chain.ExceededOwnCaps || chain.Days > normalCap

	// WHY MinimumRestDays after an extension: copied from legacy; the owner
	// decided to keep it as a setting (default 3), see tickets doc section 3.1.
	a.RequiredRestDays = p.RestDays(wni)
	if a.ChainUsedExtension && p.MinimumRestDays > 0 {
		a.RequiredRestDays = p.MinimumRestDays
	}
	if a.HasPrevious {
		a.GapDays = dates.DaysBetween(chain.Last.End(), arrival)
		a.ArrivalBeforePreviousEnd = a.GapDays < 0
		a.Consecutive = a.GapDays >= 0 && a.GapDays <= chainGapBreak
		a.ShortBreak = a.GapDays > chainGapBreak && a.GapDays < a.RequiredRestDays
	}
	if a.Consecutive {
		a.ChainDaysBefore = chain.Days
		a.AllowedProposalDays = max(0, maxConsecutive-chain.Days)
	} else {
		a.AllowedProposalDays = normalCap
	}
	a.ProjectedChainDays = a.ChainDaysBefore + a.ProposedDays
	a.ExceedsAllowance = a.ProposedDays > a.AllowedProposalDays
	// A consecutive ticket past the normal cap is flagged even inside the
	// extension allowance (legacy quirk, tickets doc section 3.1).
	a.RotationExtension = a.ExceedsAllowance || (a.Consecutive && a.ProjectedChainDays > normalCap)
	a.Pattern = PatternFromFlags(a.Consecutive, a.ShortBreak, a.RotationExtension)
	return a
}

// WeeksOnSite counts whole weeks from the check-in day to today, by calendar
// days only (owner rule, plan 2b). No check-in means no clock (0).
func WeeksOnSite(recordStart dates.Moment, today dates.Day) int {
	if recordStart == "" {
		return 0
	}
	d := dates.DaysBetween(recordStart.Day(), today)
	if d < 0 {
		return 0
	}
	return d / 7
}
