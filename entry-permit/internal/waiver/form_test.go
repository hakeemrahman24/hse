package waiver

import (
	"errors"
	"strings"
	"testing"

	"entry-permit/internal/dates"
)

const today = dates.Day("2026-11-06")

// goodForm is a visitor-company waiver that passes every rule.
func goodForm() Form {
	return Form{
		ApplicantType: TypeVisitorCompany, CompanyChosen: true,
		Name: "Budi Santoso", Gender: "Male", DOB: "1990-04-02", Nationality: "Indonesia", NIK: "3201020304900001",
		Position: "Auditor", Department: "HSE", VisitLocation: "Site A", Purpose: "Annual audit",
		From: "2026-11-10", To: "2026-11-12",
		RequesterName: "Siti Aminah", RequesterEmail: "siti@example.com", Phone: "081234567890",
		Risk: "Heavy equipment nearby", Precaution: "PPE worn", Prevent: "Escort at all times",
		RiskAnswers: "NNNNNNNNN", HealthAnswers: "NNNNNNNNNNNNNNN",
		Declaration: true,
		HasID:       true, HasPhoto: true, HasLetter: true,
	}
}

func TestValidateFormAcceptsAGoodVisitorWaiver(t *testing.T) {
	r, err := ValidateForm(pol, goodForm(), today)
	if err != nil {
		t.Fatal(err)
	}
	if r.NIK != "3201020304900001" || r.Days != 3 {
		t.Errorf("got %+v, want NIK kept and 3 days", r)
	}
}

func TestValidateFormRefusals(t *testing.T) {
	off := pol
	off.WaiverVisitorEnabled = false
	cases := []struct {
		name string
		edit func(*Form)
		want string // text the message must hold, any case
	}{
		{"declaration", func(f *Form) { f.Declaration = false }, "declaration"},
		{"no company", func(f *Form) { f.CompanyChosen = false }, "company"},
		{"government needs gov company", func(f *Form) { f.ApplicantType = TypeGovernment }, "Government"},
		{"visitor picks gov company", func(f *Form) { f.CompanyIsGov = true }, "Government"},
		{"requester name", func(f *Form) { f.RequesterName = " " }, "your name"},
		{"requester email", func(f *Form) { f.RequesterEmail = "nope" }, "email"},
		{"phone", func(f *Form) { f.Phone = "12" }, "phone"},
		{"visitor name", func(f *Form) { f.Name = "" }, "visitor's name"},
		{"gender", func(f *Form) { f.Gender = "" }, "gender"},
		{"dob", func(f *Form) { f.DOB = "" }, "date of birth"},
		{"nationality", func(f *Form) { f.Nationality = "" }, "nationality"},
		{"position", func(f *Form) { f.Position = "" }, "position"},
		{"department", func(f *Form) { f.Department = "" }, "department"},
		{"location", func(f *Form) { f.VisitLocation = "" }, "visit location"},
		{"purpose", func(f *Form) { f.Purpose = "" }, "purpose"},
		{"nik", func(f *Form) { f.NIK = "12" }, "NIK"},
		{"dates", func(f *Form) { f.To = "" }, "from-date"},
		{"too long", func(f *Form) { f.To = "2026-11-30" }, "duration"},
		{"past", func(f *Form) { f.From = "2026-11-01"; f.To = "2026-11-02" }, "before today"},
		{"risk", func(f *Form) { f.Risk = "" }, "risk"},
		{"precaution", func(f *Form) { f.Precaution = "" }, "precaution"},
		{"prevent", func(f *Form) { f.Prevent = "" }, "prevent"},
		{"medicine name", func(f *Form) { f.TakesMedicine, f.Dosage1 = true, "1 tablet" }, "medicine"},
		{"medicine dosage", func(f *Form) { f.TakesMedicine, f.Medicine1 = true, "Paracetamol" }, "dosage"},
		{"id file", func(f *Form) { f.HasID = false }, "ID document"},
		{"photo", func(f *Form) { f.HasPhoto = false }, "ID photo"},
		{"letter", func(f *Form) { f.HasLetter = false }, "appointment letter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := goodForm()
			c.edit(&f)
			_, err := ValidateForm(pol, f, today)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(c.want)) {
				t.Errorf("error %v, want text %q", err, c.want)
			}
		})
	}

	if _, err := ValidateForm(off, goodForm(), today); !errors.Is(err, ErrVisitorOff) {
		t.Errorf("visitor company off: %v", err)
	}
	// Government is not affected by the visitor switch.
	g := goodForm()
	g.ApplicantType, g.CompanyIsGov = TypeGovernment, true
	if _, err := ValidateForm(off, g, today); err != nil {
		t.Errorf("government with the visitor switch off: %v", err)
	}
}

