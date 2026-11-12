package mcusubmit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The More panel shows the worker's latest checkup on record, because HR sometimes never submits an update.
func TestLatestRecordMatchesByNameAndBirthDateAndPicksTheNewestCheckup(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Agus Prasetyo") // typed date of birth 1990-04-12

	same := seedWorker(t, s, "agus  prasetyo ", "1990-04-12", co)   // spacing and case differ: still the same person
	sameName := seedWorker(t, s, "Agus Prasetyo", "1971-01-01", co) // same name, other birth date: a different person
	addRecord(t, s, same, "MedC-OLD", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, same, "MedC-NEW", "FitWithNote", "2027-06-01", "Open", 1)
	addRecord(t, s, sameName, "MedC-NAMESAKE", "Unfit", "2028-01-01", "Open", 1)

	got, err := s.LatestRecord(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CheckUpID != "MedC-NEW" || got.Result != "FitWithNote" {
		t.Fatalf("latest = %+v, want MedC-NEW (newest of this person, never the namesake)", got)
	}
}

func TestLatestRecordIsNilWhenNothingMatchesAndUsesThePickedPatientById(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Worker One")
	if got, err := s.LatestRecord(ctx, id); err != nil || got != nil {
		t.Fatalf("no patient = %+v, %v; want nil, nil", got, err)
	}
	picked := seedWorker(t, s, "Worker One", "1999-09-09", co) // birth date differs from the typed one
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, picked, id); err != nil {
		t.Fatal(err)
	}
	addRecord(t, s, picked, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	if got, err := s.LatestRecord(ctx, id); err != nil || got == nil || got.CheckUpID != "MedC-1" {
		t.Fatalf("picked patient = %+v, %v; want MedC-1 (a picked patient is matched by id)", got, err)
	}
	if _, err := s.LatestRecord(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}

func TestStaffListCarriesTheLatestCheckupOfEachWorker(t *testing.T) {
	e := newStaffEnv(t)
	e.doc(t, "Worker One", true)
	e.doc(t, "Worker Two", true)
	w := seedWorker(t, e.store, "Worker One", "1990-04-12", 0)
	addRecord(t, e.store, w, "MedC-1", "Unfit", "2027-01-01", "Open", 1)
	rec := e.do("GET", "/api/mcu-submissions", "", false)
	var rows []struct {
		Worker string        `json:"worker"`
		Latest *LatestRecord `json:"latest"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != 2 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	for _, r := range rows {
		switch {
		case r.Worker == "Worker One" && (r.Latest == nil || r.Latest.CheckUpID != "MedC-1" || r.Latest.Result != "Unfit"):
			t.Errorf("Worker One latest = %+v, want MedC-1 Unfit", r.Latest)
		case r.Worker == "Worker Two" && r.Latest != nil:
			t.Errorf("Worker Two latest = %+v, want null (no checkup on record)", r.Latest)
		}
	}
}

// The Submitted tab offers a certificate only for a checkup the MCU tab has one for: Unfit always, Fit
// once Close, and nothing for a result that has no certificate.
func TestLatestRecordSaysWhetherTheMcuTabHasACertificate(t *testing.T) {
	for _, c := range []struct {
		result, status string
		want           bool
	}{{"Unfit", "Open", true}, {"TemporaryUnfit", "Open", true}, {"Fit", "Close", true}, {"FitWithNote", "Open", false}, {"NeedFollowUp", "Close", false}} {
		if got := (LatestRecord{Result: c.result, Status: c.status}).HasCertificate(); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.result, c.status, got, c.want)
		}
	}
}
