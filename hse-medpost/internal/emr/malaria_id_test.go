package emr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

type idFields struct {
	MalariaID int64  `json:"malariaId"`
	VisitID   int64  `json:"visitId"`
	TestID    string `json:"testId"`
	Result    string `json:"result"`
}

func (f *fixture) listIDs(t *testing.T) []idFields {
	t.Helper()
	rec := f.do(t, f.api.List, http.MethodGet, "/api/malaria-screenings", nil)
	var rows []idFields
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// A second test on the same visit (an RDT, then a smear for the same fever) is
// its own record with its own Test ID.
func TestOneVisitCanHoldMoreThanOneMalariaTest(t *testing.T) {
	f := newFixture(t)
	if rec := f.create(t, f.valid()); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	_, err := f.db.Exec(`
		INSERT INTO MalariaScreening (FK_VisitID, FK_PurposeID, TestDate, SlideInitial, AgeYears, WeightKg, HeadOfHousehold, HeadIsPatient,
			RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName, WorkPosition, Company, ExamType, Result)
		SELECT FK_VisitID, FK_PurposeID, TestDate, SlideInitial, AgeYears, WeightKg, HeadOfHousehold, HeadIsPatient,
			RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName, WorkPosition, Company, 'BloodSmear', NULL
		FROM MalariaScreening`)
	if err != nil {
		t.Fatal(err)
	}
	rows := f.listIDs(t)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	if rows[0].VisitID != rows[1].VisitID || rows[0].MalariaID == rows[1].MalariaID || rows[0].TestID == rows[1].TestID {
		t.Errorf("same visit, different record and Test ID expected: %+v", rows)
	}
}

func TestTestIDAndResultUseTheMalariaRecordNotTheVisit(t *testing.T) {
	f := newFixture(t)
	// Push the visit numbers ahead of the malaria numbers so the two differ.
	for i := 0; i < 3; i++ {
		f.db.Exec(`INSERT INTO Visit (FK_PatientID, FK_StaffID, VisitCategory, VisitAt, ReportDate, CreatedAt) VALUES (?,?,'Outpatient','2026-10-20T03:00:00','2026-10-20','2026-10-20T03:00:00')`, f.patientID, f.staffID)
	}
	body := f.valid()
	delete(body, "result")
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	row := f.listIDs(t)[0]
	if row.VisitID != 4 || row.MalariaID != 1 || row.TestID != "MAL-00001" {
		t.Fatalf("row = %+v, want visit 4, malaria 1, MAL-00001", row)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/malaria-screenings/1/result", strings.NewReader(`{"result":"Pf"}`))
	req.SetPathValue("id", "1")
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: f.staffID}))
	rec := httptest.NewRecorder()
	f.api.SetResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := f.listIDs(t)[0].Result; got != "Pf" {
		t.Errorf("result = %q, want Pf", got)
	}
}

// An older dev database keyed MalariaScreening by FK_VisitID alone.
func TestEnsureMalariaIDRebuildsAnOlderTableKeepingTheNumbers(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 2; i++ {
		if rec := f.create(t, f.valid()); rec.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
	// Rebuild the table in its old shape, with the same two rows.
	for _, q := range []string{
		`ALTER TABLE MalariaScreening RENAME TO MalariaScreeningNew`,
		`CREATE TABLE MalariaScreening (
			FK_VisitID INTEGER PRIMARY KEY REFERENCES Visit(VisitID),
			FK_PurposeID INTEGER NOT NULL DEFAULT 1, TestDate TEXT NOT NULL, SlideInitial TEXT NOT NULL, AgeYears INTEGER NULL,
			WeightKg REAL NOT NULL, HeadOfHousehold TEXT NOT NULL, HeadIsPatient INTEGER NOT NULL DEFAULT 0,
			RegencyCode TEXT NOT NULL, RegencyName TEXT NOT NULL, DistrictCode TEXT NOT NULL, DistrictName TEXT NOT NULL,
			VillageCode TEXT NOT NULL, VillageName TEXT NOT NULL, WorkPosition TEXT NOT NULL DEFAULT '', Company TEXT NOT NULL DEFAULT '',
			ExamType TEXT NOT NULL, Result TEXT NULL)`,
		`INSERT INTO MalariaScreening SELECT FK_VisitID, FK_PurposeID, TestDate, SlideInitial, AgeYears, WeightKg, HeadOfHousehold, HeadIsPatient,
			RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName, WorkPosition, Company, ExamType, Result FROM MalariaScreeningNew`,
		`DROP TABLE MalariaScreeningNew`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i := 0; i < 2; i++ { // safe to repeat
		if err := EnsureMalariaID(f.db); err != nil {
			t.Fatal(err)
		}
	}
	rows := f.listIDs(t)
	if len(rows) != 2 || rows[0].MalariaID != rows[0].VisitID || rows[1].MalariaID != rows[1].VisitID {
		t.Fatalf("rows = %+v; each malaria id should equal its old visit id", rows)
	}
	// The visit is no longer unique: a second test on visit 1 is accepted.
	if _, err := f.db.Exec(`INSERT INTO MalariaScreening (FK_VisitID, FK_PurposeID, TestDate, SlideInitial, WeightKg, HeadOfHousehold,
		RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName, ExamType) VALUES (1,1,'2026-10-20','BSU1',60,'X','a','a','b','b','c','c','RDT')`); err != nil {
		t.Errorf("second test on one visit: %v", err)
	}
}
