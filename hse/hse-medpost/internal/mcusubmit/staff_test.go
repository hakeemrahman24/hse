package mcusubmit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

const testStaffID = 7

// sentDoc stores a confirmed document and sends its batch, so it is Pending.
func sentDoc(t *testing.T, s *Store, sub int64, name string) int64 {
	t.Helper()
	id := addClean(t, s, sub, name)
	if _, err := s.SendBatch(context.Background(), sub, batchOf(t, s, id)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMarkReviewedRecordsWhoAndWhenAndOnlyFromPending(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	pending := sentDoc(t, s, sub, "Worker One")
	draft := addClean(t, s, sub, "Worker Two")

	if err := s.MarkReviewed(ctx, pending, testStaffID); err != nil {
		t.Fatal(err)
	}
	var status string
	var reviewer int64
	var at *string
	_ = s.DB.QueryRow(`SELECT Status, FK_ReviewerStaffID, ReviewedAt FROM McuSubmission WHERE SubmissionID = ?`, pending).Scan(&status, &reviewer, &at)
	if status != SubReviewed || reviewer != testStaffID || at == nil {
		t.Fatalf("status %s, reviewer %d, at %v", status, reviewer, at)
	}
	if err := s.MarkReviewed(ctx, pending, testStaffID); !errors.Is(err, ErrBadState) {
		t.Errorf("reviewing twice = %v, want ErrBadState", err)
	}
	if err := s.MarkReviewed(ctx, draft, testStaffID); !errors.Is(err, ErrBadState) {
		t.Errorf("reviewing a Draft = %v, want ErrBadState: staff must not touch unsent work", err)
	}
	if err := s.MarkReviewed(ctx, 9999, testStaffID); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}

func TestRejectNeedsAReasonAndWorksFromPendingOrReviewed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	a, b := sentDocIn(t, s, sub, "Worker One", "Worker Two")
	_ = s.MarkReviewed(ctx, b, testStaffID)

	for _, reason := range []string{"", "   ", strings.Repeat("x", maxRejectReason+1)} {
		if err := s.RejectSubmission(ctx, a, testStaffID, reason); !errors.Is(err, ErrBadReason) {
			t.Errorf("reason %q = %v, want ErrBadReason", reason, err)
		}
	}
	for _, id := range []int64{a, b} {
		if err := s.RejectSubmission(ctx, id, testStaffID, "  Scan is unreadable  "); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ := s.ListOwn(ctx, sub)
	for _, d := range docs {
		if d.Status != SubRejected || d.RejectReason != "Scan is unreadable" {
			t.Errorf("submitter sees %s / %q, want Rejected with the trimmed reason", d.Status, d.RejectReason)
		}
	}
	if err := s.RejectSubmission(ctx, a, testStaffID, "again"); !errors.Is(err, ErrBadState) {
		t.Errorf("rejecting twice = %v, want ErrBadState", err)
	}
}

// sentDocIn adds two documents to one batch and sends them together.
func sentDocIn(t *testing.T, s *Store, sub int64, first, second string) (int64, int64) {
	t.Helper()
	a, b := addClean(t, s, sub, first), addClean(t, s, sub, second)
	if _, err := s.SendBatch(context.Background(), sub, batchOf(t, s, a)); err != nil {
		t.Fatal(err)
	}
	return a, b
}

// ---- HTTP -------------------------------------------------------------------------------------

type staffEnv struct {
	store *Store
	files *memStore
	mux   http.Handler
	sub   int64
}

func newStaffEnv(t *testing.T) *staffEnv {
	t.Helper()
	s, _, co := newTestStore(t)
	files := newMemStore()
	api := &StaffAPI{Store: s, Files: files}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mcu-submissions", api.List)
	mux.HandleFunc("GET /api/mcu-submissions/{id}", api.Get)
	mux.HandleFunc("GET /api/mcu-submissions/{id}/file", api.File)
	mux.HandleFunc("GET /api/mcu-submissions/{id}/records", api.Records)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/reject", api.Reject)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/link", api.Link)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/unlink", api.Unlink)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/close", api.Close)
	// The real route wraps these in RequireAuth, which puts the session in the context.
	withSession := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Anonymous") == "" {
			r = r.WithContext(middleware.ContextWithSession(r.Context(), &auth.Session{UserID: 1, StaffID: testStaffID, Name: "Dr Test"}))
		}
		mux.ServeHTTP(w, r)
	})
	return &staffEnv{store: s, files: files, mux: withSession, sub: activeSubmitter(t, s, co, "hrone@example.com")}
}

