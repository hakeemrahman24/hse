package importer

import (
	"strings"
	"testing"

	"entry-permit/internal/policy"
)

// sheetRow builds a WorkEntryPermitLog row of 98 cells: a valid, fully reviewed Approved
// permit, with any cell replaced by the test.
func sheetRow(change map[int]string) []string {
	row := make([]string, 98)
	base := map[int]string{
		0: "8/7/2026 11:11:51", 1: "WEP-AAAAA", 2: "Rina", 3: "rina@example.com", 5: "Budi Santoso", 7: "1988-03-05", 9: "Indonesia",
		13: "FitWithNote", 14: "04 Oct 2026", 16: "Welder", 17: "PT Maju", 18: "Contractor", 19: "Starway Engineering Indonesia",
		20: "Onshore", 23: "3201", 50: "Welder", 51: "_", 52: "_", 54: "Y", 56: "1234", 60: "5678",
		68: "Jawa Barat", 69: "Bandung", 70: "Coblong", 71: "Dago", 72: "Jl. A 1", 73: "8.12345678901E+11", 77: "Siti", 78: "8.12345678902E+11",
		81: "Sari", 82: "07 Aug 2026 11:15:50", 83: "ok", 84: "Approved", 86: "Manager", 87: "07 Aug 2026 11:17:10", 88: "fine", 89: "Approved",
		91: "Approved", 92: "LEP-AAAAA", 93: "09-20-26",
	}
	for i, v := range base {
		row[i] = v
	}
	for i, v := range change {
		row[i] = v
	}
	return row
}

func fatal(ps []Problem) string {
	var out []string
	for _, p := range ps {
		if p.Fatal {
			out = append(out, p.Msg)
		}
	}
	return strings.Join(out, "; ")
}

func hasProblem(ps []Problem, field string) bool {
	for _, p := range ps {
		if p.Field == field {
			return true
		}
	}
	return false
}

func TestMapWEPMapsAnApprovedPermitAndItsTwoReviewSteps(t *testing.T) {
	m, ps := MapWEP(sheetRow(nil), 5)
	if len(ps) != 0 {
		t.Fatalf("problems on a clean row: %+v", ps)
	}
	p := m.Permit
	checks := map[string][2]string{
		"request id": {p.RequestID, "WEP-AAAAA"}, "lep": {p.LepNo, "LEP-AAAAA"}, "status": {p.Status, "Approved"},
		"name": {p.WorkerName, "Budi Santoso"}, "company": {p.Company, "PT Maju"}, "entity": {p.Entity, "Contractor"},
		"position": {p.WorkPosition, "Welder"}, "parents": {p.Parents, "Starway Engineering Indonesia (1)"},
		"dob": {p.DOB, "1988-03-05"}, "nationality": {p.Nationality, "Indonesia"}, "personal id": {p.PersonalID, "3201"},
		"mcu result": {p.McuResult, "Fit with note"}, "mcu until": {p.McuValidUntil, "2026-10-04"}, "lep until": {p.LepValidUntil, "2026-09-20"},
		"created": {p.CreatedAt, "2026-08-07 11:11:51"}, "declared": {p.DeclaredAt, "2026-08-07 11:11:51"},
		"location": {p.WorkLocation, "Onshore"}, "requester": {p.RequesterEmail, "rina@example.com"},
		"cert required": {p.CertRequired, "Y"}, "canonical": {p.PositionCanonical, "Welder"}, "cert name": {p.CertName, ""},
		"bpjs kes": {p.BpjsKes, "1234"}, "bpjs tk": {p.BpjsTk, "5678"}, "street": {p.Street, "Jl. A 1"}, "village": {p.Village, "Dago"},
		"phone": {p.Phone, "812345678901"}, "emergency name": {p.EmergencyName, "Siti"}, "emergency phone": {p.EmergencyPhone, "812345678902"},
	}
	for what, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", what, c[0], c[1])
		}
	}
	if p.PatientID != 0 {
		t.Errorf("patient id %d: the sheet has none, so the permit is linked by its personal id", p.PatientID)
	}
	want := []Step{
		{Stage: "stage1", Reviewer: "Sari", Decision: "approved", Note: "ok", At: "2026-08-07 11:15:50"},
		{Stage: "final", Reviewer: "Manager", Decision: "approved", Note: "fine", At: "2026-08-07 11:17:10"},
	}
	if len(m.Steps) != 2 || m.Steps[0] != want[0] || m.Steps[1] != want[1] {
		t.Errorf("steps %+v", m.Steps)
	}
}

