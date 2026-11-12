package jobs

import (
	"context"
	"strings"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

func TestDigestIsDueOnItsWeekdayOfEveryOtherWeekAndNeverTwiceInARow(t *testing.T) {
	// 2026-11-10 is a Tuesday, 44 weeks after the fixed first Monday (2026-01-05), so an even week.
	pol := func(weekday, weeks int) policy.Policy {
		p := policy.Default()
		p.DigestWeekday, p.DigestWeeks = weekday, weeks
		return p
	}
	cases := []struct {
		day            dates.Day
		weekday, weeks int
		want           bool
	}{
		{"2026-11-10", 2, 2, true},
		{"2026-11-17", 2, 2, false}, // the odd week
		{"2026-11-24", 2, 2, true},
		{"2026-11-11", 2, 2, false}, // right week, wrong weekday
		{"2026-11-17", 2, 1, true},  // weekly
		{"2026-11-09", 1, 2, true},  // the Monday of the same week (the default weekday)
		{"2026-12-07", 1, 4, true},  // every 4 weeks: week 48 is a multiple of 4
		{"2026-11-30", 1, 4, false}, // week 47
		{"2026-11-10", 2, 0, false}, // a broken setting sends nothing rather than dividing by zero
	}
	for _, c := range cases {
		if got := digestDue(pol(c.weekday, c.weeks), c.day); got != c.want {
			t.Errorf("digestDue(%s, weekday %d, every %d) = %v, want %v", c.day, c.weekday, c.weeks, got, c.want)
		}
	}
}

func TestAdminDigestCountsOnlyWorkPermitsAndMarksUrgentOnes(t *testing.T) {
	e := newEnv(t)
	e.pol.DigestWeekday = 2 // today is a Tuesday in an even week
	e.staff[staff.Admin] = []string{"admin@example.com"}
	e.clock.now = today + " 08:00:00"
	e.permit("LEP-AAAAA", permit.StatusApproved, "2026-11-15")     // 5 days left: urgent
	e.permit("LEP-BBBBB", permit.StatusApproved, "2026-12-05")     // 25 days: listed, not urgent
	e.permit("LEP-CCCCC", permit.StatusApproved, "2027-06-30")     // outside the 30 day window
	e.permit("LEP-DDDDD", permit.StatusExpiredGrace, "2026-11-05") // 5 days past: 9 days of grace left
	e.permit("LEP-EEEEE", permit.StatusExpiredGrace, "2026-10-28") // 13 days past: 1 day left, urgent
	e.permit("LEP-FFFFF", permit.StatusExpired, "2026-09-01")      // registered, but not listed anywhere
	e.ticket("TKT-AAAAA", "LEP-AAAAA", "2026-11-01", "2026-11-20", "")
	e.waiver("WVR-VISIT", "Approved", "2026-11-20", "Approved") // a visitor: never counted in the digest

	out, err := e.r.AdminDigest(context.Background())
	if err != nil || out.Changed != 1 || out.Errors != 0 {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(e.mailer.sent) != 1 {
		t.Fatalf("%d mails", len(e.mailer.sent))
	}
	m := e.mailer.sent[0]
	if m.Subject != "[Entry Permit] Permit Digest – 6 registered, 1 active, 2 ending within 30 days, 2 in grace period" {
		t.Errorf("subject %q", m.Subject)
	}
	if strings.Join(m.To, ",") != "admin@example.com" {
		t.Errorf("to %v", m.To)
	}
	for _, want := range []string{"LEP-AAAAA", "5d URGENT", "LEP-BBBBB", "25d", "LEP-DDDDD", "9d left", "LEP-EEEEE", "1d left URGENT"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("digest lacks %q:\n%s", want, m.Text)
		}
	}
	for _, not := range []string{"LEP-CCCCC", "LEP-FFFFF", "WVR-VISIT"} {
		if strings.Contains(m.Text, not) {
			t.Errorf("digest wrongly lists %q", not)
		}
	}
	// the nearest end comes first
	if strings.Index(m.Text, "LEP-AAAAA") > strings.Index(m.Text, "LEP-BBBBB") {
		t.Error("expiring list is not in valid-until order")
	}
}

func TestAdminDigestOnAnOffWeekSendsNothing(t *testing.T) {
	e := newEnv(t)
	e.pol.DigestWeekday = 2
	e.staff[staff.Admin] = []string{"admin@example.com"}
	e.clock.now = "2026-11-17 08:00:00" // the odd week
	out, err := e.r.AdminDigest(context.Background())
	if err != nil || out != (Outcome{}) || len(e.mailer.sent) != 0 {
		t.Fatalf("out %+v err %v mails %d", out, err, len(e.mailer.sent))
	}
}

func TestAdminDigestWithNoAdminAddressIsReportedAsSkipped(t *testing.T) {
	e := newEnv(t)
	e.pol.DigestWeekday = 2
	e.clock.now = today + " 08:00:00"
	out, err := e.r.AdminDigest(context.Background())
	if err != nil || out.Changed != 0 || out.Errors != 1 {
		t.Fatalf("out %+v err %v", out, err)
	}
}
