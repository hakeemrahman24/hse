package gatesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/policy"
	"entry-permit/internal/reflist"
)

const (
	jobName          = "gate_sync"
	defaultChunk     = 40  // items per push call; the Worker applies a call as one batch
	deactivateChunk  = 100 // permits per deactivate call
	pullLimit        = 100
	maxPullsPerRun   = 20 // bounds one run if the gate has a big backlog
	refsRefreshAfter = 60 // minutes: lists are re-sent at least this often, so a wiped D1 gets them back
	maxDetailLength  = 500
)

// Syncer runs the gate sync.
type Syncer struct {
	DB       *sql.DB
	Client   *Client
	Officers *gateofficer.Store
	Lists    *reflist.Store
	Clock    dates.Clock
	Policy   func() policy.Policy
	Chunk    int // items per push call; 0 means 40

	kickOnce sync.Once
	kickCh   chan struct{}
}

// Report says what one run did.
type Report struct {
	PermitsPushed, TicketsPushed, Deactivated int
	RefsPushed                                bool
	LogsApplied, StatesApplied                int
	TicketsApplied, TicketsSkipped            int
	Warnings                                  []string
}

func (s *Syncer) kicks() chan struct{} {
	s.kickOnce.Do(func() { s.kickCh = make(chan struct{}, 1) })
	return s.kickCh
}

// Kick asks for a sync now, without waiting for the next tick. It is used when a
// permit is approved, so the worker is not refused at the gate for minutes. Kicks
// while one is already waiting are dropped: one extra run covers them all.
func (s *Syncer) Kick() {
	select {
	case s.kicks() <- struct{}{}:
	default:
	}
}

// Run syncs once at start and then every gate_sync_minutes, or sooner on Kick,
// until ctx ends. A failed run is logged and tried again at the next tick: the
// gate keeps working on what it has, and nothing is lost.
func (s *Syncer) Run(ctx context.Context) {
	kick := s.kicks()
	for {
		if _, err := s.RunOnce(ctx); err != nil {
			log.Printf("gate sync: %v", err)
		}
		minutes := s.Policy().GateSyncMinutes
		if minutes < 1 {
			minutes = 1
		}
		timer := time.NewTimer(time.Duration(minutes) * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-kick:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// RunOnce does one full sync and records it in gate_sync_run.
//
// WHY the order: first read what the gate changed on tickets, then push. The gate
// owns a ticket's first check-in and closing; pushing first could send a stale copy
// of a ticket the gate has just started. (The Worker also refuses to undo them.)
// If that first read fails the push is skipped for the same reason.
func (s *Syncer) RunOnce(ctx context.Context) (Report, error) {
	var rep Report
	var errs []error
	pol := s.Policy()

	ticketErr := s.pullTickets(ctx, &rep)
	if ticketErr != nil {
		errs = append(errs, ticketErr)
		rep.Warnings = append(rep.Warnings, "push skipped: the gate's ticket changes could not be read first")
	} else if err := s.push(ctx, pol, &rep); err != nil {
		errs = append(errs, err)
	}
	if err := s.pullState(ctx, &rep); err != nil {
		errs = append(errs, err)
	}
	if err := s.pullLog(ctx, &rep); err != nil {
		errs = append(errs, err)
	}

	err := errors.Join(errs...)
	if recErr := s.record(ctx, rep, err); recErr != nil {
		err = errors.Join(err, recErr)
	}
	return rep, err
}

func (s *Syncer) applier() applier { return applier{db: s.DB, clock: s.Clock} }

// ── push ─────────────────────────────────────────────────────────────────────

func (s *Syncer) pushed(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT item_key, hash FROM gate_push`)
	if err != nil {
		return nil, fmt.Errorf("read push marks: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, h string
		if err := rows.Scan(&k, &h); err != nil {
			return nil, fmt.Errorf("read push mark: %w", err)
		}
		out[k] = h
	}
	return out, rows.Err()
}

// mark records what the Worker accepted. It is called only after a call succeeded,
// so a failed call leaves its items "not sent" and the next run sends them again.
func (s *Syncer) mark(ctx context.Context, marks map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin marks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := string(s.Clock.Now())
	for k, h := range marks {
		res, err := tx.ExecContext(ctx, `UPDATE gate_push SET hash = ?, pushed_at = ? WHERE item_key = ?`, h, now, k)
		if err != nil {
			return fmt.Errorf("mark %s: %w", k, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO gate_push (item_key, hash, pushed_at) VALUES (?, ?, ?)`, k, h, now); err != nil {
				return fmt.Errorf("mark %s: %w", k, err)
			}
		}
	}
	return tx.Commit()
}

