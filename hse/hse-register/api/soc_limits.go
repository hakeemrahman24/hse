package main

import (
	"sync"
	"time"
)

// limiter is a small in-memory rate limiter for the public SOC pages. It is per process, which
// is fine: the board runs one program. A restart clears it, which only ever gives a visitor a
// fresh budget, never a longer one.
type limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	hits  map[string][]time.Time
	fails map[string][]time.Time
	locks map[string]time.Time
}

func newLimiter() *limiter {
	return &limiter{
		now:   time.Now,
		hits:  map[string][]time.Time{},
		fails: map[string][]time.Time{},
		locks: map[string]time.Time{},
	}
}

// recent keeps only the timestamps inside the window.
func recent(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < window {
			out = append(out, t)
		}
	}
	return out
}

// allow records one attempt for key and reports whether it is within max attempts per window.
func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ts := recent(l.hits[key], now, window)
	if len(ts) >= max {
		l.hits[key] = ts
		return false
	}
	l.hits[key] = append(ts, now)
	return true
}

// fail records a failed attempt; after max failures inside window the key is locked for lockFor.
func (l *limiter) fail(key string, max int, window, lockFor time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ts := append(recent(l.fails[key], now, window), now)
	l.fails[key] = ts
	if len(ts) >= max {
		l.locks[key] = now.Add(lockFor)
		delete(l.fails, key)
	}
}

func (l *limiter) locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.locks[key]
	if !ok {
		return false
	}
	if !l.now().Before(until) {
		delete(l.locks, key)
		return false
	}
	return true
}