func (e *staffEnv) do(method, path, body string, anonymous bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if anonymous {
		req.Header.Set("X-Test-Anonymous", "1")
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// doc stores a real file in the memory store and a row that points at it.
func (e *staffEnv) doc(t *testing.T, name string, send bool) int64 {
	t.Helper()
	ref, err := e.files.Put(context.Background(), bytes.NewReader([]byte("%PDF-1.4 "+name+"\n%%EOF")))
	if err != nil {
		t.Fatal(err)
	}
	d := sampleDoc(name)
	d.FilePath = ref
	id, err := e.store.AddSubmission(context.Background(), e.sub, d)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.store.SetScanResult(context.Background(), id, ScanClean)
	if send {
		if _, err := e.store.SendBatch(context.Background(), e.sub, batchOf(t, e.store, id)); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestStaffRoutesRefuseAnAnonymousCaller(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Worker One", true)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/mcu-submissions"}, {"GET", fmt.Sprintf("/api/mcu-submissions/%d/file", id)},
		{"POST", fmt.Sprintf("/api/mcu-submissions/%d/unlink", id)}, {"POST", fmt.Sprintf("/api/mcu-submissions/%d/reject", id)},
	} {
		if rec := e.do(c.method, c.path, `{"reason":"x"}`, true); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

func TestStaffListShowsSentDocumentsOnlyAndNeverAFilePath(t *testing.T) {
	e := newStaffEnv(t)
	sent := e.doc(t, "Sent Worker", true)
	e.doc(t, "Draft Worker", false)

	rec := e.do("GET", "/api/mcu-submissions", "", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	var rows []staffRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].ID != sent {
		t.Fatalf("rows = %+v (%v), want only the sent document", rows, err)
	}
	if rows[0].Worker != "Sent Worker" || rows[0].Submitter != "Budi Santoso" || rows[0].Status != SubPending || !rows[0].HasFile {
		t.Errorf("row = %+v", rows[0])
	}
	if strings.Contains(rec.Body.String(), "mem-") {
		t.Error("the storage reference leaked into the list")
	}
	if got := e.do("GET", "/api/mcu-submissions?status=Rejected", "", false).Body.String(); strings.TrimSpace(got) != "[]" {
		t.Errorf("status filter returned %s, want []", got)
	}
}

func TestStaffFileIsServedForSentDocumentsAndTheViewIsLoggedWithTheStaffId(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Sent Worker", true)
	rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d/file", id), "", false)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Content-Security-Policy") != "sandbox" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("= %d, headers %v", rec.Code, rec.Header())
	}
	var actor string
	var actorID int64
	if err := e.store.DB.QueryRow(`SELECT ActorType, ActorID FROM McuSubmissionEvent WHERE Action = 'FileViewed' AND EntityID = ?`, id).Scan(&actor, &actorID); err != nil || actor != ActorStaff || actorID != testStaffID {
		t.Fatalf("event = %s %d, %v; want Staff %d", actor, actorID, err, testStaffID)
	}
}

func TestStaffCannotOpenDraftsReviewDocumentsOrWithdrawnFiles(t *testing.T) {
	e := newStaffEnv(t)
	sent := e.doc(t, "Sent Worker", true)
	if _, err := e.store.Withdraw(context.Background(), e.sub, sent); err != nil {
		t.Fatal(err)
	}
	// These two start a new batch, because the first one was sent.
	draft := e.doc(t, "Draft Worker", false)
	review := e.doc(t, "Review Worker", false)
	_, _ = e.store.DB.Exec(`UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ?`, SubReview, review)
	for _, id := range []int64{draft, review, sent, 9999} {
		if rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d/file", id), "", false); rec.Code != http.StatusNotFound {
			t.Errorf("file %d = %d, want 404", id, rec.Code)
		}
	}
}

func TestStaffReviewAndRejectOverHTTP(t *testing.T) {
	e := newStaffEnv(t)
	a := e.doc(t, "Worker One", true)
	b := e.doc(t, "Worker Two", true)

	if err := e.store.MarkReviewed(context.Background(), a, testStaffID); err != nil {
		t.Fatal(err)
	}
	// There is no HTTP route that marks a document Reviewed any more: Reviewed means a linked
	// checkup with a result, so only the link route can set it.
	if rec := e.do("POST", fmt.Sprintf("/api/mcu-submissions/%d/review", a), "", false); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("the old review route answered %d, want it gone", rec.Code)
	}
	for _, c := range []struct {
		body string
		want int
	}{{`{"reason":""}`, http.StatusBadRequest}, {`not json`, http.StatusBadRequest}, {`{"reason":"Unreadable scan"}`, http.StatusNoContent}} {
		if rec := e.do("POST", fmt.Sprintf("/api/mcu-submissions/%d/reject", b), c.body, false); rec.Code != c.want {
			t.Errorf("reject %q = %d, want %d", c.body, rec.Code, c.want)
		}
	}
	var n int
	_ = e.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmissionEvent WHERE ActorType = 'Staff' AND ActorID = ? AND Action IN ('Reviewed', 'Rejected')`, testStaffID).Scan(&n)
	if n != 2 {
		t.Errorf("%d staff events, want 2 (every state change is on the audit trail)", n)
	}
}

// ---- closing a document with the checkup record staff made ------------------------------------

func TestCloseWithRecordLinksTheRecordAndClosesFromPendingOrReviewed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	w1 := seedWorker(t, s, "Worker One", "1990-04-12", co)
	w2 := seedWorker(t, s, "Worker Two", "1991-04-12", co)
	addRecord(t, s, w1, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, w2, "MedC-2", "Fit", "2027-01-01", "Open", 1)
	a, b := sentDocIn(t, s, sub, "Worker One", "Worker Two")
	for _, id := range []int64{a, b} {
		if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, map[int64]int64{a: w1, b: w2}[id], id); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.MarkReviewed(ctx, b, testStaffID) // one Reviewed, one still Pending

	if err := closeWithRecord(s, ctx, a, testStaffID, " MedC-1 "); err != nil {
		t.Fatal(err)
	}
	if err := closeWithRecord(s, ctx, b, testStaffID, "MedC-2"); err != nil {
		t.Fatal(err)
	}
	var status string
	var rec int64
	_ = s.DB.QueryRow(`SELECT Status, FK_McuRecordID FROM McuSubmission WHERE SubmissionID = ?`, a).Scan(&status, &rec)
	if status != SubClosed || rec == 0 {
		t.Fatalf("status %s, record %d", status, rec)
	}
	if !hasEvent(t, s, EntitySubmission, a, "Closed") {
		t.Error("no Closed event")
	}
	docs, _ := s.ListOwn(ctx, sub)
	for _, d := range docs {
		if d.Status != SubClosed {
			t.Errorf("submitter sees %s, want Closed", d.Status)
		}
	}
}

func TestCloseWithRecordRefusesWhatWouldLinkTheWrongThing(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	w1 := seedWorker(t, s, "Worker One", "1990-04-12", co)
	w2 := seedWorker(t, s, "Worker Two", "1991-04-12", co)
	addRecord(t, s, w1, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, w2, "MedC-2", "Fit", "2027-01-01", "Open", 1)
	a, b := sentDocIn(t, s, sub, "Worker One", "Worker Two")
	_, _ = s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w1, a)
	draft := addClean(t, s, sub, "Worker Three")

	if err := closeWithRecord(s, ctx, a, testStaffID, "NOPE"); !errors.Is(err, ErrRecordUnknown) {
		t.Errorf("unknown checkup id = %v, want ErrRecordUnknown", err)
	}
	if err := closeWithRecord(s, ctx, a, testStaffID, "MedC-2"); !errors.Is(err, ErrRecordMismatch) {
		t.Errorf("another patient's record = %v, want ErrRecordMismatch", err)
	}
	if err := closeWithRecord(s, ctx, draft, testStaffID, "MedC-1"); !errors.Is(err, ErrBadState) {
		t.Errorf("a Draft = %v, want ErrBadState", err)
	}
	if err := closeWithRecord(s, ctx, 9999, testStaffID, "MedC-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing submission = %v, want ErrNotFound", err)
	}
	if err := closeWithRecord(s, ctx, a, testStaffID, "MedC-1"); err != nil {
		t.Fatal(err)
	}
	// b has no patient yet (a new employee); the same record cannot close a second document.
	if err := closeWithRecord(s, ctx, b, testStaffID, "MedC-1"); !errors.Is(err, ErrRecordTaken) {
		t.Errorf("a record already linked = %v, want ErrRecordTaken", err)
	}
	if err := closeWithRecord(s, ctx, a, testStaffID, "MedC-1"); !errors.Is(err, ErrBadState) {
		t.Errorf("closing twice = %v, want ErrBadState", err)
	}
}

func TestCloseWithRecordGivesANewEmployeeTheirPatient(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "New Person")
	w := seedWorker(t, s, "New Person", "1990-04-12", co) // staff created the patient and record on the checkup page
	addRecord(t, s, w, "MedC-9", "Fit", "2027-01-01", "Open", 1)

	if err := closeWithRecord(s, ctx, id, testStaffID, "MedC-9"); err != nil {
		t.Fatal(err)
	}
	var patient int64
	_ = s.DB.QueryRow(`SELECT FK_PatientID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&patient)
	if patient != w {
		t.Fatalf("patient = %d, want %d: the expiry reminders need the link", patient, w)
	}
}

