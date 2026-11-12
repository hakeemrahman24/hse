package permit

import (
	"strings"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

const today dates.Day = "2026-11-06"

// goodWNI is a form that passes for an Indonesian worker; each case below
// breaks exactly one thing.
func goodWNI() Form {
	return Form{
		WorkerName: "Budi Santoso", Declaration: true,
		Phone: "081234567890", EmergencyName: "Siti Santoso", EmergencyPhone: "081298765432",
		Province: "Aceh", Regency: "Kabupaten Aceh Selatan", Street: "Jl. Merdeka 1",
		HasIDDoc: true, HasIDPhoto: true,
	}
}

func goodForeign() Form {
	f := goodWNI()
	f.Foreign = true
	f.VisaType = "ITAS"
	f.VisaCode = "ITAS-123"
	f.VisaExpiry = "2027-05-01"
	f.HasVisaDoc = true
	f.RptkaIssued, f.RptkaDays, f.RptkaExpiry = "2026-01-01", 365, "2027-01-01"
	f.HasRptkaDoc = true
	f.ImmigrationUntil, f.HasImmigrationDoc = "2027-03-01", true
	f.PoliceUntil, f.HasPoliceDoc = "2027-03-01", true
	f.PassportUntil = "2028-01-01"
	return f
}

func TestValidateFormAcceptsGoodForms(t *testing.T) {
	if err := ValidateForm(goodWNI(), today); err != nil {
		t.Errorf("WNI: %v", err)
	}
	if err := ValidateForm(goodForeign(), today); err != nil {
		t.Errorf("foreign: %v", err)
	}
}

func TestValidateFormRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Form)
		want   string
	}{
		{"no declaration", func(f *Form) { f.Declaration = false }, "declaration"},
		{"no emergency name", func(f *Form) { f.EmergencyName = " " }, "emergency contact"},
		{"emergency is the worker", func(f *Form) { f.EmergencyName = "budi  SANTOSO" }, "different"},
		{"bad phone", func(f *Form) { f.Phone = "0812345" }, "phone"},
		{"bad emergency phone", func(f *Form) { f.EmergencyPhone = "12" }, "emergency"},
		{"same phones", func(f *Form) { f.EmergencyPhone = "+62 812-3456-7890" }, "different"},
		{"no province", func(f *Form) { f.Province = "" }, "address"},
		{"no street", func(f *Form) { f.Street = "  " }, "address"},
		{"no id doc", func(f *Form) { f.HasIDDoc = false }, "id document"},
		{"no photo", func(f *Form) { f.HasIDPhoto = false }, "photo"},
	}
	for _, c := range cases {
		f := goodWNI()
		c.mutate(&f)
		err := ValidateForm(f, today)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), c.want) {
			t.Errorf("%s: got %v, want a message with %q", c.name, err, c.want)
		}
	}
}

func TestValidateFormForeignDocuments(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Form)
		want   string
	}{
		{"no visa type", func(f *Form) { f.VisaType = "" }, "visa"},
		{"odd visa type", func(f *Form) { f.VisaType = "Tourist" }, "visa"},
		{"visa expired", func(f *Form) { f.VisaExpiry = "2026-11-05" }, "visa"},
		{"visa file missing", func(f *Form) { f.HasVisaDoc = false }, "visa"},
		{"itas without code", func(f *Form) { f.VisaCode = "" }, "itas"},
		{"rptka expired", func(f *Form) { f.RptkaExpiry = "2026-01-01" }, "rptka"},
		{"rptka no duration", func(f *Form) { f.RptkaDays = 0 }, "rptka"},
		{"rptka file missing", func(f *Form) { f.HasRptkaDoc = false }, "rptka"},
		{"immigration expired", func(f *Form) { f.ImmigrationUntil = "2026-10-01" }, "immigration"},
		{"immigration file missing", func(f *Form) { f.HasImmigrationDoc = false }, "immigration"},
		{"police expired", func(f *Form) { f.PoliceUntil = "2026-10-01" }, "police"},
		{"police file missing", func(f *Form) { f.HasPoliceDoc = false }, "police"},
		{"passport expired", func(f *Form) { f.PassportUntil = "2026-11-05" }, "passport"},
		{"passport blank", func(f *Form) { f.PassportUntil = "" }, "passport"},
	}
	for _, c := range cases {
		f := goodForeign()
		c.mutate(&f)
		err := ValidateForm(f, today)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), c.want) {
			t.Errorf("%s: got %v, want a message with %q", c.name, err, c.want)
		}
	}
}

