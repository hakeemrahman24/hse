package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---- date range filter (on the date of the finding) ----

const dateLayout = "2006-01-02"

// dateRange is the From/To filter of the dashboard and the list. Either end can be empty. Both
// ends are inclusive days.
type dateRange struct{ From, To string }

func validDate(s string) string {
	if _, err := time.Parse(dateLayout, s); err != nil {
		return ""
	}
	return s
}

// parseRange reads ?from= and ?to=. A value that is not a date is ignored, and a From later than
// To is swapped, so the page never shows an empty result because of a typing slip.
func parseRange(q url.Values) dateRange {
	r := dateRange{From: validDate(q.Get("from")), To: validDate(q.Get("to"))}
	if r.From != "" && r.To != "" && r.From > r.To {
		r.From, r.To = r.To, r.From
	}
	return r
}

func (r dateRange) Active() bool { return r.From != "" || r.To != "" }

// Label is the range in words, for the page heading.
func (r dateRange) Label() string {
	switch {
	case r.From != "" && r.To != "":
		return r.From + " to " + r.To
	case r.From != "":
		return "from " + r.From
	case r.To != "":
		return "up to " + r.To
	}
	return ""
}

// where returns the SQL conditions on col and their arguments. The To day is included by comparing
// with the start of the next day.
func (r dateRange) where(col string) ([]string, []any) {
	var cond []string
	var args []any
	if r.From != "" {
		cond, args = append(cond, col+" >= ?"), append(args, r.From+" 00:00:00")
	}
	if r.To != "" {
		to, _ := time.Parse(dateLayout, r.To)
		cond, args = append(cond, col+" < ?"), append(args, to.AddDate(0, 0, 1).Format(dateLayout)+" 00:00:00")
	}
	return cond, args
}

func (r dateRange) values() url.Values {
	v := url.Values{}
	if r.From != "" {
		v.Set("from", r.From)
	}
	if r.To != "" {
		v.Set("to", r.To)
	}
	return v
}

// ---- sidebar: Dashboard, Observations, then the sources ----

// sourceNone is the ?source= value of posts from before the source existed (the column is empty).
const sourceNone = "none"

// sidebarType is one type under a source (Site Audit under Internal Audit, ...).
type sidebarType struct {
	Name   string
	Count  int
	Active bool
}

// sidebarSource is one source of the observations (SOC, Internal Audit, ...). Value is what goes in
// ?source=; Name is what the sidebar shows. A source with types opens to them like a folder.
type sidebarSource struct {
	Name, Value  string
	Count        int
	Open, Active bool // Open: the group is expanded because it (or one of its types) is selected
	Items        []sidebarType
}

type obsNav struct {
	Total    int
	Sources  []sidebarSource
	Filtered bool // a date range is applied, so the counts are for that range
	// Q1 is "?from=..&to=.." to put after a path, Qs is "&from=..&to=.." to put after other
	// parameters (both empty without a range). They are built by url.Values.Encode, so they are
	// safe as URL parts; template.URL stops the template from encoding the & and = again.
	Q1, Qs template.URL
}

