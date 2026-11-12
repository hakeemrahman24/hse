package ticket

import (
	"errors"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

var pol = policy.Default() // WNI Onshore: cap 56, extension 7, rest 14, min rest 3

// prev builds a counting (Closed) work ticket ending on `end` after `days` days.
func prev(end dates.Day, days int, pattern string) Ticket {
	return Ticket{
		ID: "TKT-A", LEP: "LEP-1", Work: true, Status: StatusClosed,
		Arrival: end.AddDays(-(days - 1)), Departure: end,
		Location: "Onshore", Pattern: pattern,
	}
}

func analyze(prevs []Ticket, arr, dep dates.Day, loc string, wni bool) Analysis {
	return Analyze(pol, prevs, arr, dep, loc, wni)
}

func TestGolden1To2NoPrevious(t *testing.T) {
	a := analyze(nil, "2027-01-01", "2027-02-25", "Onshore", true) // 56 d
	if a.Pattern != PatternNormal || a.RotationExtension || a.ShortBreak {
		t.Errorf("56 d: %+v", a)
	}
	a = analyze(nil, "2027-01-01", "2027-02-26", "Onshore", true) // 57 d
	if !a.ExceedsAllowance || a.AllowedProposalDays != 56 || a.Pattern != PatternExtended {
		t.Errorf("57 d: %+v", a)
	}
}

func TestGolden3And4Consecutive(t *testing.T) {
	p := []Ticket{prev("2027-02-25", 56, "")}
	a := analyze(p, "2027-02-26", "2027-03-04", "Onshore", true) // 7 d
	if !a.Consecutive || a.ChainDaysBefore != 56 || a.AllowedProposalDays != 7 ||
		a.ExceedsAllowance || a.ProjectedChainDays != 63 || a.Pattern != PatternConsecutiveExtended {
		t.Errorf("7 d: %+v", a)
	}
	a = analyze(p, "2027-02-26", "2027-03-05", "Onshore", true) // 8 d
	if !a.ExceedsAllowance || a.Pattern != PatternConsecutiveExtended {
		t.Errorf("8 d: %+v", a)
	}
}

func TestGolden5ConsecutiveWithinCap(t *testing.T) {
	p := []Ticket{prev("2027-01-30", 30, "")}
	a := analyze(p, "2027-01-31", "2027-02-19", "Onshore", true) // 20 d
	if a.Pattern != PatternConsecutive || a.ProjectedChainDays != 50 || a.RotationExtension {
		t.Errorf("%+v", a)
	}
}

func TestGolden6And7Breaks(t *testing.T) {
	p := []Ticket{prev("2027-02-25", 56, "")}
	a := analyze(p, "2027-03-02", "2027-03-10", "Onshore", true) // gap 5
	if !a.ShortBreak || a.Pattern != PatternShortBreak || a.GapDays != 5 {
		t.Errorf("gap 5: %+v", a)
	}
	a = analyze(p, "2027-03-12", "2027-03-20", "Onshore", true) // gap 15
	if a.ShortBreak || a.Pattern != PatternNormal {
		t.Errorf("gap 15: %+v", a)
	}
}

func TestGolden8PostExtensionRest(t *testing.T) {
	p := []Ticket{prev("2027-03-04", 63, "ConsecutiveExtended")}
	a := analyze(p, "2027-03-14", "2027-03-20", "Onshore", true) // gap 10
	if a.RequiredRestDays != 3 || a.ShortBreak {
		t.Errorf("%+v", a)
	}
}

func TestGolden9ArrivalBeforePreviousEnd(t *testing.T) {
	p := []Ticket{prev("2027-02-25", 10, "")}
	if !analyze(p, "2027-02-20", "2027-02-28", "Onshore", true).ArrivalBeforePreviousEnd {
		t.Error("should flag arrival before end")
	}
}

func TestGolden10ChainGap(t *testing.T) {
	a := Ticket{Work: true, Status: StatusClosed, Arrival: "2027-01-01", Departure: "2027-01-10", Location: "Onshore"}
	for _, c := range []struct {
		next dates.Day
		days int
	}{{"2027-01-10", 20}, {"2027-01-11", 20}, {"2027-01-12", 10}} { // gap 0, 1, 2
		b := Ticket{Work: true, Status: StatusClosed, Arrival: c.next, Departure: c.next.AddDays(9), Location: "Onshore"}
		if got := ConsecutiveChain(pol, []Ticket{a, b}, true).Days; got != c.days {
			t.Errorf("next %s chain=%d want %d", c.next, got, c.days)
		}
	}
}

func TestGolden11To13(t *testing.T) {
	a := analyze(nil, "2027-01-01", "2027-02-12", "Offshore", true) // 43 d, cap 42
	if a.Pattern != PatternExtended {
		t.Errorf("offshore: %+v", a)
	}
	if a := analyze(nil, "2027-01-01", "2027-02-25", "Onshore", false); a.NormalCapDays != 56 || a.RequiredRestDays != 14 {
		t.Errorf("foreign: %+v", a)
	}
	// Previous offshore ticket of 50 d exceeded its own 42-day cap.
	po := prev("2027-02-19", 50, "")
	po.Location = "Offshore"
	a = analyze([]Ticket{po}, "2027-02-20", "2027-02-25", "Onshore", true)
	if !a.ChainUsedExtension {
		t.Errorf("own-cap rule lost: %+v", a)
	}
}

func baseInput() ProposalInput {
	return ProposalInput{
		LEP: "LEP-1", Today: "2026-12-01", Arrival: "2027-01-01", Departure: "2027-01-10",
		PermitFound: true, PermitApproved: true, PermitLocation: "Onshore", Wni: true,
		McuResult: "Fit", LepValidUntil: "2027-12-31",
	}
}

func TestProposeHappyPath(t *testing.T) {
	got, err := Propose(pol, baseInput())
	if err != nil {
		t.Fatal(err)
	}
	// Fatigue week = 8-2 = 6: due = arrival + 42 - 1 = 2027-02-11.
	if got.RotationNo != 1 || got.Status != StatusApproved || got.DoctorNote != "_" || got.FatigueDue != "2027-02-11" {
		t.Errorf("%+v", got)
	}
}

func TestProposeErrors(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*ProposalInput)
		want error
	}{
		{"no lep", func(i *ProposalInput) { i.LEP = "" }, ErrNoLEP},
		{"waiver", func(i *ProposalInput) { i.LEP = "wvr-ABCDE" }, ErrWaiverTicket},
		{"no dates", func(i *ProposalInput) { i.Arrival = "" }, ErrDatesRequired},
		{"dep before", func(i *ProposalInput) { i.Departure = "2026-12-31" }, ErrDepartureBefore},
		{"arrival past", func(i *ProposalInput) { i.Today = "2027-01-05" }, ErrArrivalPast},
		{"no permit", func(i *ProposalInput) { i.PermitFound = false }, ErrNoPermit},
		{"not approved", func(i *ProposalInput) { i.PermitApproved = false }, ErrPermitNotActive},
		{"no location", func(i *ProposalInput) { i.PermitLocation = "" }, ErrNoLocation},
		{"blank mcu (14)", func(i *ProposalInput) { i.McuResult = "" }, ErrMCUUnfit},
		{"tidak", func(i *ProposalInput) { i.McuResult = "Tidak layak" }, ErrMCUUnfit},
		{"after valid-until (15)", func(i *ProposalInput) { i.LepValidUntil = "2027-01-05" }, ErrAfterValidUntil},
		{"pending", func(i *ProposalInput) {
			i.Tickets = []Ticket{{Work: true, Status: StatusPendingDoctor, Pattern: "Extended"}}
		}, ErrPendingRequest},
	}
	for _, c := range cases {
		in := baseInput()
		c.mod(&in)
		if _, err := Propose(pol, in); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	in := baseInput()
	in.Arrival = "bad"
	if _, err := Propose(pol, in); err == nil {
		t.Error("bad date accepted")
	}
	in = baseInput()
	in.Departure = "bad"
	if _, err := Propose(pol, in); err == nil {
		t.Error("bad departure accepted")
	}
}

