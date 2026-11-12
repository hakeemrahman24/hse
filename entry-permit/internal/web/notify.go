package web

import (
	"context"
	"log"
	"strings"

	"entry-permit/internal/card"
	"entry-permit/internal/dates"
	"entry-permit/internal/mail"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/staff"
	"entry-permit/internal/waiver"
)

// stageLabel is how a stage reads in a rejection mail.
var stageLabel = map[string]string{
	"doctor": "Doctor review",
	"stage1": "Stage 1 review",
	"final":  "Final approval",
}

// decisionNotice works out which mail (if any) a decision calls for.
//
// WHY only the last approval and every rejection: a Stage 1 or Doctor approval
// only moves the request on, and the requester has nothing to act on yet. This
// is what the old system did (permits doc 2).
func (a *App) decisionNotice(d reviewlog.Decision, out reviewlog.Outcome) (mail.Notice, bool) {
	isWaiver := strings.HasPrefix(d.ID, "WVR-")
	n := mail.Notice{
		To: out.Notice.Requester, Name: out.Notice.Name, Company: out.Notice.Company, Position: out.Notice.Position,
		Location: out.Notice.Location, Number: d.ID,
		VisitFrom: card.ShortDate(dates.Day(out.Notice.VisitFrom)), VisitTo: card.ShortDate(dates.Day(out.Notice.VisitTo)),
		Stage: stageLabel[d.Stage], Reviewer: d.Reviewer, Note: d.Note,
		ValidUntil: card.ShortDate(dates.Day(out.Notice.ValidUntil)),
	}
	switch {
	case !d.Approve && isWaiver:
		n.Kind = mail.KindWaiverRejected
	case !d.Approve:
		n.Kind = mail.KindWEPRejected
	case isWaiver && out.Status == waiver.StatusApproved:
		n.Kind = mail.KindWaiverApproved
		n.Link = card.Payload(a.BaseURL, d.ID)
	case !isWaiver && out.LEP != "":
		n.Kind = mail.KindWEPApproved
		n.Number = out.LEP // the card and the mail speak of the LEP, not the request id
		n.Link = card.Payload(a.BaseURL, out.LEP)
	default:
		return mail.Notice{}, false
	}
	return n, true
}

// notifyDecision stores the decision mail and tries to send it at once.
//
// WHY this never fails the request: the decision is already committed and
// cannot be undone by a mail problem. A send failure leaves the row Pending for
// the drain; a mail that cannot even be stored is logged for an admin to see.
// WHY after the commit and not inside it: the rules doc says so (gate-jobs 9),
// and a slow mail server must not hold a database transaction open.
func (a *App) notifyDecision(ctx context.Context, d reviewlog.Decision, out reviewlog.Outcome) {
	if a.Outbox == nil {
		return
	}
	n, ok := a.decisionNotice(d, out)
	if !ok {
		return
	}
	msg, err := mail.Compose(n)
	if err != nil {
		log.Printf("mail for %s not queued: %v", d.ID, err)
		return
	}
	if n.Kind == mail.KindWEPApproved || n.Kind == mail.KindWaiverApproved {
		msg.CC = a.adminCC(ctx, msg.To)
	}
	now := a.Clock.Now()
	id, err := a.Outbox.Enqueue(ctx, msg, now)
	if err != nil {
		log.Printf("mail for %s not queued: %v", d.ID, err)
		return
	}
	if a.Mailer == nil {
		return // no mail server configured: the row waits
	}
	pol := a.policy()
	if err := a.Outbox.Deliver(ctx, a.Mailer, id, now, mail.Retry{MaxAttempts: pol.OutboxMaxAttempts, BaseMinutes: pol.OutboxRetryMinutes}); err != nil {
		log.Printf("mail for %s will be retried: %v", d.ID, err)
	}
}

// adminCC lists the active administrators' addresses to copy on an approval mail, leaving out
// anyone already in To. WHY only on approvals: the admin wants to know what was issued; a
// rejection concerns the requester only. An account with no usable address is skipped, so one
// bad entry cannot stop the mail to the requester.
func (a *App) adminCC(ctx context.Context, to []string) []string {
	if a.Store == nil {
		return nil
	}
	list, err := a.Store.EmailsForRole(ctx, staff.Admin)
	if err != nil {
		log.Printf("admin cc: %v", err)
		return nil
	}
	have := map[string]bool{}
	for _, t := range to {
		have[t] = true
	}
	var cc []string
	for _, e := range list {
		clean, err := mail.CleanAddresses(e)
		if err != nil || len(clean) != 1 || have[clean[0]] {
			continue
		}
		have[clean[0]] = true
		cc = append(cc, clean[0])
	}
	return cc
}
