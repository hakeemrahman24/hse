package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"entry-permit/internal/dates"
	"entry-permit/internal/mail"
	"entry-permit/internal/policy"
	"entry-permit/internal/ticket"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
	"entry-permit/internal/worker"
)

// DepartureCheck is the daily departure job: it applies the ticket rules to every
// Approved work ticket (lock, auto-close, reminders, weeks on site, the rotation cap),
// and ends the waivers and waiver tickets whose visit is over (permit rules section 9).
//
// It is safe to run again: every step the ticket rules take is guarded by a field on the
// ticket, and a visit that has ended is no longer open. The two parts are independent, so
// a failure reading one does not stop the other. Mail queued here is sent at once; if the
// mail server is down it simply waits in the outbox.
func (r *Runner) DepartureCheck(ctx context.Context) (Outcome, error) {
	var total Outcome
	var errs []error
	kick := false

	out, k, err := r.workTickets(ctx)
	total.Changed, total.Errors, kick = total.Changed+out.Changed, total.Errors+out.Errors, kick || k
	if err != nil {
		errs = append(errs, err)
	}
	out, k, err = r.endedVisits(ctx)
	total.Changed, total.Errors, kick = total.Changed+out.Changed, total.Errors+out.Errors, kick || k
	if err != nil {
		errs = append(errs, err)
	}

	if kick {
		r.kick() // a lock or a completed visit must reach the gate now
	}
	r.drain(ctx)
	return total, errors.Join(errs...)
}

// workTickets applies ticket.DailyCheck to each Approved work ticket.
func (r *Runner) workTickets(ctx context.Context) (Outcome, bool, error) {
	var out Outcome
	rows, err := r.Tickets.DailyTickets(ctx)
	if err != nil {
		return out, false, fmt.Errorf("departure check: %w", err)
	}
	pol, now := r.Policy(), r.Clock.Now()
	kick := false
	for _, row := range rows {
		wni := !worker.Employee{Nationality: row.Nationality}.IsForeign()
		res := ticket.DailyCheck(pol, row.Ticket, ticket.DailyInput{
			Today: now.Day(), Now: now, PermitValidTo: row.PermitValidTo, PermitStatus: row.PermitStatus, Wni: wni,
		})
		if res.Ticket == row.Ticket {
			continue // nothing to do for this ticket today
		}
		msgs, err := r.ticketMails(row, res, pol, wni)
		if err != nil {
			out.Errors++
			r.logf("jobs: ticket %s skipped: %v", row.Ticket.ID, err)
			continue
		}
		applied, err := r.Tickets.ApplyDaily(ctx, row.Ticket, res.Ticket, func(tx *sql.Tx) error {
			for _, m := range msgs {
				if _, err := r.Outbox.EnqueueIn(ctx, tx, m, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			out.Errors++
			r.logf("jobs: ticket %s skipped: %v", row.Ticket.ID, err)
			continue
		}
		if !applied {
			continue // the ticket changed while the job ran (the gate closed it): leave it
		}
		out.Changed++
		if hasEffect(res.Effects, ticket.EffectGateSync) {
			kick = true
		}
	}
	return out, kick, nil
}

func hasEffect(effects []ticket.Effect, want ticket.Effect) bool {
	for _, e := range effects {
		if e == want {
			return true
		}
	}
	return false
}

// ticketMails builds the mails the rules asked for. A bad address fails the ticket here,
// before anything is written, so its flag stays unset and tomorrow's run tries again.
func (r *Runner) ticketMails(row ticketlog.DailyRow, res ticket.DailyResult, pol policy.Policy, wni bool) ([]mail.Message, error) {
	t := res.Ticket
	base := mail.Notice{
		To: row.Requester, Name: row.Name, Number: t.LEP, TicketID: t.ID, Departure: longDate(t.Departure),
		WeeksOnSite: t.WeeksOnSite, MaxWeeks: pol.WorkWeeks(t.Location, wni), Link: r.ticketLink(t.LEP),
		DaysLeft: dates.DaysBetween(r.Clock.Today(), t.Departure),
	}
	var out []mail.Message
	for _, e := range res.Effects {
		n := base
		switch e {
		case ticket.EffectReminder:
			n.Kind = mail.KindTicketReminder
		case ticket.EffectWeekN1:
			n.Kind = mail.KindTicketWeekN1
		case ticket.EffectWeekN0:
			n.Kind = mail.KindTicketWeekN0
		default:
			continue // the gate sync effect has no mail
		}
		m, err := mail.Compose(n)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// endedVisits closes the waivers (and their tickets) whose visit ended before today.
func (r *Runner) endedVisits(ctx context.Context) (Outcome, bool, error) {
	var out Outcome
	now := r.Clock.Now()
	visits, err := r.Waivers.EndedVisits(ctx, now.Day())
	if err != nil {
		return out, false, fmt.Errorf("departure check: %w", err)
	}
	kick := false
	for _, v := range visits {
		after := waiver.AfterVisit(v.Status, "", v.To, now.Day())
		var changes []waiverlog.TicketChange
		for _, t := range v.Tickets {
			tv := waiver.AfterVisit(v.Status, t.Status, v.To, now.Day())
			if tv.TicketStatus != t.Status {
				changes = append(changes, waiverlog.TicketChange{ID: t.ID, From: t.Status, To: tv.TicketStatus, Reason: tv.TicketReason})
			}
		}
		if !after.Changed && len(changes) == 0 {
			continue
		}
		applied, err := r.Waivers.ApplyVisitEnd(ctx, v, after.Status, changes, now)
		if err != nil {
			out.Errors++
			r.logf("jobs: visit %s skipped: %v", v.ID, err)
			continue
		}
		if applied {
			out.Changed++
			kick = true
		}
	}
	return out, kick, nil
}
