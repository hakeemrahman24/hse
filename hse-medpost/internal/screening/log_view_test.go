package screening

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func addAlcohol(t *testing.T, db *sql.DB, staffID int64, id, createdAt, name, company, position, testType, result string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO DrugAlcoholScreening
		(ClientID, FK_StaffID, FormVersion, CreatedAt, SubmittedAt, DonorName, DonorCompany, DonorPosition, TestType, FinalResult, RawJSON)
		VALUES (?, ?, '20270228', ?, '2026-10-20T01:00:00', ?, ?, ?, ?, ?, '{}')`,
		id, staffID, createdAt, name, company, position, testType, result)
	if err != nil {
		t.Fatal(err)
	}
}

func addFatigue(t *testing.T, db *sql.DB, staffID int64, id, createdAt, examDate, name, company, position, risk string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO FatigueScreening
		(ClientID, FK_StaffID, FormVersion, CreatedAt, SubmittedAt, ExamDate, WorkerName, WorkerCompany, WorkerPosition, Examiner, ScoreTotal, RiskLevel, RawJSON)
		VALUES (?, ?, '20271006', ?, '2026-10-20T01:00:00', ?, ?, ?, ?, 'Dr A', '4', ?, '{}')`,
		id, staffID, createdAt, examDate, name, company, position, risk)
	if err != nil {
		t.Fatal(err)
	}
}

