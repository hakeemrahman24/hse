// Package waiverlog stores and reads visiting waivers.
//
// WHY separate from package waiver: waiver holds pure rules that never touch a
// database. This is the small database side, the same split as permit and
// permitlog.
package waiverlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
)

// Store reads and writes the waiver table.
type Store struct{ DB *sql.DB }

// NewWaiver is one submitted waiver, saved as PendingDoctor.
type NewWaiver struct {
	RequestID, Status, ApplicantType             string
	CompanyID                                    int64
	Company                                      string
	Name, Gender, DOB, Nationality, NIK          string
	Position, Department, VisitLocation, Purpose string
	VisitFrom, VisitTo                           string
	Days                                         int
	RequesterName, RequesterEmail, Phone         string
	Risk, Precaution, Prevent                    string
	TakesMedicine                                bool
	Medicine1, Dosage1                           string
	Medicine2, Dosage2, Medicine3, Dosage3       string
	MedicineNotes                                string
	RiskAnswers, HealthAnswers                   string
	PassportUntil, VisaUntil                     string
	IDDoc, IDPhoto, LetterDoc, VisaDoc           string
	DeclaredAt, CreatedAt                        string
}

// Exists reports whether a request id is taken (the id generator retries on true).
func (s Store) Exists(ctx context.Context, id string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM waiver WHERE request_id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check waiver id: %w", err)
	}
	return n > 0, nil
}

// Create stores a waiver. The primary key is the last guard against two
// requests drawing the same id at once.
//
// WHY the insert is built from a column list: a long INSERT with a hand-counted
// run of question marks breaks silently when a column is added.
func (s Store) Create(ctx context.Context, w NewWaiver) error {
	return insertWaiver(ctx, s.DB, w)
}

// CreateWithTicket stores a waiver and its ticket together, or neither.
//
// WHY one transaction: permits doc 8 makes the ticket at submit, so a waiver
// without its ticket (or the reverse) would be a half-saved request that the
// review portals could not handle.
func (s Store) CreateWithTicket(ctx context.Context, w NewWaiver, t ticketlog.NewTicket) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin waiver: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if err := insertWaiver(ctx, tx, w); err != nil {
		return err
	}
	if err := ticketlog.Insert(ctx, tx, t); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit waiver: %w", err)
	}
	return nil
}

// Insert stores a waiver through db or an open transaction, so the importer can write inside
// a transaction it can roll back.
func Insert(ctx context.Context, db ticketlog.Execer, w NewWaiver) error {
	return insertWaiver(ctx, db, w)
}

func insertWaiver(ctx context.Context, db ticketlog.Execer, w NewWaiver) error {
	med := 0
	if w.TakesMedicine {
		med = 1
	}
	cols := []struct {
		name string
		val  any
	}{
		{"request_id", w.RequestID}, {"status", w.Status}, {"applicant_type", w.ApplicantType},
		{"company_id", w.CompanyID}, {"company", w.Company}, {"visitor_name", w.Name}, {"gender", w.Gender},
		{"dob", w.DOB}, {"nationality", w.Nationality}, {"nik", w.NIK}, {"position", w.Position},
		{"department", w.Department}, {"visit_location", w.VisitLocation}, {"purpose", w.Purpose},
		{"visit_from", w.VisitFrom}, {"visit_to", w.VisitTo}, {"days", w.Days},
		{"requester_name", w.RequesterName}, {"requester_email", w.RequesterEmail}, {"phone", w.Phone},
		{"risk", w.Risk}, {"precaution", w.Precaution}, {"prevent", w.Prevent},
		{"takes_medicine", med}, {"medicine1", w.Medicine1}, {"dosage1", w.Dosage1},
		{"medicine2", w.Medicine2}, {"dosage2", w.Dosage2}, {"medicine3", w.Medicine3}, {"dosage3", w.Dosage3},
		{"medicine_notes", w.MedicineNotes}, {"risk_answers", w.RiskAnswers}, {"health_answers", w.HealthAnswers},
		{"passport_until", w.PassportUntil}, {"visa_until", w.VisaUntil},
		{"id_doc", w.IDDoc}, {"id_photo", w.IDPhoto}, {"letter_doc", w.LetterDoc}, {"visa_doc", w.VisaDoc},
		{"declared_at", w.DeclaredAt}, {"created_at", w.CreatedAt},
	}
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		names[i], marks[i], args[i] = c.name, "?", c.val
	}
	q := "INSERT INTO waiver (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("insert waiver: %w", err)
	}
	return nil
}

// AccessInfo is what the ticket page needs about a waiver: the birth date to
// match, whether it is Approved, and the visit it covers.
type AccessInfo struct {
	RequestID, Status, DOB, Name string
	From, To                     dates.Day
}

// Access finds a waiver by its WVR number (any case, spaces ignored).
func (s Store) Access(ctx context.Context, id string) (AccessInfo, bool, error) {
	var a AccessInfo
	var from, to string
	err := s.DB.QueryRowContext(ctx, `
SELECT request_id, status, dob, visitor_name, visit_from, visit_to FROM waiver WHERE request_id = ?`,
		strings.ToUpper(strings.TrimSpace(id))).Scan(&a.RequestID, &a.Status, &a.DOB, &a.Name, &from, &to)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessInfo{}, false, nil
	}
	if err != nil {
		return AccessInfo{}, false, fmt.Errorf("read waiver by number: %w", err)
	}
	a.From, a.To = dates.Day(from), dates.Day(to)
	return a, true, nil
}

// Open returns the waivers whose visit has not ended (to-date today or later).
// waiver.Blocking decides from these which ones really block a new request.
//
// WHY no status filter here: the blocking rule owns what "terminal" means
// (permits doc 8.2); filtering twice would let the two drift apart. Waivers are
// few (about 19 in the old sheet), so reading them all is cheap.
func (s Store) Open(ctx context.Context, today dates.Day) ([]waiver.Waiver, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT request_id, status, nik, visitor_name, dob, company, applicant_type, visit_from, visit_to
FROM waiver WHERE visit_to >= ? ORDER BY created_at, request_id`, string(today))
	if err != nil {
		return nil, fmt.Errorf("read open waivers: %w", err)
	}
	defer rows.Close()
	var out []waiver.Waiver
	for rows.Next() {
		var w waiver.Waiver
		var from, to string
		if err := rows.Scan(&w.ID, &w.Status, &w.NIK, &w.Name, &w.DOB, &w.Company, &w.Type, &from, &to); err != nil {
			return nil, fmt.Errorf("scan open waiver: %w", err)
		}
		w.From, w.To = dates.Day(from), dates.Day(to)
		out = append(out, w)
	}
	return out, rows.Err()
}

// CapRows returns what the yearly company cap counts: every waiver whose visit
// starts in the year (as "yyyy"). waiver.CapUsed picks the company and the statuses.
func (s Store) CapRows(ctx context.Context, year string) ([]waiver.Counted, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT company, status, visit_from, days FROM waiver WHERE visit_from >= ? AND visit_from <= ?`,
		year+"-01-01", year+"-12-31")
	if err != nil {
		return nil, fmt.Errorf("read waivers of the year: %w", err)
	}
	defer rows.Close()
	var out []waiver.Counted
	for rows.Next() {
		var c waiver.Counted
		var from string
		if err := rows.Scan(&c.Company, &c.Status, &from, &c.Days); err != nil {
			return nil, fmt.Errorf("scan waiver of the year: %w", err)
		}
		c.From = dates.Day(from)
		out = append(out, c)
	}
	return out, rows.Err()
}
