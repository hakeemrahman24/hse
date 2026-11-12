package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
)

// Options is what the filter bar sends. Every field is optional; the zero value shows everything.
//
// WHY these filters (the old dashboard's): the name box, the permit kind (work or visitor), the
// company picker, and one "soon" pill whose meaning depends on the tab: a permit expiring within a
// month (Registered), a ticket ending within a week (Active), a ticket starting within a week
// (Scheduled). "Checked in" keeps only people the gate shows inside (Active).
type Options struct {
	Q         string
	Kind      Kind     // "" for both work and visitor
	Companies []string // exact company names; empty means all
	Soon      bool
	CheckedIn bool
	Level2    bool // chart: roll companies deeper than level 2 up to their level 2 parent
}

// Soon windows, in days (Registered counts a month).
const (
	soonRegisteredDays = 31
	soonTicketDays     = 7
)

// Apply keeps the rows that pass the options for the given tab. today is yyyy-mm-dd.
func Apply(rows []Row, o Options, tab, today string) []Row {
	rows = Filter(rows, o.Q)
	want := map[string]bool{}
	for _, c := range o.Companies {
		want[c] = true
	}
	var out []Row
	for _, r := range rows {
		switch {
		case o.Kind != "" && r.Kind != o.Kind:
		case len(want) > 0 && !want[r.Company]:
		case o.Soon && !soonMatches(r, tab, today):
		case o.CheckedIn && r.Status != "Inside":
		default:
			out = append(out, r)
		}
	}
	return out
}

func soonMatches(r Row, tab, today string) bool {
	switch tab {
	case "registered":
		return r.ValidUntil != "" && r.ValidUntil >= today && r.ValidUntil <= string(dates.Day(today).AddDays(soonRegisteredDays))
	case "active":
		return r.Departure != "" && r.Departure >= today && r.Departure <= string(dates.Day(today).AddDays(soonTicketDays))
	case "scheduled":
		return r.Arrival != "" && r.Arrival >= today && r.Arrival <= string(dates.Day(today).AddDays(soonTicketDays))
	}
	return true
}

// Companies lists the distinct company names in rows, sorted, for the company picker.
func Companies(rows []Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if c := strings.TrimSpace(r.Company); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lower(out[i]) < lower(out[j]) })
	return out
}

// ── charts ───────────────────────────────────────────────────────────────────

// Slice is one wedge of a pie: a label, its count and the share of the whole.
type Slice struct {
	Label   string
	Value   int
	Percent string // "37%", for the legend
	Color   string
	Path    string // SVG path of the wedge, empty for a one-slice pie (drawn as a circle)
}

// palette is colour-blind friendly and keeps the legend readable on white. It repeats after 12.
var palette = []string{"#0057d9", "#e8710a", "#188038", "#a142f4", "#d93025", "#12b5cb", "#9aa0a6", "#c26401", "#1a73e8", "#7cb342", "#e52592", "#5f6368"}

const noCompany = "(No company)"

// Distribution counts rows per company, sorted by company name. With level2 a company deeper
// than level 2 is counted on its level 2 parent ("Until level 2"); levels 0 to 2 stay as they are.
func Distribution(rows []Row, level2 bool) []Slice {
	counts := map[string]int{}
	for _, r := range rows {
		label := strings.TrimSpace(r.Company)
		if level2 {
			if up := level2Parent(r.Parents); up != "" {
				label = up
			}
		}
		if label == "" {
			label = noCompany
		}
		counts[label]++
	}
	labels := make([]string, 0, len(counts))
	for l := range counts {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool { return lower(labels[i]) < lower(labels[j]) })
	out := make([]Slice, len(labels))
	for i, l := range labels {
		out[i] = Slice{Label: l, Value: counts[l]}
	}
	return Pie(out)
}

// level2Parent finds the ancestor at depth 2 in a "Name (depth); Name (depth)" chain, or "".
func level2Parent(parents string) string {
	for _, p := range strings.Split(parents, ";") {
		p = strings.TrimSpace(p)
		open := strings.LastIndex(p, "(")
		if open < 0 || !strings.HasSuffix(p, ")") {
			continue
		}
		if depth, err := strconv.Atoi(strings.TrimSpace(p[open+1 : len(p)-1])); err == nil && depth == 2 {
			return strings.TrimSpace(p[:open])
		}
	}
	return ""
}

