package settings

import "entry-permit/internal/policy"

// Value types.
const (
	TypeInt    = "int"
	TypeBool   = "bool"   // stored as "on" / "off"
	TypeChoice = "choice" // one of Options
	TypeList   = "list"   // comma-separated text, at least one entry
	TypeURL    = "url"    // an absolute http(s) address; empty only when AllowEmpty
)

// Group names, in the order the page shows them.
const (
	GroupRotation = "Rotation and tickets"
	GroupPermit   = "Permits and waivers"
	GroupReviews  = "Reviews"
	GroupLogin    = "Staff login"
	GroupTicket   = "Working schedule page"
	GroupCerts    = "Certificates"
	GroupForms    = "Request forms"
	GroupMail     = "E-mail"
	GroupGate     = "Gate"
	GroupJobs     = "Scheduled jobs"
	GroupMcu      = "Medical checkup portal"
)

// Def describes one adjustable value. The code holds only the default; the
// value in the database wins (plan section 3a).
type Def struct {
	Key, Group, Label, Type, Unit, Default string
	Min, Max                               int // TypeInt only
	Options                                []string
	AllowEmpty                             bool // TypeURL: an empty value is allowed (it hides the links)
	Description                            string
}

func intDef(group, key, label, unit, def string, lo, hi int, desc string) Def {
	return Def{Key: key, Group: group, Label: label, Type: TypeInt, Unit: unit, Default: def, Min: lo, Max: hi, Description: desc}
}

