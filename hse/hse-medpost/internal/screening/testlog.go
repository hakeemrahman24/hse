package screening

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// logColumn is one column of a test log. Expr is a SQL expression on the
// row alias d (the submission) or s (the uploading staff member). Expressions
// are written here, never taken from a request, so building the query from
// them is safe.
type logColumn struct {
	Header string
	Expr   string
}

// logSheet describes one kind of test. The same definition drives the
// on-screen table (GET /api/test-log) and the Excel sheet, so the two never
// drift apart.
type logSheet struct {
	Key   string
	Title string
	// TabLabel is the exact text shown on the program's "Test log" tab
	// button, e.g. "Alcohol test log". Kept separate from Title (used for
	// the Excel sheet name) so the two can read differently.
	TabLabel string
	// AdminLabel describes this kind's *data source* for the admin's "Test
	// log kind" picker, e.g. "Drug & Alcohol Testing data" — deliberately
	// not just "Alcohol", since a program choosing this kind (e.g. "Camp
	// Hygiene") has nothing to do with alcohol as a subject; it's picking
	// which existing submission table backs its tab, not a category that
	// needs to semantically match the program's name.
	AdminLabel string
	Table      string
	Columns    []logColumn
}

var logSheets = []logSheet{
	{
		Key:        "alcohol",
		Title:      "Alcohol",
		TabLabel:   "Alcohol test log",
		AdminLabel: "Drug & Alcohol Testing data",
		Table:      "DrugAlcoholScreening",
		Columns: []logColumn{
			// Record info
			{"Submitted", "d.SubmittedAt"},
			{"Test date", "d.CreatedAt"},
			{"Form version", "d.FormVersion"},
			// Donor
			{"Name", "d.DonorName"},
			{"NIK", "d.DonorNik"},
			{"Company", "d.DonorCompany"},
			{"Position", "d.DonorPosition"},
			{"Consent date", "d.ConsentAt"},
			{"Test type", "d.TestType"},
			// Medication
			{"No medication", "d.MedNone"},
			{"Medicine 1 name", "d.Med1Name"},
			{"Medicine 1 dose / freq.", "d.Med1Dose"},
			{"Medicine 1 reason", "d.Med1Reason"},
			{"Medicine 1 prescribed", "d.Med1Prescribed"},
			{"Medicine 2 name", "d.Med2Name"},
			{"Medicine 2 dose / freq.", "d.Med2Dose"},
			{"Medicine 2 reason", "d.Med2Reason"},
			{"Medicine 2 prescribed", "d.Med2Prescribed"},
			// Breath test
			{"Breath device", "d.BreathDevice"},
			{"Calibration until", "d.BreathCalibrationUntil"},
			{"Breath 1 time", "d.Breath1Time"},
			{"Breath 1 reading", "d.Breath1Reading"},
			{"Breath 1 mouthpiece sealed", "d.Breath1MouthpieceSealed"},
			{"Breath 1 result", "d.Breath1Result"},
			{"Breath 2 time", "d.Breath2Time"},
			{"Breath 2 reading", "d.Breath2Reading"},
			{"Breath 2 mouthpiece sealed", "d.Breath2MouthpieceSealed"},
			{"Breath 2 result", "d.Breath2Result"},
			// Specimen
			{"Specimen temperature OK", "d.SpecimenTempOk"},
			{"Specimen colour OK", "d.SpecimenColourOk"},
			{"Specimen temperature", "d.SpecimenTempValue"},
			// Panels
			{"Negative panel", "d.PanelNegative"},
			{"Non-negative panel", "d.PanelNonnegative"},
			// Result
			{"Final result", "d.FinalResult"},
			{"Remarks", "d.Remarks"},
			// Signatures
			{"Donor signed name", "d.DonorSignName"},
			{"Witness name", "d.WitnessName"},
			{"Collector name", "d.CollectorName"},
			{"Security name", "d.SecurityName"},
			{"Acknowledged by", "d.AckName"},
			// Record owner
			{"Uploaded by", "s.StaffName"},
		},
	},
	{
		Key:        "fatigue",
		Title:      "Fatigue",
		TabLabel:   "Fatigue log",
		AdminLabel: "Fatigue Scoring data",
		Table:      "FatigueScreening",
		Columns: []logColumn{
			{"Submitted", "d.SubmittedAt"},
			{"Created at", "d.CreatedAt"},
			{"Examiner", "d.Examiner"},
			{"Name", "d.WorkerName"},
			{"Company", "d.WorkerCompany"},
			{"Position", "d.WorkerPosition"},
			{"Exam date", "d.ExamDate"},
			{"Exam time", "d.ExamTime"},
			{"BP systolic", "d.BpSystolic"},
			{"BP diastolic", "d.BpDiastolic"},
			{"Pulse", "d.Pulse"},
			{"Score 24h", "d.Score24h"},
			{"Score 48h", "d.Score48h"},
			{"Score awake", "d.ScoreAwake"},
			{"Score total", "d.ScoreTotal"},
			{"Risk level", "d.RiskLevel"},
			{"Risk points", "d.RiskPoints"},
			{"Remarks", "d.Remarks"},
			{"Form version", "d.FormVersion"},
			{"Uploaded by", "s.StaffName"},
		},
	},
}

