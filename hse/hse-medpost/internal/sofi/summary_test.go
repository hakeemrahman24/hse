package sofi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// addSurvey saves one survey straight into the table: every answer is value, so each dimension
// scores 5*value and the total 25*value.
func addSurvey(t *testing.T, api *API, patientID int64, company, position, submittedAt string, value int) {
	t.Helper()
	res, err := Score(answers(value))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.saveAt(patientID, Profile{Company: company, WorkPosition: position, MCUReviewStatus: "Fit"}, 12, answers(value), res, submittedAt, 0); err != nil {
		t.Fatal(err)
	}
}

func getSummary(t *testing.T, api *API, query string) (int, summaryJSON) {
	t.Helper()
	rec := httptest.NewRecorder()
	api.StaffSummary(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log/summary?"+query, nil))
	var out summaryJSON
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad json: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

type summaryJSON struct {
	Total       int            `json:"total"`
	People      int            `json:"people"`
	Results     map[string]int `json:"results"`
	By          string         `json:"by"`
	MoreGroups  int            `json:"moreGroups"`
	WeeksOnSite map[string]struct {
		Average  float64 `json:"average"`
		Counted  int     `json:"counted"`
		OverYear int     `json:"overYear"`
	} `json:"weeksOnSite"`
	Dimensions []struct {
		Key     string         `json:"key"`
		Average float64        `json:"average"`
		Max     int            `json:"max"`
		Results map[string]int `json:"results"`
	} `json:"dimensions"`
	Groups []struct {
		Name    string         `json:"name"`
		Count   int            `json:"count"`
		Results map[string]int `json:"results"`
	} `json:"groups"`
}

func TestSummaryCountsLevelsPeopleAndDimensionAverages(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	andini := patientID(t, api, "Andini Putri")
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-01T08:00:00", 1) // total 25  Low, each dimension 5 Low
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-02T08:00:00", 3) // total 75  Moderate, 15 Moderate
	addSurvey(t, api, andini, "PT B", "Cook", "2026-10-03T08:00:00", 6) // total 150 High, 30 High
	code, s := getSummary(t, api, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if s.Total != 3 || s.People != 2 {
		t.Fatalf("total %d people %d", s.Total, s.People)
	}
	if s.Results["low"] != 1 || s.Results["moderate"] != 1 || s.Results["high"] != 1 {
		t.Fatalf("results = %v", s.Results)
	}
	if len(s.Dimensions) != 5 {
		t.Fatalf("dimensions = %d", len(s.Dimensions))
	}
	d := s.Dimensions[0]
	if d.Key != "lack_of_energy" || d.Max != 30 || d.Average != 16.7 { // (5+15+30)/3
		t.Fatalf("first dimension = %+v", d)
	}
	if d.Results["low"] != 1 || d.Results["moderate"] != 1 || d.Results["high"] != 1 {
		t.Fatalf("first dimension levels = %v", d.Results)
	}
}

func TestSummaryGroupsByCompanyOrPosition(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	andini := patientID(t, api, "Andini Putri")
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-01T08:00:00", 1)
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-02T08:00:00", 6)
	addSurvey(t, api, andini, "PT B", "Welder", "2026-10-03T08:00:00", 3)
	_, byCompany := getSummary(t, api, "by=company")
	if byCompany.By != "company" || len(byCompany.Groups) != 2 || byCompany.Groups[0].Name != "PT A" || byCompany.Groups[0].Count != 2 {
		t.Fatalf("by company = %+v", byCompany.Groups)
	}
	if byCompany.Groups[0].Results["low"] != 1 || byCompany.Groups[0].Results["high"] != 1 {
		t.Fatalf("PT A levels = %v", byCompany.Groups[0].Results)
	}
	_, byPosition := getSummary(t, api, "by=position")
	if len(byPosition.Groups) != 1 || byPosition.Groups[0].Name != "Welder" || byPosition.Groups[0].Count != 3 {
		t.Fatalf("by position = %+v", byPosition.Groups)
	}
}

// The days typed in From / To are GMT+9 days, the same zone the table shows, so October is
// 2026-09-30T15:00:00 UTC (inclusive) up to 2026-10-31T15:00:00 UTC (exclusive).
func TestDateFilterUsesTheGMT9Day(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	addSurvey(t, api, andi, "PT A", "Welder", "2026-09-30T14:59:59", 1) // 23:59:59 on 30 Sep in GMT+9: out
	addSurvey(t, api, andi, "PT A", "Welder", "2026-09-30T15:00:00", 1) // 00:00:00 on 1 Oct: in
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-31T14:59:59", 1) // 23:59:59 on 31 Oct: in
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-31T15:00:00", 1) // 00:00:00 on 1 Nov: out
	_, s := getSummary(t, api, "from=2026-10-01&to=2026-10-31")
	if s.Total != 2 {
		t.Fatalf("summary total in October = %d, want 2", s.Total)
	}
	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log?from=2026-10-01&to=2026-10-31", nil))
	var list []Record
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("list in October = %d, want 2", len(list))
	}
	// One open end: everything from the 1st on, or everything up to the 31st.
	if _, s := getSummary(t, api, "from=2026-10-01"); s.Total != 3 {
		t.Errorf("from only = %d, want 3", s.Total)
	}
	if _, s := getSummary(t, api, "to=2026-10-31"); s.Total != 3 {
		t.Errorf("to only = %d, want 3", s.Total)
	}
	for _, bad := range []string{"from=2026-13-01", "to=yesterday"} {
		if code, _ := getSummary(t, api, bad); code != http.StatusBadRequest {
			t.Errorf("summary ?%s = %d", bad, code)
		}
		rec := httptest.NewRecorder()
		api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log?"+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("list ?%s = %d", bad, rec.Code)
		}
	}
}

func TestSummaryWithNoSurveysIsEmptyNotAnError(t *testing.T) {
	api := newTestAPI(t)
	code, s := getSummary(t, api, "")
	if code != http.StatusOK || s.Total != 0 || s.People != 0 || len(s.Dimensions) != 5 || len(s.Groups) != 0 {
		t.Fatalf("code %d summary %+v", code, s)
	}
	for _, d := range s.Dimensions {
		if d.Average != 0 {
			t.Fatalf("average of nothing = %v", d.Average)
		}
	}
}

func TestSummaryRejectsAnUnknownGrouping(t *testing.T) {
	api := newTestAPI(t)
	if code, _ := getSummary(t, api, "by=examiner"); code != http.StatusBadRequest {
		t.Fatalf("status %d", code)
	}
}

func TestSummaryShowsOnlyTheBusiestGroups(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	for i := 0; i < summaryGroupLimit+3; i++ {
		addSurvey(t, api, andi, "Company "+strconv.Itoa(100+i), "Welder", "2026-10-01T08:00:00", 1)
	}
	_, s := getSummary(t, api, "by=company")
	if len(s.Groups) != summaryGroupLimit || s.MoreGroups != 3 {
		t.Fatalf("groups %d more %d", len(s.Groups), s.MoreGroups)
	}
}

// The average weeks on site of each result band leaves out "more than 1 year" (stored as a marker,
// not a number) and counts those people apart.
func TestSummaryAverageWeeksOnSiteByResult(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	andini := patientID(t, api, "Andini Putri")
	save := func(patient int64, value, weeks int) {
		t.Helper()
		res, err := Score(answers(value))
		if err != nil {
			t.Fatal(err)
		}
		if err := api.saveAt(patient, Profile{Company: "PT A", WorkPosition: "Welder", MCUReviewStatus: "Fit"}, weeks, answers(value), res, "2026-10-01T08:00:00", 0); err != nil {
			t.Fatal(err)
		}
	}
	save(andi, 1, 10)            // Low
	save(andini, 1, 15)          // Low
	save(andi, 1, WeeksOverYear) // Low, over a year: not in the average
	save(andini, 6, 30)          // High
	_, s := getSummary(t, api, "")
	low := s.WeeksOnSite["low"]
	if low.Average != 12.5 || low.Counted != 2 || low.OverYear != 1 {
		t.Errorf("low = %+v, want average 12.5 over 2 people and 1 over a year", low)
	}
	if high := s.WeeksOnSite["high"]; high.Average != 30 || high.Counted != 1 || high.OverYear != 0 {
		t.Errorf("high = %+v", high)
	}
	if mod := s.WeeksOnSite["moderate"]; mod.Average != 0 || mod.Counted != 0 {
		t.Errorf("moderate with no surveys = %+v, want zeros", mod)
	}
}
