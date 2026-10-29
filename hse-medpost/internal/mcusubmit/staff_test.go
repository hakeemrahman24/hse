package mcusubmit

import (
	"bytes"
	"context"
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
	mux.HandleFunc("GET /api/mcu-submissions/{id}/file", api.File)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/review", api.MarkReviewed)
	mux.HandleFunc("POST /api/mcu-submissions/{id}/reject", api.Reject)
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
		{"POST", fmt.Sprintf("/api/mcu-submissions/%d/review", id)}, {"POST", fmt.Sprintf("/api/mcu-submissions/%d/reject", id)},
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

	if rec := e.do("POST", fmt.Sprintf("/api/mcu-submissions/%d/review", a), "", false); rec.Code != http.StatusNoContent {
		t.Fatalf("review = %d\n%s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", fmt.Sprintf("/api/mcu-submissions/%d/review", a), "", false); rec.Code != http.StatusConflict {
		t.Errorf("review again = %d, want 409", rec.Code)
	}
	for _, c := range []struct {
		body string
		want int
	}{{`{"reason":""}`, http.StatusBadRequest}, {`not json`, http.StatusBadRequest}, {`{"reason":"Unreadable scan"}`, http.StatusNoContent}} {
		if rec := e.do("POST", fmt.Sprintf("/api/mcu-submissions/%d/reject", b), c.body, false); rec.Code != c.want {
			t.Errorf("reject %q = %d, want %d", c.body, rec.Code, c.want)
		}
	}
	if rec := e.do("POST", "/api/mcu-submissions/9999/review", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", rec.Code)
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

	if err := s.CloseWithRecord(ctx, a, testStaffID, " MedC-1 "); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWithRecord(ctx, b, testStaffID, "MedC-2"); err != nil {
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

	if err := s.CloseWithRecord(ctx, a, testStaffID, "NOPE"); !errors.Is(err, ErrRecordUnknown) {
		t.Errorf("unknown checkup id = %v, want ErrRecordUnknown", err)
	}
	if err := s.CloseWithRecord(ctx, a, testStaffID, "MedC-2"); !errors.Is(err, ErrRecordMismatch) {
		t.Errorf("another patient's record = %v, want ErrRecordMismatch", err)
	}
	if err := s.CloseWithRecord(ctx, draft, testStaffID, "MedC-1"); !errors.Is(err, ErrBadState) {
		t.Errorf("a Draft = %v, want ErrBadState", err)
	}
	if err := s.CloseWithRecord(ctx, 9999, testStaffID, "MedC-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing submission = %v, want ErrNotFound", err)
	}
	if err := s.CloseWithRecord(ctx, a, testStaffID, "MedC-1"); err != nil {
		t.Fatal(err)
	}
	// b has no patient yet (a new employee); the same record cannot close a second document.
	if err := s.CloseWithRecord(ctx, b, testStaffID, "MedC-1"); !errors.Is(err, ErrRecordTaken) {
		t.Errorf("a record already linked = %v, want ErrRecordTaken", err)
	}
	if err := s.CloseWithRecord(ctx, a, testStaffID, "MedC-1"); !errors.Is(err, ErrBadState) {
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

	if err := s.CloseWithRecord(ctx, id, testStaffID, "MedC-9"); err != nil {
		t.Fatal(err)
	}
	var patient int64
	_ = s.DB.QueryRow(`SELECT FK_PatientID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&patient)
	if patient != w {
		t.Fatalf("patient = %d, want %d: the expiry reminders need the link", patient, w)
	}
}

func TestCloseRouteMapsEachRefusalToAClearStatus(t *testing.T) {
	e := newStaffEnv(t)
	id := e.doc(t, "Worker One", true)
	w := seedWorker(t, e.store, "Worker One", "1990-04-12", 0)
	addRecord(t, e.store, w, "MedC-1", "Fit", "2027-01-01", "Open", 1)
	path := fmt.Sprintf("/api/mcu-submissions/%d/close", id)

	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, true); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", rec.Code)
	}
	if rec := e.do("POST", path, `not json`, false); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", path, `{"checkUpId":"NOPE"}`, false); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown record = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/mcu-submissions/9999/close", `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusNotFound {
		t.Errorf("missing document = %d, want 404", rec.Code)
	}
	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusNoContent {
		t.Fatalf("close = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", path, `{"checkUpId":"MedC-1"}`, false); rec.Code != http.StatusConflict {
		t.Errorf("closing twice = %d, want 409", rec.Code)
	}
}

// The count in CloseWithRecord is only the friendly first line; two staff members closing at the
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
