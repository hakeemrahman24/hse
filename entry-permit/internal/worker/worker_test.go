package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A date of birth typed as run-together digits or with other separators is read the same way.
func TestParseDOBReadsRunTogetherDigitsAndOtherSeparators(t *testing.T) {
	for _, in := range []string{"24071983", "24/071983", "24-07-1983", "24.07.1983", " 24 07 1983 "} {
		got, err := ParseDOB(in, "2026-11-08")
		if err != nil || got != "1983-07-24" {
			t.Errorf("ParseDOB(%q) = %q, %v; want 1983-07-24", in, got, err)
		}
	}
	for _, in := range []string{"20240724", "31041983", "2407198", "abc"} { // not a real day, or not a date
		if got, err := ParseDOB(in, "2026-11-08"); err == nil {
			t.Errorf("ParseDOB(%q) = %q; want an error", in, got)
		}
	}
}

func TestParseDOB(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"05/03/1988", "1988-03-05", true},
		{"5/3/1988", "1988-03-05", true},
		{" 31/12/1999 ", "1999-12-31", true},
		{"29/02/2000", "2000-02-29", true},
		{"29/02/2001", "", false}, // not a real day
		{"31/04/1990", "", false},
		{"1988-03-05", "", false},          // wrong format
		{"05-03-1988", "1988-03-05", true}, // dashes are tidied to slashes (owner decision 2026-11-08)
		{"05/03/88", "", false},
		{"00/01/1990", "", false},
		{"01/13/1990", "", false},
		{"01/01/1899", "", false}, // implausible
		{"", "", false},
	}
	for _, c := range cases {
		got, err := ParseDOB(c.in, "2026-11-06")
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParseDOB(%q) = %q, %v; want %q ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
	if _, err := ParseDOB("07/11/2026", "2026-11-06"); err == nil {
		t.Error("a future date of birth was accepted")
	}
}

// fakeMedpost answers /api/directory/employees like hse-medpost does.
func fakeMedpost(t *testing.T, rows []map[string]any) (*httptest.Server, *http.Request) {
	t.Helper()
	var last http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

var sample = []map[string]any{
	{"name": "Budi Santoso", "dateOfBirth": "1988-03-05", "company": "PT Maju", "workPosition": "Welder", "resultValidUntil": "2027-01-31"},
	{"name": "budi  santoso", "dateOfBirth": "1990-07-01", "company": "PT Lain", "workPosition": "Rigger"},
	{"name": "Budi Hartono", "dateOfBirth": "1985-01-01", "company": "PT Maju", "workPosition": "Clerk"},
}

func TestNamesNeedsFourCharactersAndDedupes(t *testing.T) {
	srv, seen := fakeMedpost(t, sample)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	if got, err := s.Names(context.Background(), "bud"); err != nil || got != nil {
		t.Fatalf("3 characters: %v %v", got, err)
	}
	if seen.URL != nil {
		t.Fatal("a short query must not reach medpost")
	}
	got, err := s.Names(context.Background(), " budi ")
	if err != nil {
		t.Fatal(err)
	}
	// Two rows differ only by case/spacing: one entry; sorted.
	if len(got) != 2 || got[0] != "Budi Hartono" || got[1] != "Budi Santoso" {
		t.Fatalf("names = %v", got)
	}
	if seen.URL.Query().Get("q") != "budi" || seen.Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("request = %v", seen.URL)
	}
}

func TestFindMatchesNameAndDOBExactly(t *testing.T) {
	srv, seen := fakeMedpost(t, sample)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	p, ok, err := s.Find(context.Background(), "budi  SANTOSO", "1988-03-05")
	if err != nil || !ok {
		t.Fatalf("Find = %v %v %v", p, ok, err)
	}
	if p.Company != "PT Maju" || p.Position != "Welder" || p.McuValidUntil != "2027-01-31" || p.Name != "Budi Santoso" {
		t.Fatalf("person = %+v", p)
	}
	if seen.URL.Query().Get("dob") != "1988-03-05" {
		t.Fatalf("dob not sent: %v", seen.URL)
	}
	// Rows medpost returns for a different name (a partial match) must not count.
	if _, ok, _ := s.Find(context.Background(), "Budi", "1988-03-05"); ok {
		t.Fatal("partial name matched")
	}
	if _, ok, _ := s.Find(context.Background(), "Nobody Here", "1988-03-05"); ok {
		t.Fatal("unknown name matched")
	}
	// A row with another date of birth must not match even if medpost sent it.
	if _, ok, _ := s.Find(context.Background(), "Budi Hartono", "1988-03-05"); ok {
		t.Fatal("wrong date of birth matched")
	}
}

