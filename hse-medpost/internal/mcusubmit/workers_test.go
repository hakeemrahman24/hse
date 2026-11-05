package mcusubmit

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func seedWorker(t *testing.T, s *Store, name, dob string, companyID int64) int64 {
	t.Helper()
	var company any
	if companyID != 0 {
		company = companyID
	}
	res, err := s.DB.Exec(`INSERT INTO Patient (Name, DateOfBirth, WorkPosition, FK_CompanyID) VALUES (?, ?, 'Welder', ?)`, name, dob, company)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestSearchWorkersMatchesPartsOfTheNameInAnyCase(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	want := seedWorker(t, s, "Siti Aminah", "1990-05-17", co)
	seedWorker(t, s, "Budi Santoso", "1985-01-02", co)

	for _, q := range []string{"ami", "SITI", "siti ami", "  aminah  "} {
		got, err := s.SearchWorkers(ctx, q)
		if err != nil || len(got) != 1 || got[0].PatientID != want {
			t.Errorf("SearchWorkers(%q) = %+v, %v; want only Siti Aminah", q, got, err)
		}
	}
	got, _ := s.SearchWorkers(ctx, "siti")
	if got[0].Company != "PT Example" || got[0].DateOfBirth != "1990-05-17" || got[0].Position != "Welder" {
		t.Errorf("worker details = %+v", got[0])
	}
}

// A short or wildcard-only query must not list the table: this list is patient data.
func TestSearchWorkersRefusesShortAndWildcardQueries(t *testing.T) {
	s, _, co := newTestStore(t)
	seedWorker(t, s, "Siti Aminah", "1990-05-17", co)
	for _, q := range []string{"", "s", "si", "%", "%%%", "___", "% %", "_i_"} {
		if got, err := s.SearchWorkers(context.Background(), q); err != nil || len(got) != 0 {
			t.Errorf("SearchWorkers(%q) = %+v, %v; want nothing", q, got, err)
		}
	}
}

func TestSearchWorkersReturnsAtMostTenAndToleratesAMissingCompany(t *testing.T) {
	s, _, _ := newTestStore(t)
	for i := 0; i < 12; i++ {
		seedWorker(t, s, fmt.Sprintf("Agus Test %02d", i), "1990-01-01", 0)
	}
	got, err := s.SearchWorkers(context.Background(), "agus")
	if err != nil || len(got) != maxWorkerResults {
		t.Fatalf("got %d, %v; want %d", len(got), err, maxWorkerResults)
	}
	if got[0].Company != "" {
		t.Errorf("company = %q, want empty for a worker without one", got[0].Company)
	}
}

func TestWorkerByIDAndWorkerExists(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := seedWorker(t, s, "Siti Aminah", "1990-05-17", co)

	w, err := s.WorkerByID(ctx, id)
	if err != nil || w.Name != "Siti Aminah" || w.CompanyID != co {
		t.Fatalf("WorkerByID = %+v, %v", w, err)
	}
	if _, err := s.WorkerByID(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing worker = %v, want ErrNotFound", err)
	}
	for _, c := range []struct {
		name, dob string
		want      bool
	}{
		{"Siti Aminah", "1990-05-17", true},
		{"  siti aminah ", "1990-05-17", true},
		{"Siti Aminah", "1990-05-18", false},
		{"Siti Aminahh", "1990-05-17", false},
	} {
		if got, err := s.WorkerExists(ctx, c.name, c.dob); err != nil || got != c.want {
			t.Errorf("WorkerExists(%q, %s) = %v, %v; want %v", c.name, c.dob, got, err, c.want)
		}
	}
}

// ---- review step -----------------------------------------------------------------------------

func addForReview(t *testing.T, s *Store, sub int64, name string) int64 {
	t.Helper()
	d := sampleDoc(name)
	d.NeedsReview = true
	id, err := s.AddSubmission(context.Background(), sub, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetScanResult(context.Background(), id, ScanClean); err != nil {
		t.Fatal(err)
	}
	return id
}

func statusOf(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var st string
	if err := s.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func batchOf(t *testing.T, s *Store, id int64) int64 {
	t.Helper()
	var b int64
	if err := s.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnUnconfirmedDocumentBlocksSendUntilItIsConfirmed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	confirmed := addClean(t, s, sub, "Worker One")
	review := addForReview(t, s, sub, "Worker Two")
	if statusOf(t, s, review) != SubReview {
		t.Fatalf("status = %s, want Review", statusOf(t, s, review))
	}
	batch := batchOf(t, s, confirmed)
	if _, err := s.SendBatch(ctx, sub, batch); !errors.Is(err, ErrBadState) {
		t.Fatalf("send with an unconfirmed document = %v, want ErrBadState", err)
	}
	if err := s.ConfirmDraft(ctx, sub, review); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SendBatch(ctx, sub, batch); err != nil || n != 2 {
		t.Fatalf("send after confirming = %d, %v; want 2 documents", n, err)
	}
}

func TestReviewDocumentsNeverReachStaff(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := addForReview(t, s, sub, "Worker One")
	for _, status := range []string{"", SubReview, SubDraft} {
		if rows, err := s.StaffQueue(ctx, status); err != nil || len(rows) != 0 {
			t.Errorf("StaffQueue(%q) = %d rows, %v; want none", status, len(rows), err)
		}
	}
	if _, err := s.StaffFile(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("StaffFile of a Review document = %v, want ErrNotFound", err)
	}
	if _, err := s.OwnFile(ctx, Scope{SubmitterID: sub}, id); err != nil {
		t.Errorf("the submitter must be able to open the file to review it: %v", err)
	}
}

func TestConfirmDraftRulesAndRemovingAReviewDocument(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	owner := activeSubmitter(t, s, co, "hrone@example.com")
	other := activeSubmitter(t, s, co, "hrtwo@example.com")
	id := addForReview(t, s, owner, "Worker One")

	if err := s.ConfirmDraft(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("another submitter confirming = %v, want ErrNotFound", err)
	}
	if err := s.ConfirmDraft(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmDraft(ctx, owner, id); !errors.Is(err, ErrBadState) {
		t.Errorf("confirming twice = %v, want ErrBadState", err)
	}
	review := addForReview(t, s, owner, "Worker Two")
	if _, err := s.RemoveDraft(ctx, owner, review); err != nil {
		t.Errorf("discarding a document under review: %v", err)
	}
}
