// Package reviewlog stores reviewer decisions and lists what is waiting for them.
//
// WHY separate from packages permit and waiver: those hold the pure rules
// (which status may a stage act on, how long must a rejection note be). This is
// the database side: it reads the status inside a transaction, asks the rule,
// and writes the result only if nobody else got there first.
package reviewlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
	"entry-permit/internal/waiver"
)

// ErrNotFound means no request has that id.
var ErrNotFound = errors.New("That request no longer exists.")

const (
	stageFinal = "final"
	stageDoc   = "doctor"

	decisionApproved = "approved"
	decisionRejected = "rejected"

	lepAttempts   = 10 // tries at drawing a free LEP number
	maxTicketNote = 80 // width of ticket.doctor_note
)

// Store reads and writes the review tables.
type Store struct{ DB *sql.DB }

// Doc is one uploaded file a reviewer can open.
type Doc struct{ Label, Path string }

// Step is a decision already made on a request, shown to the next reviewer.
type Step struct{ Stage, Reviewer, Decision, Note, At string }

// PermitItem is one work permit waiting for a reviewer.
type PermitItem struct {
	RequestID, WorkerName, Company, Position, Nationality string
	RequesterName, RequesterEmail, WorkLocation           string
	McuResult, McuValidUntil, LepValidUntil, CreatedAt    string
	Docs                                                  []Doc
	Steps                                                 []Step
}

// WaiverItem is one visiting waiver waiting for a reviewer.
type WaiverItem struct {
	RequestID, Name, Company, Nationality, Position, Department string
	VisitLocation, Purpose, VisitFrom, VisitTo                  string
	RequesterName, RequesterEmail, Risk, Precaution, Prevent    string
	Medicine, CreatedAt                                         string
	MedicineNotes, RiskAnswers, HealthAnswers                   string
	Days                                                        int
	Docs                                                        []Doc
	Steps                                                       []Step
}

// Decision is what a reviewer decided. Stage is doctor, stage1 or final.
type Decision struct {
	Stage, ID      string
	Approve        bool
	Note, Reviewer string
	At             string // yyyy-mm-dd hh:mm:ss, Jakarta
}

// Outcome is what a decision changed.
type Outcome struct {
	Status  string
	LEP     string // set when a Final approval issued one
	Renewed int    // earlier permits of the same person that were marked renewed
	Notice  Notice // what the decision e-mail needs, read in the same transaction
}

// Notice is the requester-facing data of a decided request, so the e-mail is
// built from exactly what was approved (the LEP date is the one just stored).
type Notice struct {
	Requester, Name, Company, Position, Location string
	ValidUntil, VisitFrom, VisitTo               string
}

// docColumn pairs a file column with the name a reviewer reads.
type docColumn struct{ column, label string }

var permitDocs = []docColumn{
	{"id_doc", "ID document"}, {"id_photo", "ID photo"}, {"visa_doc", "Work visa"}, {"rptka_doc", "RPTKA"},
	{"immigration_doc", "Immigration letter"}, {"police_doc", "Police letter"}, {"contract_doc", "Contract"},
	{"bpjs_kes_doc", "BPJS Kesehatan"}, {"bpjs_tk_doc", "BPJS Ketenagakerjaan"}, {"cert_doc", "Certificate of competence"},
}

var waiverDocs = []docColumn{
	{"id_doc", "ID document"}, {"id_photo", "ID photo"}, {"letter_doc", "Appointment letter"}, {"visa_doc", "ITAS / e-visa"},
}

// docsFrom keeps the files that exist, in the order of the label list.
func docsFrom(list []docColumn, paths []string) []Doc {
	var out []Doc
	for i, p := range paths {
		if p != "" {
			out = append(out, Doc{Label: list[i].label, Path: p})
		}
	}
	return out
}

func columnNames(list []docColumn) string {
	cols := make([]string, len(list))
	for i, d := range list {
		cols[i] = d.column
	}
	return strings.Join(cols, ", ")
}

// scanTargets returns a pointer to each element of vals, for Scan.
func scanTargets(vals []string) []any {
	out := make([]any, len(vals))
	for i := range vals {
		out[i] = &vals[i]
	}
	return out
}