const (
	logTimeLayout = "2006-01-02T15:04:05"
	// Open-ended bounds used when the page sends no date filter.
	logFromOpen = "0001-01-01T00:00:00"
	logToOpen   = "9999-12-31T23:59:59"
)

func findLogSheet(key string) (logSheet, bool) {
	for _, s := range logSheets {
		if s.Key == key {
			return s, true
		}
	}
	return logSheet{}, false
}

// queryLogWhere returns one test's rows for a WHERE condition on the submission
// alias d (and the staff alias s), in the given order, at most limit of them
// (0 means all). Every column is a text column and may be NULL after the LEFT
// JOIN, so each value is read as NullString and blanks come back as "". The
// condition and order are written in this package, never taken from a request;
// only the args carry request values.
func queryLogWhere(db *sql.DB, sheet logSheet, where string, args []any, order string, limit int) ([]string, [][]string, error) {
	headers := make([]string, len(sheet.Columns))
	exprs := make([]string, len(sheet.Columns))
	for i, c := range sheet.Columns {
		headers[i] = c.Header
		exprs[i] = c.Expr
	}
	query := fmt.Sprintf(`
		SELECT %s
		FROM %s d
		LEFT JOIN MedpostStaffTable s ON s.StaffID = d.FK_StaffID
		WHERE %s
		ORDER BY %s`,
		strings.Join(exprs, ", "), sheet.Table, where, order)
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := [][]string{}
	for rows.Next() {
		vals := make([]sql.NullString, len(exprs))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i] = v.String
		}
		out = append(out, row)
	}
	return headers, out, rows.Err()
}

// exportFilename names the workbook after its test and the download time, e.g.
// "fatigue test log - 17102026-143005.xlsx". A workbook with every test in it
// is just "test log - ...".
func exportFilename(sheets []logSheet, t time.Time) string {
	name := "test log"
	if len(sheets) == 1 {
		name = strings.ToLower(sheets[0].Title) + " test log"
	}
	return fmt.Sprintf("%s - %s.xlsx", name, t.Format("02012006-150405"))
}

// ListAlcoholSubmissions: GET /api/screening/submissions. A short lookup
// list (donor name, date, result) for the "link to a test" picker on a
// locked sub-header's entry — e.g. attaching a scanned Consent + Result
// form to the exact DrugAlcoholScreening row it documents. Any signed-in
// staff member; the route is wrapped in RequireAuth.
func (a *API) ListAlcoholSubmissions(w http.ResponseWriter, r *http.Request) {
	type submission struct {
		SubmissionID int64  `json:"submissionId"`
		DonorName    string `json:"donorName"`
		CreatedAt    string `json:"createdAt"`
		FinalResult  string `json:"finalResult"`
	}
	rows, err := a.DB.Query(`
		SELECT SubmissionID, DonorName, CreatedAt, FinalResult
		FROM DrugAlcoholScreening
		ORDER BY SubmittedAt DESC
		LIMIT 500`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []submission{}
	for rows.Next() {
		var s submission
		if err := rows.Scan(&s.SubmissionID, &s.DonorName, &s.CreatedAt, &s.FinalResult); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func writeLogSheet(f *excelize.File, name string, headers []string, rows [][]string) error {
	head := make([]any, len(headers))
	for i, h := range headers {
		head[i] = h
	}
	if err := f.SetSheetRow(name, "A1", &head); err != nil {
		return err
	}
	for i, row := range rows {
		vals := make([]any, len(row))
		for j, v := range row {
			vals[j] = v
		}
		if err := f.SetSheetRow(name, fmt.Sprintf("A%d", i+2), &vals); err != nil {
			return err
		}
	}
	return nil
}
