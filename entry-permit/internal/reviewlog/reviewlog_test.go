package reviewlog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
	"entry-permit/migrations"
)

var pol = policy.Default() // MCU grace 2 weeks, rejection note 3 words

func newStore(t *testing.T) (Store, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	return Store{DB: d}, d
}

// addPermit inserts a permit; mcu is its MCU valid-until day.
func addPermit(t *testing.T, d *sql.DB, id, status string, patient int64, personal, lep, mcu string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, personal_id, worker_name, company, nationality,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, id_doc, created_at)
		VALUES (?, ?, ?, ?, ?, 'Budi Santoso', 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore', ?, '', ?, '2026-11-06 08:00:00')`,
		id, lep, status, patient, personal, mcu, id+"/id_doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, d *sql.DB, table, key, id string) string {
	t.Helper()
	var s string
	if err := d.QueryRow(`SELECT status FROM `+table+` WHERE `+key+` = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPermitQueueListsOnlyThatStatusWithItsDocuments(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingStage1, 1, "", "", "2027-01-31")
	addPermit(t, d, "WEP-BBBBB", permit.StatusPendingFinal, 2, "", "", "2027-01-31")
	got, err := s.PermitQueue(context.Background(), permit.StatusPendingStage1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "WEP-AAAAA" || got[0].WorkerName != "Budi Santoso" || got[0].Company != "PT Maju" {
		t.Fatalf("got %+v", got)
	}
	if len(got[0].Docs) != 1 || got[0].Docs[0].Label != "ID document" || got[0].Docs[0].Path != "WEP-AAAAA/id_doc.pdf" {
		t.Errorf("documents %+v", got[0].Docs)
	}
}

func TestStage1ThenFinalIssuesTheLEPAndRenewsEarlierPermits(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	addPermit(t, d, "WEP-OLD01", permit.StatusApproved, 1, "3201", "LEP-OLDOL", "2026-12-31")
	addPermit(t, d, "WEP-OTHER", permit.StatusApproved, 9, "9999", "LEP-OTHER", "2026-12-31")
	addPermit(t, d, "WEP-NEW01", permit.StatusPendingStage1, 1, "3201", "", "2027-01-31")

	out, err := s.DecidePermit(ctx, pol, Decision{Stage: "stage1", ID: "WEP-NEW01", Approve: true, Reviewer: "Sari", Note: "ok", At: "2026-11-07 09:00:00"})
	if err != nil || out.Status != permit.StatusPendingFinal || out.LEP != "" {
		t.Fatalf("stage1: %+v %v", out, err)
	}
	if _, err := s.DecidePermit(ctx, pol, Decision{Stage: "stage1", ID: "WEP-NEW01", Approve: true}); !errors.Is(err, permit.ErrAlreadyActioned) {
		t.Errorf("a second stage 1 action: %v", err)
	}
	out, err = s.DecidePermit(ctx, pol, Decision{Stage: "final", ID: "WEP-NEW01", Approve: true, Reviewer: "Manager", At: "2026-11-07 10:00:00"})
	if err != nil || out.Status != permit.StatusApproved || !strings.HasPrefix(out.LEP, "LEP-") || out.Renewed != 1 {
		t.Fatalf("final: %+v %v", out, err)
	}
	var lep, valid string
	if err := d.QueryRow(`SELECT lep_no, lep_valid_until FROM permit WHERE request_id = 'WEP-NEW01'`).Scan(&lep, &valid); err != nil {
		t.Fatal(err)
	}
	// MCU valid until 2027-01-31 less the 2-week grace.
	if lep != out.LEP || valid != "2027-01-17" {
		t.Errorf("stored LEP %q valid until %q", lep, valid)
	}
	if got := status(t, d, "permit", "request_id", "WEP-OLD01"); got != permit.RenewedBy(out.LEP) {
		t.Errorf("the earlier permit is %q", got)
	}
	if got := status(t, d, "permit", "request_id", "WEP-OTHER"); got != permit.StatusApproved {
		t.Errorf("another person's permit changed to %q", got)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM review_step WHERE subject_id = 'WEP-NEW01'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("review steps %d %v", n, err)
	}
	// A second Final action (double click, second reviewer) must not issue another LEP.
	if _, err := s.DecidePermit(ctx, pol, Decision{Stage: "final", ID: "WEP-NEW01", Approve: true}); !errors.Is(err, permit.ErrAlreadyActioned) {
		t.Errorf("a second final action: %v", err)
	}
}

func TestFinalApprovalLimitsAForeignWorkersLEPByTheirVisaAndPassport(t *testing.T) {
	cases := []struct {
		name, visaType, visaExpiry, passport, want string
	}{
		{"visa ends first", "Visa", "2026-12-20", "2027-05-01", "2026-12-20"},
		{"itas ends first", "ITAS", "2026-12-21", "2027-05-01", "2026-12-21"},
		{"itap ends first", "ITAP", "2026-12-22", "2027-05-01", "2026-12-22"},
		{"passport ends first", "Visa", "2027-03-01", "2026-12-10", "2026-12-10"},
		{"mcu ends first", "Visa", "2027-03-01", "2027-05-01", "2027-01-17"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, d := newStore(t)
			addPermit(t, d, "WEP-AAAAA", permit.StatusPendingFinal, 1, "", "", "2027-01-31")
			if _, err := d.Exec(`UPDATE permit SET nationality = 'Vietnam', visa_type = ?, visa_expiry = ?, passport_until = ?`,
				c.visaType, c.visaExpiry, c.passport); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DecidePermit(context.Background(), pol, Decision{Stage: "final", ID: "WEP-AAAAA", Approve: true}); err != nil {
				t.Fatal(err)
			}
			var valid string
			if err := d.QueryRow(`SELECT lep_valid_until FROM permit`).Scan(&valid); err != nil || valid != c.want {
				t.Errorf("valid until %q, want %q (%v)", valid, c.want, err)
			}
		})
	}
}

func TestTheQueueCarriesEarlierDecisionsToTheNextReviewer(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingStage1, 1, "", "", "2027-01-31")
	if _, err := s.DecidePermit(ctx, pol, Decision{Stage: "stage1", ID: "WEP-AAAAA", Approve: true, Note: "documents match", Reviewer: "Sari", At: "2026-11-07 09:00:00"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.PermitQueue(ctx, permit.StatusPendingFinal)
	if err != nil || len(got) != 1 || len(got[0].Steps) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if st := got[0].Steps[0]; st.Stage != "stage1" || st.Reviewer != "Sari" || st.Decision != "approved" || st.Note != "documents match" {
		t.Errorf("step %+v", st)
	}
}

func TestRejectingNeedsANoteAndChangesNothingWithoutOne(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingStage1, 1, "", "", "2027-01-31")
	_, err := s.DecidePermit(ctx, pol, Decision{Stage: "stage1", ID: "WEP-AAAAA", Note: "no"})
	if !errors.Is(err, permit.ErrNoteTooShort) || status(t, d, "permit", "request_id", "WEP-AAAAA") != permit.StatusPendingStage1 {
		t.Fatalf("short note: %v", err)
	}
	out, err := s.DecidePermit(ctx, pol, Decision{Stage: "stage1", ID: "WEP-AAAAA", Note: "visa copy is unreadable", Reviewer: "Sari"})
	if err != nil || out.Status != permit.StatusRejected {
		t.Fatalf("reject: %+v %v", out, err)
	}
	var note string
	if err := d.QueryRow(`SELECT note FROM review_step WHERE subject_id = 'WEP-AAAAA' AND stage = 'stage1'`).Scan(&note); err != nil || note != "visa copy is unreadable" {
		t.Errorf("stored note %q %v", note, err)
	}
}

func TestFinalApprovalFailsCleanlyWhenNoValidityCanBeWorkedOut(t *testing.T) {
	s, d := newStore(t)
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingFinal, 1, "", "", "") // no MCU date at all
	if _, err := s.DecidePermit(context.Background(), pol, Decision{Stage: "final", ID: "WEP-AAAAA", Approve: true}); !errors.Is(err, permit.ErrNoValidity) {
		t.Fatalf("got %v", err)
	}
	var lep string
	_ = d.QueryRow(`SELECT lep_no FROM permit`).Scan(&lep)
	if status(t, d, "permit", "request_id", "WEP-AAAAA") != permit.StatusPendingFinal || lep != "" {
		t.Error("a failed approval changed the permit")
	}
}

func TestUnknownRequestIsNotFound(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.DecidePermit(context.Background(), pol, Decision{Stage: "stage1", ID: "WEP-ZZZZZ", Approve: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("permit: %v", err)
	}
	if _, err := s.DecideWaiver(context.Background(), pol, Decision{Stage: "doctor", ID: "WVR-ZZZZZ", Approve: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("waiver: %v", err)
	}
}

func addWaiver(t *testing.T, d *sql.DB, id string) {
	t.Helper()
	w := waiverlog.NewWaiver{RequestID: id, Status: waiver.StatusPendingDoctor, ApplicantType: waiver.TypeVisitorCompany, CompanyID: 1,
		Company: "PT Tamu", Name: "Tamu Satu", Gender: "Male", DOB: "1990-04-02", Nationality: "Indonesia", NIK: "3201020304900001",
		Position: "Auditor", Department: "HSE", VisitLocation: "Site A", Purpose: "Audit", VisitFrom: "2026-11-10", VisitTo: "2026-11-12",
		Days: 3, RequesterName: "R", RequesterEmail: "r@x.id", Phone: "081234567890", Risk: "r", Precaution: "p", Prevent: "v",
		IDDoc: id + "/id_doc.pdf", LetterDoc: id + "/letter_doc.pdf", DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00"}
	tk := ticketlog.NewTicket{ID: "TKT-" + id[4:], LEP: id, Status: "PendingDoctor", Arrival: "2026-11-10", Departure: "2026-11-12",
		Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-06 08:00:00"}
	if err := (waiverlog.Store{DB: d}).CreateWithTicket(context.Background(), w, tk); err != nil {
		t.Fatal(err)
	}
}

func TestWaiverGoesThroughDoctorStage1AndFinalAndActivatesItsTicket(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	addWaiver(t, d, "WVR-AAAAA")
	if got, err := s.WaiverQueue(ctx, waiver.StatusPendingDoctor); err != nil || len(got) != 1 || got[0].Name != "Tamu Satu" || len(got[0].Docs) != 2 {
		t.Fatalf("doctor queue: %+v %v", got, err)
	}
	// Each stage only acts from its own pending status.
	if _, err := s.DecideWaiver(ctx, pol, Decision{Stage: "final", ID: "WVR-AAAAA", Approve: true}); !errors.Is(err, permit.ErrAlreadyActioned) {
		t.Fatalf("final before the doctor: %v", err)
	}
	steps := []struct{ stage, want, note string }{
		{"doctor", waiver.StatusPendingStage1, "fit to visit"},
		{"stage1", waiver.StatusPendingSiteManager, "ok"},
		{"final", waiver.StatusApproved, "ok"},
	}
	for _, st := range steps {
		out, err := s.DecideWaiver(ctx, pol, Decision{Stage: st.stage, ID: "WVR-AAAAA", Approve: true, Note: st.note, Reviewer: "R", At: "2026-11-07 09:00:00"})
		if err != nil || out.Status != st.want {
			t.Fatalf("%s: %+v %v", st.stage, out, err)
		}
	}
	var tstatus, review, note string
	if err := d.QueryRow(`SELECT status, doctor_review, doctor_note FROM ticket WHERE lep_no = 'WVR-AAAAA'`).Scan(&tstatus, &review, &note); err != nil {
		t.Fatal(err)
	}
	if tstatus != "Approved" || review != "Approved" || note != "fit to visit" {
		t.Errorf("ticket %q %q %q", tstatus, review, note)
	}
}

func TestRejectedWaiverRejectsItsTicketToo(t *testing.T) {
	s, d := newStore(t)
	addWaiver(t, d, "WVR-AAAAA")
	out, err := s.DecideWaiver(context.Background(), pol, Decision{Stage: "doctor", ID: "WVR-AAAAA", Note: "not fit for this site", Reviewer: "Dr"})
	if err != nil || out.Status != waiver.StatusRejected {
		t.Fatalf("%+v %v", out, err)
	}
	if got := status(t, d, "ticket", "lep_no", "WVR-AAAAA"); got != "Rejected" {
		t.Errorf("ticket is %q", got)
	}
}

func TestOutcomeCarriesWhatTheDecisionMailNeeds(t *testing.T) {
	s, d := newStore(t)
	ctx := context.Background()
	addPermit(t, d, "WEP-AAAAA", permit.StatusPendingFinal, 1, "", "", "2027-01-31")
	if _, err := d.Exec(`UPDATE permit SET work_position = 'Welder'`); err != nil {
		t.Fatal(err)
	}
	out, err := s.DecidePermit(ctx, pol, Decision{Stage: "final", ID: "WEP-AAAAA", Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	n := out.Notice
	// The validity is the one just stored, not the empty value the row had before.
	if n.Requester != "rina@example.com" || n.Name != "Budi Santoso" || n.Company != "PT Maju" || n.Position != "Welder" || n.ValidUntil != "2027-01-17" {
		t.Errorf("permit notice %+v", n)
	}

	addWaiver(t, d, "WVR-AAAAA")
	out, err = s.DecideWaiver(ctx, pol, Decision{Stage: "doctor", ID: "WVR-AAAAA", Approve: false, Note: "not enough detail here"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Notice.Requester == "" || out.Notice.Name != "Tamu Satu" || out.Notice.VisitFrom == "" || out.Notice.VisitTo == "" || out.Notice.Location == "" {
		t.Errorf("waiver notice %+v", out.Notice)
	}
}

func TestADatabaseErrorIsReportedNotSwallowed(t *testing.T) {
	s, d := newStore(t)
	d.Close() // every query now fails
	ctx := context.Background()
	if _, err := s.DecidePermit(ctx, pol, Decision{Stage: "final", ID: "WEP-AAAAA", Approve: true}); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("permit: %v", err)
	}
	if _, err := s.DecideWaiver(ctx, pol, Decision{Stage: "doctor", ID: "WVR-AAAAA", Approve: true}); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("waiver: %v", err)
	}
}
