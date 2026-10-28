package sofi

import (
	"errors"
	"testing"
	"time"
)

// at builds a UTC time for the tests; the windows below are written in UTC so the edges are easy
// to read, and TestGMT9Input covers the GMT+9 conversion on its own.
func at(day, hour, min int) time.Time {
	return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC)
}

func TestNoWindowMeansClosed(t *testing.T) {
	api := newBareTestAPI(t)
	if w, err := api.openWindow(at(1, 0, 0)); err != nil || w != nil {
		t.Fatalf("open = %v, %v; want closed", w, err)
	}
}

func TestWindowOpensAtStartAndClosesAtEnd(t *testing.T) {
	api := newBareTestAPI(t)
	if _, err := api.CreateWindow("Round 1", at(2, 0, 0), at(4, 0, 0), "Admin", at(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		now  time.Time
		open bool
	}{
		{"a second before it opens", at(2, 0, 0).Add(-time.Second), false},
		{"exactly when it opens", at(2, 0, 0), true},
		{"in the middle", at(3, 0, 0), true},
		{"a second before it closes", at(4, 0, 0).Add(-time.Second), true},
		{"exactly when it closes", at(4, 0, 0), false},
	} {
		w, err := api.openWindow(c.now)
		if err != nil {
			t.Fatal(err)
		}
		if (w != nil) != c.open {
			t.Errorf("%s: open = %v, want %v", c.name, w != nil, c.open)
		}
	}
}

func TestSeveralFutureWindowsCanBeScheduled(t *testing.T) {
	api := newBareTestAPI(t)
	for i, d := range []int{2, 10, 20} {
		if _, err := api.CreateWindow("", at(d, 0, 0), at(d+2, 0, 0), "Admin", at(1, 0, 0)); err != nil {
			t.Fatalf("window %d: %v", i, err)
		}
	}
	next, err := api.nextWindow(at(5, 0, 0))
	if err != nil || next == nil || next.OpensAt != at(10, 0, 0).Format(timeLayoutDB) {
		t.Fatalf("next = %+v, %v; want the one opening on the 10th", next, err)
	}
}

func TestCreateWindowRejectsBadAndOverlappingWindows(t *testing.T) {
	api := newBareTestAPI(t)
	now := at(1, 0, 0)
	if _, err := api.CreateWindow("", at(5, 0, 0), at(5, 0, 0), "Admin", now); !errors.Is(err, ErrWindowOrder) {
		t.Errorf("closes == opens: %v", err)
	}
	if _, err := api.CreateWindow("", now.Add(-48*time.Hour), now.Add(-24*time.Hour), "Admin", now); !errors.Is(err, ErrWindowPast) {
		t.Errorf("already over: %v", err)
	}
	if _, err := api.CreateWindow("", at(5, 0, 0), at(8, 0, 0), "Admin", now); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ from, to time.Time }{
		{at(4, 0, 0), at(6, 0, 0)}, // overlaps the start
		{at(7, 0, 0), at(9, 0, 0)}, // overlaps the end
		{at(6, 0, 0), at(7, 0, 0)}, // inside
		{at(4, 0, 0), at(9, 0, 0)}, // around
	} {
		if _, err := api.CreateWindow("", c.from, c.to, "Admin", now); !errors.Is(err, ErrWindowOverlap) {
			t.Errorf("%v-%v: %v, want overlap", c.from, c.to, err)
		}
	}
	// Touching edges do not overlap: one window may start the moment another ends.
	if _, err := api.CreateWindow("", at(8, 0, 0), at(9, 0, 0), "Admin", now); err != nil {
		t.Errorf("back-to-back: %v", err)
	}
}

