package mail

import (
	"fmt"
	"sort"
	"strings"
)

// Staff mails: the reviewer reminder and the admin digest. They go to staff mailboxes,
// not to a requester, so they greet "Hello," and list their records in the same shared
// layout as the other mails.
const (
	KindStaleReminder = "stale-reminder"
	KindAdminDigest   = "admin-digest"
)

const staffGreeting = "Hello,"

// StaleItem is one request waiting for a decision.
type StaleItem struct {
	ID, Name, Company, Position string
	Hours                       int // whole hours it has waited
}

// StaleNotice is the reminder for one review stage.
type StaleNotice struct {
	To             []string
	Stage          string // "Stage 1 Reviewer", "Doctor", "Final Approver"
	Items          []StaleItem
	ThresholdHours int    // items at or over this are "late"
	Link           string // the stage's review page
}

// ComposeStale builds the reminder. Items are listed longest wait first, so the worst
// case is at the top; those at or over the threshold are named in the subject.
func ComposeStale(n StaleNotice) (Message, error) {
	to, err := CleanAddresses(strings.Join(n.To, ","))
	if err != nil {
		return Message{}, fmt.Errorf("compose %s: %w", KindStaleReminder, err)
	}
	items := append([]StaleItem(nil), n.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Hours > items[j].Hours })
	late := 0
	var list []row
	for _, it := range items {
		wait := fmt.Sprintf("%dh", it.Hours)
		if it.Hours >= n.ThresholdHours {
			late++
			wait += fmt.Sprintf(" (over %dh)", n.ThresholdHours)
		}
		list = append(list, row{oneLine(it.ID + " · " + it.Name), oneLine(strings.Join(nonEmpty(it.Company, it.Position), " · ") + " — " + wait)})
	}
	subject := fmt.Sprintf("[Entry Permit] %d pending %s", len(items), oneLine(n.Stage))
	after := "."
	if late > 0 {
		subject += fmt.Sprintf(" (%d over %dh)", late, n.ThresholdHours)
		after = fmt.Sprintf(", including %d waiting %d hours or more.", late, n.ThresholdHours)
	}
	p := page{
		Subject: subject, Greeting: staffGreeting,
		Before: oneLine(n.Stage) + " has ", Strong: fmt.Sprintf("%d pending request(s)", len(items)), After: after,
		Rows: list, Link: n.Link, LinkText: "Open the review page",
	}
	return finish(KindStaleReminder, n.Stage, to, p)
}

// DigestItem is one permit in the admin digest.
type DigestItem struct {
	ID, Name, Company, Position string
	ValidUntil                  string // already written for people ("12 Nov 2026")
	Left                        string // "5d", or "8d left" for grace
	Urgent                      bool
}

// DigestNotice is the periodic permit digest for the admins.
type DigestNotice struct {
	To                 []string
	Registered, Active int
	WindowDays         int
	Expiring, Grace    []DigestItem
}

// ComposeDigest builds the digest. An empty group says "None." rather than vanishing, so
// a quiet period still shows that the job ran and found nothing.
func ComposeDigest(n DigestNotice) (Message, error) {
	to, err := CleanAddresses(strings.Join(n.To, ","))
	if err != nil {
		return Message{}, fmt.Errorf("compose %s: %w", KindAdminDigest, err)
	}
	group := func(items []DigestItem) []row {
		var out []row
		for _, it := range items {
			left := it.Left
			if it.Urgent {
				left += " URGENT"
			}
			out = append(out, row{oneLine(it.ID + " · " + it.Name), oneLine(strings.Join(nonEmpty(it.Company, it.Position, "until "+it.ValidUntil), " · ") + " — " + left)})
		}
		return out
	}
	p := page{
		Subject: fmt.Sprintf("[Entry Permit] Permit Digest – %d registered, %d active, %d ending within %d days, %d in grace period",
			n.Registered, n.Active, len(n.Expiring), n.WindowDays, len(n.Grace)),
		Greeting: staffGreeting,
		Before:   "Work Entry Permit digest. ", Strong: fmt.Sprintf("%d registered, %d active", n.Registered, n.Active), After: " (active = has a working ticket today).",
		Sections: []section{
			{Title: fmt.Sprintf("Work permits ending within %d days (%d)", n.WindowDays, len(n.Expiring)), Rows: group(n.Expiring), Empty: "None."},
			{Title: fmt.Sprintf("Expired, still in the grace period (%d)", len(n.Grace)), Rows: group(n.Grace), Empty: "None."},
		},
	}
	return finish(KindAdminDigest, "", to, p)
}

func finish(kind, key string, to []string, p page) (Message, error) {
	html, err := renderHTML(p)
	if err != nil {
		return Message{}, err
	}
	return Message{Kind: kind, SourceKey: key, To: to, Subject: p.Subject, Text: renderText(p), HTML: html}, nil
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "until " {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}
