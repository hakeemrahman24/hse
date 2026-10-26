package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	noCompany     = "Not recorded" // label (and list filter value) for posts with no responsible company
	topRank       = 10             // rows in each "top" chart
	maxCompanyLen = 255
)

// rankRow is one row of a "top 10" chart on the dashboard: a company, or a person, with the status
// split of their findings. The bar is as wide as the row's share of the first row; inside it, the
// three colours show how that row's own findings split.
type rankRow struct {
	Pos                               int
	Name                              string
	Sub                               string // a person's company
	Total, Open, InProgress, Closed   int
	OpenPct, InProgressPct, ClosedPct int    // of this row's findings; they add up to 100
	Bar                               int    // width of the whole bar, relative to the busiest row
	Rate                              int    // closing rate: the closed share, in %
	RateClass                         string // good / fair / low, for the colour of the rate; "" without findings
	URL                               string
}

func newRankRow(name, sub string, open, inProgress, closed int, url string) rankRow {
	r := rankRow{Name: name, Sub: sub, Open: open, InProgress: inProgress, Closed: closed, Total: open + inProgress + closed, URL: url}
	if r.Total > 0 {
		r.OpenPct, r.InProgressPct = open*100/r.Total, inProgress*100/r.Total
		r.ClosedPct = 100 - r.OpenPct - r.InProgressPct
		r.Rate, r.RateClass = r.ClosedPct, rateClass(r.ClosedPct)
	}
	return r
}

// Closing-rate colours: green from rateGood, amber from rateFair, red below.
const (
	rateGood = 80
	rateFair = 50
)

func rateClass(pct int) string {
	switch {
	case pct >= rateGood:
		return "good"
	case pct >= rateFair:
		return "fair"
	}
	return "low"
}

// How the two rankings are ordered (?sort=). Anything else means the most findings.
const (
	sortFindings = ""
	sortRate     = "rate" // lowest closing rate first
	sortBest     = "best" // highest closing rate first
	sortOpen     = "open" // most findings still not closed first

	// minForRate keeps a row with one finding from topping the lowest or highest closing-rate list.
	minForRate = 5
)

func parseSort(s string) string {
	if s == sortRate || s == sortBest || s == sortOpen {
		return s
	}
	return sortFindings
}

// rankSQL returns the HAVING clause (with its leading space, or empty) and the ORDER BY terms for a
// sort. Only these fixed strings reach the query, never the request value.
func rankSQL(sort string) (having, order string) {
	switch sort {
	case sortRate:
		return fmt.Sprintf(" HAVING COUNT(*) >= %d", minForRate), "SUM(status='closed') / COUNT(*), COUNT(*) DESC"
	case sortBest:
		return fmt.Sprintf(" HAVING COUNT(*) >= %d", minForRate), "SUM(status='closed') / COUNT(*) DESC, COUNT(*) DESC"
	case sortOpen:
		return "", "SUM(status <> 'closed') DESC, COUNT(*) DESC"
	}
	return "", "COUNT(*) DESC"
}

type sortLink struct {
	Label, URL string
	Active     bool
}

// sortLinks are the three sort choices of the Companies & people tab, each keeping the date range.
func sortLinks(sort string, rng dateRange) []sortLink {
	var out []sortLink
	for _, o := range []struct{ key, label string }{{sortFindings, "Most findings"}, {sortRate, "Lowest closing rate"}, {sortBest, "Highest closing rate"}, {sortOpen, "Most still open"}} {
		q := rng.values()
		q.Set("tab", tabCompanies)
		if o.key != sortFindings {
			q.Set("sort", o.key)
		}
		out = append(out, sortLink{Label: o.label, URL: "/observations?" + q.Encode(), Active: o.key == sort})
	}
	return out
}

// rankBars numbers the rows and sets each bar relative to the busiest row (not always the first:
// the lowest-rate order puts a small company first).
func rankBars(rows []rankRow) {
	most := 0
	for _, r := range rows {
		most = max(most, r.Total)
	}
	for i := range rows {
		rows[i].Pos = i + 1
		if most > 0 {
			rows[i].Bar = rows[i].Total * 100 / most
		}
	}
}

// companyURL is the list of one company's findings, keeping the date range.
func companyURL(name string, rng dateRange) string {
	return listQuery{Company: name, Range: rng}.URL()
}

func rangeWhere(rng dateRange) (string, []any) {
	cond, args := rng.where("observed_at")
	if len(cond) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(cond, " AND "), args
}

func scanRank(rs *sql.Rows, make func(name, sub string, open, prog, closed int) rankRow) ([]rankRow, error) {
	var out []rankRow
	for rs.Next() {
		var name, sub string
		var open, prog, closed int
		if err := rs.Scan(&name, &sub, &open, &prog, &closed); err != nil {
			rs.Close()
			return nil, err
		}
		out = append(out, make(name, sub, open, prog, closed))
	}
	if err := closeRows(rs); err != nil {
		return nil, err
	}
	rankBars(out)
	return out, nil
}

// loadTopCompanies ranks the responsible companies by number of findings (all sources), busiest
// first, limited to the date range. The alias is not "company": GROUP BY would pick the reporter's
// company column over it and silently group by the wrong thing.
func loadTopCompanies(ctx context.Context, db *sql.DB, rng dateRange, sort string) ([]rankRow, error) {
	where, args := rangeWhere(rng)
	having, order := rankSQL(sort)
	rs, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT COALESCE(NULLIF(responsible_company,''),'%s') AS responsible, '',
		COALESCE(SUM(status='open'),0), COALESCE(SUM(status='in_progress'),0), COALESCE(SUM(status='closed'),0)
		FROM observations_observation`+where+` GROUP BY responsible`+having+` ORDER BY `+order+`, responsible LIMIT %d`, noCompany, topRank), args...)
	if err != nil {
		return nil, err
	}
	return scanRank(rs, func(name, _ string, open, prog, closed int) rankRow {
		return newRankRow(name, "", open, prog, closed, companyURL(name, rng))
	})
}

// loadTopPeople ranks the people who reported the most findings (all sources) in the date range.
// A SOC reporter is one person by employee ID (the Medpost key); a staff post has none, so the name
// stands in. The company shown is the reporter's own.
func loadTopPeople(ctx context.Context, db *sql.DB, rng dateRange, sort string) ([]rankRow, error) {
	where, args := rangeWhere(rng)
	having, order := rankSQL(sort)
	rs, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT MAX(observer_name) AS person, COALESCE(MAX(NULLIF(company,'')),''),
		COALESCE(SUM(status='open'),0), COALESCE(SUM(status='in_progress'),0), COALESCE(SUM(status='closed'),0)
		FROM observations_observation`+where+` GROUP BY COALESCE(NULLIF(employee_id,''), observer_name)`+having+`
		ORDER BY `+order+`, person LIMIT %d`, topRank), args...)
	if err != nil {
		return nil, err
	}
	return scanRank(rs, func(name, sub string, open, prog, closed int) rankRow {
		return newRankRow(name, sub, open, prog, closed, "")
	})
}
