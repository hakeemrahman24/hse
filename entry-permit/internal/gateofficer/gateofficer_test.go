package gateofficer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/migrations"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return New(d, dates.FixedClock("2026-11-06 10:00:00")).WithIterations(10), d
}

func TestAddValidatesAndRejectsDuplicates(t *testing.T) {
	s, _ := newStore(t)
	cases := []struct {
		name, user, code string
		want             error
	}{
		{" ", "budi", "abcd12", ErrName},
		{"Budi", "budi", "ab", ErrPIN},
		{"Budi", "budi", "has space", ErrPIN},
		{"Budi", "budi", "123456789", ErrPIN}, // 9 characters: too long
		{"Budi", "", "abcd12", ErrUsername},
		{"Budi", "b", "abcd12", ErrUsername},
		{"Budi", "has space", "abcd12", ErrUsername},
	}
	for _, c := range cases {
		if _, err := s.Add(ctx, "arif", c.name, c.user, c.code); !errors.Is(err, c.want) {
			t.Errorf("Add(%q,%q,%q) = %v want %v", c.name, c.user, c.code, err, c.want)
		}
	}
	if _, err := s.Add(ctx, "arif", "Budi", "budi", "abcd12"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, "arif", "budi", "budi2", "zzzz99"); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("same name, other case: %v", err)
	}
	// The scanner asks for the name first, so two officers may share a PIN.
	if _, err := s.Add(ctx, "arif", "Sari", "sari", "abcd12"); err != nil {
		t.Errorf("same PIN, other officer: %v", err)
	}
}

func TestCodeIsStoredHashedAndVerifies(t *testing.T) {
	s, d := newStore(t)
	o, err := s.Add(ctx, "arif", "Budi", "budi", "abcd12")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = d.QueryRow(`SELECT code_hash FROM gate_officer WHERE id=?`, o.ID).Scan(&stored)
	if strings.Contains(stored, "abcd12") || !strings.HasPrefix(stored, "pbkdf2-sha256$") {
		t.Fatalf("stored = %q", stored)
	}
	if !Verify("abcd12", stored) || Verify("abcd13", stored) || Verify("abcd12", "garbage") {
		t.Fatal("Verify wrong")
	}
	rows, _ := audit.Recent(ctx, d, 5)
	for _, r := range rows {
		if strings.Contains(r.Old+r.New+r.Target, "abcd12") {
			t.Fatal("code leaked into audit")
		}
	}
}

