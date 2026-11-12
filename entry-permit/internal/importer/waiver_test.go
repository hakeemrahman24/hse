package importer

import (
	"strings"
	"testing"
)

// waiverRow builds a WaiverLog row of 91 cells: a finished Government visit with all three
// approvals, with any cell replaced by the test.
func waiverRow(change map[int]string) []string {
	row := make([]string, 91)
	base := map[int]string{
		0: "8/7/2026 10:00:00", 1: "WVR-AAAAA", 2: "WVR-AAAAA", 4: "Government Official", 5: "Tamu@Example.com", 7: "Indonesia",
		8: "3201020304900001", 9: "Tamu Satu", 10: "Male", 11: "1990-04-02", 14: "Auditor", 15: "HSE", 16: "Dinas Kerja",
		17: "Site A", 18: "Audit", 19: "3", 20: "2026-11-10", 21: "2026-11-12", 24: "risk", 25: "precaution", 26: "prevent",
		64: "Yes", 69: "Approved", 70: "08-07-26", 71: "Dr Ayu", 73: "Approved", 74: "08-07-26", 75: "Sari",
		77: "Manager", 78: "08-08-26", 79: "Manager", 82: "Approved", 83: "TKT-WVR01", 88: "8.12345678901E+11",
	}
	for i, v := range base {
		row[i] = v
	}
	for i, v := range change {
		row[i] = v
	}
	return row
}

func TestMapWaiverMapsAFinishedVisitAndItsThreeApprovals(t *testing.T) {
	m, ps := MapWaiver(waiverRow(nil), 5)
	if len(ps) != 0 {
		t.Fatalf("problems on a clean row: %+v", ps)
	}
	w := m.Waiver
	checks := map[string][2]string{
		"id": {w.RequestID, "WVR-AAAAA"}, "status": {w.Status, "Approved"}, "type": {w.ApplicantType, "Government"},
		"company": {w.Company, "Dinas Kerja"}, "name": {w.Name, "Tamu Satu"}, "gender": {w.Gender, "Male"}, "dob": {w.DOB, "1990-04-02"},
		"nik": {w.NIK, "3201020304900001"}, "position": {w.Position, "Auditor"}, "location": {w.VisitLocation, "Site A"},
		"from": {w.VisitFrom, "2026-11-10"}, "to": {w.VisitTo, "2026-11-12"}, "email": {w.RequesterEmail, "tamu@example.com"},
		"phone": {w.Phone, "812345678901"}, "created": {w.CreatedAt, "2026-08-07 10:00:00"}, "declared": {w.DeclaredAt, "2026-08-07 10:00:00"},
		"risk": {w.Risk, "risk"},
	}
	for what, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", what, c[0], c[1])
		}
	}
	if w.Days != 3 || w.TakesMedicine {
		t.Errorf("days %d medicine %v", w.Days, w.TakesMedicine)
	}
	want := []Step{
		{Stage: "doctor", Reviewer: "Dr Ayu", Decision: "approved", At: "2026-08-07 00:00:00"},
		{Stage: "stage1", Reviewer: "Sari", Decision: "approved", At: "2026-08-07 00:00:00"},
		{Stage: "final", Reviewer: "Manager", Decision: "approved", At: "2026-08-08 00:00:00"},
	}
	if len(m.Steps) != 3 || m.Steps[0] != want[0] || m.Steps[1] != want[1] || m.Steps[2] != want[2] {
		t.Errorf("steps %+v", m.Steps)
	}
}

func TestMapWaiverKnowsBothApplicantGroupsAndMedicine(t *testing.T) {
	m, _ := MapWaiver(waiverRow(map[int]string{4: "Company Survey", 27: "Metformin", 28: "500 mg"}), 6)
	if m.Waiver.ApplicantType != "VisitorCompany" || !m.Waiver.TakesMedicine || m.Waiver.Medicine1 != "Metformin" || m.Waiver.Dosage1 != "500 mg" {
		t.Errorf("%+v", m.Waiver)
	}
	_, ps := MapWaiver(waiverRow(map[int]string{4: "Aliens"}), 6)
	if fatal(ps) == "" {
		t.Error("an unknown visitor group was accepted")
	}
}

