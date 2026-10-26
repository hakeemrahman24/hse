package emr

import (
	"encoding/json"
	"net/http"
	"testing"
)

type summaryBody struct {
	Total      int            `json:"total"`
	Patients   int            `json:"patients"`
	Positive   int            `json:"positive"`
	Negative   int            `json:"negative"`
	Pending    int            `json:"pending"`
	ByExamType map[string]int `json:"byExamType"`
	By         string         `json:"by"`
	Species    map[string]int `json:"species"`
	Regions    []struct {
		Code     string         `json:"code"`
		Name     string         `json:"name"`
		Count    int            `json:"count"`
		Positive int            `json:"positive"`
		Species  map[string]int `json:"species"`
	} `json:"regions"`
	MoreRegions int `json:"moreRegions"`
}

func (f *fixture) summary(t *testing.T, query string) (int, summaryBody) {
	t.Helper()
	rec := f.do(t, f.api.Summary, http.MethodGet, "/api/malaria-screenings/summary"+query, nil)
	var body summaryBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, body
}

// seedSummary saves four screenings: three in Kabupaten Jayapura (one positive,
// one pending, one negative) and one in Kota Jayapura by a second patient.
func (f *fixture) seedSummary(t *testing.T) {
	t.Helper()
	negative := f.valid() // 2026-10-20, Kab. Jayapura, Negative
	positive := f.valid()
	positive["testDate"], positive["result"] = "2026-10-10", "Pf"
	pending := f.valid()
	pending["examType"] = "BloodSmear"
	delete(pending, "result")
	res, err := f.db.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Siti Aminah','1995-03-03')`)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := res.LastInsertId()
	city := f.valid()
	city["patientId"] = second
	city["regencyCode"], city["districtCode"], city["villageCode"] = "94.71", "94.71.01", "94.71.01.1001"
	for _, body := range []map[string]any{negative, positive, pending, city} {
		if rec := f.create(t, body); rec.Code != http.StatusCreated {
			t.Fatalf("seed: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestSummaryCountsScreeningsPatientsAndResults(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t)
	code, got := f.summary(t, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Total != 4 || got.Patients != 2 || got.Positive != 1 || got.Negative != 2 || got.Pending != 1 {
		t.Fatalf("totals = %+v", got)
	}
	if got.ByExamType["RDT"] != 3 || got.ByExamType["BloodSmear"] != 1 {
		t.Fatalf("by exam type = %v", got.ByExamType)
	}
}

func TestSummaryGroupsByRegionBiggestFirst(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t)
	_, byRegency := f.summary(t, "")
	if byRegency.By != "regency" || len(byRegency.Regions) != 2 ||
		byRegency.Regions[0].Name != "Kab. Jayapura" || byRegency.Regions[0].Count != 3 || byRegency.Regions[0].Positive != 1 ||
		byRegency.Regions[1].Name != "Kota Jayapura" || byRegency.Regions[1].Count != 1 {
		t.Fatalf("by regency = %+v", byRegency)
	}
	_, byDistrict := f.summary(t, "?by=district")
	if byDistrict.By != "district" || len(byDistrict.Regions) != 2 || byDistrict.Regions[0].Name != "Wanggar" || byDistrict.Regions[1].Name != "Abepura" {
		t.Fatalf("by district = %+v", byDistrict)
	}
	_, byVillage := f.summary(t, "?by=village")
	if len(byVillage.Regions) != 2 || byVillage.Regions[0].Name != "Bakungan" {
		t.Fatalf("by village = %+v", byVillage)
	}
}

func TestSummaryDateFilterIsInclusive(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t)
	if _, got := f.summary(t, "?from=2026-10-15"); got.Total != 3 || got.Positive != 0 {
		t.Fatalf("from 15th: %+v", got)
	}
	if _, got := f.summary(t, "?to=2026-10-10"); got.Total != 1 || got.Positive != 1 {
		t.Fatalf("up to the 10th: %+v", got)
	}
	if _, got := f.summary(t, "?from=2026-10-10&to=2026-10-10"); got.Total != 1 {
		t.Fatalf("exactly the 10th: %+v", got)
	}
}

func TestSummaryOfNothingIsZerosAndAnEmptyList(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.api.Summary, http.MethodGet, "/api/malaria-screenings/summary", nil)
	var raw map[string]any
	json.Unmarshal(rec.Body.Bytes(), &raw)
	if raw["total"] != float64(0) || raw["patients"] != float64(0) {
		t.Fatalf("empty summary = %v", raw)
	}
	if list, ok := raw["regions"].([]any); !ok || len(list) != 0 {
		t.Fatalf("regions should be [], got %v", raw["regions"])
	}
}

func TestSummaryRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"?by=province", "?by=name%27%20OR%201=1", "?from=20/10/2026", "?to=garbage"} {
		if code, _ := f.summary(t, q); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
}

func TestSummaryBreaksPositivesDownBySpecies(t *testing.T) {
	f := newFixture(t)
	res, err := f.db.Exec(`INSERT INTO Patient (Name, DateOfBirth) VALUES ('Siti Aminah','1995-03-03')`)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := res.LastInsertId()
	save := func(patient int64, result string, city bool) {
		body := f.valid()
		body["patientId"], body["result"] = patient, result
		if city {
			body["regencyCode"], body["districtCode"], body["villageCode"] = "94.71", "94.71.01", "94.71.01.1001"
		}
		if rec := f.create(t, body); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", result, rec.Code, rec.Body.String())
		}
	}
	// Kabupaten Jayapura: Pv, Pv, Pf, Negative. Kota Jayapura: Po, Mixed.
	save(f.patientID, "Pv", false)
	save(f.patientID, "Pv", false)
	save(second, "Pf", false)
	save(second, "Negative", false)
	save(second, "Po", true)
	save(f.patientID, "Mixed", true)

	_, got := f.summary(t, "")
	want := map[string]int{"Pv": 2, "Pf": 1, "Po": 1, "Mixed": 1}
	for key, n := range want {
		if got.Species[key] != n {
			t.Errorf("species %s = %d, want %d (all: %v)", key, got.Species[key], n, got.Species)
		}
	}
	sum := 0
	for _, n := range got.Species {
		sum += n
	}
	if got.Positive != 5 || sum != got.Positive {
		t.Errorf("positive = %d, species add up to %d; they must agree", got.Positive, sum)
	}
	if got.Negative != 1 || got.Total != 6 {
		t.Errorf("totals = %+v", got)
	}
	if len(got.Regions) != 2 {
		t.Fatalf("regions = %+v", got.Regions)
	}
	kab, kota := got.Regions[0], got.Regions[1]
	if kab.Name != "Kab. Jayapura" || kab.Species["Pv"] != 2 || kab.Species["Pf"] != 1 || kab.Species["Po"] != 0 || kab.Species["Mixed"] != 0 || kab.Positive != 3 {
		t.Errorf("Kab. Jayapura = %+v", kab)
	}
	if kota.Name != "Kota Jayapura" || kota.Species["Po"] != 1 || kota.Species["Mixed"] != 1 || kota.Positive != 2 {
		t.Errorf("Kota Jayapura = %+v", kota)
	}
}

func TestSummarySpeciesAreZerosWhenThereIsNoData(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.api.Summary, http.MethodGet, "/api/malaria-screenings/summary", nil)
	var raw struct {
		Species map[string]int `json:"species"`
	}
	json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, key := range []string{"Pv", "Pf", "Po", "Mixed"} {
		if n, ok := raw.Species[key]; !ok || n != 0 {
			t.Errorf("species %s = %v (present %v), want 0", key, n, ok)
		}
	}
}
