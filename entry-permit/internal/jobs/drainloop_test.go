package jobs

import (
	"context"
	"testing"
	"time"

	"entry-permit/internal/mail"
)

func (e *env) queue(lep string) {
	e.t.Helper()
	m, err := mail.Compose(ticketNoticeFor(lep))
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.r.Outbox.Enqueue(context.Background(), m, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
}

func ticketNoticeFor(lep string) mail.Notice {
	return mail.Notice{Kind: mail.KindTicketReminder, To: "rina@example.com", Name: "Budi", Number: lep, TicketID: "TKT-AAAAA", Departure: "1 Dec 2026", DaysLeft: 7}
}

func TestDrainLoopSendsWhatIsWaitingAtStartAndAgainOnEachTick(t *testing.T) {
	e := newEnv(t)
	e.r.DrainTick = 20 * time.Millisecond
	e.queue("LEP-AAAAA")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.r.DrainLoop(ctx); close(done) }()

	waitFor(t, func() bool { return e.sentCount() == 1 }, "the mail waiting at start")
	e.queue("LEP-BBBBB") // arrives later: a later tick sends it
	waitFor(t, func() bool { return e.sentCount() == 2 }, "the mail queued later")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not stop when its context ended")
	}
}

func TestDrainIntervalComesFromTheSettingAndNeverFallsBelowAMinute(t *testing.T) {
	e := newEnv(t)
	if got := e.r.drainEvery(); got != time.Hour {
		t.Errorf("default %v, want 1h", got)
	}
	e.pol.OutboxDrainMinutes = 5
	if got := e.r.drainEvery(); got != 5*time.Minute {
		t.Errorf("5 minutes gave %v", got)
	}
	e.pol.OutboxDrainMinutes = 0 // a broken setting must not make a busy loop
	if got := e.r.drainEvery(); got != time.Minute {
		t.Errorf("0 minutes gave %v, want the 1 minute floor", got)
	}
}

func TestDrainLoopWithNoMailerLeavesMailWaiting(t *testing.T) {
	e := newEnv(t)
	e.r.Mailer = nil
	e.r.DrainTick = 10 * time.Millisecond
	e.queue("LEP-AAAAA")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	e.r.DrainLoop(ctx) // returns when the context ends
	if got := e.str(`SELECT status FROM email_outbox`); got != "Pending" {
		t.Errorf("status %q, want Pending", got)
	}
}

func (e *env) sentCount() int {
	e.mailer.mu.Lock()
	defer e.mailer.mu.Unlock()
	return len(e.mailer.sent)
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDrainIsDueOnceTheIntervalHasPassedSinceTheLastSend(t *testing.T) {
	// Checked every minute against the current setting, so lowering the interval takes effect
	// within a minute instead of after the old, longer wait.
	t0 := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		since time.Duration
		every time.Duration
		want  bool
	}{
		{59 * time.Minute, time.Hour, false},
		{60 * time.Minute, time.Hour, true},
		{6 * time.Minute, 5 * time.Minute, true}, // the admin lowered it from 60 to 5 minutes
		{0, time.Minute, false},
	}
	for _, c := range cases {
		if got := drainDue(t0, t0.Add(c.since), c.every); got != c.want {
			t.Errorf("after %v with interval %v: %v, want %v", c.since, c.every, got, c.want)
		}
	}
}
