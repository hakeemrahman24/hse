package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiverlog"
)

// Where each kind of row is recorded in import_row.
const (
	sourceWEP    = "wep"
	sourceWaiver = "waiver"
	sourceTicket = "ticket"
)

// maxExamples is how many request ids a warning group lists.
const maxExamples = 3

// Options controls a run.
type Options struct {
	HeaderRows int // rows above the first data row (the sheet has 4)
	DryRun     bool
	Clock      dates.Clock

	// FillValidUntil computes the LEP valid-until day of a permit whose sheet cell is empty,
	// by the rule Final approval uses and with Policy. Off by default: it is a decision about
	// real permits, so the owner makes it with the dry-run report in hand.
	FillValidUntil bool
	Policy         policy.Policy
}

// WarnGroup is one kind of non-fatal problem, counted, with a few example request ids.
type WarnGroup struct {
	Count    int
	Examples []string
	Msg      string
}

// Report says what a run did (or, for a dry run, would do) for one sheet. It names request
// ids and fields, never a name, a birth date or an address.
type Report struct {
	DryRun                                 bool
	Rows                                   int // data rows read, blank ones included
	Inserted, Replaced, Unchanged, Skipped int
	Rejected                               []Problem // rows not imported, with the reason
	Warnings                               map[string]WarnGroup
	ByStatus                               map[string]int // statuses of the rows that are in (or went in)
}

// SheetReport is the report of one sheet, with the name it is printed under.
type SheetReport struct {
	Name string
	Report
}

// Book is the sheets to import. A nil sheet is skipped.
type Book struct{ Permits, Waivers, Tickets [][]string }

// item is one sheet row ready to store, whatever sheet it came from. Everything that differs
// between a permit, a waiver and a ticket is in these functions, so the dry run, the
// idempotency and the "changed in the new system" check are written once.
type item struct {
	Source, ID, Hash, Status string
	Keys                     []key // values that must be unique within the sheet
	// Conflict says why a NEW row cannot be stored (something native has its id), or "".
	Conflict func(ctx context.Context, tx *sql.Tx) (string, error)
	Insert   func(ctx context.Context, tx *sql.Tx) error
	Remove   func(ctx context.Context, tx *sql.Tx) error
	// State fingerprints what the new system can change on the stored row.
	State func(ctx context.Context, tx *sql.Tx) (string, error)
}

type key struct{ kind, value string }

// mapper reads one sheet row. It may look in the open transaction (a ticket takes its address
// from its permit, which this same run has just stored).
type mapper func(ctx context.Context, tx *sql.Tx, row []string, rowNo int) (item, []Problem, error)

// Run imports the WorkEntryPermitLog rows only. See RunAll.
func Run(ctx context.Context, d *sql.DB, rows [][]string, opt Options) (Report, error) {
	reps, err := RunAll(ctx, d, Book{Permits: rows}, opt)
	if len(reps) == 0 {
		return Report{}, err
	}
	return reps[0].Report, err
}

// RunAll imports permits, then waivers, then tickets (a ticket needs its permit or waiver).
// Everything happens in one transaction: a real run commits it, a dry run rolls it back, so
// the dry-run report is exactly what a real run would do. A row that cannot be imported is
// reported and skipped; a database error stops the run and writes nothing.
func RunAll(ctx context.Context, d *sql.DB, b Book, opt Options) ([]SheetReport, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("start import: %w", err)
	}
	defer tx.Rollback() // a no-op after Commit

	now := opt.Clock.Now()
	var out []SheetReport
	for _, sh := range []struct {
		name string
		rows [][]string
		mp   mapper
	}{
		{"Work permits", b.Permits, permitMapper(opt, permitlog.Store{DB: d})},
		{"Waivers", b.Waivers, waiverMapper()},
		{"Tickets", b.Tickets, ticketMapper()},
	} {
		rep, err := runSheet(ctx, tx, sh.rows, opt, sh.mp, now)
		out = append(out, SheetReport{Name: sh.name, Report: rep})
		if err != nil {
			return out, err
		}
	}
	if opt.DryRun {
		return out, nil // the deferred Rollback discards everything
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit import: %w", err)
	}
	return out, nil
}

