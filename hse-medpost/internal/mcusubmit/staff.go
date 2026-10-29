package mcusubmit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/middleware"
)

// maxRejectReason is the longest rejection reason (RejectReason is shown to the submitter).
const maxRejectReason = 200

// ErrBadReason means a rejection had no reason, or one that is too long.
var ErrBadReason = errors.New("mcusubmit: a rejection needs a reason of 1 to 200 characters")

var (
	// ErrRecordUnknown: no McuRecord has the CheckUpID staff typed.
	ErrRecordUnknown = errors.New("mcusubmit: no checkup record has that ID")
	// ErrRecordTaken: that McuRecord already closed another document.
	ErrRecordTaken = errors.New("mcusubmit: that checkup record is already linked to another document")
	// ErrRecordMismatch: the record belongs to a different patient than the document's worker.
	ErrRecordMismatch = errors.New("mcusubmit: that checkup record belongs to another patient")
)

// stateError tells "no such document" from "wrong status" after a guarded UPDATE touched no row.
func (s *Store) stateError(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return ErrBadState
}

// MarkReviewed records that staff looked at a sent document. Only a Pending document can be
// reviewed: a Draft or a document under Review is unsent work that staff must not touch. The
// guarded UPDATE makes two staff members racing each other safe: the second one gets ErrBadState.
func (s *Store) MarkReviewed(ctx context.Context, id, staffID int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ?, ReviewedAt = ?, FK_ReviewerStaffID = ? WHERE SubmissionID = ? AND Status = ?`,
		SubReviewed, s.stamp(), staffID, id, SubPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return s.stateError(ctx, id)
	}
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "Reviewed"})
}

// RejectSubmission refuses a Pending or Reviewed document with a reason the submitter will see.
// The file is kept until its normal retention date, like any other document.
func (s *Store) RejectSubmission(ctx context.Context, id, staffID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxRejectReason {
		return ErrBadReason
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ?, RejectReason = ?, ReviewedAt = ?, FK_ReviewerStaffID = ? WHERE SubmissionID = ? AND Status IN (?, ?)`,
		SubRejected, reason, s.stamp(), staffID, id, SubPending, SubReviewed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return s.stateError(ctx, id)
	}
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "Rejected", Detail: reason})
}

// CloseWithRecord closes a Pending or Reviewed document with the checkup record staff made for it
// on the Medical Checkups page, found by its CheckUpID. The record must belong to the document's
// worker when the worker is already known; for a new employee the record's patient becomes the
// document's patient, which is what later lets the expiry reminders find them. A record can close
// only one document. (The "already linked" check is a read before the write: two staff linking the
// same record in the same instant could both pass. MariaDB does not allow the update to look at its
// own table in a subquery, and this is a rare, manual action.)
func (s *Store) CloseWithRecord(ctx context.Context, id, staffID int64, checkUpID string) error {
	checkUpID = strings.TrimSpace(checkUpID)
	var status string
	var patient sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT Status, FK_PatientID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status, &patient)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != SubPending && status != SubReviewed {
		return ErrBadState
	}
	var recordID, recordPatient int64
	err = s.DB.QueryRowContext(ctx, `SELECT McuRecordID, FK_PatientID FROM McuRecord WHERE CheckUpID = ?`, checkUpID).Scan(&recordID, &recordPatient)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRecordUnknown
	}
	if err != nil {
		return err
	}
	if patient.Valid && patient.Int64 != recordPatient {
		return ErrRecordMismatch
	}
	var taken int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM McuSubmission WHERE FK_McuRecordID = ?`, recordID).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ErrRecordTaken
	}
	now := s.stamp()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ?, FK_McuRecordID = ?, FK_PatientID = COALESCE(FK_PatientID, ?),
		   ReviewedAt = COALESCE(ReviewedAt, ?), FK_ReviewerStaffID = COALESCE(FK_ReviewerStaffID, ?)
		 WHERE SubmissionID = ? AND Status IN (?, ?)`,
		SubClosed, recordID, recordPatient, now, staffID, id, SubPending, SubReviewed)
	// The count above gives the friendly answer; the unique index uq_mcusub_mcurecord is the real
	// guard when two staff members link the same record in the same instant.
	if isUniqueViolation(err) {
		return ErrRecordTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return s.stateError(ctx, id)
	}
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "Closed", Detail: checkUpID})
}

// ---- HTTP for staff ---------------------------------------------------------------------------

// StaffAPI is the staff side of the review queue. It lives in this package because it shares the
// store and the file rules, but it is mounted on the staff app's own router behind its login
// (RequireAuth and the clinician check); it never uses the portal cookie.
type StaffAPI struct {
	Store *Store
	Files FileStore
}

// staffRow is one line of the review list. It has no file path: staff open files only through File.
type staffRow struct {
	ID            int64  `json:"id"`
	Worker        string `json:"worker"`
	DateOfBirth   string `json:"dob"`
	Position      string `json:"position"`
	Company       string `json:"company"`
	Nationality   string `json:"nationality"`
	Submitter     string `json:"submitter"`
	Status        string `json:"status"`
	RejectReason  string `json:"rejectReason"`
	SentAt        string `json:"sentAt"`
	HasFile       bool   `json:"hasFile"`
	IsNewEmployee bool   `json:"isNewEmployee"`
}

