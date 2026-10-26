package emr

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
	"hse-medpost/internal/middleware"
)

type fixture struct {
	db        *sql.DB
	api       *API
	staffID   int64
	patientID int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"001_sqlite.sql", "004_company.sql", "005_patient.sql", "013_region.sql", "014_visit_malaria.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	exec := func(q string, args ...any) int64 {
		res, err := database.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	f := &fixture{db: database}
	f.staffID = exec(`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES ('Dr Examiner','Doctor','Active')`)
	company := exec(`INSERT INTO Company (Name) VALUES ('PT Contoh')`)
	f.patientID = exec(`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_CompanyID, WorkPosition) VALUES ('Budi Santoso','M','1990-06-15',?,'Welder')`, company)
	for _, r := range [][]any{
		{"94", "Papua", 1, nil},
		{"94.01", "Kab. Jayapura", 2, "94"},
		{"94.01.05", "Wanggar", 3, "94.01"},
		{"94.01.05.2001", "Bakungan", 4, "94.01.05"},
		{"94.71", "Kota Jayapura", 2, "94"},
		{"94.71.01", "Abepura", 3, "94.71"},
		{"94.71.01.1001", "Asano", 4, "94.71.01"},
	} {
		exec(`INSERT INTO Region (Code, Name, Level, ParentCode) VALUES (?,?,?,?)`, r...)
	}
	f.api = &API{DB: database, Now: func() time.Time { return time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC) }}
	return f
}

// valid is a complete request; tests change one field at a time.
func (f *fixture) valid() map[string]any {
	return map[string]any{
		"patientId": f.patientID, "testDate": "2026-10-20", "weightKg": 61.5,
		"headOfHousehold": "  siti   aminah ", "headIsPatient": false,
		"regencyCode": "94.01", "districtCode": "94.01.05", "villageCode": "94.01.05.2001",
		"examType": "RDT", "result": "Negative",
	}
}

func (f *fixture) do(t *testing.T, h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: f.staffID, Name: "Dr Examiner"}))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func (f *fixture) create(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	return f.do(t, f.api.Create, http.MethodPost, "/api/malaria-screenings", body)
}

func TestCreateStoresVisitAndScreeningWithServerSideValues(t *testing.T) {
	f := newFixture(t)
	rec := f.create(t, f.valid())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var category, visitAt, reportDate string
	var staff int64
	if err := f.db.QueryRow(`SELECT VisitCategory, VisitAt, ReportDate, FK_StaffID FROM Visit`).Scan(&category, &visitAt, &reportDate, &staff); err != nil {
		t.Fatal(err)
	}
	if category != "Screening" || staff != f.staffID || reportDate != "2026-10-20" || visitAt != "2026-10-20T03:00:00" {
		t.Fatalf("visit = %s %s %s %d", category, visitAt, reportDate, staff)
	}
	var head, regency, district, village, position, company, exam string
	var age int
	var weight float64
	var isPatient int
	err := f.db.QueryRow(`SELECT HeadOfHousehold, HeadIsPatient, RegencyName, DistrictName, VillageName, WorkPosition, Company, ExamType, AgeYears, WeightKg FROM MalariaScreening`).
		Scan(&head, &isPatient, &regency, &district, &village, &position, &company, &exam, &age, &weight)
	if err != nil {
		t.Fatal(err)
	}
	if head != "Siti Aminah" || isPatient != 0 || regency != "Kab. Jayapura" || district != "Wanggar" || village != "Bakungan" {
		t.Fatalf("saved = %q %d %q %q %q", head, isPatient, regency, district, village)
	}
	if position != "Welder" || company != "PT Contoh" || age != 36 || weight != 61.5 || exam != "RDT" {
		t.Fatalf("saved = %q %q %d %v %q", position, company, age, weight, exam)
	}
}

func TestClientCannotSetAgeCompanyOrNames(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	body["ageYears"], body["company"], body["regencyName"], body["workPosition"] = 5, "Evil Corp", "Hacked", "CEO"
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var company, regency string
	var age int
	f.db.QueryRow(`SELECT Company, RegencyName, AgeYears FROM MalariaScreening`).Scan(&company, &regency, &age)
	if company != "PT Contoh" || regency != "Kab. Jayapura" || age != 36 {
		t.Fatalf("client values leaked: %q %q %d", company, regency, age)
	}
}

func TestHeadIsPatientUsesThePatientsName(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	body["headIsPatient"], body["headOfHousehold"] = true, "ignored"
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var head string
	var flag int
	f.db.QueryRow(`SELECT HeadOfHousehold, HeadIsPatient FROM MalariaScreening`).Scan(&head, &flag)
	if head != "Budi Santoso" || flag != 1 {
		t.Fatalf("head = %q flag %d", head, flag)
	}
}

