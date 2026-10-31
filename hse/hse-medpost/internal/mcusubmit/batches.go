package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Submission statuses (plan section 7: one status list, not two pairs).
const (
	SubReview    = "Review"    // uploaded, waiting for the submitter to confirm it: staff cannot see it
	SubDraft     = "Draft"     // confirmed, in the open batch, batch not sent: staff cannot see it
	SubPending   = "Pending"   // batch sent, waiting for staff
	SubReviewed  = "Reviewed"  // staff checked the file
	SubClosed    = "Closed"    // a checkup record was created from it
	SubRejected  = "Rejected"  // refused by staff, with a reason
	SubWithdrawn = "Withdrawn" // the submitter took it back while it was still Pending

	// Infected files never get a status: the row is deleted at once (plan section 5).
	ScanScanning = "Scanning"
	ScanClean    = "Clean"
	ScanError    = "ScanError" // scanner unreachable; never treated as clean

	// Per-submitter upload cap in any 24 hours. Each upload runs a malware scan and Ghostscript on
	// a Raspberry Pi, so this protects the Pi from a signed-in account being used to hammer it.
	maxUploadsPerDay = 50
	uploadWindow     = 24 * time.Hour
	// Files are kept 2 years from upload, Drafts included (owner, 2026-10-28).
	retentionYears = 2
)

// ErrDailyLimit means the submitter has already uploaded the maximum in the last 24 hours.
var ErrDailyLimit = errors.New("mcusubmit: daily upload limit reached")

// The reasons SendBatch can refuse. Each wraps ErrBadState, so a caller that only cares that the
// state was wrong still matches it, while the page can say which of them it was.
var (
	ErrBatchSent       = fmt.Errorf("%w: the batch was already sent", ErrBadState)
	ErrBatchEmpty      = fmt.Errorf("%w: the batch has no documents", ErrBadState)
	ErrBatchUnfinished = fmt.Errorf("%w: a document is still being checked or not yet confirmed", ErrBadState)
)

// NewSubmission is what the upload pipeline hands over after the file is stored. Pat* fields are a
// snapshot of what the submitter typed; Patient itself is never written by a submitter.
type NewSubmission struct {
	PatName, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality string
	PatNik, PatPassportNo                                                string
	PatGender                                                            string // Male or Female, "" = unknown
	CompanyID, PatientID                                                 int64  // 0 = not set
	IsNewEmployee                                                        bool
	// NeedsReview stores the document as Review: the submitter must confirm the details and the
	// file once before it can join the batch that is sent (owner, 2026-10-29).
	NeedsReview          bool
	FilePath, FileSha256 string
	FileBytes            int
}

// Submission is one row as the portal and the staff list show it. FilePath is deliberately not
// here: only OwnFile and StaffFile hand a path out, and only after their checks.
type Submission struct {
	ID, BatchID, SubmitterID                             int64
	SubmitterName                                        string // filled for the staff list
	PatName, PatDateOfBirth, PatPosition, PatCompanyText string
	PatNationality                                       string
	IsNewEmployee, HasFile                               bool
	ScanStatus, Status, RejectReason                     string
	SubmittedAt, SentAt, WithdrawnAt, FileExpiresAt      string
	// Staff-side facts. ReviewedBy is the reviewer's name, never shown to a submitter. PatientID and
	// CompanyID are 0 when not set; PatNik and PatPassportNo are what the submitter typed and are
	// blank after retention.
	ReviewedAt, ReviewedBy string
	// Result is the linked checkup's result (Fit, Unfit, ...), "" until a checkup is linked.
	Result                string
	ResultNotes           string // the linked checkup's notes
	Recommendation        string // the linked checkup's recommendation
	PatNik, PatPassportNo string
	PatGender             string
	RecordID              int64 // the checkup record a Closed document was linked to, 0 = none
	PatientID, CompanyID  int64
}

const submissionColumns = `s.SubmissionID, s.FK_BatchID, s.FK_SubmitterID, COALESCE(u.Name, ''),
	s.PatName, s.PatDateOfBirth, s.PatPosition, s.PatCompanyText, s.PatNationality,
	s.IsNewEmployee, CASE WHEN s.FilePath IS NULL THEN 0 ELSE 1 END, s.ScanStatus, s.Status, COALESCE(s.RejectReason, ''),
	s.SubmittedAt, COALESCE(b.SentAt, ''), COALESCE(s.WithdrawnAt, ''), COALESCE(s.FileExpiresAt, ''),
	COALESCE(s.ReviewedAt, ''), COALESCE(rs.StaffName, ''), COALESCE(s.PatNik, ''), COALESCE(s.PatPassportNo, ''), COALESCE(s.PatGender, ''),
	COALESCE(s.FK_PatientID, 0), COALESCE(s.FK_CompanyID, 0), COALESCE(s.FK_McuRecordID, 0), COALESCE(rec.ResultCriteria, ''), COALESCE(rec.ResultNotes, ''), COALESCE(rec.Recommendation, '')`