func (s *Syncer) push(ctx context.Context, pol policy.Policy, rep *Report) error {
	pushed, err := s.pushed(ctx)
	if err != nil {
		return err
	}
	all, err := items(ctx, s.DB)
	if err != nil {
		return err
	}
	var changed []Item
	for _, it := range all {
		if pushed[it.Key] != it.Hash {
			changed = append(changed, it)
		}
	}

	chunk := s.Chunk
	if chunk <= 0 {
		chunk = defaultChunk
	}
	for i := 0; i < len(changed); i += chunk {
		end := min(i+chunk, len(changed))
		var body PushBody
		marks := map[string]string{}
		permits, tickets := 0, 0
		for _, it := range changed[i:end] {
			marks[it.Key] = it.Hash
			if it.Permit != nil {
				body.Permits = append(body.Permits, *it.Permit)
				permits++
			} else {
				body.TripTickets = append(body.TripTickets, *it.Ticket)
				tickets++
			}
		}
		if _, err := s.Client.Push(ctx, body); err != nil {
			return err // later chunks are not tried; this one stays unsent
		}
		if err := s.mark(ctx, marks); err != nil {
			return err
		}
		rep.PermitsPushed += permits
		rep.TicketsPushed += tickets
	}

	if err := s.deactivate(ctx, pushed, rep); err != nil {
		return err
	}
	return s.pushReferences(ctx, pol, pushed["refs"], rep)
}

func (s *Syncer) deactivate(ctx context.Context, pushed map[string]string, rep *Report) error {
	list, err := deactivations(ctx, s.DB, pushed)
	if err != nil {
		return err
	}
	byStatus := map[string][]string{}
	var order []string
	for _, d := range list {
		if _, seen := byStatus[d.Status]; !seen {
			order = append(order, d.Status)
		}
		byStatus[d.Status] = append(byStatus[d.Status], d.Lep)
	}
	for _, status := range order {
		leps := byStatus[status]
		for i := 0; i < len(leps); i += deactivateChunk {
			part := leps[i:min(i+deactivateChunk, len(leps))]
			if err := s.Client.Deactivate(ctx, status, part); err != nil {
				return err
			}
			marks := map[string]string{}
			for _, lep := range part {
				marks["deact:"+lep] = hashOf(status)
			}
			if err := s.mark(ctx, marks); err != nil {
				return err
			}
			rep.Deactivated += len(part)
		}
	}
	return nil
}

func (s *Syncer) pushReferences(ctx context.Context, pol policy.Policy, lastHash string, rep *Report) error {
	refs, warnings, err := s.references(ctx, pol)
	if err != nil {
		return err
	}
	rep.Warnings = append(rep.Warnings, warnings...)
	hash := hashOf(refs)

	due := lastHash != hash
	if !due {
		var at string
		if err := s.DB.QueryRowContext(ctx, `SELECT pushed_at FROM gate_push WHERE item_key = 'refs'`).Scan(&at); err == nil {
			due = dates.Moment(at).AddMinutes(refsRefreshAfter) <= s.Clock.Now()
		}
	}
	if !due {
		return nil
	}
	res, err := s.Client.Push(ctx, PushBody{References: refs})
	if err != nil {
		return err
	}
	if len(res.Skipped) > 0 {
		rep.Warnings = append(rep.Warnings, "the gate skipped empty lists: "+strings.Join(res.Skipped, ", "))
	}
	if err := s.mark(ctx, map[string]string{"refs": hash}); err != nil {
		return err
	}
	rep.RefsPushed = true
	return nil
}

