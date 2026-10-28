package sofi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

// clockAt makes the API think it is now; the returned func moves the clock.
func clockAt(api *API, t time.Time) func(time.Time) {
	now := t
	api.now = func() time.Time { return now }
	return func(to time.Time) { now = to }
}

func get(h http.HandlerFunc, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: LangEN})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestClosedPublicRoutesAnswerClosed(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(1, 0, 0))

	rec := get(api.SignInPage, "/sofi")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "The questionnaire is closed") {
		t.Fatalf("sign-in page: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No opening date has been set yet") {
		t.Fatal("with nothing scheduled the page should say so")
	}
	for name, h := range map[string]http.HandlerFunc{"names": api.Names, "signin": api.SignIn, "check": api.Check} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/sofi/api/x?q=andi", strings.NewReader(`{"token":"dev-pass"}`)))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Kuesioner sedang ditutup") {
			t.Errorf("%s while closed: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestClosedPageShowsTheNextOpeningInGMT9(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(1, 0, 0))
	// 23:00 UTC on the 2nd is 08:00 on the 3rd in GMT+9.
	if _, err := api.CreateWindow("", at(2, 23, 0), at(5, 0, 0), "Admin", at(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	body := get(api.SignInPage, "/sofi").Body.String()
	if !strings.Contains(body, "2026-11-03 08:00 (GMT+9)") {
		t.Fatalf("closed page does not show the GMT+9 opening time: %s", body)
	}
}

// The closed page always tells people who to ask, in both languages, with or without a next
// opening date. It names no person or phone number: it is a public page.
func TestClosedPageAlwaysSaysWhoToAsk(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(1, 0, 0))
	check := func(when string) {
		for lang, want := range map[string]string{
			LangEN: "Questions? Ask the medical post.",
			LangID: "Ada pertanyaan? Tanyakan ke pos medis.",
		} {
			req := httptest.NewRequest(http.MethodGet, "/sofi", nil)
			req.AddCookie(&http.Cookie{Name: langCookie, Value: lang})
			rec := httptest.NewRecorder()
			api.SignInPage(rec, req)
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s, %s: no contact line %q", when, lang, want)
			}
		}
	}
	check("nothing scheduled")
	if _, err := api.CreateWindow("", at(5, 0, 0), at(6, 0, 0), "Admin", at(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	check("next window scheduled")
}

func TestSignInRecordsTheWindowAndSubmitSavesIt(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(2, 0, 0))
	id, _ := api.CreateWindow("Round 1", at(2, 1, 0), at(2, 9, 0), "Admin", at(1, 0, 0))
	move(at(2, 2, 0))
	c := signedInCookie(t, api)

	// The window ends by itself while the person fills in the form: they may still finish.
	move(at(2, 9, 30))
	if rec := get(api.Start, "/sofi/start", c); strings.Contains(rec.Body.String(), "The questionnaire is closed") || rec.Code != http.StatusOK {
		t.Fatalf("start after the window ran out: %d", rec.Code)
	}
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("submit after the window ran out: %d %s", rec.Code, rec.Body.String())
	}
	var got int64
	if err := api.DB.QueryRow(`SELECT FK_WindowID FROM SofiScreening`).Scan(&got); err != nil || got != id {
		t.Fatalf("FK_WindowID = %d, %v; want %d", got, err, id)
	}
}

func TestAdminClosingTheWindowStopsASignedInPerson(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(2, 0, 0))
	id, _ := api.CreateWindow("", at(2, 1, 0), at(2, 9, 0), "Admin", at(1, 0, 0))
	move(at(2, 2, 0))
	c := signedInCookie(t, api)
	if err := api.EndWindow(id, "Admin", at(2, 3, 0)); err != nil {
		t.Fatal(err)
	}
	move(at(2, 3, 5))
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusForbidden {
		t.Fatalf("submit after Close now: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(api.Start, "/sofi/start", c); !strings.Contains(rec.Body.String(), "The questionnaire is closed") {
		t.Fatal("the form was still served after Close now")
	}
	var n int
	_ = api.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows saved after Close now", n)
	}
}

