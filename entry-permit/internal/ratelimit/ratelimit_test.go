package ratelimit

import (
	"testing"
	"time"
)

func TestCountOnlyIncludesEventsInsideTheWindow(t *testing.T) {
	l := New()
	l.Add("ip:1")
	l.Add("ip:1")
	l.Add("ip:2")

	if got := l.Count("ip:1", time.Minute); got != 2 {
		t.Fatalf("ip:1 inside window = %d, want 2", got)
	}
	if got := l.Count("ip:2", time.Minute); got != 1 {
		t.Fatalf("ip:2 inside window = %d, want 1", got)
	}
	if got := l.Count("never", time.Minute); got != 0 {
		t.Fatalf("unknown key = %d, want 0", got)
	}
}

func TestForgetClearsOneKeyOnly(t *testing.T) {
	l := New()
	l.Add("lep:1")
	l.Add("lep:1")
	l.Add("lep:2")
	l.Forget("lep:1")
	if got := l.Count("lep:1", time.Minute); got != 0 {
		t.Fatalf("forgotten key still counts %d", got)
	}
	if got := l.Count("lep:2", time.Minute); got != 1 {
		t.Fatalf("another key lost its events: %d", got)
	}
	l.Forget("never-seen") // must not panic
}

func TestCountDropsEventsOlderThanTheWindow(t *testing.T) {
	l := New()
	l.Add("k")
	time.Sleep(20 * time.Millisecond)

	if got := l.Count("k", time.Millisecond); got != 0 {
		t.Fatalf("expired event still counted: %d", got)
	}
	// An expired key is forgotten, so memory does not grow with every address ever seen.
	if _, ok := l.events["k"]; ok {
		t.Fatal("expired key was not removed from the map")
	}
}