func TestGolden16ProposalWindow(t *testing.T) {
	working := Ticket{Work: true, Status: StatusApproved, Arrival: "2026-11-25", Departure: "2026-12-21", Location: "Onshore"}
	in := baseInput()
	in.Today, in.Arrival, in.Departure = "2026-12-01", "2026-12-22", "2026-12-30"
	in.Tickets = []Ticket{working} // departs in 20 days
	if _, err := Propose(pol, in); !errors.Is(err, ErrWindowNotOpen) {
		t.Errorf("20 days before: %v", err)
	}
	in.Today = "2026-12-07" // exactly 14 days before
	if _, err := Propose(pol, in); err != nil {
		t.Errorf("14 days before: %v", err)
	}
}

func TestProposeConsent(t *testing.T) {
	in := baseInput()
	in.Departure = "2027-02-26" // 57 days -> extended
	if _, err := Propose(pol, in); !errors.Is(err, ErrFatigueConsent) {
		t.Errorf("got %v", err)
	}
	in.FatigueConsent = true
	got, err := Propose(pol, in)
	if err != nil || got.DoctorNote != PatternExtended {
		t.Errorf("%+v %v", got, err)
	}
}

func TestProposeBeforePreviousEnd(t *testing.T) {
	in := baseInput()
	in.Tickets = []Ticket{prev("2027-01-05", 10, "")}
	if _, err := Propose(pol, in); !errors.Is(err, ErrBeforePreviousEnd) {
		t.Errorf("got %v", err)
	}
}

