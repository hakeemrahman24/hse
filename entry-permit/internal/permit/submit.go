package permit

import (
	"errors"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/phone"
)

// Visa kinds a foreign worker can hold (business-rules-permits.md section 7.12).
const (
	VisaKindVisa = "Visa"
	VisaKindItas = "ITAS"
	VisaKindItap = "ITAP"
)

// Form is what the requester typed and uploaded in part 2 of the request.
// Identity, company, position and MCU are not here: they come from medpost.
//
// WHY Has* booleans and not file bytes: the rules only need to know a file was
// supplied. Size and type are checked where the bytes are read (filestore).
type Form struct {
	WorkerName  string
	Foreign     bool
	Declaration bool

	Phone, EmergencyName, EmergencyPhone string

	Province, Regency, District, Village, Street string

	HasIDDoc, HasIDPhoto bool

	// Optional sections. The caller fills the flags from Settings; a section that
	// is not shown is ignored whatever was posted.
	ContractShown, ContractRequired bool
	ContractUntil                   dates.Day
	HasContractDoc                  bool

	BpjsKesShown, BpjsKesRequired bool
	BpjsTkShown, BpjsTkRequired   bool
	BpjsKes, BpjsTk               string // digits, spaces allowed

	// CertNeeded: the position needs a certificate and the setting requires it.
	// CertFixedUntil is set for positions whose certificate never expires, so no
	// date is asked.
	CertNeeded     bool
	CertFixedUntil dates.Day
	CertUntil      dates.Day
	HasCertDoc     bool

	// Foreign workers only.
	VisaType               string
	VisaCode, VisaIssuedBy string
	VisaIssued, VisaExpiry dates.Day
	HasVisaDoc             bool
	RptkaIssued            dates.Day
	RptkaExpiry            dates.Day
	RptkaDays              int
	HasRptkaDoc            bool

	ImmigrationUntil, PoliceUntil, PassportUntil dates.Day
	HasImmigrationDoc, HasPoliceDoc              bool
}

