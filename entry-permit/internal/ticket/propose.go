package ticket

import (
	"errors"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Errors returned by Propose. The first failing check stops the proposal.
var (
	ErrNoLEP             = errors.New("LEP number is required")
	ErrWaiverTicket      = errors.New("Waiver tickets are read-only")
	ErrDatesRequired     = errors.New("Planned arrival and departure are required")
	ErrDepartureBefore   = errors.New("Departure cannot be before arrival")
	ErrArrivalPast       = errors.New("Planned arrival cannot be before today")
	ErrNoPermit          = errors.New("Permit not found")
	ErrPermitNotActive   = errors.New("Permit is not approved")
	ErrNoLocation        = errors.New("Work location (Onshore / Offshore) is required")
	ErrMCUUnfit          = errors.New("MCU result is Unfit, cannot propose a working ticket")
	ErrAfterValidUntil   = errors.New("Departure cannot be after the LEP valid-until date")
	ErrPendingRequest    = errors.New("A ticket request is already waiting for review")
	ErrWindowNotOpen     = errors.New("The next ticket can only be proposed closer to the current departure")
	ErrBeforePreviousEnd = errors.New("Arrival cannot be before the previous ticket ends")
	ErrFatigueConsent    = errors.New("Fatigue survey consent is required for this proposal")
	ErrNoteAgreement     = errors.New("You must acknowledge the MCU note disclaimer")
	ErrMedicationAnswer  = errors.New("Answer whether you take personal medication (Y or N)")
	ErrMedicationDetail  = errors.New("Medication details and a medicine photo are required")
)

// IsMCUUnfit: a blank result counts as unfit; text containing "unfit" or
// "tidak" (Indonesian "not") is unfit.
func IsMCUUnfit(result string) bool {
	v := strings.ToLower(strings.TrimSpace(result))
	return v == "" || strings.Contains(v, "unfit") || strings.Contains(v, "tidak")
}

// IsMCUFitWithNote: contains "note" or "catatan", and is not unfit.
func IsMCUFitWithNote(result string) bool {
	if IsMCUUnfit(result) {
		return false
	}
	v := strings.ToLower(result)
	return strings.Contains(v, "note") || strings.Contains(v, "catatan")
}

// ConsentGiven reports whether a form answer counts as consent (y or yes).
func ConsentGiven(s string) bool {
	v := strings.ToLower(strings.TrimSpace(s))
	return v == "y" || v == "yes"
}

// ProposalInput gathers everything Propose needs. The LEP access session
// (check 2) is verified by the caller before this is built.
type ProposalInput struct {
	LEP                string
	Today              dates.Day
	Arrival, Departure string // raw input, validated here
	PermitFound        bool
	PermitApproved     bool
	PermitLocation     string
	FormLocation       string
	Wni                bool
	McuResult          string
	LepValidUntil      dates.Day
	Tickets            []Ticket // every ticket of this LEP

	FatigueConsent    bool
	McuNoteAgreement  bool
	MedicationAnswer  string // "Y", "N" or blank
	MedicationDetails string
	MedicinePhoto     bool
}

// Proposal is what gets stored on a successful new ticket.
type Proposal struct {
	Analysis    Analysis
	RotationNo  int
	FatigueDue  dates.Day
	Status      Status
	DoctorNote  string
	Arrival     dates.Day
	Departure   dates.Day
	FitWithNote bool
}

// Propose runs checks 1 to 11 in the legacy order. Status is always Approved:
// the owner kept automatic approval (tickets doc section 2.1).
func Propose(p policy.Policy, in ProposalInput) (Proposal, error) {
	arrival, departure, err := checkBasics(in)
	if err != nil {
		return Proposal{}, err
	}
	location, err := checkPermit(in)
	if err != nil {
		return Proposal{}, err
	}
	if IsMCUUnfit(in.McuResult) {
		return Proposal{}, ErrMCUUnfit
	}
	if in.LepValidUntil != "" && departure > in.LepValidUntil {
		return Proposal{}, ErrAfterValidUntil
	}
	if err := checkPipelineAndWindow(p, in); err != nil {
		return Proposal{}, err
	}

	a := Analyze(p, in.Tickets, arrival, departure, location, in.Wni)
	if a.ArrivalBeforePreviousEnd {
		return Proposal{}, ErrBeforePreviousEnd
	}
	fitNote := IsMCUFitWithNote(in.McuResult)
	if err := checkConsent(a, in, fitNote); err != nil {
		return Proposal{}, err
	}

	fatigueWeek := p.FatigueWeek(p.WorkWeeks(location, in.Wni))
	note := "_"
	if fitNote {
		note = "MCU note self-declaration"
	} else if a.Pattern != PatternNormal {
		note = a.Pattern
	}
	return Proposal{
		Analysis: a, RotationNo: RotationNo(in.Tickets),
		FatigueDue: arrival.AddDays(fatigueWeek*7 - 1),
		Status:     StatusApproved, DoctorNote: note,
		Arrival: arrival, Departure: departure, FitWithNote: fitNote,
	}, nil
}

// checkBasics covers checks 1 and 3: LEP, dates, and "not before today".
func checkBasics(in ProposalInput) (dates.Day, dates.Day, error) {
	lep := strings.ToUpper(strings.TrimSpace(in.LEP))
	if lep == "" {
		return "", "", ErrNoLEP
	}
	if strings.HasPrefix(lep, "WVR-") {
		return "", "", ErrWaiverTicket
	}
	if strings.TrimSpace(in.Arrival) == "" || strings.TrimSpace(in.Departure) == "" {
		return "", "", ErrDatesRequired
	}
	arrival, err := dates.ParseDay(in.Arrival)
	if err != nil {
		return "", "", err
	}
	departure, err := dates.ParseDay(in.Departure)
	if err != nil {
		return "", "", err
	}
	if departure < arrival {
		return "", "", ErrDepartureBefore
	}
	if arrival < in.Today {
		return "", "", ErrArrivalPast
	}
	return arrival, departure, nil
}

// checkPermit covers checks 4 and 5; the permit's own location wins.
func checkPermit(in ProposalInput) (string, error) {
	if !in.PermitFound {
		return "", ErrNoPermit
	}
	if !in.PermitApproved {
		return "", ErrPermitNotActive
	}
	location := NormalizeLocation(in.PermitLocation)
	if location == "" {
		location = NormalizeLocation(in.FormLocation)
	}
	if location == "" {
		return "", ErrNoLocation
	}
	return location, nil
}

// checkPipelineAndWindow covers checks 8 and 9.
func checkPipelineAndWindow(p policy.Policy, in ProposalInput) error {
	var working *Ticket
	for i, t := range in.Tickets {
		if t.Work && t.Status == StatusPendingDoctor && IsExtendedPattern(t.Pattern) {
			return ErrPendingRequest
		}
		// A working ticket is Approved, not closed, and still running today.
		if t.Work && t.Status == StatusApproved && t.ClosedAt == "" && in.Today <= t.Departure {
			if working == nil || t.Departure > working.Departure {
				working = &in.Tickets[i]
			}
		}
	}
	if working != nil && in.Today < working.Departure.AddDays(-p.ProposalWindowDays) {
		return ErrWindowNotOpen
	}
	return nil
}

// checkConsent covers check 11: fatigue consent and the Fit-with-Note
// declarations.
func checkConsent(a Analysis, in ProposalInput, fitNote bool) error {
	if (a.RotationExtension || a.ShortBreak) && !in.FatigueConsent {
		return ErrFatigueConsent
	}
	if !fitNote {
		return nil
	}
	if !in.McuNoteAgreement {
		return ErrNoteAgreement
	}
	ans := strings.ToUpper(strings.TrimSpace(in.MedicationAnswer))
	if ans != "Y" && ans != "N" {
		return ErrMedicationAnswer
	}
	if ans == "Y" && (strings.TrimSpace(in.MedicationDetails) == "" || !in.MedicinePhoto) {
		return ErrMedicationDetail
	}
	return nil
}