// steps reads the decisions already made on a request, oldest first.
func (s Store) steps(ctx context.Context, id string) ([]Step, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT stage, reviewer, decision, note, at FROM review_step WHERE subject_id = ? ORDER BY at, stage`, id)
	if err != nil {
		return nil, fmt.Errorf("read review steps: %w", err)
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.Stage, &st.Reviewer, &st.Decision, &st.Note, &st.At); err != nil {
			return nil, fmt.Errorf("scan review step: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// PermitQueue lists the work permits in one status, oldest first.
func (s Store) PermitQueue(ctx context.Context, status string) ([]PermitItem, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT request_id, worker_name, company, work_position, nationality, requester_name, requester_email, work_location,
       mcu_result, mcu_valid_until, lep_valid_until, created_at, `+columnNames(permitDocs)+`
FROM permit WHERE status = ? ORDER BY created_at, request_id`, status)
	if err != nil {
		return nil, fmt.Errorf("read permit queue: %w", err)
	}
	defer rows.Close()
	var out []PermitItem
	for rows.Next() {
		var it PermitItem
		paths := make([]string, len(permitDocs))
		dest := append([]any{&it.RequestID, &it.WorkerName, &it.Company, &it.Position, &it.Nationality, &it.RequesterName,
			&it.RequesterEmail, &it.WorkLocation, &it.McuResult, &it.McuValidUntil, &it.LepValidUntil, &it.CreatedAt}, scanTargets(paths)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan permit queue: %w", err)
		}
		it.Docs = docsFrom(permitDocs, paths)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // finish with this cursor before the next query: the test pool has one connection
	for i := range out {
		if out[i].Steps, err = s.steps(ctx, out[i].RequestID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// WaiverQueue lists the visiting waivers in one status, oldest first.
func (s Store) WaiverQueue(ctx context.Context, status string) ([]WaiverItem, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT request_id, visitor_name, company, nationality, position, department, visit_location, purpose, visit_from, visit_to,
       requester_name, requester_email, risk, precaution, prevent, medicine1, created_at, days,
       dosage1, medicine2, dosage2, medicine3, dosage3, medicine_notes, risk_answers, health_answers, `+columnNames(waiverDocs)+`
FROM waiver WHERE status = ? ORDER BY created_at, request_id`, status)
	if err != nil {
		return nil, fmt.Errorf("read waiver queue: %w", err)
	}
	defer rows.Close()
	var out []WaiverItem
	for rows.Next() {
		var it WaiverItem
		var d1, m2, d2, m3, d3 string
		paths := make([]string, len(waiverDocs))
		dest := append([]any{&it.RequestID, &it.Name, &it.Company, &it.Nationality, &it.Position, &it.Department, &it.VisitLocation,
			&it.Purpose, &it.VisitFrom, &it.VisitTo, &it.RequesterName, &it.RequesterEmail, &it.Risk, &it.Precaution, &it.Prevent,
			&it.Medicine, &it.CreatedAt, &it.Days, &d1, &m2, &d2, &m3, &d3, &it.MedicineNotes, &it.RiskAnswers, &it.HealthAnswers}, scanTargets(paths)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan waiver queue: %w", err)
		}
		it.Medicine = medicineLines(it.Medicine, d1, m2, d2, m3, d3)
		it.Docs = docsFrom(waiverDocs, paths)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if out[i].Steps, err = s.steps(ctx, out[i].RequestID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// recordStep writes the decision inside the open transaction.
func recordStep(ctx context.Context, tx *sql.Tx, d Decision) error {
	decision := decisionRejected
	if d.Approve {
		decision = decisionApproved
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO review_step (subject_id, stage, reviewer, decision, note, at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Stage, d.Reviewer, decision, strings.TrimSpace(d.Note), d.At)
	if err != nil {
		return fmt.Errorf("record review step: %w", err)
	}
	return nil
}

// guardedUpdate runs an UPDATE that must hit exactly one row. WHY: every update
// says "WHERE status = <the status I read>", so a double click or a second
// reviewer finds zero rows and gets ErrAlreadyActioned instead of acting twice.
func guardedUpdate(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return permit.ErrAlreadyActioned
	}
	return nil
}

// permitRow is the part of a stored permit a decision needs.
type permitRow struct {
	status, personal, nationality, mcu, visaType, visaExpiry, passport, contract string
	patient                                                                      int64
}

// validityFor works the LEP valid-until date out of the stored row, the same
// inputs the submit used (permits doc section 3).
func validityFor(p policy.Policy, cur permitRow) (dates.Day, error) {
	in := permit.ValidityInput{
		Foreign:       !strings.EqualFold(strings.TrimSpace(cur.nationality), "indonesia"),
		McuValidUntil: dates.Day(cur.mcu), ContractValidUntil: dates.Day(cur.contract), PassportValidUntil: dates.Day(cur.passport),
	}
	switch cur.visaType {
	case permit.VisaKindVisa:
		in.Visa = dates.Day(cur.visaExpiry)
	case permit.VisaKindItas:
		in.Itas = dates.Day(cur.visaExpiry)
	case permit.VisaKindItap:
		in.Itap = dates.Day(cur.visaExpiry)
	}
	valid, _, err := permit.ResolveValidity(p, in)
	return valid, err
}

// DecidePermit records a Stage 1 or Final decision on a work permit. A Final
// approval also issues the LEP number, recomputes the validity from the stored
// row and marks the person's earlier permits renewed, all in one transaction.
func (s Store) DecidePermit(ctx context.Context, p policy.Policy, d Decision) (Outcome, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, fmt.Errorf("begin review: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed

	var cur permitRow
	err = tx.QueryRowContext(ctx, `
SELECT status, patient_id, personal_id, nationality, mcu_valid_until, visa_type, visa_expiry, passport_until, contract_until
FROM permit WHERE request_id = ?`, d.ID).
		Scan(&cur.status, &cur.patient, &cur.personal, &cur.nationality, &cur.mcu, &cur.visaType, &cur.visaExpiry, &cur.passport, &cur.contract)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrNotFound
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("read permit: %w", err)
	}
	next, err := permit.Review(p, permit.Stage(d.Stage), cur.status, d.Approve, d.Note)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Status: next}

	if d.Stage == stageFinal && d.Approve {
		if err := issueLEP(ctx, tx, p, d, cur, next, &out); err != nil {
			return Outcome{}, err
		}
	} else if err := guardedUpdate(ctx, tx, `UPDATE permit SET status = ? WHERE request_id = ? AND status = ?`, next, d.ID, cur.status); err != nil {
		return Outcome{}, err
	}
	if err := recordStep(ctx, tx, d); err != nil {
		return Outcome{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT requester_email, worker_name, company, work_position, work_location, lep_valid_until FROM permit WHERE request_id = ?`, d.ID).
		Scan(&out.Notice.Requester, &out.Notice.Name, &out.Notice.Company, &out.Notice.Position, &out.Notice.Location, &out.Notice.ValidUntil); err != nil {
		return Outcome{}, fmt.Errorf("read permit notice: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Outcome{}, fmt.Errorf("commit review: %w", err)
	}
	return out, nil
}

// issueLEP is the Final approval: it draws a free LEP number, stores it with the
// recomputed validity, and renews the person's earlier permits.
func issueLEP(ctx context.Context, tx *sql.Tx, p policy.Policy, d Decision, cur permitRow, next string, out *Outcome) error {
	valid, err := validityFor(p, cur)
	if err != nil {
		return err
	}
	lep, err := permit.UniqueID("LEP-", lepAttempts, func(id string) (bool, error) {
		var n int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM permit WHERE lep_no = ?`, id).Scan(&n)
		return n > 0, err
	})
	if err != nil {
		return err
	}
	if err := guardedUpdate(ctx, tx, `UPDATE permit SET status = ?, lep_no = ?, lep_valid_until = ? WHERE request_id = ? AND status = ?`,
		next, lep, string(valid), d.ID, cur.status); err != nil {
		return err
	}
	out.LEP = lep
	out.Renewed, err = renewEarlier(ctx, tx, cur.patient, cur.personal, lep)
	return err
}

// renewEarlier marks the person's earlier permits "Renewed by <new LEP>"
// (permits doc section 5) and returns how many it changed.
//
// WHY the person is matched in SQL and passed to the rule as one fixed key:
// medpost's NIK is not always known, so a person is the same patient id or the
// same NIK, exactly as the duplicate gate matches them (permitlog.Latest).
func renewEarlier(ctx context.Context, tx *sql.Tx, patient int64, personal, newLEP string) (int, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT request_id, status, lep_no FROM permit WHERE patient_id = ? OR (? <> '' AND personal_id = ?)`, patient, personal, personal)
	if err != nil {
		return 0, fmt.Errorf("read earlier permits: %w", err)
	}
	const samePerson = "same"
	var all []permit.Existing
	for rows.Next() {
		e := permit.Existing{PersonalID: samePerson}
		if err := rows.Scan(&e.ID, &e.Status, &e.LEP); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan earlier permit: %w", err)
		}
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close() // the transaction has one connection: finish reading before writing
	targets := permit.RenewalTargets(samePerson, newLEP, all)
	for _, t := range targets {
		if _, err := tx.ExecContext(ctx, `UPDATE permit SET status = ? WHERE request_id = ?`, t.NewStatus, t.ID); err != nil {
			return 0, fmt.Errorf("mark permit renewed: %w", err)
		}
	}
	return len(targets), nil
}

// DecideWaiver records a Doctor, Stage 1 or Final decision on a waiver. Final
// approval activates the waiver's ticket, and any rejection rejects it.
func (s Store) DecideWaiver(ctx context.Context, p policy.Policy, d Decision) (Outcome, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, fmt.Errorf("begin review: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var cur string
	err = tx.QueryRowContext(ctx, `SELECT status FROM waiver WHERE request_id = ?`, d.ID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrNotFound
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("read waiver: %w", err)
	}
	next, err := waiver.Review(p, waiver.Stage(d.Stage), cur, d.Approve, d.Note)
	if err != nil {
		return Outcome{}, err
	}
	if err := guardedUpdate(ctx, tx, `UPDATE waiver SET status = ? WHERE request_id = ? AND status = ?`, next, d.ID, cur); err != nil {
		return Outcome{}, err
	}
	if err := recordStep(ctx, tx, d); err != nil {
		return Outcome{}, err
	}
	if err := moveWaiverTicket(ctx, tx, d.ID, next); err != nil {
		return Outcome{}, err
	}
	out := Outcome{Status: next}
	if err := tx.QueryRowContext(ctx, `SELECT requester_email, visitor_name, company, visit_location, visit_from, visit_to FROM waiver WHERE request_id = ?`, d.ID).
		Scan(&out.Notice.Requester, &out.Notice.Name, &out.Notice.Company, &out.Notice.Location, &out.Notice.VisitFrom, &out.Notice.VisitTo); err != nil {
		return Outcome{}, fmt.Errorf("read waiver notice: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Outcome{}, fmt.Errorf("commit review: %w", err)
	}
	return out, nil
}

// moveWaiverTicket keeps the waiver's ticket in step with the waiver. The ticket
// only ever leaves PendingDoctor once, so a repeat does nothing.
func moveWaiverTicket(ctx context.Context, tx *sql.Tx, id, next string) error {
	switch next {
	case waiver.StatusApproved:
		// The doctor's note carries over: the ticket shows who cleared the visitor and why.
		var note string
		err := tx.QueryRowContext(ctx, `SELECT note FROM review_step WHERE subject_id = ? AND stage = ?`, id, stageDoc).Scan(&note)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read doctor note: %w", err)
		}
		if note = strings.TrimSpace(note); note == "" {
			note = "_"
		}
		if r := []rune(note); len(r) > maxTicketNote {
			note = string(r[:maxTicketNote])
		}
		_, err = tx.ExecContext(ctx, `UPDATE ticket SET status = 'Approved', doctor_review = 'Approved', doctor_note = ? WHERE lep_no = ? AND status = 'PendingDoctor'`, note, id)
		return wrapTicket(err)
	case waiver.StatusRejected:
		_, err := tx.ExecContext(ctx, `UPDATE ticket SET status = 'Rejected' WHERE lep_no = ? AND status = 'PendingDoctor'`, id)
		return wrapTicket(err)
	}
	return nil
}

func wrapTicket(err error) error {
	if err != nil {
		return fmt.Errorf("update waiver ticket: %w", err)
	}
	return nil
}

// medicineLines joins the medicines a visitor listed into one readable text, "Name (dosage)" each,
// separated by "; ". Blank pairs are left out.
func medicineLines(m1, d1, m2, d2, m3, d3 string) string {
	var parts []string
	for _, p := range [][2]string{{m1, d1}, {m2, d2}, {m3, d3}} {
		name, dose := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		switch {
		case name == "":
		case dose == "":
			parts = append(parts, name)
		default:
			parts = append(parts, name+" ("+dose+")")
		}
	}
	return strings.Join(parts, "; ")
}

// Recorded says whether the Risk Exposure and Health Declaration cards were answered. Requests
// made before the cards existed (and imported ones) have none, and a reviewer should be told so
// instead of seeing an empty list that looks like "all No".
func (w WaiverItem) Recorded() bool {
	return waiver.ValidAnswers(w.RiskAnswers, waiver.RiskQuestions) && waiver.ValidAnswers(w.HealthAnswers, waiver.HealthQuestions)
}

// RiskYes and HealthYes list the questions the visitor answered Yes.
func (w WaiverItem) RiskYes() []string { return waiver.YesLabels(w.RiskAnswers, waiver.RiskQuestions) }
func (w WaiverItem) HealthYes() []string {
	return waiver.YesLabels(w.HealthAnswers, waiver.HealthQuestions)
}

// HighRisk is the old "high risk" flag: any Yes on either card.
func (w WaiverItem) HighRisk() bool { return waiver.HighRisk(w.RiskAnswers, w.HealthAnswers) }
