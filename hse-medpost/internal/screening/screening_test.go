package screening

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
)

const testPassword = "246810"

func openScreeningTestDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "screening.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"001_sqlite.sql", "011_screening.sql", "012_fatigue.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := EnsureDrugAlcoholColumns(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFatigueColumns(database); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	res, err := database.Exec(`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES ('Test Examiner', 'HSE', 'Active')`)
	if err != nil {
		t.Fatal(err)
	}
	staffID, _ := res.LastInsertId()
	if _, err := database.Exec(`INSERT INTO AppUserTable (Username, PasswordHash, Role, FK_StaffID) VALUES ('examiner', ?, 'staff', ?)`, string(hash), staffID); err != nil {
		t.Fatal(err)
	}
	return database, staffID
}

func newAPI(database *sql.DB) *API {
	return &API{
		DB:        database,
		AuthStore: auth.NewStore("test-secret"),
		Now:       func() time.Time { return time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC) },
	}
}

func postJSON(t *testing.T, handler http.HandlerFunc, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func login(t *testing.T, api *API) string {
	t.Helper()
	rec := postJSON(t, api.Login, "/api/screening/login", map[string]string{"username": "examiner", "password": testPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}

func TestLoginRejectsBadPasswordAndSetsNoCookie(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	tests := []struct {
		name     string
		password string
		want     int
	}{
		{"not six digits", "12ab", http.StatusBadRequest},
		{"wrong password", "000000", http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, api.Login, "/api/screening/login", map[string]string{"username": "examiner", "password": tc.password}, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatal("error response must carry CORS header so the form can read it")
			}
		})
	}
}

func TestLoginReturnsTokenWithoutCookie(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	rec := postJSON(t, api.Login, "/api/screening/login", map[string]string{"username": "examiner", "password": testPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("bearer login must not set a cookie")
	}
	var out struct {
		Token   string `json:"token"`
		StaffID int64  `json:"staffId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sess, err := api.AuthStore.Verify(out.Token)
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if sess.StaffID != out.StaffID {
		t.Fatalf("token staff = %d, body staff = %d", sess.StaffID, out.StaffID)
	}
}

func TestSubmitRequiresValidToken(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	body := map[string]any{"form": "DrugAlcoholConsent", "formVersion": "20270228", "records": []any{map[string]string{"id": "r1"}}}
	for _, token := range []string{"", "garbage.token"} {
		rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", token, rec.Code)
		}
	}
}

func TestSubmitStoresOnceAndAttributesStaff(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	if sess, err := api.AuthStore.Verify(token); err != nil || sess.StaffID != staffID {
		t.Fatalf("token staff = %v, err = %v; want %d", sess, err, staffID)
	}
	body := map[string]any{
		"form":        "DrugAlcoholConsent",
		"formVersion": "20270228",
		"records": []any{
			map[string]string{"id": "client-1", "donor_name": "Budi", "test_type": "random", "breath1_reading": "0.00", "breath1_result": "neg", "final_result": "neg"},
			map[string]string{"id": ""}, // invalid: missing id, must not be reported as accepted
		},
	}

	first := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	if first.Code != http.StatusOK {
		t.Fatalf("submit status = %d body = %s", first.Code, first.Body.String())
	}
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accepted) != 1 || out.Accepted[0] != "client-1" {
		t.Fatalf("accepted = %v, want [client-1]", out.Accepted)
	}

	second := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	if second.Code != http.StatusOK {
		t.Fatalf("retry status = %d body = %s", second.Code, second.Body.String())
	}
	var count int
	var owner int64
	if err := database.QueryRow(`SELECT COUNT(*), MAX(FK_StaffID) FROM DrugAlcoholScreening WHERE ClientID = 'client-1'`).Scan(&count, &owner); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows for client-1 = %d, want 1 (retry must not duplicate)", count)
	}
	if owner != staffID {
		t.Fatalf("FK_StaffID = %d, want %d", owner, staffID)
	}
}

func TestSubmitRejectsUnknownForm(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{"form": "FatigueScoring", "formVersion": "20271006", "records": []any{map[string]string{"id": "x"}}}
	rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
