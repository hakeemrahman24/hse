package directory

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// filter is what an outside database may ask for on /api/directory/checkups and
// /api/directory/latest-checkups, so it does not have to download every checkup and sort them
// itself. Every field is optional; with none set the routes return everything, as before.
type filter struct {
	patientID  int64
	employeeID string
	checkUpID  string
	name       string // part of the name, at least two letters
	result     string // the stored code (FitWithNote), whatever the caller typed
	from, to   string // YYYY-MM-DD, on the checkup date (else the review date)
}

var errBadFilter = errors.New("invalid filter")

// resultCodes maps what a caller may type (the words the API returns, or the stored code) to the
// stored code. Keys are lower case with spaces removed.
var resultCodes = map[string]string{
	"fit": "Fit", "fitwithnote": "FitWithNote", "unfit": "Unfit",
	"temporarilyunfit": "TemporaryUnfit", "temporaryunfit": "TemporaryUnfit",
	"needfollowup": "NeedFollowUp", "incomplete": "IncompleteResult", "incompleteresult": "IncompleteResult",
}

func parseFilter(r *http.Request) (filter, error) {
	q := r.URL.Query()
	f := filter{
		employeeID: strings.TrimSpace(q.Get("employeeId")),
		checkUpID:  strings.TrimSpace(q.Get("checkUpId")),
		name:       strings.TrimSpace(q.Get("q")),
		from:       strings.TrimSpace(q.Get("from")),
		to:         strings.TrimSpace(q.Get("to")),
	}
	if raw := strings.TrimSpace(q.Get("patientId")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return filter{}, errors.New("invalid patientId")
		}
		f.patientID = id
	}
	if raw := strings.TrimSpace(q.Get("result")); raw != "" {
		code, ok := resultCodes[strings.ToLower(strings.ReplaceAll(raw, " ", ""))]
		if !ok {
			return filter{}, errors.New("invalid result")
		}
		f.result = code
	}
	for _, d := range []struct{ name, value string }{{"from", f.from}, {"to", f.to}} {
		if d.value == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.value); err != nil {
			return filter{}, errors.New("invalid " + d.name + ", use yyyy-MM-dd")
		}
	}
	if len([]rune(f.name)) == 1 {
		f.name = "" // one letter matches half the roster; the employee search ignores it too
	}
	return f, nil
}

// checkupDate is the date a checkup counts under: the day it was done, else the day it was reviewed.
func checkupDate(alias string) string {
	return "COALESCE(NULLIF(" + alias + ".DateCheckUp, ''), " + alias + ".ReviewDate)"
}

// conditions returns " AND ..." clauses for the filters. p is the Patient alias and m the McuRecord
// alias the clauses apply to. Every value travels as a bound parameter.
func (f filter) conditions(p, m string) (string, []any) {
	var sb strings.Builder
	var args []any
	add := func(clause string, arg any) {
		sb.WriteString(" AND " + clause)
		args = append(args, arg)
	}
	if f.patientID != 0 {
		add(p+".PatientID = ?", f.patientID)
	}
	if f.employeeID != "" {
		add(p+".EmployeeID = ?", f.employeeID)
	}
	if f.checkUpID != "" {
		add(m+".CheckUpID = ?", f.checkUpID)
	}
	if f.name != "" {
		add("LOWER("+p+".Name) LIKE ?", "%"+strings.ToLower(f.name)+"%")
	}
	if f.result != "" {
		add(m+".ResultCriteria = ?", f.result)
	}
	if f.from != "" {
		add(checkupDate(m)+" >= ?", f.from)
	}
	if f.to != "" {
		add(checkupDate(m)+" <= ?", f.to)
	}
	return sb.String(), args
}

// checkupQueryFor is every checkup (oldest first) that matches the filters.
func checkupQueryFor(f filter) (string, []any) {
	where, args := f.conditions("p", "m")
	return checkupSelect + " WHERE 1 = 1" + where + " ORDER BY m.ReviewDate, m.CheckUpID", args
}