// buildNav counts the observations per source and type for the sidebar (within the date range).
// activeSource is the ?source= value of the page and activeType its ?type= ("" for none): the
// sidebar marks them and opens the source that holds the active type.
func buildNav(ctx context.Context, db *sql.DB, snap *refSnapshot, activeSource, activeType string, rng dateRange) (*obsNav, error) {
	cond, args := rng.where("observed_at")
	q := `SELECT COALESCE(source, ''), COALESCE(source_detail, ''), COUNT(*) FROM observations_observation`
	if len(cond) > 0 {
		q += " WHERE " + strings.Join(cond, " AND ")
	}
	rows, err := db.QueryContext(ctx, q+" GROUP BY source, source_detail", args...)
	if err != nil {
		return nil, err
	}
	nav := &obsNav{Filtered: rng.Active()}
	if enc := rng.values().Encode(); enc != "" {
		nav.Q1, nav.Qs = template.URL("?"+enc), template.URL("&"+enc)
	}
	bySource := map[string]int{}
	byType := map[[2]string]int{}
	for rows.Next() {
		var src, typ string
		var n int
		if err := rows.Scan(&src, &typ, &n); err != nil {
			rows.Close()
			return nil, err
		}
		bySource[src] += n
		byType[[2]string{src, typ}] += n
		nav.Total += n
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	nav.Sources = navSources(snap, bySource, byType, activeSource, activeType)
	return nav, nil
}

// navSources lists SOC first, then the staff sources in list order (an archived source or type only
// while posts still carry it), then "Not recorded" for posts without a source.
func navSources(snap *refSnapshot, bySource map[string]int, byType map[[2]string]int, activeSource, activeType string) []sidebarSource {
	var out []sidebarSource
	add := func(name, value string, details []refDetail, archived bool) {
		n := bySource[value]
		if archived && n == 0 {
			return
		}
		s := sidebarSource{Name: name, Value: value, Count: n, Active: value == activeSource && activeType == ""}
		for _, d := range details {
			c := byType[[2]string{value, d.Name}]
			if d.Archived && c == 0 {
				continue
			}
			item := sidebarType{Name: d.Name, Count: c, Active: value == activeSource && d.Name == activeType}
			s.Open = s.Open || item.Active
			s.Items = append(s.Items, item)
		}
		s.Open = s.Open || s.Active
		out = append(out, s)
	}
	add(sourceSOC, sourceSOC, nil, false)
	for _, src := range snap.Sources {
		add(src.Name, src.Name, src.Details, src.Archived)
	}
	if n := bySource[""]; n > 0 {
		out = append(out, sidebarSource{Name: "Not recorded", Value: sourceNone, Count: n, Active: activeSource == sourceNone})
	}
	return out
}

// navSourceOf is the ?source= value that stands for a post's source in the sidebar.
func navSourceOf(source string) string {
	if source == "" {
		return sourceNone
	}
	return source
}

// listFilter is the hazard filter row of the list page. The sidebar no longer groups by hazard, so
// the hazard class, category and control topic are chosen here. Keep holds every other filter, sent
// again as hidden fields so choosing a hazard does not drop the source, status or dates.
type listFilter struct {
	Keep         map[string]string
	Classes      []string
	Class        string
	HazardGroups []hazardGroup
	Hazard       int64
	Controls     []refTopic
	Topic        int64
}

const classUnclassified = "unclassified"

func newListFilter(snap *refSnapshot, lq listQuery) *listFilter {
	f := &listFilter{Keep: map[string]string{}, Class: lq.Class, Hazard: lq.Hazard, Topic: lq.Topic}
	keep := lq
	keep.Class, keep.Hazard, keep.Topic, keep.ControlClass = "", 0, 0, ""
	for k, v := range keep.values() {
		f.Keep[k] = v[0]
	}
	for _, g := range snap.HazardGroups() {
		f.Classes = append(f.Classes, g.Class)
	}
	f.HazardGroups = snap.HazardGroups()
	for _, t := range snap.Topics {
		if t.Control {
			f.Controls = append(f.Controls, t)
		}
	}
	return f
}

// ---- list links ----

// listQuery is everything the list can be filtered by; URL() writes it as a link.
type listQuery struct {
	Class          string
	Hazard, Topic  int64  // Topic: a control topic picked in the sidebar
	ControlClass   string // a class of the hierarchy of controls picked in the sidebar
	Status, Source string
	Type           string // the source type (Site Audit, ...), only together with Source
	Company        string // responsible company; noCompany matches posts without one
	Verify         bool
	Range          dateRange
}

func (q listQuery) values() url.Values {
	v := q.Range.values()
	if q.Class != "" {
		v.Set("class", q.Class)
	}
	if q.Hazard != 0 {
		v.Set("hazard", strconv.FormatInt(q.Hazard, 10))
	}
	if q.Topic != 0 {
		v.Set("topic", strconv.FormatInt(q.Topic, 10))
	}
	if q.ControlClass != "" {
		v.Set("cclass", q.ControlClass)
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.Source != "" {
		v.Set("source", q.Source)
	}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.Company != "" {
		v.Set("company", q.Company)
	}
	if q.Verify {
		v.Set("verify", "1")
	}
	return v
}

func (q listQuery) URL() string {
	if enc := q.values().Encode(); enc != "" {
		return "/observations/list?" + enc
	}
	return "/observations/list"
}

// ---- dashboard: three graphic blocks (all findings, SOC, PMR) ----

type countRow struct {
	Label   string
	Count   int
	Percent int // bar width, relative to the largest row
}

func withPercent(rows []countRow) []countRow {
	top := 0
	for _, r := range rows {
		top = max(top, r.Count)
	}
	for i := range rows {
		if top > 0 {
			rows[i].Percent = rows[i].Count * 100 / top
		}
	}
	return rows
}

// dashBlock is one block of the dashboard: the status counts, and (for SOC and PMR) the breakdowns.
type dashBlock struct {
	Title                             string
	Total, Open, InProgress, Closed   int
	OpenPct, InProgressPct, ClosedPct int // stacked bar, percent of the total
	Hazards, Areas, Subcontractors    []countRow
	Breakdown                         bool // show hazard categories, area, subcontractor
	Positive                          int
	ShowPositive                      bool
	// Links to the matching list, so each number can be clicked.
	TotalURL, OpenURL, InProgressURL, ClosedURL string
}

const tabCompanies = "companies" // ?tab= of the "Companies & people" view; anything else is the overview

type dashboard struct {
	Tab                       string // "" (overview) or tabCompanies
	Sort                      string // order of the two rankings: sortFindings, sortRate or sortOpen
	SortLinks                 []sortLink
	OverviewURL, CompaniesURL string // the two tabs, both keeping the date range
	Blocks                    []dashBlock
	TopCompanies, TopPeople   []rankRow
	AwaitingVerification      int
	AwaitingURL               string
}

const topRows = 10 // rows shown in each breakdown

func rowsOf(ctx context.Context, db *sql.DB, query string, args []any) ([]countRow, error) {
	rs, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []countRow
	for rs.Next() {
		var r countRow
		if err := rs.Scan(&r.Label, &r.Count); err != nil {
			rs.Close()
			return nil, err
		}
		out = append(out, r)
	}
	if err := closeRows(rs); err != nil {
		return nil, err
	}
	return withPercent(out), nil
}

// loadBlock counts one block. source is "" for all findings, or the source value to match.
func loadBlock(ctx context.Context, db *sql.DB, title, source string, rng dateRange, breakdown, positive bool) (dashBlock, error) {
	b := dashBlock{Title: title, Breakdown: breakdown, ShowPositive: positive}
	var cond []string
	var args []any
	if source != "" {
		cond, args = append(cond, "source = ?"), append(args, source)
	}
	c, a := rng.where("observed_at")
	cond, args = append(cond, c...), append(args, a...)
	where := ""
	if len(cond) > 0 {
		where = " WHERE " + strings.Join(cond, " AND ")
	}
	and := func(extra string) string {
		if where == "" {
			return " WHERE " + extra
		}
		return where + " AND " + extra
	}

	err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status='open'),0), COALESCE(SUM(status='in_progress'),0), COALESCE(SUM(status='closed'),0)
		FROM observations_observation`+where, args...).Scan(&b.Total, &b.Open, &b.InProgress, &b.Closed)
	if err != nil {
		return b, err
	}
	if b.Total > 0 {
		b.OpenPct, b.InProgressPct = b.Open*100/b.Total, b.InProgress*100/b.Total
		b.ClosedPct = 100 - b.OpenPct - b.InProgressPct
	}
	if positive {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations_observation`+and("category = 'positive_finding'"), args...).Scan(&b.Positive); err != nil {
			return b, err
		}
	}
	if !breakdown {
		return b, nil
	}
	limit := fmt.Sprintf(" LIMIT %d", topRows)
	if b.Hazards, err = rowsOf(ctx, db, `SELECT h.name, COUNT(*) FROM observations_observation o JOIN ref_hazard_category h ON h.id = o.hazard_id`+
		strings.ReplaceAll(where, "observed_at", "o.observed_at")+" GROUP BY h.id, h.name ORDER BY 2 DESC, 1"+limit, args); err != nil {
		return b, err
	}
	if b.Areas, err = rowsOf(ctx, db, `SELECT location, COUNT(*) FROM observations_observation`+where+` GROUP BY location ORDER BY 2 DESC, 1`+limit, args); err != nil {
		return b, err
	}
	b.Subcontractors, err = rowsOf(ctx, db, `SELECT COALESCE(NULLIF(responsible_company,''),'Not recorded'), COUNT(*) FROM observations_observation`+where+
		` GROUP BY 1 ORDER BY 2 DESC, 1`+limit, args)
	return b, err
}

