package sofi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/ratelimit"
)

// patientID looks a seeded patient up by name.
func patientID(t *testing.T, api *API, name string) int64 {
	t.Helper()
	var id int64
	if err := api.DB.QueryRow(`SELECT PatientID FROM Patient WHERE Name = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestProfileReadsAgeCompanyPositionAndLatestMcuStatus(t *testing.T) {
	api := newTestAPI(t)
	id := strconv.FormatInt(patientID(t, api, "Andi Pratama"), 10)
	for _, q := range []string{
		`INSERT INTO Company (Name) VALUES ('PT Contoh')`,
		`UPDATE Patient SET FK_CompanyID = (SELECT CompanyID FROM Company WHERE Name = 'PT Contoh'), WorkPosition = 'Welder' WHERE PatientID = ` + id,
		`INSERT INTO McuRecord (FK_PatientID, DateCheckUp, ResultCriteria, CheckUpStatus, Timestamp, ReviewDate, CheckUpID) VALUES (` + id + `, '2025-01-10', 'Unfit', 'Close', '2025-01-10T08:00:00', '2025-01-10', 'CHK1')`,
		`INSERT INTO McuRecord (FK_PatientID, DateCheckUp, ResultCriteria, CheckUpStatus, Timestamp, ReviewDate, CheckUpID) VALUES (` + id + `, '2026-03-01', 'FitWithNote', 'Close', '2026-03-01T08:00:00', '2026-03-01', 'CHK2')`,
		`INSERT INTO McuRecord (FK_PatientID, DateCheckUp, ResultCriteria, CheckUpStatus, Timestamp, ReviewDate, CheckUpID) VALUES (` + id + `, '2026-09-01', NULL, 'Open', '2026-09-01T08:00:00', '2026-09-01', 'CHK3')`,
	} {
		if _, err := api.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	api.now = func() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) }
	p, err := api.profile(patientID(t, api, "Andi Pratama"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Andi Pratama" || p.Company != "PT Contoh" || p.WorkPosition != "Welder" {
		t.Fatalf("profile = %+v", p)
	}
	if p.Age != 36 { // born 1990-05-14
		t.Fatalf("age = %d", p.Age)
	}
	if p.DateOfBirth != "14/05/1990" {
		t.Fatalf("dob = %q", p.DateOfBirth)
	}
	// The newest checkup has no result yet, so the latest one with a result is shown.
	if p.MCUReviewStatus != "Fit with note" {
		t.Fatalf("mcu status = %q", p.MCUReviewStatus)
	}
}

func TestProfileWithNoCheckup(t *testing.T) {
	api := newTestAPI(t)
	p, err := api.profile(patientID(t, api, "Andini Putri"))
	if err != nil {
		t.Fatal(err)
	}
	if p.MCUReviewStatus != noMcuStatus {
		t.Fatalf("mcu status = %q", p.MCUReviewStatus)
	}
}

// signedInCookie signs "Andi Pratama" in and returns the session cookie.
func signedInCookie(t *testing.T, api *API) *http.Cookie {
	t.Helper()
	got := searchNames(t, api, "andi%20p")
	rec := signIn(t, api, got[0].Ref, "1990-05-14")
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatalf("not signed in: %d %s", rec.Code, rec.Body.String())
	return nil
}

func submit(api *API, c *http.Cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/sofi/api/submit", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	api.Submit(rec, req)
	return rec
}

func answersJSON(v int) string {
	b, _ := json.Marshal(answers(v))
	return string(b)
}

func TestSubmitScoresOnTheServerAndSaves(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(4)+`,"total":0,"level":"Low"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var res Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	// A score or level sent by the browser is ignored: 25 answers of 4 is 100, Moderate.
	if res.Total != 100 || res.Level != LevelModerate {
		t.Fatalf("result = %+v", res)
	}
	var weeks, total int
	var level string
	err := api.DB.QueryRow(`SELECT WeeksOnSite, ScoreTotal, ResultLevel FROM SofiScreening WHERE FK_PatientID = ?`,
		patientID(t, api, "Andi Pratama")).Scan(&weeks, &total, &level)
	if err != nil || weeks != 12 || total != 100 || level != LevelModerate {
		t.Fatalf("row = %d %d %s err %v", weeks, total, level, err)
	}
}

func TestSubmitNeedsASignedInPerson(t *testing.T) {
	api := newTestAPI(t)
	if rec := submit(api, nil, `{"weeksOnSite":1,"answers":`+answersJSON(1)+`}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSubmitRejectsBadInput(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	bad := []string{
		`{"weeksOnSite":-1,"answers":` + answersJSON(1) + `}`,
		`{"weeksOnSite":0,"answers":` + answersJSON(1) + `}`,
		`{"weeksOnSite":1,"answers":` + answersJSON(1) + `}`,
		`{"weeksOnSite":55,"answers":` + answersJSON(1) + `}`,
		`{"weeksOnSite":5000,"answers":` + answersJSON(1) + `}`,
		`{"answers":` + answersJSON(1) + `}`,
		`{"weeksOnSite":3,"answers":[1,2,3]}`,
		`{"weeksOnSite":3,"answers":` + answersJSON(9) + `}`,
		`not json`,
	}
	for _, body := range bad {
		if rec := submit(api, c, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", body, rec.Code)
		}
	}
	var n int
	_ = api.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows saved from bad input", n)
	}
}

func TestSubmitIsRateLimitedPerPerson(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	body := `{"weeksOnSite":2,"answers":` + answersJSON(1) + `}`
	for i := 0; i < submitMax; i++ {
		if rec := submit(api, c, body); rec.Code != http.StatusOK {
			t.Fatalf("submit %d: %d", i, rec.Code)
		}
	}
	if rec := submit(api, c, body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}
}

func TestStaffListShowsSavedRecordsWithLevels(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	submit(api, c, `{"weeksOnSite":7,"answers":`+answersJSON(6)+`}`)
	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log?q=andi", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var rows []struct {
		Name        string `json:"name"`
		WeeksOnSite int    `json:"weeksOnSite"`
		Total       int    `json:"total"`
		Level       string `json:"level"`
		Dimensions  []struct {
			Score int    `json:"score"`
			Level string `json:"level"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Andi Pratama" || rows[0].WeeksOnSite != 7 || rows[0].Total != 150 || rows[0].Level != LevelHigh {
		t.Fatalf("rows = %+v", rows)
	}
	if len(rows[0].Dimensions) != 5 || rows[0].Dimensions[0].Score != 30 || rows[0].Dimensions[0].Level != LevelHigh {
		t.Fatalf("dimensions = %+v", rows[0].Dimensions)
	}
}

func TestWeeksOnSiteAllowsTwoToFiftyThreeAndMoreThanOneYear(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	for _, weeks := range []int{2, 3, 52, 53, WeeksOverYear} {
		api.lim = ratelimit.New() // the per-person submit limit is not what is tested here
		body := `{"weeksOnSite":` + strconv.Itoa(weeks) + `,"answers":` + answersJSON(1) + `}`
		if rec := submit(api, c, body); rec.Code != http.StatusOK {
			t.Fatalf("weeks %d refused: %d %s", weeks, rec.Code, rec.Body.String())
		}
	}
}

func TestStaffListFlagsMoreThanOneYear(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	submit(api, c, `{"weeksOnSite":`+strconv.Itoa(WeeksOverYear)+`,"answers":`+answersJSON(1)+`}`)
	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log", nil))
	var rows []struct {
		WeeksOnSite   int  `json:"weeksOnSite"`
		WeeksOverYear bool `json:"weeksOverYear"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v err %v", rows, err)
	}
	if !rows[0].WeeksOverYear {
		t.Fatalf("not flagged: %+v", rows[0])
	}
}

func TestFormOffersTheWeekChoices(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(c)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: LangEN})
	rec := httptest.NewRecorder()
	api.Start(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `<select id="weeks"`) {
		t.Fatal("weeks is not a choice list")
	}
	for _, want := range []string{`<option value="2">`, `<option value="53">`, `<option value="` + strconv.Itoa(WeeksOverYear) + `">More than 1 year`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, not := range []string{`<option value="0">`, `<option value="1">`, `<option value="54">53`} {
		if strings.Contains(body, not) {
			t.Fatalf("unexpected %q", not)
		}
	}
}
