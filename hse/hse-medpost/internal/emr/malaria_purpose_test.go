package emr

import (
	"encoding/json"
	"net/http"
	"testing"
)

type purposeFields struct {
	PurposeID int    `json:"purposeId"`
	Purpose   string `json:"purpose"`
}

func TestScreeningPurposeDefaultsToOnArrival(t *testing.T) {
	f := newFixture(t)
	rec := f.create(t, f.valid()) // no purposeId sent
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var row purposeFields
	json.Unmarshal(rec.Body.Bytes(), &row)
	if row.PurposeID != 1 || row.Purpose != "On arrival" {
		t.Errorf("purpose = %d %q, want 1 On arrival", row.PurposeID, row.Purpose)
	}
	var stored int
	if err := f.db.QueryRow(`SELECT FK_PurposeID FROM MalariaScreening`).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("stored FK_PurposeID = %d (%v), want 1", stored, err)
	}
}

func TestScreeningPurposeSymptomaticIsStoredAndListed(t *testing.T) {
	f := newFixture(t)
	body := f.valid()
	body["purposeId"] = 2
	if rec := f.create(t, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	rec := f.do(t, f.api.List, http.MethodGet, "/api/malaria-screenings", nil)
	var rows []purposeFields
	json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0].PurposeID != 2 || rows[0].Purpose != "Symptomatic" {
		t.Errorf("list = %+v", rows)
	}
}

func TestScreeningPurposeMustBeOneOfTheLookupRows(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []any{3, -1, "x"} {
		body := f.valid()
		body["purposeId"] = bad
		if rec := f.create(t, body); rec.Code != http.StatusBadRequest {
			t.Errorf("purposeId %v: status %d, want 400", bad, rec.Code)
		}
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM Visit`).Scan(&n)
	if n != 0 {
		t.Errorf("%d visits saved by rejected requests", n)
	}
}

// An older dev database has neither the lookup table nor the column.
func TestEnsureMalariaPurposeUpgradesAnOlderDatabase(t *testing.T) {
	f := newFixture(t)
	f.create(t, f.valid())
	for _, q := range []string{
		`ALTER TABLE MalariaScreening DROP COLUMN FK_PurposeID`,
		`DROP TABLE MalariaPurposeTable`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i := 0; i < 2; i++ { // safe to repeat
		if err := EnsureMalariaPurpose(f.db); err != nil {
			t.Fatal(err)
		}
	}
	var id int
	var name string
	err := f.db.QueryRow(`SELECT m.FK_PurposeID, t.PurposeName FROM MalariaScreening m JOIN MalariaPurposeTable t ON t.PurposeID = m.FK_PurposeID`).Scan(&id, &name)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 || name != "On arrival" {
		t.Errorf("existing row = %d %q, want 1 On arrival", id, name)
	}
}
