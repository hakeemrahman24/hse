package sofi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setPreviewVia(api *API, on string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	api.AdminPreview(rec, adminReq(http.MethodPost, "/api/sofi-preview", `{"on":`+on+`}`, ""))
	return rec
}

func savedRows(t *testing.T, api *API) int {
	t.Helper()
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPreviewOpensTheFormButSavesNothing(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(2, 0, 0))

	if rec := get(api.SignInPage, "/sofi"); !strings.Contains(rec.Body.String(), "The questionnaire is closed") {
		t.Fatal("without a window or preview /sofi must be closed")
	}
	if rec := setPreviewVia(api, "true"); rec.Code != http.StatusOK {
		t.Fatalf("turn on: %d %s", rec.Code, rec.Body.String())
	}
	rec := get(api.SignInPage, "/sofi")
	if strings.Contains(rec.Body.String(), "The questionnaire is closed") || !strings.Contains(rec.Body.String(), "Preview: your answers are not saved.") {
		t.Fatalf("preview sign-in page should be open with the banner: %s", rec.Body.String())
	}

	c := signedInCookie(t, api)
	if rec := get(api.Start, "/sofi/start", c); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Preview: your answers are not saved.") {
		t.Fatalf("start in preview: %d", rec.Code)
	}
	// Twice, to show the one-per-period rule does not apply to a preview.
	for i := 0; i < 2; i++ {
		rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(4)+`}`)
		var out struct {
			Total   int  `json:"total"`
			Preview bool `json:"preview"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Total != 80 || !out.Preview {
			t.Fatalf("preview submit %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if n := savedRows(t, api); n != 0 {
		t.Fatalf("preview saved %d rows", n)
	}
}

func TestPreviewTurnsItselfOffAndOnRequest(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(2, 0, 0))
	setPreviewVia(api, "true")
	c := signedInCookie(t, api)

	move(at(2, 0, 0).Add(previewTTL + time.Minute))
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusForbidden {
		t.Fatalf("after the time limit: %d", rec.Code)
	}

	move(at(3, 0, 0))
	setPreviewVia(api, "true")
	setPreviewVia(api, "false")
	if rec := get(api.SignInPage, "/sofi"); !strings.Contains(rec.Body.String(), "The questionnaire is closed") {
		t.Fatal("closing the preview should close /sofi")
	}
}

func TestPreviewCannotStartWhileAWindowIsOpenAndRealWindowWins(t *testing.T) {
	api := newBareTestAPI(t)
	move := clockAt(api, at(2, 0, 0))
	setPreviewVia(api, "true")
	c := signedInCookie(t, api) // signed in under preview

	id, _ := api.CreateWindow("Round 1", at(2, 1, 0), at(2, 9, 0), "Admin", at(1, 0, 0))
	move(at(2, 2, 0))
	if rec := setPreviewVia(api, "true"); rec.Code != http.StatusConflict {
		t.Fatalf("preview during a window: %d", rec.Code)
	}
	// The window is now open, so the same person's answers are recorded for real.
	if rec := submit(api, c, `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	var got int64
	if err := api.DB.QueryRow(`SELECT FK_WindowID FROM SofiScreening`).Scan(&got); err != nil || got != id {
		t.Fatalf("FK_WindowID = %d, %v; want %d", got, err, id)
	}
}

func TestPreviewStatusIsReportedToStaff(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(2, 0, 0))
	setPreviewVia(api, "true")
	rec := httptest.NewRecorder()
	api.WindowStatus(rec, adminReq(http.MethodGet, "/api/sofi-windows/status", "", ""))
	var st windowStatus
	if json.Unmarshal(rec.Body.Bytes(), &st) != nil || !st.Preview || st.PreviewUntil == "" {
		t.Fatalf("status = %s", rec.Body.String())
	}
}

// A restart makes a new API over the same database. The preview must still be on: that was the
// bug (the switch was in memory, so every restart closed it long before its two hours).
func TestPreviewSurvivesARestart(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(2, 0, 0))
	if rec := setPreviewVia(api, "true"); rec.Code != http.StatusOK {
		t.Fatalf("turn on: %d %s", rec.Code, rec.Body.String())
	}
	restarted, err := New(api.DB, "test-secret", filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	move := clockAt(restarted, at(2, 1, 30))
	if !restarted.previewActive() {
		t.Fatal("preview closed after a restart, 90 minutes in")
	}
	move(at(2, 2, 1))
	if restarted.previewActive() {
		t.Fatal("preview is still on after its two hours")
	}
}

func TestPreviewWithoutItsTableIsOffAndTheButtonSaysSo(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(2, 0, 0))
	if _, err := api.DB.Exec(`DROP TABLE SofiPreview`); err != nil {
		t.Fatal(err)
	}
	if api.previewActive() {
		t.Fatal("preview counted as on with no table")
	}
	if rec := setPreviewVia(api, "true"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("turn on without the table: %d", rec.Code)
	}
	if rec := get(api.SignInPage, "/sofi"); !strings.Contains(rec.Body.String(), "The questionnaire is closed") {
		t.Fatal("/sofi should stay closed")
	}
}
