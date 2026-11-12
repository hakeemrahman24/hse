package jobs

import (
	"context"
	"fmt"

	"entry-permit/internal/permit"
)

// ExpiryCheck is the daily permit expiry job (permit rules section 6). For every
// Approved or already expired permit it works out, from the valid-until day and today,
// whether the permit is still valid, in the grace period, or expired, and moves it. A
// permit that has just left Approved has its open tickets locked in the same step.
//
// Running it again the same day changes nothing: a permit already in the right state
// is not a change. The gate is asked to sync once for the whole run, not once a permit.
func (r *Runner) ExpiryCheck(ctx context.Context) (Outcome, error) {
	var out Outcome
	cands, err := r.Permits.ExpiryCandidates(ctx)
	if err != nil {
		return out, fmt.Errorf("expiry check: %w", err)
	}
	pol, today := r.Policy(), r.Clock.Today()
	for _, c := range cands {
		e := permit.ExpiryStatus(pol, c.Status, c.ValidUntil, today)
		if !e.Changed {
			continue
		}
		applied, err := r.Permits.ApplyExpiry(ctx, c, e.Status, e.LockTickets)
		if err != nil {
			out.Errors++
			r.logf("jobs: expiry of %s skipped: %v", c.LEP, err)
			continue
		}
		if applied {
			out.Changed++
		}
	}
	if out.Changed > 0 {
		r.kick() // expired permits must stop being admitted at the gate
	}
	return out, nil
}