const submissionFrom = ` FROM McuSubmission s
	JOIN McuBatch b ON b.BatchID = s.FK_BatchID
	JOIN Submitter u ON u.SubmitterID = s.FK_SubmitterID
	LEFT JOIN MedpostStaffTable rs ON rs.StaffID = s.FK_ReviewerStaffID
	LEFT JOIN McuRecord rec ON rec.McuRecordID = s.FK_McuRecordID`

// ResultMessage is what the submitter may read about a result that is not a plain Fit, shown only
// once the reviewer closed the document (that is the moment the submitter is told). Owner,
// 2026-10-31: an incomplete result shows the Recommendation, and a temporarily unfit or unfit
// worker shows the notes, exactly as the reviewer wrote them. Both are free text written for the
// record, so reviewers must write them knowing the submitter reads them.
func (d Submission) ResultMessage() string {
	if d.Status != SubClosed {
		return ""
	}
	switch d.Result {
	case "NeedFollowUp", "IncompleteResult":
		return d.Recommendation
	case "TemporaryUnfit", "Unfit":
		return d.ResultNotes
	}
	return ""
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }

func (s *Store) querySubmissions(ctx context.Context, where string, args ...any) ([]Submission, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+submissionColumns+submissionFrom+` `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Submission
	for rows.Next() {
		var d Submission
		var isNew, hasFile int
		if err := rows.Scan(&d.ID, &d.BatchID, &d.SubmitterID, &d.SubmitterName, &d.PatName, &d.PatDateOfBirth,
			&d.PatPosition, &d.PatCompanyText, &d.PatNationality, &isNew, &hasFile, &d.ScanStatus, &d.Status,
			&d.RejectReason, &d.SubmittedAt, &d.SentAt, &d.WithdrawnAt, &d.FileExpiresAt,
			&d.ReviewedAt, &d.ReviewedBy, &d.PatNik, &d.PatPassportNo, &d.PatGender, &d.PatientID, &d.CompanyID, &d.RecordID, &d.Result, &d.ResultNotes, &d.Recommendation); err != nil {
			return nil, err
		}
		d.IsNewEmployee, d.HasFile = isNew == 1, hasFile == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// requireActive refuses everything but a signed-in-able account: a Disabled or not yet approved
// submitter must not create batches or send them.
func (s *Store) requireActive(ctx context.Context, submitterID int64) error {
	u, err := s.ByID(ctx, submitterID)
	if err != nil {
		return err
	}
	if u.Status != StatusActive || u.IsBuiltIn {
		return ErrBadState
	}
	return nil
}

// OpenBatch returns the submitter's unsent batch, creating one when there is none. A sent batch is
// locked, so the first upload after Send starts a new batch.
func (s *Store) OpenBatch(ctx context.Context, submitterID int64) (int64, error) {
	if err := s.requireActive(ctx, submitterID); err != nil {
		return 0, err
	}
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT BatchID FROM McuBatch WHERE FK_SubmitterID = ? AND SentAt IS NULL ORDER BY BatchID DESC LIMIT 1`,
		submitterID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO McuBatch (FK_SubmitterID, CreatedAt) VALUES (?, ?)`, submitterID, s.stamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CheckQuota returns ErrDailyLimit when the submitter has used the day's uploads. The upload page
// calls it before running Ghostscript (the expensive part) and AddSubmission checks again, so two
// uploads racing each other still cannot go over.
func (s *Store) CheckQuota(ctx context.Context, submitterID int64) error {
	var recent int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM McuSubmissionEvent WHERE ActorType = ? AND ActorID = ? AND Action = 'Uploaded' AND At > ?`,
		ActorSubmitter, submitterID, stampOf(s.now().Add(-uploadWindow))).Scan(&recent); err != nil {
		return err
	}
	if recent >= maxUploadsPerDay {
		return ErrDailyLimit
	}
	return nil
}

// AddSubmission stores one document as a Scanning Draft in the open batch and writes an Uploaded
// event. The limit counts those events, not rows, so removing a draft does not hand the quota back.
func (s *Store) AddSubmission(ctx context.Context, submitterID int64, in NewSubmission) (int64, error) {
	if in.PatName == "" || in.PatDateOfBirth == "" || in.FilePath == "" {
		return 0, errors.New("mcusubmit: name, date of birth and file are required")
	}
	batchID, err := s.OpenBatch(ctx, submitterID)
	if err != nil {
		return 0, err
	}
	if err := s.CheckQuota(ctx, submitterID); err != nil {
		return 0, err
	}
	isNew := 0
	if in.IsNewEmployee {
		isNew = 1
	}
	now := s.now()
	startStatus := SubDraft
	if in.NeedsReview {
		startStatus = SubReview
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO McuSubmission (FK_SubmitterID, FK_BatchID, FK_PatientID, PatName, PatDateOfBirth, PatPosition,
		   PatCompanyText, PatNationality, PatNik, PatPassportNo, PatGender, FK_CompanyID, IsNewEmployee, FilePath, FileSha256,
		   FileBytes, FileExpiresAt, ScanStatus, Status, SubmittedAt)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		submitterID, batchID, nullInt(in.PatientID), in.PatName, in.PatDateOfBirth, in.PatPosition,
		in.PatCompanyText, in.PatNationality, in.PatNik, in.PatPassportNo, in.PatGender, nullInt(in.CompanyID), isNew, in.FilePath,
		in.FileSha256, in.FileBytes, stampOf(now.AddDate(retentionYears, 0, 0)), ScanScanning, startStatus, stampOf(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: submitterID, EntityType: EntitySubmission, EntityID: id, Action: "Uploaded"})
}

// SetScanResult records the scanner's verdict. Infected is not a verdict here: RemoveInfected
// deletes the row instead.
func (s *Store) SetScanResult(ctx context.Context, submissionID int64, status string) error {
	if status != ScanClean && status != ScanError && status != ScanScanning {
		return errors.New("mcusubmit: unknown scan status")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE McuSubmission SET ScanStatus = ? WHERE SubmissionID = ?`, status, submissionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveInfected deletes an infected upload outright (owner, 2026-10-28). The only trace is a
// System event with the signature name; it carries nothing about the worker.
func (s *Store) RemoveInfected(ctx context.Context, submitterID, submissionID int64, signature string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM McuSubmission WHERE SubmissionID = ? AND FK_SubmitterID = ?`, submissionID, submitterID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmission, EntityID: submissionID, Action: "Infected", Detail: signature})
}

// ownedStatus returns the stored file path and status of a submission owned by submitterID. Another
// submitter's id gets ErrNotFound, the same as a missing one, so ids cannot be probed.
func (s *Store) ownedStatus(ctx context.Context, submitterID, id int64) (path, status string, err error) {
	var p sql.NullString
	err = s.DB.QueryRowContext(ctx,
		`SELECT FilePath, Status FROM McuSubmission WHERE SubmissionID = ? AND FK_SubmitterID = ?`, id, submitterID).Scan(&p, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return p.String, status, err
}

// ConfirmDraft is the submitter's review step: after looking at the worker details and the file,
// the document moves from Review to Draft and so joins the batch. Only then can Send include it.
func (s *Store) ConfirmDraft(ctx context.Context, submitterID, id int64) error {
	_, status, err := s.ownedStatus(ctx, submitterID, id)
	if err != nil {
		return err
	}
	if status != SubReview {
		return ErrBadState
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ? AND FK_SubmitterID = ? AND Status = ?`,
		SubDraft, id, submitterID, SubReview)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrBadState
	}
	return s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: submitterID, EntityType: EntitySubmission, EntityID: id, Action: "Confirmed"})
}