func TestFitWithNote(t *testing.T) {
	in := baseInput()
	in.McuResult = "Fit with Note"
	steps := []struct {
		mod  func(*ProposalInput)
		want error
	}{
		{func(i *ProposalInput) {}, ErrNoteAgreement},
		{func(i *ProposalInput) { i.McuNoteAgreement = true }, ErrMedicationAnswer},
		{func(i *ProposalInput) { i.MedicationAnswer = "Y" }, ErrMedicationDetail},
		{func(i *ProposalInput) { i.MedicationDetails = "x" }, ErrMedicationDetail},
		{func(i *ProposalInput) { i.MedicinePhoto = true }, nil},
	}
	for n, s := range steps {
		s.mod(&in)
		_, err := Propose(pol, in)
		if !errors.Is(err, s.want) {
			t.Errorf("step %d: got %v want %v", n, err, s.want)
		}
	}
	in.MedicationAnswer, in.MedicationDetails, in.MedicinePhoto = "N", "", false
	got, err := Propose(pol, in)
	if err != nil || got.DoctorNote != "MCU note self-declaration" || !got.FitWithNote {
		t.Errorf("%+v %v", got, err)
	}
}

func TestHelpers(t *testing.T) {
	if NormalizeStatus("active") != StatusApproved || NormalizeStatus(" Closed ") != StatusClosed ||
		NormalizeStatus("PendingDoctor") != StatusPendingDoctor || NormalizeStatus("rejected") != StatusRejected ||
		NormalizeStatus("canceled") != StatusCancelled || NormalizeStatus("weird") != "weird" {
		t.Error("NormalizeStatus")
	}
	if NormalizeLocation(" ONSHORE ") != "Onshore" || NormalizeLocation("x") != "" || NormalizeLocation("offshore") != "Offshore" {
		t.Error("NormalizeLocation")
	}
	for _, p := range []string{"", "n", "_", "Normal", "Consecutive", "ShortBreak"} {
		if IsExtendedPattern(p) {
			t.Errorf("%q should not be extended", p)
		}
	}
	for _, p := range []string{"y", "Extended", "ShortBreakExtended"} {
		if !IsExtendedPattern(p) {
			t.Errorf("%q should be extended", p)
		}
	}
	if !ConsentGiven("YES") || ConsentGiven("no") {
		t.Error("ConsentGiven")
	}
	if PatternFromFlags(false, true, true) != PatternShortBreakExtended {
		t.Error("ShortBreakExtended")
	}
	if got := RotationNo([]Ticket{prev("2027-01-01", 3, ""), {Work: false, Status: StatusApproved}, {Work: true, Status: StatusRejected}}); got != 2 {
		t.Errorf("RotationNo=%d", got)
	}
	if IsMCUFitWithNote("Unfit with note") || !IsMCUFitWithNote("Fit dengan catatan") {
		t.Error("IsMCUFitWithNote")
	}
}

func TestWeeksOnSite(t *testing.T) {
	cases := []struct {
		start dates.Moment
		today dates.Day
		want  int
	}{
		{"", "2027-01-20", 0},
		{"2027-01-01 23:59:00", "2027-01-08", 1}, // calendar days: time of day ignored
		{"2027-01-01 00:00:00", "2027-01-07", 0},
		{"2027-01-10 08:00:00", "2027-01-01", 0}, // check-in in the future
	}
	for _, c := range cases {
		if got := WeeksOnSite(c.start, c.today); got != c.want {
			t.Errorf("%q %s = %d want %d", c.start, c.today, got, c.want)
		}
	}
}

// ---- daily job (golden 17 to 19) ----

func approved() Ticket {
	return Ticket{ID: "TKT-1", LEP: "LEP-1", Work: true, Status: StatusApproved,
		Arrival: "2027-01-01", Departure: "2027-02-25", Location: "Offshore"} // max 6 weeks
}

func din(today dates.Day) DailyInput {
	return DailyInput{Today: today, Now: dates.Moment(string(today) + " 06:00:00"), PermitValidTo: "2027-12-31", Wni: true}
}

func has(effects []Effect, e Effect) bool {
	for _, x := range effects {
		if x == e {
			return true
		}
	}
	return false
}