func TestVerifiersAreForActiveOfficersAndNeverHoldThePIN(t *testing.T) {
	s, _ := newStore(t)
	a, _ := s.Add(ctx, "admin", "Budi Santoso", "budi", "abcd12")
	b, _ := s.Add(ctx, "admin", "Sari Dewi", "sari", "WXYZ99")
	if err := s.SetArchived(ctx, "admin", b.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Verifiers(ctx)
	if err != nil || len(got) != 1 || got[0].Name != a.Name {
		t.Fatalf("got %+v err %v: only the active officer goes to the gate", got, err)
	}
	if !strings.HasPrefix(got[0].Hash, "pbkdf2-sha256$") || strings.Contains(got[0].Hash, "abcd12") || !Verify("abcd12", got[0].Hash) {
		t.Errorf("verifier %q", got[0].Hash)
	}
}

func TestSetCodeArchiveRestoreRemove(t *testing.T) {
	s, d := newStore(t)
	a, _ := s.Add(ctx, "arif", "Budi", "budi", "abcd12")
	b, _ := s.Add(ctx, "arif", "Sari", "sari", "wxyz34")
	if err := s.SetPIN(ctx, "arif", a.ID, "wxyz34"); err != nil { // another officer's PIN is allowed
		t.Fatalf("shared PIN: %v", err)
	}
	if err := s.SetPIN(ctx, "arif", a.ID, "x"); !errors.Is(err, ErrPIN) {
		t.Fatalf("short PIN: %v", err)
	}
	if err := s.SetPIN(ctx, "arif", a.ID, "new777"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetArchived(ctx, "arif", a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List(ctx, false); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("active list = %+v", got)
	}
	if got, _ := s.List(ctx, true); len(got) != 2 {
		t.Fatalf("with archived = %+v", got)
	}
	if err := s.SetArchived(ctx, "arif", a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "arif", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "arif", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	if got, _ := s.List(ctx, true); len(got) != 1 {
		t.Fatalf("after remove = %+v", got)
	}
	if rows, _ := audit.Recent(ctx, d, 50); len(rows) < 6 {
		t.Fatalf("audit rows = %d", len(rows))
	}
}

func TestUsername(t *testing.T) {
	s, _ := newStore(t)
	a, err := s.Add(ctx, "arif", "Budi", " Budi.S ", "abcd12")
	if err != nil || a.Username != "budi.s" {
		t.Fatalf("add: %+v %v", a, err)
	}
	if _, err := s.Add(ctx, "arif", "Sari", "BUDI.S", "wxyz34"); !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("same username, other case: %v", err)
	}
	b, _ := s.Add(ctx, "arif", "Sari", "sari", "wxyz34")
	if err := s.SetUsername(ctx, "arif", b.ID, "budi.s"); !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("take another's username: %v", err)
	}
	if err := s.SetUsername(ctx, "arif", b.ID, "sari"); err != nil { // own username again is fine
		t.Fatalf("own username: %v", err)
	}
	if err := s.SetUsername(ctx, "arif", b.ID, "sari.w"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUsername(ctx, "arif", b.ID, "x y"); !errors.Is(err, ErrUsername) {
		t.Fatalf("bad username: %v", err)
	}
	if err := s.SetUsername(ctx, "arif", 999, "someone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	got, _ := s.List(ctx, false)
	if len(got) != 2 || got[0].Username != "budi.s" || got[1].Username != "sari.w" {
		t.Fatalf("list = %+v", got)
	}
}

// eligibleSet builds the check function the sweep calls.
func eligibleSet(names ...string) func(context.Context, string) (bool, error) {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(_ context.Context, n string) (bool, error) { return set[n], nil }
}

func TestArchiveIneligible(t *testing.T) {
	s, d := newStore(t)
	for i, n := range []string{"Budi", "Sari", "Dewi", "Agus"} {
		if _, err := s.Add(ctx, "arif", n, []string{"budi", "sari", "dewi", "agus"}[i], "abcd12"); err != nil {
			t.Fatal(err)
		}
	}
	// Dewi is already archived by hand: she is not checked and not touched.
	list, _ := s.List(ctx, false)
	for _, o := range list {
		if o.Name == "Dewi" {
			_ = s.SetArchived(ctx, "arif", o.ID, true)
		}
	}
	got, err := s.ArchiveIneligible(ctx, eligibleSet("Budi", "Dewi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "Agus" || got[1] != "Sari" {
		t.Fatalf("archived = %v, want [Agus Sari]", got)
	}
	active, _ := s.List(ctx, false)
	if len(active) != 1 || active[0].Name != "Budi" {
		t.Fatalf("still active = %+v", active)
	}
	rows, _ := audit.Recent(ctx, d, 3)
	if rows[0].Actor != "system" || rows[0].Action != "gate_officer.archive" {
		t.Fatalf("audit = %+v", rows[0])
	}
	// A second run changes nothing.
	if again, _ := s.ArchiveIneligible(ctx, eligibleSet("Budi")); len(again) != 0 {
		t.Fatalf("second run archived %v", again)
	}
}

func TestArchiveIneligibleDoesNothingWhenTheListCannotBeTrusted(t *testing.T) {
	s, _ := newStore(t)
	for _, n := range []string{"Budi", "Sari", "Dewi"} {
		if _, err := s.Add(ctx, "arif", n, strings.ToLower(n), "abcd12"); err != nil {
			t.Fatal(err)
		}
	}
	// medpost unreachable on the second officer: nobody is archived, not even the first.
	calls := 0
	down := func(context.Context, string) (bool, error) {
		calls++
		if calls == 2 {
			return false, errors.New("medpost down")
		}
		return false, nil
	}
	if _, err := s.ArchiveIneligible(ctx, down); err == nil {
		t.Fatal("a failed check returned no error")
	}
	// Everyone "ineligible" at once (3 or more officers) looks like a bad answer, not 3 expired MCUs.
	if _, err := s.ArchiveIneligible(ctx, eligibleSet()); !errors.Is(err, ErrSuspicious) {
		t.Fatalf("all ineligible: %v", err)
	}
	if active, _ := s.List(ctx, false); len(active) != 3 {
		t.Fatalf("%d active after refused sweeps, want 3", len(active))
	}
}