func TestSessionFromBeforeWindowsNeedsAnOpenWindow(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(2, 0, 0))
	// A cookie issued before windows existed carries no window id.
	token, err := api.store.IssueFor(auth.Session{UserID: patientID(t, api, "Andi Pratama"), Name: "Andi", Role: roleSession}, sessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	old := &http.Cookie{Name: CookieName, Value: token}
	if rec := submit(api, old, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusForbidden {
		t.Fatalf("no window open: %d", rec.Code)
	}
	id, _ := api.CreateWindow("", at(2, 1, 0), at(2, 9, 0), "Admin", at(1, 0, 0))
	move(at(2, 2, 0))
	if rec := submit(api, old, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("window open: %d %s", rec.Code, rec.Body.String())
	}
	var got int64
	_ = api.DB.QueryRow(`SELECT FK_WindowID FROM SofiScreening`).Scan(&got)
	if got != id {
		t.Fatalf("FK_WindowID = %d, want %d", got, id)
	}
}

func adminReq(method, path, body, id string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if id != "" {
		req.SetPathValue("id", id)
	}
	return req.WithContext(middleware.ContextWithSession(context.Background(), &auth.Session{Name: "Dr Sari", Role: "admin"}))
}

func TestAdminAPICreatesListsAndEndsWindows(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))

	create := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.AdminCreateWindow(rec, adminReq(http.MethodPost, "/api/sofi-windows", body, ""))
		return rec
	}
	// Typed in GMT+9: 08:00 on the 3rd to 17:00 on the 5th.
	if rec := create(`{"label":"Round 1","opens":"2026-11-03T08:00","closes":"2026-11-05T17:00"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := create(`{"opens":"2026-11-04T08:00","closes":"2026-11-06T17:00"}`); rec.Code != http.StatusConflict {
		t.Errorf("overlap: %d", rec.Code)
	}
	if rec := create(`{"opens":"2026-11-08T08:00","closes":"2026-11-07T17:00"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("closes before opens: %d", rec.Code)
	}
	if rec := create(`{"opens":"soon","closes":"later"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad times: %d", rec.Code)
	}

	rec := httptest.NewRecorder()
	api.AdminWindows(rec, adminReq(http.MethodGet, "/api/sofi-windows", "", ""))
	var list []WindowView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	if list[0].Opens != "2026-11-03 08:00" || list[0].Closes != "2026-11-05 17:00" || list[0].CreatedBy != "Dr Sari" || list[0].State != StateUpcoming {
		t.Fatalf("window = %+v", list[0])
	}

	end := func(id string) int {
		rec := httptest.NewRecorder()
		api.AdminEndWindow(rec, adminReq(http.MethodPost, "/api/sofi-windows/"+id+"/end", "", id))
		return rec.Code
	}
	if code := end("1"); code != http.StatusOK {
		t.Errorf("cancel: %d", code)
	}
	if code := end("1"); code != http.StatusConflict {
		t.Errorf("cancel twice: %d", code)
	}
	if code := end("99"); code != http.StatusNotFound {
		t.Errorf("unknown: %d", code)
	}
	if code := end("abc"); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
}

func TestWindowStatusAndRounds(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(1, 0, 0))
	status := func() windowStatus {
		rec := httptest.NewRecorder()
		api.WindowStatus(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-windows/status", nil))
		var s windowStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := status(); s.State != "closed" || s.Zone != "GMT+9" || len(s.Rounds) != 0 {
		t.Fatalf("nothing scheduled: %+v", s)
	}
	past, _ := api.CreateWindow("Old", at(2, 0, 0), at(3, 0, 0), "A", at(1, 0, 0))
	cancelled, _ := api.CreateWindow("Never", at(6, 0, 0), at(7, 0, 0), "A", at(1, 0, 0))
	_ = api.EndWindow(cancelled, "A", at(1, 0, 0))
	_, _ = api.CreateWindow("Now", at(9, 0, 0), at(12, 0, 0), "A", at(1, 0, 0))
	_, _ = api.CreateWindow("Later", at(20, 0, 0), at(21, 0, 0), "A", at(1, 0, 0))

	move(at(5, 0, 0))
	if s := status(); s.State != "upcoming" || s.Opens != "2026-11-09 09:00" {
		t.Fatalf("before the next window: %+v", s)
	}
	move(at(10, 0, 0))
	s := status()
	if s.State != "open" || s.Until != "2026-11-12 09:00" {
		t.Fatalf("while open: %+v", s)
	}
	// Rounds: the old one and the open one; not the cancelled or the upcoming one.
	if len(s.Rounds) != 2 || s.Rounds[0].Label != "Now" || s.Rounds[1].ID != past {
		t.Fatalf("rounds = %+v", s.Rounds)
	}
}

func TestResultsCanBeFilteredByRound(t *testing.T) {
	api := newTestAPI(t)
	var w1, w2 int64
	for _, v := range []struct {
		dst  *int64
		from string
	}{{&w1, "2026-11-02T00:00:00"}, {&w2, "2026-11-10T00:00:00"}} {
		res, err := api.DB.Exec(`INSERT INTO SofiWindow (Label, OpensAt, ClosesAt, CreatedAt) VALUES ('', ?, '2026-12-31T00:00:00', ?)`, v.from, v.from)
		if err != nil {
			t.Fatal(err)
		}
		*v.dst, _ = res.LastInsertId()
	}
	andi := patientID(t, api, "Andi Pratama")
	score, _ := Score(answers(1))
	for _, w := range []int64{0, w1, w1, w2} {
		if err := api.saveAt(andi, Profile{}, 12, answers(1), score, "2026-11-11T00:00:00", w); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string) int {
		rec := httptest.NewRecorder()
		api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log?"+query, nil))
		var recs []Record
		_ = json.Unmarshal(rec.Body.Bytes(), &recs)
		return len(recs)
	}
	id1, id2 := strconv.FormatInt(w1, 10), strconv.FormatInt(w2, 10)
	for q, want := range map[string]int{"": 4, "window=" + id1: 2, "window=" + id2: 1, "window=none": 1} {
		if got := count(q); got != want {
			t.Errorf("list ?%s = %d, want %d", q, got, want)
		}
	}
	if _, s := getSummary(t, api, "window="+id1); s.Total != 2 {
		t.Errorf("summary for round 1 total = %d", s.Total)
	}
	if code, _ := getSummary(t, api, "window=abc"); code != http.StatusBadRequest {
		t.Errorf("bad window: %d", code)
	}
}
