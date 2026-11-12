package policy

import "testing"

func TestDefaultWorkWeeks(t *testing.T) {
	p := Default()
	cases := []struct {
		location string
		wni      bool
		want     int
	}{
		{"Onshore", true, 8},
		{"Offshore", true, 6},
		{"Onshore", false, 8},
		{"Offshore", false, 6},
		{"offshore", true, 6}, // case-insensitive
		{"", true, 8},         // unknown location falls back to onshore
	}
	for _, c := range cases {
		if got := p.WorkWeeks(c.location, c.wni); got != c.want {
			t.Errorf("WorkWeeks(%q,%v)=%d want %d", c.location, c.wni, got, c.want)
		}
	}
}

func TestRestDays(t *testing.T) {
	if got := Default().RestDays(true); got != 14 {
		t.Errorf("RestDays=%d", got)
	}
	if got := Default().RestDays(false); got != 14 {
		t.Errorf("RestDays foreign=%d", got)
	}
}

func TestMcuLepGraceDays(t *testing.T) {
	p := Default()
	if got := p.McuLepGraceDays(); got != 14 { // 2 weeks
		t.Errorf("weeks grace = %d", got)
	}
	p.McuLepGraceValue, p.McuLepGraceUnit = 5, "days"
	if got := p.McuLepGraceDays(); got != 5 {
		t.Errorf("days grace = %d", got)
	}
}

func TestFatigueWeek(t *testing.T) {
	p := Default() // before-cap = 2
	cases := []struct{ max, want int }{{8, 6}, {6, 4}, {2, 1}, {1, 1}}
	for _, c := range cases {
		if got := p.FatigueWeek(c.max); got != c.want {
			t.Errorf("FatigueWeek(%d)=%d want %d", c.max, got, c.want)
		}
	}
}

func TestPerpetualCert(t *testing.T) {
	p := Default()
	if !p.IsPerpetualCertPosition("  Paramedic ") {
		t.Error("Paramedic should be perpetual")
	}
	if p.IsPerpetualCertPosition("Welder") {
		t.Error("Welder is not perpetual")
	}
}
