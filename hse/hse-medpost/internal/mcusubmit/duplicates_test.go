package mcusubmit

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// today's date plus n days, in the clinic's zone like ResultValidUntil.
func inDays(s *Store, n int) string { return s.clinicToday().AddDate(0, 0, n).Format("2006-01-02") }

func TestWorkerBlockIsOpenForAWorkerWithNothingOnFile(t *testing.T) {
	s, _, co := newTestStore(t)
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	p := seedWorker(t, s, "Free Worker", "1990-04-12", co)
	if b, err := s.WorkerBlock(context.Background(), sub, p); err != nil || b.Kind != "" {
		t.Fatalf("= %+v, %v; want no block", b, err)
	}
}

func TestWorkerBlockWaitsForADocumentThatIsPendingOrReviewed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.a@example.com")
	b := activeSubmitter(t, s, co, "hr.b@example.com")
	p := seedWorker(t, s, "Busy Worker", "1990-04-12", co)
	sentFor(t, s, a, p, "Busy Worker") // Pending
	for _, who := range []int64{a, b} {
		if got, err := s.WorkerBlock(ctx, who, p); err != nil || got.Kind != BlockWaiting {
			t.Fatalf("submitter %d: %+v, %v; want waiting (a sent document blocks everyone)", who, got, err)
		}
	}
}

// A Reviewed document with an Incomplete result is still open, and the way forward is a new
// document from the submitter, so it must not make the worker "wait" (owner, 2026-11-10).
func TestWorkerBlockDoesNotWaitForAReviewedDocumentWithAnIncompleteResult(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrinc2@example.com")
	w := seedWorker(t, s, "Open Worker", "1990-04-12", co)
	addRecord(t, s, w, "MedC-OPEN", "Fit", "2027-01-01", "Open", 0)
	a, _ := sentDocIn(t, s, sub, "Open Worker", "Other Four")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-OPEN"); err != nil { // Reviewed with a Fit result
		t.Fatal(err)
	}
	if b, _ := s.WorkerBlock(ctx, sub, w); b.Kind != BlockWaiting {
		t.Fatalf("a Reviewed document with a Fit result: %+v, want waiting", b)
	}
	// The local SQLite schema has the old CHECK list, so write IncompleteResult with it switched off.
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{`PRAGMA ignore_check_constraints = ON`,
		`UPDATE McuRecord SET ResultCriteria = 'IncompleteResult', ResultValidUntil = NULL WHERE CheckUpID = 'MedC-OPEN'`,
		`PRAGMA ignore_check_constraints = OFF`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := s.WorkerBlock(ctx, sub, w); err != nil || b.Kind != "" {
		t.Fatalf("a Reviewed document with an Incomplete result: %+v, %v; want a new document accepted", b, err)
	}
	// Every result but Fit and Fit with note leaves the document Reviewed, so none of them may block.
	for _, result := range []string{"Unfit", "TemporaryUnfit", "NeedFollowUp"} {
		if _, err := conn.ExecContext(ctx, `UPDATE McuRecord SET ResultCriteria = '`+result+`' WHERE CheckUpID = 'MedC-OPEN'`); err != nil {
			t.Fatal(err)
		}
		if b, err := s.WorkerBlock(ctx, sub, w); err != nil || b.Kind != "" {
			t.Fatalf("a Reviewed document with %s: %+v, %v; want a new document accepted", result, b, err)
		}
	}
}

