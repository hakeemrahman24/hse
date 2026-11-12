// Package waiver holds the visiting-waiver (WVR) rules: id format, applicant
// type, dates, annual company cap, blocking waivers, review order and the
// post-visit departure job.
package waiver

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
)

// Waiver statuses.
const (
	StatusPendingDoctor      = "PendingDoctor"
	StatusPendingStage1      = "PendingStage1"
	StatusPendingSiteManager = "PendingSiteManager" // the Final stage
	StatusApproved           = "Approved"
	StatusRejected           = "Rejected"
	StatusCompleted          = "Completed"
	StatusCancelled          = "Cancelled"
	StatusExpired            = "Expired"
)

// Applicant types.
const (
	TypeGovernment     = "Government"
	TypeVisitorCompany = "VisitorCompany"
)

// Errors for the submit checks.
var (
	ErrIDRequired     = errors.New("NIK / Passport is required")
	ErrNIKFormat      = errors.New("An Indonesian visitor needs the 16-digit NIK (numbers only)")
	ErrPassportFormat = errors.New("A visitor from another country needs the passport number: 7 to 9 letters or numbers")
	ErrVisitorOff     = errors.New("Visitor company waivers are currently switched off")
	ErrNeedGovCompany = errors.New("A Government applicant must pick a Government company")
	ErrNoGovCompany   = errors.New("A visitor company applicant cannot pick a Government company")
	ErrDatesRequired  = errors.New("Visit from-date and to-date are required")
	ErrToBeforeFrom   = errors.New("Visit end cannot be before visit start")
	ErrFromPast       = errors.New("Visit start cannot be before today")
	ErrDuration       = errors.New("Visit duration is outside the allowed range")
	ErrDurationMatch  = errors.New("Duration does not match the visit dates")
	ErrCompanyCap     = errors.New("This company has reached its yearly waiver days")
	ErrUnknownStage   = errors.New("Unknown review stage")
)

var (
	nonAlnum   = regexp.MustCompile(`[^A-Za-z0-9]`)
	nikRe      = regexp.MustCompile(`^[0-9]{16}$`)
	passportRe = regexp.MustCompile(`^[A-Z0-9]{7,9}$`)
)

// IsIndonesian is true when the nationality is Indonesia (any case, spaces ignored). It is the same
// test medpost uses to choose between a NIK and a passport number.
func IsIndonesian(nationality string) bool {
	return strings.EqualFold(strings.TrimSpace(nationality), "Indonesia")
}

// CleanID strips everything but letters and digits, upper-cases, and checks the id against the
// visitor's nationality, as medpost does: an Indonesian gives the 16-digit NIK, everyone else the
// passport number (7 to 9 letters or digits). WHY by nationality and not "either one": a NIK typed
// for a foreigner, or a passport number for an Indonesian, is a mistake worth catching at the form.
func CleanID(raw, nationality string) (string, error) {
	s := strings.ToUpper(nonAlnum.ReplaceAllString(raw, ""))
	if s == "" {
		return "", ErrIDRequired
	}
	if IsIndonesian(nationality) {
		if !nikRe.MatchString(s) {
			return "", ErrNIKFormat
		}
		return s, nil
	}
	if !passportRe.MatchString(s) {
		return "", ErrPassportFormat
	}
	return s, nil
}

// NormalizeType maps blank to VisitorCompany (legacy default).
func NormalizeType(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), TypeGovernment) {
		return TypeGovernment
	}
	return TypeVisitorCompany
}

// CheckApplicant covers the applicant-type rules (8.1 item 1 and 2).
func CheckApplicant(applicantType string, visitorEnabled, companyIsGov bool) error {
	switch NormalizeType(applicantType) {
	case TypeGovernment:
		if !companyIsGov {
			return ErrNeedGovCompany
		}
	default:
		if !visitorEnabled {
			return ErrVisitorOff
		}
		if companyIsGov {
			return ErrNoGovCompany
		}
	}
	return nil
}

// CheckDates validates the visit window and returns the inclusive days.
// WHY derived: the duration is computed from the dates, and a typed duration
// that disagrees is refused (permits doc 8.3.1). durationTyped 0 = not given.
func CheckDates(p policy.Policy, from, to string, durationTyped int, today dates.Day) (int, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return 0, ErrDatesRequired
	}
	f, err := dates.ParseDay(from)
	if err != nil {
		return 0, err
	}
	t, err := dates.ParseDay(to)
	if err != nil {
		return 0, err
	}
	if t < f {
		return 0, ErrToBeforeFrom
	}
	if f < today {
		return 0, ErrFromPast
	}
	days := dates.InclusiveDays(f, t)
	if days < 1 || days > p.WaiverMaxDays {
		return 0, fmt.Errorf("%w (1 to %d days)", ErrDuration, p.WaiverMaxDays)
	}
	if durationTyped != 0 && durationTyped != days {
		return 0, ErrDurationMatch
	}
	return days, nil
}

