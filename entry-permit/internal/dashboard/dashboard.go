// Package dashboard builds the read-only lists staff see on the dashboard:
// Pending, Registered, Active, Scheduled, who is Inside the gate now, and the
// gate's activity log.
//
// Everything here reads the Pi database only. The gate's side (gate_state,
// gate_log) arrives through gatesync, so these lists can lag the gate by one
// sync (about five minutes). No list holds a birth date, an e-mail address or a
// document path: the dashboard is open to every staff member, including the
// shared Viewer account.
package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/waiver"
)

// Kind says what a row is, so the page can label and link it.
type Kind string

const (
	KindWork    Kind = "work"
	KindVisitor Kind = "visitor"
	KindTicket  Kind = "ticket"
)

const statusInside = "INSIDE" // the gate Worker's word for "checked in and not out"

// Row is one line of any list. Fields a list does not use stay empty.
type Row struct {
	Kind                    Kind
	ID                      string // LEP-, WVR- or, in Pending, the request id
	Name, Company, Position string
	Entity, Parents         string
	Status                  string
	ValidUntil              string
	Arrival, Departure      string // the ticket window, when the list has one
	Active                  bool   // Registered: the permit is still Approved
	Stage                   string // Pending: stage1, doctor, final
	At, Gate, Action        string // Inside and gate activity
	Reason, Officer         string
	PrevEnd                 string // Rest: the day the previous ticket ended
	RestDays, RestRequired  int    // Rest: the gap, and the rest the rules ask for
	RestKind, RestLabel     string // Rest: "short", "ok", ... and the words for it
	Ring                    string // set by WithRings, for the Ring pie
}

// Store reads the lists.
type Store struct{ DB *sql.DB }

// ── Pending ──────────────────────────────────────────────────────────────────

var stageOrder = map[string]int{"stage1": 0, "doctor": 1, "final": 2}

var stageLabel = map[string]string{
	"stage1": "Waiting for Stage 1",
	"doctor": "Waiting for Doctor",
	"final":  "Waiting for Final",
}

// Pending lists every request still waiting for a decision: work permits and
// waivers at their stage, and tickets waiting for the doctor. Stage 1 comes
// first, then Doctor, then Final, each by name.
func (s Store) Pending(ctx context.Context) ([]Row, error) {
	var out []Row
	permits, err := s.rows(ctx, `SELECT request_id, worker_name, company, work_position, entity, parents,
		CASE status WHEN ? THEN 'stage1' ELSE 'final' END
		FROM permit WHERE status IN (?, ?)`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Entity, &w.Parents, &w.Stage)
		}, permit.StatusPendingStage1, permit.StatusPendingStage1, permit.StatusPendingFinal)
	if err != nil {
		return nil, err
	}
	for _, r := range permits {
		r.Kind = KindWork
		out = append(out, r)
	}

	waivers, err := s.rows(ctx, `SELECT request_id, visitor_name, company, position,
		CASE status WHEN ? THEN 'doctor' WHEN ? THEN 'stage1' ELSE 'final' END
		FROM waiver WHERE status IN (?, ?, ?)`,
		func(r *sql.Rows, w *Row) error { return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Stage) },
		waiver.StatusPendingDoctor, waiver.StatusPendingStage1,
		waiver.StatusPendingDoctor, waiver.StatusPendingStage1, waiver.StatusPendingSiteManager)
	if err != nil {
		return nil, err
	}
	for _, r := range waivers {
		r.Kind = KindVisitor
		out = append(out, r)
	}

	// A ticket's owner is a permit (LEP) or a waiver (WVR).
	tickets, err := s.rows(ctx, `SELECT t.ticket_id,
		COALESCE((SELECT worker_name FROM permit WHERE lep_no = t.lep_no), (SELECT visitor_name FROM waiver WHERE request_id = t.lep_no), ''),
		COALESCE((SELECT company FROM permit WHERE lep_no = t.lep_no), (SELECT company FROM waiver WHERE request_id = t.lep_no), ''),
		COALESCE((SELECT work_position FROM permit WHERE lep_no = t.lep_no), (SELECT position FROM waiver WHERE request_id = t.lep_no), ''),
		t.arrival, t.departure
		FROM ticket t WHERE t.status = 'PendingDoctor'`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Arrival, &w.Departure)
		})
	if err != nil {
		return nil, err
	}
	for _, r := range tickets {
		r.Kind, r.Stage = KindTicket, "doctor"
		out = append(out, r)
	}

	for i := range out {
		out[i].Status = stageLabel[out[i].Stage]
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := stageOrder[out[i].Stage], stageOrder[out[j].Stage]; a != b {
			return a < b
		}
		return lower(out[i].Name) < lower(out[j].Name)
	})
	return out, nil
}

// ── Registered ───────────────────────────────────────────────────────────────

