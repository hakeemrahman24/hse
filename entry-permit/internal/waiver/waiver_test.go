package waiver

import (
	"errors"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
)

var pol = policy.Default() // max 7 days, cap 14, note 3 words

// The id box follows the visitor's nationality, as in medpost (owner decision 2026-11-08): an
// Indonesian gives the 16-digit NIK, everyone else the passport number (7 to 9 letters or digits).
// This replaces the legacy rule (6 to 16 digits, or 6 to 9 characters) that accepted either.
func TestCleanIDFollowsTheNationality(t *testing.T) {
	cases := []struct {
		nationality string
		in          string
		want        string
		wantErr     error
	}{
		{"Indonesia", "1234567890123456", "1234567890123456", nil},
		{"Indonesia", "3201 0203-0490 0001", "3201020304900001", nil}, // spaces and dashes removed
		{" indonesia ", "1234567890123456", "1234567890123456", nil},  // any case, trimmed
		{"Indonesia", "123456789012345", "", ErrNIKFormat},            // 15 digits
		{"Indonesia", "12345678901234567", "", ErrNIKFormat},          // 17 digits
		{"Indonesia", "AB12345", "", ErrNIKFormat},                    // a passport number is not a NIK
		{"Indonesia", "  ", "", ErrIDRequired},
		{"Vietnam", "P1234567", "P1234567", nil},
		{"Vietnam", "ab-123 4567", "AB1234567", nil}, // punctuation removed, upper-cased
		{"Vietnam", "1234567", "1234567", nil},       // all digits is a fine passport number
		{"Vietnam", "AB1234", "", ErrPassportFormat}, // 6 characters
		{"Vietnam", "AB12345678", "", ErrPassportFormat},
		{"Vietnam", "1234567890123456", "", ErrPassportFormat}, // a NIK is not a passport number
		{"Vietnam", "", "", ErrIDRequired},
	}
	for _, c := range cases {
		got, err := CleanID(c.in, c.nationality)
		if got != c.want || !errors.Is(err, c.wantErr) {
			t.Errorf("CleanID(%q, %q)=%q,%v want %q,%v", c.in, c.nationality, got, err, c.want, c.wantErr)
		}
	}
}

func TestGolden22And23Dates(t *testing.T) {
	today := dates.Day("2027-01-10")
	if d, err := CheckDates(pol, "2027-01-10", "2027-01-16", 0, today); err != nil || d != 7 {
		t.Errorf("7 days: %d %v", d, err)
	}
	if _, err := CheckDates(pol, "2027-01-10", "2027-01-17", 0, today); !errors.Is(err, ErrDuration) {
		t.Errorf("8 days: %v", err)
	}
	if _, err := CheckDates(pol, "2027-01-09", "2027-01-10", 0, today); !errors.Is(err, ErrFromPast) {
		t.Errorf("yesterday: %v", err)
	}
	if _, err := CheckDates(pol, "2027-01-12", "2027-01-11", 0, today); !errors.Is(err, ErrToBeforeFrom) {
		t.Errorf("to<from: %v", err)
	}
	if _, err := CheckDates(pol, "", "2027-01-11", 0, today); !errors.Is(err, ErrDatesRequired) {
		t.Errorf("blank: %v", err)
	}
	if _, err := CheckDates(pol, "bad", "2027-01-11", 0, today); err == nil {
		t.Error("bad from")
	}
	if _, err := CheckDates(pol, "2027-01-11", "bad", 0, today); err == nil {
		t.Error("bad to")
	}
	if _, err := CheckDates(pol, "2027-01-10", "2027-01-12", 5, today); !errors.Is(err, ErrDurationMatch) {
		t.Errorf("mismatch: %v", err)
	}
	if d, err := CheckDates(pol, "2027-01-10", "2027-01-12", 3, today); err != nil || d != 3 {
		t.Errorf("matching typed duration: %d %v", d, err)
	}
}

func TestGolden24Cap(t *testing.T) {
	if err := CheckCompanyCap(pol, TypeVisitorCompany, 10, 5); !errors.Is(err, ErrCompanyCap) {
		t.Errorf("15: %v", err)
	}
	if err := CheckCompanyCap(pol, "", 10, 4); err != nil {
		t.Errorf("14: %v", err)
	}
	if err := CheckCompanyCap(pol, TypeGovernment, 100, 7); err != nil {
		t.Errorf("government exempt: %v", err)
	}
}

func TestGolden25Applicant(t *testing.T) {
	if err := CheckApplicant(TypeGovernment, true, false); !errors.Is(err, ErrNeedGovCompany) {
		t.Errorf("gov+non-gov: %v", err)
	}
	if err := CheckApplicant(TypeVisitorCompany, true, true); !errors.Is(err, ErrNoGovCompany) {
		t.Errorf("visitor+gov: %v", err)
	}
	if err := CheckApplicant("", false, false); !errors.Is(err, ErrVisitorOff) {
		t.Errorf("toggle off: %v", err)
	}
	if CheckApplicant(TypeGovernment, false, true) != nil || CheckApplicant("", true, false) != nil {
		t.Error("valid combinations refused")
	}
}

