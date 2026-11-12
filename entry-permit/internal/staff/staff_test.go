package staff

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/policy"
	"entry-permit/migrations"
)

// mutableClock lets a test move time forward to pass a lockout.
type mutableClock struct{ now dates.Moment }

func (c *mutableClock) Now() dates.Moment { return c.now }
func (c *mutableClock) Today() dates.Day  { return c.now.Day() }

func newStore(t *testing.T) (*Store, *mutableClock, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	clock := &mutableClock{now: "2026-11-06 08:00:00"}
	return NewStore(d, clock, policy.Default()).WithCost(bcrypt.MinCost), clock, d
}

var ctx = context.Background()

func mustCreate(t *testing.T, s *Store, username, name string, roles ...Role) User {
	t.Helper()
	u, err := s.Create(ctx, username, name, "", "1234", roles)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLandingPriorityAndPaths(t *testing.T) {
	cases := []struct {
		roles []Role
		want  string
	}{
		{[]Role{Dashboard}, "/staff/dashboard"},
		{[]Role{Doctor, Stage1}, "/staff/stage1"},
		{[]Role{Dashboard, Admin}, "/staff/admin"},
		{[]Role{GateOfficer}, "/staff/gate"},
		{[]Role{Final, GateAdmin}, "/staff/gate-admin"},
		{[]Role{GateAdmin, Admin}, "/staff/admin"},
		{[]Role{Final, Doctor}, "/staff/final"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := LandingPath(c.roles); got != c.want {
			t.Errorf("%v -> %q want %q", c.roles, got, c.want)
		}
	}
	for _, r := range Priority {
		if !r.Valid() || r.Label() == "" || r.Slug() == "" {
			t.Errorf("role %q incomplete", r)
		}
	}
	if Role("nope").Valid() || Role("nope").Label() != "nope" {
		t.Error("unknown role handling")
	}
}

func TestParseRoles(t *testing.T) {
	got, err := ParseRoles(" Stage1, doctor ,,")
	if err != nil || len(got) != 2 || got[0] != Stage1 || got[1] != Doctor {
		t.Errorf("%v %v", got, err)
	}
	if _, err := ParseRoles("admin,king"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestValidPIN(t *testing.T) {
	for pin, ok := range map[string]bool{"1234": true, "ab12": true, "ABCD1234": true, "123": false, "123456789": false, "12 4": false, "12-4": false, "": false} {
		if (ValidPIN(pin) == nil) != ok {
			t.Errorf("ValidPIN(%q) ok=%v want %v", pin, !ok, ok)
		}
	}
}

func TestCreateValidation(t *testing.T) {
	s, _, _ := newStore(t)
	cases := []struct {
		name               string
		user, display, pin string
		roles              []Role
		want               error
	}{
		{"username", "A", "Ann", "1234", []Role{Doctor}, ErrUsername},
		{"name", "ann", "  ", "1234", []Role{Doctor}, ErrNoName},
		{"roles", "ann", "Ann", "1234", nil, ErrNoRoles},
		{"pin", "ann", "Ann", "12", []Role{Doctor}, ErrBadPIN},
	}
	for _, c := range cases {
		if _, err := s.Create(ctx, c.user, c.display, "", c.pin, c.roles); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := s.Create(ctx, "ann", "Ann", "", "1234", []Role{"king"}); err == nil {
		t.Error("unknown role accepted")
	}
	mustCreate(t, s, "ann", "Ann", Doctor, Doctor, Stage1) // duplicate role collapses
	if _, err := s.Create(ctx, "ANN", "Other", "", "1234", []Role{Doctor}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	u, err := s.ByID(ctx, 1)
	if err != nil || len(u.Roles) != 2 || !u.HasRole(Stage1) || u.HasRole(Admin) {
		t.Errorf("%+v %v", u, err)
	}
}

func TestAuthenticateHappyAndBad(t *testing.T) {
	s, _, _ := newStore(t)
	mustCreate(t, s, "ann", "Ann Lee", Doctor)
	u, err := s.Authenticate(ctx, " ANN ", "1234")
	if err != nil || u.DisplayName != "Ann Lee" || !u.HasRole(Doctor) {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.Authenticate(ctx, "ann", "9999"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("wrong pin: %v", err)
	}
	if _, err := s.Authenticate(ctx, "nobody", "1234"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("unknown: %v", err)
	}
}

func TestGolden20Lockout(t *testing.T) {
	s, clock, _ := newStore(t)
	mustCreate(t, s, "ann", "Ann", Doctor)
	for i := 1; i <= 4; i++ {
		if _, err := s.Authenticate(ctx, "ann", "0000"); !errors.Is(err, ErrBadLogin) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, err := s.Authenticate(ctx, "ann", "0000"); !errors.Is(err, ErrLocked) { // 5th wrong try locks
		t.Fatalf("5th: %v", err)
	}
	// While locked, even the right PIN is refused.
	if _, err := s.Authenticate(ctx, "ann", "1234"); !errors.Is(err, ErrLocked) {
		t.Errorf("right pin while locked: %v", err)
	}
	clock.now = "2026-11-06 08:14:59"
	if _, err := s.Authenticate(ctx, "ann", "1234"); !errors.Is(err, ErrLocked) {
		t.Errorf("still inside 15 minutes: %v", err)
	}
	clock.now = "2026-11-06 08:15:00" // lock has passed: correct PIN works and clears the counter
	if _, err := s.Authenticate(ctx, "ann", "1234"); err != nil {
		t.Errorf("after lock: %v", err)
	}
	if _, err := s.Authenticate(ctx, "ann", "0000"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("counter should restart at 1: %v", err)
	}
}

func TestSuccessResetsCounter(t *testing.T) {
	s, _, _ := newStore(t)
	mustCreate(t, s, "ann", "Ann", Doctor)
	for i := 0; i < 4; i++ {
		_, _ = s.Authenticate(ctx, "ann", "0000")
	}
	if _, err := s.Authenticate(ctx, "ann", "1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ann", "0000"); !errors.Is(err, ErrBadLogin) { // not locked: counter was reset
		t.Errorf("%v", err)
	}
}

func TestAdminActions(t *testing.T) {
	s, _, _ := newStore(t)
	mustCreate(t, s, "ann", "Ann", Doctor)
	mustCreate(t, s, "bob", "Bob", Admin)
	for i := 0; i < 5; i++ {
		_, _ = s.Authenticate(ctx, "ann", "0000")
	}
	if err := s.Unlock(ctx, "ann"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ann", "1234"); err != nil {
		t.Errorf("after unlock: %v", err)
	}
	if err := s.SetPIN(ctx, "ann", "abcd"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ann", "1234"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("old pin still works: %v", err)
	}
	if _, err := s.Authenticate(ctx, "ann", "abcd"); err != nil {
		t.Errorf("new pin: %v", err)
	}
	if err := s.SetPIN(ctx, "ann", "1"); !errors.Is(err, ErrBadPIN) {
		t.Errorf("short pin: %v", err)
	}
	// Deactivated accounts vanish from the dropdown and cannot log in.
	if err := s.SetActive(ctx, "ann", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ann", "abcd"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("inactive login: %v", err)
	}
	names, err := s.ActiveNames(ctx)
	if err != nil || len(names) != 1 || names[0].Username != "bob" {
		t.Errorf("%v %v", names, err)
	}
	for _, f := range []func() error{
		func() error { return s.Unlock(ctx, "ghost") },
		func() error { return s.SetActive(ctx, "ghost", true) },
		func() error { return s.SetPIN(ctx, "ghost", "1234") },
	} {
		if err := f(); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown user: %v", err)
		}
	}
	if _, err := s.ByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("ByID: %v", err)
	}
	// Activate again.
	if err := s.SetActive(ctx, "ann", true); err != nil {
		t.Error(err)
	}
}

func TestPINIsNotStoredInPlainText(t *testing.T) {
	s, _, d := newStore(t)
	mustCreate(t, s, "ann", "Ann", Doctor)
	var hash string
	if err := d.QueryRow(`SELECT pin_hash FROM staff_user WHERE username='ann'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "1234" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("1234")) != nil {
		t.Error("PIN must be stored as a bcrypt hash")
	}
}
