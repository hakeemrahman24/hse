package dashboard

import (
	"context"
	"sort"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
	"entry-permit/internal/ticket"
)

// RestGaps lists, for every approved or closed work ticket that follows an earlier one, how long
// the worker rested in between. The reading is ticket.RestGaps, the same rules as a proposal.
// Workers are sorted by name and each worker's tickets by arrival.
func (s Store) RestGaps(ctx context.Context, p policy.Policy) ([]Row, error) {
	type owner struct{ name, company, nationality string }
	rs, err := s.DB.QueryContext(ctx, `SELECT t.ticket_id, t.lep_no, t.status, t.arrival, t.departure, t.location, t.pattern,
		pm.worker_name, pm.company, pm.nationality
		FROM ticket t JOIN permit pm ON pm.lep_no = t.lep_no
		WHERE t.is_work = 1 AND t.status IN ('Approved', 'Closed') ORDER BY t.lep_no, t.arrival, t.ticket_id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	byLEP := map[string][]ticket.Ticket{}
	who := map[string]owner{}
	var order []string
	for rs.Next() {
		var t ticket.Ticket
		var st, arr, dep string
		var o owner
		if err := rs.Scan(&t.ID, &t.LEP, &st, &arr, &dep, &t.Location, &t.Pattern, &o.name, &o.company, &o.nationality); err != nil {
			return nil, err
		}
		t.Work, t.Status, t.Arrival, t.Departure = true, ticket.Status(st), dates.Day(arr), dates.Day(dep)
		if _, seen := who[t.LEP]; !seen {
			order = append(order, t.LEP)
		}
		who[t.LEP] = o
		byLEP[t.LEP] = append(byLEP[t.LEP], t)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	var out []Row
	for _, lep := range order {
		o, tickets := who[lep], byLEP[lep]
		wni := o.nationality == "" || o.nationality == "Indonesia"
		gaps := ticket.RestGaps(p, tickets, wni)
		prev := ""
		for _, t := range tickets { // already in arrival order
			g, ok := gaps[t.ID]
			if ok && !g.First {
				out = append(out, Row{Kind: KindWork, ID: lep, Name: o.name, Company: o.company, PrevEnd: prev,
					Arrival: string(t.Arrival), Departure: string(t.Departure), Status: string(t.Status),
					RestDays: g.Days, RestRequired: g.Required, RestKind: g.Kind, RestLabel: g.Label()})
			}
			if ok {
				prev = string(t.End())
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := lower(out[i].Name), lower(out[j].Name); a != b {
			return a < b
		}
		return out[i].Arrival < out[j].Arrival
	})
	return out, nil
}