func TestValidateFormVisaTypes(t *testing.T) {
	visa := goodForeign()
	visa.VisaType, visa.VisaCode, visa.VisaIssued = "Visa", "", "2026-02-01"
	if err := ValidateForm(visa, today); err != nil {
		t.Errorf("Visa with an issue date: %v", err)
	}
	visa.VisaIssued = ""
	if err := ValidateForm(visa, today); err == nil {
		t.Error("Visa without an issue date was accepted")
	}
	itap := goodForeign()
	itap.VisaType, itap.VisaCode, itap.VisaIssuedBy = "ITAP", "ITAP-9", "Imigrasi Jakarta"
	if err := ValidateForm(itap, today); err != nil {
		t.Errorf("ITAP: %v", err)
	}
	itap.VisaIssuedBy = ""
	if err := ValidateForm(itap, today); err == nil {
		t.Error("ITAP without an issuing office was accepted")
	}
}

func TestValidateFormIgnoresForeignFieldsForIndonesians(t *testing.T) {
	f := goodWNI()
	f.VisaType, f.VisaExpiry, f.PassportUntil = "ITAS", "2020-01-01", "2020-01-01"
	if err := ValidateForm(f, today); err != nil {
		t.Errorf("an Indonesian's stray visa dates must be ignored: %v", err)
	}
}

func TestFormValidityInputUsesTheRightVisaDate(t *testing.T) {
	pol := policy.Default()
	// MCU 2027-06-30 minus the 2-week grace is 2027-06-16.
	f := goodForeign()
	day, reason, err := ResolveValidity(pol, f.ValidityInput("2027-06-30"))
	if err != nil || day != "2027-05-01" || reason != ReasonVisa {
		t.Errorf("got %s %s %v, want 2027-05-01 visa", day, reason, err)
	}
	f.VisaType, f.VisaExpiry = "Visa", "2027-08-01"
	day, reason, _ = ResolveValidity(pol, f.ValidityInput("2027-06-30"))
	if day != "2027-06-16" || reason != ReasonMCU {
		t.Errorf("got %s %s, want the MCU date 2027-06-16", day, reason)
	}
	w := goodWNI()
	w.VisaExpiry, w.PassportUntil = "2020-01-01", "2020-01-01"
	day, reason, _ = ResolveValidity(pol, w.ValidityInput("2027-06-30"))
	if day != "2027-06-16" || reason != ReasonMCU {
		t.Errorf("an Indonesian must ignore visa/passport dates, got %s %s", day, reason)
	}
}

func TestValidateFormContract(t *testing.T) {
	f := goodWNI()
	f.ContractShown, f.ContractRequired = true, true
	if err := ValidateForm(f, today); err == nil || !strings.Contains(strings.ToLower(err.Error()), "contract") {
		t.Errorf("a required contract was not asked for: %v", err)
	}
	f.HasContractDoc = true
	if err := ValidateForm(f, today); err == nil || !strings.Contains(strings.ToLower(err.Error()), "contract") {
		t.Errorf("a required contract without a date was accepted: %v", err)
	}
	f.ContractUntil = "2026-11-05"
	if err := ValidateForm(f, today); err == nil {
		t.Error("a contract that ended yesterday was accepted")
	}
	f.ContractUntil = "2026-11-06" // today counts as valid
	if err := ValidateForm(f, today); err != nil {
		t.Errorf("contract valid until today: %v", err)
	}

	// Shown but not required: blank is fine, a given date is still checked.
	opt := goodWNI()
	opt.ContractShown = true
	if err := ValidateForm(opt, today); err != nil {
		t.Errorf("optional contract left blank: %v", err)
	}
	opt.ContractUntil = "2020-01-01"
	if err := ValidateForm(opt, today); err == nil {
		t.Error("an optional contract with a past date was accepted")
	}

	// Hidden: whatever was posted is ignored.
	hidden := goodWNI()
	hidden.ContractRequired, hidden.ContractUntil = true, "2020-01-01"
	if err := ValidateForm(hidden, today); err != nil {
		t.Errorf("a hidden contract must be ignored: %v", err)
	}
}