func TestLinkRouteMapsEachRefusalToAClearStatus(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Worker One", true)
	w := seedWorker(t, e.store, "Worker One", "1990-04-12", 0)
	addRecord(t, e.store, w, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	path := fmt.Sprintf("/api/mcu-submissions/%d/link", id)

	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, true); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", rec.Code)
	}
	if rec := e.do("POST", path, `not json`, false); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", path, `{"checkUpId":"NOPE"}`, false); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown record = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/mcu-submissions/9999/link", `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusNotFound {
		t.Errorf("missing document = %d, want 404", rec.Code)
	}
	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusNoContent {
		t.Fatalf("link = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusNoContent {
		t.Errorf("linking the same record again = %d, want 204 (saving an edited checkup does this)", rec.Code)
	}
	closePath := fmt.Sprintf("/api/mcu-submissions/%d/close", id)
	if rec := e.do("POST", closePath, ``, false); rec.Code != http.StatusNoContent {
		t.Errorf("notify = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", closePath, ``, false); rec.Code != http.StatusConflict {
		t.Errorf("notifying twice = %d, want 409", rec.Code)
	}
}

// The count in LinkRecord is only the friendly first line; two staff members closing at the
// same instant both pass it. The unique index is what must stop the second link.
func TestTheDatabaseRefusesTwoDocumentsOnOneRecord(t *testing.T) {
	s, _, co := newTestStore(t)
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	w1 := seedWorker(t, s, "Worker One", "1990-04-12", co)
	addRecord(t, s, w1, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	var recordID int64
	if err := s.DB.QueryRow(`SELECT McuRecordID FROM McuRecord WHERE CheckUpID = 'MedC-1'`).Scan(&recordID); err != nil {
		t.Fatal(err)
	}
	a, b := sentDocIn(t, s, sub, "Worker One", "Worker Two")

	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_McuRecordID = ? WHERE SubmissionID = ?`, recordID, a); err != nil {
		t.Fatal(err)
	}
	_, err := s.DB.Exec(`UPDATE McuSubmission SET FK_McuRecordID = ? WHERE SubmissionID = ?`, recordID, b)
	if !isUniqueViolation(err) {
		t.Fatalf("a second document on the same record = %v, want a unique violation", err)
	}
	// Documents with no record yet must stay allowed: NULLs do not collide.
	c := addClean(t, s, sub, "Worker Three")
	d := addClean(t, s, sub, "Worker Four")
	if c == d {
		t.Fatal("setup")
	}
}