func TestResultIsOptionalUntilTheSmearReturns(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	body["examType"] = "BloodSmear"
	delete(body, "result")
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var visitID int64
	var result sql.NullString
	f.db.QueryRow(`SELECT FK_VisitID, Result FROM MalariaScreening`).Scan(&visitID, &result)
	if result.Valid {
		t.Fatalf("result = %q, want NULL", result.String)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/malaria-screenings/1/result", strings.NewReader(`{"result":"Pf"}`))
	req.SetPathValue("id", "1")
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: f.staffID}))
	rec := httptest.NewRecorder()
	f.api.SetResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	f.db.QueryRow(`SELECT Result FROM MalariaScreening WHERE FK_VisitID = ?`, visitID).Scan(&result)
	if result.String != "Pf" {
		t.Fatalf("result = %q", result.String)
	}
}

func TestInvalidRequestsAreRejectedAndSaveNothing(t *testing.T) {
	f := newFixture(t)
	cases := map[string]func(m map[string]any){
		"weight zero":               func(m map[string]any) { m["weightKg"] = 0 },
		"weight over 150":           func(m map[string]any) { m["weightKg"] = 150.1 },
		"weight negative":           func(m map[string]any) { m["weightKg"] = -3 },
		"weight as text":            func(m map[string]any) { m["weightKg"] = "heavy" },
		"no patient":                func(m map[string]any) { m["patientId"] = 9999 },
		"future date":               func(m map[string]any) { m["testDate"] = "2026-10-21" },
		"bad date":                  func(m map[string]any) { m["testDate"] = "20/10/2026" },
		"head empty after cleaning": func(m map[string]any) { m["headOfHousehold"] = "123 !!" },
		"head too long":             func(m map[string]any) { m["headOfHousehold"] = strings.Repeat("a", 151) },
		"unknown regency":           func(m map[string]any) { m["regencyCode"] = "99.99" },
		"district not in regency":   func(m map[string]any) { m["districtCode"] = "94.71.01" },
		"village not in district":   func(m map[string]any) { m["villageCode"] = "94.71.01.1001" },
		"regency is a province":     func(m map[string]any) { m["regencyCode"] = "94" },
		"bad exam type":             func(m map[string]any) { m["examType"] = "Magic" },
		"bad result":                func(m map[string]any) { m["result"] = "Maybe" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := f.valid()
			mutate(body)
			if rec := f.create(t, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	var visits, screenings int
	f.db.QueryRow(`SELECT COUNT(*) FROM Visit`).Scan(&visits)
	f.db.QueryRow(`SELECT COUNT(*) FROM MalariaScreening`).Scan(&screenings)
	if visits != 0 || screenings != 0 {
		t.Fatalf("rejected requests saved %d visits, %d screenings", visits, screenings)
	}
}

func TestWeightIsRoundedToOneDecimal(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	body["weightKg"] = 150.0
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("150 should be allowed: %d %s", rec.Code, rec.Body.String())
	}
	body["weightKg"] = 70.26
	f.create(t, body)
	var w float64
	f.db.QueryRow(`SELECT WeightKg FROM MalariaScreening ORDER BY FK_VisitID DESC LIMIT 1`).Scan(&w)
	if w != 70.3 {
		t.Fatalf("weight = %v, want 70.3", w)
	}
}

func TestListShowsNewestFirstWithWeekAndExaminer(t *testing.T) {
	f := newFixture(t)
	older := f.valid()
	older["testDate"] = "2026-10-05"
	f.create(t, older)
	f.create(t, f.valid())

	rec := f.do(t, f.api.List, http.MethodGet, "/api/malaria-screenings", nil)
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("body %s: %v", rec.Body.String(), err)
	}
	if rows[0]["testId"] != "MAL-00002" || rows[1]["testId"] != "MAL-00001" {
		t.Fatalf("test ids = %v, %v", rows[0]["testId"], rows[1]["testId"])
	}
	if rows[0]["testDate"] != "2026-10-20" || rows[0]["week"] != "2026-W43" || rows[0]["examiner"] != "Dr Examiner" || rows[0]["patientName"] != "Budi Santoso" {
		t.Fatalf("first row = %v", rows[0])
	}
	if rows[1]["week"] != "2026-W41" {
		t.Fatalf("second week = %v", rows[1]["week"])
	}

	rec = f.do(t, f.api.List, http.MethodGet, "/api/malaria-screenings?from=2026-10-10&to=2026-10-31", nil)
	json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("filtered rows = %d", len(rows))
	}
}

