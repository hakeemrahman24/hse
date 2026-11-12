package dashboard

import "testing"

func TestComputeRing(t *testing.T) {
	rules := []RingRule{
		{"Ring1", "Kabupaten Teluk Bintuni", "kabupaten"},
		{"Ring2", "Distrik Babo", "distrik"},
		{"Ring1", "Onar", "desa"},
	}
	cases := []struct {
		name string
		p    Place
		want string
	}{
		{"foreigner", Place{Nationality: "China", Province: "Papua Barat"}, "Foreign"},
		{"village beats regency", Place{"Indonesia", "Papua Barat", "Teluk Bintuni", "Babo", "Onar"}, "Ring1"},
		{"district beats regency", Place{"Indonesia", "Papua Barat", "Teluk Bintuni", "Babo", "Other"}, "Ring2"},
		{"regency rule", Place{"Indonesia", "Papua Barat", "Kabupaten Teluk Bintuni", "Aranday", ""}, "Ring1"},
		{"papua default", Place{"Indonesia", "Papua Barat", "Sorong", "", ""}, "Ring4"},
		{"rest of Indonesia", Place{"Indonesia", "Jawa Timur", "Malang", "", ""}, "Indonesian"},
		{"no address", Place{Nationality: "Indonesia"}, ""},
	}
	for _, c := range cases {
		if got := ComputeRing(c.p, rules); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRingDistributionCountsWaiversAndMissingRings(t *testing.T) {
	rows := WithRings([]Row{{ID: "LEP-A", Kind: KindWork}, {ID: "WVR-1", Kind: KindVisitor}, {ID: "WVR-2", Kind: KindVisitor}},
		map[string]Place{"LEP-A": {Nationality: "Indonesia", Province: "Jawa"}}, nil)
	got := map[string]int{}
	for _, s := range RingDistribution(rows) {
		got[s.Label] = s.Value
	}
	if got["Indonesian"] != 1 || got["Waiver"] != 2 || len(got) != 2 {
		t.Errorf("slices %v", got)
	}
}

func TestWeeksSinceCountsFromTheTicketStartToToday(t *testing.T) {
	for _, c := range []struct{ arrival, today, want string }{
		{"2026-11-01", "2026-11-15", "2"}, {"2026-11-01", "2026-11-18", "2.4"}, {"2026-11-12", "2026-11-12", "0"},
		{"2026-11-20", "2026-11-12", ""}, {"", "2026-11-12", ""},
	} {
		if got := WeeksSince(c.arrival, c.today); got != c.want {
			t.Errorf("%s..%s: %q, want %q", c.arrival, c.today, got, c.want)
		}
	}
}
