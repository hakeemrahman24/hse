package mcusubmit

import (
	"context"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

// storedDoc puts a real (fake-bytes) file in the store and a submission row pointing at it.
func storedDoc(t *testing.T, s *Store, files *memStore, sub int64, name string) (id int64, ref string) {
	t.Helper()
	ctx := context.Background()
	ref, err := files.Put(ctx, strings.NewReader("%PDF-1.7 "+name))
	if err != nil {
		t.Fatal(err)
	}
	in := sampleDoc(name)
	in.FilePath = ref
	id, err = s.AddSubmission(ctx, sub, in)
	if err != nil {
		t.Fatal(err)
	}
	return id, ref
}

func hasEvent(t *testing.T, s *Store, entity string, id int64, action string) bool {
	t.Helper()
	evs, err := s.Events(context.Background(), entity, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Action == action {
			return true
		}
	}
	return false
}

func TestExpireFilesDeletesOnlyFilesPastTheirDate(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	files := newMemStore()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	oldID, oldRef := storedDoc(t, s, files, sub, "Old Worker")
	clock.advance(364 * day)
	youngID, youngRef := storedDoc(t, s, files, sub, "Young Worker")

	// 732 days after the first upload: past its 2 years (731 days across the 2028 leap day), while
	// the second upload is only 368 days old.
	clock.advance((732 - 364) * day)
	n, err := s.ExpireFiles(ctx, files)
	if err != nil || n != 1 {
		t.Fatalf("expired %d, %v; want 1", n, err)
	}
	if _, err := files.Open(ctx, oldRef); err == nil {
		t.Error("the expired file is still in the store")
	}
	if _, err := files.Open(ctx, youngRef); err != nil {
		t.Errorf("a file inside its retention period was deleted: %v", err)
	}
	var path *string
	if err := s.DB.QueryRow(`SELECT FilePath FROM McuSubmission WHERE SubmissionID = ?`, oldID).Scan(&path); err != nil || path != nil {
		t.Errorf("FilePath of the expired row = %v (%v), want NULL", path, err)
	}
	var kept string
	if err := s.DB.QueryRow(`SELECT FileSha256 FROM McuSubmission WHERE SubmissionID = ?`, oldID).Scan(&kept); err != nil || kept == "" {
		t.Errorf("the audit stub lost its hash: %q %v", kept, err)
	}
	if !hasEvent(t, s, EntitySubmission, oldID, "FileExpired") || hasEvent(t, s, EntitySubmission, youngID, "FileExpired") {
		t.Error("FileExpired must be logged for the old row only")
	}
	if n, _ := s.ExpireFiles(ctx, files); n != 0 {
		t.Errorf("a second run expired %d more", n)
	}
}

func TestExpireFilesLeavesTheRowWhenTheStoreFails(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	files := newMemStore()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id, _ := storedDoc(t, s, files, sub, "Old Worker")
	clock.advance(3 * 365 * day)

	n, err := s.ExpireFiles(ctx, failingDeletes{files})
	if err == nil || n != 0 {
		t.Fatalf("n %d err %v, want 0 and an error", n, err)
	}
	var path *string
	_ = s.DB.QueryRow(`SELECT FilePath FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&path)
	if path == nil {
		t.Fatal("FilePath was cleared although the file was not deleted: the file would be orphaned for good")
	}
}

type failingDeletes struct{ *memStore }

func (failingDeletes) Delete(context.Context, string) error { return ErrScanUnavailable }

func TestEraseBlanksOnlyLongInactiveAccountsWithoutFiles(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	files := newMemStore()
	idle := activeSubmitter(t, s, co, "idle@example.com")
	holder := activeSubmitter(t, s, co, "holder@example.com")
	storedDoc(t, s, files, holder, "Worker One")
	clock.advance(300 * day)
	recent := activeSubmitter(t, s, co, "recent@example.com")

	// 1000 days after the first two sign-ups: idle and holder are past 2 years, recent (700 days) is not.
	clock.advance(700 * day)
	n, err := s.EraseInactiveAccounts(ctx)
	if err != nil || n != 1 {
		t.Fatalf("erased %d, %v; want only the idle account", n, err)
	}
	u, _ := s.ByID(ctx, idle)
	if u.Name != "" || u.Position != "" || u.Status != StatusDisabled || u.ErasedAt == "" || u.Email != "erased-"+itoa(idle)+"@invalid" {
		t.Errorf("idle account after erase = %+v", u)
	}
	if _, err := s.ByUsername(ctx, "idle"); err == nil {
		t.Error("the erased username can still be looked up")
	}
	if u, _ := s.ByID(ctx, holder); u.ErasedAt != "" {
		t.Error("an account that still has a file was erased")
	}
	if u, _ := s.ByID(ctx, recent); u.ErasedAt != "" {
		t.Error("an account inside the 2 years was erased")
	}
	if !hasEvent(t, s, EntitySubmitter, idle, "Erased") {
		t.Error("no Erased event")
	}
	if n, _ := s.EraseInactiveAccounts(ctx); n != 0 {
		t.Errorf("a second run erased %d more", n)
	}
	// The real address is free again.
	if _, err := s.Register(ctx, "idle@example.com", "New Person", co, "HR"); err != nil {
		t.Errorf("the erased address cannot be registered again: %v", err)
	}
}

func TestEraseNeverTouchesTheBuiltInAdmin(t *testing.T) {
	s, clock, _ := newTestStore(t)
	ctx := context.Background()
	mustAdmin(t, s)
	clock.advance(5 * 365 * day)
	if n, err := s.EraseInactiveAccounts(ctx); err != nil || n != 0 {
		t.Fatalf("erased %d, %v; the built-in admin must stay", n, err)
	}
}

func TestEraseUsesTheLastSignInNotTheSignUpDate(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	id := activeSubmitter(t, s, co, "regular@example.com")
	clock.advance(500 * day)
	if _, err := s.DB.Exec(`UPDATE Submitter SET LastLoginAt = ? WHERE SubmitterID = ?`, stampOf(clock.now()), id); err != nil {
		t.Fatal(err)
	}
	clock.advance(400 * day) // 900 days since sign-up, 400 since the last sign-in
	if n, _ := s.EraseInactiveAccounts(ctx); n != 0 {
		t.Fatalf("erased %d: an account that signed in 400 days ago is not inactive", n)
	}
}

func TestRetentionBlanksTheWorkersIdentityOnceTheFileIsGone(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	files := newMemStore()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	oldID, _ := storedDoc(t, s, files, sub, "Old Worker")
	// A withdrawn document loses its file at once, so only its age can tell when its identity goes.
	withdrawn, _ := storedDoc(t, s, files, sub, "Withdrawn Worker")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FilePath = NULL, Status = ? WHERE SubmissionID = ?`, SubWithdrawn, withdrawn); err != nil {
		t.Fatal(err)
	}
	clock.advance(364 * day)
	youngID, _ := storedDoc(t, s, files, sub, "Young Worker")

	clock.advance((732 - 364) * day)
	if _, _, err := s.RunRetention(ctx, files); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{oldID, withdrawn} {
		var name, dob, position string
		var nik *string
		if err := s.DB.QueryRow(`SELECT PatName, PatDateOfBirth, PatPosition, PatNik FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&name, &dob, &position, &nik); err != nil {
			t.Fatal(err)
		}
		if name != erasedName || dob != "" || nik != nil {
			t.Errorf("submission %d keeps identity: %q %q %v", id, name, dob, nik)
		}
		if position != "Welder" {
			t.Errorf("submission %d lost its position: %q", id, position)
		}
	}
	var name string
	_ = s.DB.QueryRow(`SELECT PatName FROM McuSubmission WHERE SubmissionID = ?`, youngID).Scan(&name)
	if name != "Young Worker" {
		t.Errorf("a document inside its retention period was blanked: %q", name)
	}
	// Running again changes nothing and must not fail.
	if _, _, err := s.RunRetention(ctx, files); err != nil {
		t.Fatal(err)
	}
}