func TestValidateFormBpjs(t *testing.T) {
	f := goodWNI()
	f.BpjsKesShown, f.BpjsKesRequired, f.BpjsTkShown, f.BpjsTkRequired = true, true, true, true
	if err := ValidateForm(f, today); err == nil || !strings.Contains(err.Error(), "Kesehatan") {
		t.Errorf("BPJS Kesehatan not asked for: %v", err)
	}
	f.BpjsKes = "0001234567890"
	if err := ValidateForm(f, today); err == nil || !strings.Contains(err.Error(), "Ketenagakerjaan") {
		t.Errorf("BPJS Ketenagakerjaan not asked for: %v", err)
	}
	f.BpjsTk = "ab12"
	if err := ValidateForm(f, today); err == nil {
		t.Error("a BPJS number with letters was accepted")
	}
	f.BpjsTk = "2100 1234 5678"
	if err := ValidateForm(f, today); err != nil {
		t.Errorf("a number typed with spaces: %v", err)
	}
	// Foreign workers have no BPJS.
	fo := goodForeign()
	fo.BpjsKesShown, fo.BpjsKesRequired = true, true
	if err := ValidateForm(fo, today); err != nil {
		t.Errorf("BPJS must not be asked of a foreign worker: %v", err)
	}
	// Shown, not required: blank is fine.
	opt := goodWNI()
	opt.BpjsKesShown, opt.BpjsTkShown = true, true
	if err := ValidateForm(opt, today); err != nil {
		t.Errorf("optional BPJS left blank: %v", err)
	}
}

func TestValidateFormCertificate(t *testing.T) {
	f := goodWNI()
	f.CertNeeded = true
	if err := ValidateForm(f, today); err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Errorf("certificate not asked for: %v", err)
	}
	f.HasCertDoc = true
	if err := ValidateForm(f, today); err == nil {
		t.Error("a certificate without a validity date was accepted")
	}
	f.CertUntil = "2026-11-05"
	if err := ValidateForm(f, today); err == nil {
		t.Error("an expired certificate was accepted")
	}
	f.CertUntil = "2027-05-01"
	if err := ValidateForm(f, today); err != nil {
		t.Errorf("good certificate: %v", err)
	}
	// Paramedic, Nurse, Doctor: the date is fixed, only the file is needed.
	p := goodWNI()
	p.CertNeeded, p.CertFixedUntil, p.HasCertDoc = true, "2090-12-31", true
	if err := ValidateForm(p, today); err != nil {
		t.Errorf("perpetual certificate: %v", err)
	}
	p.HasCertDoc = false
	if err := ValidateForm(p, today); err == nil {
		t.Error("a perpetual certificate still needs its file")
	}
	// Not needed: nothing asked.
	if err := ValidateForm(goodWNI(), today); err != nil {
		t.Errorf("no certificate needed: %v", err)
	}
}

func TestFormValidityInputUsesTheContractDate(t *testing.T) {
	f := goodWNI()
	f.ContractShown, f.ContractUntil = true, "2027-03-01"
	day, reason, err := ResolveValidity(policy.Default(), f.ValidityInput("2027-06-30"))
	if err != nil || day != "2027-03-01" || reason != ReasonContract {
		t.Errorf("got %s %s %v, want the contract date", day, reason, err)
	}
	f.ContractShown = false // hidden: the typed date must not cap the permit
	day, reason, _ = ResolveValidity(policy.Default(), f.ValidityInput("2027-06-30"))
	if day != "2027-06-16" || reason != ReasonMCU {
		t.Errorf("a hidden contract capped the permit: %s %s", day, reason)
	}
}
