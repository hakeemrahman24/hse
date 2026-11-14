package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// "My employees" (owner, 2026-11-13): the account sees every worker of its own company with the
// validity of their latest medical checkup, so HR can plan renewals without opening each document.
// A worker belongs to the company through Patient.FK_CompanyID. Only the account's own company is
// listed, and the subcontractors' workers only when the account ticks "Include subcontractors"
// (owner, 2026-11-13; a main contractor already reads their documents). The companies come from the
// session's Scope, never from the request, so nobody can ask for a company outside their tree.

// Validity labels. They are the choices of the status filter, in the order HR thinks of them.
const (
	ValValid      = "Valid"
	ValExpiring   = "Expiring soon"
	ValExpired    = "Expired"
	ValUnfit      = "Unfit"
	ValTempUnfit  = "Temporarily unfit"
	ValFollowUp   = "Needs follow-up"
	ValInProgress = "Checkup in progress"
	ValNone       = "No checkup on record"
)

var employeeStatuses = []string{ValValid, ValExpiring, ValExpired, ValUnfit, ValTempUnfit, ValFollowUp, ValInProgress, ValNone}

// Employee is one worker of the company and where their latest checkup stands.
type Employee struct {
	PatientID                       int64
	Name, Position, Company         string
	Nationality                     string // from NationalityTable; empty when the worker has none on record
	CheckUpDate, Result, ValidUntil string
	Status                          string // one of the Val* labels
	DaysLeft                        int    // days until ValidUntil; negative once expired; only meaningful for the three fit statuses
	HasValidity                     bool   // Status is Valid, Expiring soon or Expired (DaysLeft means something)
	NoteLabel, Note                 string // "Recommendation" or "Notes" and the reviewer's words; empty when there is none to show
	HasCertificate                  bool   // the latest checkup has a certificate the page may offer (see LatestRecord.HasCertificate)
}

// StatusSlug is the status as a CSS word ("tone-expiring" colours the tile, the dot and the card edge).
// It is a method so the template can ask the row, and a switch so a new status has to be named here.
func (e Employee) StatusSlug() string {
	switch e.Status {
	case ValValid:
		return "valid"
	case ValExpiring:
		return "expiring"
	case ValExpired:
		return "expired"
	case ValUnfit:
		return "unfit"
	case ValTempUnfit:
		return "tempunfit"
	case ValFollowUp:
		return "followup"
	case ValInProgress:
		return "progress"
	}
	return "none"
}

// DaysExpired is how many days ago the validity ran out, for the "Expired for 12 days" text; zero for
// anyone still valid. DaysLeft is negative once expired, so this is its absolute value.
func (e Employee) DaysExpired() int {
	if e.DaysLeft < 0 {
		return -e.DaysLeft
	}
	return 0
}

// validityOf turns a checkup's result and validity date into the label HR reads. It is pure so the
// rule is tested without a database. Fit and Fit with note are the only results with a lapse date
// that matters; the other results are shown as they are because a date would mislead (an unfit
// worker is not covered by their date). windowDays is the same "expiring soon" window as the
// reminder mails, so the list and the mail agree.
func validityOf(result, validUntil string, today time.Time, windowDays int) (status string, daysLeft int, hasValidity bool) {
	switch result {
	case "Fit", "FitWithNote":
		if len(validUntil) < 10 {
			return ValInProgress, 0, false // fit but no date yet: the checkup is not finished
		}
		end, err := time.Parse(dateOnlyLayout, validUntil[:10])
		if err != nil {
			return ValInProgress, 0, false
		}
		day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		left := int(end.Sub(day).Hours() / 24)
		switch {
		case left < 0:
			return ValExpired, left, true
		case left <= windowDays:
			return ValExpiring, left, true
		}
		return ValValid, left, true
	case "Unfit":
		return ValUnfit, 0, false
	case "TemporaryUnfit":
		return ValTempUnfit, 0, false
	case "NeedFollowUp":
		return ValFollowUp, 0, false
	}
	return ValInProgress, 0, false
}

// CompanyEmployees lists the workers of one company with their latest checkup (the newest by date,
// the same order LatestRecord uses). A worker whose latest checkup is Archived / Exit has left and
// is not listed. Worst first would hide the quiet majority, so it is by name; the page filters.
func (s *Store) CompanyEmployees(ctx context.Context, companyIDs []int64, windowDays int) ([]Employee, error) {
	if len(companyIDs) == 0 {
		return nil, nil
	}
	marks, args := strings.TrimSuffix(strings.Repeat("?,", len(companyIDs)), ","), make([]any, len(companyIDs))
	for i, id := range companyIDs {
		args[i] = id
	}
	return s.listEmployees(ctx, `p.FK_CompanyID IN (`+marks+`)`, args, windowDays)
}

