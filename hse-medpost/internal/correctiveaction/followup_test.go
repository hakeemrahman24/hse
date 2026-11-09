package correctiveaction

import (
	"errors"
	"strings"
	"testing"
)

func (f fixture) followUpInput(desc string) FollowUpInput {
	return FollowUpInput{Description: desc, PicPatientID: f.patient2ID, DueDate: "2026-11-15"}
}

func (f fixture) addFollowUp(t *testing.T, actionID int64, desc string) int64 {
	t.Helper()
	id, err := f.store.AddFollowUp(actionID, f.followUpInput(desc), f.staffID)
	if err != nil {
		t.Fatalf("AddFollowUp(%q): %v", desc, err)
	}
	return id
}

func TestFollowUpHasItsOwnPicAndDueDate(t *testing.T) {
	f := newFixture(t)
	id := f.create(t) // the finding's PIC is Budi Santoso
	f.addFollowUp(t, id, "Buy shelves")

	got, err := f.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FollowUps) != 1 {
		t.Fatalf("follow-ups = %+v", got.FollowUps)
	}
	fu := got.FollowUps[0]
	if fu.PicName != "Budiman Hakim" || fu.PicEmployeeID != "E-002" || fu.DueDate != "2026-11-15" || fu.Status != FollowUpOpen {
		t.Fatalf("follow-up = %+v, want its own PIC, due date and Open status", fu)
	}
	if got.PicName != "Budi Santoso" {
		t.Fatalf("the finding's PIC changed to %q", got.PicName)
	}
	if got.FollowUpsTotal != 1 || got.FollowUpsDone != 0 || got.NextFollowUpDue != "2026-11-15" {
		t.Fatalf("summary = %d/%d next %q", got.FollowUpsDone, got.FollowUpsTotal, got.NextFollowUpDue)
	}
}

func TestAddFollowUpRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	cases := []struct {
		name   string
		mutate func(*FollowUpInput)
	}{
		{"no description", func(in *FollowUpInput) { in.Description = "  " }},
		{"no PIC picked", func(in *FollowUpInput) { in.PicPatientID = 0 }},
		{"PIC not in the employee list", func(in *FollowUpInput) { in.PicPatientID = 9999 }},
		{"bad due date", func(in *FollowUpInput) { in.DueDate = "15/11/2026" }},
		{"due before the inspection date", func(in *FollowUpInput) { in.DueDate = "2026-10-01" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := f.followUpInput("Buy shelves")
			c.mutate(&in)
			if _, err := f.store.AddFollowUp(id, in, f.staffID); !isValidation(err) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
		})
	}
	if _, err := f.store.AddFollowUp(9999, f.followUpInput("x"), f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown finding error = %v", err)
	}
}

func TestFindingStatusFollowsItsFollowUps(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	first := f.addFollowUp(t, id, "Buy shelves")
	second := f.addFollowUp(t, id, "Install shelves")

	status := func() string {
		got, _ := f.store.Get(id)
		return got.Status
	}
	if status() != StatusOpen {
		t.Fatalf("no follow-up done: status = %s, want Open", status())
	}
	if err := f.store.CompleteFollowUp(first, "2026-11-05", "Shelves bought", f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); got.Status != StatusInProgress || got.FollowUpsDone != 1 || got.FollowUpsTotal != 2 {
		t.Fatalf("one of two done: %s %d/%d, want In Progress 1/2", got.Status, got.FollowUpsDone, got.FollowUpsTotal)
	}
	if err := f.store.CompleteFollowUp(second, "2026-11-08", "Installed", f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); got.Status == StatusClosed || got.FollowUpsDone != 2 {
		t.Fatalf("all follow-ups done must not close the finding by itself: %s", got.Status)
	}
	if err := f.store.ReopenFollowUp(first, f.staffID); err != nil {
		t.Fatal(err)
	}
	if status() != StatusInProgress {
		t.Fatalf("one reopened: status = %s, want In Progress", status())
	}
	if err := f.store.ReopenFollowUp(second, f.staffID); err != nil {
		t.Fatal(err)
	}
	if status() != StatusOpen {
		t.Fatalf("both reopened: status = %s, want Open", status())
	}
}