// Registered lists every LEP ever issued (any status, so a resigned or expired
// permit still shows) and the approved or completed waivers. Work permits come
// first, then visitors, each by name.
func (s Store) Registered(ctx context.Context) ([]Row, error) {
	work, err := s.rows(ctx, `SELECT lep_no, worker_name, company, work_position, entity, parents, status, lep_valid_until
		FROM permit WHERE lep_no <> ''`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Entity, &w.Parents, &w.Status, &w.ValidUntil)
		})
	if err != nil {
		return nil, err
	}
	for i := range work {
		work[i].Kind = KindWork
		work[i].Active = work[i].Status == permit.StatusApproved
	}
	visitors, err := s.rows(ctx, `SELECT request_id, visitor_name, company, position, status, visit_to
		FROM waiver WHERE status IN (?, ?)`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Status, &w.ValidUntil)
		}, waiver.StatusApproved, waiver.StatusCompleted)
	if err != nil {
		return nil, err
	}
	for i := range visitors {
		visitors[i].Kind = KindVisitor
		visitors[i].Active = visitors[i].Status == waiver.StatusApproved
	}
	sortByName(work)
	sortByName(visitors)
	return append(work, visitors...), nil
}

// ── Active and Scheduled ─────────────────────────────────────────────────────

type window struct{ arrival, departure string }

// approvedWork loads the Approved work permits by LEP, and every open work
// ticket (Approved, not closed) by LEP. Both are read completely before the
// caller does anything else, so no cursor stays open.
func (s Store) approvedWork(ctx context.Context) (map[string]Row, map[string][]window, error) {
	permits, err := s.rows(ctx, `SELECT lep_no, worker_name, company, work_position, entity, parents, lep_valid_until
		FROM permit WHERE status = ? AND lep_no <> ''`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Entity, &w.Parents, &w.ValidUntil)
		}, permit.StatusApproved)
	if err != nil {
		return nil, nil, err
	}
	byLEP := make(map[string]Row, len(permits))
	for _, p := range permits {
		p.Kind = KindWork
		byLEP[p.ID] = p
	}
	tickets, err := s.rows(ctx, `SELECT lep_no, arrival, departure FROM ticket
		WHERE is_work = 1 AND status = 'Approved' AND closed_at = '' ORDER BY arrival`,
		func(r *sql.Rows, w *Row) error { return r.Scan(&w.ID, &w.Arrival, &w.Departure) })
	if err != nil {
		return nil, nil, err
	}
	open := map[string][]window{}
	for _, t := range tickets {
		open[t.ID] = append(open[t.ID], window{t.Arrival, t.Departure})
	}
	return byLEP, open, nil
}

// insideSet is who the gate says is inside now, with where and since when.
func (s Store) insideSet(ctx context.Context) (map[string]Row, error) {
	rows, err := s.rows(ctx, `SELECT lep_no, gate, last_action_at, officer FROM gate_state WHERE status = ?`,
		func(r *sql.Rows, w *Row) error { return r.Scan(&w.ID, &w.Gate, &w.At, &w.Officer) }, statusInside)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Row, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// Active lists approved work permits that are inside the gate right now, or
// whose open ticket covers today. A worker inside who has no ticket today still
// shows, because the gate lets them check out.
func (s Store) Active(ctx context.Context, today string) ([]Row, error) {
	byLEP, open, err := s.approvedWork(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.insideSet(ctx)
	if err != nil {
		return nil, err
	}
	var out []Row
	for lep, p := range byLEP {
		g, isIn := in[lep]
		cover, covers := covering(open[lep], today)
		if !isIn && !covers {
			continue
		}
		if covers {
			p.Arrival, p.Departure = cover.arrival, cover.departure
		}
		p.Status = "In ticket window"
		if isIn {
			p.Status, p.Gate, p.At = "Inside", g.Gate, g.At
		}
		p.Active = true
		out = append(out, p)
	}
	sortByName(out)
	return out, nil
}

// covering picks, among tickets whose window holds today, the one that ends last.
func covering(ws []window, today string) (window, bool) {
	var best window
	found := false
	for _, w := range ws {
		if w.arrival <= today && today <= w.departure && (!found || w.departure > best.departure) {
			best, found = w, true
		}
	}
	return best, found
}

// Scheduled lists approved work permits with an open ticket that starts after
// today. A permit shows once, with its earliest coming ticket.
func (s Store) Scheduled(ctx context.Context, today string) ([]Row, error) {
	byLEP, open, err := s.approvedWork(ctx)
	if err != nil {
		return nil, err
	}
	var out []Row
	for lep, p := range byLEP {
		var next *window
		for i, w := range open[lep] { // already ordered by arrival
			if w.arrival > today {
				next = &open[lep][i]
				break
			}
		}
		if next == nil {
			continue
		}
		p.Arrival, p.Departure, p.Status, p.Active = next.arrival, next.departure, "Scheduled", true
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Arrival != out[j].Arrival {
			return out[i].Arrival < out[j].Arrival
		}
		return lower(out[i].Name) < lower(out[j].Name)
	})
	return out, nil
}

// ── The gate ─────────────────────────────────────────────────────────────────

// Inside lists everyone the gate says is inside now, with their names.
func (s Store) Inside(ctx context.Context) ([]Row, error) {
	rows, err := s.rows(ctx, `SELECT g.lep_no,
		COALESCE((SELECT worker_name FROM permit WHERE lep_no = g.lep_no), (SELECT visitor_name FROM waiver WHERE request_id = g.lep_no), ''),
		COALESCE((SELECT company FROM permit WHERE lep_no = g.lep_no), (SELECT company FROM waiver WHERE request_id = g.lep_no), ''),
		g.gate, g.last_action_at, g.officer
		FROM gate_state g WHERE g.status = ?`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Gate, &w.At, &w.Officer)
		}, statusInside)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = "Inside"
	}
	sortByName(rows)
	return rows, nil
}