func TestPatientContextGivesAgeCompanyAndPosition(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.api.PatientContext, http.MethodGet, "/api/malaria-screenings/patient-context?patientId=1&testDate=2026-06-14", nil)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got["name"] != "Budi Santoso" || got["ageYears"] != float64(35) || got["company"] != "PT Contoh" || got["workPosition"] != "Welder" {
		t.Fatalf("context = %v", got)
	}
	rec = f.do(t, f.api.PatientContext, http.MethodGet, "/api/malaria-screenings/patient-context?patientId=999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown patient status %d", rec.Code)
	}
}

func (f *fixture) slideInitials(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT SlideInitial FROM MalariaScreening ORDER BY FK_VisitID`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestSlideInitialComesFromTheNameAndIsReturned(t *testing.T) {
	f := newFixture(t)
	rec := f.create(t, f.valid())
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created["slideInitial"] != "BSU1" {
		t.Fatalf("created = %v", created)
	}
	if got := f.slideInitials(t); len(got) != 1 || got[0] != "BSU1" {
		t.Fatalf("stored %v", got)
	}
}

func TestSlideInitialIsUniquePerDayButStableForOnePatient(t *testing.T) {
	f := newFixture(t)
	other := func() int64 {
		res, err := f.db.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Budi Sutanto','1985-01-01')`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}()

	f.create(t, f.valid()) // Budi Santoso -> BSU1
	second := f.valid()
	second["patientId"] = other // same letters, so the digit tells them apart: BSU2
	f.create(t, second)
	f.create(t, f.valid()) // the first patient again, same day: same code
	next := f.valid()
	next["testDate"] = "2026-10-19" // another day: the letters are free again
	f.create(t, next)

	got := f.slideInitials(t)
	if len(got) != 4 || got[0] != "BSU1" || got[1] != "BSU2" || got[2] != "BSU1" || got[3] != "BSU1" {
		t.Fatalf("initials = %v", got)
	}
}

func (f *fixture) preview(t *testing.T, query string) (int, string) {
	t.Helper()
	rec := f.do(t, f.api.SlideInitialPreview, http.MethodGet, "/api/malaria-screenings/slide-initial"+query, nil)
	var body struct {
		SlideInitial string `json:"slideInitial"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.SlideInitial
}

func TestSlideInitialPreviewMatchesWhatSavingWouldAssignAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	if code, got := f.preview(t, "?patientId=1&testDate=2026-10-20"); code != http.StatusOK || got != "BSU1" {
		t.Fatalf("first preview = %d %q", code, got)
	}
	var visits int
	f.db.QueryRow(`SELECT COUNT(*) FROM Visit`).Scan(&visits)
	if visits != 0 {
		t.Fatalf("preview wrote %d visits", visits)
	}

	f.create(t, f.valid()) // patient 1 takes BSU1
	other := func() int64 {
		res, _ := f.db.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Budi Sutanto','1985-01-01')`)
		id, _ := res.LastInsertId()
		return id
	}()
	if _, got := f.preview(t, "?patientId="+strconv.FormatInt(other, 10)+"&testDate=2026-10-20"); got != "BSU2" {
		t.Errorf("same letters, same day: preview = %q, want BSU2", got)
	}
	if _, got := f.preview(t, "?patientId=1&testDate=2026-10-20"); got != "BSU1" {
		t.Errorf("same patient again: preview = %q, want BSU1", got)
	}
	if _, got := f.preview(t, "?patientId="+strconv.FormatInt(other, 10)+"&testDate=2026-10-19"); got != "BSU1" {
		t.Errorf("another day: preview = %q, want BSU1", got)
	}
}

func TestSlideInitialPreviewRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	for query, want := range map[string]int{
		"?patientId=999&testDate=2026-10-20": http.StatusNotFound,
		"?patientId=abc":                     http.StatusBadRequest,
		"?patientId=1&testDate=garbage":      http.StatusBadRequest,
	} {
		if code, _ := f.preview(t, query); code != want {
			t.Errorf("%s: status %d, want %d", query, code, want)
		}
	}
	// No date means today (clinic day).
	if code, got := f.preview(t, "?patientId=1"); code != http.StatusOK || got != "BSU1" {
		t.Errorf("no date: %d %q", code, got)
	}
}

func TestRegencyIsDerivedFromTheDistrictWhenNotGiven(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	delete(body, "regencyCode")
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var code, name string
	f.db.QueryRow(`SELECT RegencyCode, RegencyName FROM MalariaScreening`).Scan(&code, &name)
	if code != "94.01" || name != "Kab. Jayapura" {
		t.Fatalf("regency = %q %q", code, name)
	}

	// A regency that is given must still match the district's own.
	wrong := f.valid()
	wrong["regencyCode"] = "94.71"
	if rec := f.create(t, wrong); rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched regency status %d, want 400", rec.Code)
	}
}
