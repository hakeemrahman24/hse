package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	activitiesFetchLimit = 500 // the most hse-medpost returns in one answer
	activitiesCacheTTL   = 2 * time.Minute
)

var errNoActivitiesRoute = errors.New("this hse-medpost has no activities list yet")

// medpostActivity is one program activity as hse-medpost lists it for other applications:
// program, kind, title, summary, date and place. Nothing else is read from the answer.
type medpostActivity struct {
	ActivityID  int64  `json:"activityId"`
	Program     string `json:"program"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Date        string `json:"date"`
	Location    string `json:"location"`
	SubHeaderID int64  `json:"subHeaderId"` // non-zero: an entry of a program sub-header
	// Photos and Proof come with the list (?files=1); an older hse-medpost leaves them empty.
	Photos []medpostFile `json:"photos"`
	Proof  []medpostFile `json:"proof"`
}

// Activities reads the newest activities (GET /api/directory/activities), the same bearer token
// as the employee directory. It is read-only: this application never writes to hse-medpost.
func (m *medpostDirectory) Activities(ctx context.Context, limit int) ([]medpostActivity, error) {
	target := m.baseURL + "/api/directory/activities?files=1&subheaders=1&limit=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second, CheckRedirect: medpostTestClient.CheckRedirect}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost request failed: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, errNoActivitiesRoute
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("medpost answered %d", resp.StatusCode)
	}
	var rows []medpostActivity
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("medpost answer could not be read: %w", err)
	}
	for i := range rows {
		finishFiles(rows[i].Photos)
		finishFiles(rows[i].Proof)
	}
	return rows, nil
}

// activitiesHandlers serves the view-only Medical Activities page for every signed-in user.
// The list is cached for a couple of minutes so the page does not call hse-medpost on every
// view; Reset drops it when an administrator changes the connection.
type activitiesHandlers struct {
	auth *authContext
	conn *medpostConn

	mu   sync.Mutex
	rows []medpostActivity
	subs []medpostSubHeader
	at   time.Time
}

func (h *activitiesHandlers) Reset() {
	h.mu.Lock()
	h.rows, h.subs, h.at = nil, nil, time.Time{}
	h.mu.Unlock()
}

// list returns the cached rows, or fetches them. A failed fetch serves older rows when there
// are some, so a short outage does not blank the page.
func (h *activitiesHandlers) list(ctx context.Context, d *medpostDirectory) ([]medpostActivity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rows != nil && time.Since(h.at) < activitiesCacheTTL {
		return h.rows, nil
	}
	rows, err := d.Activities(ctx, activitiesFetchLimit)
	if err != nil {
		if h.rows != nil {
			return h.rows, nil
		}
		return nil, err
	}
	// Sub-headers are a bonus: an older hse-medpost without the route just shows none.
	if subs, err := d.SubHeaders(ctx); err != nil {
		if !errors.Is(err, errMedpostNotFound) {
			log.Printf("sub-headers: %v", err)
		}
		h.subs = nil
	} else {
		h.subs = subs
	}
	h.rows, h.at = rows, time.Now()
	return rows, nil
}

// subHeaders returns the sub-headers fetched with the last list.
func (h *activitiesHandlers) subHeaders() []medpostSubHeader {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.subs
}

// Activity page states.
const (
	activitiesOK    = "ok"
	activitiesOff   = "off"   // not connected to hse-medpost
	activitiesNoAPI = "noapi" // hse-medpost has no activities list yet
	activitiesError = "error" // hse-medpost could not be reached or answered badly
)

func (h *activitiesHandlers) show(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := PageData{
		Title: "Medical Activities", Heading: "Medical Activities", Active: "activities", ActSide: true,
		Subheading: "View only. Health programs and what was done at the first-aid post, with the photos and documents uploaded in HSE Medpost.",
		Session:    h.auth.session(r), ActState: activitiesOK, ActTab: "activities",
	}
	if q.Get("tab") == "reference" {
		data.ActTab = "reference"
	}
	d := h.conn.Get()
	if d == nil {
		data.ActState = activitiesOff
		renderStatus(w, http.StatusOK, "activities", data)
		return
	}
	rows, err := h.list(r.Context(), d)
	switch {
	case errors.Is(err, errNoActivitiesRoute):
		data.ActState = activitiesNoAPI
	case err != nil:
		log.Printf("activities: %v", err) // never contains the token or the answer body
		data.ActState = activitiesError
	}
	if data.ActState != activitiesOK {
		renderStatus(w, http.StatusOK, "activities", data)
		return
	}
	data.ActFrom, data.ActTo, data.ActProgram = validDay(q.Get("from")), validDay(q.Get("to")), q.Get("program")
	subs := h.subHeaders()
	sub, _ := strconv.ParseInt(q.Get("sub"), 10, 64)
	data.ActPrograms = programsOf(rows)
	data.ActTree, data.ActSub = programTree(data.ActPrograms, subs, data.ActProgram, sub)
	if data.ActProgram != "" {
		data.Heading = data.ActProgram
	}
	if data.ActSub != nil {
		data.Heading, data.ActProgram = data.ActSub.Name, data.ActSub.Program
	}
	if data.ActTab == "reference" {
		h.loadReference(r, d, &data)
	} else {
		var shown []medpostActivity
		for _, a := range rows {
			if (a.SubHeaderID != 0) == (data.ActSub != nil) && (data.ActSub == nil || a.SubHeaderID == data.ActSub.SubHeaderID) {
				shown = append(shown, a)
			}
		}
		data.Activities = filterActivities(shown, data.ActFrom, data.ActTo, data.ActProgram)
		data.ActGroups = groupByType(data.Activities)
		data.ActMore = len(rows) >= activitiesFetchLimit
	}
	renderStatus(w, http.StatusOK, "activities", data)
}

// loadReference fills the Reference tab: the program documents, for one program or for all.
func (h *activitiesHandlers) loadReference(r *http.Request, d *medpostDirectory, data *PageData) {
	docs, err := d.Documents(r.Context())
	switch {
	case errors.Is(err, errMedpostNotFound):
		data.ActState = activitiesNoAPI
		return
	case err != nil:
		log.Printf("documents: %v", err)
		data.ActState = activitiesError
		return
	}
	if data.ActProgram != "" {
		kept := docs[:0:0]
		for _, doc := range docs {
			if doc.Program == data.ActProgram {
				kept = append(kept, doc)
			}
		}
		docs = kept
	}
	data.DocGroups = groupDocs(docs)
}

// actGroup is the activities of one type (Monitoring/Inspection, Socialization), newest first, as
// Medpost's program page lists them.
type actGroup struct {
	Type  string
	Items []medpostActivity
}

func groupByType(rows []medpostActivity) []actGroup {
	var out []actGroup
	idx := map[string]int{}
	for _, a := range rows {
		i, ok := idx[a.Type]
		if !ok {
			i = len(out)
			idx[a.Type] = i
			out = append(out, actGroup{Type: a.Type})
		}
		out[i].Items = append(out[i].Items, a)
	}
	return out
}

// validDay returns s when it is a YYYY-MM-DD date, otherwise "" (the filter is then ignored).
func validDay(s string) string {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

func programsOf(rows []medpostActivity) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range rows {
		if a.Program != "" && !seen[a.Program] {
			seen[a.Program] = true
			out = append(out, a.Program)
		}
	}
	sort.Strings(out)
	return out
}

// filterActivities keeps the rows inside the date range and program. Dates are YYYY-MM-DD text,
// so comparing the strings compares the days.
func filterActivities(rows []medpostActivity, from, to, program string) []medpostActivity {
	out := make([]medpostActivity, 0, len(rows))
	for _, a := range rows {
		switch {
		case from != "" && a.Date < from, to != "" && a.Date > to:
			continue
		case program != "" && a.Program != program:
			continue
		}
		out = append(out, a)
	}
	return out
}

// actProgram is one program of the sidebar tree, with its sub-headers.
type actProgram struct {
	Name   string
	Active bool // the program page itself is open
	Open   bool // expanded: the program or one of its sub-headers is open
	Subs   []actSub
}

type actSub struct {
	SubHeaderID int64
	Name        string
	Program     string
	Active      bool
}

// programTree builds the sidebar: every program (from the activities and the sub-headers), each
// with its sub-headers. selected is the open sub-header, or nil.
func programTree(programs []string, subs []medpostSubHeader, program string, subID int64) ([]actProgram, *actSub) {
	names := append([]string(nil), programs...)
	seen := map[string]bool{}
	for _, p := range names {
		seen[p] = true
	}
	for _, s := range subs {
		if s.Program != "" && !seen[s.Program] {
			seen[s.Program] = true
			names = append(names, s.Program)
		}
	}
	sort.Strings(names)
	var selected *actSub
	out := make([]actProgram, 0, len(names))
	for _, n := range names {
		p := actProgram{Name: n}
		for _, s := range subs {
			if s.Program != n {
				continue
			}
			item := actSub{SubHeaderID: s.SubHeaderID, Name: s.Name, Program: n, Active: s.SubHeaderID == subID}
			if item.Active {
				selected = &item
				p.Open = true
			}
			p.Subs = append(p.Subs, item)
		}
		if subID == 0 && n == program {
			p.Active, p.Open = true, true
		}
		out = append(out, p)
	}
	return out, selected
}