type viewList struct {
	Test    string     `json:"test"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

func listView(t *testing.T, api *API, query string) (int, viewList) {
	t.Helper()
	rec := getTestLog(t, api.LogViewList, "/api/screening-log?"+query)
	var out viewList
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func names(out viewList) string {
	var got []string
	for _, row := range out.Rows {
		got = append(got, row[indexOf(out.Headers, "Name")])
	}
	return strings.Join(got, ",")
}

// The alcohol test date is CreatedAt, stored in UTC; the clinic's day starts
// nine hours earlier (Asia/Jayapura), so a test at 16:00 UTC on the 1st is the
// morning of the 2nd for the clinic.
func TestLogViewAlcoholDatesAreClinicDays(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addAlcohol(t, database, staffID, "a1", "2026-10-01T16:00:00", "Late", "PT A", "Driver", "random", "neg")
	addAlcohol(t, database, staffID, "a2", "2026-10-01T14:59:59", "Early", "PT A", "Driver", "random", "neg")

	_, onSecond := listView(t, api, "test=alcohol&from=2026-10-02&to=2026-10-02")
	if names(onSecond) != "Late" {
		t.Errorf("clinic day 2026-10-02 = %q, want Late (01:00 local)", names(onSecond))
	}
	_, onFirst := listView(t, api, "test=alcohol&from=2026-10-01&to=2026-10-01")
	if names(onFirst) != "Early" {
		t.Errorf("clinic day 2026-10-01 = %q, want Early (23:59 local)", names(onFirst))
	}
	_, all := listView(t, api, "test=alcohol")
	if names(all) != "Late,Early" {
		t.Errorf("no dates = %q, want newest first: Late,Early", names(all))
	}
}

// The fatigue test date is the exam date typed on the form, falling back to the
// day the record was created when it was left blank.
func TestLogViewFatigueUsesExamDateElseCreated(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addFatigue(t, database, staffID, "f1", "2026-10-09T08:00:00", "2026-10-05", "Typed", "PT A", "Driver", "Low")
	addFatigue(t, database, staffID, "f2", "2026-10-06T08:00:00", "", "Blank", "PT A", "Driver", "Low")

	_, got := listView(t, api, "test=fatigue&from=2026-10-05&to=2026-10-06")
	if names(got) != "Blank,Typed" {
		t.Errorf("5th..6th = %q, want Blank,Typed (exam date 5th, created 6th)", names(got))
	}
	_, got = listView(t, api, "test=fatigue&from=2026-10-09&to=2026-10-09")
	if len(got.Rows) != 0 {
		t.Errorf("the typed exam date wins over the created day: %q", names(got))
	}
}

func TestLogViewSearchesByNameWords(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addAlcohol(t, database, staffID, "a1", "2026-10-01T01:00:00", "Budi Santoso", "PT A", "", "random", "neg")
	addAlcohol(t, database, staffID, "a2", "2026-10-01T02:00:00", "Siti 50% Aminah", "PT A", "", "random", "neg")
	addFatigue(t, database, staffID, "f1", "2026-10-01T01:00:00", "", "Budi Hartono", "PT A", "", "Low")

	_, got := listView(t, api, "test=alcohol&q=SANTOSO+bud")
	if names(got) != "Budi Santoso" {
		t.Errorf("words in any order and case = %q", names(got))
	}
	_, got = listView(t, api, "test=alcohol&q=50%25")
	if names(got) != "Siti 50% Aminah" {
		t.Errorf("a typed %% matches only itself = %q", names(got))
	}
	_, got = listView(t, api, "test=alcohol&q=%25")
	if len(got.Rows) != 1 {
		t.Errorf("a lone %% is not a wildcard: %q", names(got))
	}
	_, got = listView(t, api, "test=fatigue&q=budi")
	if names(got) != "Budi Hartono" {
		t.Errorf("fatigue searches WorkerName = %q", names(got))
	}
}

func TestLogViewRejectsBadRequests(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	for _, q := range []string{"test=nope", "", "test=alcohol&from=2026-13-40", "test=alcohol&to=abc"} {
		if code, _ := listView(t, api, q); code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, code)
		}
	}
	for _, q := range []string{"test=alcohol&by=examiner", "test=fatigue&by=testType", "test=fatigue&by=zzz", "test=nope", "test=alcohol&from=bad"} {
		rec := getTestLog(t, api.LogViewSummary, "/api/screening-log/summary?"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("summary %q: status %d, want 400", q, rec.Code)
		}
	}
}

func TestLogViewExportFollowsTheFiltersAndHasEveryColumn(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addAlcohol(t, database, staffID, "a1", "2026-10-01T01:00:00", "Budi", "PT A", "", "random", "neg")
	addAlcohol(t, database, staffID, "a2", "2026-10-05T01:00:00", "Siti", "PT A", "", "random", "pos")

	read := func(query string) [][]string {
		rec := getTestLog(t, api.LogViewExport, "/api/screening-log.xlsx?"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		book, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		defer book.Close()
		rows, err := book.GetRows(book.GetSheetName(0))
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	all := read("test=alcohol")
	sheet, _ := findLogSheet("alcohol")
	if len(all) != 3 || len(all[0]) != len(sheet.Columns) {
		t.Fatalf("unfiltered: %d rows of %d columns, want header + 2 rows of %d", len(all), len(all[0]), len(sheet.Columns))
	}
	one := read("test=alcohol&from=2026-10-05&q=siti")
	if len(one) != 2 || one[1][indexOf(one[0], "Name")] != "Siti" {
		t.Errorf("filtered export = %v", one)
	}
}

type viewSummaryBody struct {
	Test    string         `json:"test"`
	Total   int            `json:"total"`
	People  int            `json:"people"`
	Results map[string]int `json:"results"`
	By      string         `json:"by"`
	Groups  []struct {
		Name    string         `json:"name"`
		Count   int            `json:"count"`
		Results map[string]int `json:"results"`
	} `json:"groups"`
	MoreGroups int     `json:"moreGroups"`
	Weeks      float64 `json:"weeks"`
}

func summaryView(t *testing.T, api *API, query string) viewSummaryBody {
	t.Helper()
	rec := getTestLog(t, api.LogViewSummary, "/api/screening-log/summary?"+query)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out viewSummaryBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLogViewAlcoholSummary(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	// The form stores neg/pos/lab; older or hand-made rows may say negative.
	addAlcohol(t, database, staffID, "a1", "2026-10-01T01:00:00", "Budi", "PT A", "Driver", "random", "neg")
	addAlcohol(t, database, staffID, "a2", "2026-10-02T01:00:00", "Budi", "PT A", "Driver", "pre", "NEGATIVE")
	addAlcohol(t, database, staffID, "a3", "2026-10-03T01:00:00", "Siti", "PT A", "Cook", "random", "pos")
	addAlcohol(t, database, staffID, "a4", "2026-10-04T01:00:00", "Andi", "PT B", "Cook", "random", "lab")
	addAlcohol(t, database, staffID, "a5", "2026-10-05T01:00:00", "Rina", "", "Cook", "post", "")

	s := summaryView(t, api, "test=alcohol")
	if s.Total != 5 || s.People != 4 || s.By != "company" {
		t.Errorf("total %d people %d by %q, want 5 4 company", s.Total, s.People, s.By)
	}
	want := map[string]int{"negative": 2, "positive": 1, "lab": 1, "other": 1}
	for k, n := range want {
		if s.Results[k] != n {
			t.Errorf("results[%s] = %d, want %d (%v)", k, s.Results[k], n, s.Results)
		}
	}
	if len(s.Groups) != 3 || s.Groups[0].Name != "PT A" || s.Groups[0].Count != 3 ||
		s.Groups[0].Results["negative"] != 2 || s.Groups[0].Results["positive"] != 1 {
		t.Errorf("groups by company = %+v", s.Groups)
	}
	if s.Groups[len(s.Groups)-1].Name != "" || s.Groups[len(s.Groups)-1].Count != 1 {
		t.Errorf("a blank company is its own group with an empty name: %+v", s.Groups)
	}
	byType := summaryView(t, api, "test=alcohol&by=testType")
	if byType.By != "testType" || byType.Groups[0].Name != "random" || byType.Groups[0].Count != 3 {
		t.Errorf("by test type = %+v", byType.Groups)
	}
	dated := summaryView(t, api, "test=alcohol&from=2026-10-03&to=2026-10-04")
	if dated.Total != 2 || dated.Results["positive"] != 1 || dated.Results["lab"] != 1 {
		t.Errorf("dated = %+v", dated)
	}
}

func TestLogViewFatigueSummary(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addFatigue(t, database, staffID, "f1", "2026-10-01T01:00:00", "2026-10-01", "Budi", "PT A", "Driver", "Low")
	addFatigue(t, database, staffID, "f2", "2026-10-02T01:00:00", "2026-10-02", "Siti", "PT A", "Cook", "high")
	addFatigue(t, database, staffID, "f3", "2026-10-03T01:00:00", "2026-10-03", "Siti", "PT B", "Cook", "Medium")
	addFatigue(t, database, staffID, "f4", "2026-10-04T01:00:00", "2026-10-04", "Rina", "PT B", "Cook", "")

	s := summaryView(t, api, "test=fatigue&by=position")
	if s.Total != 4 || s.People != 3 || s.By != "position" {
		t.Errorf("total %d people %d by %q", s.Total, s.People, s.By)
	}
	want := map[string]int{"low": 1, "mild": 0, "medium": 1, "high": 1, "other": 1}
	for k, n := range want {
		if got, ok := s.Results[k]; !ok || got != n {
			t.Errorf("results[%s] = %d (present %v), want %d", k, got, ok, n)
		}
	}
	if s.Groups[0].Name != "Cook" || s.Groups[0].Count != 3 || s.Groups[0].Results["high"] != 1 {
		t.Errorf("groups = %+v", s.Groups)
	}
	byExaminer := summaryView(t, api, "test=fatigue&by=examiner")
	if byExaminer.Groups[0].Name != "Dr A" || byExaminer.Groups[0].Count != 4 {
		t.Errorf("by examiner = %+v", byExaminer.Groups)
	}
}

func TestLogViewSummaryWithNoDataHasZeroedResults(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	for test, keys := range map[string][]string{
		"alcohol": {"negative", "positive", "lab", "other"},
		"fatigue": {"low", "mild", "medium", "high", "other"},
	} {
		s := summaryView(t, api, "test="+test)
		if s.Total != 0 || s.People != 0 || len(s.Groups) != 0 {
			t.Errorf("%s: %+v", test, s)
		}
		for _, k := range keys {
			if n, ok := s.Results[k]; !ok || n != 0 {
				t.Errorf("%s results[%s] = %d (present %v), want 0", test, k, n, ok)
			}
		}
	}
}

// The dashboard's weekly average needs the number of weeks the tests span: first
// to last test day, both included, never less than one week.
func TestLogViewFatigueSummaryWeeks(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	addFatigue(t, database, staffID, "w1", "2026-10-01T01:00:00", "2026-10-01", "Budi", "PT A", "Driver", "Low")
	if got := summaryView(t, api, "test=fatigue").Weeks; got != 1 {
		t.Errorf("one day spans %v weeks, want 1 (the minimum)", got)
	}
	addFatigue(t, database, staffID, "w2", "2026-10-21T01:00:00", "2026-10-21", "Siti", "PT A", "Cook", "High")
	if got := summaryView(t, api, "test=fatigue").Weeks; got != 3 {
		t.Errorf("21 days span %v weeks, want 3", got)
	}
	if got := summaryView(t, api, "test=fatigue&from=2030-01-01").Weeks; got != 0 {
		t.Errorf("no matching tests gave %v weeks, want 0", got)
	}
	if got := summaryView(t, api, "test=alcohol").Weeks; got != 0 {
		t.Errorf("alcohol has no plain date, got %v weeks, want 0", got)
	}
}
