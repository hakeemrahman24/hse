package main

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func rangeOf(query string) dateRange {
	v, _ := url.ParseQuery(query)
	return parseRange(v)
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		name, query string
		want        dateRange
	}{
		{"both", "from=2026-10-01&to=2026-10-15", dateRange{"2026-10-01", "2026-10-15"}},
		{"from only", "from=2026-10-01", dateRange{From: "2026-10-01"}},
		{"to only", "to=2026-10-15", dateRange{To: "2026-10-15"}},
		{"none", "", dateRange{}},
		{"garbage ignored", "from=yesterday&to=2026-13-40", dateRange{}},
		{"swapped when from is after to", "from=2026-10-15&to=2026-10-01", dateRange{"2026-10-01", "2026-10-15"}},
		{"same day", "from=2026-10-05&to=2026-10-05", dateRange{"2026-10-05", "2026-10-05"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rangeOf(c.query); got != c.want {
				t.Errorf("parseRange(%q) = %+v, want %+v", c.query, got, c.want)
			}
		})
	}
}

func TestRangeWhereIncludesTheWholeLastDay(t *testing.T) {
	cond, args := dateRange{"2026-10-01", "2026-10-15"}.where("observed_at")
	if !reflect.DeepEqual(cond, []string{"observed_at >= ?", "observed_at < ?"}) {
		t.Errorf("conditions = %v", cond)
	}
	// "To" is a whole day: everything before the start of the next day.
	if !reflect.DeepEqual(args, []any{"2026-10-01 00:00:00", "2026-10-16 00:00:00"}) {
		t.Errorf("args = %v", args)
	}
	if cond, _ := (dateRange{}).where("observed_at"); len(cond) != 0 {
		t.Errorf("an empty range must add no condition, got %v", cond)
	}
	if _, args := (dateRange{To: "2026-12-31"}).where("o.observed_at"); !reflect.DeepEqual(args, []any{"2027-01-01 00:00:00"}) {
		t.Errorf("year end rolls over: %v", args)
	}
}

func TestRangeLabelAndActive(t *testing.T) {
	if (dateRange{}).Active() || (dateRange{}).Label() != "" {
		t.Error("empty range is inactive")
	}
	cases := map[dateRange]string{
		{"2026-10-01", "2026-10-15"}: "2026-10-01 to 2026-10-15",
		{From: "2026-10-01"}:         "from 2026-10-01",
		{To: "2026-10-15"}:           "up to 2026-10-15",
	}
	for r, want := range cases {
		if !r.Active() || r.Label() != want {
			t.Errorf("%+v: active=%v label=%q, want %q", r, r.Active(), r.Label(), want)
		}
	}
}

func TestListQueryURLKeepsEveryFilter(t *testing.T) {
	if got := (listQuery{}).URL(); got != "/observations/list" {
		t.Errorf("empty query = %q", got)
	}
	got := listQuery{Class: "Safety", Hazard: 9, Status: statusOpen, Source: sourcePMR, Type: "Site Audit", Verify: true, Range: dateRange{"2026-10-01", "2026-10-15"}}.URL()
	want := "/observations/list?class=Safety&from=2026-10-01&hazard=9&source=Project+Monitoring+Action+Report&status=open&to=2026-10-15&type=Site+Audit&verify=1"
	if got != want {
		t.Errorf("URL =\n %q\nwant\n %q", got, want)
	}
	// Round trip: the list page reads back what the link wrote.
	u, _ := url.Parse(got)
	if r := parseRange(u.Query()); r != (dateRange{"2026-10-01", "2026-10-15"}) {
		t.Errorf("range did not survive the link: %+v", r)
	}
}

func TestWithPercentScalesToTheLargestRow(t *testing.T) {
	rows := withPercent([]countRow{{"a", 8, 0}, {"b", 2, 0}, {"c", 0, 0}})
	if rows[0].Percent != 100 || rows[1].Percent != 25 || rows[2].Percent != 0 {
		t.Errorf("percents = %+v", rows)
	}
	if got := withPercent([]countRow{{"a", 0, 0}}); got[0].Percent != 0 {
		t.Errorf("all-zero rows must not divide by zero: %+v", got)
	}
}