func TestFindingCannotCloseWhileAFollowUpIsOpen(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	first := f.addFollowUp(t, id, "Buy shelves")
	f.addFollowUp(t, id, "Install shelves")
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	f.store.CompleteFollowUp(first, "2026-11-05", "Bought", f.staffID)

	err := f.store.Close(id, "2026-11-10", "Done", f.staffID)
	if !isValidation(err) || !strings.Contains(err.Error(), "Install shelves") {
		t.Fatalf("error = %v, want a ValidationError that names the open follow-up", err)
	}
	if got, _ := f.store.Get(id); got.Status == StatusClosed {
		t.Fatal("finding was closed with a follow-up still open")
	}
}

func TestFindingClosesOnceEveryFollowUpIsDone(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	first := f.addFollowUp(t, id, "Buy shelves")
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	f.store.CompleteFollowUp(first, "2026-11-05", "Bought", f.staffID)
	if err := f.store.Close(id, "2026-11-10", "Done", f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); got.Status != StatusClosed {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestClosedFindingLocksItsFollowUps(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	first := f.addFollowUp(t, id, "Buy shelves")
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	f.store.CompleteFollowUp(first, "2026-11-05", "Bought", f.staffID)
	f.store.Close(id, "2026-11-10", "Done", f.staffID)

	if _, err := f.store.AddFollowUp(id, f.followUpInput("More"), f.staffID); !isValidation(err) {
		t.Fatalf("add to closed finding: error = %v", err)
	}
	if err := f.store.ReopenFollowUp(first, f.staffID); !isValidation(err) {
		t.Fatalf("reopen follow-up of closed finding: error = %v", err)
	}
	if err := f.store.DeleteFollowUp(first, f.staffID); !isValidation(err) {
		t.Fatalf("delete follow-up of closed finding: error = %v", err)
	}
	// Reopening the finding unlocks them again.
	if err := f.store.Reopen(id, f.staffID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AddFollowUp(id, f.followUpInput("More"), f.staffID); err != nil {
		t.Fatalf("add after reopening the finding: %v", err)
	}
}

func TestCompleteFollowUpRules(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	fu := f.addFollowUp(t, id, "Buy shelves")

	if err := f.store.CompleteFollowUp(fu, "2026-11-05", "  ", f.staffID); !isValidation(err) {
		t.Fatalf("no action taken: error = %v", err)
	}
	if err := f.store.CompleteFollowUp(fu, "2026-10-01", "Bought", f.staffID); !isValidation(err) {
		t.Fatalf("done before the inspection: error = %v", err)
	}
	if err := f.store.CompleteFollowUp(fu, "", "Bought", f.staffID); err != nil {
		t.Fatalf("blank date should default to today: %v", err)
	}
	got, _ := f.store.Get(id)
	if got.FollowUps[0].Status != FollowUpDone || len(got.FollowUps[0].DoneDate) != 10 || got.FollowUps[0].DoneByName != "Nurse A" {
		t.Fatalf("follow-up = %+v", got.FollowUps[0])
	}
	if err := f.store.CompleteFollowUp(fu, "2026-11-06", "again", f.staffID); !isValidation(err) {
		t.Fatalf("completing twice: error = %v", err)
	}
	if err := f.store.CompleteFollowUp(9999, "2026-11-06", "x", f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown follow-up error = %v", err)
	}
}

func TestUpdateFollowUpKeepsPicWhenNoneIsPicked(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	fu := f.addFollowUp(t, id, "Buy shelves")

	in := FollowUpInput{Description: "Buy steel shelves", DueDate: "2026-11-18"}
	if err := f.store.UpdateFollowUp(fu, in, f.staffID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if row := got.FollowUps[0]; row.Description != "Buy steel shelves" || row.DueDate != "2026-11-18" || row.PicName != "Budiman Hakim" {
		t.Fatalf("follow-up = %+v", row)
	}
	in.PicPatientID = f.patientID
	if err := f.store.UpdateFollowUp(fu, in, f.staffID); err != nil {
		t.Fatal(err)
	}
	in.PicPatientID = 9999
	if err := f.store.UpdateFollowUp(fu, in, f.staffID); !isValidation(err) {
		t.Fatalf("unlisted PIC: error = %v", err)
	}
}

func TestOverdueFollowUpMakesTheFindingOverdue(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.DueDate = "2999-01-01" // the finding itself is not overdue
	id, _ := f.store.Create(in, f.staffID)
	late := FollowUpInput{Description: "Buy shelves", PicPatientID: f.patient2ID, DueDate: "2026-11-02"}
	fu, err := f.store.AddFollowUp(id, late, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	f.addFollowUp(t, id, "Later step") // due 2026-11-15, also past; the earlier one is the next due

	got, _ := f.store.Get(id)
	if !got.IsOverdue || !got.FollowUps[0].IsOverdue || got.NextFollowUpDue != "2026-11-02" {
		t.Fatalf("overdue = %v / %v, next due %q", got.IsOverdue, got.FollowUps[0].IsOverdue, got.NextFollowUpDue)
	}
	if overdue, _ := f.store.List(Filter{Status: StatusOverdue}); len(overdue) != 1 || overdue[0].ID != id {
		t.Fatalf("Overdue filter must include a finding with an overdue follow-up: %+v", overdue)
	}

	f.store.CompleteFollowUp(fu, "2026-11-04", "Done", f.staffID)
	got, _ = f.store.Get(id)
	if got.FollowUps[0].IsOverdue {
		t.Fatal("a Done follow-up is never overdue")
	}
}

func TestDeleteFollowUpRecomputesStatus(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	done := f.addFollowUp(t, id, "Buy shelves")
	open := f.addFollowUp(t, id, "Install shelves")
	f.store.CompleteFollowUp(done, "2026-11-05", "Bought", f.staffID)

	if err := f.store.DeleteFollowUp(open, f.staffID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if got.FollowUpsTotal != 1 || got.FollowUpsDone != 1 || got.Status != StatusInProgress {
		t.Fatalf("after delete: %d/%d %s", got.FollowUpsDone, got.FollowUpsTotal, got.Status)
	}
	if err := f.store.DeleteFollowUp(open, f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}

func TestManualStatusIsIgnoredWhenFollowUpsExist(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	f.addFollowUp(t, id, "Buy shelves")
	in := f.input()
	in.Status = StatusInProgress
	if err := f.store.Update(id, in, f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); got.Status != StatusOpen {
		t.Fatalf("status = %s, want it computed from the follow-ups (Open), not typed", got.Status)
	}
}

func TestFollowUpChangesAreLoggedInHistory(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	fu := f.addFollowUp(t, id, "Buy shelves")
	f.store.CompleteFollowUp(fu, "2026-11-05", "Bought", f.staffID)
	f.store.ReopenFollowUp(fu, f.staffID)
	f.store.DeleteFollowUp(fu, f.staffID)

	hist, _ := f.store.History(id)
	seen := map[string]bool{}
	for _, h := range hist {
		seen[h.Field] = true
	}
	for _, want := range []string{"follow_up_added", "follow_up_done", "follow_up_reopened", "follow_up_removed"} {
		if !seen[want] {
			t.Fatalf("history %+v is missing %q", hist, want)
		}
	}
}

func TestDeleteFindingRemovesItsFollowUps(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	f.addFollowUp(t, id, "Buy shelves")
	if _, err := f.store.Delete(id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionFollowUpTable`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("follow-up rows = %d, %v; want 0", n, err)
	}
}