func runSheet(ctx context.Context, tx *sql.Tx, rows [][]string, opt Options, mp mapper, now dates.Moment) (Report, error) {
	rep := Report{DryRun: opt.DryRun, Warnings: map[string]WarnGroup{}, ByStatus: map[string]int{}}
	seen := map[key]bool{}
	for i := opt.HeaderRows; i < len(rows); i++ {
		rep.Rows++
		if blankRow(rows[i]) {
			rep.Skipped++
			continue
		}
		it, problems, err := mp(ctx, tx, rows[i], i+1)
		if err != nil {
			return rep, err
		}
		if reasons := fatalReasons(problems); len(reasons) > 0 {
			rep.Rejected = append(rep.Rejected, Problem{Row: i + 1, ID: it.ID, Msg: strings.Join(reasons, "; ")})
			continue
		}
		if why := duplicateInSheet(it.Keys, seen); why != "" {
			rep.Rejected = append(rep.Rejected, Problem{Row: i + 1, ID: it.ID, Msg: why})
			continue
		}
		result, why, err := apply(ctx, tx, it, now)
		if err != nil {
			return rep, err
		}
		switch result {
		case outInserted:
			rep.Inserted++
		case outReplaced:
			rep.Replaced++
		case outUnchanged:
			rep.Unchanged++
		case outRejected:
			rep.Rejected = append(rep.Rejected, Problem{Row: i + 1, ID: it.ID, Msg: why})
			continue
		}
		rep.ByStatus[it.Status]++
		rep.warn(problems)
	}
	return rep, nil
}

func blankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func fatalReasons(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		if p.Fatal {
			out = append(out, p.Msg)
		}
	}
	return out
}

// duplicateInSheet keeps the first row of a repeated id, LEP number or ticket id.
func duplicateInSheet(keys []key, seen map[key]bool) string {
	for _, k := range keys {
		if seen[k] {
			return fmt.Sprintf("%s %s appears again in the sheet; only its first row is imported", k.kind, k.value)
		}
	}
	for _, k := range keys {
		seen[k] = true
	}
	return ""
}

func (r *Report) warn(ps []Problem) {
	for _, p := range ps {
		if p.Fatal {
			continue
		}
		g := r.Warnings[p.Field]
		g.Count++
		g.Msg = p.Msg
		if len(g.Examples) < maxExamples {
			g.Examples = append(g.Examples, p.ID)
		}
		r.Warnings[p.Field] = g
	}
}

// statusGroup folds every "Renewed by LEP-xxxxx" into one line of the report.
func statusGroup(s string) string {
	if strings.HasPrefix(s, "Renewed by ") {
		return "Renewed by ..."
	}
	return s
}

type outcome int

const (
	outInserted outcome = iota
	outReplaced
	outUnchanged
	outRejected
)

// apply stores one mapped row.
//
// WHY the "state" check: a re-run during the freeze window must not undo what the new system
// has done since the first import. After each import the row's state (what the reviews, the
// gate and the daily jobs can change) is fingerprinted; if it differs at the next run, someone
// or something has acted on the row in the new system and the sheet row is refused, not applied.
func apply(ctx context.Context, tx *sql.Tx, it item, now dates.Moment) (outcome, string, error) {
	var oldHash, oldState string
	err := tx.QueryRowContext(ctx, `SELECT hash, state FROM import_row WHERE source = ? AND source_id = ?`, it.Source, it.ID).Scan(&oldHash, &oldState)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("read import record: %w", err)
	}
	switch {
	case known && oldHash == it.Hash:
		return outUnchanged, "", nil
	case known:
		current, err := it.State(ctx, tx)
		if err != nil {
			return 0, "", err
		}
		if current != oldState {
			return outRejected, "this row was changed in the new system since the import, so the sheet row is not applied", nil
		}
		if err := it.Remove(ctx, tx); err != nil {
			return 0, "", err
		}
	default:
		why, err := it.Conflict(ctx, tx)
		if err != nil {
			return 0, "", err
		}
		if why != "" {
			return outRejected, why, nil
		}
	}
	if err := it.Insert(ctx, tx); err != nil {
		return 0, "", err
	}
	state, err := it.State(ctx, tx)
	if err != nil {
		return 0, "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM import_row WHERE source = ? AND source_id = ?`, it.Source, it.ID); err != nil {
		return 0, "", fmt.Errorf("update import record: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO import_row (source, source_id, hash, state, at) VALUES (?, ?, ?, ?, ?)`,
		it.Source, it.ID, it.Hash, state, string(now)); err != nil {
		return 0, "", fmt.Errorf("record import of %s: %w", it.ID, err)
	}
	if known {
		return outReplaced, "", nil
	}
	return outInserted, "", nil
}

// ---- permits ----

