package directory

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"hse-medpost/internal/db"
	"hse-medpost/internal/entrymcu"
)

func TestRotateTokenReplacesAccess(t *testing.T) {
	database := openDirectoryTestDB(t)
	if err := EnsureToken(database, "old-secret"); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database, Token: "old-secret", Now: fixedNow}

	rec := httptest.NewRecorder()
	api.RotateToken(rec, httptest.NewRequest(http.MethodPost, "/api/directory/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d body = %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Token == "" || rotated.Token == "old-secret" {
		t.Fatalf("token = %q", rotated.Token)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/employees?q=andi", nil)
	req.Header.Set("Authorization", "Bearer old-secret")
	api.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	api.GetToken(rec, httptest.NewRequest(http.MethodGet, "/api/directory/token", nil))
	if rec.Code != http.StatusOK || !bytesContains(rec.Body.String(), rotated.Token) {
		t.Fatalf("get token = %d %s", rec.Code, rec.Body.String())
	}
}

func TestEligibleTokenCannotReadEveryCheckup(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	if err := EnsureToken(database, "eligible-secret"); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database, Token: "eligible-secret", Now: fixedNow}

	rec := httptest.NewRecorder()
	api.RotateToken(rec, httptest.NewRequest(http.MethodPost, "/api/directory/full-token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate full status = %d body = %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		Token     string `json:"token"`
		FullToken string `json:"fullToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Token != "eligible-secret" || rotated.FullToken == "" || rotated.FullToken == rotated.Token {
		t.Fatalf("tokens were not split")
	}

	fitness := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/fitness", nil)
	req.Header.Set("Authorization", "Bearer eligible-secret")
	api.Fitness(fitness, req)
	if fitness.Code != http.StatusOK {
		t.Fatalf("eligible fitness status = %d", fitness.Code)
	}

	denied := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/directory/checkups", nil)
	req.Header.Set("Authorization", "Bearer eligible-secret")
	api.Checkups(denied, req)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("eligible checkups status = %d", denied.Code)
	}

	allowed := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/directory/checkups", nil)
	req.Header.Set("Authorization", "Bearer "+rotated.FullToken)
	api.Checkups(allowed, req)
	if allowed.Code != http.StatusOK {
		t.Fatalf("full checkups status = %d body = %s", allowed.Code, allowed.Body.String())
	}
}

func bytesContains(body, token string) bool {
	return len(token) > 0 && len(body) > 0 && (len(body) >= len(token)) && (func() bool {
		return jsonToken(body) == token
	})()
}

func jsonToken(body string) string {
	var got struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	return got.Token
}

func TestDirectoryRequiresToken(t *testing.T) {
	api := &API{Token: "", Now: fixedNow}
	rec := httptest.NewRecorder()
	api.List(rec, httptest.NewRequest(http.MethodGet, "/api/directory/employees?q=andi", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unset token status = %d", rec.Code)
	}

	api.Token = "secret"
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/employees?q=andi", nil)
	api.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/directory/employees?q=andi", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	api.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer status = %d", rec.Code)
	}
}

func TestDirectoryReturnsLatestFitReviewOnly(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)

	api := &API{DB: database, Token: "secret", Now: fixedNow}
	body := getDirectory(t, api, "/api/directory/employees?q=and")

	if len(body) != 1 {
		t.Fatalf("hits = %d, want the current Fit worker only", len(body))
	}
	got := body[0]
	if got["name"] != "Andi Wijaya" || got["resultCriteria"] != "Fit" {
		t.Fatalf("worker = %v", got)
	}
	if got["checkUpId"] != "MedC-new" || got["resultValidUntil"] != "2027-01-15" {
		t.Fatalf("review = %v", got)
	}
	if got["resultNotes"] != "fit for site" {
		t.Fatalf("notes = %v", got["resultNotes"])
	}
	if got["company"] != "PT Karya" || got["companyEntity"] != "Contractor" {
		t.Fatalf("company = %v / %v", got["company"], got["companyEntity"])
	}
	parents, _ := got["parents"].([]any)
	if len(parents) != 1 {
		t.Fatalf("parents = %v", got["parents"])
	}
	parent, _ := parents[0].(map[string]any)
	if parent["name"] != "PT LNG" {
		t.Fatalf("parent = %v", parent)
	}
	for _, hidden := range []string{"personalId", "visionCriteria", "bmiValue", "recommendation", "cbcNotes"} {
		if _, ok := got[hidden]; ok {
			t.Fatalf("response included %s", hidden)
		}
	}

	byID := getDirectory(t, api, "/api/directory/employees?checkUpId=MedC-new")
	if len(byID) != 1 || byID[0]["employeeId"] != "E-1" {
		t.Fatalf("checkup lookup = %v", byID)
	}

	note := getDirectory(t, api, "/api/directory/employees?q=sari")
	if len(note) != 1 || note[0]["resultCriteria"] != "Fit with note" {
		t.Fatalf("fit with note = %v", note)
	}
	if got := getDirectory(t, api, "/api/directory/employees?q=budi"); len(got) != 0 {
		t.Fatalf("unfit worker leaked: %v", got)
	}
	if got := getDirectory(t, api, "/api/directory/employees?q=lama"); len(got) != 0 {
		t.Fatalf("expired fit leaked: %v", got)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/employees", nil)
	req.Header.Set("Authorization", "Bearer secret")
	api.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing selector status = %d", rec.Code)
	}
}

// An unfinished (Open) checkup with a newer document date must not hide an
// older closed Fit one that was submitted later. A newer CLOSED Unfit still
// does, because that is a real decision that replaces the older Fit.
func TestDirectoryIgnoresNewerOpenCheckups(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	mustExec(t, database, `INSERT INTO Patient (PatientID, Name, Gender, DateOfBirth, FK_NationalityID, EmployeeID, FK_CompanyID, WorkPosition) VALUES
		(5, 'Wang Laizhong', 'Male', '1985-05-05', 1, 'E-5', 2, 'Construction Engineer'),
		(6, 'Tono Berubah', 'Male', '1986-06-06', 1, 'E-6', 2, 'Driver')`)
	insertMCU(t, database, 5, "MedC-wang-fit", "2026-08-01", "FitWithNote", "2027-08-01", "valid", "Close")
	insertMCU(t, database, 5, "MedC-wang-open1", "2026-09-01", "NeedFollowUp", "", "", "Open")
	insertMCU(t, database, 5, "MedC-wang-open2", "2026-09-15", "NeedFollowUp", "", "", "Open")
	insertMCU(t, database, 6, "MedC-tono-fit", "2026-08-01", "Fit", "2027-08-01", "", "Close")
	insertMCU(t, database, 6, "MedC-tono-unfit", "2026-09-01", "Unfit", "2027-09-01", "", "Close")

	api := &API{DB: database, Token: "secret", Now: fixedNow}

	got := getDirectory(t, api, "/api/directory/employees?q=wang%20laizhong")
	if len(got) != 1 || got[0]["checkUpId"] != "MedC-wang-fit" {
		t.Fatalf("open checkups hid the closed Fit one: %v", got)
	}
	if got := getDirectory(t, api, "/api/directory/employees?q=tono"); len(got) != 0 {
		t.Fatalf("newer closed Unfit did not hide the older Fit: %v", got)
	}
}

// The real Wang Laizhong case: the newer-dated checkup was saved as Close but
// with an IncompleteResult, and the system had already stamped it
// "Renewed by <the Fit one>". Neither a superseded row nor an IncompleteResult
// is a decision, so the older closed Fit must still be returned.
func TestDirectoryIgnoresIncompleteAndRenewedCheckups(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	mustExec(t, database, `INSERT INTO Patient (PatientID, Name, Gender, DateOfBirth, FK_NationalityID, EmployeeID, FK_CompanyID, WorkPosition) VALUES
		(7, 'Wang Laizhong', 'Male', '1985-05-05', 1, 'E-7', 2, 'Construction Engineer'),
		(8, 'Citra Belum Lengkap', 'Female', '1990-01-01', 1, 'E-8', 2, 'Clerk'),
		(9, 'Dedi Tindak Lanjut', 'Male', '1991-02-02', 1, 'E-9', 2, 'Cook')`)
	insertMCU(t, database, 7, "MedC-wang-fit", "2026-09-30", "FitWithNote", "2027-09-25", "valid", "Close")
	insertMCU(t, database, 7, "MedC-wang-incomplete", "2026-10-16", "IncompleteResult", "", "", "Close")
	mustExec(t, database, `UPDATE McuRecord SET HcValidityStatus = 'Renewed by MedC-wang-fit' WHERE CheckUpID = 'MedC-wang-incomplete'`)
	// Same shape without the Renewed by stamp: IncompleteResult alone is enough.
	insertMCU(t, database, 8, "MedC-citra-fit", "2026-09-30", "Fit", "2027-09-30", "", "Close")
	insertMCU(t, database, 8, "MedC-citra-incomplete", "2026-10-16", "IncompleteResult", "", "", "Close")
	// A newer closed NeedFollowUp is a clinical decision and still hides the older Fit.
	insertMCU(t, database, 9, "MedC-dedi-fit", "2026-09-30", "Fit", "2027-09-30", "", "Close")
	insertMCU(t, database, 9, "MedC-dedi-follow", "2026-10-16", "NeedFollowUp", "", "", "Close")

	api := &API{DB: database, Token: "secret", Now: fixedNow}

	got := getDirectory(t, api, "/api/directory/employees?q=wang%20laizhong")
	if len(got) != 1 || got[0]["checkUpId"] != "MedC-wang-fit" {
		t.Fatalf("renewed / incomplete row hid the Fit one: %v", got)
	}
	got = getDirectory(t, api, "/api/directory/employees?q=citra")
	if len(got) != 1 || got[0]["checkUpId"] != "MedC-citra-fit" {
		t.Fatalf("IncompleteResult hid the Fit one: %v", got)
	}
	if got := getDirectory(t, api, "/api/directory/employees?q=dedi"); len(got) != 0 {
		t.Fatalf("newer closed NeedFollowUp did not hide the older Fit: %v", got)
	}
}

func TestFitnessOmitsOverdueArchivedAndUnfit(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	api := &API{DB: database, Token: "secret", Now: fixedNow}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/directory/fitness", nil)
	api.Fitness(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", rec.Code)
	}

	body := getJSONList(t, api, "/api/directory/fitness", api.Fitness)
	ids := map[string]string{}
	for _, row := range body {
		ids[row["checkUpId"].(string)] = row["hcValidityStatus"].(string)
		if row["resultCriteria"] != "Fit" && row["resultCriteria"] != "Fit with note" {
			t.Fatalf("result = %v", row["resultCriteria"])
		}
	}
	if ids["MedC-new"] != "Valid" || ids["MedC-note"] != "Valid" {
		t.Fatalf("clearances = %v", ids)
	}
	if _, ok := ids["MedC-old"]; ok {
		t.Fatal("archived fit was included")
	}
	if _, ok := ids["MedC-expired"]; ok {
		t.Fatal("overdue fit was included")
	}
	if _, ok := ids["MedC-unfit"]; ok {
		t.Fatal("unfit was included")
	}
}

func TestCheckupsReturnsEveryRecord(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	api := &API{DB: database, Token: "secret", FullToken: "secret", Now: fixedNow}
	body := getJSONList(t, api, "/api/directory/checkups", api.Checkups)
	if len(body) != 5 {
		t.Fatalf("rows = %d", len(body))
	}
	var unfit map[string]any
	for _, row := range body {
		if row["checkUpId"] == "MedC-unfit" {
			unfit = row
		}
		if _, ok := row["visionCriteria"]; !ok {
			t.Fatal("exam field missing")
		}
	}
	// WHY empty: Unfit is an open result (entrymcu.ShownValidity), so it has no validity standing
	// even when its stored status is Close.
	if unfit == nil || unfit["resultCriteria"] != "Unfit" || unfit["hcValidityStatus"] != "" {
		t.Fatalf("unfit = %v", unfit)
	}
	var fit map[string]any
	for _, row := range body {
		if row["checkUpId"] == "MedC-new" {
			fit = row
		}
	}
	if fit["parent"] != "PT LNG" || fit["depth"] != "1" {
		t.Fatalf("relation = %v / %v", fit["parent"], fit["depth"])
	}
}

func getJSONList(t *testing.T, api *API, path string, handler func(http.ResponseWriter, *http.Request)) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer secret")
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func fixedNow() time.Time {
	return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
}

func getDirectory(t *testing.T, api *API, path string) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer secret")
	api.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func openDirectoryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"004_company.sql", "005_patient.sql", "006_entrymcu.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, step := range []func(*sql.DB) error{
		entrymcu.EnsureTaskCategoryNamesColumn,
		entrymcu.EnsureRadiologyColumns,
		entrymcu.EnsurePhysicalExamNotesColumn,
		entrymcu.EnsureBloodTypeColumn,
		entrymcu.EnsurePatientHeightColumn,
		entrymcu.EnsureMcuRecordPersonalIDColumn,
		// Lets the test database store IncompleteResult like production does.
		entrymcu.EnsureResultCriteriaEditable,
	} {
		if err := step(database); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func seedDirectoryFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExec(t, database, `INSERT INTO EntityType (EntityTypeID, Name) VALUES (1, 'Contractor'), (2, 'Client')`)
	mustExec(t, database, `INSERT INTO Company (CompanyID, Name, FK_EntityTypeID) VALUES (1, 'PT LNG', 2), (2, 'PT Karya', 1)`)
	mustExec(t, database, `INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID, Depth) VALUES ('R1', 2, 1, 1)`)
	mustExec(t, database, `INSERT INTO NationalityTable (NationalityID, Name) VALUES (1, 'Indonesia')`)
	mustExec(t, database, `INSERT INTO Patient (PatientID, Name, Gender, DateOfBirth, FK_NationalityID, BloodType, EmployeeID, FK_CompanyID, WorkPosition) VALUES
		(1, 'Andi Wijaya', 'Male', '1990-03-05', 1, 'O+', 'E-1', 2, 'Welder'),
		(2, 'Sari Dewi', 'Female', '1992-07-01', 1, 'A+', 'E-2', 2, 'Nurse'),
		(3, 'Budi Santoso', 'Male', '1988-01-02', 1, 'B+', 'E-3', 2, 'Helper'),
		(4, 'Lama Fit', 'Male', '1980-01-02', 1, 'AB+', 'E-4', 2, 'Rigger')`)
	insertMCU(t, database, 1, "MedC-old", "2025-01-01", "Fit", "2026-01-01", "old fit", "Close")
	insertMCU(t, database, 1, "MedC-new", "2026-01-15", "Fit", "2027-01-15", "fit for site", "Close")
	insertMCU(t, database, 2, "MedC-note", "2026-02-01", "FitWithNote", "2027-02-01", "bring medication", "Close")
	insertMCU(t, database, 3, "MedC-unfit", "2026-03-01", "Unfit", "2027-03-01", "unfit", "Close")
	insertMCU(t, database, 4, "MedC-expired", "2024-01-01", "Fit", "2026-10-02", "expired", "Close")
}

func insertMCU(t *testing.T, database *sql.DB, patientID int, checkUpID, date, result, until, notes, status string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, DateCheckUp, ResultCriteria, ResultValidUntil, ResultNotes, CheckUpStatus)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		patientID, date+"T00:00:00", date, checkUpID, date, result, until, notes, status)
}

func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// The permit app asks for expired workers only to tell them their checkup has ended. Every other
// caller (name search, gate officer checks) must keep getting valid checkups only.
func TestEmployeesCanIncludeAnExpiredFitCheckupOnlyWhenAsked(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	api := &API{DB: database, Token: "secret", Now: fixedNow}

	if got := getDirectory(t, api, "/api/directory/employees?q=lama"); len(got) != 0 {
		t.Fatalf("expired fit returned without being asked: %v", got)
	}
	got := getDirectory(t, api, "/api/directory/employees?q=lama&includeExpired=1")
	if len(got) != 1 || got[0]["checkUpId"] != "MedC-expired" || got[0]["resultValidUntil"] != "2026-10-02" {
		t.Fatalf("includeExpired = %v", got)
	}
	// Asking for expired ones does not widen anything else: unfit stays out, and a worker's
	// current valid checkup is still the one returned.
	if got := getDirectory(t, api, "/api/directory/employees?q=budi&includeExpired=1"); len(got) != 0 {
		t.Fatalf("unfit worker leaked: %v", got)
	}
	if got := getDirectory(t, api, "/api/directory/employees?q=and&includeExpired=1"); len(got) != 1 || got[0]["checkUpId"] != "MedC-new" {
		t.Fatalf("current checkup = %v", got)
	}
	// Any value other than 1 means "not asked".
	if got := getDirectory(t, api, "/api/directory/employees?q=lama&includeExpired=yes"); len(got) != 0 {
		t.Fatalf("a loose value switched it on: %v", got)
	}
}

// resultCriteria comes back in words (Fit with note, Temporarily Unfit, Need follow up), not as the
// stored code, the same as the employee endpoints.
func TestCheckupsResultCriteriaIsReadable(t *testing.T) {
	database := openDirectoryTestDB(t)
	seedDirectoryFixture(t, database)
	insertMCU(t, database, 2, "MedC-temp", "2026-04-01", "TemporaryUnfit", "", "bp high", "Open")
	insertMCU(t, database, 2, "MedC-nfu", "2026-05-01", "NeedFollowUp", "", "", "Open")
	insertMCU(t, database, 2, "MedC-inc", "2026-06-01", "IncompleteResult", "", "", "Open")
	api := &API{DB: database, Token: "secret", FullToken: "secret", Now: fixedNow}
	want := map[string]string{
		"MedC-inc":   "Incomplete",
		"MedC-new":   "Fit",
		"MedC-note":  "Fit with note",
		"MedC-unfit": "Unfit",
		"MedC-temp":  "Temporarily Unfit",
		"MedC-nfu":   "Need follow up",
	}
	for _, row := range getJSONList(t, api, "/api/directory/checkups", api.Checkups) {
		id, _ := row["checkUpId"].(string)
		w, ok := want[id]
		if !ok {
			continue
		}
		if row["resultCriteria"] != w {
			t.Errorf("%s: resultCriteria=%v, want %v", id, row["resultCriteria"], w)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("rows not returned: %v", want)
	}
}