func clean(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// expiredOrBlank reports a missing or malformed date, or one before today (Jakarta day).
func expiredOrBlank(d, today dates.Day) bool {
	if d == "" {
		return true
	}
	if _, err := dates.ParseDay(string(d)); err != nil {
		return true
	}
	return d < today
}

// ValidateForm applies the part-2 checks of section 7 in their documented
// order and returns the first problem as a message fit for the requester.
// Contract, BPJS and certificate checks wait for their Settings toggles.
func ValidateForm(f Form, today dates.Day) error {
	fail := func(msg string) error { return errors.New(msg) }
	switch {
	case !f.Declaration:
		return fail("Tick the declaration to confirm the details are true.")
	case strings.TrimSpace(f.Province) == "" || strings.TrimSpace(f.Regency) == "" || strings.TrimSpace(f.Street) == "":
		return fail("Enter the home address: province and regency from the list, and the street address.")
	case contractProblem(f, today) != "":
		return fail(contractProblem(f, today))
	case bpjsProblem(f) != "":
		return fail(bpjsProblem(f))
	case strings.TrimSpace(f.EmergencyName) == "" || strings.TrimSpace(f.EmergencyPhone) == "":
		return fail("Enter the emergency contact's name and phone number.")
	case clean(f.EmergencyName) == clean(f.WorkerName):
		return fail("The emergency contact must be a different person from the worker.")
	case !phone.Valid(phone.Normalize(f.Phone)):
		return fail("Enter a valid phone number for the worker (for example 081234567890).")
	case !phone.Valid(phone.Normalize(f.EmergencyPhone)):
		return fail("Enter a valid phone number for the emergency contact.")
	case phone.Normalize(f.Phone) == phone.Normalize(f.EmergencyPhone):
		return fail("The emergency phone number must be different from the worker's own.")
	}
	if msg := certProblem(f, today); msg != "" {
		return fail(msg)
	}
	if f.Foreign {
		if err := validateForeign(f, today); err != nil {
			return err
		}
	}
	switch {
	case !f.HasIDDoc:
		return fail("Upload the ID document (KTP, or the passport for a foreign worker).")
	case !f.HasIDPhoto:
		return fail("Upload the ID photo.")
	}
	return nil
}

func validateForeign(f Form, today dates.Day) error {
	fail := func(msg string) error { return errors.New(msg) }
	switch f.VisaType {
	case VisaKindVisa:
		if f.VisaIssued == "" {
			return fail("Enter the visa issue date.")
		}
	case VisaKindItas:
		if strings.TrimSpace(f.VisaCode) == "" {
			return fail("Enter the ITAS code.")
		}
	case VisaKindItap:
		if strings.TrimSpace(f.VisaCode) == "" {
			return fail("Enter the ITAP code.")
		}
		if strings.TrimSpace(f.VisaIssuedBy) == "" {
			return fail("Enter the office that issued the ITAP.")
		}
	default:
		return fail("Choose the work visa type: Visa, ITAS or ITAP.")
	}
	switch {
	case expiredOrBlank(f.VisaExpiry, today):
		return fail("The work visa expiry date is missing or already passed.")
	case !f.HasVisaDoc:
		return fail("Upload the work visa document.")
	case f.RptkaDays <= 0 || f.RptkaIssued == "":
		return fail("Enter the RPTKA issue date and duration in days.")
	case expiredOrBlank(f.RptkaExpiry, today):
		return fail("The RPTKA expiry date is missing or already passed.")
	case !f.HasRptkaDoc:
		return fail("Upload the RPTKA document.")
	case expiredOrBlank(f.ImmigrationUntil, today):
		return fail("The immigration reporting letter's valid-until date is missing or already passed.")
	case !f.HasImmigrationDoc:
		return fail("Upload the immigration reporting letter.")
	case expiredOrBlank(f.PoliceUntil, today):
		return fail("The police letter's valid-until date is missing or already passed.")
	case !f.HasPoliceDoc:
		return fail("Upload the police letter.")
	case expiredOrBlank(f.PassportUntil, today):
		return fail("The passport valid-until date is missing or already passed.")
	}
	return nil
}

// ValidityInput builds the input for ResolveValidity. Only the date that
// belongs to the chosen visa kind is passed, and an Indonesian's foreign dates
// are dropped.
func (f Form) ValidityInput(mcuValidUntil dates.Day) ValidityInput {
	in := ValidityInput{Foreign: f.Foreign, McuValidUntil: mcuValidUntil}
	if f.ContractShown {
		in.ContractValidUntil = f.ContractUntil
	}
	if !f.Foreign {
		return in
	}
	switch f.VisaType {
	case VisaKindVisa:
		in.Visa = f.VisaExpiry
	case VisaKindItas:
		in.Itas = f.VisaExpiry
	case VisaKindItap:
		in.Itap = f.VisaExpiry
	}
	in.PassportValidUntil = f.PassportUntil
	return in
}

// contractProblem: business-rules-permits.md section 7.6. Required only while the
// section is shown and required; a date that is given must not be in the past.
func contractProblem(f Form, today dates.Day) string {
	if !f.ContractShown {
		return ""
	}
	if f.ContractRequired {
		switch {
		case !f.HasContractDoc:
			return "Upload the contract document."
		case f.ContractUntil == "":
			return "Enter the contract validity date."
		}
	}
	if f.ContractUntil != "" && expiredOrBlank(f.ContractUntil, today) {
		return "The contract validity date is invalid or already passed."
	}
	return ""
}

// bpjsDigits keeps what a BPJS number may hold: digits, typed with or without spaces.
func bpjsDigits(s string) (digits string, ok bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		default:
			return "", false
		}
	}
	d := b.String()
	return d, len(d) >= bpjsMinDigits && len(d) <= bpjsMaxDigits
}

const (
	bpjsMinDigits = 8
	bpjsMaxDigits = 20
)

// bpjsProblem: section 7.7. Indonesians only, and only when the toggle says so.
func bpjsProblem(f Form) string {
	if f.Foreign {
		return ""
	}
	for _, c := range []struct {
		shown, required bool
		value, label    string
	}{
		{f.BpjsKesShown, f.BpjsKesRequired, f.BpjsKes, "BPJS Kesehatan"},
		{f.BpjsTkShown, f.BpjsTkRequired, f.BpjsTk, "BPJS Ketenagakerjaan"},
	} {
		if !c.shown {
			continue
		}
		if strings.TrimSpace(c.value) == "" {
			if c.required {
				return "Enter the " + c.label + " number."
			}
			continue
		}
		if _, ok := bpjsDigits(c.value); !ok {
			return "The " + c.label + " number must be digits only (8 to 20 digits)."
		}
	}
	return ""
}

// certProblem: section 7.9. The file is always needed; the date is typed unless
// the position's certificate never expires.
func certProblem(f Form, today dates.Day) string {
	if !f.CertNeeded {
		return ""
	}
	switch {
	case !f.HasCertDoc:
		return "Upload the certificate of competence."
	case f.CertFixedUntil != "":
		return ""
	case f.CertUntil == "":
		return "Enter the certificate validity date."
	case expiredOrBlank(f.CertUntil, today):
		return "The certificate validity date is invalid or already passed."
	}
	return ""
}