func permitMapper(opt Options, store permitlog.Store) mapper {
	return func(_ context.Context, _ *sql.Tx, row []string, rowNo int) (item, []Problem, error) {
		m, problems := MapWEP(row, rowNo)
		if opt.FillValidUntil {
			m, problems = fillValidUntil(m, problems, row, opt.Policy)
		}
		id, lep := m.Permit.RequestID, m.Permit.LepNo
		keys := []key{{"request id", id}}
		if lep != "" {
			keys = append(keys, key{"LEP number", lep})
		}
		return item{
			Source: sourceWEP, ID: id, Hash: m.Hash, Status: statusGroup(m.Permit.Status), Keys: keys,
			Conflict: func(ctx context.Context, tx *sql.Tx) (string, error) {
				if exists, err := rowExists(ctx, tx, `permit`, `request_id`, id); err != nil || exists {
					return "a permit with this request id already exists in the new system (it was not imported)", err
				}
				if lep != "" {
					if exists, err := rowExists(ctx, tx, `permit`, `lep_no`, lep); err != nil || exists {
						return fmt.Sprintf("LEP number %s already belongs to another permit in the new system", lep), err
					}
				}
				return "", nil
			},
			Insert: func(ctx context.Context, tx *sql.Tx) error {
				if err := store.CreateIn(ctx, tx, m.Permit); err != nil {
					return err
				}
				return insertSteps(ctx, tx, id, m.Steps)
			},
			Remove: func(ctx context.Context, tx *sql.Tx) error {
				return removeRows(ctx, tx, id, `DELETE FROM review_step WHERE subject_id = ?`, `DELETE FROM permit WHERE request_id = ?`)
			},
			State: func(ctx context.Context, tx *sql.Tx) (string, error) {
				// what a review or the expiry job can change: status, LEP number, the review decisions
				return fingerprint(ctx, tx, id, `SELECT status || '|' || lep_no FROM permit WHERE request_id = ?`)
			},
		}, problems, nil
	}
}

// ---- waivers ----

func waiverMapper() mapper {
	return func(ctx context.Context, tx *sql.Tx, row []string, rowNo int) (item, []Problem, error) {
		m, problems := MapWaiver(row, rowNo)
		id := m.Waiver.RequestID
		var companyID int64
		err := tx.QueryRowContext(ctx, `SELECT company_id FROM company WHERE LOWER(name) = LOWER(?)`, m.Waiver.Company).Scan(&companyID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			problems = append(problems, Problem{Row: rowNo, ID: id, Field: "company_id",
				Msg: "the company is not in the company list copied from medpost; the waiver is imported with company 0 and a re-run after the next company sync links it"})
		case err != nil:
			return item{}, nil, fmt.Errorf("look up company: %w", err)
		}
		m.Waiver.CompanyID = companyID
		m.Hash = hashOf(struct {
			W waiverlog.NewWaiver
			S []Step
		}{m.Waiver, m.Steps})
		return item{
			Source: sourceWaiver, ID: id, Hash: m.Hash, Status: m.Waiver.Status, Keys: []key{{"request id", id}},
			Conflict: func(ctx context.Context, tx *sql.Tx) (string, error) {
				exists, err := rowExists(ctx, tx, `waiver`, `request_id`, id)
				if exists {
					return "a waiver with this request id already exists in the new system (it was not imported)", err
				}
				return "", err
			},
			Insert: func(ctx context.Context, tx *sql.Tx) error {
				if err := waiverlog.Insert(ctx, tx, m.Waiver); err != nil {
					return err
				}
				return insertSteps(ctx, tx, id, m.Steps)
			},
			Remove: func(ctx context.Context, tx *sql.Tx) error {
				return removeRows(ctx, tx, id, `DELETE FROM review_step WHERE subject_id = ?`, `DELETE FROM waiver WHERE request_id = ?`)
			},
			State: func(ctx context.Context, tx *sql.Tx) (string, error) {
				return fingerprint(ctx, tx, id, `SELECT status FROM waiver WHERE request_id = ?`)
			},
		}, problems, nil
	}
}

// ---- tickets ----

func ticketMapper() mapper {
	return func(ctx context.Context, tx *sql.Tx, row []string, rowNo int) (item, []Problem, error) {
		m, problems := MapTicket(row, rowNo)
		id := m.Ticket.ID
		// A ticket has no address in the sheet: it uses its permit's or waiver's, and cannot be
		// stored at all when that permit or waiver is not in the new system.
		table, keyCol, what := `permit`, `lep_no`, "permit"
		if !m.Ticket.Work {
			table, keyCol, what = `waiver`, `request_id`, "waiver"
		}
		var email string
		err := tx.QueryRowContext(ctx, `SELECT requester_email FROM `+table+` WHERE `+keyCol+` = ?`, m.Ticket.LEP).Scan(&email)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			problems = append(problems, Problem{Row: rowNo, ID: id, Field: "permit_no", Fatal: true,
				Msg: fmt.Sprintf("its %s %s was not imported, so the ticket has nothing to belong to", what, m.Ticket.LEP)})
		case err != nil:
			return item{}, nil, fmt.Errorf("look up the %s of ticket %s: %w", what, id, err)
		}
		m.Ticket.RequesterEmail = email
		return item{
			Source: sourceTicket, ID: id, Hash: m.Hash, Status: m.Ticket.Status, Keys: []key{{"ticket id", id}},
			Conflict: func(ctx context.Context, tx *sql.Tx) (string, error) {
				exists, err := rowExists(ctx, tx, `ticket`, `ticket_id`, id)
				if exists {
					return "a ticket with this id already exists in the new system (it was not imported)", err
				}
				return "", err
			},
			Insert: func(ctx context.Context, tx *sql.Tx) error { return ticketlog.Insert(ctx, tx, m.Ticket) },
			Remove: func(ctx context.Context, tx *sql.Tx) error {
				return removeRows(ctx, tx, id, `DELETE FROM ticket WHERE ticket_id = ?`)
			},
			State: func(ctx context.Context, tx *sql.Tx) (string, error) {
				// what the gate sync and the daily jobs can change on a ticket
				return fingerprint(ctx, tx, id, `SELECT status || '|' || record_start || '|' || closed_at || '|' || lep_locked || '|' || weeks_on_site ||
					'|' || reminder_notified || '|' || week_n1_sent_at || '|' || week_n0_locked_at FROM ticket WHERE ticket_id = ?`)
			},
		}, problems, nil
	}
}

