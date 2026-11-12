// Package permit holds the work-entry-permit (WEP/LEP) rules: validity,
// expiry, duplicate gate, renewal, review order and id generation.
//
// WHY pure functions again: same reason as the ticket package. The rules
// take values and return decisions; the caller (HTTP handler or job) loads
// data, calls the rule, and saves the result in one transaction.
package permit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Permit statuses. "Renewed by LEP-xxxxx" is free text built from the new LEP.
const (
	StatusPendingStage1 = "PendingStage1"
	StatusPendingFinal  = "PendingFinal"
	StatusApproved      = "Approved"
	StatusRejected      = "Rejected"
	StatusResigned      = "Resigned"
	StatusExpiredGrace  = "Expired in grace period"
	StatusExpired       = "Expired"
	renewedPrefix       = "Renewed by "
)

// RenewedBy builds the status text for a superseded permit.
func RenewedBy(lep string) string { return renewedPrefix + lep }

// IsRenewed reports whether status is a "Renewed by ..." value.
func IsRenewed(status string) bool {
	return strings.HasPrefix(strings.TrimSpace(status), renewedPrefix)
}

// IsExpiredLike is true for both expiry statuses.
func IsExpiredLike(status string) bool {
	s := strings.TrimSpace(status)
	return s == StatusExpired || s == StatusExpiredGrace
}

// ---- LEP validity (section 3) ----

// Reason names the date that limited the LEP.
type Reason string

const (
	ReasonMCU      Reason = "MCU validity"
	ReasonContract Reason = "Contract validity"
	ReasonVisa     Reason = "Work visa validity"
	ReasonPassport Reason = "Passport validity"
)

// ErrNoValidity is returned when no usable date exists at all.
var ErrNoValidity = errors.New("Could not determine permit validity - MCU date is missing")

// ValidityInput lists every date that can limit a LEP. Blank means absent.
type ValidityInput struct {
	Foreign            bool
	McuValidUntil      dates.Day
	ContractValidUntil dates.Day
	Visa, Itas, Itap   dates.Day
	PassportValidUntil dates.Day
}

// ResolveValidity returns the earliest limiting date and why. Ties go to the
// first reason in the order MCU, contract, visa, passport.
func ResolveValidity(p policy.Policy, in ValidityInput) (dates.Day, Reason, error) {
	type cand struct {
		day    dates.Day
		reason Reason
	}
	var cands []cand
	if in.McuValidUntil != "" {
		day := in.McuValidUntil
		// WHY grace: the LEP ends before the MCU does (owner setting, weeks or days).
		if g := p.McuLepGraceDays(); g > 0 {
			day = day.AddDays(-g)
		}
		cands = append(cands, cand{day, ReasonMCU})
	}
	if in.ContractValidUntil != "" {
		cands = append(cands, cand{in.ContractValidUntil, ReasonContract})
	}
	if in.Foreign {
		if v := earliest(in.Visa, in.Itas, in.Itap); v != "" {
			cands = append(cands, cand{v, ReasonVisa})
		}
		if in.PassportValidUntil != "" {
			cands = append(cands, cand{in.PassportValidUntil, ReasonPassport})
		}
	}
	if len(cands) == 0 {
		return "", "", ErrNoValidity
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.day < best.day { // strict: ties keep the earlier reason
			best = c
		}
	}
	return best.day, best.reason, nil
}

// earliest returns the smallest non-blank day, or "".
func earliest(days ...dates.Day) dates.Day {
	var out dates.Day
	for _, d := range days {
		if d != "" && (out == "" || d < out) {
			out = d
		}
	}
	return out
}

// ---- expiry job (section 6) ----

// Expiry is the outcome of the daily expiry check for one permit.
type Expiry struct {
	Status      string
	Changed     bool
	LockTickets bool // the permit just left Approved: lock its tickets
}

// ExpiryStatus evaluates one permit. Only Approved or expired-like permits
// are looked at. Valid until today or later means still valid.
func ExpiryStatus(p policy.Policy, status string, validUntil, today dates.Day) Expiry {
	status = strings.TrimSpace(status)
	same := Expiry{Status: status}
	if validUntil == "" || (status != StatusApproved && !IsExpiredLike(status)) {
		return same
	}
	past := dates.DaysBetween(validUntil, today)
	var next string
	switch {
	case past <= 0:
		return same
	case past <= p.LepExpiryGraceDays:
		next = StatusExpiredGrace
	default:
		next = StatusExpired
	}
	return Expiry{Status: next, Changed: next != status, LockTickets: status == StatusApproved}
}

// ---- duplicate gate (section 4) ----

// Kind is the duplicate decision.
type Kind string

const (
	KindNone   Kind = "none"   // allowed, nothing to reuse
	KindActive Kind = "active" // blocked: person already has a live permit
	KindReuse  Kind = "reuse"  // allowed, offer the old permit's details
)

// Previous is the latest earlier permit for the same person.
type Previous struct {
	Exists        bool
	Status        string
	Company       string
	McuValidUntil dates.Day
	VisaExpiry    dates.Day
	LepValidUntil dates.Day
}

// Current is what the new request carries.
type Current struct {
	Company       string
	McuValidUntil dates.Day
	Foreign       bool
	VisaExpiry    dates.Day
}

// Decision is the result of the duplicate gate.
type Decision struct {
	Kind               Kind
	McuRenewal         bool
	VisaRenewal        bool
	VisaRenewalPending bool
}

