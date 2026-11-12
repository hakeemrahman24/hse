package staff

import (
	"errors"
	"testing"

	"entry-permit/internal/audit"
)

func TestCreateByAdminUsesDefaultPIN(t *testing.T) {
	s, _, d := newStore(t)
	u, err := s.CreateByAdmin(ctx, "arif", "ayu", "Ayu", "ayu@x.id", []Role{Doctor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ayu", DefaultPIN); err != nil {
		t.Fatalf("default PIN login: %v", err)
	}
	rows, _ := audit.Recent(ctx, d, 5)
	if len(rows) != 1 || rows[0].Target != "staff:"+u.Username || rows[0].Actor != "arif" {
		t.Fatalf("audit = %+v", rows)
	}
	if rows[0].New+rows[0].Old != "" && (rows[0].New == DefaultPIN) {
		t.Fatal("PIN written to audit")
	}
}

func TestResetPINOnlyToDefaultAndClearsLock(t *testing.T) {
	s, _, _ := newStore(t)
	mustCreate(t, s, "ayu", "Ayu", Doctor)
	if err := s.SetPIN(ctx, "ayu", "secret1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, _ = s.Authenticate(ctx, "ayu", "bad")
	}
	if err := s.ResetPIN(ctx, "arif", "ayu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ayu", DefaultPIN); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	if err := s.ResetPIN(ctx, "arif", "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestChangeOwnPIN(t *testing.T) {
	s, _, d := newStore(t)
	u := mustCreate(t, s, "ayu", "Ayu", Doctor) // PIN 1234 per helper
	if err := s.ChangeOwnPIN(ctx, u.ID, "wrong", "abcd12"); !errors.Is(err, ErrWrongPIN) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangeOwnPIN(ctx, u.ID, "1234", "ab"); !errors.Is(err, ErrBadPIN) {
		t.Fatalf("bad new: %v", err)
	}
	if err := s.ChangeOwnPIN(ctx, u.ID, "1234", "abcd12"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ayu", "abcd12"); err != nil {
		t.Fatalf("new PIN: %v", err)
	}
	if _, err := s.Authenticate(ctx, "ayu", "1234"); err == nil {
		t.Fatal("old PIN still works")
	}
	rows, _ := audit.Recent(ctx, d, 5)
	for _, r := range rows {
		if r.New == "abcd12" || r.Old == "1234" {
			t.Fatal("PIN leaked into audit")
		}
	}
}

func TestChangeOwnPINWrongTriesLock(t *testing.T) {
	s, _, _ := newStore(t)
	u := mustCreate(t, s, "ayu", "Ayu", Doctor)
	var err error
	for i := 0; i < 5; i++ {
		err = s.ChangeOwnPIN(ctx, u.ID, "wrong", "abcd12")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("5th wrong current PIN: %v, want locked", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	s, _, _ := newStore(t)
	u := mustCreate(t, s, "ayu", "Ayu", Doctor)
	mustCreate(t, s, "budi", "Budi", Doctor)
	if err := s.UpdateProfile(ctx, u.ID, "budi", "Ayu"); !errors.Is(err, ErrExists) {
		t.Fatalf("taken: %v", err)
	}
	if err := s.UpdateProfile(ctx, u.ID, "A y", "Ayu"); !errors.Is(err, ErrUsername) {
		t.Fatalf("bad username: %v", err)
	}
	if err := s.UpdateProfile(ctx, u.ID, "ayu", " "); !errors.Is(err, ErrNoName) {
		t.Fatalf("no name: %v", err)
	}
	if err := s.UpdateProfile(ctx, u.ID, "AYU.D", "Ayu Dewi"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ByID(ctx, u.ID)
	if got.Username != "ayu.d" || got.DisplayName != "Ayu Dewi" {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.Authenticate(ctx, "ayu.d", "1234"); err != nil {
		t.Fatalf("login with new username: %v", err)
	}
	if err := s.UpdateProfile(ctx, u.ID, "ayu.d", "Ayu Dewi"); err != nil { // keeping own name is fine
		t.Fatalf("unchanged: %v", err)
	}
}

func TestAdminUpdateAndLastAdminGuard(t *testing.T) {
	s, _, _ := newStore(t)
	a := mustCreate(t, s, "arif", "Arif", Admin)
	if err := s.AdminUpdate(ctx, "arif", "arif", "Arif R", "", []Role{Doctor}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("removing last admin role: %v", err)
	}
	if err := s.AdminSetActive(ctx, "arif", "arif", false); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deactivating last admin: %v", err)
	}
	if err := s.AdminUpdate(ctx, "arif", "arif", "Arif R", "a@x.id", []Role{Admin, Doctor}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ByID(ctx, a.ID)
	if got.DisplayName != "Arif R" || len(got.Roles) != 2 {
		t.Fatalf("got %+v", got)
	}
	mustCreate(t, s, "romaryo", "Romaryo", Admin)
	if err := s.AdminSetActive(ctx, "arif", "arif", false); err != nil {
		t.Fatalf("second admin exists: %v", err)
	}
	if err := s.AdminUpdate(ctx, "arif", "romaryo", "R", "", nil); !errors.Is(err, ErrNoRoles) {
		t.Fatalf("no roles: %v", err)
	}
}

func TestAllListsInactiveAndLocked(t *testing.T) {
	s, _, _ := newStore(t)
	mustCreate(t, s, "ayu", "Ayu", Doctor)
	mustCreate(t, s, "budi", "Budi", Doctor)
	_ = s.SetActive(ctx, "budi", false)
	for i := 0; i < 5; i++ {
		_, _ = s.Authenticate(ctx, "ayu", "bad")
	}
	all, err := s.All(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("all=%v err=%v", all, err)
	}
	if !all[0].Locked || all[1].Active {
		t.Fatalf("flags wrong: %+v", all)
	}
}

func TestEmailsForRoleListsOnlyActiveStaffWithAValidAddress(t *testing.T) {
	// Reminders and digests go to the people who hold the role, so a leaver or a blank address
	// must never receive (or block) them.
	s, _, _ := newStore(t)
	mk := func(user, email string, roles ...Role) {
		t.Helper()
		if _, err := s.Create(ctx, user, user, email, "1234", roles); err != nil {
			t.Fatal(err)
		}
	}
	mk("anna", "Anna@Example.com", Stage1)
	mk("budi", "budi@example.com", Stage1, Final)
	mk("citra", "", Stage1)                 // no address: skipped
	mk("dewi", "not-an-address", Stage1)    // unusable address: skipped
	mk("eka", "eka@example.com", Stage1)    // switched off below
	mk("fajar", "anna@example.com", Stage1) // the same address twice: once
	mk("gita", "gita@example.com", Doctor)  // other role
	if err := s.SetActive(ctx, "eka", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.EmailsForRole(ctx, Stage1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"anna@example.com", "budi@example.com"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Stage1 = %v, want %v", got, want)
	}
	if got, _ := s.EmailsForRole(ctx, Admin); len(got) != 0 {
		t.Fatalf("Admin = %v, want none", got)
	}
}
