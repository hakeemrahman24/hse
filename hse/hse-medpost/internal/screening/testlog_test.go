package screening

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// seedTestLog inserts one alcohol row and two fatigue rows with fixed upload
// times, so the date filter and sort order can be checked exactly.
func seedTestLog(t *testing.T, staffID int64, api *API) {
	t.Helper()
	db := api.DB
	if _, err := db.Exec(`INSERT INTO DrugAlcoholScreening
		(ClientID, FK_StaffID, FormVersion, CreatedAt, SubmittedAt, DonorName, DonorNik, TestType, FinalResult, RawJSON)
		VALUES ('a-1', ?, '20270228', '2026-10-01T10:00:00', '2026-10-01T10:05:00', 'Andi', '0123', 'random', 'negative', '{}')`, staffID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO FatigueScreening
		(ClientID, FK_StaffID, FormVersion, CreatedAt, SubmittedAt, WorkerName, ScoreTotal, RiskLevel, RawJSON)
		VALUES
		('f-1', ?, '20271006', '2026-10-06T08:00:00', '2026-10-06T09:00:00', 'Budi', '7', 'medium', '{}'),
		('f-2', ?, '20271006', '2026-10-07T08:00:00', '2026-10-07T23:30:00', 'Sari', '3', 'low', '{}')`, staffID, staffID); err != nil {
		t.Fatal(err)
	}
}

func getTestLog(t *testing.T, handler http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// indexOf is the position of name in a header row, or -1.
func indexOf(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}
