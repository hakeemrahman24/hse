package presence

import (
	"testing"
	"time"
)

func TestActiveListsOtherPeopleWithSection(t *testing.T) {
	store := NewStore()
	store.Touch(1, "User 1", "user1", "Data entry")
	store.Touch(3, "User 3", "user3", "MCU data")
	store.Touch(2, "User 2", "user2", "Activity")

	got := store.Active(1)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "User 2" || got[0].Section != "Activity" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Name != "User 3" || got[1].Section != "MCU data" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestActiveDropsStaleHeartbeats(t *testing.T) {
	store := NewStore()
	store.Touch(2, "User 2", "user2", "Activity")
	store.mu.Lock()
	stale := store.byID[2]
	stale.lastSeen = time.Now().Add(-time.Minute)
	store.byID[2] = stale
	store.mu.Unlock()

	if got := store.Active(1); len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestActiveOmitsAdministratorUsername(t *testing.T) {
	store := NewStore()
	store.Touch(1, "Administrator", "admin", "MCU data")
	store.Touch(4, "Administrator", "Admin", "Settings")
	store.Touch(2, "dr. Edric Sugito, MKKK", "edric", "Dashboard")
	store.Touch(3, "David Alfarius Fernando Marpaung", "david", "Activity")

	got := store.Active(99)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "David Alfarius Fernando Marpaung" || got[1].Name != "dr. Edric Sugito, MKKK" {
		t.Fatalf("got %+v", got)
	}
}

func TestCleanSectionCollapsesSpaces(t *testing.T) {
	got, ok := cleanSection("  Activity   ")
	if !ok || got != "Activity" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := cleanSection("   "); ok {
		t.Fatal("blank section should be rejected")
	}
}
