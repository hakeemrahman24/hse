package screening

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

func fatigueRow() map[string]any {
	return map[string]any{
		"id": "fa-1", "created_at": "2026-10-07T09:00:00.000Z", "form_version": "web1",
		"examiner": "Dewi Lestari", "name": "Budi Santoso", "company": "PT Contoh", "position": "Operator",
		"date": "2026-10-07", "time": "08:30", "bp_systolic": 120, "bp_diastolic": 80, "pulse": 72,
		"feels_healthy": "Y", "takes_medicine": "N", "medicine_name": "",
		"sleep_24h": 5, "sleep_48h": 11, "awake_hours": 16,
		"score_24h": 4, "score_48h": 2, "score_awake": 2, "score_total": 8,
		"risk_level": "High", "risk_points": "8 or more points", "remarks": "",
	}
}

func postFatigueEntry(t *testing.T, api *API, staffID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/screening-log/fatigue", bytes.NewReader(raw))
	if staffID != 0 {
		req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: staffID}))
	}
	rec := httptest.NewRecorder()
	api.CreateFatigueEntry(rec, req)
	return rec
}

func TestFatigueEntryStoredOnceForSessionStaff(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	if rec := postFatigueEntry(t, api, staffID, fatigueRow()); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := postFatigueEntry(t, api, staffID, fatigueRow()); rec.Code != http.StatusOK {
		t.Fatalf("repeat status = %d", rec.Code)
	}
	var n int
	var name, risk string
	var fk int64
	database.QueryRow(`SELECT COUNT(*) FROM FatigueScreening WHERE ClientID = 'fa-1'`).Scan(&n)
	database.QueryRow(`SELECT WorkerName, RiskLevel, FK_StaffID FROM FatigueScreening WHERE ClientID = 'fa-1'`).Scan(&name, &risk, &fk)
	if n != 1 || name != "Budi Santoso" || risk != "High" || fk != staffID {
		t.Fatalf("rows=%d name=%q risk=%q fk=%d", n, name, risk, fk)
	}
}

func TestFatigueEntryRejectsBadRequests(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	for _, key := range []string{"examiner", "name", "company", "position", "sleep_24h", "sleep_48h", "awake_hours", "risk_level", "score_total", "form_version"} {
		row := fatigueRow()
		row["id"] = "fa-bad"
		row[key] = ""
		if rec := postFatigueEntry(t, api, staffID, row); rec.Code != http.StatusBadRequest {
			t.Errorf("blank %s: status = %d", key, rec.Code)
		}
	}
	if rec := postFatigueEntry(t, api, 0, fatigueRow()); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status = %d", rec.Code)
	}
	if rec := postFatigueEntry(t, api, staffID, "nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad shape: status = %d", rec.Code)
	}
	var n int
	database.QueryRow(`SELECT COUNT(*) FROM FatigueScreening`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected rows stored: %d", n)
	}
}