func TestStaffGetGivesTheFormWhatItNeedsAndHidesUnsentWork(t *testing.T) {
	e := newStaffEnv(t)
	sent := e.doc(t, "Sent Worker", true)
	draft := e.doc(t, "Draft Worker", false)

	if rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d", sent), "", true); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous get = %d, want 401", rec.Code)
	}
	if rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d", draft), "", false); rec.Code != http.StatusNotFound {
		t.Errorf("a Draft = %d, want 404", rec.Code)
	}
	if rec := e.do("GET", "/api/mcu-submissions/99999", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("a missing id = %d, want 404", rec.Code)
	}
	_, _ = e.store.DB.Exec(`UPDATE McuSubmission SET PatNik = '3201010101010001', FK_PatientID = NULL WHERE SubmissionID = ?`, sent)
	rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d", sent), "", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}
	var got staffDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Worker != "Sent Worker" || got.PersonalID != "3201010101010001" || got.Status != SubPending || got.PatientID != 0 {
		t.Errorf("detail = %+v", got)
	}
	if strings.Contains(rec.Body.String(), "FilePath") || strings.Contains(rec.Body.String(), "filePath") {
		t.Error("the detail leaks a file path")
	}
}

func TestStaffListNamesWhoReviewedAndWhen(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Sent Worker", true)
	if err := e.store.MarkReviewed(context.Background(), id, testStaffID); err != nil {
		t.Fatal(err)
	}
	rec := e.do("GET", "/api/mcu-submissions", "", false)
	var rows []staffRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list = %v, %s", err, rec.Body.String())
	}
	if rows[0].ReviewedBy != "Dr Test" || rows[0].ReviewedAt == "" {
		t.Errorf("reviewed by %q at %q, want Dr Test and a time", rows[0].ReviewedBy, rows[0].ReviewedAt)
	}
}