// GateActivity lists the newest scans first, optionally for one permit number
// (any case). limit caps the rows; the log grows without end.
func (s Store) GateActivity(ctx context.Context, lep string, limit int) ([]Row, error) {
	lep = strings.ToUpper(strings.TrimSpace(lep))
	return s.rows(ctx, `SELECT lep_no, worker_name, company, action, reason, officer, gate, at
		FROM gate_log WHERE (? = '' OR lep_no = ?) ORDER BY at DESC, worker_id DESC LIMIT ?`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Action, &w.Reason, &w.Officer, &w.Gate, &w.At)
		}, lep, lep, limit)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// rows runs one query and reads it completely, closing the cursor before it
// returns (the tests, and a small pool in production, allow only one open cursor).
func (s Store) rows(ctx context.Context, q string, scan func(*sql.Rows, *Row) error, args ...any) ([]Row, error) {
	rs, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard query: %w", err)
	}
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var r Row
		if err := scan(rs, &r); err != nil {
			return nil, fmt.Errorf("dashboard row: %w", err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("dashboard rows: %w", err)
	}
	return out, nil
}

func lower(s string) string { return strings.ToLower(s) }

func sortByName(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := lower(rows[i].Name), lower(rows[j].Name); a != b {
			return a < b
		}
		return rows[i].ID < rows[j].ID
	})
}

// fold makes text comparable for search: lower case and no apostrophes (plain
// or curly), so "As'adul" and "asadul" are the same word.
func fold(s string) string {
	s = strings.ToLower(s)
	for _, q := range []string{"'", "’", "‘", "`"} {
		s = strings.ReplaceAll(s, q, "")
	}
	return strings.TrimSpace(s)
}

// Filter keeps the rows whose name, company, position or number contains the
// search text. An empty search keeps everything.
func Filter(rows []Row, q string) []Row {
	q = fold(q)
	if q == "" {
		return rows
	}
	var out []Row
	for _, r := range rows {
		if strings.Contains(fold(r.Name), q) || strings.Contains(fold(r.Company), q) ||
			strings.Contains(fold(r.Position), q) || strings.Contains(fold(r.ID), q) {
			out = append(out, r)
		}
	}
	return out
}

// Find looks up one permit by its LEP number, or one waiver by its WVR number
// (any case), for the gate page. A request id (WEP-) never matches: it is not a
// number a gate scanner ever reads, and permits without an LEP have none.
func (s Store) Find(ctx context.Context, id string) (Row, bool, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" {
		return Row{}, false, nil
	}
	rows, err := s.rows(ctx, `SELECT lep_no, worker_name, company, work_position, status, lep_valid_until
		FROM permit WHERE lep_no = ?`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Status, &w.ValidUntil)
		}, id)
	if err != nil {
		return Row{}, false, err
	}
	if len(rows) > 0 {
		rows[0].Kind = KindWork
		return rows[0], true, nil
	}
	rows, err = s.rows(ctx, `SELECT request_id, visitor_name, company, position, status, visit_to
		FROM waiver WHERE request_id = ?`,
		func(r *sql.Rows, w *Row) error {
			return r.Scan(&w.ID, &w.Name, &w.Company, &w.Position, &w.Status, &w.ValidUntil)
		}, id)
	if err != nil || len(rows) == 0 {
		return Row{}, false, err
	}
	rows[0].Kind = KindVisitor
	return rows[0], true, nil
}

// HasCard reports whether the row's number opens a public card: only a work
// permit that has been issued an LEP number has one.
// Weeks is the ticket window's length in weeks (one decimal, no ".0" on whole weeks), or "" with no window.
func (r Row) Weeks() string {
	if r.Arrival == "" || r.Departure == "" {
		return ""
	}
	days := dates.InclusiveDays(dates.Day(r.Arrival), dates.Day(r.Departure))
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(days)/7), ".0")
}

func (r Row) HasCard() bool { return r.Kind == KindWork && strings.HasPrefix(r.ID, "LEP-") }