func (b *dashBlock) linkTo(source string, rng dateRange) {
	q := listQuery{Source: source, Range: rng}
	b.TotalURL = q.URL()
	q.Status = statusOpen
	b.OpenURL = q.URL()
	q.Status = statusInProgress
	b.InProgressURL = q.URL()
	q.Status = statusClosed
	b.ClosedURL = q.URL()
}

const sourcePMR = "Project Monitoring Action Report"

func loadDashboard(ctx context.Context, db *sql.DB, rng dateRange, tab, sort string) (*dashboard, error) {
	var err error
	d := &dashboard{Tab: tab, Sort: sort}
	q := rng.values()
	d.OverviewURL = "/observations"
	if enc := q.Encode(); enc != "" {
		d.OverviewURL += "?" + enc
	}
	q.Set("tab", tabCompanies)
	d.CompaniesURL = "/observations?" + q.Encode()

	if tab == tabCompanies {
		d.SortLinks = sortLinks(sort, rng)
		// The two rankings only; the overview blocks are not needed for this view.
		if d.TopCompanies, err = loadTopCompanies(ctx, db, rng, sort); err != nil {
			return nil, err
		}
		if d.TopPeople, err = loadTopPeople(ctx, db, rng, sort); err != nil {
			return nil, err
		}
		return d, nil
	}
	specs := []struct {
		title, source       string
		breakdown, positive bool
	}{
		{"HSE recommendations status", "", false, false},
		{"SOC status", sourceSOC, true, true},
		{"PMR status", sourcePMR, true, false},
	}
	for _, s := range specs {
		b, err := loadBlock(ctx, db, s.title, s.source, rng, s.breakdown, s.positive)
		if err != nil {
			return nil, err
		}
		b.linkTo(s.source, rng)
		d.Blocks = append(d.Blocks, b)
	}
	cond, args := rng.where("observed_at")
	cond = append([]string{"source = 'SOC'", "COALESCE(verification,'Not yet') <> 'Verified'"}, cond...)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations_observation WHERE `+strings.Join(cond, " AND "), args...).Scan(&d.AwaitingVerification); err != nil {
		return nil, err
	}
	d.AwaitingURL = listQuery{Verify: true, Range: rng}.URL()
	return d, nil
}

// ---- detail ----

// fileBlock is one evidence section as the page shows it.
type fileBlock struct {
	Section   fileSection
	ObsID     int64
	CanUpload bool
	CanDelete bool
}

func (b fileBlock) CanAddPhoto() bool { return b.CanUpload && b.Section.CanAddPhoto() }
func (b fileBlock) CanAddPDF() bool   { return b.CanUpload && b.Section.CanAddPDF() }

type obsDetail struct {
	O           *Observation
	Topic       string
	Hazard      string
	HazardClass string
	ControlTag  string // the control topic the wizard's picklist answer set (PPE, Permit To Work, ...)
	Perms       obsPerms
	Blocks      []fileBlock
	CloseHint   string
}

// chip is one filter button above the list.
type chip struct {
	Label  string
	URL    string
	Active bool
}

type obsViewHandlers struct {
	db   *sql.DB
	auth *authContext
	ref  *refData
}

func (h *obsViewHandlers) viewer(r *http.Request) viewer {
	s := h.auth.session(r)
	return viewer{Username: s.Username, Role: s.Role}
}

func (h *obsViewHandlers) snapshot(w http.ResponseWriter, r *http.Request) *refSnapshot {
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		log.Printf("observation pages: %v", err)
		http.Error(w, "The lookup lists could not be loaded. Did you run migrations 007-009?", http.StatusServiceUnavailable)
		return nil
	}
	return snap
}

func (h *obsViewHandlers) dashboard(w http.ResponseWriter, r *http.Request) {
	snap := h.snapshot(w, r)
	if snap == nil {
		return
	}
	rng := parseRange(r.URL.Query())
	nav, err := buildNav(r.Context(), h.db, snap, "", "", rng)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tab := ""
	if r.URL.Query().Get("tab") == tabCompanies {
		tab = tabCompanies
	}
	sort := parseSort(r.URL.Query().Get("sort"))
	d, err := loadDashboard(r.Context(), h.db, rng, tab, sort)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sub := "HSE observations at a glance (by date of finding)"
	if rng.Active() {
		sub = "Date of finding: " + rng.Label()
	}
	// The date form and its Clear link stay on the tab being viewed.
	hidden, clear := map[string]string(nil), "/observations"
	if tab == tabCompanies {
		hidden, clear = map[string]string{"tab": tabCompanies}, "/observations?tab="+tabCompanies
		if sort != sortFindings {
			hidden["sort"], clear = sort, clear+"&sort="+sort
		}
	}
	render(w, "obs_dashboard", PageData{
		Title: "Dashboard", Heading: "Dashboard", Subheading: sub,
		Active: "obs-dashboard", Session: h.auth.session(r), ObsNav: nav, Dash: d,
		Range: rng, FilterAction: "/observations", FilterHidden: hidden, ClearURL: clear,
	})
}

func (h *obsViewHandlers) list(w http.ResponseWriter, r *http.Request) {
	snap := h.snapshot(w, r)
	if snap == nil {
		return
	}
	qv := r.URL.Query()
	lq := listQuery{Class: qv.Get("class"), Range: parseRange(qv), Verify: qv.Get("verify") == "1"}
	lq.Hazard, _ = strconv.ParseInt(qv.Get("hazard"), 10, 64)
	lq.Topic, _ = strconv.ParseInt(qv.Get("topic"), 10, 64)
	lq.ControlClass = qv.Get("cclass")
	if s := qv.Get("status"); validStatus(s) {
		lq.Status = s
	}
	if c := qv.Get("company"); len(c) <= maxCompanyLen {
		lq.Company = c
	}
	if s := qv.Get("source"); s == sourceSOC || s == sourceNone || snap.KnownSource(s) {
		lq.Source = s
		if t := qv.Get("type"); len(t) <= 80 && s != sourceNone {
			lq.Type = t
		}
	}

	var where []string
	var args []any
	heading := "Observations"
	switch {
	case lq.Hazard != 0:
		if hz, ok := snap.hazardByID[lq.Hazard]; ok {
			heading = hz.Class + " · " + hz.Name
			lq.Class = hz.Class // so the sidebar opens the right group
		}
		where, args = append(where, "hazard_id = ?"), append(args, lq.Hazard)
	case lq.Topic != 0:
		if t, ok := snap.topicByID[lq.Topic]; ok {
			heading = t.ControlClass + " · " + t.Name
			lq.ControlClass = t.ControlClass // so the sidebar opens the right class
		}
		// A control tag is kept in control_topic_id by the wizard, and in topic_id by older posts.
		where, args = append(where, "(topic_id = ? OR control_topic_id = ?)"), append(args, lq.Topic, lq.Topic)
	case lq.ControlClass != "":
		var ids []any
		marks := []string{}
		for _, g := range snap.ControlClasses() {
			if g.Name != lq.ControlClass {
				continue
			}
			for _, t := range g.Topics {
				ids, marks = append(ids, t.ID), append(marks, "?")
			}
		}
		heading = "Control · " + lq.ControlClass
		if len(ids) == 0 {
			where = append(where, "1 = 0")
		} else {
			in := strings.Join(marks, ",")
			where, args = append(where, "(topic_id IN ("+in+") OR control_topic_id IN ("+in+"))"), append(append(args, ids...), ids...)
		}
	case lq.Class == classUnclassified:
		heading = "Not classified"
		// A post with a control topic (PPE, ...) is classified even without a hazard category.
		where = append(where, "hazard_id IS NULL AND (topic_id IS NULL OR topic_id NOT IN (SELECT id FROM ref_topic WHERE kind = 'control'))")
	case lq.Class != "":
		var ids []any
		marks := []string{}
		for _, hz := range snap.Hazards {
			if hz.Class == lq.Class {
				ids, marks = append(ids, hz.ID), append(marks, "?")
			}
		}
		heading = lq.Class
		if len(ids) == 0 {
			where = append(where, "1 = 0")
		} else {
			where, args = append(where, "hazard_id IN ("+strings.Join(marks, ",")+")"), append(args, ids...)
		}
	}
	if lq.Status != "" {
		where, args = append(where, "status = ?"), append(args, lq.Status)
	}
	switch lq.Source {
	case "":
	case sourceNone:
		where = append(where, "source IS NULL")
		heading += " · Not recorded"
	default:
		where, args = append(where, "source = ?"), append(args, lq.Source)
		heading += " · " + lq.Source
		if lq.Type != "" {
			where, args = append(where, "source_detail = ?"), append(args, lq.Type)
			heading += " · " + lq.Type
		}
	}
	switch lq.Company {
	case "":
	case noCompany:
		where = append(where, "COALESCE(responsible_company,'') = ''")
		heading += " · " + noCompany
	default:
		where, args = append(where, "responsible_company = ?"), append(args, lq.Company)
		heading += " · " + lq.Company
	}
	if lq.Verify {
		where = append(where, "source = 'SOC' AND COALESCE(verification,'Not yet') <> 'Verified'")
	}
	c, a := lq.Range.where("observed_at")
	where, args = append(where, c...), append(args, a...)

	query := "SELECT " + observationColumns + " FROM observations_observation"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY observed_at DESC, id DESC LIMIT 500"
	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list := []*Observation{}
	for rows.Next() {
		o, err := scanObservation(rows)
		if err != nil {
			rows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		list = append(list, o)
	}
	if err := closeRows(rows); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nav, err := buildNav(r.Context(), h.db, snap, lq.Source, lq.Type, lq.Range)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Status chips keep every other filter (hazard, source, dates).
	chipURL := func(status string, verify bool) string {
		c := lq
		c.Status, c.Verify = status, verify
		return c.URL()
	}
	chips := []chip{{"All", chipURL("", false), lq.Status == "" && !lq.Verify}}
	for _, o := range observationStatuses {
		chips = append(chips, chip{o.Label, chipURL(o.Value, false), lq.Status == o.Value && !lq.Verify})
	}
	chips = append(chips, chip{"Waiting for verification", chipURL("", true), lq.Verify})

	filter := newListFilter(snap, lq)

	// The date form keeps the other filters as hidden fields; Clear drops only the dates.
	hidden := lq.values()
	hidden.Del("from")
	hidden.Del("to")
	hiddenMap := map[string]string{}
	for k := range hidden {
		hiddenMap[k] = hidden.Get(k)
	}
	clear := lq
	clear.Range = dateRange{}

	sub := fmt.Sprintf("%d shown", len(list))
	if lq.Range.Active() {
		sub += " · date of finding " + lq.Range.Label()
	}
	if len(list) == 500 {
		sub += " (first 500)"
	}
	render(w, "observations_list", PageData{
		Title: heading, Heading: heading, Subheading: sub,
		Active: "obs-list", Session: h.auth.session(r), ObsNav: nav, Observations: list, Chips: chips,
		Range: lq.Range, FilterAction: "/observations/list", FilterHidden: hiddenMap, ClearURL: clear.URL(), ListFilter: filter,
	})
}

// show renders one observation. flash is an optional message (kind "error" or "success").
func (h *obsViewHandlers) show(w http.ResponseWriter, r *http.Request, status int, id int64, flash, kind string) {
	snap := h.snapshot(w, r)
	if snap == nil {
		return
	}
	o, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files, err := listFiles(h.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	perms := permsFor(o, h.viewer(r))
	d := &obsDetail{O: o, Perms: perms}
	if t, ok := snap.topicByID[o.TopicID]; ok {
		d.Topic = t.Name
	}
	if hz, ok := snap.hazardByID[o.HazardID]; ok {
		d.Hazard, d.HazardClass = hz.Name, hz.Class
	}
	if t, ok := snap.topicByID[o.ControlTopicID]; ok {
		d.ControlTag = t.Name
	}
	sections := sectionsOf(files)
	before, after := sections[0], sections[1]
	d.Blocks = []fileBlock{
		{Section: before, ObsID: id, CanUpload: false, CanDelete: false}, // evidence is added on the edit page
		{Section: after, ObsID: id, CanUpload: perms.CanAddAfter, CanDelete: perms.CanAddAfter},
	}
	if !perms.Locked && closeBlocked(o.Category, statusClosed, after.Count()) {
		d.CloseHint = "Add at least one After photo or PDF below before closing."
	}
	nav, err := buildNav(r.Context(), h.db, snap, navSourceOf(o.Source), o.SourceDetail, dateRange{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "obs_detail", PageData{
		Title: fmt.Sprintf("Observation #%d", id), Heading: fmt.Sprintf("Observation #%d", id),
		Active: "obs-list", Session: h.auth.session(r), ObsNav: nav, Detail: d, Flash: flash, FlashKind: kind,
	})
}

func (h *obsViewHandlers) detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.show(w, r, http.StatusOK, id, flashFor(r.URL.Query().Get("done")), "success")
}

func flashFor(done string) string {
	switch done {
	case "started":
		return "Marked In Progress."
	case "closed":
		return "Closed. This post can no longer be edited."
	case "verified":
		return "Verified."
	case "uploaded":
		return "Files added."
	case "created":
		return "Observation saved."
	}
	return ""
}

// setStatus moves a post to In Progress or Closed. Closing needs After evidence (Positive
// Findings are exempt).
func (h *obsViewHandlers) setStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	o, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	perms := permsFor(o, h.viewer(r))
	to := r.FormValue("to")
	switch {
	case perms.Locked:
		h.show(w, r, http.StatusConflict, id, "This post is closed and can no longer be changed.", "error")
		return
	case to == statusInProgress && perms.CanStart:
	case to == statusClosed && perms.CanClose:
		after, err := countAfterFiles(r.Context(), h.db, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if closeBlocked(o.Category, statusClosed, after) {
			h.show(w, r, http.StatusUnprocessableEntity, id, msgCloseNeedsAfter, "error")
			return
		}
	default:
		http.Error(w, "That change is not allowed.", http.StatusForbidden)
		return
	}
	// "status <> 'closed'" makes a second person's late click on a post that was just closed a no-op.
	res, err := h.db.ExecContext(r.Context(), `UPDATE observations_observation SET status = ?, updated_at = NOW() WHERE id = ? AND status <> 'closed'`, to, id)
	if err != nil {
		log.Printf("status change failed: %v", err)
		http.Error(w, "Could not save the change.", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.show(w, r, http.StatusConflict, id, "This post was closed by someone else just now.", "error")
		return
	}
	done := "started"
	if to == statusClosed {
		done = "closed"
	}
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(id, 10)+"?done="+done, http.StatusSeeOther)
}

func (h *obsViewHandlers) verify(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	o, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !permsFor(o, h.viewer(r)).CanVerify {
		http.Error(w, "This post cannot be verified.", http.StatusForbidden)
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE observations_observation SET verification = 'Verified', updated_at = NOW()
		WHERE id = ? AND source = 'SOC'`, id); err != nil {
		log.Printf("verify failed: %v", err)
		http.Error(w, "Could not save the change.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(id, 10)+"?done=verified", http.StatusSeeOther)
}
