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

// StaffDoc is one document as staff see it. Draft and Review are unsent work, so they are
// ErrNotFound, the same as for the list.
func (s *Store) StaffDoc(ctx context.Context, id int64) (Submission, error) {
	docs, err := s.querySubmissions(ctx, `WHERE s.SubmissionID = ? AND s.Status NOT IN (?, ?)`, id, SubDraft, SubReview)
	if err != nil {
		return Submission{}, err
	}
	if len(docs) == 0 {
		return Submission{}, ErrNotFound
	}
	return docs[0], nil
}

// MarkReviewed marks a Pending document Reviewed. No HTTP route calls it any more (2026-11-01):
// Reviewed now means a linked checkup with a result, which only LinkRecord sets. It stays as the
// single place that writes that transition, and tests use it to set up Reviewed documents.
// Original note: it records that staff looked at a sent document. Only a Pending document can be
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

// LinkRecord links a Pending document to the checkup record staff made for it on the Medical
// Checkups page, found by its CheckUpID. Owner's rule (2026-10-31): a document counts as Reviewed
// only when its worker has a checkup WITH a result (fit, unfit, ...), so opening a document
// changes nothing, and linking a record that has no result leaves it Pending. Closing is a
// separate step (CloseReviewed), because Closed is what tells the submitter. The record must belong to the document's
// worker. When the worker is already known (a picked patient) that is a plain patient-id match.
// For a new employee there is no patient yet, so the record's patient must be the same person by
// the facts the submitter typed: the same date of birth, or the same ID number. Without that check
// any record could be linked to any new-employee document, and the submitter could then download
// somebody else's certificate. On success the record's patient becomes the document's patient,
// which is what later lets the expiry reminders find them. A record can close only one document.
// (The "already linked" check is a read before the write: two staff linking the same record in the
// same instant could both pass. MariaDB does not allow the update to look at its own table in a
// subquery, and this is a rare, manual action.)
func (s *Store) LinkRecord(ctx context.Context, id, staffID int64, checkUpID string) error {
	checkUpID = strings.TrimSpace(checkUpID)
	var status, dob, nik, passport string
	var patient, linked sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT Status, FK_PatientID, PatDateOfBirth, COALESCE(PatNik, ''), COALESCE(PatPassportNo, ''), FK_McuRecordID FROM McuSubmission WHERE SubmissionID = ?`, id).
		Scan(&status, &patient, &dob, &nik, &passport, &linked)
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
	var recordDOB, recordPersonalID, recordResult string
	err = s.DB.QueryRowContext(ctx,
		`SELECT r.McuRecordID, r.FK_PatientID, COALESCE(p.DateOfBirth, ''), COALESCE(p.PersonalID, ''), COALESCE(r.ResultCriteria, '')
		 FROM McuRecord r JOIN Patient p ON p.PatientID = r.FK_PatientID WHERE r.CheckUpID = ?`, checkUpID).
		Scan(&recordID, &recordPatient, &recordDOB, &recordPersonalID, &recordResult)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRecordUnknown
	}
	if err != nil {
		return err
	}
	if patient.Valid {
		if patient.Int64 != recordPatient {
			return ErrRecordMismatch
		}
	} else if !sameWorker(dob, nik, passport, recordDOB, recordPersonalID) {
		return ErrRecordMismatch
	}
	// Saving the checkup again after editing it (for example to add the result) links the SAME record
	// again, which is allowed; pointing the document at a different record is not (Unlink first).
	if linked.Valid && linked.Int64 != recordID {
		return ErrBadState
	}
	var taken int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM McuSubmission WHERE FK_McuRecordID = ? AND SubmissionID <> ?`, recordID, id).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ErrRecordTaken
	}
	now := s.stamp()
	// A result turns Pending into Reviewed; without one the record is linked but the status stays.
	// The record must be unset or the same one: a linked document is never silently re-pointed.
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = CASE WHEN ? <> '' THEN ? ELSE Status END, FK_McuRecordID = ?, FK_PatientID = COALESCE(FK_PatientID, ?),
		   ReviewedAt = COALESCE(ReviewedAt, ?), FK_ReviewerStaffID = COALESCE(FK_ReviewerStaffID, ?)
		 WHERE SubmissionID = ? AND Status IN (?, ?) AND (FK_McuRecordID IS NULL OR FK_McuRecordID = ?)`,
		recordResult, SubReviewed, recordID, recordPatient, now, staffID, id, SubPending, SubReviewed, recordID)
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
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "RecordLinked", Detail: checkUpID})
}

// UnlinkRecord undoes a wrong link, for a document that has not been closed yet (once Closed the
// submitter has been told and may have downloaded the certificate, so that is final). The document
// goes back to Pending, because Reviewed means a linked checkup with a result. The checkup record
// itself is not touched.
func (s *Store) UnlinkRecord(ctx context.Context, id, staffID int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ?, FK_McuRecordID = NULL WHERE SubmissionID = ? AND Status IN (?, ?) AND FK_McuRecordID IS NOT NULL`,
		SubPending, id, SubPending, SubReviewed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return s.stateError(ctx, id)
	}
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "RecordUnlinked"})
}