func TestValidateFormForeignVisitor(t *testing.T) {
	f := goodForm()
	f.Foreign, f.Nationality, f.NIK = true, "Philippines", "p1234567"
	f.PassportUntil, f.VisaUntil, f.HasVisa = "2027-05-01", "2027-02-01", true
	if _, err := ValidateForm(pol, f, today); err != nil {
		t.Fatalf("a complete foreign visitor: %v", err)
	}
	for name, edit := range map[string]func(*Form){
		"no visa file":     func(f *Form) { f.HasVisa = false },
		"passport expired": func(f *Form) { f.PassportUntil = "2026-11-05" },
		"passport blank":   func(f *Form) { f.PassportUntil = "" },
		"visa expired":     func(f *Form) { f.VisaUntil = "2026-11-05" },
		"visa blank":       func(f *Form) { f.VisaUntil = "" },
	} {
		g := f
		edit(&g)
		if _, err := ValidateForm(pol, g, today); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}

func TestValidateFormCleansTheIDAndKeepsTheDays(t *testing.T) {
	f := goodForm()
	f.NIK = "3201 0203-0490 0001" // an Indonesian visitor: spaces and dashes are removed
	f.From, f.To = "2026-11-06", "2026-11-12"
	r, err := ValidateForm(pol, f, today)
	if err != nil || r.NIK != "3201020304900001" || r.Days != 7 {
		t.Errorf("got %+v, %v", r, err)
	}
}

func TestCapUsedCountsRequestedDaysOfLiveWaivers(t *testing.T) {
	// Owner question 2 (the recommended answer, not yet confirmed): every waiver
	// that is not rejected or cancelled counts its requested days, by the year of
	// the visit start.
	rows := []Counted{
		{Company: "Acme  Ltd", Status: StatusApproved, From: "2026-03-01", Days: 5},
		{Company: "acme ltd", Status: StatusPendingDoctor, From: "2026-06-01", Days: 2},
		{Company: "Acme Ltd", Status: StatusRejected, From: "2026-07-01", Days: 7},
		{Company: "Acme Ltd", Status: StatusCancelled, From: "2026-08-01", Days: 7},
		{Company: "Acme Ltd", Status: StatusCompleted, From: "2025-12-30", Days: 3}, // last year
		{Company: "Other", Status: StatusApproved, From: "2026-03-01", Days: 9},
	}
	if got := CapUsed(rows, "ACME LTD", "2026"); got != 7 {
		t.Errorf("used = %d, want 7 (5 approved + 2 pending)", got)
	}
}

func TestValidationErrorsNameTheirField(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Form)
		field string
	}{
		{"email", func(f *Form) { f.RequesterEmail = "nope" }, "requester_email"},
		{"gender", func(f *Form) { f.Gender = "" }, "gender"},
		{"nik", func(f *Form) { f.NIK = "12" }, "nik"},
		{"dates", func(f *Form) { f.To = "" }, "from"},
		{"precaution", func(f *Form) { f.Precaution = "" }, "precaution"},
		{"declaration", func(f *Form) { f.Declaration = false }, "declaration"},
		{"photo", func(f *Form) { f.HasPhoto = false }, "id_photo"},
	}
	for _, c := range cases {
		f := goodForm()
		c.edit(&f)
		if _, err := ValidateForm(pol, f, today); FieldOf(err) != c.field {
			t.Errorf("%s: field %q, want %q (err %v)", c.name, FieldOf(err), c.field, err)
		}
	}
}