func TestGolden20And21Blocking(t *testing.T) {
	open := Waiver{ID: "WVR-OLD", Status: StatusPendingStage1, NIK: "1234567890", Name: "Ann", DOB: "1990-01-01",
		Company: "Acme", From: "2027-02-01", To: "2027-02-05"}
	today := dates.Day("2027-01-10")
	// 20: Government, same NIK, overlapping vs not.
	gov := Waiver{Type: TypeGovernment, NIK: "1234567890", Name: "Zed", From: "2027-02-04", To: "2027-02-08"}
	if Blocking(gov, []Waiver{open}, today) == nil {
		t.Error("overlap should block")
	}
	gov.From, gov.To = "2027-02-06", "2027-02-08"
	if Blocking(gov, []Waiver{open}, today) != nil {
		t.Error("no overlap should allow")
	}
	// 21: VisitorCompany, name + company, different dates still blocks.
	vis := Waiver{Type: TypeVisitorCompany, NIK: "999999", Name: " ann ", Company: "ACME", From: "2027-03-01", To: "2027-03-02"}
	if got := Blocking(vis, []Waiver{open}, today); got == nil || got.ID != "WVR-OLD" {
		t.Error("name+company should block")
	}
	// Name + DOB.
	vis2 := Waiver{NIK: "888888", Name: "Ann", DOB: "1990-01-01", Company: "Other"}
	if Blocking(vis2, []Waiver{open}, today) == nil {
		t.Error("name+dob should block")
	}
	// No match.
	if Blocking(Waiver{NIK: "777777", Name: "Bob", Company: "Other"}, []Waiver{open}, today) != nil {
		t.Error("unrelated blocked")
	}
	// Terminal or finished waivers never block.
	for _, s := range []string{StatusCancelled, StatusRejected, StatusCompleted, StatusExpired, "Not approved by x"} {
		o := open
		o.Status = s
		if Blocking(vis, []Waiver{o}, today) != nil {
			t.Errorf("%s blocked", s)
		}
	}
	ended := open
	ended.To = "2027-01-09"
	if Blocking(vis, []Waiver{ended}, today) != nil {
		t.Error("ended visit blocked")
	}
}

func TestGolden30ReviewOrder(t *testing.T) {
	// Doctor approving an Approved waiver is refused.
	if _, err := Review(pol, StageDoctor, StatusApproved, true, ""); !errors.Is(err, permit.ErrAlreadyActioned) {
		t.Errorf("%v", err)
	}
	steps := []struct {
		stage Stage
		from  string
		want  string
	}{
		{StageDoctor, StatusPendingDoctor, StatusPendingStage1},
		{StageStage1, StatusPendingStage1, StatusPendingSiteManager},
		{StageFinal, StatusPendingSiteManager, StatusApproved},
	}
	for _, s := range steps {
		if got, err := Review(pol, s.stage, s.from, true, ""); err != nil || got != s.want {
			t.Errorf("%s: %s %v", s.stage, got, err)
		}
	}
	if _, err := Review(pol, StageFinal, StatusPendingSiteManager, false, "no"); !errors.Is(err, permit.ErrNoteTooShort) {
		t.Errorf("short note: %v", err)
	}
	if got, err := Review(pol, StageFinal, StatusPendingSiteManager, false, "visit not justified"); err != nil || got != StatusRejected {
		t.Errorf("reject: %s %v", got, err)
	}
	if _, err := Review(pol, "x", StatusPendingDoctor, true, ""); !errors.Is(err, ErrUnknownStage) {
		t.Errorf("stage: %v", err)
	}
}

func TestSelfCancel(t *testing.T) {
	if !CanSelfCancel(StatusPendingDoctor) || CanSelfCancel(StatusPendingStage1) {
		t.Error("only PendingDoctor can self-cancel")
	}
}

func TestGolden26And27AfterVisit(t *testing.T) {
	r := AfterVisit(StatusApproved, TicketApproved, "2027-01-09", "2027-01-10")
	if !r.Changed || r.Status != StatusCompleted || r.TicketStatus != TicketClosed || r.TicketReason != ReasonPlannedPassed {
		t.Errorf("26: %+v", r)
	}
	r = AfterVisit(StatusPendingStage1, TicketPendingDoctor, "2027-01-09", "2027-01-10")
	if r.Status != StatusCancelled || r.TicketStatus != TicketCancelled || r.TicketReason != ReasonWindowEnded {
		t.Errorf("27: %+v", r)
	}
	// Visit not over yet (to == today): nothing changes.
	if r := AfterVisit(StatusApproved, TicketApproved, "2027-01-10", "2027-01-10"); r.Changed {
		t.Errorf("same day: %+v", r)
	}
	// Other statuses are left alone.
	if r := AfterVisit(StatusRejected, TicketClosed, "2020-01-01", "2027-01-10"); r.Changed {
		t.Errorf("rejected: %+v", r)
	}
	if !GateStateExpired(true, "2027-01-09", "2027-01-10") || GateStateExpired(false, "2027-01-09", "2027-01-10") ||
		GateStateExpired(true, "2027-01-10", "2027-01-10") || GateStateExpired(true, "", "2027-01-10") {
		t.Error("GateStateExpired")
	}
}

func TestNormalizeType(t *testing.T) {
	if NormalizeType(" government ") != TypeGovernment || NormalizeType("") != TypeVisitorCompany || NormalizeType("x") != TypeVisitorCompany {
		t.Error("NormalizeType")
	}
}