func TestWorkerBlockTreatsOnlyTheOwnDraftAsWaiting(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.a@example.com")
	b := activeSubmitter(t, s, co, "hr.b@example.com")
	p := seedWorker(t, s, "Draft Worker", "1990-04-12", co)
	d := NewSubmission{PatientID: p, PatName: "Draft Worker", PatDateOfBirth: "1990-04-12", PatPosition: "Welder", PatCompanyText: "PT Example",
		PatNationality: "Indonesia", FilePath: "/q/x.pdf", FileSha256: strings.Repeat("a", 64), FileBytes: 10}
	if _, err := s.AddSubmission(ctx, a, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorkerBlock(ctx, a, p); got.Kind != BlockWaiting {
		t.Errorf("own draft: %+v, want waiting", got)
	}
	if got, _ := s.WorkerBlock(ctx, b, p); got.Kind != "" {
		t.Errorf("a colleague's unsent draft blocked someone else: %+v", got)
	}
}

// withCheckupDate sets the day the record's checkup was done (addRecord leaves it at 2026-01-01).
func withCheckupDate(t *testing.T, s *Store, checkUpID, date string) {
	t.Helper()
	if _, err := s.DB.Exec(`UPDATE McuRecord SET DateCheckUp = ?, ReviewDate = ? WHERE CheckUpID = ?`, date, date, checkUpID); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerBlockAcceptsANewCheckupOnlyMoreThan180DaysAfterTheLast(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	mk := func(name, dob, id, result string, doneDaysAgo, validDays int) int64 {
		p := seedWorker(t, s, name, dob, co)
		addRecord(t, s, p, id, result, inDays(s, validDays), "Close", 1)
		withCheckupDate(t, s, id, inDays(s, -doneDaysAgo))
		return p
	}
	recent := mk("Recent", "1990-04-12", "MedC-recent", "Fit", 100, 265)   // first open on day 181
	edge := mk("Edge", "1990-04-13", "MedC-edge", "FitWithNote", 180, 185) // exactly 180 days: still blocked
	old := mk("Old", "1990-04-14", "MedC-old", "Fit", 181, 184)            // 181 days: open
	short := mk("Short", "1990-04-15", "MedC-short", "Fit", 150, 10)       // lapses first: open the day after
	lapsed := mk("Lapsed", "1990-04-16", "MedC-lapsed", "Fit", 300, -5)    // no longer valid: open

	b, err := s.WorkerBlock(ctx, sub, recent)
	if err != nil || b.Kind != BlockValid || b.From != inDays(s, 81) || b.Until != inDays(s, 265) {
		t.Fatalf("recent = %+v, %v; want blocked until %s", b, err, inDays(s, 81))
	}
	if b.Last != inDays(s, -100) || !strings.Contains(b.Message(), "done on "+b.Last) || !strings.Contains(b.Message(), "valid until "+b.Until) ||
		!strings.Contains(b.Message(), "more than 180 days") || !strings.Contains(b.Message(), "from "+b.From) {
		t.Errorf("message = %q", b.Message())
	}
	if got, _ := s.WorkerBlock(ctx, sub, edge); got.Kind != BlockValid || got.From != inDays(s, 1) {
		t.Errorf("exactly 180 days old = %+v, want blocked until tomorrow", got)
	}
	if got, _ := s.WorkerBlock(ctx, sub, old); got.Kind != "" {
		t.Errorf("181 days old must be renewable: %+v", got)
	}
	if got, _ := s.WorkerBlock(ctx, sub, short); got.Kind != BlockValid || got.From != inDays(s, 11) {
		t.Errorf("short validity = %+v, want blocked only until it lapses (%s)", got, inDays(s, 11))
	}
	if got, _ := s.WorkerBlock(ctx, sub, lapsed); got.Kind != "" {
		t.Errorf("a lapsed checkup must not block: %+v", got)
	}
}

func TestWorkerBlockIgnoresUnfitArchivedAndNonFinalRecords(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	far := inDays(s, 400)
	cases := map[string]struct {
		result, status string
		terminal       int
	}{
		"Unfit worker":      {"Unfit", "Close", 1},
		"Temp unfit worker": {"TemporaryUnfit", "Open", 1},
		"Archived worker":   {"Fit", "Archived / Exit", 1},
		"Draft record":      {"Fit", "Open", 0},
	}
	i := 0
	for name, c := range cases {
		i++
		p := seedWorker(t, s, name, "1990-05-"+string(rune('0'+i))+"1", co)
		addRecord(t, s, p, "MedC-"+itoa(int64(i)), c.result, far, c.status, c.terminal)
		withCheckupDate(t, s, "MedC-"+itoa(int64(i)), inDays(s, -10))
		if got, err := s.WorkerBlock(ctx, sub, p); err != nil || got.Kind != "" {
			t.Errorf("%s: %+v, %v; want no block", name, got, err)
		}
	}
}

// ---- through the pages ----------------------------------------------------------------------------

func TestPickerMarksAWorkerWithAValidCheckupAsBlocked(t *testing.T) {
	u := newUploadEnv(t)
	pid := u.patient("Valid Vera", "1985-06-01")
	addRecord(t, u.store, pid, "MedC-v", "Fit", inDays(u.store, 400), "Close", 1)
	withCheckupDate(t, u.store, "MedC-v", inDays(u.store, -30))
	free := u.patient("Valid Free", "1985-06-02")

	body := u.get("/mcu-submit/workers?q=Valid", u.cookie).Body.String()
	if !strings.Contains(body, `"blocked":"valid"`) || !strings.Contains(body, `"until":"`+inDays(u.store, 400)+`"`) {
		t.Errorf("the worker with a valid checkup is not marked blocked:\n%s", body)
	}
	if strings.Count(body, `"blocked":""`) != 1 {
		t.Errorf("exactly the free worker should be open:\n%s", body)
	}
	_ = free
}

func TestUploadForAWorkerWithAValidCheckupIsRefusedAndStoresNothing(t *testing.T) {
	u := newUploadEnv(t)
	pid := u.patient("Valid Vera", "1985-06-01")
	addRecord(t, u.store, pid, "MedC-v", "Fit", inDays(u.store, 400), "Close", 1)
	withCheckupDate(t, u.store, "MedC-v", inDays(u.store, -30))

	rec := u.upload(u.cookie, map[string]string{"patient_id": itoa(pid)}, plainPDF("", ""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "valid until "+inDays(u.store, 400)) {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	var n int
	if err := u.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmission WHERE FK_PatientID = ?`, pid).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused upload left %d rows (%v)", n, err)
	}
}

func TestTheSameWorkerCannotBeUploadedTwice(t *testing.T) {
	u := newUploadEnv(t)
	pid := u.patient("Twice Tono", "1985-06-01")
	if rec := u.upload(u.cookie, map[string]string{"patient_id": itoa(pid)}, plainPDF("", "")); rec.Code != http.StatusSeeOther {
		t.Fatalf("first upload = %d\n%s", rec.Code, rec.Body)
	}
	rec := u.upload(u.cookie, map[string]string{"patient_id": itoa(pid)}, plainPDF("", ""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already has a document waiting") {
		t.Fatalf("second upload = %d\n%s", rec.Code, rec.Body)
	}
}

// The refusal sentences have dates in them, so they go through the translation patterns, not the
// exact-match dictionary: check both languages really translate them.
func TestBlockMessagesAreTranslated(t *testing.T) {
	valid := Block{Kind: BlockValid, Last: "2026-09-01", Until: "2027-09-01", From: "2027-03-01"}
	wait := Block{Kind: BlockWaiting}
	for lang, want := range map[string][]string{
		"id": {"dilakukan 2026-09-01", "berlaku sampai 2027-09-01", "mulai 2027-03-01", "Pekerja ini sudah memiliki dokumen"},
		"zh": {"2026-09-01", "有效期至 2027-09-01", "从 2027-03-01 起", "该员工已有文件在等待"},
	} {
		got := translateHTML(`<html lang="en"><p>`+valid.Message()+`</p><p>`+wait.Message()+`</p>`, lang)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", lang, w, got)
			}
		}
		if strings.Contains(got, "more than 180 days") || strings.Contains(got, "already has a document") {
			t.Errorf("%s left English in the page:\n%s", lang, got)
		}
	}
}