func TestStaffDetailCarriesTheGenderAndEnsureGenderColumnUpgradesAnOldTable(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Sent Worker", true)
	rec := e.do("GET", fmt.Sprintf("/api/mcu-submissions/%d", id), "", false)
	var got staffDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Gender != "Male" {
		t.Fatalf("gender = %q, %v; want Male (sampleDoc)", got.Gender, err)
	}
	// Running it on a database that already has the column is a no-op, not an error.
	if err := EnsureGenderColumn(e.store.DB); err != nil {
		t.Fatalf("second run: %v", err)
	}
	// A table from before the column gets it, and old rows read back as an empty gender.
	if _, err := e.store.DB.Exec(`ALTER TABLE McuSubmission DROP COLUMN PatGender`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGenderColumn(e.store.DB); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	docs, err := e.store.querySubmissions(context.Background(), `WHERE s.SubmissionID = ?`, id)
	if err != nil || len(docs) != 1 || docs[0].PatGender != "" {
		t.Fatalf("after upgrade: %+v, %v", docs, err)
	}
}

// A new employee has no patient yet, so the record's patient must be the same person by the facts the
// submitter typed. Before this rule any record could be linked to any new-employee document.
func TestCloseWithRecordRefusesAnotherPersonsRecordForANewEmployee(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Agus Prasetyo") // typed date of birth 1990-04-12
	other := seedWorker(t, s, "Someone Else", "1985-01-01", co)
	addRecord(t, s, other, "MedC-OTHER", "Fit", "2027-01-01", "Open", 1)

	if err := closeWithRecord(s, ctx, id, testStaffID, "MedC-OTHER"); !errors.Is(err, ErrRecordMismatch) {
		t.Fatalf("another person's record = %v, want ErrRecordMismatch", err)
	}
	var status string
	var patient sql.NullInt64
	_ = s.DB.QueryRow(`SELECT Status, FK_PatientID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status, &patient)
	if status != SubPending || patient.Valid {
		t.Fatalf("a refused link changed the document: status %s, patient %v", status, patient)
	}
}

func TestCloseWithRecordAcceptsTheSameIDNumberWhenTheBirthDateWasMistyped(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Agus Prasetyo")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET PatNik = '3201010101900001' WHERE SubmissionID = ?`, id); err != nil {
		t.Fatal(err)
	}
	w := seedWorker(t, s, "Agus Prasetyo", "1990-04-21", co) // staff corrected the birth date
	if _, err := s.DB.Exec(`UPDATE Patient SET PersonalID = '3201 0101-0190 0001' WHERE PatientID = ?`, w); err != nil {
		t.Fatal(err)
	}
	addRecord(t, s, w, "MedC-NIK", "Fit", "2027-01-01", "Open", 1)
	if err := closeWithRecord(s, ctx, id, testStaffID, "MedC-NIK"); err != nil {
		t.Fatalf("same NIK, other birth date = %v, want nil", err)
	}
}

func TestSameWorker(t *testing.T) {
	cases := []struct {
		name                  string
		dob, nik, pass        string
		patientDOB, patientID string
		want                  bool
	}{
		{"same birth date", "1990-04-12", "", "", "1990-04-12", "", true},
		{"birth date with a time on the patient", "1990-04-12", "", "", "1990-04-12 00:00:00", "", true},
		{"other birth date, no id", "1990-04-12", "", "", "1985-01-01", "", false},
		{"same nik formatted differently", "1990-04-12", "3201010101900001", "", "1985-01-01", "3201-0101 0190 0001", true},
		{"same passport in other case", "1990-04-12", "", "ab123456", "1985-01-01", "AB123456", true},
		{"other id", "1990-04-12", "3201010101900001", "", "1985-01-01", "3201010101900002", false},
		{"patient without id never matches an empty one", "", "", "", "", "", false},
	}
	for _, c := range cases {
		if got := sameWorker(c.dob, c.nik, c.pass, c.patientDOB, c.patientID); got != c.want {
			t.Errorf("%s: sameWorker = %v, want %v", c.name, got, c.want)
		}
	}
}

// The Submitted tab's "Link a checkup" list: only this worker's checkups that no document has yet.
func TestCandidateRecordsListsOnlyThisWorkersUnlinkedCheckups(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Agus Prasetyo") // typed date of birth 1990-04-12
	other := sentDoc(t, s, sub, "Another Person")

	same := seedWorker(t, s, "Agus Prasetyo", "1990-04-12", co)
	stranger := seedWorker(t, s, "Someone Else", "1985-01-01", co)
	addRecord(t, s, same, "MedC-OLD", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, same, "MedC-NEW", "Fit", "2027-06-01", "Open", 1)
	addRecord(t, s, same, "MedC-USED", "Fit", "2027-06-01", "Open", 1)
	addRecord(t, s, stranger, "MedC-STRANGER", "Fit", "2027-01-01", "Open", 1)
	if err := closeWithRecord(s, ctx, other, testStaffID, "MedC-USED"); err != nil {
		t.Fatal(err)
	}

	got, err := s.CandidateRecords(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.CheckUpID)
	}
	if strings.Join(ids, ",") != "MedC-NEW,MedC-OLD" {
		t.Fatalf("candidates = %v, want [MedC-NEW MedC-OLD]: newest first, no stranger, none already linked", ids)
	}
	// Whatever the list offers, a close accepts.
	if err := closeWithRecord(s, ctx, id, testStaffID, ids[0]); err != nil {
		t.Fatalf("closing with an offered record = %v", err)
	}
}

func TestCandidateRecordsForAPickedPatientAndTheRefusals(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Worker One")
	w := seedWorker(t, s, "Worker One", "1990-04-12", co)
	twin := seedWorker(t, s, "Twin", "1990-04-12", co) // same birth date, different patient
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, id); err != nil {
		t.Fatal(err)
	}
	addRecord(t, s, w, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, twin, "MedC-TWIN", "Fit", "2027-01-01", "Open", 1)

	got, err := s.CandidateRecords(ctx, id)
	if err != nil || len(got) != 1 || got[0].CheckUpID != "MedC-1" {
		t.Fatalf("picked patient candidates = %v, %v; want only MedC-1 (a picked patient is matched by id, not by birth date)", got, err)
	}
	draft := addClean(t, s, sub, "Worker Three")
	if _, err := s.CandidateRecords(ctx, draft); !errors.Is(err, ErrBadState) {
		t.Errorf("a Draft = %v, want ErrBadState", err)
	}
	if _, err := s.CandidateRecords(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}

func TestRecordsRouteReturnsTheListAsJSON(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Worker One", true)
	w := seedWorker(t, e.store, "Worker One", "1990-04-12", 0)
	addRecord(t, e.store, w, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	path := fmt.Sprintf("/api/mcu-submissions/%d/records", id)
	if rec := e.do("GET", path, "", true); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", rec.Code)
	}
	rec := e.do("GET", path, "", false)
	var list []CandidateRecord
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].CheckUpID != "MedC-1" {
		t.Fatalf("records = %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("GET", "/api/mcu-submissions/9999/records", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", rec.Code)
	}
}

// closeWithRecord is the whole staff path in one call: link the checkup, then notify (close).
func closeWithRecord(s *Store, ctx context.Context, id, staff int64, checkUpID string) error {
	if err := s.LinkRecord(ctx, id, staff, checkUpID); err != nil {
		return err
	}
	return s.CloseReviewed(ctx, id, staff)
}

