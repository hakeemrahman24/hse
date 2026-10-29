package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const (
	// maxWorkerResults and minWorkerQuery keep the picker from being a way to download the patient
	// table: a short query matches too much, so it returns nothing, and a long result list is cut.
	maxWorkerResults = 10
	minWorkerQuery   = 3
)

// Worker is an existing patient as the picker shows it. A submitter chooses one instead of typing
// the details, which is what stops misspelled names and mistyped dates of birth.
type Worker struct {
	PatientID   int64
	Name        string
	DateOfBirth string
	Position    string
	Company     string
	Nationality string
	CompanyID   int64 // 0 when the patient has no company
}

const workerSelect = `SELECT p.PatientID, p.Name, COALESCE(p.DateOfBirth, ''), COALESCE(p.WorkPosition, ''),
	COALESCE(p.FK_CompanyID, 0), COALESCE(c.Name, ''), COALESCE(n.Name, '')
	FROM Patient p
	LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
	LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID`

func scanWorker(row interface{ Scan(...any) error }) (Worker, error) {
	var w Worker
	err := row.Scan(&w.PatientID, &w.Name, &w.DateOfBirth, &w.Position, &w.CompanyID, &w.Company, &w.Nationality)
	return w, err
}

// searchWords reduces a typed query to words made of letters, digits, dot, hyphen and apostrophe.
// The characters that mean something to LIKE (% and _) are dropped rather than escaped: an ESCAPE
// clause with a backslash behaves differently on SQLite and MariaDB (see CLAUDE.md), and a name
// never contains them.
func searchWords(q string) []string {
	words := splitWords(q)
	letters := 0
	for _, w := range words {
		letters += len([]rune(w))
	}
	if letters < minWorkerQuery {
		return nil
	}
	return words
}

// splitWords is the cleaning step alone, with no minimum length. The document filters use it
// directly: a filter may be one letter, because it narrows a list the person already has.
func splitWords(q string) []string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '\'' {
			return r
		}
		return ' '
	}, q)
	return strings.Fields(clean)
}

// SearchWorkers finds patients whose name contains every word of the query, for the picker.
func (s *Store) SearchWorkers(ctx context.Context, q string) ([]Worker, error) {
	words := searchWords(q)
	if len(words) == 0 {
		return nil, nil
	}
	query := workerSelect + ` WHERE 1 = 1`
	args := make([]any, 0, len(words))
	for _, w := range words {
		query += ` AND p.Name LIKE ?`
		args = append(args, "%"+w+"%")
	}
	// The limit is a constant written into the text, not a user value, so it is safe to concatenate.
	query += ` ORDER BY p.Name LIMIT ` + strconv.Itoa(maxWorkerResults)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkerByID returns one patient, or ErrNotFound. The upload takes the details from here and not
// from the form, so a changed form field cannot alter what is recorded about the worker.
func (s *Store) WorkerByID(ctx context.Context, id int64) (Worker, error) {
	w, err := scanWorker(s.DB.QueryRowContext(ctx, workerSelect+` WHERE p.PatientID = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Worker{}, ErrNotFound
	}
	return w, err
}

// WorkerExists reports whether a patient with this name and date of birth is already on file. The
// "new employee" form refuses such a worker, so the same person is not added twice with a slightly
// different spelling of the same data.
func (s *Store) WorkerExists(ctx context.Context, name, dob string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM Patient WHERE LOWER(TRIM(Name)) = ? AND DateOfBirth = ?`,
		strings.ToLower(strings.TrimSpace(name)), dob).Scan(&n)
	return n > 0, err
}