func TestMapWEPKeepsARejectionAndItsReason(t *testing.T) {
	m, ps := MapWEP(sheetRow(map[int]string{84: "Rejected", 83: "scan is blurry", 86: "", 87: "", 88: "", 89: "", 91: "Rejected", 92: "", 93: ""}), 6)
	if len(ps) != 0 || m.Permit.Status != "Rejected" || m.Permit.LepNo != "" {
		t.Fatalf("permit %+v problems %+v", m.Permit, ps)
	}
	if len(m.Steps) != 1 || m.Steps[0].Decision != "rejected" || m.Steps[0].Note != "scan is blurry" || m.Steps[0].Stage != "stage1" {
		t.Errorf("steps %+v", m.Steps)
	}
}

func TestMapWEPPendingPermitHasNoDecisionYet(t *testing.T) {
	m, ps := MapWEP(sheetRow(map[int]string{84: "Pending", 81: "", 82: "", 83: "", 86: "", 87: "", 88: "", 89: "", 91: "PendingStage1", 92: "", 93: ""}), 7)
	if len(ps) != 0 || len(m.Steps) != 0 || m.Permit.Status != "PendingStage1" {
		t.Fatalf("steps %+v status %q problems %+v", m.Steps, m.Permit.Status, ps)
	}
}

func TestMapWEPRefusesWhatCannotBeImportedSafely(t *testing.T) {
	cases := []struct {
		name   string
		change map[int]string
		want   string
	}{
		{"no request id", map[int]string{1: ""}, "request id"},
		{"id too long", map[int]string{1: "WEP-WAYTOOLONG1"}, "request id"},
		{"unknown status", map[int]string{91: "Maybe"}, "status"},
		{"approved without an LEP", map[int]string{92: ""}, "LEP number"},
		{"approved with an unreadable end date", map[int]string{93: "09-20-266"}, "valid-until"},
		{"approved with no end date", map[int]string{93: "_"}, "valid-until"},
		{"unknown work location", map[int]string{20: "Mars"}, "work location"},
		{"unreadable submit time", map[int]string{0: "yesterday"}, "submit time"},
		{"no name", map[int]string{5: ""}, "name"},
		{"LEP too long", map[int]string{92: "LEP-WAYTOOLONG1"}, "LEP number"},
	}
	for _, c := range cases {
		_, ps := MapWEP(sheetRow(c.change), 9)
		if got := fatal(ps); !strings.Contains(got, c.want) {
			t.Errorf("%s: fatal problems %q, want one about %q", c.name, got, c.want)
		}
	}
}

func TestMapWEPReportsAnUnreadableOptionalDateAndKeepsThePermit(t *testing.T) {
	m, ps := MapWEP(sheetRow(map[int]string{14: "04 Oct 20266"}), 10)
	if fatal(ps) != "" {
		t.Fatalf("an unreadable MCU date must not reject the permit: %v", fatal(ps))
	}
	if m.Permit.McuValidUntil != "" || !hasProblem(ps, "mcu_valid_until") {
		t.Errorf("mcu until %q, problems %+v", m.Permit.McuValidUntil, ps)
	}
}

func TestMapWEPFlagsALossyPhoneAndAMissingBirthDateOnAnApprovedPermit(t *testing.T) {
	_, ps := MapWEP(sheetRow(map[int]string{73: "8.1234567890E+11", 7: ""}), 11)
	if !hasProblem(ps, "phone") || !hasProblem(ps, "dob") {
		t.Errorf("problems %+v: a number that may have lost digits and an Approved permit without a birth date must both be reported", ps)
	}
	if fatal(ps) != "" {
		t.Errorf("neither is a reason to refuse the permit: %v", fatal(ps))
	}
}

func TestMapWEPFixesTheMisspeltNationalityAndTheTwoSpellingsOfFitWithNote(t *testing.T) {
	m, _ := MapWEP(sheetRow(map[int]string{9: "Phillipines", 13: "FitWithNote"}), 12)
	if m.Permit.Nationality != "Philippines" || m.Permit.McuResult != "Fit with note" {
		t.Errorf("nationality %q, mcu %q", m.Permit.Nationality, m.Permit.McuResult)
	}
	m, _ = MapWEP(sheetRow(map[int]string{13: "Fit with note"}), 12)
	if m.Permit.McuResult != "Fit with note" {
		t.Errorf("mcu %q", m.Permit.McuResult)
	}
}