const (
	pieSize   = 120.0
	pieRadius = 56.0
)

// Pie gives every slice its colour, percentage and SVG path, drawn clockwise from the top.
func Pie(in []Slice) []Slice {
	total := 0
	for _, s := range in {
		total += s.Value
	}
	out := make([]Slice, len(in))
	copy(out, in)
	if total == 0 {
		return out
	}
	c := pieSize / 2
	angle := -math.Pi / 2
	for i := range out {
		out[i].Color = palette[i%len(palette)]
		out[i].Percent = fmt.Sprintf("%d%%", int(math.Round(float64(out[i].Value)*100/float64(total))))
		if len(out) == 1 {
			continue // a full circle is drawn by the page, an arc cannot close on itself
		}
		next := angle + 2*math.Pi*float64(out[i].Value)/float64(total)
		large := 0
		if next-angle > math.Pi {
			large = 1
		}
		out[i].Path = fmt.Sprintf("M%.2f %.2f L%.2f %.2f A%.2f %.2f 0 %d 1 %.2f %.2f Z", c, c,
			c+pieRadius*math.Cos(angle), c+pieRadius*math.Sin(angle), pieRadius, pieRadius, large,
			c+pieRadius*math.Cos(next), c+pieRadius*math.Sin(next))
		angle = next
	}
	return out
}

// ── Pending summary ──────────────────────────────────────────────────────────

// StageCount is how many requests wait at one stage.
type StageCount struct {
	Label string
	Count int
}

// StageCounts counts pending rows per stage, in review order, for the strip above the list.
func StageCounts(rows []Row) []StageCount {
	n := map[string]int{}
	for _, r := range rows {
		n[r.Stage]++
	}
	return []StageCount{{"Stage 1", n["stage1"]}, {"Doctor", n["doctor"]}, {"Final", n["final"]}}
}

// ApprovalStats is the old "WEP approval status" pie: work permits issued or rejected, split
// into Indonesian and non-WNI. A permit counts as approved once it has an LEP number (a later
// expiry or resignation does not undo that).
func (s Store) ApprovalStats(ctx context.Context) ([]Slice, error) {
	type key struct {
		approved, wni bool
	}
	counts := map[key]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT CASE WHEN lep_no <> '' THEN 1 ELSE 0 END, CASE WHEN nationality = 'Indonesia' THEN 1 ELSE 0 END, COUNT(*)
		FROM permit WHERE lep_no <> '' OR status = ? GROUP BY 1, 2`, permit.StatusRejected)
	if err != nil {
		return nil, fmt.Errorf("approval stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, w, n int
		if err := rows.Scan(&a, &w, &n); err != nil {
			return nil, fmt.Errorf("approval stats row: %w", err)
		}
		counts[key{a == 1, w == 1}] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Slice
	for _, c := range []struct {
		label         string
		approved, wni bool
	}{{"Approved · Indonesian", true, true}, {"Approved · Non-WNI", true, false}, {"Rejected · Indonesian", false, true}, {"Rejected · Non-WNI", false, false}} {
		if n := counts[key{c.approved, c.wni}]; n > 0 {
			out = append(out, Slice{Label: c.label, Value: n})
		}
	}
	return Pie(out), nil
}

// GateActivityOn lists the gate scans of one day (yyyy-mm-dd), newest first, at most limit.
func (s Store) GateActivityOn(ctx context.Context, day string, limit int) ([]Row, error) {
	return s.rows(ctx, `SELECT lep_no, worker_name, company, action, reason, officer, gate, at
		FROM gate_log WHERE at >= ? AND at < ? ORDER BY at DESC, worker_id DESC LIMIT ?`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Action, &w.Reason, &w.Officer, &w.Gate, &w.At)
		}, day+" 00:00:00", string(dates.Day(day).AddDays(1))+" 00:00:00", limit)
}
