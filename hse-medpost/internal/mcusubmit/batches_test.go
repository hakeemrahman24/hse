package mcusubmit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// activeSubmitter returns a submitter who can sign in, with a username made from the email.
func activeSubmitter(t *testing.T, s *Store, companyID int64, email string) int64 {
	t.Helper()
	id := approved(t, s, companyID, email)
	hash, _ := HashPassword("pelican-orbit-77")
	if err := s.CompleteSetup(context.Background(), id, strings.Split(email, "@")[0], hash); err != nil {
		t.Fatal(err)
	}
	return id
}

func sampleDoc(name string) NewSubmission {
	return NewSubmission{
		PatName: name, PatDateOfBirth: "1990-04-12", PatPosition: "Welder", PatCompanyText: "PT Example",
		PatNationality: "Indonesia", PatNik: "3201011204900001",
		FilePath: "/quarantine/" + strings.ReplaceAll(name, " ", "_") + ".pdf", FileSha256: strings.Repeat("a", 64), FileBytes: 500000,
	}
}

// addClean adds a document and marks its scan Clean, the normal happy path.
func addClean(t *testing.T, s *Store, submitterID int64, name string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.AddSubmission(ctx, submitterID, sampleDoc(name))
	if err != nil {
		t.Fatalf("AddSubmission(%s): %v", name, err)
	}
	if err := s.SetScanResult(ctx, id, ScanClean); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOpenBatchReusesTheUnsentBatchAndNeedsAnActiveAccount(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")

	first, err := s.OpenBatch(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.OpenBatch(ctx, sub)
	if again != first {
		t.Fatalf("second OpenBatch = %d, want the same open batch %d", again, first)
	}
	requested := register(t, s, co, "new@example.com")
	if _, err := s.OpenBatch(ctx, requested); !errors.Is(err, ErrBadState) {
		t.Fatalf("OpenBatch for a Requested account = %v, want ErrBadState", err)
	}
}

func TestAddSubmissionStartsAsAScanningDraftInTheOpenBatch(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")

	id, err := s.AddSubmission(ctx, sub, sampleDoc("Andi Wijaya"))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListOwn(ctx, sub)
	if len(list) != 1 || list[0].ID != id || list[0].Status != SubDraft || list[0].ScanStatus != ScanScanning {
		t.Fatalf("ListOwn = %+v", list)
	}
	// Retention: 2 years from upload, stored per row so a later policy change rewrites nothing.
	want := stampOf(clock.t.AddDate(2, 0, 0))
	if list[0].FileExpiresAt != want {
		t.Fatalf("FileExpiresAt = %q, want %q", list[0].FileExpiresAt, want)
	}
}

func TestAddSubmissionStopsAtTheDailyLimit(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")

	for i := 0; i < maxUploadsPerDay; i++ {
		if _, err := s.AddSubmission(ctx, sub, sampleDoc("Worker")); err != nil {
			t.Fatalf("upload %d: %v", i+1, err)
		}
	}
	if _, err := s.AddSubmission(ctx, sub, sampleDoc("One too many")); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("over the limit = %v, want ErrDailyLimit", err)
	}
}

func TestSendNeedsADocumentAndNoUnfinishedScan(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	batch, _ := s.OpenBatch(ctx, sub)

	if _, err := s.SendBatch(ctx, sub, batch); !errors.Is(err, ErrBadState) {
		t.Fatalf("Send of an empty batch = %v, want ErrBadState", err)
	}
	scanning, _ := s.AddSubmission(ctx, sub, sampleDoc("Still Scanning"))
	if _, err := s.SendBatch(ctx, sub, batch); !errors.Is(err, ErrBadState) {
		t.Fatalf("Send while Scanning = %v, want ErrBadState", err)
	}
	if err := s.SetScanResult(ctx, scanning, ScanError); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendBatch(ctx, sub, batch); !errors.Is(err, ErrBadState) {
		t.Fatalf("Send with a ScanError document = %v, want ErrBadState", err)
	}
}

func TestSendMovesEveryDocumentToPendingAndLocksTheBatch(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	a, b := addClean(t, s, sub, "Andi Wijaya"), addClean(t, s, sub, "Siti Rahma")
	batch, _ := s.OpenBatch(ctx, sub)

	n, err := s.SendBatch(ctx, sub, batch)
	if err != nil || n != 2 {
		t.Fatalf("SendBatch = %d, %v; want 2, nil", n, err)
	}
	list, _ := s.ListOwn(ctx, sub)
	for _, d := range list {
		if (d.ID == a || d.ID == b) && d.Status != SubPending {
			t.Fatalf("document %d is %s, want Pending", d.ID, d.Status)
		}
	}
	if _, err := s.SendBatch(ctx, sub, batch); !errors.Is(err, ErrBadState) {
		t.Fatalf("sending twice = %v, want ErrBadState", err)
	}
	// A sent batch is locked: the next upload starts a new one.
	next, _ := s.AddSubmission(ctx, sub, sampleDoc("Later Worker"))
	newBatch, _ := s.OpenBatch(ctx, sub)
	if newBatch == batch {
		t.Fatal("after Send a new batch must open")
	}
	got, _ := s.ListOwn(ctx, sub)
	for _, d := range got {
		if d.ID == next && d.BatchID != newBatch {
			t.Fatal("a late upload went into the locked batch")
		}
	}
	events, _ := s.Events(ctx, EntityBatch, batch)
	if len(events) != 1 || events[0].Action != "Sent" || events[0].ActorID != sub {
		t.Fatalf("Sent event = %+v", events)
	}
}

func TestASubmitterCannotSendAnotherSubmittersBatch(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	owner := activeSubmitter(t, s, co, "owner@example.com")
	other := activeSubmitter(t, s, co, "other@example.com")
	addClean(t, s, owner, "Andi Wijaya")
	batch, _ := s.OpenBatch(ctx, owner)

	if _, err := s.SendBatch(ctx, other, batch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign Send = %v, want ErrNotFound", err)
	}
}

func TestWithdrawWorksOnlyWhilePendingAndDeletesTheFile(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	id := addClean(t, s, sub, "Andi Wijaya")

	if _, err := s.Withdraw(ctx, sub, id); !errors.Is(err, ErrBadState) {
		t.Fatalf("Withdraw of a Draft = %v, want ErrBadState (drafts are removed, not withdrawn)", err)
	}
	batch, _ := s.OpenBatch(ctx, sub)
	if _, err := s.SendBatch(ctx, sub, batch); err != nil {
		t.Fatal(err)
	}
	path, err := s.Withdraw(ctx, sub, id)
	if err != nil || path != "/quarantine/Andi_Wijaya.pdf" {
		t.Fatalf("Withdraw = %q, %v; the caller needs the path to delete the file", path, err)
	}
	list, _ := s.ListOwn(ctx, sub)
	if list[0].Status != SubWithdrawn || list[0].HasFile || list[0].WithdrawnAt == "" {
		t.Fatalf("after withdraw: %+v", list[0])
	}
	if _, err := s.OwnFile(ctx, sub, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file route after withdraw = %v, want ErrNotFound", err)
	}
	if _, err := s.Withdraw(ctx, sub, id); !errors.Is(err, ErrBadState) {
		t.Fatalf("withdrawing twice = %v, want ErrBadState", err)
	}
	events, _ := s.Events(ctx, EntitySubmission, id)
	if len(events) != 2 || events[1].Action != "Withdrawn" || events[1].ActorID != sub {
		t.Fatalf("events = %+v, want Uploaded then Withdrawn", events)
	}
}

func TestWithdrawAndRemoveRefuseAnotherSubmittersDocument(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	owner := activeSubmitter(t, s, co, "owner@example.com")
	other := activeSubmitter(t, s, co, "other@example.com")
	id := addClean(t, s, owner, "Andi Wijaya")

	if _, err := s.RemoveDraft(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign RemoveDraft = %v, want ErrNotFound", err)
	}
	if _, err := s.Withdraw(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign Withdraw = %v, want ErrNotFound", err)
	}
}

func TestRemoveDraftDeletesTheRowAndReturnsThePath(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	id := addClean(t, s, sub, "Andi Wijaya")

	path, err := s.RemoveDraft(ctx, sub, id)
	if err != nil || path == "" {
		t.Fatalf("RemoveDraft = %q, %v", path, err)
	}
	if list, _ := s.ListOwn(ctx, sub); len(list) != 0 {
		t.Fatalf("draft still listed: %+v", list)
	}
	pending := addClean(t, s, sub, "Siti Rahma")
	batch, _ := s.OpenBatch(ctx, sub)
	_, _ = s.SendBatch(ctx, sub, batch)
	if _, err := s.RemoveDraft(ctx, sub, pending); !errors.Is(err, ErrBadState) {
		t.Fatalf("removing a Pending document = %v, want ErrBadState", err)
	}
}

func TestListOwnShowsOnlyTheSignedInSubmittersRows(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "first@example.com")
	b := activeSubmitter(t, s, co, "second@example.com")
	addClean(t, s, a, "Worker A")
	addClean(t, s, b, "Worker B")

	list, _ := s.ListOwn(ctx, a)
	if len(list) != 1 || list[0].PatName != "Worker A" {
		t.Fatalf("ListOwn(a) = %+v", list)
	}
}

