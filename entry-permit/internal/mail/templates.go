package mail

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// Mail kinds, also stored in the outbox.
const (
	KindWEPApproved    = "wep-approved"
	KindWEPRejected    = "wep-rejected"
	KindWaiverApproved = "waiver-approved"
	KindWaiverRejected = "waiver-rejected"

	// Ticket mails, sent by the daily departure job to the requester.
	KindTicketReminder = "ticket-reminder" // the next rotation should be proposed
	KindTicketWeekN1   = "ticket-week-n1"  // one week before the rotation cap
	KindTicketWeekN0   = "ticket-week-n0"  // the rotation cap is reached
)

// Notice is everything a decision mail needs. The caller fills what the kind
// uses; empty fields are left out of the mail.
type Notice struct {
	Kind string
	To   string // comma list: requester plus any CC

	Name, Company, Position, Number, ValidUntil string
	VisitFrom, VisitTo, Location                string
	Stage, Reviewer, Note                       string
	Link                                        string // the working-schedule page for this number

	// The ticket mails.
	TicketID, Departure string
	DaysLeft            int // days until the planned departure
	WeeksOnSite         int
	MaxWeeks            int // the rotation cap for this worker
}

const footer = "This is an automated message from the Layar Nusantara Gas Site Entry Permit System. Please do not reply.\nQuestions? Contact the Site Medical Team."

type row struct{ Label, Value string }

// page is the shared shape of every mail, so the text and HTML parts never drift.
type page struct {
	Subject, Before, Strong, After string
	Rows                           []row
	Sections                       []section // titled lists after the rows (the staff mails)
	Link, LinkText, Hint, Closing  string
	Greeting                       string // empty means "Dear Requester,"
}

// section is a titled list, for a mail that has more than one.
type section struct {
	Title string
	Rows  []row
	Empty string // said instead of the rows when there are none
}

func (p page) greeting() string {
	if p.Greeting == "" {
		return "Dear Requester,"
	}
	return p.Greeting
}

// Compose builds the message for a notice.
func Compose(n Notice) (Message, error) {
	to, err := CleanAddresses(n.To)
	if err != nil {
		return Message{}, fmt.Errorf("compose %s: %w", n.Kind, err)
	}
	name := oneLine(n.Name)
	var p page
	switch n.Kind {
	case KindWEPApproved:
		p = page{
			Subject: "[NO-REPLY] LNG Entry Permit – Approved – " + name + " – " + oneLine(n.Number),
			Before:  "The Layar Entry Permit request for ", Strong: name, After: " has been APPROVED.",
			Rows: rows(row{"Name", n.Name}, row{"Company", n.Company}, row{"Position", n.Position}, row{"LEP Code", n.Number}, row{"Valid Until", n.ValidUntil}),
			Link: n.Link, LinkText: "Open Working Ticket/Print",
			Hint: "Confirm the worker's date of birth (DDMMYYYY) on the ticket page.",
		}
	case KindWEPRejected:
		p = page{
			Subject: "[NO-REPLY] LNG Entry Permit – Rejected – " + name + " – " + oneLine(n.Number),
			Before:  "The Layar Entry Permit request for ", Strong: name, After: " has been REJECTED.",
			Rows:    rows(row{"Request", n.Number}, row{"Stage", n.Stage}, row{"Reviewer", n.Reviewer}, row{"Reason", n.Note}),
			Closing: "Please correct the problem and submit a new request.",
		}
	case KindWaiverApproved:
		p = page{
			Subject: "[NO-REPLY] LNG Visitor Entry Permit – Approved – " + name + " – " + oneLine(n.Number),
			Before:  "The Visitor Entry Permit request for ", Strong: name, After: " has been APPROVED.",
			Rows: rows(row{"Visitor", n.Name}, row{"Company", n.Company}, row{"Permit Code", n.Number}, row{"Visit From", n.VisitFrom}, row{"Visit To", n.VisitTo}, row{"Location", n.Location}),
			Link: n.Link, LinkText: "Open Visit Ticket",
			Hint: "Confirm the visitor's date of birth (DDMMYYYY) on the ticket page.",
		}
	case KindWaiverRejected:
		p = page{
			Subject: "[NO-REPLY] LNG Visitor Entry Permit – Rejected – " + name + " – " + oneLine(n.Number),
			Before:  "The Visitor Entry Permit request for ", Strong: name, After: " has been REJECTED.",
			Rows:    rows(row{"Request", n.Number}, row{"Stage", n.Stage}, row{"Reviewer", n.Reviewer}, row{"Reason", n.Note}),
			Closing: "Please correct the problem and submit a new request.",
		}
	case KindTicketReminder:
		days := fmt.Sprintf("%d days", n.DaysLeft)
		if n.DaysLeft == 1 {
			days = "1 day"
		}
		p = page{
			Subject: "[NO-REPLY] Departure in " + days + " – Propose next rotation – " + name + " – " + oneLine(n.Number),
			Before:  "The current working ticket for ", Strong: name, After: " is scheduled to depart in " + days + ".",
			Rows: rows(row{"Planned departure", n.Departure}, row{"Ticket ID", n.TicketID}, row{"Layar Entry Permit", n.Number}),
			Link: n.Link, LinkText: "Open ticket page",
			Hint: "To propose the next rotation schedule, scan the QR code on the Layar Entry Permit, or open the link and confirm the worker's date of birth (DDMMYYYY).",
		}
	case KindTicketWeekN1:
		left := n.MaxWeeks - n.WeeksOnSite
		p = page{
			Subject: fmt.Sprintf("[NO-REPLY] Departure Reminder – %d week(s) remaining – %s", left, name),
			Before:  "The worker ", Strong: name,
			After:   fmt.Sprintf(" has been on site for %d week(s). The maximum rotation is %d weeks (%d week(s) remaining).", n.WeeksOnSite, n.MaxWeeks, left),
			Rows:    rows(row{"Ticket ID", n.TicketID}, row{"Layar Entry Permit", n.Number}),
			Closing: "Please arrange the departure schedule accordingly. Please contact the site clinic for a burnout assessment.",
		}
	case KindTicketWeekN0:
		// WHY it does not say the permit is locked (the old system did): reaching the cap
		// only locks the gate when the owner switches that on, so the mail must not promise it.
		p = page{
			Subject: "[NO-REPLY] ROTATION CAP – Departure required – " + name + " (" + oneLine(n.TicketID) + ")",
			Before:  "The worker ", Strong: name,
			After:   fmt.Sprintf(" has reached the maximum rotation of %d weeks on site.", n.MaxWeeks),
			Rows:    rows(row{"Ticket ID", n.TicketID}, row{"Layar Entry Permit", n.Number}),
			Closing: "Departure is required. Please coordinate the return with the escort team immediately.",
		}
	default:
		return Message{}, fmt.Errorf("compose: unknown mail kind %q", n.Kind)
	}
	html, err := renderHTML(p)
	if err != nil {
		return Message{}, err
	}
	return Message{Kind: n.Kind, SourceKey: n.Number, To: to, Subject: p.Subject, Text: renderText(p), HTML: html}, nil
}

