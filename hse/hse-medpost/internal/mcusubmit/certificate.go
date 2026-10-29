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

// OwnClosedRecord returns the checkup record a submitter's own Closed document was linked to. A
// document of someone else, one that is not Closed, or one with no record is ErrNotFound, so the
// route cannot be used to probe other people's documents.
func (s *Store) OwnClosedRecord(ctx context.Context, submitterID, id int64) (int64, error) {
	var record int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT FK_McuRecordID FROM McuSubmission
		 WHERE SubmissionID = ? AND FK_SubmitterID = ? AND Status = ? AND FK_McuRecordID IS NOT NULL`,
		id, submitterID, SubClosed).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return record, err
}

// DocumentCertificate lets a submitter download the health certificate of their own Closed document.
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
	record, err := a.Store.OwnClosedRecord(r.Context(), u.ID, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: own closed record: %v", err)
		}
		a.notFound(w, u)
		return
	}
	cert, err := a.certs.Certificate(r.Context(), record)
	var unavailable *CertificateUnavailable
	if errors.As(err, &unavailable) {
		a.message(w, http.StatusConflict, "No certificate available", "A certificate cannot be issued for this checkup: "+unavailable.Reason+". Please contact the medical post.", u)
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
