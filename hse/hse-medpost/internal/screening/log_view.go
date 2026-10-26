package screening

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/dates"
)

// The Medical Record page shows each test (alcohol, fatigue) as a Table tab and
// a Dashboard tab. These handlers back them. They reuse the column lists in
// logSheets (testlog.go), so the table, the Excel file and the Activity Log's
// test log read the same columns. Unlike that older test log, which filters by
// the time a record was uploaded, these filter by the date the test was done.

const (
	viewListLimit      = 500
	viewExportLimit    = 50000
	viewGroupLimit     = 15
	viewMaxNameWords   = 6
	viewMaxNameRunes   = 40
	viewLikeEscape     = "!" // a backslash means different things to MariaDB and SQLite literals
	viewOtherResultKey = "other"
)

// resultClass is one outcome the dashboard counts. Match is a SQL condition on
// the row alias d. The forms store short codes (neg, pos, lab; Low, Mild, ...),
// and older or hand-made rows may spell them out or use another case, so each
// class accepts the variants.
type resultClass struct {
	Key   string
	Match string
}

// logView adds what the dashboard needs to a logSheet: which columns hold the
// name, the company and the position, how to tell the test date, how outcomes
// are classified, and what the chart can be grouped by. Every expression is
// written here, never taken from a request.
type logView struct {
	Name, Company, Position string
	// DateExpr is the test date as YYYY-MM-DD, for kinds that store a plain date.
	// Empty means the date is the UTC timestamp d.CreatedAt, read in the clinic's zone.
	DateExpr string
	Order    string
	Results  []resultClass
	Groups   map[string]string // ?by= choice -> column expression
	// GroupOrder is the ?by= choices in display order; the first is the default.
	GroupOrder []string
}

const fatigueDateExpr = `CASE WHEN d.ExamDate <> '' THEN d.ExamDate ELSE SUBSTR(d.CreatedAt, 1, 10) END`

var logViews = map[string]logView{
	"alcohol": {
		Name: "d.DonorName", Company: "d.DonorCompany", Position: "d.DonorPosition",
		Order: "d.CreatedAt DESC, d.SubmissionID DESC",
		Results: []resultClass{
			{"negative", `LOWER(TRIM(d.FinalResult)) IN ('neg', 'negative')`},
			{"positive", `LOWER(TRIM(d.FinalResult)) IN ('pos', 'positive')`},
			{"lab", `LOWER(TRIM(d.FinalResult)) IN ('lab', 'laboratory')`},
		},
		Groups:     map[string]string{"company": "d.DonorCompany", "position": "d.DonorPosition", "testType": "d.TestType"},
		GroupOrder: []string{"company", "position", "testType"},
	},
	"fatigue": {
		Name: "d.WorkerName", Company: "d.WorkerCompany", Position: "d.WorkerPosition",
		DateExpr: fatigueDateExpr,
		Order:    fatigueDateExpr + " DESC, d.CreatedAt DESC, d.SubmissionID DESC",
		Results: []resultClass{
			{"low", `LOWER(TRIM(d.RiskLevel)) = 'low'`},
			{"mild", `LOWER(TRIM(d.RiskLevel)) = 'mild'`},
			{"medium", `LOWER(TRIM(d.RiskLevel)) = 'medium'`},
			{"high", `LOWER(TRIM(d.RiskLevel)) = 'high'`},
		},
		Groups:     map[string]string{"company": "d.WorkerCompany", "position": "d.WorkerPosition", "examiner": "d.Examiner"},
		GroupOrder: []string{"company", "position", "examiner"},
	},
}

type badView string

func (e badView) Error() string { return string(e) }

