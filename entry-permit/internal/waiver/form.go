package waiver

import (
	"errors"
	"net/mail"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/phone"
	"entry-permit/internal/policy"
)

// Form is what a requester typed and uploaded for a visiting waiver.
//
// WHY the facts about the company are booleans (CompanyChosen, CompanyIsGov): the
// company list is the medpost mirror and lives in a database. The handler looks
// the company up and tells the rules only what they need, so this package stays
// free of storage (the same split as permit.Form).
type Form struct {
	ApplicantType string
	CompanyChosen bool
	CompanyIsGov  bool

	Name, Gender, DOB, Nationality, NIK string
	Position, Department                string
	VisitLocation, Purpose              string
	From, To                            string

	RequesterName, RequesterEmail, Phone string

	// The medical deviation questions (business-rules-permits.md 8.1 item 9).
	Risk, Precaution, Prevent string
	TakesMedicine             bool
	Medicine1, Dosage1        string
	Medicine2, Dosage2        string
	Medicine3, Dosage3        string
	MedicineNotes             string

	// RiskAnswers and HealthAnswers are the two Yes / No / N/A cards, one letter per question.
	RiskAnswers, HealthAnswers string

	Declaration bool

	HasID, HasPhoto, HasLetter bool

	// Foreign visitors only.
	Foreign                  bool
	PassportUntil, VisaUntil dates.Day
	HasVisa                  bool
}

// Checked is what ValidateForm works out and the caller stores.
type Checked struct {
	NIK  string // cleaned: upper-case letters and digits only
	Days int    // inclusive days, derived from the dates
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && addr.Name == ""
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// pastOrBlank is true for a missing, malformed or already passed day.
func pastOrBlank(d, today dates.Day) bool {
	if _, err := dates.ParseDay(string(d)); err != nil {
		return true
	}
	return d < today
}

// notADay is true for a date of birth that is missing or not a real calendar day.
func notADay(s string) bool {
	_, err := dates.ParseDay(strings.TrimSpace(s))
	return err != nil
}

// ValidateForm applies the submit checks of business-rules-permits.md 8.1 in
// their documented order and returns the first problem as a message fit for
// the requester. It does not do the two checks that need stored waivers: the
// blocking waiver and the yearly company cap (Blocking, CheckCompanyCap).
// FieldError is a validation message that also names the form field it is about (the input's name attribute),
// so the page can open the right step of the step-by-step form and put the cursor on that field.
// Error() is the message alone, so anything that only prints the error is unchanged.
type FieldError struct {
	Field, Msg string
	Err        error // the original error when one was wrapped, so errors.Is still finds it
}

func (e FieldError) Error() string { return e.Msg }

func (e FieldError) Unwrap() error { return e.Err }

// FieldOf returns the field name an error carries, or "" for an error that is not about one field.
func FieldOf(err error) string {
	var fe FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func bad(field, msg string) error { return FieldError{Field: field, Msg: msg} }

// wrap names the field an existing error is about without changing the error.
func wrap(field string, err error) error { return FieldError{Field: field, Msg: err.Error(), Err: err} }

func ValidateForm(p policy.Policy, f Form, today dates.Day) (Checked, error) {
	fail := func(field, msg string) (Checked, error) { return Checked{}, bad(field, msg) }

	if !f.CompanyChosen {
		return fail("company_id", "Pick the visitor's company from the list.")
	}
	if err := CheckApplicant(f.ApplicantType, p.WaiverVisitorEnabled, f.CompanyIsGov); err != nil {
		return Checked{}, wrap("applicant_type", err)
	}
	switch {
	case blank(f.RequesterName):
		return fail("requester_name", "Enter your name (the person making this request).")
	case !validEmail(strings.TrimSpace(f.RequesterEmail)):
		return fail("requester_email", "Enter a valid email address. The decision will be sent there.")
	case !phone.Valid(phone.Normalize(f.Phone)):
		return fail("phone", "Enter a valid phone number for the visitor (for example 081234567890).")
	case blank(f.Name):
		return fail("name", "Enter the visitor's name.")
	case blank(f.Gender):
		return fail("gender", "Choose the visitor's gender.")
	case notADay(f.DOB):
		return fail("dob", "Enter the visitor's date of birth.")
	case blank(f.Nationality):
		return fail("nationality", "Choose the visitor's nationality.")
	case blank(f.Position):
		return fail("position", "Enter the visitor's position.")
	case blank(f.Department):
		return fail("department", "Enter the visitor's department.")
	case blank(f.VisitLocation):
		return fail("visit_location", "Enter the visit location.")
	case blank(f.Purpose):
		return fail("purpose", "Enter the purpose of the visit.")
	}
	nik, err := CleanID(f.NIK, f.Nationality)
	if err != nil {
		return Checked{}, wrap("nik", err)
	}
	days, err := CheckDates(p, f.From, f.To, 0, today)
	if err != nil {
		return Checked{}, wrap("from", err)
	}
	switch {
	case blank(f.Risk):
		return fail("risk", "Describe the identified risk.")
	case blank(f.Precaution):
		return fail("precaution", "Describe the precaution taken.")
	case blank(f.Prevent):
		return fail("prevent", "Describe how this will be prevented.")
	case f.TakesMedicine && blank(f.Medicine1):
		return fail("medicine1", "Enter the name of the medicine.")
	case f.TakesMedicine && blank(f.Dosage1):
		return fail("dosage1", "Enter the medicine dosage.")
	case !ValidAnswers(f.RiskAnswers, RiskQuestions):
		return fail("risk_"+RiskQuestions[0].Key, "Answer every Risk Exposure question.")
	case !ValidAnswers(f.HealthAnswers, HealthQuestions):
		return fail("health_"+HealthQuestions[0].Key, "Answer every Health Declaration question.")
	case !f.Declaration:
		return fail("declaration", "Tick the declaration to confirm the details are true.")
	case !f.HasID:
		return fail("id_doc", "Upload the ID document (KTP, or the passport for a foreign visitor).")
	case !f.HasPhoto:
		return fail("id_photo", "Upload the ID photo.")
	case !f.HasLetter:
		return fail("letter_doc", "Upload the appointment letter.")
	}
	if f.Foreign {
		switch {
		case pastOrBlank(f.PassportUntil, today):
			return fail("passport_until", "The passport valid-until date is missing or already passed.")
		case pastOrBlank(f.VisaUntil, today):
			return fail("visa_until", "The visa valid-until date is missing or already passed.")
		case !f.HasVisa:
			return fail("visa_doc", "Upload the ITAS or e-visa.")
		}
	}
	return Checked{NIK: nik, Days: days}, nil
}

// Counted is the part of a stored waiver the yearly cap needs.
type Counted struct {
	Company, Status string
	From            dates.Day
	Days            int
}

// CapUsed totals the requested days a company has already used in a year.
//
// WHY requested days of every waiver that is not Rejected or Cancelled: the
// old rule counted only days actually checked in, so several waivers sent
// before anyone checked in could together pass the cap (permits doc 8.3 item 2).
// That is the recommended answer to open question 2 and the owner has not
// confirmed it, so it lives in this one function. The year is the year of the
// visit start, as "yyyy".
func CapUsed(rows []Counted, company, year string) int {
	want := key(company)
	total := 0
	for _, r := range rows {
		if key(r.Company) != want || !strings.HasPrefix(string(r.From), year) {
			continue
		}
		if r.Status == StatusRejected || r.Status == StatusCancelled {
			continue
		}
		total += r.Days
	}
	return total
}
