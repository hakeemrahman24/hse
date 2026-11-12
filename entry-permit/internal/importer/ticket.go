package importer

import (
	"fmt"
	"strconv"
	"strings"

	"entry-permit/internal/ticketlog"
)

// Column positions (0-based) in the TripTicket sheet, from COL_TICKET in the old Code.js.
const (
	tTimestamp    = 0
	tID           = 1
	tSourceType   = 2
	tPermitNo     = 3
	tRotationNo   = 8
	tArrival      = 9
	tDeparture    = 10
	tSubmitted    = 11
	tStatus       = 12
	tRecordStart  = 13
	tClosedAt     = 14
	tCloseReason  = 15
	tWeeksOnSite  = 16
	tFatigueDue   = 19
	tLepLocked    = 21
	tWeekN1       = 22
	tWeekN0       = 23
	tMcuResult    = 26
	tMedAnswer    = 28
	tMedDetails   = 29
	tDoctorReview = 32
	tDoctorNote   = 33
	tShortBreak   = 36
	tPattern      = 37
	tFatigueFlag  = 39
	tFatigueAgree = 40
	tGapDays      = 41
	tDep7dNotice  = 43
	tLocation     = 44
)

const maxTicketID = 12

// MappedTicket is one TripTicket row, ready to store. The requester's address is not in the
// sheet row: it is the permit's, and is filled in when the ticket is stored.
type MappedTicket struct {
	Ticket ticketlog.NewTicket
	Hash   string
}

// MapTicket turns one TripTicket row into a ticket.
func MapTicket(row []string, rowNo int) (MappedTicket, []Problem) {
	id := cell(row, tID)
	var ps []Problem
	bad := func(field, msg string, fatal bool) {
		ps = append(ps, Problem{Row: rowNo, ID: id, Field: field, Msg: msg, Fatal: fatal})
	}
	day := func(col int, field string, required bool) string {
		d, ok := dayOfAny(cell(row, col))
		switch {
		case !ok || (required && d == ""):
			bad(field, fmt.Sprintf("%s %q cannot be read", field, cell(row, col)), required)
		}
		return d
	}
	stamp := func(col int, field string) string {
		m, ok := stampOfDay(cell(row, col))
		if !ok {
			bad(field, fmt.Sprintf("%s %q cannot be read; left blank", field, cell(row, col)), false)
		}
		return m
	}
	atoi := func(col int, field string) int {
		v := text(row, col)
		if v == "" {
			return 0
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			bad(field, fmt.Sprintf("%s %q is not a number; left as 0", field, v), false)
		}
		return n
	}

	status := cell(row, tStatus)
	if id == "" || len(id) > maxTicketID {
		bad("ticket_id", fmt.Sprintf("ticket id %q is missing or longer than %d characters", id, maxTicketID), true)
	}
	if !knownTicketStatus(status) {
		bad("status", fmt.Sprintf("status %q is not one the system knows", status), true)
	}
	var work bool
	switch strings.ToLower(cell(row, tSourceType)) {
	case "workentry":
		work = true
	case "waiver":
	default:
		bad("source_type", fmt.Sprintf("source type %q is not WorkEntry or Waiver", cell(row, tSourceType)), true)
	}
	permitNo := strings.ToUpper(cell(row, tPermitNo))
	if permitNo == "" {
		bad("permit_no", "the ticket names no permit number", true)
	}
	submitted, ok := Moment(cell(row, tSubmitted))
	if !ok || submitted == "" {
		bad("submit_time", fmt.Sprintf("submit time %q cannot be read", cell(row, tSubmitted)), true)
	}

	pattern := text(row, tPattern)
	n := ticketlog.NewTicket{
		ID: id, LEP: permitNo, Work: work, Status: status,
		Arrival: day(tArrival, "arrival", true), Departure: day(tDeparture, "departure", true),
		Location: normaliseLocation(cell(row, tLocation)), Pattern: pattern, RotationNo: atoi(tRotationNo, "rotation_no"),
		FatigueDue: day(tFatigueDue, "fatigue_due", false), GapDays: atoi(tGapDays, "gap_days"),
		ShortBreak: truthy(cell(row, tShortBreak)), RotationExtension: strings.Contains(pattern, "Extended"), FatigueAgree: truthy(cell(row, tFatigueAgree)),
		DoctorReview: text(row, tDoctorReview), DoctorNote: text(row, tDoctorNote), McuResult: mcuResult(text(row, tMcuResult)),
		MedAnswer: medAnswer(cell(row, tMedAnswer)), MedDetails: text(row, tMedDetails), SubmittedAt: submitted,
		RecordStart: stamp(tRecordStart, "record_start"), ClosedAt: stamp(tClosedAt, "closed_at"), ClosedReason: text(row, tCloseReason),
		LepLocked: truthy(cell(row, tLepLocked)), WeeksOnSite: atoi(tWeeksOnSite, "weeks_on_site"),
		ReminderNotified: !Blank(cell(row, tDep7dNotice)), FatigueFlag: truthy(cell(row, tFatigueFlag)),
		WeekN1SentAt: stamp(tWeekN1, "week_n1_sent_at"), WeekN0LockedAt: stamp(tWeekN0, "week_n0_locked_at"),
	}
	if n.RotationNo == 0 {
		n.RotationNo = 1
	}
	fit(bad, []textField{
		{"doctor_note", &n.DoctorNote, 80}, {"closed_reason", &n.ClosedReason, 60}, {"med_details", &n.MedDetails, 255},
		{"pattern", &n.Pattern, 30}, {"doctor_review", &n.DoctorReview, 20}, {"mcu_result", &n.McuResult, 20},
	})
	m := MappedTicket{Ticket: n}
	m.Hash = hashOf(n)
	return m, ps
}

func knownTicketStatus(s string) bool {
	switch s {
	case "PendingDoctor", "Approved", "Rejected", "Closed", "Cancelled":
		return true
	}
	return false
}

// medAnswer keeps Y and N and drops the sheet's placeholder.
func medAnswer(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "Y":
		return "Y"
	case "N":
		return "N"
	}
	return ""
}
