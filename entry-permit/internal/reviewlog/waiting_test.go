package reviewlog

import (
	"context"
	"testing"

	"entry-permit/internal/permit"
	"entry-permit/internal/waiver"
)

func addStep(t *testing.T, id, stage, at string, exec func(string, ...any) error) {
	t.Helper()
	if err := exec(`INSERT INTO review_step (subject_id, stage, reviewer, decision, at) VALUES (?, ?, 'Rev', 'approved', ?)`, id, stage, at); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingCountsEachRequestFromTheMomentItReachedItsStage(t *testing.T) {
	// Legacy rule (rules doc section 7): WEP at Stage 1 waits since it was submitted, WEP at Final since
	// Stage 1 approved; a waiver at the Doctor since submit, at Stage 1 since the Doctor, at Final since Stage 1.
	s, d := newStore(t)
	exec := func(q string, a ...any) error { _, err := d.Exec(q, a...); return err }
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingStage1, 1, "", "", "2027-01-31") // submitted 2026-11-06 08:00
	addPermit(t, d, "WEP-BBBBB", permit.StatusPendingFinal, 2, "", "", "2027-01-31")
	addStep(t, "WEP-BBBBB", "stage1", "2026-11-06 09:00:00", exec)
	addPermit(t, d, "WEP-CCCCC", permit.StatusPendingFinal, 3, "", "", "2027-01-31") // no step row: falls back to submit time
	addWaiver(t, d, "WVR-DOCTR")
	addWaiver(t, d, "WVR-STAGE")
	addWaiver(t, d, "WVR-FINAL")
	for id, st := range map[string]string{"WVR-STAGE": waiver.StatusPendingStage1, "WVR-FINAL": waiver.StatusPendingSiteManager} {
		if err := exec(`UPDATE waiver SET status = ? WHERE request_id = ?`, st, id); err != nil {
			t.Fatal(err)
		}
	}
	addStep(t, "WVR-STAGE", "doctor", "2026-11-06 10:00:00", exec)
	addStep(t, "WVR-FINAL", "stage1", "2026-11-06 11:00:00", exec)
	// not waiting: already approved
	addPermit(t, d, "WEP-DONE1", permit.StatusApproved, 4, "", "LEP-DONE1", "2027-01-31")

	got, err := s.Waiting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type k struct{ stage, id string }
	since := map[k]string{}
	for _, w := range got {
		since[k{w.Stage, w.ID}] = w.Since
		if w.Name == "" || w.Company == "" {
			t.Errorf("%s has no name or company: %+v", w.ID, w)
		}
	}
	want := map[k]string{
		{"stage1", "WEP-AAAAA"}: "2026-11-06 08:00:00",
		{"final", "WEP-BBBBB"}:  "2026-11-06 09:00:00",
		{"final", "WEP-CCCCC"}:  "2026-11-06 08:00:00",
		{"doctor", "WVR-DOCTR"}: "2026-11-06 08:00:00",
		{"stage1", "WVR-STAGE"}: "2026-11-06 10:00:00",
		{"final", "WVR-FINAL"}:  "2026-11-06 11:00:00",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d waiting, want %d: %+v", len(got), len(want), got)
	}
	for key, at := range want {
		if since[key] != at {
			t.Errorf("%v since %q, want %q", key, since[key], at)
		}
	}
}