// RemoveDraft deletes one of the submitter's own Draft or Review documents and returns the file path for the
// caller to delete from storage. A document that was already sent must be withdrawn instead.
func (s *Store) RemoveDraft(ctx context.Context, submitterID, id int64) (string, error) {
	path, status, err := s.ownedStatus(ctx, submitterID, id)
	if err != nil {
		return "", err
	}
	if status != SubDraft && status != SubReview {
		return "", ErrBadState
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM McuSubmission WHERE SubmissionID = ? AND FK_SubmitterID = ? AND Status = ?`, id, submitterID, status)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrBadState
	}
	return path, s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: submitterID, EntityType: EntitySubmission, EntityID: id, Action: "Removed"})
}

// SendBatch is the only moment documents become visible to staff: one transaction sets SentAt and
// moves every document to Pending. It refuses an empty batch and any document still Scanning or
// ScanError, so a locked batch never holds a stuck draft (plan section 8). Returns how many
// documents were sent.
func (s *Store) SendBatch(ctx context.Context, submitterID, batchID int64) (int, error) {
	if err := s.requireActive(ctx, submitterID); err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var sentAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT SentAt FROM McuBatch WHERE BatchID = ? AND FK_SubmitterID = ?`, batchID, submitterID).Scan(&sentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if sentAt.Valid {
		return 0, ErrBatchSent
	}
	var total, unfinished int
	// A document still in Review has not been confirmed by its submitter, so it blocks the send
	// like an unfinished scan does: the batch holds only what the submitter has looked at.
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN ScanStatus <> ? OR Status = ? THEN 1 ELSE 0 END), 0) FROM McuSubmission WHERE FK_BatchID = ?`,
		ScanClean, SubReview, batchID).Scan(&total, &unfinished); err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, ErrBatchEmpty
	}
	if unfinished > 0 {
		return 0, ErrBatchUnfinished
	}
	res, err := tx.ExecContext(ctx, `UPDATE McuBatch SET SentAt = ? WHERE BatchID = ? AND SentAt IS NULL`, s.stamp(), batchID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrBatchSent // another request sent it between the read and the write
	}
	if _, err := tx.ExecContext(ctx, `UPDATE McuSubmission SET Status = ? WHERE FK_BatchID = ? AND Status = ?`, SubPending, batchID, SubDraft); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: submitterID, EntityType: EntityBatch, EntityID: batchID, Action: "Sent", Detail: strconv.Itoa(total) + " documents"})
}

// Withdraw lets a submitter take back a document while it is still Pending. The row stays as an
// audit stub with FilePath cleared; the returned path is the file the caller must delete now.
func (s *Store) Withdraw(ctx context.Context, submitterID, id int64) (string, error) {
	path, status, err := s.ownedStatus(ctx, submitterID, id)
	if err != nil {
		return "", err
	}
	if status != SubPending {
		return "", ErrBadState
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET Status = ?, WithdrawnAt = ?, FilePath = NULL WHERE SubmissionID = ? AND FK_SubmitterID = ? AND Status = ?`,
		SubWithdrawn, s.stamp(), id, submitterID, SubPending)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrBadState // staff reviewed it between the read and the write
	}
	return path, s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: submitterID, EntityType: EntitySubmission, EntityID: id, Action: "Withdrawn"})
}

