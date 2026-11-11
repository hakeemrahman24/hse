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
	CheckUpID   string `json:"checkUpId"`
	DateCheckUp string `json:"dateCheckUp"`
	Result      string `json:"result"`
	ValidUntil  string `json:"validUntil"`
	Status      string `json:"status"`
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
		`SELECT CheckUpID, COALESCE(DateCheckUp, ''), COALESCE(ResultCriteria, ''), COALESCE(ResultValidUntil, ''), COALESCE(CheckUpStatus, '')
		 FROM McuRecord WHERE FK_PatientID IN (`+marks+`)
		 ORDER BY COALESCE(DateCheckUp, '') DESC, McuRecordID DESC LIMIT 1`, args...).
		Scan(&r.CheckUpID, &r.DateCheckUp, &r.Result, &r.ValidUntil, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
