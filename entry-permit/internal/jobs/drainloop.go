package jobs

import (
	"context"
	"time"
)

// minDrainEvery keeps a broken setting (0 or negative) from turning the loop into a busy one.
// It is also how often the loop looks at the clock.
const minDrainEvery = time.Minute

// drainEvery is how long to wait between sends: the Settings value, never under a minute.
func (r *Runner) drainEvery() time.Duration {
	if r.DrainTick > 0 {
		return r.DrainTick
	}
	if d := time.Duration(r.Policy().OutboxDrainMinutes) * time.Minute; d >= minDrainEvery {
		return d
	}
	return minDrainEvery
}

// drainDue says whether the interval has passed since the last send.
func drainDue(last, now time.Time, every time.Duration) bool { return now.Sub(last) >= every }

// DrainLoop sends the mail that is waiting in the outbox now, and again every
// drainEvery, until ctx ends. It is what retries a mail that failed once: the decision
// and job mails try to send at once, and anything the mail server refused is picked up
// here, with the growing wait and the attempt limit the outbox already enforces.
//
// WHY it looks at the clock every minute instead of sleeping the whole interval: the
// interval is a Setting, and an admin who lowers it from 60 to 5 minutes expects that to
// work soon, not after the old hour is up. With no mail server configured nothing is sent
// and mail stays Pending.
func (r *Runner) DrainLoop(ctx context.Context) {
	poll := minDrainEvery
	if r.DrainTick > 0 {
		poll = r.DrainTick // tests: look often, send as often
	}
	last := time.Now()
	r.drain(ctx)
	for {
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if now := time.Now(); drainDue(last, now, r.drainEvery()) {
			last = now
			r.drain(ctx)
		}
	}
}