func TestLinkRecordReviewsOnlyWhenTheCheckupHasAResultAndCloseNeedsReviewed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrlink@example.com")
	w1 := seedWorker(t, s, "Has Result", "1990-04-12", co)
	w2 := seedWorker(t, s, "No Result", "1991-04-12", co)
	addRecord(t, s, w1, "MedC-R1", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, w2, "MedC-R2", "Fit", "2027-01-01", "Open", 1)
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = NULL WHERE CheckUpID = 'MedC-R2'`); err != nil {
		t.Fatal(err)
	}
	a, b := sentDocIn(t, s, sub, "Has Result", "No Result")
	for id, p := range map[int64]int64{a: w1, b: w2} {
		if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, p, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("closing an unlinked Pending document: %v, want ErrBadState", err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-R1"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, b, testStaffID, "MedC-R2"); err != nil {
		t.Fatal(err)
	}
	status := func(id int64) string {
		var st string
		_ = s.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&st)
		return st
	}
	if status(a) != SubReviewed || status(b) != SubPending {
		t.Fatalf("after linking: %s and %s, want Reviewed and Pending", status(a), status(b))
	}
	if err := s.CloseReviewed(ctx, b, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("closing a checkup with no result: %v, want ErrBadState", err)
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); err != nil {
		t.Fatal(err)
	}
	if status(a) != SubClosed {
		t.Fatalf("status %s, want Closed", status(a))
	}
}

func TestResultMessageShownToTheSubmitterOnlyOnceClosed(t *testing.T) {
	cases := []struct {
		status, result, want string
	}{
		{SubClosed, "NeedFollowUp", "REC"},
		{SubClosed, "TemporaryUnfit", "NOTES"},
		{SubClosed, "Unfit", "NOTES"},
		{SubClosed, "Fit", ""},
		{SubReviewed, "Unfit", ""}, // not told yet
	}
	for _, c := range cases {
		d := Submission{Status: c.status, Result: c.result, ResultNotes: "NOTES", Recommendation: "REC"}
		if got := d.ResultMessage(); got != c.want {
			t.Errorf("%s/%s = %q, want %q", c.status, c.result, got, c.want)
		}
	}
}

func TestLinkAgainSameRecordIsAllowedAndUnlinkUndoesAWrongLink(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrun@example.com")
	w := seedWorker(t, s, "Un Link", "1990-04-12", co)
	addRecord(t, s, w, "MedC-U1", "Fit", "2027-01-01", "Open", 1)
	addRecord(t, s, w, "MedC-U2", "Fit", "2027-01-01", "Open", 1)
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = NULL WHERE CheckUpID = 'MedC-U1'`); err != nil {
		t.Fatal(err)
	}
	a, _ := sentDocIn(t, s, sub, "Un Link", "Other One")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		var st string
		_ = s.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, a).Scan(&st)
		return st
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-U1"); err != nil || status() != SubPending {
		t.Fatalf("link without a result: %v, %s", err, status())
	}
	// the reviewer edits the checkup and adds the result, then saves again: the same link again
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = 'Fit' WHERE CheckUpID = 'MedC-U1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-U1"); err != nil || status() != SubReviewed {
		t.Fatalf("link again: %v, %s", err, status())
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-U2"); !errors.Is(err, ErrBadState) {
		t.Fatalf("pointing at another record: %v, want ErrBadState", err)
	}
	if err := s.UnlinkRecord(ctx, a, testStaffID); err != nil || status() != SubPending {
		t.Fatalf("unlink: %v, %s", err, status())
	}
	if err := s.UnlinkRecord(ctx, a, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("unlinking twice: %v, want ErrBadState", err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-U2"); err != nil || status() != SubReviewed {
		t.Fatalf("relink: %v, %s", err, status())
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlinkRecord(ctx, a, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("unlinking a Closed document: %v, want ErrBadState", err)
	}
}

func TestCloseReviewedRefusesACheckupWhoseResultWasClearedAfterLinking(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrclr@example.com")
	w := seedWorker(t, s, "Clear Result", "1990-04-12", co)
	addRecord(t, s, w, "MedC-CLR", "Fit", "2027-01-01", "Open", 1)
	a, _ := sentDocIn(t, s, sub, "Clear Result", "Other Two")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-CLR"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = NULL WHERE CheckUpID = 'MedC-CLR'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("closing with the result cleared: %v, want ErrBadState", err)
	}
}

