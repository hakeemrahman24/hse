package gatesync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"entry-permit/internal/permit"
	"entry-permit/internal/policy"
	"entry-permit/internal/reflist"
	"entry-permit/internal/waiver"
)

// Item is one permit, waiver or ticket to push, with the hash of what would be sent.
type Item struct {
	Key    string // permit:LEP-x, ticket:TKT-x
	Hash   string
	Permit *WirePermit
	Ticket *WireTicket
}

func hashOf(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// items reads everything the gate should hold: Approved permits that have an LEP
// number, Approved waivers, and the tickets of those that are Approved or Closed.
// Rows are read completely and the cursor closed before returning, so a slow push
// never holds a database connection.
func items(ctx context.Context, db *sql.DB) ([]Item, error) {
	type who struct{ name, company string }
	known := map[string]who{}
	var out []Item

	prows, err := db.QueryContext(ctx, `SELECT lep_no, worker_name, company, work_location, mcu_valid_until, lep_valid_until
		FROM permit WHERE status = ? AND lep_no <> '' ORDER BY lep_no`, permit.StatusApproved)
	if err != nil {
		return nil, fmt.Errorf("read permits: %w", err)
	}
	for prows.Next() {
		var p WirePermit
		var location string
		if err := prows.Scan(&p.Lep, &p.Name, &p.Company, &location, &p.Mcu, &p.PermitValidUntil); err != nil {
			prows.Close()
			return nil, fmt.Errorf("read permit: %w", err)
		}
		p.Status, p.Location = permit.StatusApproved, location
		known[p.Lep] = who{p.Name, p.Company}
		out = append(out, Item{Key: "permit:" + p.Lep, Hash: hashOf(p), Permit: &p})
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("read permits: %w", err)
	}

	wrows, err := db.QueryContext(ctx, `SELECT request_id, visitor_name, company, visit_location, visit_from, visit_to
		FROM waiver WHERE status = ? ORDER BY request_id`, waiver.StatusApproved)
	if err != nil {
		return nil, fmt.Errorf("read waivers: %w", err)
	}
	for wrows.Next() {
		p := WirePermit{Status: waiver.StatusApproved}
		if err := wrows.Scan(&p.Lep, &p.Name, &p.Company, &p.Location, &p.Arrival, &p.Departure); err != nil {
			wrows.Close()
			return nil, fmt.Errorf("read waiver: %w", err)
		}
		p.PermitValidUntil = p.Departure // a visitor's permit ends on the last day of the visit; no MCU rule
		known[p.Lep] = who{p.Name, p.Company}
		out = append(out, Item{Key: "permit:" + p.Lep, Hash: hashOf(p), Permit: &p})
	}
	wrows.Close()
	if err := wrows.Err(); err != nil {
		return nil, fmt.Errorf("read waivers: %w", err)
	}

	trows, err := db.QueryContext(ctx, `SELECT ticket_id, lep_no, status, arrival, departure, record_start, closed_at, closed_reason,
		submitted_at, med_answer, med_details, lep_locked FROM ticket WHERE status IN ('Approved', 'Closed') ORDER BY ticket_id`)
	if err != nil {
		return nil, fmt.Errorf("read tickets: %w", err)
	}
	for trows.Next() {
		var t WireTicket
		var status, recordStart, closedAt, medAnswer string
		var locked int
		if err := trows.Scan(&t.TicketID, &t.Lep, &status, &t.PlannedArrival, &t.PlannedDeparture, &recordStart, &closedAt, &t.CloseReason,
			&t.SubmittedAt, &medAnswer, &t.MedDetails, &locked); err != nil {
			trows.Close()
			return nil, fmt.Errorf("read ticket: %w", err)
		}
		w, ok := known[t.Lep]
		if !ok {
			continue // its permit is not on the gate (not Approved), so the ticket is not either
		}
		t.Name, t.Company = w.name, w.company
		// WHY the lock is pushed: the daily jobs lock a ticket when its permit expires or is
		// renewed (and, if the owner switches it on, at the rotation cap). The gate refuses
		// a check-in on a locked ticket, so a lock that stayed on the Pi would do nothing.
		t.LepLocked = locked == 1
		t.SubmittedAt = isoFromJakarta(t.SubmittedAt)
		t.RecordStart = isoFromJakarta(recordStart)
		t.MedDeclaration = "No"
		if medAnswer == "Y" {
			t.MedDeclaration = "Yes"
		}
		switch {
		case status == "Closed":
			t.Status, t.ClosedAt = "COMPLETED", isoFromJakarta(closedAt)
		case recordStart != "":
			t.Status = "ACTIVE"
		default:
			t.Status = "PLANNED"
		}
		out = append(out, Item{Key: "ticket:" + t.TicketID, Hash: hashOf(t), Ticket: &t})
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, fmt.Errorf("read tickets: %w", err)
	}
	return out, nil
}

// Deactivation is a permit the gate must stop admitting.
type Deactivation struct{ Lep, Status string }

// deactivations lists permits that left Approved (Resigned, Expired, Renewed) AND
// reached the gate, and whose deactivation has not been sent yet. A permit the gate
// never had needs nothing, and sending each one once keeps the history of old
// permits out of every sync.
func deactivations(ctx context.Context, db *sql.DB, pushed map[string]string) ([]Deactivation, error) {
	var out []Deactivation
	collect := func(q string, args ...any) error {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("read deactivations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d Deactivation
			if err := rows.Scan(&d.Lep, &d.Status); err != nil {
				return fmt.Errorf("read deactivation: %w", err)
			}
			if _, reached := pushed["permit:"+d.Lep]; reached && pushed["deact:"+d.Lep] != hashOf(d.Status) {
				out = append(out, d)
			}
		}
		return rows.Err()
	}
	if err := collect(`SELECT lep_no, status FROM permit WHERE lep_no <> '' AND (status IN (?, ?, ?) OR status LIKE ?) ORDER BY lep_no`,
		permit.StatusResigned, permit.StatusExpired, permit.StatusExpiredGrace, "Renewed by %"); err != nil {
		return nil, err
	}
	// A waiver whose visit is over is Completed (the departure job) or Expired; either way
	// the gate must stop admitting it.
	if err := collect(`SELECT request_id, status FROM waiver WHERE status IN (?, ?) ORDER BY request_id`, waiver.StatusExpired, waiver.StatusCompleted); err != nil {
		return nil, err
	}
	return out, nil
}

// references builds the lists the gate page needs, and says what it left out.
// An empty list is never sent: see References.
func (s *Syncer) references(ctx context.Context, pol policy.Policy) (*References, []string, error) {
	var warnings []string
	refs := &References{}

	verifiers, err := s.Officers.Verifiers(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range verifiers {
		refs.Officers = append(refs.Officers, WireOfficer{Name: v.Name, Verifier: v.Hash})
	}
	if len(refs.Officers) == 0 {
		warnings = append(warnings, "no officers to send: the gate keeps its old officer list")
	}

	list := func(key string) ([]string, error) {
		def, _ := reflist.Find(key)
		got, err := s.Lists.Items(ctx, def, false)
		if err != nil {
			return nil, fmt.Errorf("read list %s: %w", key, err)
		}
		var names []string
		for _, it := range got {
			if len(it.Values) > 0 && strings.TrimSpace(it.Values[0]) != "" {
				names = append(names, it.Values[0])
			}
		}
		return names, nil
	}
	if refs.Gates, err = list("gate_name"); err != nil {
		return nil, nil, err
	}
	if len(refs.Gates) == 0 {
		warnings = append(warnings, "no gates to send: the gate keeps its old gate list")
	}
	if refs.Reasons, err = list("checkout_reason"); err != nil {
		return nil, nil, err
	}
	if len(refs.Reasons) == 0 {
		warnings = append(warnings, "no checkout reasons to send: the gate keeps its old list")
	}
	refs.CompletingReasons = pol.CompletingReasons
	refs.Settings = map[string]int{"gate_duplicate_scan_seconds": pol.GateDuplicateSeconds}
	return refs, warnings, nil
}