// CloseReviewed closes a Reviewed document at the moment the reviewer notifies the submitter (owner,
// 2026-10-31: Closed means the submitter was told). The daily "your checkups are done" mail looks
// for Closed documents. Only a Reviewed document with a
// linked checkup can close, which is the same as "the worker has a checkup with a result".
func (s *Store) CloseReviewed(ctx context.Context, id, staffID int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ? AND Status = ? AND FK_McuRecordID IS NOT NULL
		   AND EXISTS (SELECT 1 FROM McuRecord r WHERE r.McuRecordID = McuSubmission.FK_McuRecordID AND r.ResultCriteria IS NOT NULL)`,
		SubClosed, id, SubReviewed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return s.stateError(ctx, id)
	}
	return s.LogEvent(ctx, Event{ActorType: ActorStaff, ActorID: staffID, EntityType: EntitySubmission, EntityID: id, Action: "Closed"})
}

// sameWorker says whether the patient of a checkup record is the person a new-employee document is
// about: the same date of birth, or the same ID number (NIK or passport, spaces and hyphens
// ignored). Names are not compared, because staff correct the spelling the submitter typed.
func sameWorker(dob, nik, passport, patientDOB, patientPersonalID string) bool {
	if len(dob) >= 10 && len(patientDOB) >= 10 && dob[:10] == patientDOB[:10] {
		return true
	}
	have := idKey(patientPersonalID)
	return have != "" && (have == idKey(nik) || have == idKey(passport))
}

func idKey(s string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(s))
}

// CandidateRecord is a checkup record a reviewer could link to a document. The Submitted tab lists
// these so nobody has to remember or type a Checkup ID.
type CandidateRecord struct {
	CheckUpID   string `json:"checkUpId"`
	DateCheckUp string `json:"dateCheckUp"`
	Result      string `json:"result"`
}

// maxCandidateRecords keeps the pick-list short; a worker rarely has more unlinked checkups.
const maxCandidateRecords = 20

// CandidateRecords lists the checkups of this document's worker that no document is linked to yet,
// newest first. For a picked patient that is the patient's own records. A new employee has no
// patient, so it is the patients who are the same person by the same rule LinkRecord applies
// (same date of birth, or same ID number): anything the list shows, a link will accept.
func (s *Store) CandidateRecords(ctx context.Context, id int64) ([]CandidateRecord, error) {
	var status, dob, nik, passport string
	var patient sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT Status, FK_PatientID, PatDateOfBirth, COALESCE(PatNik, ''), COALESCE(PatPassportNo, '') FROM McuSubmission WHERE SubmissionID = ?`, id).
		Scan(&status, &patient, &dob, &nik, &passport)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != SubPending && status != SubReviewed {
		return nil, ErrBadState
	}
	who := `(p.DateOfBirth = ? OR (? <> '' AND REPLACE(REPLACE(UPPER(COALESCE(p.PersonalID, '')), ' ', ''), '-', '') = ?))`
	args := []any{dob, idKey(firstNonEmpty(nik, passport)), idKey(firstNonEmpty(nik, passport))}
	if patient.Valid {
		who, args = `p.PatientID = ?`, []any{patient.Int64}
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT r.CheckUpID, COALESCE(r.DateCheckUp, ''), COALESCE(r.ResultCriteria, '')
		 FROM McuRecord r JOIN Patient p ON p.PatientID = r.FK_PatientID
		 WHERE `+who+` AND NOT EXISTS (SELECT 1 FROM McuSubmission m WHERE m.FK_McuRecordID = r.McuRecordID)
		 ORDER BY r.McuRecordID DESC LIMIT `+strconv.Itoa(maxCandidateRecords), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CandidateRecord{}
	for rows.Next() {
		var c CandidateRecord
		if err := rows.Scan(&c.CheckUpID, &c.DateCheckUp, &c.Result); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
	ReviewedAt    string `json:"reviewedAt"`
	ReviewedBy    string `json:"reviewedBy"`
	Result        string `json:"result"`    // the linked checkup's result, "" when none is linked
	HasRecord     bool   `json:"hasRecord"` // a checkup is linked (Close needs one)
}

// staffDetail is what the checkup form pre-fills from: the row plus the identity the submitter
// typed. It is only sent to a signed-in staff member, and the NIK is blank after retention.
type staffDetail struct {
	staffRow
	Nationality string `json:"nationality"`
	Gender      string `json:"gender"`     // Male or Female, "" when unknown
	PersonalID  string `json:"personalId"` // the NIK, or the passport number when there is no NIK
	PatientID   int64  `json:"patientId"`  // 0 = not on file yet
	CompanyID   int64  `json:"companyId"`  // 0 = not picked
	RecordID    int64  `json:"recordId"`   // the linked checkup record, 0 = none
}

func rowOf(d Submission) staffRow {
	return staffRow{
		ID: d.ID, Worker: d.PatName, DateOfBirth: d.PatDateOfBirth, Position: d.PatPosition, Company: d.PatCompanyText,
		Nationality: d.PatNationality, Submitter: d.SubmitterName, Status: d.Status, RejectReason: d.RejectReason,
		SentAt: d.SentAt, HasFile: d.HasFile, IsNewEmployee: d.IsNewEmployee, ReviewedAt: d.ReviewedAt, ReviewedBy: d.ReviewedBy,
		Result: d.Result, HasRecord: d.RecordID != 0,
	}
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
		rows = append(rows, rowOf(d))
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

// Get returns one sent document for the checkup form to pre-fill from. Drafts and documents still
// under Review are not staff's to see, so they are 404 here exactly as they are missing from List.
func (a *StaffAPI) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := staffID(w, r); !ok {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	d, err := a.Store.StaffDoc(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("mcusubmit: staff get: %v", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	out := staffDetail{staffRow: rowOf(d), Nationality: d.PatNationality, Gender: d.PatGender, PersonalID: d.PatNik, PatientID: d.PatientID, CompanyID: d.CompanyID, RecordID: d.RecordID}
	if out.PersonalID == "" {
		out.PersonalID = d.PatPassportNo
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// Records returns the checkups that could be linked to this document, for the Submitted tab's
// "Link a checkup" list. It reads the same facts LinkRecord checks.
func (a *StaffAPI) Records(w http.ResponseWriter, r *http.Request) {
	if _, ok := staffID(w, r); !ok {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	list, err := a.Store.CandidateRecords(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, ErrBadState):
		http.Error(w, "This document is not in a status that allows that.", http.StatusConflict)
		return
	case err != nil:
		log.Printf("mcusubmit: candidate records: %v", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(list)
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

// Link links the checkup record staff made to the document; the JSON body is {"checkUpId": "..."}.
func (a *StaffAPI) Link(w http.ResponseWriter, r *http.Request) {
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
		return a.Store.LinkRecord(ctx, id, staff, body.CheckUpID)
	})
}

// Unlink undoes a wrong link on a document that is not closed yet.
func (a *StaffAPI) Unlink(w http.ResponseWriter, r *http.Request) {
	a.act(w, r, func(ctx context.Context, id, staff int64) error { return a.Store.UnlinkRecord(ctx, id, staff) })
}

// Close closes a Reviewed document. The page calls it "Notify submitter": the reviewer closes the
// document when they tell the submitter the checkup is done, and the daily mail picks it up.
func (a *StaffAPI) Close(w http.ResponseWriter, r *http.Request) {
	a.act(w, r, func(ctx context.Context, id, staff int64) error { return a.Store.CloseReviewed(ctx, id, staff) })
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
