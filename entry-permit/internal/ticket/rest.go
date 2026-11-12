package ticket

import (
	"fmt"
	"sort"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Rest is how long a worker rested before one ticket: the gap since the previous ticket ended,
// read with the same rules as a proposal (Analyze), so the page can never disagree with the check.
type Rest struct {
	First    bool   // no earlier ticket
	Days     int    // days from the previous ticket's last day to this arrival
	Required int    // rest the rules ask for before a new rotation
	Kind     string // "first", "consecutive", "overlap", "short" or "ok"
}

// Label is the short text shown in a table cell.
func (r Rest) Label() string {
	switch r.Kind {
	case "first":
		return "First ticket"
	case "consecutive":
		return "Consecutive (no break)"
	case "overlap":
		return "Overlaps the previous ticket"
	case "short":
		return fmt.Sprintf("%d days: short break (needs %d)", r.Days, r.Required)
	}
	return fmt.Sprintf("%d days: full rest", r.Days)
}

// RestGaps works out the rest before every counting work ticket of one permit (Approved or
// Closed), keyed by ticket id. Earlier tickets are all that each one is judged against.
func RestGaps(p policy.Policy, all []Ticket, wni bool) map[string]Rest {
	counting := Counting(all)
	sort.SliceStable(counting, func(i, j int) bool { return counting[i].Arrival < counting[j].Arrival })
	out := make(map[string]Rest, len(counting))
	for i, t := range counting {
		if i == 0 {
			out[t.ID] = Rest{First: true, Kind: "first"}
			continue
		}
		a := Analyze(p, counting[:i], t.Arrival, t.End(), t.Location, wni)
		r := Rest{Days: a.GapDays, Required: a.RequiredRestDays}
		switch {
		case a.ArrivalBeforePreviousEnd:
			r.Kind = "overlap"
		case a.Consecutive:
			r.Kind = "consecutive"
		case a.ShortBreak:
			r.Kind = "short"
		default:
			r.Kind = "ok"
		}
		out[t.ID] = r
	}
	return out
}

// EarliestFullRest is the first arrival day that counts as a full rest after the latest counting
// ticket, or "" when there is none. A worker who arrives earlier (but after the next day) is
// on a short break, one who arrives the next day is consecutive.
func EarliestFullRest(p policy.Policy, all []Ticket, wni bool) (day dates.Day, required int) {
	chain := ConsecutiveChain(p, all, wni)
	if chain.Last == nil {
		return "", 0
	}
	a := Analyze(p, all, chain.Last.End().AddDays(chainGapBreak+1), chain.Last.End().AddDays(chainGapBreak+2), chain.Last.Location, wni)
	return chain.Last.End().AddDays(a.RequiredRestDays), a.RequiredRestDays
}
