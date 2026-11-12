package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// LatestRecord is the newest checkup on record for the worker of a document. The Submitted tab
// shows it in the More panel, because HR does not always send an update through the portal: the
// reviewer can still see where the worker stands without leaving the page.
type LatestRecord struct {
	RecordID    int64  `json:"recordId"` // McuRecordID: what the certificate download is asked for
	CheckUpID   string `json:"checkUpId"`
	DateCheckUp string `json:"dateCheckUp"`
	Result      string `json:"result"`
	ValidUntil  string `json:"validUntil"`
	Status      string `json:"status"`
	Certificate bool   `json:"certificate"` // the MCU tab offers a certificate for it (see HasCertificate)
}

// nameKey makes two spellings of one name comparable: case and spacing are ignored, nothing else.
func nameKey(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), " "))
}

// LatestRecord finds the worker of document id and returns their newest checkup, or nil when there
// is none. A picked patient is matched by id. Anyone else (a new employee, or a name typed by hand)
// is matched by name AND date of birth together (owner, 2026-11-12): the name alone would mix up
// two people who share a common name. It reads only, so it works for a document in any status.
func (s *Store) LatestRecord(ctx context.Context, id int64) (*LatestRecord, error) {
	var name, dob string
	var patient sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT PatName, PatDateOfBirth, FK_PatientID FROM McuSubmission WHERE SubmissionID = ?`, id).
		Scan(&name, &dob, &patient)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	if patient.Valid {
		ids = append(ids, patient.Int64)
	} else if len(dob) >= 10 && strings.TrimSpace(name) != "" {
		// The birth date narrows it in SQL; the name is compared in Go so the same rule works on
		// SQLite and MariaDB (collapsing inner spaces has no portable SQL).
		rows, err := s.DB.QueryContext(ctx,
			`SELECT PatientID, Name FROM Patient WHERE SUBSTR(DateOfBirth, 1, 10) = ?`, dob[:10])
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var pid int64
			var pname string
			if err := rows.Scan(&pid, &pname); err != nil {
				return nil, err
			}
			if nameKey(pname) == nameKey(name) {
				ids = append(ids, pid)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	var r LatestRecord
	err = s.DB.QueryRowContext(ctx,
		`SELECT McuRecordID, CheckUpID, COALESCE(DateCheckUp, ''), COALESCE(ResultCriteria, ''), COALESCE(ResultValidUntil, ''), COALESCE(CheckUpStatus, '')
		 FROM McuRecord WHERE FK_PatientID IN (`+marks+`)
		 ORDER BY COALESCE(DateCheckUp, '') DESC, McuRecordID DESC LIMIT 1`, args...).
		Scan(&r.RecordID, &r.CheckUpID, &r.DateCheckUp, &r.Result, &r.ValidUntil, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Certificate = r.HasCertificate()
	return &r, nil
}

// HasCertificate says whether the MCU tab offers a certificate for this checkup. It is the same rule the
// certificate generator applies (internal/entrymcu CertificateFile): a Temporarily Unfit or Unfit result
// always has one (the written opinion the company keeps), Fit and Fit with note only once the record is
// Close. The generator still has the last word, for example it refuses an overdue or archived record.
func (l LatestRecord) HasCertificate() bool {
	switch l.Result {
	case "TemporaryUnfit", "Unfit":
		return true
	case "Fit", "FitWithNote":
		return l.Status == "Close"
	}
	return false
}

// certificateStatuses are the document statuses that may show a worker's existing certificate: the
// document has been sent, and was not rejected or withdrawn.
var certificateStatuses = map[string]bool{SubPending: true, SubReviewed: true, SubClosed: true}

// latestCertificateRecord returns the worker's newest checkup when it has a certificate and the document
// (in this status) may show it, else 0. This is how a certificate that already exists on the MCU tab
// reaches the submitter although the document was never linked to that checkup (owner, 2026-11-12).
func (s *Store) latestCertificateRecord(ctx context.Context, id int64, status string) (int64, error) {
	if !certificateStatuses[status] {
		return 0, nil
	}
	l, err := s.LatestRecord(ctx, id)
	if err != nil || l == nil || !l.HasCertificate() {
		return 0, err
	}
	return l.RecordID, nil
}
