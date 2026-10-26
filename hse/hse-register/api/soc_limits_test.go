package main

import (
	"testing"
	"time"
)

func TestLimiterAllowsUpToMaxThenBlocks(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	l := newLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.allow("ip", 3, time.Minute) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.allow("ip", 3, time.Minute) {
		t.Fatal("4th attempt inside the window must be blocked")
	}
	if !l.allow("other", 3, time.Minute) {
		t.Fatal("another key has its own budget")
	}
	now = now.Add(61 * time.Second)
	if !l.allow("ip", 3, time.Minute) {
		t.Fatal("window passed, attempts allowed again")
	}
}

func TestLimiterLocksAfterTooManyFailures(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	l := newLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if l.locked("emp1") {
			t.Fatalf("locked too early after %d failures", i)
		}
		l.fail("emp1", 5, 15*time.Minute, 15*time.Minute)
	}
	if !l.locked("emp1") {
		t.Fatal("5 failures in 15 minutes must lock")
	}
	now = now.Add(14 * time.Minute)
	if !l.locked("emp1") {
		t.Fatal("still locked inside the 15-minute lock")
	}
	now = now.Add(2 * time.Minute)
	if l.locked("emp1") {
		t.Fatal("lock must end after 15 minutes")
	}
}
