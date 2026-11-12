package mail

import (
	"context"
	"strings"
	"testing"
)

func ticketNotice(kind string) Notice {
	return Notice{
		Kind: kind, To: "Rina@example.com", Name: "Budi Santoso", Number: "LEP-ABCDE", TicketID: "TKT-AAAAA",
		Departure: "25 Jan 2027", DaysLeft: 7, WeeksOnSite: 7, MaxWeeks: 8,
		Link: "https://permit.example/ticket?lep=LEP-ABCDE",
	}
}

func TestComposeTheNextRotationReminder(t *testing.T) {
	m, err := Compose(ticketNotice(KindTicketReminder))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[NO-REPLY] Departure in 7 days – Propose next rotation – Budi Santoso – LEP-ABCDE" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{"Budi Santoso", "7 days", "25 Jan 2027", "TKT-AAAAA", "LEP-ABCDE", "https://permit.example/ticket?lep=LEP-ABCDE", "DDMMYYYY"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("reminder text lacks %q", want)
		}
	}
	if m.Kind != KindTicketReminder || m.SourceKey != "LEP-ABCDE" || strings.Join(m.To, ",") != "rina@example.com" {
		t.Errorf("addressing %+v", m)
	}
}

func TestTheReminderSaysDayNotDaysForOne(t *testing.T) {
	n := ticketNotice(KindTicketReminder)
	n.DaysLeft = 1
	m, err := Compose(n)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Subject, "Departure in 1 day –") || strings.Contains(m.Subject, "1 days") {
		t.Errorf("subject %q", m.Subject)
	}
}

func TestComposeTheWeekBeforeTheCapWarning(t *testing.T) {
	m, err := Compose(ticketNotice(KindTicketWeekN1))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[NO-REPLY] Departure Reminder – 1 week(s) remaining – Budi Santoso" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{"7 week(s)", "8 weeks", "TKT-AAAAA", "burnout assessment"} {
		if !strings.Contains(m.Text, want) || !strings.Contains(m.HTML, want) {
			t.Errorf("week N-1 mail lacks %q", want)
		}
	}
}

func TestTheCapMailAsksForDepartureAndDoesNotClaimTheLepIsLocked(t *testing.T) {
	n := ticketNotice(KindTicketWeekN0)
	n.WeeksOnSite = 8
	m, err := Compose(n)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[NO-REPLY] ROTATION CAP – Departure required – Budi Santoso (TKT-AAAAA)" {
		t.Errorf("subject %q", m.Subject)
	}
	if !strings.Contains(m.Text, "maximum rotation of 8 weeks") || !strings.Contains(m.Text, "escort team") {
		t.Errorf("text:\n%s", m.Text)
	}
	// the cap only locks the gate when the owner switches that on, so the mail must not promise it
	if strings.Contains(strings.ToLower(m.Text), "locked") {
		t.Error("the cap mail says the permit is locked, but the cap does not lock it")
	}
}

func TestTicketMailsEscapeWhatAWorkerNameCouldCarry(t *testing.T) {
	for _, kind := range []string{KindTicketReminder, KindTicketWeekN1, KindTicketWeekN0} {
		n := ticketNotice(kind)
		n.Name = `<script>alert(1)</script>`
		m, err := Compose(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(m.HTML, "<script>") {
			t.Errorf("%s: a name became markup", kind)
		}
	}
}

func TestEnqueueInJoinsTheCallersTransaction(t *testing.T) {
	o, d := newOutbox(t)
	ctx := context.Background()
	m, err := Compose(ticketNotice(KindTicketReminder))
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM email_outbox`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.EnqueueIn(ctx, tx, m, "2027-01-18 06:00:00"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Error("a rolled-back transaction still left a mail in the outbox")
	}

	tx, _ = d.BeginTx(ctx, nil)
	id, err := o.EnqueueIn(ctx, tx, m, "2027-01-18 06:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || rowOf(t, d, id).status != StatusPending {
		t.Error("a committed transaction should leave one Pending mail")
	}
	if _, err := o.EnqueueIn(ctx, d, Message{Kind: KindTicketReminder, To: []string{"not an address"}}, "2027-01-18 06:00:00"); err == nil {
		t.Error("a bad recipient was accepted")
	}
}