func TestMapWaiverRefusesWhatCannotBeImportedSafelyAndReportsTheRest(t *testing.T) {
	cases := []struct {
		name   string
		change map[int]string
		want   string
	}{
		{"no id", map[int]string{1: "", 2: ""}, "request id"},
		{"unknown status", map[int]string{82: "Maybe"}, "status"},
		{"unreadable visit start", map[int]string{20: "soon"}, "visit"},
		{"unreadable visit end", map[int]string{21: ""}, "visit"},
		{"no name", map[int]string{9: ""}, "name"},
		{"unreadable submit time", map[int]string{0: "x"}, "submit time"},
	}
	for _, c := range cases {
		_, ps := MapWaiver(waiverRow(c.change), 7)
		if got := fatal(ps); got == "" || !strings.Contains(strings.ToLower(got), c.want) {
			t.Errorf("%s: fatal %q, want one about %q", c.name, got, c.want)
		}
	}
	m, ps := MapWaiver(waiverRow(map[int]string{11: "19900-04-02"}), 7) // the real sheet has one 5-digit year
	if fatal(ps) != "" || m.Waiver.DOB != "" || !hasProblem(ps, "dob") {
		t.Errorf("dob typo: dob %q problems %+v", m.Waiver.DOB, ps)
	}
}

func TestMapWaiverFallsBackToTheRequestNumberAndComputesDaysWhenBlank(t *testing.T) {
	m, ps := MapWaiver(waiverRow(map[int]string{2: "", 19: ""}), 8)
	if fatal(ps) != "" || m.Waiver.RequestID != "WVR-AAAAA" || m.Waiver.Days != 3 {
		t.Errorf("id %q days %d problems %+v", m.Waiver.RequestID, m.Waiver.Days, ps)
	}
}

// The sheet's Risk Exposure and Health Declaration cells become the one-letter-per-question
// answers; a blank card is "not recorded" without a complaint, an odd or half-blank one is
// reported and left unrecorded rather than guessed.
func TestMapWaiverReadsTheRiskAndHealthAnswers(t *testing.T) {
	change := map[int]string{29: "Vitamin C", 30: "100", 33: "After meals", 35: "Yes", 36: "N/A", 54: "yes"}
	for i := 34; i <= 42; i++ {
		if _, set := change[i]; !set {
			change[i] = "No"
		}
	}
	for i := 45; i <= 59; i++ {
		if _, set := change[i]; !set {
			change[i] = "No"
		}
	}
	m, ps := MapWaiver(waiverRow(change), 5)
	if len(ps) != 0 {
		t.Fatalf("problems: %+v", ps)
	}
	w := m.Waiver
	if w.RiskAnswers != "NYANNNNNN" || w.HealthAnswers != "NNNNNNNNNYNNNNN" {
		t.Errorf("answers %q %q", w.RiskAnswers, w.HealthAnswers)
	}
	if w.Medicine2 != "Vitamin C" || w.Dosage2 != "100" || w.MedicineNotes != "After meals" {
		t.Errorf("medicines %+v", w)
	}
	// An old row with no answers at all imports quietly as "not recorded".
	if m, ps := MapWaiver(waiverRow(nil), 6); len(ps) != 0 || m.Waiver.RiskAnswers != "" || m.Waiver.HealthAnswers != "" {
		t.Errorf("blank card: %q %q %+v", m.Waiver.RiskAnswers, m.Waiver.HealthAnswers, ps)
	}
	// A card with a cell that is not Yes / No / N/A is reported and left unrecorded.
	m, ps = MapWaiver(waiverRow(map[int]string{34: "Maybe"}), 7)
	if len(ps) != 1 || ps[0].Fatal || m.Waiver.RiskAnswers != "" {
		t.Errorf("odd answer: %+v %q", ps, m.Waiver.RiskAnswers)
	}
	// So is a half-blank card.
	_, ps = MapWaiver(waiverRow(map[int]string{34: "No"}), 8)
	if len(ps) != 1 || ps[0].Fatal || ps[0].Field != "risk_answers" {
		t.Errorf("half blank: %+v", ps)
	}
}
