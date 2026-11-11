package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
)

// The health certificate belongs to the checkup record the reviewer saved, not to the submitted
// PDF, and it is built by the staff side (entrymcu). The portal only asks for it through this
// small interface, so this package does not import the clinical code and a test can fake it.

// CertificateFile is a ready certificate PDF on disk and the name to offer it under.
type CertificateFile struct{ Path, Filename string }

// CertificateUnavailable means the rules refuse a certificate for this record (for example it is
// Incomplete, or past its validity). Reason is written for the person asking.
type CertificateUnavailable struct{ Reason string }

func (e *CertificateUnavailable) Error() string { return e.Reason }

// CertificateSource builds or finds the certificate of one checkup record. The rules for which
// results get one are the staff side's: a submitter gets exactly what staff can download.
type CertificateSource interface {
	Certificate(ctx context.Context, mcuRecordID int64) (CertificateFile, error)
}

// SetCertificates turns on the certificate download for closed documents. Without it the route
// answers 404, so a build without the clinical side simply has no such button.
func (a *API) SetCertificates(src CertificateSource) { a.certs = src }

// OwnCertificateRecord returns the checkup record a document in the submitter's scope (their own,
// their company's, a subcontractor's) was linked to, when the certificate may be offered: the
// document is Closed, or Reviewed with an eligible result (see Submission.CertificateReady). A document
// outside the scope, one with no record, or one that is not eligible is ErrNotFound, so the route
// cannot be used to probe other people's documents or other results.
func (s *Store) OwnCertificateRecord(ctx context.Context, sc Scope, id int64) (int64, error) {
	var record int64
	clause, args := sc.clause()
	err := s.DB.QueryRowContext(ctx,
		`SELECT s.FK_McuRecordID FROM McuSubmission s
		 LEFT JOIN McuRecord rec ON rec.McuRecordID = s.FK_McuRecordID
		 WHERE s.SubmissionID = ? AND `+clause+` AND s.FK_McuRecordID IS NOT NULL
		   AND (s.Status = ? OR (s.Status = ? AND rec.ResultCriteria IN ('Fit', 'FitWithNote')))`,
		append(append([]any{id}, args...), SubClosed, SubReviewed)...).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return record, err
}

// DocumentCertificate lets a submitter download the health certificate of their own document once it is eligible.
func (a *API) DocumentCertificate(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok || a.certs == nil {
		a.notFound(w, u)
		return
	}
	sc, err := a.Store.ScopeFor(r.Context(), u)
	if err != nil {
		a.serverError(w, "scope", err)
		return
	}
	record, err := a.Store.OwnCertificateRecord(r.Context(), sc, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: own certificate record: %v", err)
		}
		a.notFound(w, u)
		return
	}
	cert, err := a.certs.Certificate(r.Context(), record)
	var unavailable *CertificateUnavailable
	if errors.As(err, &unavailable) {
		a.message(w, http.StatusConflict, "No certificate available", "A certificate cannot be issued for this checkup: "+unavailable.Reason+". Please contact the medical team.", u)
		return
	}
	if err != nil {
		a.serverError(w, "certificate", err)
		return
	}
	f, err := os.Open(cert.Path)
	if err != nil {
		a.serverError(w, "open certificate", err)
		return
	}
	defer f.Close()
	if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorSubmitter, ActorID: u.ID, EntityType: EntitySubmission, EntityID: id, Action: "CertificateDownloaded"}); err != nil {
		log.Printf("mcusubmit: log certificate download: %v", err)
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": cert.Filename}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}