func TestStaffQueueHidesDraftsAndWithdrawnByDefault(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	kept := addClean(t, s, sub, "Kept Pending")
	gone := addClean(t, s, sub, "Gets Withdrawn")
	batch, _ := s.OpenBatch(ctx, sub)
	_, _ = s.SendBatch(ctx, sub, batch)
	_, _ = s.Withdraw(ctx, sub, gone)
	addClean(t, s, sub, "Unsent Draft") // new open batch, never sent

	queue, err := s.StaffQueue(ctx, "")
	if err != nil || len(queue) != 1 || queue[0].ID != kept {
		t.Fatalf("StaffQueue = %+v, %v; want only the Pending one", queue, err)
	}
	if queue[0].SubmitterName == "" {
		t.Fatal("the staff list needs the submitter's name")
	}
	withdrawn, _ := s.StaffQueue(ctx, SubWithdrawn)
	if len(withdrawn) != 1 || withdrawn[0].ID != gone {
		t.Fatalf("withdrawn filter = %+v", withdrawn)
	}
}

func TestFileRoutesRefuseUnscannedAndOtherPeoplesFiles(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	owner := activeSubmitter(t, s, co, "owner@example.com")
	other := activeSubmitter(t, s, co, "other@example.com")
	scanning, _ := s.AddSubmission(ctx, owner, sampleDoc("Not Scanned Yet"))
	clean := addClean(t, s, owner, "Scanned Clean")

	if _, err := s.OwnFile(ctx, owner, scanning); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unscanned own file = %v, want ErrNotFound", err)
	}
	if p, err := s.OwnFile(ctx, owner, clean); err != nil || p == "" {
		t.Fatalf("clean own draft = %q, %v; a submitter may check their own drafts", p, err)
	}
	// Same answer for someone else's id as for a missing one: never confirm the id exists.
	if _, err := s.OwnFile(ctx, other, clean); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign file = %v, want ErrNotFound", err)
	}
	if _, err := s.StaffFile(ctx, clean); !errors.Is(err, ErrNotFound) {
		t.Fatalf("staff must not open a Draft, got %v", err)
	}
}

