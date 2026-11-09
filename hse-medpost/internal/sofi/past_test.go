package sofi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func startPage(t *testing.T, api *API, lang string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(signedInCookie(t, api))
	req.AddCookie(&http.Cookie{Name: langCookie, Value: lang})
	rec := httptest.NewRecorder()
	api.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// A person who signs in again sees their own earlier results, newest first, in GMT+9, and nobody
// else's.
func TestStartListsThePersonsEarlierResultsOnly(t *testing.T) {
	api := newTestAPI(t)
	me, other := patientID(t, api, "Andi Pratama"), patientID(t, api, "Andini Putri")
	addSurvey(t, api, me, "PT A", "Welder", "2026-10-20T00:00:00", 1) // 2026-10-20 09:00 GMT+9, Low
	addSurvey(t, api, me, "PT A", "Welder", "2026-10-27T15:30:00", 5) // 2026-10-28 00:30 GMT+9, High
	addSurvey(t, api, other, "PT B", "Cook", "2026-10-22T00:00:00", 3)

	body := startPage(t, api, LangEN)
	for _, want := range []string{"Your earlier results", "2026-10-20 09:00", "2026-10-28 00:30"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "2026-10-22 09:00") {
		t.Error("page shows another person's result")
	}
	if strings.Index(body, "2026-10-28 00:30") > strings.Index(body, "2026-10-20 09:00") {
		t.Error("newest result is not first")
	}
	// scores come from Score() on the stored answers: every answer 5 on 20 items is total 100 of 120
	if !strings.Contains(body, "100 / 120") {
		t.Error("page lacks the total score of the second result")
	}
}

func TestStartHasNoEarlierResultsBlockForAFirstTimer(t *testing.T) {
	api := newTestAPI(t)
	if body := startPage(t, api, LangEN); strings.Contains(body, "Your earlier results") {
		t.Error("shows the block with nothing to show")
	}
}

func TestEarlierResultsAreTranslated(t *testing.T) {
	api := newTestAPI(t)
	addSurvey(t, api, patientID(t, api, "Andi Pratama"), "PT A", "Welder", "2026-10-20T00:00:00", 1)
	body := startPage(t, api, LangID)
	if strings.Contains(body, "Your earlier results") || !strings.Contains(body, "Hasil Anda sebelumnya") {
		t.Error("heading is not in Indonesian")
	}
}

// One person, one questionnaire per test period.
func TestASecondSubmitInTheSamePeriodIsRefused(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(1)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("first submit: %d %s", rec.Code, rec.Body.String())
	}
	rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(5)+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second submit: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening WHERE FK_PatientID = ?`, patientID(t, api, "Andi Pratama")).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d err %v", n, err)
	}
}

func TestAnotherPersonMaySubmitInThePeriod(t *testing.T) {
	api := newTestAPI(t)
	if rec := submit(api, signedInCookie(t, api), `{"weeksOnSite":12,"answers":`+answersJSON(1)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got := searchNames(t, api, "andini")
	rec := signIn(t, api, got[0].Ref, "1988-01-02")
	var c *http.Cookie
	for _, k := range rec.Result().Cookies() {
		if k.Name == CookieName {
			c = k
		}
	}
	if c == nil {
		t.Fatalf("not signed in: %d", rec.Code)
	}
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(1)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

// After submitting, signing in again shows the earlier result and no form.
func TestStartAfterSubmittingShowsNoFormForThePeriod(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(1)+`}`)
	req := httptest.NewRequest(http.MethodGet, "/sofi/start", nil)
	req.AddCookie(c)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: LangEN})
	rec := httptest.NewRecorder()
	api.Start(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `id="sofi-form"`) {
		t.Error("the form is still offered")
	}
	if !strings.Contains(body, "already completed") || !strings.Contains(body, "Your earlier results") {
		t.Error("no message or earlier result")
	}
}

// Taps arriving together must save one questionnaire, not several.
func TestConcurrentSubmitsSaveOnlyOne(t *testing.T) {
	api := newTestAPI(t)
	c := signedInCookie(t, api)
	body := `{"weeksOnSite":12,"answers":` + answersJSON(1) + `}`
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = submit(api, c, body).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		}
	}
	var n int
	_ = api.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening`).Scan(&n)
	if ok != 1 || n != 1 {
		t.Fatalf("%d accepted, %d rows saved; codes %v", ok, n, codes)
	}
}