// ── pull ─────────────────────────────────────────────────────────────────────

// pullLoop pulls, applies and acks in rounds. The ack is sent only after the rows
// are committed, so a crash in between just means the same rows come back.
func pullLoop[T any](ctx context.Context, pull func() ([]T, error), apply func([]T) error, ack func([]T) error) error {
	for i := 0; i < maxPullsPerRun; i++ {
		rows, err := pull()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := apply(rows); err != nil {
			return err
		}
		if err := ack(rows); err != nil {
			return err
		}
		if len(rows) < pullLimit {
			return nil
		}
	}
	return nil
}

func (s *Syncer) pullTickets(ctx context.Context, rep *Report) error {
	return pullLoop(ctx,
		func() ([]TicketRow, error) { return s.Client.PullTickets(ctx, pullLimit) },
		func(rows []TicketRow) error {
			applied, skipped, err := s.applier().ticketRows(ctx, rows)
			rep.TicketsApplied += applied
			rep.TicketsSkipped += skipped
			return err
		},
		func(rows []TicketRow) error {
			ids := make([]string, len(rows))
			for i, r := range rows {
				ids[i] = r.TicketID
			}
			return s.Client.AckTickets(ctx, ids)
		})
}

func (s *Syncer) pullState(ctx context.Context, rep *Report) error {
	return pullLoop(ctx,
		func() ([]StateRow, error) { return s.Client.PullState(ctx, pullLimit) },
		func(rows []StateRow) error {
			n, err := s.applier().stateRows(ctx, rows)
			rep.StatesApplied += n
			return err
		},
		func(rows []StateRow) error {
			leps := make([]string, len(rows))
			for i, r := range rows {
				leps[i] = r.Lep
			}
			return s.Client.AckState(ctx, leps)
		})
}

func (s *Syncer) pullLog(ctx context.Context, rep *Report) error {
	return pullLoop(ctx,
		func() ([]LogRow, error) { return s.Client.PullLog(ctx, pullLimit) },
		func(rows []LogRow) error {
			n, err := s.applier().logRows(ctx, rows)
			rep.LogsApplied += n
			return err
		},
		func(rows []LogRow) error {
			ids := make([]int64, len(rows))
			for i, r := range rows {
				ids[i] = r.ID
			}
			return s.Client.AckLog(ctx, ids)
		})
}

// ── status ───────────────────────────────────────────────────────────────────

func (s *Syncer) record(ctx context.Context, rep Report, runErr error) error {
	detail := fmt.Sprintf("pushed %d permits, %d tickets, %d deactivations, lists %t; pulled %d scans, %d states, %d ticket updates (%d unknown)",
		rep.PermitsPushed, rep.TicketsPushed, rep.Deactivated, rep.RefsPushed, rep.LogsApplied, rep.StatesApplied, rep.TicketsApplied, rep.TicketsSkipped)
	if len(rep.Warnings) > 0 {
		detail += "; " + strings.Join(rep.Warnings, "; ")
	}
	if runErr != nil {
		detail += "; ERROR: " + strings.ReplaceAll(runErr.Error(), "\n", " | ")
	}
	ok := 0
	if runErr == nil {
		ok = 1
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO gate_sync_run (job, ran_at, ok, detail) VALUES (?, ?, ?, ?)`,
		jobName, string(s.Clock.Now()), ok, clipRunes(detail, maxDetailLength)); err != nil {
		return fmt.Errorf("record sync run: %w", err)
	}
	return nil
}

// LastSuccess is the Jakarta moment of the last run that finished without an error,
// or "" if none has. The admin page shows it and warns when it is old.
func LastSuccess(ctx context.Context, db *sql.DB) (string, error) {
	var at string
	err := db.QueryRowContext(ctx, `SELECT ran_at FROM gate_sync_run WHERE job = ? AND ok = 1 ORDER BY id DESC LIMIT 1`, jobName).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read last sync: %w", err)
	}
	return at, nil
}