// Defs lists every setting that feeds policy.Policy. Settings for later slices
// (colours, form toggles, job hours, upload limits) are added with the slice
// that reads them, so no setting exists that nothing uses.
var Defs = []Def{
	intDef(GroupRotation, "onshore_weeks", "Onshore weeks on site", "weeks", "8", 1, 52, "Longest stay on an onshore rotation."),
	intDef(GroupRotation, "offshore_weeks", "Offshore weeks on site", "weeks", "6", 1, 52, "Longest stay on an offshore rotation."),
	intDef(GroupRotation, "rest_days_wni", "Rest days between rotations (WNI)", "days", "14", 0, 90, "Required rest for Indonesian workers."),
	intDef(GroupRotation, "rest_days_foreign", "Rest days between rotations (foreign)", "days", "14", 0, 90, "Required rest for foreign workers."),
	intDef(GroupRotation, "extension_days", "Extension length", "days", "7", 0, 60, "Days added by one extension."),
	intDef(GroupRotation, "minimum_rest_days", "Minimum rest after an extension", "days", "3", 0, 60, "Rest required after an extended rotation."),
	intDef(GroupRotation, "proposal_window_days", "Proposal window", "days", "14", 1, 90, "How far ahead a ticket may be proposed."),
	intDef(GroupRotation, "fatigue_weeks_before_cap", "Fatigue consent starts before the cap", "weeks", "2", 0, 8, "The fatigue consent applies from (cap minus this) weeks."),
	{Key: "block_gate_at_cap", Group: GroupRotation, Label: "Block the gate at the week cap", Type: TypeBool, Default: "off", Description: "Off: reaching the cap only sends a notice."},
	intDef(GroupRotation, "next_rotation_reminder_days", "Next-rotation reminder", "days", "7", 0, 60, "Days before departure that the reminder is sent."),
	intDef(GroupRotation, "short_ticket_days", "Short ticket length", "days", "7", 1, 60, "Tickets shorter than this use the short reminder."),
	intDef(GroupRotation, "short_ticket_reminder_days", "Short ticket reminder", "days", "2", 0, 60, "Days before departure for a short ticket."),
	intDef(GroupRotation, "cap_warning_weeks_before", "Week cap warning", "weeks", "1", 0, 8, "Warn this many weeks before the cap."),

	{Key: "mcu_portal_url", Group: GroupMcu, Label: "Medical checkup submission address", Type: TypeURL, AllowEmpty: true, Default: "https://medical-activities.ayahaif.my.id/mcu-submit/signin", Description: "Where companies send medical checkup results. The Entry Permit home page and menu link to it; leave empty to hide those links."},

	intDef(GroupPermit, "mcu_lep_grace_value", "LEP grace after MCU expiry", "", "2", 0, 104, "The LEP is valid until the MCU valid-until minus this."),
	{Key: "mcu_lep_grace_unit", Group: GroupPermit, Label: "Grace unit", Type: TypeChoice, Options: []string{"weeks", "days"}, Default: "weeks", Description: "Weeks or days."},
	intDef(GroupPermit, "lep_expiry_grace_days", "Expired-in-grace period", "days", "14", 0, 90, "Days a permit stays in the grace state after expiry."),
	intDef(GroupPermit, "waiver_max_days", "Waiver longest stay", "days", "7", 1, 60, "Most days one waiver may cover."),
	intDef(GroupPermit, "waiver_company_cap_days_per_year", "Waiver days per company per year", "days", "14", 1, 365, "Checked-in days counted against this cap."),

	intDef(GroupReviews, "approver_reminder_hours", "Approver reminder", "hours", "24", 1, 168, "A pending request is reminded after this long."),
	intDef(GroupReviews, "rejection_note_min_words", "Rejection note length", "words", "3", 0, 50, "Fewest words a rejection note may have."),

	intDef(GroupLogin, "staff_login_max_tries", "Wrong PINs before a lock", "tries", "5", 1, 20, ""),
	intDef(GroupLogin, "staff_lock_minutes", "Lock length", "minutes", "15", 1, 1440, ""),
	intDef(GroupLogin, "staff_session_hours", "Login lasts", "hours", "12", 1, 72, "Applies to new sign-ins."),

	intDef(GroupTicket, "ticket_access_max_tries", "Wrong birth dates before a lock", "tries", "5", 1, 20, "Counted per permit number on the working schedule page."),
	intDef(GroupTicket, "ticket_access_lock_minutes", "Lock length", "minutes", "15", 1, 1440, ""),
	intDef(GroupTicket, "ticket_access_session_minutes", "Schedule page stays open", "minutes", "30", 5, 240, "After the permit number and birth date are accepted."),

	intDef(GroupMail, "outbox_max_attempts", "Send attempts before a mail is marked failed", "tries", "14", 1, 100, "A failed mail is listed for an admin to send again."),
	intDef(GroupMail, "outbox_retry_minutes", "Wait between send attempts", "minutes", "15", 1, 1440, "The wait grows: the Nth retry waits N times this long."),

	intDef(GroupGate, "gate_sync_minutes", "Minutes between gate syncs", "minutes", "5", 1, 60, "A newly approved permit can be refused at the gate until the next sync (an approval also syncs at once)."),
	intDef(GroupGate, "gate_duplicate_scan_seconds", "Double-tap window", "seconds", "5", 0, 60, "The same scan by the same officer inside this time is blocked."),
	{Key: "ticket_completing_checkout_reasons", Group: GroupGate, Label: "Check-out reasons that complete the ticket", Type: TypeList, Default: "Sesuai jadwal keluar, Selesai awal", Description: "Comma-separated, exact text from the Checkout reasons list."},

	intDef(GroupJobs, "job_hour_departure_check", "Hour of the daily departure check", "hour (0-23)", "6", 0, 23, "Closes tickets past their departure, sends rotation reminders, and ends visits that are over. Jakarta time."),
	intDef(GroupJobs, "job_hour_expiry_check", "Hour of the daily permit expiry check", "hour (0-23)", "6", 0, 23, "Moves permits to the grace period and then to Expired, and locks their tickets. Jakarta time."),
	intDef(GroupJobs, "job_hour_stale_reminder", "Hour of the daily reviewer reminder", "hour (0-23)", "7", 0, 23, "Mails each review stage when a request has waited longer than the reminder time. Jakarta time."),
	intDef(GroupJobs, "job_hour_admin_digest", "Hour of the admin digest", "hour (0-23)", "8", 0, 23, "The digest goes out on its weekday, every few weeks, at this hour. Jakarta time."),
	intDef(GroupJobs, "digest_weeks", "Weeks between digests", "weeks", "2", 1, 12, "The count starts from a fixed Monday, so a restart never moves the schedule."),
	intDef(GroupJobs, "digest_weekday", "Weekday of the digest", "1 Monday to 7 Sunday", "1", 1, 7, ""),
	intDef(GroupJobs, "digest_expiry_window_days", "Digest: permits ending within", "days", "30", 1, 365, "Work permits whose valid-until day falls in this window are listed."),
	intDef(GroupJobs, "digest_urgent_days", "Digest: mark urgent when this many days or fewer remain", "days", "7", 0, 365, ""),
	intDef(GroupJobs, "digest_grace_urgent_days", "Digest: mark urgent when this much grace or less remains", "days", "3", 0, 60, ""),
	intDef(GroupJobs, "outbox_drain_minutes", "Minutes between mail sends", "minutes", "60", 1, 1440, "Mail that could not be sent is tried again at this interval, within the retry rules of the E-mail group."),

	intDef(GroupForms, "upload_max_kb", "Largest upload", "KB", "700", 50, 5000, "Largest document or photo the permit form accepts (PDF, JPG or PNG)."),
	{Key: "email_mx_check", Group: GroupForms, Label: "Check that the email domain can receive mail", Type: TypeBool, Default: "on", Description: "Switch off only when DNS is down, so requests are not blocked."},
	{Key: "bpjs_kes_shown", Group: GroupForms, Label: "Show the BPJS Kesehatan box", Type: TypeBool, Default: "on", Description: "Indonesian workers only."},
	{Key: "bpjs_kes_required", Group: GroupForms, Label: "Require the BPJS Kesehatan number", Type: TypeBool, Default: "off", Description: "Only counts while the box is shown."},
	{Key: "bpjs_tk_shown", Group: GroupForms, Label: "Show the BPJS Ketenagakerjaan box", Type: TypeBool, Default: "on", Description: "Indonesian workers only."},
	{Key: "bpjs_tk_required", Group: GroupForms, Label: "Require the BPJS Ketenagakerjaan number", Type: TypeBool, Default: "off", Description: "Only counts while the box is shown."},
	{Key: "contract_shown", Group: GroupForms, Label: "Show the contract document and date", Type: TypeBool, Default: "on", Description: ""},
	{Key: "contract_required", Group: GroupForms, Label: "Require the contract document and date", Type: TypeBool, Default: "off", Description: "Only counts while the contract is shown."},
	{Key: "certificate_required", Group: GroupForms, Label: "Require the certificate of competence", Type: TypeBool, Default: "on", Description: "For positions whose list entry says a certificate is needed."},
	{Key: "waiver_visitor_company_enabled", Group: GroupForms, Label: "Allow visitor-company waivers", Type: TypeBool, Default: "on", Description: "When off, only Government visitors can request a waiver."},
	{Key: "perpetual_cert_positions", Group: GroupCerts, Label: "Positions whose certificate never expires", Type: TypeList, Default: "Paramedic, Nurse, Doctor / Site Physician", Description: "Comma-separated position names."},
}

