package web

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/card"
	"entry-permit/internal/dashboard"
	"entry-permit/internal/dates"
	"entry-permit/internal/gatesync"
	"entry-permit/internal/staff"
)

const (
	gateActivityRows   = 100 // newest scans shown on the dashboard's Gate tab
	lookupActivity     = 20  // scans shown for one permit on the gate page
	staleSyncIntervals = 3   // a gate not synced for this many intervals is flagged
	defaultDashTab     = "active"
)

// DashTab is one tab of the dashboard, with the number of rows behind it.
type DashTab struct {
	Slug, Label string
	Count       int
	Current     bool
}

// SyncNote tells staff how fresh the gate's side of the data is.
type SyncNote struct {
	At           string // the last sync that finished without an error
	Never, Stale bool
}

// GateLookup is the answer for one scanned permit number.
type GateLookup struct {
	Row        dashboard.Row
	State      string
	StateClass string
	Activity   []dashboard.Row
}

// CompanyOpt is one entry of the company picker.
type CompanyOpt struct {
	Name string
	On   bool
}

// DashData feeds the dashboard and the gate page.
type DashData struct {
	Tab      string
	Tabs     []DashTab
	Q        string
	Rows     []dashboard.Row // the list of a list tab
	Inside   []dashboard.Row
	Activity []dashboard.Row
	Sync     SyncNote
	PDFHref  string // the PDF of the tab and filters on screen
	Lep      string // the permit number typed on the gate page, only when well-formed
	Hint     string // why a lookup shows nothing
	Lookup   *GateLookup

	// The filter bar and the charts (the old dashboard's behaviour, as plain GET links and forms).
	Kind          string // "", "work" or "visitor"
	CompanyOpts   []CompanyOpt
	CompanyLabel  string // the picker's summary: "All companies", the one picked, or "3 companies"
	SoonLabel     string // the pill's text for this tab; empty when the tab has no such pill
	Soon          bool
	ShowCheckedIn bool
	CheckedIn     bool
	Level2        bool
	Filtered      bool // any filter is on, so "Clear" is offered
	Total         int  // rows the tab holds before any filter
	Charted       bool // the tab shows a company pie
	Slices        []dashboard.Slice
	Stages        []dashboard.StageCount // Pending: how many wait at each stage
	Approval      []dashboard.Slice      // Pending: the approved / rejected pie
	Day, PrevDay  string                 // Gate: the day shown and the day before
	NextDay       string                 // empty when the day shown is today
}

func (a *App) dash() dashboard.Store { return dashboard.Store{DB: a.SQL} }

// syncNote reads when the gate was last synced. A read error is logged and shown
// as "never": the page is still useful, and a wrong "fresh" is the worse mistake.
func (a *App) syncNote(ctx context.Context) SyncNote {
	at, err := gatesync.LastSuccess(ctx, a.SQL)
	if err != nil {
		log.Printf("dashboard: %v", err)
		return SyncNote{Never: true}
	}
	if at == "" {
		return SyncNote{Never: true}
	}
	every := max(a.policy().GateSyncMinutes, 1)
	return SyncNote{At: at, Stale: dates.Moment(at).AddMinutes(staleSyncIntervals*every) < a.Clock.Now()}
}

func (a *App) dashFail(w http.ResponseWriter, what string, err error) {
	log.Printf("dashboard %s: %v", what, err)
	http.Error(w, "Something went wrong", http.StatusInternalServerError)
}

// dashboardPage is /staff/dashboard: five tabs of read-only lists with a search box.
// It is open to every staff member, so no list holds a birth date, e-mail or file.
func (a *App) dashboardPage(w http.ResponseWriter, r *http.Request, u *staff.User) {
	d, err := a.dashData(r.Context(), r.URL.Query())
	if err != nil {
		a.dashFail(w, "page", err)
		return
	}
	a.render(w, "dashboard", http.StatusOK, View{Title: staff.Dashboard.Label(), User: u, Links: menu(*u, staff.Dashboard.Slug()), Dash: d})
}

// soonLabels are the pill texts, one per tab that has such a pill.
var soonLabels = map[string]string{"registered": "Expiring in 1 month", "active": "Ticket ending in 7 days", "scheduled": "Starting in 7 days"}

