// Package ratelimit is a small in-memory sliding-window counter, shared by the public pages
// (/sofi today, the medical checkup submission portal next) that must slow down a single address.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter counts events per key inside a sliding window; it keeps only what it still needs.
//
// WHY in memory and not in the database: these limits only need to survive a few minutes, a
// restart of the service resetting them is harmless, and a database write on every anonymous
// request would be a cheap way for an attacker to hurt the Pi.
type Limiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
}

// New returns an empty Limiter.
func New() *Limiter { return &Limiter{events: map[string][]time.Time{}} }

// Add records one event for key at the current time.
func (l *Limiter) Add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events[key] = append(l.events[key], time.Now())
}

// Count returns how many events for key happened within window, and forgets older ones.
func (l *Limiter) Count(key string, window time.Duration) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-window)
	var kept []time.Time
	for _, t := range l.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.events, key)
		return 0
	}
	l.events[key] = kept
	return len(kept)
}
