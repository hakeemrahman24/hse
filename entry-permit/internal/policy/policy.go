// Package policy holds every adjustable business number as one struct.
//
// WHY: the owner wants every adjustable value editable in the Settings page.
// The code therefore holds only defaults (Default); rule functions receive a
// Policy value and never read constants directly. The Settings loader will
// build a Policy from the database later (slice 1).
package policy

import "strings"

// Policy is passed by value into rule functions so a rule cannot change it.
type Policy struct {
	OnshoreWeeks    int
	OffshoreWeeks   int
	RestDaysWNI     int
	RestDaysForeign int
	ExtensionDays   int
	MinimumRestDays int

	McuLepGraceValue int
	McuLepGraceUnit  string // "weeks" or "days"

	// McuPortalURL is the address of the medical checkup submission portal; empty hides its links.
	McuPortalURL string

	FatigueWeeksBeforeCap int
	BlockGateAtCap        bool
	ProposalWindowDays    int

	ReminderDays         int
	ShortTicketDays      int
	ShortLeadDays        int
	CapWarningWeeks      int
	LepExpiryGraceDays   int
	WaiverMaxDays        int
	WaiverCompanyCapDays int
	// WaiverVisitorEnabled: visitor-company waivers may be requested (Government ones always may).
	WaiverVisitorEnabled  bool
	RejectionNoteMinWords int
	ApproverReminderHours int

	// Staff login: wrong PINs before a lock, minutes locked, hours a login lasts.
	LoginMaxTries    int
	LoginLockMinutes int
	SessionHours     int

	// Ticket page access: wrong date-of-birth tries per permit, minutes locked, minutes a session lasts.
	TicketMaxTries, TicketLockMinutes, TicketSessionMinutes int

	// E-mail outbox: send attempts before a mail is marked Failed, and the base
	// of the growing wait between attempts (attempt number x this many minutes).
	OutboxMaxAttempts, OutboxRetryMinutes int

	// Gate: minutes between syncs with the gate Worker, the window in which a
	// repeated scan counts as a double tap, and the check-out reasons that
	// complete a ticket (exact text from the Checkout reasons list).
	GateSyncMinutes, GateDuplicateSeconds int
	CompletingReasons                     []string

	// Scheduled jobs: the hour (0 to 23, Jakarta time) each daily job first runs.
	// A restart or a missed hour runs the job once that day, never twice.
	JobHourDeparture, JobHourExpiry int
	JobHourStale, JobHourDigest     int

	// OutboxDrainMinutes: how often queued mail is sent (and retried) on its own.
	OutboxDrainMinutes int

	// Admin digest: sent every DigestWeeks weeks on DigestWeekday (1 Monday to 7 Sunday).
	// It lists permits ending within DigestExpiryDays; a permit with DigestUrgentDays or
	// fewer left, or DigestGraceUrgentDays or fewer of grace left, is marked urgent.
	DigestWeeks, DigestWeekday, DigestExpiryDays, DigestUrgentDays, DigestGraceUrgentDays int

	// UploadMaxKB is the largest document the permit form accepts; EmailMXCheck
	// makes the form refuse an address whose domain cannot receive mail.
	UploadMaxKB  int
	EmailMXCheck bool

	// Form toggles (production values from the Reference sheet): a section can be
	// shown without being required. BPJS applies to Indonesians only.
	BpjsKesShown, BpjsKesRequired   bool
	BpjsTkShown, BpjsTkRequired     bool
	ContractShown, ContractRequired bool
	// CertificateShown: a position that needs a certificate gets the certificate section; off hides it for everyone.
	// CertificateRequired: with the section shown, the certificate must be sent (otherwise it is optional).
	CertificateShown, CertificateRequired bool

	// PerpetualCertPositions never expire their certificate (valid to 2090-12-31).
	PerpetualCertPositions []string
}

// Default returns the production values from the legacy system.
func Default() Policy {
	return Policy{
		OnshoreWeeks: 8, OffshoreWeeks: 6,
		RestDaysWNI: 14, RestDaysForeign: 14,
		ExtensionDays: 7, MinimumRestDays: 3,
		McuLepGraceValue: 2, McuLepGraceUnit: "weeks",
		FatigueWeeksBeforeCap: 2,
		BlockGateAtCap:        false,
		ProposalWindowDays:    14,
		ReminderDays:          7, ShortTicketDays: 7, ShortLeadDays: 2,
		CapWarningWeeks: 1, LepExpiryGraceDays: 14,
		WaiverMaxDays: 7, WaiverCompanyCapDays: 14, WaiverVisitorEnabled: true,
		RejectionNoteMinWords: 3, ApproverReminderHours: 24,
		LoginMaxTries: 5, LoginLockMinutes: 15, SessionHours: 12,
		TicketMaxTries: 5, TicketLockMinutes: 15, TicketSessionMinutes: 30,
		UploadMaxKB: 700, EmailMXCheck: true,
		OutboxMaxAttempts: 14, OutboxRetryMinutes: 15,
		GateSyncMinutes: 5, GateDuplicateSeconds: 5,
		JobHourDeparture: 6, JobHourExpiry: 6, JobHourStale: 7, JobHourDigest: 8,
		OutboxDrainMinutes: 60,
		DigestWeeks:        2, DigestWeekday: 1, DigestExpiryDays: 30, DigestUrgentDays: 7, DigestGraceUrgentDays: 3,
		CompletingReasons: []string{"Sesuai jadwal keluar", "Selesai awal"},
		BpjsKesShown:      true, BpjsTkShown: true, ContractShown: true, CertificateShown: true, CertificateRequired: true,
		PerpetualCertPositions: []string{"Paramedic", "Nurse", "Doctor / Site Physician"},
	}
}

// WorkWeeks is the week cap for a location. wni is kept in the signature
// because the owner may later split caps by nationality; today both match.
func (p Policy) WorkWeeks(location string, wni bool) int {
	if strings.EqualFold(strings.TrimSpace(location), "offshore") {
		return p.OffshoreWeeks
	}
	return p.OnshoreWeeks
}

// RestDays is the required rest between rotations.
func (p Policy) RestDays(wni bool) int {
	if wni {
		return p.RestDaysWNI
	}
	return p.RestDaysForeign
}

// McuLepGraceDays converts the grace setting (weeks or days) to days.
func (p Policy) McuLepGraceDays() int {
	if strings.EqualFold(p.McuLepGraceUnit, "days") {
		return p.McuLepGraceValue
	}
	return p.McuLepGraceValue * 7
}

// FatigueWeek is the week at which the fatigue consent starts: max(1, cap-N).
func (p Policy) FatigueWeek(maxWeeks int) int {
	w := maxWeeks - p.FatigueWeeksBeforeCap
	if w < 1 {
		return 1
	}
	return w
}

// IsPerpetualCertPosition reports whether the position's cert never expires.
func (p Policy) IsPerpetualCertPosition(position string) bool {
	want := strings.ToLower(strings.TrimSpace(position))
	for _, name := range p.PerpetualCertPositions {
		if strings.ToLower(strings.TrimSpace(name)) == want {
			return true
		}
	}
	return false
}