// ---- shared database helpers ----

func rowExists(ctx context.Context, tx *sql.Tx, table, col, val string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, val).Scan(&n); err != nil {
		return false, fmt.Errorf("check existing %s: %w", table, err)
	}
	return n > 0, nil
}

func insertSteps(ctx context.Context, tx *sql.Tx, id string, steps []Step) error {
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO review_step (subject_id, stage, reviewer, decision, note, at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, s.Stage, s.Reviewer, s.Decision, s.Note, s.At); err != nil {
			return fmt.Errorf("store review step of %s: %w", id, err)
		}
	}
	return nil
}

// removeRows clears an imported row, so the changed sheet row can be stored in its place within
// the same transaction.
func removeRows(ctx context.Context, tx *sql.Tx, id string, stmts ...string) error {
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("replace %s: %w", id, err)
		}
	}
	return nil
}

// fingerprint joins a one-value query about the row with the number of review decisions it has.
func fingerprint(ctx context.Context, tx *sql.Tx, id, query string) (string, error) {
	var state string
	if err := tx.QueryRowContext(ctx, query, id).Scan(&state); err != nil {
		return "", fmt.Errorf("read state of %s: %w", id, err)
	}
	var steps int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM review_step WHERE subject_id = ?`, id).Scan(&steps); err != nil {
		return "", fmt.Errorf("count review steps of %s: %w", id, err)
	}
	return fmt.Sprintf("%s|%d", state, steps), nil
}

// fillValidUntil replaces the "valid-until is missing" refusal with a computed day, when the
// cell is empty (not when it holds an unreadable date, which is a typo someone should look at)
// and the rule has dates to work from. The result is reported as a warning.
func fillValidUntil(m Mapped, problems []Problem, row []string, pol policy.Policy) (Mapped, []Problem) {
	if m.Permit.LepValidUntil != "" || !mustHaveEnd(m.Permit.Status) || !Blank(cell(row, colLepUntil)) {
		return m, problems
	}
	in := permit.ValidityInput{
		Foreign:       !strings.EqualFold(m.Permit.Nationality, "indonesia"),
		McuValidUntil: dates.Day(m.Permit.McuValidUntil), ContractValidUntil: dates.Day(m.Permit.ContractUntil), PassportValidUntil: dates.Day(m.Permit.PassportUntil),
	}
	switch m.Permit.VisaType {
	case permit.VisaKindVisa:
		in.Visa = dates.Day(m.Permit.VisaExpiry)
	case permit.VisaKindItas:
		in.Itas = dates.Day(m.Permit.VisaExpiry)
	case permit.VisaKindItap:
		in.Itap = dates.Day(m.Permit.VisaExpiry)
	}
	day, _, err := permit.ResolveValidity(pol, in)
	if err != nil {
		// the refusal stays, with the rule's own reason added so the owner knows what to fix
		return m, append(problems, Problem{Row: firstRow(problems), ID: m.Permit.RequestID, Field: "lep_valid_until", Msg: err.Error(), Fatal: true})
	}
	var kept []Problem
	for _, p := range problems {
		if !(p.Fatal && p.Field == "lep_valid_until") {
			kept = append(kept, p)
		}
	}
	m.Permit.LepValidUntil = string(day)
	m.Hash = hash(m)
	return m, append(kept, Problem{Row: firstRow(problems), ID: m.Permit.RequestID, Field: "lep_valid_until_computed",
		Msg: "the sheet had no valid-until date; it was computed by the rule Final approval uses (the earliest of the MCU, contract, visa and passport dates)"})
}

func firstRow(ps []Problem) int {
	if len(ps) > 0 {
		return ps[0].Row
	}
	return 0
}

// SortedWarnings lists the warning fields in a stable order, for printing.
func (r Report) SortedWarnings() []string {
	keys := make([]string, 0, len(r.Warnings))
	for k := range r.Warnings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