// The database refuses a second questionnaire for the same person and window even if the in-memory
// lock is bypassed (a second server process); old rows without a window are not affected.
func TestDatabaseRefusesADuplicatePersonAndWindow(t *testing.T) {
	api := newTestAPI(t)
	me := patientID(t, api, "Andi Pratama")
	res, _ := Score(answers(1))
	save := func(win int64) error {
		return api.saveAt(me, Profile{}, 12, answers(1), res, "2026-10-20T00:00:00", win)
	}
	if err := save(1); err != nil {
		t.Fatal(err)
	}
	err := save(1)
	if err == nil || !isDuplicateKey(err) {
		t.Fatalf("second row for the same window: err = %v", err)
	}
	if err := save(0); err != nil || save(0) != nil {
		t.Fatalf("rows without a window must stay allowed: %v", err)
	}
}

func TestIsDuplicateKeyRecognisesBothDatabases(t *testing.T) {
	for _, msg := range []string{"UNIQUE constraint failed: SofiScreening.FK_PatientID", "Error 1062 (23000): Duplicate entry '5-2' for key 'uq_sofi_one_per_window'"} {
		if !isDuplicateKey(errors.New(msg)) {
			t.Errorf("not recognised: %s", msg)
		}
	}
	if isDuplicateKey(errors.New("disk full")) || isDuplicateKey(nil) {
		t.Error("recognised something that is not a duplicate")
	}
}

// The staff table shows at most listLimit rows; it says how many there are in all.
func TestStaffListReportsWhenItShowsOnlyTheFirstRows(t *testing.T) {
	api := newTestAPI(t)
	me := patientID(t, api, "Andi Pratama")
	res, _ := Score(answers(1))
	for i := 0; i < listLimit+1; i++ { // window 0 (before windows) allows several rows per person
		if err := api.saveAt(me, Profile{}, 12, answers(1), res, "2026-10-20T00:00:00", 0); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log", nil))
	var rows []Record
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != listLimit || rec.Header().Get("X-Total-Count") != strconv.Itoa(listLimit+1) {
		t.Fatalf("%d rows, X-Total-Count %q", len(rows), rec.Header().Get("X-Total-Count"))
	}
	// a search that matches nobody is not a truncation
	rec = httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log?q=nobody", nil))
	if rec.Header().Get("X-Total-Count") != "0" {
		t.Fatalf("X-Total-Count %q", rec.Header().Get("X-Total-Count"))
	}
}

// A result saved by the earlier 25-word form shows on the new 20-item scale in the person's own
// list and in the staff table: the five dropped words are not counted, and the maximum is 120.
func TestEarlier25WordResultsShowOnTheNewScale(t *testing.T) {
	api := newTestAPI(t)
	me := patientID(t, api, "Andi Pratama")
	old := make([]int, legacyItemCount)
	for i := range old {
		old[i] = 2
	}
	for pos := range legacyDropped {
		old[pos] = 6 // the dropped words must not add to the score
	}
	res, err := Score(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.saveAt(me, Profile{Company: "PT A", WorkPosition: "Welder", MCUReviewStatus: "Fit"}, 12, old, res, "2026-10-20T00:00:00", 0); err != nil {
		t.Fatal(err)
	}

	if body := startPage(t, api, LangEN); !strings.Contains(body, "40 / 120") {
		t.Error("the person's earlier result is not 40 / 120")
	}
	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log", nil))
	var rows []struct {
		Total    int    `json:"total"`
		TotalMax int    `json:"totalMax"`
		Level    string `json:"level"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err %v", rec.Body.String(), err)
	}
	if rows[0].Total != 40 || rows[0].TotalMax != 120 || rows[0].Level != LevelLow {
		t.Errorf("staff row = %+v, want 40 of 120, Low", rows[0])
	}
}