// The submitter can be told about any result (owner, 2026-11-09), but an IncompleteResult checkup
// stays Open, so notifying then leaves the document Reviewed; the final result closes it and mails
// again. A document closed earlier with an Incomplete result is put back to Reviewed at start.
func TestIncompleteResultNotifiesButKeepsTheDocumentReviewed(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrinc@example.com")
	w := seedWorker(t, s, "Inc Omplete", "1990-04-12", co)
	addRecord(t, s, w, "MedC-INC", "Fit", "2027-01-01", "Open", 1)
	// The local SQLite schema still has the old CHECK list (no IncompleteResult, which the settings
	// list adds), so write the value on one connection with that check switched off.
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{`PRAGMA ignore_check_constraints = ON`,
		`UPDATE McuRecord SET ResultCriteria = 'IncompleteResult' WHERE CheckUpID = 'MedC-INC'`,
		`PRAGMA ignore_check_constraints = OFF`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := sentDocIn(t, s, sub, "Inc Omplete", "Other Three")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-INC"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); err != nil {
		t.Fatalf("notifying about an incomplete result: %v", err)
	}
	var status string
	var requested, told sql.NullString
	read := func() {
		t.Helper()
		if err := s.DB.QueryRow(`SELECT Status, NotifyRequestedAt, CertNotifiedAt FROM McuSubmission WHERE SubmissionID = ?`, a).Scan(&status, &requested, &told); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if status != SubReviewed || !requested.Valid {
		t.Fatalf("after notify: status %q requested %v, want Reviewed and requested", status, requested)
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); !errors.Is(err, ErrBadState) {
		t.Fatalf("notifying twice: %v, want ErrBadState", err)
	}
	// The daily mail picks it up while it is still Reviewed.
	due, err := s.dueClosedNotices(ctx)
	if err != nil || len(due) != 1 || len(due[0].lines) != 1 {
		t.Fatalf("due notices %v (%v), want the one Reviewed document", due, err)
	}
	// The final result: notifying again closes it and makes it due for the mail once more.
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = 'Fit' WHERE CheckUpID = 'MedC-INC'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET CertNotifiedAt = '2026-11-09T01:00:00' WHERE SubmissionID = ?`, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseReviewed(ctx, a, testStaffID); err != nil {
		t.Fatalf("closing with the final result: %v", err)
	}
	read()
	if status != SubClosed || told.Valid || requested.Valid {
		t.Fatalf("after close: status %q told %v requested %v, want Closed, mail due again", status, told, requested)
	}
	// Closed earlier with an Incomplete result: back to Reviewed, and not mailed twice.
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = 'Fit' WHERE CheckUpID = 'MedC-INC'`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`PRAGMA ignore_check_constraints = ON`,
		`UPDATE McuRecord SET ResultCriteria = 'IncompleteResult' WHERE CheckUpID = 'MedC-INC'`,
		`PRAGMA ignore_check_constraints = OFF`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET CertNotifiedAt = '2026-11-09T02:00:00' WHERE SubmissionID = ?`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NormalizeReviewed(ctx); err != nil {
		t.Fatal(err)
	}
	read()
	if status != SubReviewed || !requested.Valid || !told.Valid {
		t.Fatalf("after normalize: status %q requested %v told %v, want Reviewed, requested, still marked told", status, requested, told)
	}
}

// Saving the follow-up checkup (the MCU table stamps the open record "Renewed by <ID>") closes the
// old document with an Incomplete result, as the table closes the record, without a second mail.
// A Fit document renewed the same way is left alone: it keeps its own notify step.
func TestCloseSupersededDocumentsFollowsTheMcuTableRenewal(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrsup@example.com")
	linked := func(name, checkUpID string) int64 {
		w := seedWorker(t, s, name, "1990-04-12", co)
		addRecord(t, s, w, checkUpID, "Fit", "2027-01-01", "Open", 0)
		id, _ := sentDocIn(t, s, sub, name, "Other "+name)
		if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, id); err != nil {
			t.Fatal(err)
		}
		if err := s.LinkRecord(ctx, id, testStaffID, checkUpID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	inc, fit := linked("Sup Incomplete", "MedC-SUP1"), linked("Sup Fit", "MedC-SUP2")
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{`PRAGMA ignore_check_constraints = ON`,
		`UPDATE McuRecord SET ResultCriteria = 'IncompleteResult' WHERE CheckUpID = 'MedC-SUP1'`,
		`PRAGMA ignore_check_constraints = OFF`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id int64) (st string, told sql.NullString) {
		t.Helper()
		if err := s.DB.QueryRow(`SELECT Status, CertNotifiedAt FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&st, &told); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n, err := s.CloseSupersededDocuments(ctx); err != nil || n != 0 {
		t.Fatalf("nothing renewed yet: closed %d (%v), want 0", n, err)
	}
	// What closeParentRecord writes when the follow-up is saved.
	if _, err := s.DB.Exec(`UPDATE McuRecord SET HcValidityStatus = 'Renewed by MedC-NEW' WHERE CheckUpID IN ('MedC-SUP1', 'MedC-SUP2')`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CloseSupersededDocuments(ctx); err != nil || n != 1 {
		t.Fatalf("closed %d (%v), want only the Incomplete one", n, err)
	}
	if st, told := status(inc); st != SubClosed || !told.Valid {
		t.Fatalf("incomplete document: %q told %v, want Closed and marked told (no second mail)", st, told)
	}
	if st, _ := status(fit); st != SubReviewed {
		t.Fatalf("fit document: %q, want it left Reviewed", st)
	}
	if _, err := s.NormalizeReviewed(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := status(inc); st != SubClosed {
		t.Fatalf("after normalize: %q, a superseded document must stay Closed", st)
	}
	docs, err := s.querySubmissions(ctx, `WHERE s.SubmissionID = ?`, inc)
	if err != nil || len(docs) != 1 || docs[0].RenewedBy != "Renewed by MedC-NEW" {
		t.Fatalf("detail %v (%v), want \"Renewed by MedC-NEW\"", docs, err)
	}
}

func TestWithdrawFreesALinkedCheckupAndAskedBeforeInThePage(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrwd@example.com")
	w := seedWorker(t, s, "With Drawn", "1990-04-12", co)
	addRecord(t, s, w, "MedC-WD", "Fit", "2027-01-01", "Open", 1)
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = NULL WHERE CheckUpID = 'MedC-WD'`); err != nil {
		t.Fatal(err)
	}
	a, b := sentDocIn(t, s, sub, "With Drawn", "Other Doc")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-WD"); err != nil { // linked, no result: still Pending
		t.Fatal(err)
	}
	if _, err := s.Withdraw(ctx, sub, a); err != nil {
		t.Fatal(err)
	}
	// the record is free again for the other document
	if err := s.LinkRecord(ctx, b, testStaffID, "MedC-WD"); err != nil && !errors.Is(err, ErrRecordMismatch) {
		t.Fatalf("the record stayed tied to the withdrawn document: %v", err)
	}
}

// Reviewed means a decision was saved. Linking a checkup with no result records no reviewer and no
// date, unlinking clears them, and old documents marked Reviewed on opening go back to Pending.
func TestReviewedMeansADecisionNotJustOpened(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrdec@example.com")
	w := seedWorker(t, s, "Dec Ision", "1990-04-12", co)
	addRecord(t, s, w, "MedC-DEC", "Fit", "2027-01-01", "Open", 1)
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = NULL WHERE CheckUpID = 'MedC-DEC'`); err != nil {
		t.Fatal(err)
	}
	a, old := sentDocIn(t, s, sub, "Dec Ision", "Opened Only")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ? WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	reviewedAt := func(id int64) (st, at string, rev int64) {
		var by sql.NullInt64
		_ = s.DB.QueryRow(`SELECT Status, COALESCE(ReviewedAt, ''), FK_ReviewerStaffID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&st, &at, &by)
		return st, at, by.Int64
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-DEC"); err != nil {
		t.Fatal(err)
	}
	if st, at, by := reviewedAt(a); st != SubPending || at != "" || by != 0 {
		t.Fatalf("linked without a result: %s, %q, %d; want Pending and no reviewer or date", st, at, by)
	}
	if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = 'Fit' WHERE CheckUpID = 'MedC-DEC'`); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-DEC"); err != nil {
		t.Fatal(err)
	}
	if st, at, by := reviewedAt(a); st != SubReviewed || at == "" || by != testStaffID {
		t.Fatalf("with a result: %s, %q, %d; want Reviewed with the reviewer and date", st, at, by)
	}
	if err := s.UnlinkRecord(ctx, a, testStaffID); err != nil {
		t.Fatal(err)
	}
	if st, at, by := reviewedAt(a); st != SubPending || at != "" || by != 0 {
		t.Fatalf("after unlink: %s, %q, %d", st, at, by)
	}
	// a document the old Review button marked Reviewed with nothing linked goes back to Pending
	if err := s.MarkReviewed(ctx, old, testStaffID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.NormalizeReviewed(ctx); err != nil || n != 1 {
		t.Fatalf("normalize = %d, %v; want 1", n, err)
	}
	if st, at, _ := reviewedAt(old); st != SubPending || at != "" {
		t.Fatalf("old document: %s, %q", st, at)
	}
	if n, _ := s.NormalizeReviewed(ctx); n != 0 {
		t.Errorf("second run changed %d documents", n)
	}
}

func TestRemoveSubmissionRemovesTheRowKeepsTheCheckupAndLogsWithoutPersonalData(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrrm@example.com")
	w := seedWorker(t, s, "Remove Me", "1990-04-12", co)
	addRecord(t, s, w, "MedC-RM", "Fit", "2027-01-01", "Open", 1)
	a, b := sentDocIn(t, s, sub, "Remove Me", "Other Doc")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET FK_PatientID = ?, FilePath = 'files/abc.pdf' WHERE SubmissionID = ?`, w, a); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkRecord(ctx, a, testStaffID, "MedC-RM"); err != nil {
		t.Fatal(err)
	}
	ref, err := s.RemoveSubmission(ctx, a, testStaffID)
	if err != nil || ref != "files/abc.pdf" {
		t.Fatalf("remove = %q, %v; want the stored file reference back", ref, err)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmission WHERE SubmissionID = ?`, a).Scan(&n)
	if n != 0 {
		t.Error("the document row is still there")
	}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM McuRecord WHERE CheckUpID = 'MedC-RM'`).Scan(&n)
	if n != 1 {
		t.Error("the linked checkup must stay")
	}
	var detail string
	if err := s.DB.QueryRow(`SELECT Detail FROM McuSubmissionEvent WHERE Action = 'Removed' AND EntityID = ?`, a).Scan(&detail); err != nil || strings.Contains(detail, "Remove Me") {
		t.Errorf("event detail %q, %v: must exist and carry no name", detail, err)
	}
	if _, err := s.RemoveSubmission(ctx, a, testStaffID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v, want ErrNotFound", err)
	}
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET Status = 'Draft' WHERE SubmissionID = ?`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveSubmission(ctx, b, testStaffID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing a draft: %v, want ErrNotFound (unsent work is not staff's)", err)
	}
}