// dashData builds what one tab shows from the query string (tab, q, kind, company, soon,
// checked_in, group, date). The page and the PDF both use it, so the file can never hold more, or
// different rows, than the screen.
func (a *App) dashData(ctx context.Context, v url.Values) (DashData, error) {
	tab := v.Get("tab")
	st, today := a.dash(), string(a.Clock.Today())
	lists := map[string][]dashboard.Row{}
	var err error
	for _, l := range []struct {
		slug string
		load func() ([]dashboard.Row, error)
	}{
		{"pending", func() ([]dashboard.Row, error) { return st.Pending(ctx) }},
		{"registered", func() ([]dashboard.Row, error) { return st.Registered(ctx) }},
		{"active", func() ([]dashboard.Row, error) { return st.Active(ctx, today) }},
		{"scheduled", func() ([]dashboard.Row, error) { return st.Scheduled(ctx, today) }},
		{"gate", func() ([]dashboard.Row, error) { return st.Inside(ctx) }},
	} {
		if lists[l.slug], err = l.load(); err != nil {
			return DashData{}, fmt.Errorf("%s list: %w", l.slug, err)
		}
	}

	if _, ok := lists[tab]; !ok {
		tab = defaultDashTab // an unknown tab is the default, never an error
	}
	opts := dashboard.Options{Q: strings.TrimSpace(v.Get("q")), Soon: v.Get("soon") == "1" && soonLabels[tab] != "",
		CheckedIn: v.Get("checked_in") == "1" && tab == "active", Level2: v.Get("group") == "level2"}
	switch dashboard.Kind(v.Get("kind")) {
	case dashboard.KindWork:
		opts.Kind = dashboard.KindWork
	case dashboard.KindVisitor:
		opts.Kind = dashboard.KindVisitor
	}
	opts.Companies = v["company"]
	d := DashData{Tab: tab, Q: opts.Q, Sync: a.syncNote(ctx), Kind: string(opts.Kind), Soon: opts.Soon, SoonLabel: soonLabels[tab],
		ShowCheckedIn: tab == "active", CheckedIn: opts.CheckedIn, Level2: opts.Level2, Total: len(lists[tab])}
	for _, t := range []struct{ slug, label string }{{"pending", "Pending"}, {"registered", "Registered"}, {"active", "Active"}, {"scheduled", "Scheduled"}, {"gate", "Gate"}} {
		d.Tabs = append(d.Tabs, DashTab{Slug: t.slug, Label: t.label, Count: len(lists[t.slug]), Current: t.slug == tab})
	}
	switch tab {
	case "gate":
		d.Inside = dashboard.Filter(lists["gate"], opts.Q)
		d.Day = gateDay(v.Get("date"), today)
		d.PrevDay = string(dates.Day(d.Day).AddDays(-1))
		if d.Day < today {
			d.NextDay = string(dates.Day(d.Day).AddDays(1))
		}
		if d.Activity, err = st.GateActivityOn(ctx, d.Day, gateActivityRows); err != nil {
			return DashData{}, fmt.Errorf("gate activity: %w", err)
		}
		d.Activity = dashboard.Filter(d.Activity, opts.Q)
		d.Filtered = opts.Q != "" || d.Day != today
	case "pending":
		d.Rows = dashboard.Filter(lists[tab], opts.Q)
		d.Stages = dashboard.StageCounts(lists[tab])
		if d.Approval, err = st.ApprovalStats(ctx); err != nil {
			return DashData{}, fmt.Errorf("approval stats: %w", err)
		}
		d.Filtered = opts.Q != ""
	default:
		picked := map[string]bool{}
		for _, c := range opts.Companies {
			picked[c] = true
		}
		for _, c := range dashboard.Companies(lists[tab]) {
			d.CompanyOpts = append(d.CompanyOpts, CompanyOpt{Name: c, On: picked[c]})
		}
		// A company that is no longer in the list cannot stay picked and hide everything.
		opts.Companies = opts.Companies[:0:0]
		for _, c := range d.CompanyOpts {
			if c.On {
				opts.Companies = append(opts.Companies, c.Name)
			}
		}
		d.CompanyLabel = companyLabel(opts.Companies)
		d.Rows = dashboard.Apply(lists[tab], opts, tab, today)
		d.Charted, d.Slices = true, dashboard.Distribution(d.Rows, opts.Level2)
		d.Filtered = opts.Q != "" || opts.Kind != "" || len(opts.Companies) > 0 || opts.Soon || opts.CheckedIn
	}
	d.PDFHref = pdfHref(d, v)
	return d, nil
}

// gateDay reads the gate tab's day: a real yyyy-mm-dd that is not in the future, else today.
func gateDay(s, today string) string {
	day, err := dates.ParseDay(strings.TrimSpace(s))
	if err != nil || string(day) > today {
		return today
	}
	return string(day)
}

func companyLabel(picked []string) string {
	switch len(picked) {
	case 0:
		return "All companies"
	case 1:
		return picked[0]
	}
	return fmt.Sprintf("%d companies", len(picked))
}

