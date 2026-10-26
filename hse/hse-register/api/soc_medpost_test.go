package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeMedpost stands in for hse-medpost's GET /api/directory/employees. It records every request
// so the tests can check what the register sends, and it returns the same shape as the real one
// (including dateOfBirth and a medical result, which the client must ignore).
type fakeMedpost struct {
	rows     []map[string]any
	requests []*http.Request
	status   int
}

func (f *fakeMedpost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r)
	if r.Header.Get("Authorization") != "Bearer secret-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	pid := r.URL.Query().Get("patientId")
	var out []map[string]any
	for _, row := range f.rows {
		name := strings.ToLower(row["name"].(string))
		if q != "" && !strings.Contains(name, q) {
			continue
		}
		if pid != "" && pid != jsonNumber(row["patientId"]) {
			continue
		}
		out = append(out, row)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func person(id int, empID, name, dob, position, company string) map[string]any {
	return map[string]any{
		"patientId": id, "employeeId": empID, "name": name, "dateOfBirth": dob, "workPosition": position,
		"company": company, "resultCriteria": "Fit", "resultNotes": "bring medication", "bloodType": "O+",
	}
}

func newFake(t *testing.T) (*medpostDirectory, *fakeMedpost) {
	t.Helper()
	f := &fakeMedpost{rows: []map[string]any{
		person(1, "E-1", "Andi Wijaya", "1990-03-05", "Welder", "PT Karya"),
		person(2, "E-2", "Andi Wijaya", "1985-07-21", "Driver", "PT LNG"),
		person(3, "E-3", "Budi Santoso", "1988-01-02", "Helper", "PT Karya"),
		person(4, "", "Citra Tanpa ID", "1991-02-02", "Nurse", "PT Karya"),
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return newMedpostDirectory(srv.URL, "secret-token"), f
}

func TestMedpostSearchMapsNamesAndFlagsDuplicates(t *testing.T) {
	d, f := newFake(t)
	got, err := d.Search(context.Background(), "andi", 5)
	if err != nil || len(got) != 2 {
		t.Fatalf("want 2 hits, got %v (%v)", got, err)
	}
	for _, m := range got {
		if !m.Duplicate || m.Company == "" || m.Key == "" {
			t.Errorf("identical names must be flagged with a company: %+v", m)
		}
	}
	if got[0].Key == got[1].Key {
		t.Error("each hit needs its own key")
	}
	req := f.requests[0]
	if req.URL.Path != "/api/directory/employees" || req.URL.Query().Get("q") != "andi" {
		t.Fatalf("unexpected request %s", req.URL)
	}
	one, _ := d.Search(context.Background(), "budi", 5)
	if len(one) != 1 || one[0].Duplicate {
		t.Fatalf("a unique name must not be flagged: %+v", one)
	}
}

func TestMedpostSearchCapsAtMax(t *testing.T) {
	d, f := newFake(t)
	for i := 10; i < 20; i++ {
		f.rows = append(f.rows, person(i, "E-x", "Rudi Tester", "1990-01-01", "", "PT Karya"))
	}
	got, _ := d.Search(context.Background(), "rudi", 5)
	if len(got) != 5 {
		t.Fatalf("want 5, got %d", len(got))
	}
}

// Most Medpost patients have no employee ID, so they must still be found and able to sign in. Their
// post-owner key is then built from the Medpost patient ID.
func TestMedpostSearchOffersPeopleWithoutEmployeeID(t *testing.T) {
	d, _ := newFake(t)
	got, _ := d.Search(context.Background(), "citra", 5)
	if len(got) != 1 {
		t.Fatalf("a person with no employee ID must be offered, got %v", got)
	}
}

func TestMedpostVerifyKeysPeopleWithoutEmployeeIDByPatientID(t *testing.T) {
	d, f := newFake(t)
	f.rows = []map[string]any{person(42, "", "Citra Dewi", "1992-02-02", "Cook", "PT Karya")}
	got, err := d.Verify(context.Background(), "42", time.Date(1992, 2, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || got == nil || got.Key != "P42" || got.Name != "Citra Dewi" {
		t.Fatalf("want key P42, got %+v %v", got, err)
	}
}

func TestMedpostVerifyMatchesDateLocally(t *testing.T) {
	d, f := newFake(t)
	got, err := d.Verify(context.Background(), "3", time.Date(1988, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || got == nil {
		t.Fatalf("right date should verify: %v %v", got, err)
	}
	if got.Key != "E-3" || got.Name != "Budi Santoso" || got.Designation != "Helper" || got.Company != "PT Karya" {
		t.Fatalf("unexpected employee: %+v", got)
	}
	// The typed date of birth must never be put in a URL (it would reach access logs).
	for _, r := range f.requests {
		if strings.Contains(r.URL.String(), "dob") || strings.Contains(r.URL.String(), "1988") {
			t.Fatalf("date of birth leaked into a request URL: %s", r.URL)
		}
	}
	if wrong, err := d.Verify(context.Background(), "3", time.Date(1988, 1, 3, 0, 0, 0, 0, time.UTC)); err != nil || wrong != nil {
		t.Fatalf("wrong date must give nil, nil, got %v %v", wrong, err)
	}
	if none, err := d.Verify(context.Background(), "999", time.Date(1988, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil || none != nil {
		t.Fatalf("unknown person must give nil, nil, got %v %v", none, err)
	}
	if bad, err := d.Verify(context.Background(), "not-a-number", time.Date(1988, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil || bad != nil {
		t.Fatalf("a malformed key must give nil, nil, got %v %v", bad, err)
	}
}

func TestMedpostVerifyMissingDOBNeverMatches(t *testing.T) {
	d, f := newFake(t)
	f.rows = []map[string]any{person(7, "E-7", "Tanpa Tanggal", "", "Helper", "PT Karya")}
	zero := time.Time{}
	if got, err := d.Verify(context.Background(), "7", zero); err != nil || got != nil {
		t.Fatalf("an empty stored date must not match a zero date, got %v %v", got, err)
	}
}

func TestMedpostErrorsAreErrorsNotNoMatch(t *testing.T) {
	d, f := newFake(t)
	f.status = http.StatusInternalServerError
	if _, err := d.Search(context.Background(), "andi", 5); err == nil {
		t.Fatal("a server error must be an error, not an empty result")
	}
	if _, err := d.Verify(context.Background(), "3", time.Date(1988, 1, 2, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("a server error must be an error so the page can say it is unavailable")
	}
	bad := newMedpostDirectory(d.baseURL, "wrong-token")
	if _, err := bad.Search(context.Background(), "andi", 5); err == nil {
		t.Fatal("a rejected token must be an error")
	}
}
