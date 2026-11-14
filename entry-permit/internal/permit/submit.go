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

// FieldError is a validation message that also names the form field it is about (the input's name attribute),
// so the page can open the right step of the step-by-step form and put the cursor on that field.
// Error() is the message alone, so everything that only prints the error is unchanged.
type FieldError struct{ Field, Msg string }

func (e FieldError) Error() string { return e.Msg }

// FieldOf returns the field name an error carries, or "" for an error that is not about one field.
func FieldOf(err error) string {
	var fe FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func bad(field, msg string) error { return FieldError{Field: field, Msg: msg} }

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
	if !f.Declaration {
		return bad("declaration", "Tick the declaration to confirm the details are true.")
	}
	if strings.TrimSpace(f.Province) == "" || strings.TrimSpace(f.Regency) == "" {
		return bad("regency", "Enter the home address: province and regency from the list, and the street address.")
	}
	if strings.TrimSpace(f.Street) == "" {
		return bad("street", "Enter the home address: province and regency from the list, and the street address.")
	}
	if err := contractProblem(f, today); err != nil {
		return err
	}
	if err := bpjsProblem(f); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(f.EmergencyName) == "":
		return bad("emergency_name", "Enter the emergency contact's name and phone number.")
	case strings.TrimSpace(f.EmergencyPhone) == "":
		return bad("emergency_phone", "Enter the emergency contact's name and phone number.")
	case clean(f.EmergencyName) == clean(f.WorkerName):
		return bad("emergency_name", "The emergency contact must be a different person from the worker.")
	case !phone.Valid(phone.Normalize(f.Phone)):
		return bad("phone", "Enter a valid phone number for the worker (for example 081234567890).")
	case !phone.Valid(phone.Normalize(f.EmergencyPhone)):
		return bad("emergency_phone", "Enter a valid phone number for the emergency contact.")
	case phone.Normalize(f.Phone) == phone.Normalize(f.EmergencyPhone):
		return bad("emergency_phone", "The emergency phone number must be different from the worker's own.")
	}
	if err := certProblem(f, today); err != nil {
		return err
	}
	if f.Foreign {
		if err := validateForeign(f, today); err != nil {
			return err
		}
	}
	switch {
	case !f.HasIDDoc:
		return bad("id_doc", "Upload the ID document (KTP, or the passport for a foreign worker).")
	case !f.HasIDPhoto:
		return bad("id_photo", "Upload the ID photo.")
	}
	return nil
}

func validateForeign(f Form, today dates.Day) error {
	switch f.VisaType {
	case VisaKindVisa:
		if f.VisaIssued == "" {
			return bad("visa_issued", "Enter the visa issue date.")
		}
	case VisaKindItas:
		if strings.TrimSpace(f.VisaCode) == "" {
			return bad("visa_code", "Enter the ITAS code.")
		}
	case VisaKindItap:
		if strings.TrimSpace(f.VisaCode) == "" {
			return bad("visa_code", "Enter the ITAP code.")
		}
		if strings.TrimSpace(f.VisaIssuedBy) == "" {
			return bad("visa_issued_by", "Enter the office that issued the ITAP.")
		}
	default:
		return bad("visa_type", "Choose the work visa type: Visa, ITAS or ITAP.")
	}
	switch {
	case expiredOrBlank(f.VisaExpiry, today):
		return bad("visa_expiry", "The work visa expiry date is missing or already passed.")
	case !f.HasVisaDoc:
		return bad("visa_doc", "Upload the work visa document.")
	case f.RptkaDays <= 0 || f.RptkaIssued == "":
		return bad("rptka_issued", "Enter the RPTKA issue date and duration in days.")
	case expiredOrBlank(f.RptkaExpiry, today):
		return bad("rptka_expiry", "The RPTKA expiry date is missing or already passed.")
	case !f.HasRptkaDoc:
		return bad("rptka_doc", "Upload the RPTKA document.")
	case expiredOrBlank(f.ImmigrationUntil, today):
		return bad("immigration_until", "The immigration reporting letter's valid-until date is missing or already passed.")
	case !f.HasImmigrationDoc:
		return bad("immigration_doc", "Upload the immigration reporting letter.")
	case expiredOrBlank(f.PoliceUntil, today):
		return bad("police_until", "The police letter's valid-until date is missing or already passed.")
	case !f.HasPoliceDoc:
		return bad("police_doc", "Upload the police letter.")
	case expiredOrBlank(f.PassportUntil, today):
		return bad("passport_until", "The passport valid-until date is missing or already passed.")
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
func contractProblem(f Form, today dates.Day) error {
	if !f.ContractShown {
		return nil
	}
	if f.ContractRequired {
		switch {
		case !f.HasContractDoc:
			return bad("contract_doc", "Upload the contract document.")
		case f.ContractUntil == "":
			return bad("contract_until", "Enter the contract validity date.")
		}
	}
	if f.ContractUntil != "" && expiredOrBlank(f.ContractUntil, today) {
		return bad("contract_until", "The contract validity date is invalid or already passed.")
	}
	return nil
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
func bpjsProblem(f Form) error {
	if f.Foreign {
		return nil
	}
	for _, c := range []struct {
		shown, required bool
		value, label    string
		field           string
	}{
		{f.BpjsKesShown, f.BpjsKesRequired, f.BpjsKes, "BPJS Kesehatan", "bpjs_kes"},
		{f.BpjsTkShown, f.BpjsTkRequired, f.BpjsTk, "BPJS Ketenagakerjaan", "bpjs_tk"},
	} {
		if !c.shown {
			continue
		}
		if strings.TrimSpace(c.value) == "" {
			if c.required {
				return bad(c.field, "Enter the "+c.label+" number.")
			}
			continue
		}
		if _, ok := bpjsDigits(c.value); !ok {
			return bad(c.field, "The "+c.label+" number must be digits only (8 to 20 digits).")
		}
	}
	return nil
}

// certProblem: section 7.9. The file is always needed; the date is typed unless
// the position's certificate never expires.
func certProblem(f Form, today dates.Day) error {
	if !f.CertNeeded {
		return nil
	}
	switch {
	case !f.HasCertDoc:
		return bad("cert_doc", "Upload the certificate of competence.")
	case f.CertFixedUntil != "":
		return nil
	case f.CertUntil == "":
		return bad("cert_until", "Enter the certificate validity date.")
	case expiredOrBlank(f.CertUntil, today):
		return bad("cert_until", "The certificate validity date is invalid or already passed.")
	}
	return nil
}