// AllEmployees is the admin's view (owner, 2026-11-14): every worker of every company, with the same
// latest-checkup rule as CompanyEmployees. There is deliberately no company filter here, so only the
// admin handlers may call it; a company account always goes through CompanyEmployees and its Scope.
func (s *Store) AllEmployees(ctx context.Context, windowDays int) ([]Employee, error) {
	return s.listEmployees(ctx, `1 = 1`, nil, windowDays)
}

// listEmployees runs the shared query. where is built by the two callers above from constants and
// "?" marks only, never from request text, so the values travel as args.
func (s *Store) listEmployees(ctx context.Context, where string, args []any, windowDays int) ([]Employee, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT p.PatientID, p.Name, COALESCE(p.WorkPosition, ''), COALESCE(c.Name, ''), COALESCE(nt.Name, ''), COALESCE(m.McuRecordID, 0),
		        COALESCE(m.DateCheckUp, ''), COALESCE(m.ResultCriteria, ''), COALESCE(m.ResultValidUntil, ''), COALESCE(m.CheckUpStatus, ''),
		        COALESCE(m.ResultNotes, ''), COALESCE(m.Recommendation, '')
		 FROM Patient p
		 LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		 LEFT JOIN NationalityTable nt ON nt.NationalityID = p.FK_NationalityID
		 LEFT JOIN McuRecord m ON m.McuRecordID = (
		       SELECT m2.McuRecordID FROM McuRecord m2 WHERE m2.FK_PatientID = p.PatientID
		       ORDER BY COALESCE(m2.DateCheckUp, '') DESC, m2.McuRecordID DESC LIMIT 1)
		 WHERE `+where+`
		 ORDER BY p.Name, p.PatientID`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	today := s.clinicToday()
	var out []Employee
	for rows.Next() {
		var e Employee
		var record int64
		var checkUpStatus, notes, recommendation string
		if err := rows.Scan(&e.PatientID, &e.Name, &e.Position, &e.Company, &e.Nationality, &record, &e.CheckUpDate, &e.Result, &e.ValidUntil, &checkUpStatus, &notes, &recommendation); err != nil {
			return nil, err
		}
		if checkUpStatus == "Archived / Exit" {
			continue
		}
		if record == 0 {
			e.Status = ValNone
		} else {
			e.Status, e.DaysLeft, e.HasValidity = validityOf(e.Result, e.ValidUntil, today, windowDays)
			// The same rule as the document list and the MCU tab: Unfit always, Fit only once Close.
			e.HasCertificate = LatestRecord{Result: e.Result, Status: checkUpStatus}.HasCertificate()
			e.NoteLabel, e.Note = noteFor(e.Result, notes, recommendation)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// noteFor is what the More button shows, by the portal's existing rule (owner, 2026-10-31, see
// Submission.ResultMessage): an incomplete or follow-up result shows the Recommendation, a temporarily
// unfit or unfit one the notes, exactly as the reviewer wrote them. The notes of a Fit result are
// not shown: reviewers do not write those for the submitter to read.
func noteFor(result, notes, recommendation string) (label, text string) {
	switch result {
	case "NeedFollowUp", "IncompleteResult":
		return "Recommendation", strings.TrimSpace(recommendation)
	case "TemporaryUnfit", "Unfit":
		return "Notes", strings.TrimSpace(notes)
	}
	return "", ""
}

// filterEmployees keeps the workers whose name has every typed word (any order, like the document
// search) and, when status is not empty, that validity label.
func filterEmployees(all []Employee, worker, status, nationality string) []Employee {
	words := splitWords(worker)
	var out []Employee
	for _, e := range all {
		if status != "" && e.Status != status {
			continue
		}
		if nationality != "" && e.Nationality != nationality {
			continue
		}
		name := strings.ToUpper(e.Name)
		match := true
		for _, w := range words {
			if !strings.Contains(name, strings.ToUpper(w)) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}

// Employees is the "My employees" page.
func (a *API) Employees(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	known := status == ""
	for _, s := range employeeStatuses {
		known = known || s == status
	}
	p := a.newPage("Employee checkups")
	p.User, p.Caption = u, "Latest medical checkup of every worker of your company"
	p.Statuses = employeeStatuses
	p.Form = map[string]string{"worker": q.Get("worker"), "status": status, "nationality": q.Get("nationality")}
	// The admin sees every company, so the Company column is always on for them.
	p.ShowCompany = u.Role == RoleAdmin
	if p.ShowCompany {
		p.Caption = "Latest medical checkup of every worker of every company"
	}
	all, subs, canSubs, err := a.employeeSource(r.Context(), u, q.Get("subs") == "1")
	if err != nil {
		a.serverError(w, "company employees", err)
		return
	}
	p.CanSubs = canSubs
	if subs {
		p.Form["subs"] = "1"
	}
	if !known {
		p.Error = "Check the status you chose."
		a.render(w, http.StatusBadRequest, "employees.html", p)
		return
	}
	// The tiles count the whole company whatever the filter, like the Needs action chip.
	p.Counts = map[string]int{}
	for _, e := range all {
		p.Counts[e.Status]++
	}
	if a.certs == nil { // a build without the clinical side has no download route, so no links
		for i := range all {
			all[i].HasCertificate = false
		}
	}
	// The order is part of the address only when the person picked one; the default (latest checkup
	// first) needs no words, so the plain page address stays plain.
	sortKey, sortDir, explicit := parseSort(q)
	if explicit {
		p.Form["sort"], p.Form["dir"] = sortKey, sortDir
	}
	p.SortKey, p.SortDir = sortKey, sortDir
	p.SortURLs, p.SortAria = sortLinks(p.Form, sortKey, sortDir)
	p.Tiles = statusTiles(p.Counts, p.Form, status)
	// Every matching worker is sent: the list scrolls inside the page instead of paging.
	// The nationality choices are the ones that exist in this list, so the box never offers a country
	// with nobody in it. The export panel shows both totals: what is on screen and everything.
	p.Nationalities = employeeNationalities(all)
	p.AllTotal = len(all)
	p.Employees = sortEmployees(filterEmployees(all, p.Form["worker"], status, p.Form["nationality"]), sortKey, sortDir)
	p.Result = DocPage{Total: len(p.Employees)}
	a.render(w, http.StatusOK, "employees.html", p)
}

// employeeSource is the one place that decides which workers an account may list, so the page and
// the Excel export cannot drift apart. The admin gets everybody. Anyone else gets their own company,
// plus the subcontractors when wantSubs is asked and they have any; the companies come from the
// session's Scope, never from the request. It returns whether subcontractors ended up included and
// whether the option exists for this account at all.
func (a *API) employeeSource(ctx context.Context, u *Submitter, wantSubs bool) (list []Employee, subs, canSubs bool, err error) {
	if u.Role == RoleAdmin {
		list, err = a.Store.AllEmployees(ctx, reminderWindowDays)
		return list, false, false, err
	}
	sc, err := a.Store.ScopeFor(ctx, u)
	if err != nil {
		return nil, false, false, fmt.Errorf("scope: %w", err)
	}
	companies := []int64{u.CompanyID}
	canSubs = len(sc.CompanyIDs) > 1 // without subcontractors the tick is ignored
	if canSubs && wantSubs {
		subs, companies = true, sc.CompanyIDs
	}
	list, err = a.Store.CompanyEmployees(ctx, companies, reminderWindowDays)
	return list, subs, canSubs, err
}

// EmployeeCertificateRecord returns the newest checkup of a worker when the worker belongs to a company
// in the account's scope (own company or a subcontractor) and that checkup has a certificate, else
// ErrNotFound. The worker id comes from the link, so the company check is what stops one company
// from opening another's certificate, and a missing worker and a forbidden one look the same.
func (s *Store) EmployeeCertificateRecord(ctx context.Context, sc Scope, patientID int64, isAdmin bool) (int64, error) {
	var company sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT FK_CompanyID FROM Patient WHERE PatientID = ?`, patientID).Scan(&company); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	// The admin sees every company's workers on the list, so their certificates open too. Everyone
	// else stays inside their scope.
	if !isAdmin && (!company.Valid || !sc.Covers(company.Int64)) {
		return 0, ErrNotFound
	}
	var l LatestRecord
	err := s.DB.QueryRowContext(ctx,
		`SELECT McuRecordID, COALESCE(ResultCriteria, ''), COALESCE(CheckUpStatus, '') FROM McuRecord WHERE FK_PatientID = ?
		 ORDER BY COALESCE(DateCheckUp, '') DESC, McuRecordID DESC LIMIT 1`, patientID).Scan(&l.RecordID, &l.Result, &l.Status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !l.HasCertificate()) {
		return 0, ErrNotFound
	}
	return l.RecordID, err
}

// EmployeeCertificate sends the certificate of a worker's latest checkup (?view=1 opens it in the tab).
func (a *API) EmployeeCertificate(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok || a.certs == nil {
		a.notFound(w, u)
		return
	}
	var sc Scope // the admin has no company, so no scope; EmployeeCertificateRecord skips it for them
	if u.Role != RoleAdmin {
		var err error
		if sc, err = a.Store.ScopeFor(r.Context(), u); err != nil {
			a.serverError(w, "scope", err)
			return
		}
	}
	record, err := a.Store.EmployeeCertificateRecord(r.Context(), sc, id, u.Role == RoleAdmin)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: employee certificate record: %v", err)
		}
		a.notFound(w, u)
		return
	}
	// The audit row is about the account that downloaded; Detail holds the checkup record id, never a name.
	a.serveCertificate(w, r, u, record, EntitySubmitter, u.ID)
}
