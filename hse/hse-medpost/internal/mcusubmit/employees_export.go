package mcusubmit

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// Excel export of the Employee checkups list (owner, 2026-11-14). It gives HR the same rows the page
// shows for the chosen filters (employee, status, include subcontractors), all pages instead of one.
// The companies come from the session's Scope exactly like the page, never from the request.

var employeeExportHeaders = []string{"Employee", "Company", "Position", "Nationality", "Last checkup", "Valid until", "Days left", "Validity", "Result", "Recommendation / notes"}

// employeeExportRows turns the employees into sheet rows. Cells are plain values (never formulas),
// so a name that starts with "=" is shown as text and is not run by Excel.
func employeeExportRows(list []Employee) [][]any {
	rows := make([][]any, 0, len(list))
	for _, e := range list {
		validUntil, daysLeft := any(""), any("")
		if e.HasValidity {
			validUntil, daysLeft = e.ValidUntil, e.DaysLeft
		}
		note := e.Note
		if e.NoteLabel != "" && note != "" {
			note = e.NoteLabel + ": " + note
		}
		rows = append(rows, []any{e.Name, e.Company, e.Position, e.Nationality, e.CheckUpDate, validUntil, daysLeft, e.Status, e.Result, note})
	}
	return rows
}

// What the file holds (owner, 2026-11-14). "displayed" is the list as the page shows it now, with its
// filters and order, and is what a link without a scope gives, so old links keep working. "all" is every
// worker the account may see, whatever the filters. "latest" is the N most recent checkups, newest first.
const (
	exportDisplayed = "displayed"
	exportAll       = "all"
	exportLatest    = "latest"

	defaultLatest = 100
	maxLatest     = 10000
)

// exportSelection picks and orders the rows of the file. The scope and the count come from the request
// but are only ever words and a bounded number: the companies still come from the session's Scope.
func exportSelection(all []Employee, q url.Values) ([]Employee, error) {
	sortKey, sortDir, _ := parseSort(q) // the file is in the order the page showed
	switch scope := q.Get("scope"); scope {
	case "", exportDisplayed:
		return sortEmployees(filterEmployees(all, q.Get("worker"), q.Get("status"), q.Get("nationality")), sortKey, sortDir), nil
	case exportAll:
		return sortEmployees(all, sortKey, sortDir), nil
	case exportLatest:
		n, err := strconv.Atoi(strings.TrimSpace(q.Get("n")))
		if err != nil || n < 1 || n > maxLatest {
			return nil, fmt.Errorf("Enter how many records, from 1 to %d.", maxLatest)
		}
		// A worker with no checkup has no record to be "latest", so they are left out.
		var dated []Employee
		for _, e := range sortEmployees(all, sortCheckup, dirDesc) {
			if e.CheckUpDate != "" {
				dated = append(dated, e)
			}
		}
		if len(dated) > n {
			dated = dated[:n]
		}
		return dated, nil
	default:
		return nil, errors.New("Choose what to export.")
	}
}

// EmployeesExport downloads the chosen employees (see exportSelection) as an .xlsx file.
func (a *API) EmployeesExport(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	known := validStatusFilter(status)
	if !known {
		http.Error(w, "Check the status you chose.", http.StatusBadRequest)
		return
	}
	all, _, _, err := a.employeeSource(r.Context(), u, q.Get("subs") == "1")
	if err != nil {
		a.serverError(w, "company employees", err)
		return
	}
	rows, err := exportSelection(all, q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Employees"
	_ = f.SetSheetName("Sheet1", sheet)
	for i, h := range employeeExportHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for r, vals := range employeeExportRows(rows) {
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	widths := []float64{28, 28, 24, 16, 14, 14, 10, 22, 16, 50}
	for i, wd := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetColWidth(sheet, col, col, wd)
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="employee-checkups-`+time.Now().Format("2006-01-02")+`.xlsx"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := f.Write(w); err != nil {
		log.Printf("employees export write: %v", err)
	}
}