// CheckCompanyCap enforces the yearly days per company. Government is exempt.
func CheckCompanyCap(p policy.Policy, applicantType string, usedDays, requestedDays int) error {
	if NormalizeType(applicantType) == TypeGovernment {
		return nil
	}
	if usedDays+requestedDays > p.WaiverCompanyCapDays {
		return fmt.Errorf("%w (%d used, %d requested, limit %d)", ErrCompanyCap, usedDays, requestedDays, p.WaiverCompanyCapDays)
	}
	return nil
}

// Waiver is the part of a waiver the blocking rule needs.
type Waiver struct {
	ID, Status, NIK, Name, DOB, Company, Type string
	From, To                                  dates.Day
}

func key(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func isTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case StatusCancelled, StatusRejected, StatusCompleted, StatusExpired:
		return true
	}
	return strings.Contains(strings.ToLower(status), "not approved")
}

func sameNonBlank(a, b string) bool { return a != "" && key(a) == key(b) }

// Blocking finds an earlier open waiver that blocks cand: not terminal, visit
// end today or later, and matching on NIK, or name + date of birth, or name +
// company. For a Government candidate the visit ranges must also overlap.
// WHY name + company stays: legacy behaviour (CHECK in permits doc 8.2).
func Blocking(cand Waiver, existing []Waiver, today dates.Day) *Waiver {
	for i, ex := range existing {
		if isTerminal(ex.Status) || ex.To < today {
			continue
		}
		same := sameNonBlank(cand.NIK, ex.NIK) ||
			(sameNonBlank(cand.Name, ex.Name) && sameNonBlank(cand.DOB, ex.DOB)) ||
			(sameNonBlank(cand.Name, ex.Name) && sameNonBlank(cand.Company, ex.Company))
		if !same {
			continue
		}
		if NormalizeType(cand.Type) == TypeGovernment && !(cand.From <= ex.To && ex.From <= cand.To) {
			continue
		}
		return &existing[i]
	}
	return nil
}

// Stage is a waiver review stage.
type Stage string

const (
	StageDoctor Stage = "doctor"
	StageStage1 Stage = "stage1"
	StageFinal  Stage = "final"
)

// Review returns the next status; each stage only acts on its own pending
// status (permits doc 2.1). Reject needs the note of at least N words.
func Review(p policy.Policy, stage Stage, status string, approve bool, note string) (string, error) {
	var from, onApprove string
	switch stage {
	case StageDoctor:
		from, onApprove = StatusPendingDoctor, StatusPendingStage1
	case StageStage1:
		from, onApprove = StatusPendingStage1, StatusPendingSiteManager
	case StageFinal:
		from, onApprove = StatusPendingSiteManager, StatusApproved
	default:
		return "", ErrUnknownStage
	}
	if strings.TrimSpace(status) != from {
		return "", permit.ErrAlreadyActioned
	}
	if approve {
		return onApprove, nil
	}
	if err := permit.CheckRejectionNote(p, note); err != nil {
		return "", err
	}
	return StatusRejected, nil
}

// CanSelfCancel: a requester may cancel only while PendingDoctor (the old UI
// also offered PendingStage1 but the server refused it; we follow the server).
func CanSelfCancel(status string) bool { return strings.TrimSpace(status) == StatusPendingDoctor }

// Ticket outcomes written by the waiver departure job.
const (
	ReasonPlannedPassed = "Planned departure passed"
	ReasonWindowEnded   = "Visit window ended before approval"
)

// Ticket statuses used here (the waiver ticket lives in the ticket table).
const (
	TicketApproved      = "Approved"
	TicketPendingDoctor = "PendingDoctor"
	TicketClosed        = "Closed"
	TicketCancelled     = "Cancelled"
)

// PostVisit is the outcome of the waiver departure job for one waiver.
type PostVisit struct {
	Changed      bool
	Status       string
	TicketStatus string
	TicketReason string
}

// AfterVisit decides what happens once the visit has ended (to-date before
// today). Approved becomes Completed and its ticket Closed; a waiver still
// pending becomes Cancelled, and a PendingDoctor ticket Cancelled.
func AfterVisit(status, ticketStatus string, to, today dates.Day) PostVisit {
	out := PostVisit{Status: status, TicketStatus: ticketStatus}
	if to >= today {
		return out
	}
	switch status {
	case StatusApproved:
		out.Status, out.Changed = StatusCompleted, true
	case StatusPendingDoctor, StatusPendingStage1, StatusPendingSiteManager:
		out.Status, out.Changed = StatusCancelled, true
	}
	switch ticketStatus {
	case TicketApproved:
		out.TicketStatus, out.TicketReason, out.Changed = TicketClosed, ReasonPlannedPassed, true
	case TicketPendingDoctor:
		out.TicketStatus, out.TicketReason, out.Changed = TicketCancelled, ReasonWindowEnded, true
	}
	return out
}

// GateStateExpired: an active gate row whose valid-until is before today
// becomes Expired and inactive.
func GateStateExpired(active bool, validUntil, today dates.Day) bool {
	return active && validUntil != "" && validUntil < today
}