func TestCancellingAFutureWindowKeepsTheRowAndNeverOpens(t *testing.T) {
	api := newBareTestAPI(t)
	id, err := api.CreateWindow("", at(5, 0, 0), at(8, 0, 0), "Admin", at(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.EndWindow(id, "Dr Sari", at(2, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if w, _ := api.openWindow(at(6, 0, 0)); w != nil {
		t.Fatal("a cancelled window opened")
	}
	list, err := api.ListWindows(at(6, 0, 0))
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v; the cancelled row must stay", list, err)
	}
	if list[0].State != StateCancelled || list[0].EndedBy != "Dr Sari" || list[0].CreatedBy != "Admin" {
		t.Fatalf("audit fields = %+v", list[0])
	}
	// Its time slot is free again.
	if _, err := api.CreateWindow("", at(5, 0, 0), at(8, 0, 0), "Admin", at(2, 0, 0)); err != nil {
		t.Fatalf("re-scheduling a cancelled slot: %v", err)
	}
}

func TestCloseNowEndsAnOpenWindowAtOnce(t *testing.T) {
	api := newBareTestAPI(t)
	id, _ := api.CreateWindow("", at(2, 0, 0), at(9, 0, 0), "Admin", at(1, 0, 0))
	if err := api.EndWindow(id, "Admin", at(3, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if w, _ := api.openWindow(at(3, 0, 1)); w != nil {
		t.Fatal("still open after Close now")
	}
	if w, _ := api.openWindow(at(2, 12, 0)); w == nil {
		t.Fatal("the time before Close now must stay recorded as open")
	}
	list, _ := api.ListWindows(at(4, 0, 0))
	if list[0].State != StateClosedEarly {
		t.Fatalf("state = %q", list[0].State)
	}
}

func TestEndWindowRefusesFinishedAndUnknownWindows(t *testing.T) {
	api := newBareTestAPI(t)
	id, _ := api.CreateWindow("", at(2, 0, 0), at(3, 0, 0), "Admin", at(1, 0, 0))
	if err := api.EndWindow(id, "Admin", at(5, 0, 0)); !errors.Is(err, ErrWindowFinished) {
		t.Errorf("past window: %v", err)
	}
	if err := api.EndWindow(999, "Admin", at(5, 0, 0)); !errors.Is(err, ErrWindowNotFound) {
		t.Errorf("unknown window: %v", err)
	}
	id2, _ := api.CreateWindow("", at(7, 0, 0), at(8, 0, 0), "Admin", at(1, 0, 0))
	_ = api.EndWindow(id2, "Admin", at(5, 0, 0))
	if err := api.EndWindow(id2, "Admin", at(5, 0, 1)); !errors.Is(err, ErrWindowFinished) {
		t.Errorf("twice: %v", err)
	}
}

func TestWindowStates(t *testing.T) {
	api := newBareTestAPI(t)
	now := at(10, 0, 0)
	_, _ = api.CreateWindow("past", at(2, 0, 0), at(3, 0, 0), "Admin", at(1, 0, 0))
	_, _ = api.CreateWindow("now", at(9, 0, 0), at(12, 0, 0), "Admin", at(1, 0, 0))
	_, _ = api.CreateWindow("later", at(20, 0, 0), at(21, 0, 0), "Admin", at(1, 0, 0))
	list, err := api.ListWindows(now)
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %v, %v", list, err)
	}
	// Newest opening first.
	want := []struct{ label, state string }{{"later", StateUpcoming}, {"now", StateOpen}, {"past", StatePast}}
	for i, w := range want {
		if list[i].Label != w.label || list[i].State != w.state {
			t.Errorf("row %d = %s/%s, want %s/%s", i, list[i].Label, list[i].State, w.label, w.state)
		}
	}
}

func TestGMT9Input(t *testing.T) {
	// 08:00 on the 3rd in GMT+9 is 23:00 UTC on the 2nd.
	got, err := ParseSiteTime("2026-11-03T08:00")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v", got.UTC())
	}
	if _, err := ParseSiteTime("03/11/2026"); err == nil {
		t.Fatal("a wrong format was accepted")
	}
	if s := FormatSiteTime(time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)); s != "2026-11-03 08:00" {
		t.Fatalf("formatted %q", s)
	}
}

func TestSessionWindowIsUsableUntilAnAdminEndsIt(t *testing.T) {
	api := newBareTestAPI(t)
	id, _ := api.CreateWindow("", at(2, 0, 0), at(3, 0, 0), "Admin", at(1, 0, 0))
	// The window has closed by itself, but someone who signed in during it may still finish.
	if ok, _ := api.windowUsable(id); !ok {
		t.Fatal("a naturally closed window must stay usable for a signed-in session")
	}
	_ = api.EndWindow(id, "Admin", at(2, 12, 0)) // closed early while they were filling it in
	if ok, _ := api.windowUsable(id); ok {
		t.Fatal("a window an admin ended must stop accepting submissions")
	}
	if ok, _ := api.windowUsable(12345); ok {
		t.Fatal("an unknown window was usable")
	}
}