var reviewStatuses = map[string]bool{"": true, SubPending: true, SubReviewed: true, SubClosed: true, SubRejected: true, SubWithdrawn: true}

// staffID returns the signed-in staff member's id, or answers 401.
func staffID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	sess := middleware.Session(r)
	if sess == nil || sess.StaffID == 0 {
		http.Error(w, "Sign in first.", http.StatusUnauthorized)
		return 0, false
	}
	return sess.StaffID, true
}

// List returns the review queue, newest batch first. Drafts and documents still under Review are
// never in it, and Withdrawn ones only when that status is asked for.
func (a *StaffAPI) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := staffID(w, r); !ok {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	result, err := a.Store.SearchDocs(r.Context(), DocFilter{
		Status: q.Get("status"), Worker: q.Get("worker"), Company: q.Get("company"), Submitter: q.Get("submitter"),
		From: q.Get("from"), To: q.Get("to"), Page: page,
	})
	if errors.Is(err, ErrBadFilter) {
		http.Error(w, "Unknown status or date.", http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("mcusubmit: staff list: %v", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	docs := result.Rows
	rows := make([]staffRow, 0, len(docs))
	for _, d := range docs {
		rows = append(rows, staffRow{
			ID: d.ID, Worker: d.PatName, DateOfBirth: d.PatDateOfBirth, Position: d.PatPosition, Company: d.PatCompanyText,
			Nationality: d.PatNationality, Submitter: d.SubmitterName, Status: d.Status, RejectReason: d.RejectReason,
			SentAt: d.SentAt, HasFile: d.HasFile, IsNewEmployee: d.IsNewEmployee,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// The body stays a plain array; the paging numbers travel in headers.
	w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
	w.Header().Set("X-Page", strconv.Itoa(result.Page))
	w.Header().Set("X-Pages", strconv.Itoa(result.Pages))
	_ = json.NewEncoder(w).Encode(rows)
}

// File serves a sent document's PDF. A Draft, a document under Review, a withdrawn one and a missing
// one all give the same 404. Each open is written to the audit trail with the staff member's id,
// because these are health records.
func (a *StaffAPI) File(w http.ResponseWriter, r *http.Request) {
	staff, ok := staffID(w, r)
	if !ok {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if a.Files == nil { // the file folder could not be opened at start-up
		http.NotFound(w, r)
		return
	}
	ref, err := a.Store.StaffFile(r.Context(), id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: staff file: %v", err)
		}
		http.NotFound(w, r)
		return
	}
	rc, err := a.Files.Open(r.Context(), ref)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: staff open file: %v", err)
		}
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorStaff, ActorID: staff, EntityType: EntitySubmission, EntityID: id, Action: "FileViewed"}); err != nil {
		log.Printf("mcusubmit: log staff file view: %v", err)
	}
	servePDF(w, rc)
}

// MarkReviewed marks a Pending document Reviewed.
func (a *StaffAPI) MarkReviewed(w http.ResponseWriter, r *http.Request) {
	a.act(w, r, func(ctx context.Context, id, staff int64) error { return a.Store.MarkReviewed(ctx, id, staff) })
}

// Reject refuses a document; the JSON body is {"reason": "..."}.
func (a *StaffAPI) Reject(w http.ResponseWriter, r *http.Request) {
	if _, ok := staffID(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "The request could not be read.", http.StatusBadRequest)
		return
	}
	a.act(w, r, func(ctx context.Context, id, staff int64) error {
		return a.Store.RejectSubmission(ctx, id, staff, body.Reason)
	})
}

// Close links the checkup record staff made and closes the document; the JSON body is
// {"checkUpId": "..."}.
func (a *StaffAPI) Close(w http.ResponseWriter, r *http.Request) {
	if _, ok := staffID(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	var body struct {
		CheckUpID string `json:"checkUpId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "The request could not be read.", http.StatusBadRequest)
		return
	}
	a.act(w, r, func(ctx context.Context, id, staff int64) error {
		return a.Store.CloseWithRecord(ctx, id, staff, body.CheckUpID)
	})
}

func (a *StaffAPI) act(w http.ResponseWriter, r *http.Request, do func(context.Context, int64, int64) error) {
	staff, ok := staffID(w, r)
	if !ok {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch err := do(r.Context(), id, staff); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, ErrBadState):
		http.Error(w, "This document is not in a status that allows that.", http.StatusConflict)
	case errors.Is(err, ErrBadReason):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrRecordUnknown):
		http.Error(w, "No checkup record has that ID. Create it on the Medical Checkups page first.", http.StatusBadRequest)
	case errors.Is(err, ErrRecordTaken):
		http.Error(w, "That checkup record is already linked to another document.", http.StatusConflict)
	case errors.Is(err, ErrRecordMismatch):
		http.Error(w, "That checkup record belongs to a different patient than this worker.", http.StatusConflict)
	default:
		log.Printf("mcusubmit: staff action: %v", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
	}
}

// servePDF writes a stored file with the headers every health-record PDF here carries: the type is
// fixed and must not be sniffed, nothing may be cached, and `sandbox` stops anything in the file
// from running as part of our origin.
func servePDF(w http.ResponseWriter, rc io.Reader) {
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, rc)
}
