package dashboard

import (
	"strings"
	"testing"
)

func TestApplyKeepsOnlyRowsThatPassEveryFilter(t *testing.T) {
	rows := []Row{
		{Kind: KindWork, ID: "LEP-1", Name: "Ani", Company: "PT A", ValidUntil: "2026-11-20", Departure: "2026-11-09", Arrival: "2026-11-08", Status: "Inside"},
		{Kind: KindWork, ID: "LEP-2", Name: "Budi", Company: "PT B", ValidUntil: "2027-06-01", Departure: "2026-12-30", Arrival: "2026-12-01", Status: "In ticket window"},
		{Kind: KindVisitor, ID: "WVR-3", Name: "Cici", Company: "PT A", ValidUntil: "2026-11-30", Departure: "2026-11-30", Arrival: "2026-11-30"},
	}
	ids := func(rs []Row) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.ID)
		}
		return strings.Join(s, ",")
	}
	const today = "2026-11-06"
	for name, c := range map[string]struct {
		o    Options
		tab  string
		want string
	}{
		"nothing":        {Options{}, "registered", "LEP-1,LEP-2,WVR-3"},
		"kind":           {Options{Kind: KindVisitor}, "registered", "WVR-3"},
		"company":        {Options{Companies: []string{"PT A"}}, "registered", "LEP-1,WVR-3"},
		"two companies":  {Options{Companies: []string{"PT A", "PT B"}}, "registered", "LEP-1,LEP-2,WVR-3"},
		"expiring month": {Options{Soon: true}, "registered", "LEP-1,WVR-3"},
		"ticket ending":  {Options{Soon: true}, "active", "LEP-1"},
		"starting":       {Options{Soon: true}, "scheduled", "LEP-1"},
		"checked in":     {Options{CheckedIn: true}, "active", "LEP-1"},
		"search":         {Options{Q: "budi"}, "registered", "LEP-2"},
		"all together":   {Options{Kind: KindWork, Companies: []string{"PT A"}, Soon: true}, "registered", "LEP-1"},
	} {
		if got := ids(Apply(rows, c.o, c.tab, today)); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestDistributionCountsPerCompanyAndRollsDeepOnesUpToLevel2(t *testing.T) {
	rows := []Row{
		{Company: "PT Sub Deep", Parents: "PT Root (0); PT Mid (1); PT Sub (2)"},
		{Company: "PT Sub Deep", Parents: "PT Root (0); PT Mid (1); PT Sub (2)"},
		{Company: "PT Sub", Parents: "PT Root (0); PT Mid (1)"},
		{Company: ""},
	}
	all := Distribution(rows, false)
	if len(all) != 3 || all[0].Label != "(No company)" || all[1].Label != "PT Sub" || all[2].Label != "PT Sub Deep" || all[2].Value != 2 {
		t.Errorf("all = %+v", all)
	}
	// PT Sub Deep has a level 2 ancestor, so it counts on PT Sub; PT Sub has none and stays.
	l2 := Distribution(rows, true)
	if len(l2) != 2 || l2[1].Label != "PT Sub" || l2[1].Value != 3 {
		t.Errorf("level 2 = %+v", l2)
	}
}

func TestPieGivesColoursPercentagesAndPaths(t *testing.T) {
	three := Pie([]Slice{{Label: "a", Value: 2}, {Label: "b", Value: 1}, {Label: "c", Value: 1}})
	if three[0].Percent != "50%" || three[1].Percent != "25%" || three[0].Color == three[1].Color || !strings.HasPrefix(three[0].Path, "M60.00 60.00") {
		t.Errorf("pie = %+v", three)
	}
	// One slice is a whole circle, drawn by the page: no path.
	if one := Pie([]Slice{{Label: "a", Value: 5}}); one[0].Path != "" || one[0].Percent != "100%" {
		t.Errorf("one = %+v", one)
	}
	if none := Pie(nil); len(none) != 0 {
		t.Errorf("none = %+v", none)
	}
}
