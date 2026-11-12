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
func ValidateForm(p policy.Policy, f Form, today dates.Day) (Checked, error) {
	fail := func(msg string) (Checked, error) { return Checked{}, errors.New(msg) }

	if !f.CompanyChosen {
		return fail("Pick the visitor's company from the list.")
	}
	if err := CheckApplicant(f.ApplicantType, p.WaiverVisitorEnabled, f.CompanyIsGov); err != nil {
		return Checked{}, err
	}
	switch {
	case blank(f.RequesterName):
		return fail("Enter your name (the person making this request).")
	case !validEmail(strings.TrimSpace(f.RequesterEmail)):
		return fail("Enter a valid email address. The decision will be sent there.")
	case !phone.Valid(phone.Normalize(f.Phone)):
		return fail("Enter a valid phone number for the visitor (for example 081234567890).")
	case blank(f.Name):
		return fail("Enter the visitor's name.")
	case blank(f.Gender):
		return fail("Choose the visitor's gender.")
	case notADay(f.DOB):
		return fail("Enter the visitor's date of birth.")
	case blank(f.Nationality):
		return fail("Choose the visitor's nationality.")
	case blank(f.Position):
		return fail("Enter the visitor's position.")
	case blank(f.Department):
		return fail("Enter the visitor's department.")
	case blank(f.VisitLocation):
		return fail("Enter the visit location.")
	case blank(f.Purpose):
		return fail("Enter the purpose of the visit.")
	}
	nik, err := CleanID(f.NIK, f.Nationality)
	if err != nil {
		return Checked{}, err
	}
	days, err := CheckDates(p, f.From, f.To, 0, today)
	if err != nil {
		return Checked{}, err
	}
	switch {
	case blank(f.Risk):
		return fail("Describe the identified risk.")
	case blank(f.Precaution):
		return fail("Describe the precaution taken.")
	case blank(f.Prevent):
		return fail("Describe how this will be prevented.")
	case f.TakesMedicine && blank(f.Medicine1):
		return fail("Enter the name of the medicine.")
	case f.TakesMedicine && blank(f.Dosage1):
		return fail("Enter the medicine dosage.")
	case !ValidAnswers(f.RiskAnswers, RiskQuestions):
		return fail("Answer every Risk Exposure question.")
	case !ValidAnswers(f.HealthAnswers, HealthQuestions):
		return fail("Answer every Health Declaration question.")
	case !f.Declaration:
		return fail("Tick the declaration to confirm the details are true.")
	case !f.HasID:
		return fail("Upload the ID document (KTP, or the passport for a foreign visitor).")
	case !f.HasPhoto:
		return fail("Upload the ID photo.")
	case !f.HasLetter:
		return fail("Upload the appointment letter.")
	}
	if f.Foreign {
		switch {
		case pastOrBlank(f.PassportUntil, today):
			return fail("The passport valid-until date is missing or already passed.")
		case pastOrBlank(f.VisaUntil, today):
			return fail("The visa valid-until date is missing or already passed.")
		case !f.HasVisa:
			return fail("Upload the ITAS or e-visa.")
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
