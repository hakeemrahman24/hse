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

func postWebEntry(t *testing.T, api *API, staffID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/screening-log/alcohol", bytes.NewReader(raw))
	if staffID != 0 {
		req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: staffID}))
	}
	rec := httptest.NewRecorder()
	api.CreateAlcoholEntry(rec, req)
	return rec
}

func TestWebEntryStoresRecordForSessionStaff(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	row := fullAlcoholRow()
	row["id"] = "web-1"

	rec := postWebEntry(t, api, staffID, row)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var name, form string
	var fk int64
	err := database.QueryRow(`SELECT DonorName, FormVersion, FK_StaffID FROM DrugAlcoholScreening WHERE ClientID = 'web-1'`).Scan(&name, &form, &fk)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Budi" || fk != staffID || form != "20270228" {
		t.Fatalf("stored %q %q %d", name, form, fk)
	}
}

func TestWebEntryRepeatIsNotDuplicated(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	row := fullAlcoholRow()
	row["id"] = "web-2"
	postWebEntry(t, api, staffID, row)
	rec := postWebEntry(t, api, staffID, row)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat status = %d", rec.Code)
	}
	var n int
	database.QueryRow(`SELECT COUNT(*) FROM DrugAlcoholScreening WHERE ClientID = 'web-2'`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestWebEntryRejectsBadRequests(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	missing := func(key string) map[string]any {
		row := fullAlcoholRow()
		row["id"] = "web-3"
		row[key] = ""
		return row
	}
	for _, key := range []string{"donor_name", "donor_company", "donor_position", "consent_at", "test_type"} {
		if rec := postWebEntry(t, api, staffID, missing(key)); rec.Code != http.StatusBadRequest {
			t.Errorf("blank %s: status = %d", key, rec.Code)
		}
	}
	noVersion := fullAlcoholRow()
	delete(noVersion, "form_version")
	if rec := postWebEntry(t, api, staffID, noVersion); rec.Code != http.StatusBadRequest {
		t.Errorf("no form_version: status = %d", rec.Code)
	}
	if rec := postWebEntry(t, api, 0, fullAlcoholRow()); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status = %d", rec.Code)
	}
	if rec := postWebEntry(t, api, staffID, "not an object"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json shape: status = %d", rec.Code)
	}
	var n int
	database.QueryRow(`SELECT COUNT(*) FROM DrugAlcoholScreening`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected rows were stored: %d", n)
	}
}
