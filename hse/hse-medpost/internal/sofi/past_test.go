package sofi

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
	// scores come from Score() on the stored answers: every answer 5 on 25 items is total 125 of 150
	if !strings.Contains(body, "125 / 150") {
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