func sameCompany(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// Decide ports evaluateWepDuplicateDecision_ in its documented order.
func Decide(prev Previous, cur Current, today dates.Day) Decision {
	none := Decision{Kind: KindNone}
	status := strings.TrimSpace(prev.Status)
	if !prev.Exists || IsRenewed(status) || status == StatusResigned {
		return none
	}
	// A new company always allows a new permit.
	if !sameCompany(prev.Company, cur.Company) {
		return none
	}
	mcuChanged := cur.McuValidUntil != prev.McuValidUntil
	visaChanged := cur.Foreign && cur.VisaExpiry != "" && cur.VisaExpiry != prev.VisaExpiry
	docChanged := mcuChanged || visaChanged

	if status == StatusPendingStage1 || status == StatusPendingFinal || status == "" {
		return Decision{Kind: KindActive}
	}
	lepLive := prev.LepValidUntil == "" || prev.LepValidUntil >= today
	if status == StatusApproved && lepLive {
		if !docChanged {
			if cur.Foreign && cur.VisaExpiry == "" {
				return Decision{Kind: KindReuse, VisaRenewalPending: true}
			}
			return Decision{Kind: KindActive}
		}
		return Decision{Kind: KindReuse, McuRenewal: mcuChanged, VisaRenewal: visaChanged}
	}
	if status == StatusApproved || IsExpiredLike(status) {
		return Decision{Kind: KindReuse}
	}
	return none
}

// ---- renewal (section 5) ----

// Existing is a stored permit, for the renewal sweep.
type Existing struct {
	ID, PersonalID, Status, LEP string
}

// Renewal says: set permit ID to NewStatus, deactivate it at the gate and
// lock the tickets of its LEP.
type Renewal struct {
	ID, LEP, NewStatus string
}

// RenewalTargets lists the permits superseded by a newly issued LEP: same
// person, Approved or expired-like (not already renewed), with a real LEP
// number. Resigned and Rejected permits are left alone.
func RenewalTargets(personalID, newLEP string, all []Existing) []Renewal {
	var out []Renewal
	for _, e := range all {
		if e.PersonalID != personalID || e.LEP == "" || e.LEP == newLEP {
			continue
		}
		if e.Status == StatusApproved || IsExpiredLike(e.Status) {
			out = append(out, Renewal{ID: e.ID, LEP: e.LEP, NewStatus: RenewedBy(newLEP)})
		}
	}
	return out
}

// ---- reviews (section 2) ----

// Stage is a WEP review stage.
type Stage string

const (
	StageStage1 Stage = "stage1"
	StageFinal  Stage = "final"
)

// Errors from reviews.
var (
	ErrAlreadyActioned = errors.New("This request was already actioned by someone else")
	ErrNoteTooShort    = errors.New("Please write a longer note for a rejection")
	ErrUnknownStage    = errors.New("Unknown review stage")
)

// CheckRejectionNote requires the configured minimum number of words.
func CheckRejectionNote(p policy.Policy, note string) error {
	if len(strings.Fields(note)) < p.RejectionNoteMinWords {
		return fmt.Errorf("%w (at least %d words)", ErrNoteTooShort, p.RejectionNoteMinWords)
	}
	return nil
}

// Review returns the next status. WHY the status guard: each action is only
// allowed from its own pending status, so a stale browser tab or two
// reviewers cannot reopen or overwrite a finished request (owner decision,
// permits doc 2.1). An empty status counts as PendingStage1.
func Review(p policy.Policy, stage Stage, status string, approve bool, note string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = StatusPendingStage1
	}
	var from, onApprove string
	switch stage {
	case StageStage1:
		from, onApprove = StatusPendingStage1, StatusPendingFinal
	case StageFinal:
		from, onApprove = StatusPendingFinal, StatusApproved
	default:
		return "", ErrUnknownStage
	}
	if status != from {
		return "", ErrAlreadyActioned
	}
	if approve {
		return onApprove, nil
	}
	if err := CheckRejectionNote(p, note); err != nil {
		return "", err
	}
	return StatusRejected, nil
}

// ---- ids (section 1) ----

const (
	idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	idLength   = 5
	// randomLimit rejects bytes >= 252 so every letter is equally likely
	// (256 is not a multiple of 36).
	randomLimit = 252
)

// NewID makes a code like "LEP-A1B2C" from crypto/rand.
func NewID(prefix string) (string, error) { return newID(prefix, rand.Reader) }

func newID(prefix string, r io.Reader) (string, error) {
	out := make([]byte, 0, idLength)
	buf := make([]byte, 1)
	for len(out) < idLength {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("generate id: %w", err)
		}
		if buf[0] >= randomLimit {
			continue
		}
		out = append(out, idAlphabet[int(buf[0])%len(idAlphabet)])
	}
	return prefix + string(out), nil
}

// UniqueID retries until exists reports the code is free. The database
// unique index is still the final guard against a race.
func UniqueID(prefix string, attempts int, exists func(string) (bool, error)) (string, error) {
	for i := 0; i < attempts; i++ {
		id, err := NewID(prefix)
		if err != nil {
			return "", err
		}
		taken, err := exists(id)
		if err != nil {
			return "", fmt.Errorf("check id: %w", err)
		}
		if !taken {
			return id, nil
		}
	}
	return "", errors.New("could not find a free id")
}