func TestDailySkipsNonWorkOrNonApproved(t *testing.T) {
	w := approved()
	w.Work = false
	if r := DailyCheck(pol, w, din("2027-03-01")); r.Ticket.Status != StatusApproved || len(r.Effects) != 0 {
		t.Errorf("%+v", r)
	}
	c := approved()
	c.Status = StatusClosed
	if r := DailyCheck(pol, c, din("2027-03-01")); len(r.Effects) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestGolden17AutoClose(t *testing.T) {
	r := DailyCheck(pol, approved(), din("2027-02-26"))
	if r.Ticket.Status != StatusClosed || r.Ticket.ClosedReason != ClosedPlannedPast ||
		r.Ticket.ClosedAt != "2027-02-26 06:00:00" || len(r.Effects) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestDailyExpiryLock(t *testing.T) {
	in := din("2027-01-10")
	in.PermitStatus = "Expired in grace period"
	r := DailyCheck(pol, approved(), in)
	if !r.Ticket.LepLocked || !has(r.Effects, EffectGateSync) || has(r.Effects, EffectReminder) {
		t.Errorf("%+v", r)
	}
	in = din("2028-01-01")
	in.PermitStatus = "" // today beyond valid-to
	if r := DailyCheck(pol, approved(), in); !r.Ticket.LepLocked {
		t.Errorf("valid-to rule lost: %+v", r)
	}
}

func TestGolden18ReminderLead(t *testing.T) {
	long := approved() // 56 days -> lead 7
	if r := DailyCheck(pol, long, din("2027-02-17")); has(r.Effects, EffectReminder) {
		t.Error("8 days left is too early")
	}
	r := DailyCheck(pol, long, din("2027-02-18")) // 7 days left
	if !has(r.Effects, EffectReminder) || !r.Ticket.ReminderNotified {
		t.Errorf("%+v", r)
	}
	if r2 := DailyCheck(pol, r.Ticket, din("2027-02-19")); has(r2.Effects, EffectReminder) {
		t.Error("reminder sent twice")
	}
	short := approved()
	short.Departure = "2027-01-06" // 6 days -> lead 2
	if r := DailyCheck(pol, short, din("2027-01-03")); has(r.Effects, EffectReminder) {
		t.Error("3 days left: too early for short ticket")
	}
	if r := DailyCheck(pol, short, din("2027-01-04")); !has(r.Effects, EffectReminder) {
		t.Error("2 days left: should remind")
	}
	seven := approved()
	seven.Departure = "2027-01-07" // exactly 7 days -> lead 7
	if r := DailyCheck(pol, seven, din("2027-01-01")); !has(r.Effects, EffectReminder) {
		t.Error("7-day ticket uses lead 7")
	}
}

func TestGolden19WeeklyChecks(t *testing.T) {
	tk := approved()
	tk.RecordStart = "2027-01-01 08:00:00"
	tk.ReminderNotified = true
	// Week 4 = fatigue week for offshore (6-2); week 5 = N-1; week 6 = N0.
	r := DailyCheck(pol, tk, din("2027-01-29"))
	if r.Ticket.WeeksOnSite != 4 || !r.Ticket.FatigueFlag || has(r.Effects, EffectWeekN1) {
		t.Errorf("week 4: %+v", r)
	}
	r = DailyCheck(pol, r.Ticket, din("2027-02-05"))
	if !has(r.Effects, EffectWeekN1) || r.Ticket.WeekN1SentAt == "" || has(r.Effects, EffectWeekN0) {
		t.Errorf("week 5: %+v", r)
	}
	if r2 := DailyCheck(pol, r.Ticket, din("2027-02-06")); has(r2.Effects, EffectWeekN1) {
		t.Error("N-1 sent twice")
	}
	r = DailyCheck(pol, r.Ticket, din("2027-02-12"))
	if !has(r.Effects, EffectWeekN0) || r.Ticket.WeekN0LockedAt == "" || r.Ticket.LepLocked {
		t.Errorf("week 6: %+v", r)
	}
	if r2 := DailyCheck(pol, r.Ticket, din("2027-02-13")); has(r2.Effects, EffectWeekN0) {
		t.Error("N0 sent twice")
	}
}

func TestBlockGateAtCap(t *testing.T) {
	p := pol
	p.BlockGateAtCap = true
	tk := approved()
	tk.RecordStart = "2027-01-01 08:00:00"
	tk.ReminderNotified = true
	r := DailyCheck(p, tk, din("2027-02-12"))
	if !r.Ticket.LepLocked || !has(r.Effects, EffectGateSync) || !has(r.Effects, EffectWeekN0) {
		t.Errorf("%+v", r)
	}
}

func TestDailyDoesNotMutateInput(t *testing.T) {
	tk := approved()
	DailyCheck(pol, tk, din("2027-02-26"))
	if tk.Status != StatusApproved || tk.ClosedAt != "" {
		t.Error("input ticket was mutated")
	}
}