func TestRankRowSplitsAddUpAndBarsAreRelativeToTheFirstRow(t *testing.T) {
	r := newRankRow("ACME", "", 2, 1, 5, "/x")
	if r.Total != 8 || r.ClosedPct != 63 || r.InProgressPct != 12 || r.OpenPct != 25 || r.ClosedPct+r.InProgressPct+r.OpenPct != 100 {
		t.Errorf("percents = %+v", r)
	}
	if none := newRankRow("Y", "", 0, 0, 0, ""); none.Total != 0 || none.ClosedPct != 0 || none.OpenPct != 0 {
		t.Errorf("no findings must not divide by zero: %+v", none)
	}
	rows := []rankRow{newRankRow("A", "", 4, 0, 4, ""), newRankRow("B", "", 0, 0, 4, ""), newRankRow("C", "", 1, 0, 0, "")}
	rankBars(rows)
	if rows[0].Bar != 100 || rows[1].Bar != 50 || rows[2].Bar != 12 || rows[0].Pos != 1 || rows[2].Pos != 3 {
		t.Errorf("bars and positions = %+v", rows)
	}
	rankBars(nil) // an empty list is fine
}

func TestCompanyLinkKeepsRangeAndEncodesName(t *testing.T) {
	url := companyURL("PT A & B", dateRange{From: "2026-10-01"})
	if url != "/observations/list?company=PT+A+%26+B&from=2026-10-01" {
		t.Errorf("URL = %q", url)
	}
}

func TestRateClassColoursByClosingRate(t *testing.T) {
	for _, c := range []struct {
		closed, total int
		want          string
	}{{9, 10, "good"}, {8, 10, "good"}, {5, 10, "fair"}, {79, 100, "fair"}, {4, 10, "low"}, {0, 3, "low"}, {0, 0, ""}} {
		r := newRankRow("x", "", 0, c.total-c.closed, c.closed, "")
		if r.RateClass != c.want {
			t.Errorf("%d/%d: class %q, want %q", c.closed, c.total, r.RateClass, c.want)
		}
	}
	if r := newRankRow("x", "", 2, 1, 5, ""); r.Rate != 63 {
		t.Errorf("rate = %d, want the closed percentage 63", r.Rate)
	}
}

func TestParseSortFallsBackToMostFindings(t *testing.T) {
	for in, want := range map[string]string{"": sortFindings, "rate": sortRate, "best": sortBest, "open": sortOpen, "DROP TABLE": sortFindings, "Rate": sortFindings} {
		if got := parseSort(in); got != want {
			t.Errorf("parseSort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankSQLOrdersAndOnlyRatesCompaniesWithEnoughFindings(t *testing.T) {
	having, order := rankSQL(sortRate)
	if !strings.Contains(having, fmt.Sprintf("COUNT(*) >= %d", minForRate)) || !strings.Contains(order, "SUM(status='closed') / COUNT(*)") {
		t.Errorf("rate sort: having %q order %q", having, order)
	}
	if having, order = rankSQL(sortBest); !strings.Contains(having, fmt.Sprintf("COUNT(*) >= %d", minForRate)) || !strings.Contains(order, "SUM(status='closed') / COUNT(*) DESC") {
		t.Errorf("best sort: having %q order %q", having, order)
	}
	if having, order = rankSQL(sortOpen); having != "" || !strings.Contains(order, "status <> 'closed'") {
		t.Errorf("open sort: having %q order %q", having, order)
	}
	if having, order = rankSQL(sortFindings); having != "" || !strings.HasPrefix(order, "COUNT(*) DESC") {
		t.Errorf("default sort: having %q order %q", having, order)
	}
}

func TestSortLinksKeepTabAndRange(t *testing.T) {
	links := sortLinks(sortRate, dateRange{From: "2026-10-01"})
	if len(links) != 4 || links[0].Active || !links[1].Active || links[2].Active || links[3].Active || links[2].Label != "Highest closing rate" {
		t.Fatalf("links = %+v", links)
	}
	if links[0].URL != "/observations?from=2026-10-01&tab=companies" || links[1].URL != "/observations?from=2026-10-01&sort=rate&tab=companies" {
		t.Errorf("urls = %q, %q", links[0].URL, links[1].URL)
	}
}
