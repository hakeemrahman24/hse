package importer

import (
	"strings"
	"testing"
)

// ticketRow builds a TripTicket row of 47 cells: an Approved Normal work ticket.
func ticketRow(change map[int]string) []string {
	row := make([]string, 47)
	base := map[int]string{
		0: "8/7/2026 11:11:51", 1: "TKT-AAAAA", 2: "WorkEntry", 3: "LEP-AAAAA", 8: "1",
		9: "10/19/2026 0:00:00", 10: "11/20/2026 0:00:00", 11: "10/10/2026 9:00:00", 12: "Approved", 16: "0",
		19: "11/1/2026 0:00:00", 21: "FALSE", 26: "FitWithNote", 28: "N", 32: "Approved", 33: "_", 36: "N", 37: "Normal",
		40: "Y", 41: "0", 44: "Onshore",
	}
	for i, v := range base {
		row[i] = v
	}
	for i, v := range change {
		row[i] = v
	}
	return row
}

func TestMapTicketMapsAnApprovedWorkTicket(t *testing.T) {
	m, ps := MapTicket(ticketRow(nil), 5)
	if len(ps) != 0 {
		t.Fatalf("problems on a clean row: %+v", ps)
	}
	n := m.Ticket
	checks := map[string][2]string{
		"id": {n.ID, "TKT-AAAAA"}, "lep": {n.LEP, "LEP-AAAAA"}, "status": {n.Status, "Approved"},
		"arrival": {n.Arrival, "2026-10-19"}, "departure": {n.Departure, "2026-11-20"}, "fatigue due": {n.FatigueDue, "2026-11-01"},
		"submitted": {n.SubmittedAt, "2026-10-10 09:00:00"}, "location": {n.Location, "Onshore"}, "pattern": {n.Pattern, "Normal"},
		"mcu": {n.McuResult, "Fit with note"}, "med answer": {n.MedAnswer, "N"}, "doctor review": {n.DoctorReview, "Approved"},
		"record start": {n.RecordStart, ""}, "closed at": {n.ClosedAt, ""},
	}
	for what, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", what, c[0], c[1])
		}
	}
	if !n.Work || n.RotationNo != 1 || !n.FatigueAgree || n.ShortBreak || n.RotationExtension || n.LepLocked || n.ReminderNotified {
		t.Errorf("flags %+v", n)
	}
}

func TestMapTicketKeepsWhatTheGateAndTheDailyJobsAlreadyDid(t *testing.T) {
	m, ps := MapTicket(ticketRow(map[int]string{
		12: "Closed", 13: "10-19-26", 14: "11-20-26", 15: "Planned departure passed", 16: "5", 21: "TRUE",
		22: "11-13-26", 23: "11-17-26", 43: "11-13-26", 36: "Y", 37: "ConsecutiveExtended", 39: "Y",
	}), 6)
	if len(ps) != 0 {
		t.Fatalf("problems %+v", ps)
	}
	n := m.Ticket
	if n.Status != "Closed" || n.RecordStart != "2026-10-19 00:00:00" || n.ClosedAt != "2026-11-20 00:00:00" || n.ClosedReason != "Planned departure passed" {
		t.Errorf("closing: %+v", n)
	}
	if !n.LepLocked || n.WeeksOnSite != 5 || !n.ReminderNotified || !n.FatigueFlag || n.WeekN1SentAt != "2026-11-13 00:00:00" || n.WeekN0LockedAt != "2026-11-17 00:00:00" {
		t.Errorf("daily-job state: %+v", n)
	}
	if !n.ShortBreak || !n.RotationExtension || n.Pattern != "ConsecutiveExtended" {
		t.Errorf("pattern flags: %+v", n)
	}
}

func TestMapTicketWaiverTicketIsNotAWorkTicketAndHasNoLocation(t *testing.T) {
	m, ps := MapTicket(ticketRow(map[int]string{1: "TKT-WVR01", 2: "Waiver", 3: "WVR-AAAAA", 44: "", 26: "_", 28: "_", 37: "", 36: "", 40: "", 19: "_", 41: ""}), 7)
	if len(ps) != 0 {
		t.Fatalf("problems %+v", ps)
	}
	if m.Ticket.Work || m.Ticket.Location != "" || m.Ticket.McuResult != "" || m.Ticket.FatigueDue != "" {
		t.Errorf("%+v", m.Ticket)
	}
}

func TestMapTicketRefusesWhatCannotBeImportedSafely(t *testing.T) {
	cases := []struct {
		name   string
		change map[int]string
		want   string
	}{
		{"no id", map[int]string{1: ""}, "ticket id"},
		{"id too long", map[int]string{1: "TKT-WAYTOOLONG1"}, "ticket id"},
		{"unknown status", map[int]string{12: "Maybe"}, "status"},
		{"unknown source type", map[int]string{2: "Alien"}, "source"},
		{"no permit number", map[int]string{3: ""}, "permit number"},
		{"unreadable arrival", map[int]string{9: "soon"}, "arrival"},
		{"unreadable departure", map[int]string{10: ""}, "departure"},
		{"unreadable submit time", map[int]string{11: "x"}, "submit time"},
	}
	for _, c := range cases {
		_, ps := MapTicket(ticketRow(c.change), 8)
		if got := fatal(ps); !strings.Contains(got, c.want) {
			t.Errorf("%s: fatal %q, want one about %q", c.name, got, c.want)
		}
	}
}

func TestMapTicketClipsTextToItsColumnAndSaysSo(t *testing.T) {
	long := strings.Repeat("x", 140)
	m, ps := MapTicket(ticketRow(map[int]string{12: "Rejected", 33: long, 15: long}), 9)
	if len(m.Ticket.DoctorNote) != 80 || len(m.Ticket.ClosedReason) != 60 {
		t.Errorf("lengths %d %d", len(m.Ticket.DoctorNote), len(m.Ticket.ClosedReason))
	}
	if !hasProblem(ps, "doctor_note") || !hasProblem(ps, "closed_reason") || fatal(ps) != "" {
		t.Errorf("problems %+v: clipped text must be reported, not refused", ps)
	}
}

func TestMapTicketHashChangesWithAnyField(t *testing.T) {
	a, _ := MapTicket(ticketRow(nil), 5)
	b, _ := MapTicket(ticketRow(nil), 99)
	c, _ := MapTicket(ticketRow(map[int]string{10: "11/27/2026 0:00:00"}), 5)
	if a.Hash == "" || a.Hash != b.Hash || a.Hash == c.Hash {
		t.Errorf("hashes %q %q %q", a.Hash, b.Hash, c.Hash)
	}
}