// byKey finds a Def.
func byKey(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// fields maps a key to the Policy field it sets. One table, so adding a
// setting is one Def plus one line here.
func fields(p *policy.Policy) map[string]*int {
	return map[string]*int{
		"onshore_weeks": &p.OnshoreWeeks, "offshore_weeks": &p.OffshoreWeeks,
		"rest_days_wni": &p.RestDaysWNI, "rest_days_foreign": &p.RestDaysForeign,
		"extension_days": &p.ExtensionDays, "minimum_rest_days": &p.MinimumRestDays,
		"proposal_window_days": &p.ProposalWindowDays, "fatigue_weeks_before_cap": &p.FatigueWeeksBeforeCap,
		"next_rotation_reminder_days": &p.ReminderDays, "short_ticket_days": &p.ShortTicketDays,
		"short_ticket_reminder_days": &p.ShortLeadDays, "cap_warning_weeks_before": &p.CapWarningWeeks,
		"mcu_lep_grace_value": &p.McuLepGraceValue, "lep_expiry_grace_days": &p.LepExpiryGraceDays,
		"waiver_max_days": &p.WaiverMaxDays, "waiver_company_cap_days_per_year": &p.WaiverCompanyCapDays,
		"approver_reminder_hours": &p.ApproverReminderHours, "rejection_note_min_words": &p.RejectionNoteMinWords,
		"staff_login_max_tries": &p.LoginMaxTries, "staff_lock_minutes": &p.LoginLockMinutes,
		"staff_session_hours": &p.SessionHours, "upload_max_kb": &p.UploadMaxKB,
		"ticket_access_max_tries": &p.TicketMaxTries, "ticket_access_lock_minutes": &p.TicketLockMinutes,
		"ticket_access_session_minutes": &p.TicketSessionMinutes,
		"outbox_max_attempts":           &p.OutboxMaxAttempts, "outbox_retry_minutes": &p.OutboxRetryMinutes,
		"gate_sync_minutes": &p.GateSyncMinutes, "gate_duplicate_scan_seconds": &p.GateDuplicateSeconds,
		"job_hour_departure_check": &p.JobHourDeparture, "job_hour_expiry_check": &p.JobHourExpiry,
		"job_hour_stale_reminder": &p.JobHourStale, "job_hour_admin_digest": &p.JobHourDigest,
		"digest_weeks": &p.DigestWeeks, "digest_weekday": &p.DigestWeekday,
		"digest_expiry_window_days": &p.DigestExpiryDays, "digest_urgent_days": &p.DigestUrgentDays,
		"digest_grace_urgent_days": &p.DigestGraceUrgentDays, "outbox_drain_minutes": &p.OutboxDrainMinutes,
	}
}

// boolFields maps a key to the Policy flag it sets, like fields does for numbers.
func boolFields(p *policy.Policy) map[string]*bool {
	return map[string]*bool{
		"block_gate_at_cap": &p.BlockGateAtCap, "email_mx_check": &p.EmailMXCheck,
		"bpjs_kes_shown": &p.BpjsKesShown, "bpjs_kes_required": &p.BpjsKesRequired,
		"bpjs_tk_shown": &p.BpjsTkShown, "bpjs_tk_required": &p.BpjsTkRequired,
		"contract_shown": &p.ContractShown, "contract_required": &p.ContractRequired,
		"certificate_required": &p.CertificateRequired, "waiver_visitor_company_enabled": &p.WaiverVisitorEnabled,
	}
}