// dateClause turns ?from= and ?to= (YYYY-MM-DD, inclusive) into SQL on the test date.
func (v logView) dateClause(r *http.Request) (string, []any, error) {
	clause := ""
	var args []any
	for _, bound := range []struct{ param, op string }{{"from", ">="}, {"to", "<="}} {
		day, err := dates.Normalize(r.URL.Query().Get(bound.param))
		if err != nil {
			return "", nil, badView(bound.param + ": " + err.Error())
		}
		if day == "" {
			continue
		}
		if v.DateExpr != "" {
			clause += " AND (" + v.DateExpr + ") " + bound.op + " ?"
			args = append(args, day)
			continue
		}
		// The clinic's day starts nine hours before UTC's, so the day's edges in
		// UTC are not the day's own date.
		start, err := time.ParseInLocation("2006-01-02", day, dates.ClinicLocation)
		if err != nil {
			return "", nil, badView(bound.param + ": invalid date")
		}
		edge := start
		if bound.param == "to" {
			edge = start.AddDate(0, 0, 1).Add(-time.Second)
		}
		clause += " AND d.CreatedAt " + bound.op + " ?"
		args = append(args, edge.UTC().Format(dates.TimestampLayout))
	}
	return clause, args, nil
}

// nameClause turns ?q= into conditions on the person's name: every word typed
// must appear in it, in any order and any case. %, _ and the escape character
// in the typed text match only themselves.
func (v logView) nameClause(r *http.Request) (string, []any) {
	clause := ""
	var args []any
	words := strings.Fields(r.URL.Query().Get("q"))
	if len(words) > viewMaxNameWords {
		words = words[:viewMaxNameWords]
	}
	replacer := strings.NewReplacer(viewLikeEscape, viewLikeEscape+viewLikeEscape, "%", viewLikeEscape+"%", "_", viewLikeEscape+"_")
	for _, word := range words {
		if runes := []rune(word); len(runes) > viewMaxNameRunes {
			word = string(runes[:viewMaxNameRunes])
		}
		clause += " AND " + v.Name + " LIKE ? ESCAPE '" + viewLikeEscape + "'"
		args = append(args, "%"+replacer.Replace(word)+"%")
	}
	return clause, args
}

// viewRequest is a parsed ?test=, ?from=, ?to= and ?q=.
type viewRequest struct {
	sheet logSheet
	view  logView
	where string // starts with " AND ..." or is empty
	args  []any
}

func parseViewRequest(r *http.Request) (viewRequest, error) {
	key := r.URL.Query().Get("test")
	sheet, ok := findLogSheet(key)
	view, hasView := logViews[key]
	if !ok || !hasView {
		return viewRequest{}, badView("unknown test")
	}
	dateWhere, dateArgs, err := view.dateClause(r)
	if err != nil {
		return viewRequest{}, err
	}
	nameWhere, nameArgs := view.nameClause(r)
	return viewRequest{sheet: sheet, view: view, where: dateWhere + nameWhere, args: append(dateArgs, nameArgs...)}, nil
}

func writeViewError(w http.ResponseWriter, err error) {
	if bad, ok := err.(badView); ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": string(bad)})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
}

// LogViewList: GET /api/screening-log?test=alcohol|fatigue&from=&to=&q=. The
// same rows and columns as the test log, newest test first, filtered by test
// date and by name. Any signed-in staff member (RequireAuth).
func (a *API) LogViewList(w http.ResponseWriter, r *http.Request) {
	req, err := parseViewRequest(r)
	if err != nil {
		writeViewError(w, err)
		return
	}
	headers, rows, err := queryLogWhere(a.DB, req.sheet, "1=1"+req.where, req.args, req.view.Order, viewListLimit)
	if err != nil {
		writeViewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"test": req.sheet.Key, "headers": headers, "rows": rows})
}

// LogViewExport: GET /api/screening-log.xlsx?test=&from=&to=&q=. One sheet with
// every column, following the same filters as the table. With none, every record.
func (a *API) LogViewExport(w http.ResponseWriter, r *http.Request) {
	req, err := parseViewRequest(r)
	if err != nil {
		writeViewError(w, err)
		return
	}
	headers, rows, err := queryLogWhere(a.DB, req.sheet, "1=1"+req.where, req.args, req.view.Order, viewExportLimit)
	if err != nil {
		writeViewError(w, err)
		return
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", req.sheet.Title); err != nil {
		writeViewError(w, err)
		return
	}
	if err := writeLogSheet(f, req.sheet.Title, headers, rows); err != nil {
		writeViewError(w, err)
		return
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		writeViewError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exportFilename([]logSheet{req.sheet}, a.now())))
	_, _ = w.Write(buf.Bytes())
}