func TestStaffFileOpensASentCleanDocument(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	id := addClean(t, s, sub, "Andi Wijaya")
	batch, _ := s.OpenBatch(ctx, sub)
	_, _ = s.SendBatch(ctx, sub, batch)

	if p, err := s.StaffFile(ctx, id); err != nil || p == "" {
		t.Fatalf("StaffFile = %q, %v", p, err)
	}
}

func TestRemoveInfectedDeletesTheRowAndKeepsOnlyAnEvent(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "budi@example.com")
	id, _ := s.AddSubmission(ctx, sub, sampleDoc("Bad File"))

	if err := s.RemoveInfected(ctx, sub, id, "Eicar-Test-Signature"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListOwn(ctx, sub); len(list) != 0 {
		t.Fatalf("infected row still listed: %+v", list)
	}
	events, _ := s.Events(ctx, EntitySubmission, id)
	if len(events) != 2 {
		t.Fatalf("events = %+v, want Uploaded then Infected", events)
	}
	last := events[1]
	if last.Action != "Infected" || last.Detail != "Eicar-Test-Signature" || last.ActorType != ActorSystem {
		t.Fatalf("Infected event = %+v", last)
	}
	for _, e := range events {
		if strings.Contains(e.Detail, "Bad File") {
			t.Fatal("an event must not carry the worker's name")
		}
	}
}