func TestNotConfiguredAndMedpostDown(t *testing.T) {
	if _, err := (HTTPSource{}).Names(context.Background(), "budi"); err == nil {
		t.Error("unconfigured source returned no error")
	}
	srv, _ := fakeMedpost(t, sample)
	bad := HTTPSource{BaseURL: srv.URL, Token: "wrong"}
	if _, err := bad.Names(context.Background(), "budi"); err == nil {
		t.Error("401 returned no error")
	}
	if _, _, err := bad.Find(context.Background(), "Budi Santoso", "1988-03-05"); err == nil {
		t.Error("401 on Find returned no error")
	}
}

func TestEligibleIsAnExactNameMatch(t *testing.T) {
	srv, _ := fakeMedpost(t, sample)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	for name, want := range map[string]bool{
		"Budi Santoso":    true,
		" budi  SANTOSO ": true,  // spacing and case do not matter
		"Budi":            false, // a partial name is not a person
		"Nobody Here":     false,
	} {
		got, err := s.Eligible(context.Background(), name)
		if err != nil || got != want {
			t.Errorf("Eligible(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
	if _, err := (HTTPSource{}).Eligible(context.Background(), "Budi Santoso"); err == nil {
		t.Error("unconfigured source returned no error")
	}
}

func TestPickReturnsTheFullRecord(t *testing.T) {
	rows := []map[string]any{{
		"patientId": 42, "personalId": " 3174010101010001 ", "name": "Chen  Wei", "gender": "M", "dateOfBirth": "1990-02-03",
		"nationality": "China", "company": "PT Maju", "companyEntity": "Contractor",
		"parents": []map[string]any{{"name": "PT Induk", "depth": 1}}, "workPosition": "Welder",
		"checkUpId": "CU-9", "resultCriteria": "Fit with note", "resultValidUntil": "2027-01-31", "resultNotes": " Wear corrective lenses ",
	}}
	srv, _ := fakeMedpost(t, rows)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	e, ok, err := s.Pick(context.Background(), "chen wei", "1990-02-03")
	if err != nil || !ok {
		t.Fatalf("Pick = %v %v", ok, err)
	}
	if e.PatientID != 42 || e.PersonalID != "3174010101010001" || e.Name != "Chen Wei" || e.Entity != "Contractor" ||
		len(e.Parents) != 1 || e.Parents[0].Name != "PT Induk" || e.CheckUpID != "CU-9" ||
		e.McuResult != "Fit with note" || e.McuValidUntil != "2027-01-31" || e.McuNote != "Wear corrective lenses" {
		t.Fatalf("record = %+v", e)
	}
	if !e.IsForeign() {
		t.Error("a Chinese national must count as foreign")
	}
}

func TestPickRefusesAnAmbiguousMatch(t *testing.T) {
	// Two records with the same name and date of birth: never guess which one.
	rows := []map[string]any{
		{"patientId": 1, "name": "Budi Santoso", "dateOfBirth": "1988-03-05"},
		{"patientId": 2, "name": "Budi Santoso", "dateOfBirth": "1988-03-05"},
	}
	srv, _ := fakeMedpost(t, rows)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	if _, ok, err := s.Pick(context.Background(), "Budi Santoso", "1988-03-05"); ok || err != nil {
		t.Fatalf("ambiguous Pick = %v %v", ok, err)
	}
}

func TestIsForeign(t *testing.T) {
	for nat, want := range map[string]bool{"Indonesia": false, " indonesia ": false, "INDONESIA": false, "China": true, "": true} {
		if got := (Employee{Nationality: nat}).IsForeign(); got != want {
			t.Errorf("IsForeign(%q) = %v want %v", nat, got, want)
		}
	}
}

func TestOnlyPickIncludingExpiredAsksMedpostForExpiredCheckups(t *testing.T) {
	srv, seen := fakeMedpost(t, sample)
	s := HTTPSource{BaseURL: srv.URL, Token: "tok"}
	if _, _, err := s.Pick(context.Background(), "Budi Santoso", "1988-03-05"); err != nil {
		t.Fatal(err)
	}
	if got := seen.URL.Query().Get("includeExpired"); got != "" {
		t.Errorf("Pick asked for expired checkups: includeExpired=%q", got)
	}
	e, ok, err := s.PickIncludingExpired(context.Background(), "Budi Santoso", "1988-03-05")
	if err != nil || !ok || e.Name != "Budi Santoso" {
		t.Fatalf("PickIncludingExpired = %+v %v %v", e, ok, err)
	}
	if got := seen.URL.Query().Get("includeExpired"); got != "1" {
		t.Errorf("includeExpired = %q, want 1", got)
	}
	// The name search and the officer check never ask for expired checkups.
	if _, err := s.Names(context.Background(), "budi"); err != nil {
		t.Fatal(err)
	}
	if seen.URL.Query().Get("includeExpired") != "" {
		t.Error("Names asked for expired checkups")
	}
	if _, err := s.Eligible(context.Background(), "Budi Santoso"); err != nil {
		t.Fatal(err)
	}
	if seen.URL.Query().Get("includeExpired") != "" {
		t.Error("Eligible asked for expired checkups")
	}
}