type viewGroup struct {
	Name    string         `json:"name"`
	Count   int            `json:"count"`
	Results map[string]int `json:"results"`
}

type viewSummary struct {
	Test       string         `json:"test"`
	Total      int            `json:"total"`
	People     int            `json:"people"`
	Results    map[string]int `json:"results"`
	By         string         `json:"by"`
	Groups     []viewGroup    `json:"groups"`
	MoreGroups int            `json:"moreGroups"`
}

// resultSums is one SUM column per result class, in order. The conditions are
// constants from logViews.
func (v logView) resultSums() string {
	cols := ""
	for _, c := range v.Results {
		cols += ", COALESCE(SUM(CASE WHEN " + c.Match + " THEN 1 ELSE 0 END), 0)"
	}
	return cols
}

// resultCounts zero-fills every class (and "other") so the page can always read
// every key, and gives the scan destinations for the class columns.
func (v logView) resultCounts() (map[string]int, []any) {
	counts := make(map[string]int, len(v.Results)+1)
	dests := make([]any, len(v.Results))
	for i, c := range v.Results {
		counts[c.Key] = 0
		dests[i] = new(int)
	}
	counts[viewOtherResultKey] = 0
	return counts, dests
}

// fillResults copies the scanned class counts in and sets "other" to whatever
// is left of the total: a result that is blank or not one of the known ones.
func (v logView) fillResults(counts map[string]int, dests []any, total int) {
	known := 0
	for i, c := range v.Results {
		counts[c.Key] = *dests[i].(*int)
		known += counts[c.Key]
	}
	counts[viewOtherResultKey] = total - known
}

// LogViewSummary: GET /api/screening-log/summary?test=&by=&from=&to=&q=. How
// many tests were done, for how many people, how they came out, and the
// busiest groups (company by default) with their own outcome counts.
func (a *API) LogViewSummary(w http.ResponseWriter, r *http.Request) {
	req, err := parseViewRequest(r)
	if err != nil {
		writeViewError(w, err)
		return
	}
	view := req.view
	by := r.URL.Query().Get("by")
	if by == "" {
		by = view.GroupOrder[0]
	}
	groupExpr, ok := view.Groups[by]
	if !ok {
		writeViewError(w, badView("by must be one of "+strings.Join(view.GroupOrder, ", ")))
		return
	}

	results, dests := view.resultCounts()
	out := viewSummary{Test: req.sheet.Key, Results: results, By: by, Groups: []viewGroup{}}
	err = a.DB.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT NULLIF(LOWER(TRIM(`+view.Name+`)), ''))`+view.resultSums()+`
		FROM `+req.sheet.Table+` d WHERE 1=1`+req.where, req.args...).
		Scan(append([]any{&out.Total, &out.People}, dests...)...)
	if err != nil {
		writeViewError(w, err)
		return
	}
	view.fillResults(out.Results, dests, out.Total)

	group := "TRIM(" + groupExpr + ")"
	rows, err := a.DB.Query(`
		SELECT `+group+`, COUNT(*)`+view.resultSums()+`
		FROM `+req.sheet.Table+` d WHERE 1=1`+req.where+`
		GROUP BY `+group+`
		ORDER BY COUNT(*) DESC, CASE WHEN `+group+` = '' THEN 1 ELSE 0 END, `+group, req.args...)
	if err != nil {
		writeViewError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var g viewGroup
		var groupDests []any
		g.Results, groupDests = view.resultCounts()
		if err := rows.Scan(append([]any{&g.Name, &g.Count}, groupDests...)...); err != nil {
			writeViewError(w, err)
			return
		}
		view.fillResults(g.Results, groupDests, g.Count)
		if len(out.Groups) < viewGroupLimit {
			out.Groups = append(out.Groups, g)
		} else {
			out.MoreGroups++
		}
	}
	if err := rows.Err(); err != nil {
		writeViewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