func TestMapWEPReadsTheThreeVisaKinds(t *testing.T) {
	visa, _ := MapWEP(sheetRow(map[int]string{26: "2026-01-01", 27: "2027-01-19"}), 13)
	if p := visa.Permit; p.VisaType != "Visa" || p.VisaIssued != "2026-01-01" || p.VisaExpiry != "2027-01-19" {
		t.Errorf("visa %+v", p)
	}
	itas, _ := MapWEP(sheetRow(map[int]string{29: "C182C42", 30: "2026-12-18"}), 13)
	if p := itas.Permit; p.VisaType != "ITAS" || p.VisaCode != "C182C42" || p.VisaExpiry != "2026-12-18" {
		t.Errorf("itas %+v", p)
	}
	itap, _ := MapWEP(sheetRow(map[int]string{32: "2C2C2D41", 33: "Imigrasi Jakarta", 34: "2031-05-26"}), 13)
	if p := itap.Permit; p.VisaType != "ITAP" || p.VisaCode != "2C2C2D41" || p.VisaIssuedBy != "Imigrasi Jakarta" || p.VisaExpiry != "2031-05-26" {
		t.Errorf("itap %+v", p)
	}
	none, _ := MapWEP(sheetRow(nil), 13)
	if none.Permit.VisaType != "" {
		t.Errorf("an Indonesian worker got visa type %q", none.Permit.VisaType)
	}
}

func TestMapWEPCertificateAndParentsEdgeCases(t *testing.T) {
	m, _ := MapWEP(sheetRow(map[int]string{54: "N#A", 51: "Welder cert", 52: "2027-03-01", 19: "Alpha Co / Beta Co"}), 14)
	p := m.Permit
	if p.CertRequired != "" || p.CertName != "Welder cert" || p.CertUntil != "2027-03-01" {
		t.Errorf("cert %q %q %q", p.CertRequired, p.CertName, p.CertUntil)
	}
	if p.Parents != "Alpha Co (1); Beta Co (1)" {
		t.Errorf("parents %q", p.Parents)
	}
	none, _ := MapWEP(sheetRow(map[int]string{19: ""}), 14)
	if none.Permit.Parents != "" {
		t.Errorf("parents %q for a client", none.Permit.Parents)
	}
}

func TestMapWEPHashIsStableForTheSameRowAndChangesWithAnyField(t *testing.T) {
	a, _ := MapWEP(sheetRow(nil), 5)
	b, _ := MapWEP(sheetRow(nil), 99) // the row number is not part of the content
	c, _ := MapWEP(sheetRow(map[int]string{83: "ok, with a changed note"}), 5)
	if a.Hash == "" || a.Hash != b.Hash || a.Hash == c.Hash {
		t.Errorf("hashes %q %q %q", a.Hash, b.Hash, c.Hash)
	}
}

func TestMapWEPShortRowsAreReadAsBlankNotAsAPanic(t *testing.T) {
	_, ps := MapWEP([]string{"8/7/2026 11:11:51", "WEP-SHORT"}, 3)
	if fatal(ps) == "" {
		t.Error("a row with only two cells must be refused (no name, no status)")
	}
}

func TestMapWEPWarnsWhenTheRequestWasSubmittedAfterItsFirstReview(t *testing.T) {
	// A day-first/month-first mix-up would put the submit time weeks away from the review time.
	_, ps := MapWEP(sheetRow(map[int]string{0: "9/7/2026 11:11:51"}), 15) // 7 Sep: a month after the 7 Aug review
	if !hasProblem(ps, "submit_time") {
		t.Errorf("problems %+v", ps)
	}
	if _, ps = MapWEP(sheetRow(nil), 15); hasProblem(ps, "submit_time") {
		t.Errorf("a normal row was flagged: %+v", ps)
	}
}

func policyDefault() policy.Policy { return policy.Default() }

func TestMapWEPReadsTheRptkaDurationAndReportsOneThatIsNotANumber(t *testing.T) {
	m, ps := MapWEP(sheetRow(map[int]string{37: "60"}), 16)
	if m.Permit.RptkaDays != 60 || hasProblem(ps, "rptka_days") {
		t.Errorf("days %d problems %+v", m.Permit.RptkaDays, ps)
	}
	m, ps = MapWEP(sheetRow(map[int]string{37: "sixty"}), 16)
	if m.Permit.RptkaDays != 0 || !hasProblem(ps, "rptka_days") || fatal(ps) != "" {
		t.Errorf("days %d problems %+v", m.Permit.RptkaDays, ps)
	}
}

func TestMapWEPCutsOverlongTextToItsColumnAndReportsIt(t *testing.T) {
	// MariaDB refuses a value wider than its column, which would stop a whole import.
	long := strings.Repeat("a", 300)
	m, ps := MapWEP(sheetRow(map[int]string{72: long}), 17)
	if len([]rune(m.Permit.Street)) != 240 || !hasProblem(ps, "street") || fatal(ps) != "" {
		t.Errorf("street length %d problems %+v", len(m.Permit.Street), ps)
	}
}