// syncLine is the sentence the PDF prints about how fresh the gate data is.
func syncLine(n SyncNote) string {
	switch {
	case n.Never:
		return "The gate has not been synced yet."
	case n.Stale:
		return "Gate data is out of date: last good sync " + n.At + "."
	}
	return "Gate data last synced " + n.At + "."
}

// exportDoc turns what the page shows into a PDF document.
func exportDoc(d DashData, u *staff.User, at string) dashboard.Doc {
	label := d.Tab
	for _, t := range d.Tabs {
		if t.Slug == d.Tab {
			label = t.Label
		}
	}
	return dashboard.Doc{
		Tab: d.Tab, Title: "Dashboard: " + label, Query: d.Q, GeneratedAt: at, By: u.DisplayName,
		Sync: syncLine(d.Sync), Rows: d.Rows, Inside: d.Inside, Activity: d.Activity,
	}
}

// dashboardPDF is /staff/dashboard.pdf: the tab and search on screen, as a file.
//
// WHY it is recorded before it is sent: the file is a list of named people, and
// the shared Viewer account may download it. If the audit row cannot be written
// the file is not served (the same rule as every admin save).
func (a *App) dashboardPDF(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d, err := a.dashData(r.Context(), r.URL.Query())
	if err != nil {
		a.dashFail(w, "pdf", err)
		return
	}
	now := a.Clock.Now()
	if err := audit.Write(r.Context(), a.SQL, now, u.Username, "dashboard.export_pdf", "dashboard:"+d.Tab, "", d.Q); err != nil {
		a.dashFail(w, "pdf audit", err)
		return
	}
	var buf bytes.Buffer // built fully first, so a failure never sends half a file
	if err := dashboard.WritePDF(&buf, exportDoc(d, u, string(now))); err != nil {
		a.dashFail(w, "pdf", err)
		return
	}
	// d.Tab is one of five fixed words, so the file name never holds typed text.
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dashboard-%s-%s.pdf"`, d.Tab, a.Clock.Today()))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = buf.WriteTo(w)
}

// pdfHref is the link to the PDF of the tab and filters now on screen. Only the known filter
// names are carried over, so nothing else typed in the address bar reaches the link.
func pdfHref(d DashData, v url.Values) string {
	out := url.Values{"tab": {d.Tab}}
	for _, k := range []string{"q", "kind", "soon", "checked_in", "group", "date"} {
		if x := v.Get(k); x != "" {
			out.Set(k, x)
		}
	}
	for _, c := range v["company"] {
		out.Add("company", c)
	}
	return "/staff/dashboard.pdf?" + out.Encode()
}

// gatePage is /staff/gate, where the QR scan link lands for a gate officer:
// who is inside now and, when a permit number is given, that permit's state and
// recent scans. Check-in and check-out themselves happen on the gate scanner page.
func (a *App) gatePage(w http.ResponseWriter, r *http.Request, u *staff.User) {
	ctx, st := r.Context(), a.dash()
	inside, err := st.Inside(ctx)
	if err != nil {
		a.dashFail(w, "inside", err)
		return
	}
	d := DashData{Inside: inside, Sync: a.syncNote(ctx)}
	if lep := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("lep"))); lep != "" {
		if !lepPattern.MatchString(lep) {
			// WHY the typed text is not echoed: it came from the address bar.
			d.Hint = "Enter a permit number like LEP-ABCDE or WVR-ABCDE."
		} else {
			d.Lep = lep
			if d.Lookup, d.Hint, err = a.gateLookup(ctx, st, lep); err != nil {
				a.dashFail(w, "lookup", err)
				return
			}
		}
	}
	a.render(w, "gate", http.StatusOK, View{Title: staff.GateOfficer.Label(), User: u, Links: menu(*u, staff.GateOfficer.Slug()), Dash: d})
}

func (a *App) gateLookup(ctx context.Context, st dashboard.Store, lep string) (*GateLookup, string, error) {
	row, found, err := st.Find(ctx, lep)
	if err != nil {
		return nil, "", fmt.Errorf("find %s: %w", lep, err)
	}
	if !found {
		return nil, "No permit with that number.", nil
	}
	activity, err := st.GateActivity(ctx, lep, lookupActivity)
	if err != nil {
		return nil, "", fmt.Errorf("activity %s: %w", lep, err)
	}
	state := card.StateOf(row.Status, dates.Day(row.ValidUntil), a.Clock.Today())
	return &GateLookup{Row: row, State: state.Label(), StateClass: stateClass(state), Activity: activity}, "", nil
}