// ListOwn returns every document of one submitter, newest first. It takes the submitter id from the
// session, never from the request, so a submitter can only ever see their own rows.
func (s *Store) ListOwn(ctx context.Context, submitterID int64) ([]Submission, error) {
	return s.querySubmissions(ctx, `WHERE s.FK_SubmitterID = ? ORDER BY s.SubmissionID DESC`, submitterID)
}

// StaffQueue is the review list. Drafts are never listed (staff must not see unsent work), and
// Withdrawn rows are hidden unless that status is asked for.
func (s *Store) StaffQueue(ctx context.Context, status string) ([]Submission, error) {
	switch status {
	case "":
		return s.querySubmissions(ctx, `WHERE s.Status NOT IN (?, ?, ?) ORDER BY b.SentAt DESC, s.SubmissionID DESC`, SubReview, SubDraft, SubWithdrawn)
	case SubDraft, SubReview:
		return nil, nil
	default:
		return s.querySubmissions(ctx, `WHERE s.Status = ? ORDER BY b.SentAt DESC, s.SubmissionID DESC`, status)
	}
}

// fileFor returns a stored path only when every rule for opening it holds; any miss is ErrNotFound.
func (s *Store) fileFor(ctx context.Context, where string, args ...any) (string, error) {
	var p sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT s.FilePath FROM McuSubmission s WHERE `+where+` AND s.FilePath IS NOT NULL AND s.ScanStatus = '`+ScanClean+`'`, args...).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return p.String, err
}

// OwnFile is the path a submitter may open: their own, scanned clean, not withdrawn. Drafts are
// allowed so they can check what they uploaded before sending.
func (s *Store) OwnFile(ctx context.Context, submitterID, id int64) (string, error) {
	return s.fileFor(ctx, `s.SubmissionID = ? AND s.FK_SubmitterID = ? AND s.Status <> ?`, id, submitterID, SubWithdrawn)
}

// StaffFile is the path a staff member may open: scanned clean and sent (never a Draft or a
// Withdrawn document).
func (s *Store) StaffFile(ctx context.Context, id int64) (string, error) {
	return s.fileFor(ctx, `s.SubmissionID = ? AND s.Status NOT IN (?, ?, ?)`, id, SubReview, SubDraft, SubWithdrawn)
}
