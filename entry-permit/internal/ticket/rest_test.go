package ticket

import (
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

func TestRestGapsReadEachTicketAgainstTheOnesBeforeIt(t *testing.T) {
	p := policy.Default()
	mk := func(id, from, to string) Ticket {
		return Ticket{ID: id, LEP: "LEP-1", Work: true, Status: StatusClosed, Arrival: dates.Day(from), Departure: dates.Day(to), Location: "Onshore", Pattern: "Normal"}
	}
	all := []Ticket{
		mk("T1", "2026-01-01", "2026-01-20"),
		mk("T2", "2026-01-21", "2026-02-10"), // the next day: consecutive
		mk("T3", "2026-02-20", "2026-03-10"), // 10 days after: a short break (14 needed)
		mk("T4", "2026-05-01", "2026-05-20"), // weeks later: full rest
		{ID: "T5", LEP: "LEP-1", Work: true, Status: StatusRejected, Arrival: "2026-06-01", Departure: "2026-06-05"},
	}
	got := RestGaps(p, all, true)
	for id, kind := range map[string]string{"T1": "first", "T2": "consecutive", "T3": "short", "T4": "ok"} {
		if got[id].Kind != kind {
			t.Errorf("%s = %+v, want %s", id, got[id], kind)
		}
	}
	if got["T3"].Days != 10 || got["T3"].Required != 14 || got["T3"].Label() != "10 days: short break (needs 14)" {
		t.Errorf("T3 = %+v %q", got["T3"], got["T3"].Label())
	}
	if _, ok := got["T5"]; ok {
		t.Error("a rejected ticket must not have a rest")
	}
	if day, req := EarliestFullRest(p, all, true); day == "" || req <= 0 {
		t.Errorf("earliest full rest %q %d", day, req)
	}
	if day, _ := EarliestFullRest(p, nil, true); day != "" {
		t.Errorf("no tickets, no earliest day: %q", day)
	}
	if ov := RestGaps(p, []Ticket{mk("A", "2026-01-01", "2026-01-10"), mk("B", "2026-01-05", "2026-01-15")}, true); ov["B"].Kind != "overlap" {
		t.Errorf("overlap = %+v", ov["B"])
	}
}