// rows drops the empty ones.
func rows(all ...row) []row {
	var out []row
	for _, r := range all {
		if strings.TrimSpace(r.Value) != "" {
			out = append(out, r)
		}
	}
	return out
}

// oneLine keeps a value that goes into the Subject on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderText(p page) string {
	var b strings.Builder
	b.WriteString(p.greeting() + "\n\n" + p.Before + p.Strong + p.After + "\n\n")
	for _, r := range p.Rows {
		fmt.Fprintf(&b, "%s: %s\n", r.Label, r.Value)
	}
	for _, sec := range p.Sections {
		b.WriteString("\n" + sec.Title + "\n")
		if len(sec.Rows) == 0 {
			b.WriteString(sec.Empty + "\n")
		}
		for _, r := range sec.Rows {
			fmt.Fprintf(&b, "- %s: %s\n", r.Label, r.Value)
		}
	}
	if p.Link != "" {
		fmt.Fprintf(&b, "\n%s:\n%s\n", p.LinkText, p.Link)
	}
	if p.Hint != "" {
		b.WriteString("\n" + p.Hint + "\n")
	}
	if p.Closing != "" {
		b.WriteString("\n" + p.Closing + "\n")
	}
	b.WriteString("\nRegards,\nMedical Project LNG\n\n-- \n" + footer + "\n")
	return b.String()
}

// html/template escapes every value, so a name or a rejection note typed into a
// public form cannot add markup to the mail. The inline styles are for mail
// clients; the site's own CSP does not apply to e-mail.
var htmlMail = template.Must(template.New("mail").Parse(`<div style="font-family:Arial,sans-serif;color:#1f2937;line-height:1.5;">
<p>{{.Greeting}}</p>
<p>{{.Before}}<strong>{{.Strong}}</strong>{{.After}}</p>
{{if .Rows}}<table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin:12px 0;">
{{range .Rows}}<tr><td style="padding:4px 14px 4px 0;vertical-align:top;color:#64748b;white-space:nowrap;">{{.Label}}</td><td style="padding:4px 0;color:#1f2937;"><strong>{{.Value}}</strong></td></tr>
{{end}}</table>{{end}}
{{range .Sections}}<p style="margin:16px 0 4px;"><strong>{{.Title}}</strong></p>
{{if .Rows}}<table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin:4px 0;">
{{range .Rows}}<tr><td style="padding:3px 14px 3px 0;vertical-align:top;color:#1f2937;">{{.Label}}</td><td style="padding:3px 0;color:#1f2937;white-space:nowrap;"><strong>{{.Value}}</strong></td></tr>
{{end}}</table>{{else}}<p style="color:#64748b;margin:4px 0;">{{.Empty}}</p>{{end}}
{{end}}
{{if .Link}}<p style="margin:16px 0;"><a href="{{.Link}}" style="background:#0057d9;color:#fff;padding:10px 20px;border-radius:4px;text-decoration:none;font-weight:bold;display:inline-block;">{{.LinkText}}</a></p>{{end}}
{{if .Hint}}<p style="font-size:12px;color:#64748b;">{{.Hint}}</p>{{end}}
{{if .Closing}}<p>{{.Closing}}</p>{{end}}
<p>Regards,<br>Medical Project LNG</p>
<p style="font-size:11px;color:#64748b;margin-top:24px;border-top:1px solid #e5e7eb;padding-top:12px;line-height:1.5;">This is an automated message from the Layar Nusantara Gas Site Entry Permit System. Please do not reply.<br>Questions? Contact the Site Medical Team.</p>
</div>
`))

func renderHTML(p page) (string, error) {
	p.Greeting = p.greeting()
	var buf bytes.Buffer
	if err := htmlMail.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("render mail: %w", err)
	}
	return buf.String(), nil
}
