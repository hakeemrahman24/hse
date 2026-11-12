package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Run results stored in job_run.
const (
	resultRunning = "running"
	resultOK      = "ok"
	resultPartial = "partial" // finished, but some records were skipped; counts as done for the day
	resultFailed  = "failed"  // the job itself failed (the database was down): tried again later
)

// ErrBusy: the job is running now, so it was not started a second time.
var ErrBusy = errors.New("that job is running right now")

// ErrUnknownJob: no job has that name.
var ErrUnknownJob = errors.New("there is no job with that name")

const maxDetail = 200 // the width of job_run.detail

const (
	retryFailedAfter = 10 // minutes before a failed run is tried again
	takeOverAfter    = 30 // minutes before a run that never finished is taken over
	defaultInterval  = time.Minute
)

// Job is one scheduled task. Hour says, from the current Settings, when it first runs
// each day (Jakarta time).
type Job struct {
	Name string
	Hour func(policy.Policy) int
	Run  func(ctx context.Context) (Outcome, error)
}

// Scheduler runs jobs once a day each, in the order listed, at or after their hour.
//
// WHY it is a check every minute and not a sleep until 06:00: a restart, a missed hour or
// a changed setting needs no special case. Every minute it asks "is this job due and not
// yet done today?", and job_run answers (one row per job per day).
type Scheduler struct {
	DB       *sql.DB
	Clock    dates.Clock
	Policy   func() policy.Policy
	Jobs     []Job
	Interval time.Duration // 0 means one minute
	Logf     func(format string, args ...any)
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Run ticks until ctx ends. A tick that fails is logged and the next one tries again.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	for {
		if err := s.Tick(ctx); err != nil {
			s.logf("scheduler: %v", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Tick runs every job that is due and has not been done today. A job that fails or
// panics is recorded and does not stop the next one; the returned error is only about
// being unable to read or write the run log.
func (s *Scheduler) Tick(ctx context.Context) error {
	now, pol := s.Clock.Now(), s.Policy()
	var errs []error
	for _, j := range s.Jobs {
		if hourOf(now) < j.Hour(pol) {
			continue
		}
		claimed, err := s.claim(ctx, j.Name, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !claimed {
			continue
		}
		result, _, detail := s.run(ctx, j)
		if err := s.finish(ctx, j.Name, now.Day(), result, detail); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// run executes one job and turns whatever happens into a result word, the counts, and
// a short line for the status page.
func (s *Scheduler) run(ctx context.Context, j Job) (result string, out Outcome, detail string) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("scheduler: job %s panicked: %v", j.Name, r)
			result, out, detail = resultFailed, Outcome{}, clip(fmt.Sprintf("failed: panic: %v", r))
		}
	}()
	out, err := j.Run(ctx)
	switch {
	case err != nil:
		s.logf("scheduler: job %s failed: %v", j.Name, err)
		return resultFailed, out, clip("failed: " + err.Error())
	case out.Errors > 0:
		s.logf("scheduler: job %s finished: %d changed, %d records skipped", j.Name, out.Changed, out.Errors)
		return resultPartial, out, fmt.Sprintf("%d changed, %d skipped", out.Changed, out.Errors)
	}
	s.logf("scheduler: job %s finished: %d changed", j.Name, out.Changed)
	return resultOK, out, fmt.Sprintf("%d changed", out.Changed)
}

// clip keeps a message inside the column.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDetail {
		return s[:maxDetail-3] + "..."
	}
	return s
}

// claim takes today's run of a job, or says someone else has it or has finished it.
//
// WHY it is a row, not a lock: the row is the record that the job ran today, so a restart,
// a second process, or two ticks at once all agree. The primary key (job, day) lets only
// one insert win.
func (s *Scheduler) claim(ctx context.Context, name string, now dates.Moment) (bool, error) {
	day := string(now.Day())
	var result, at string
	err := s.DB.QueryRowContext(ctx, `SELECT result, at FROM job_run WHERE job_name = ? AND run_day = ?`, name, day).Scan(&result, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, ierr := s.DB.ExecContext(ctx, `INSERT INTO job_run (job_name, run_day, result, at) VALUES (?, ?, ?, ?)`, name, day, resultRunning, string(now))
		if ierr == nil {
			return true, nil
		}
		// An insert can fail because another tick won the race a moment ago: not an error.
		if exists, qerr := s.exists(ctx, name, day); qerr == nil && exists {
			return false, nil
		}
		return false, fmt.Errorf("claim job %s: %w", name, ierr)
	case err != nil:
		return false, fmt.Errorf("read run of job %s: %w", name, err)
	}

	retake := false
	switch result {
	case resultRunning: // a run that never wrote its result: the process died
		retake = dates.Moment(at).AddMinutes(takeOverAfter) <= now
	case resultFailed:
		retake = dates.Moment(at).AddMinutes(retryFailedAfter) <= now
	}
	if !retake {
		return false, nil
	}
	return s.retake(ctx, name, day, result, at, now)
}

// retake moves a run back to "running". Only one caller can change the row from the state
// it just read, so two ticks, or a tick and "Run now", never both start the job.
func (s *Scheduler) retake(ctx context.Context, name, day, result, at string, now dates.Moment) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE job_run SET result = ?, at = ?, detail = '' WHERE job_name = ? AND run_day = ? AND result = ? AND at = ?`,
		resultRunning, string(now), name, day, result, at)
	if err != nil {
		return false, fmt.Errorf("retake job %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("retake job %s: %w", name, err)
	}
	return n == 1, nil
}

func (s *Scheduler) exists(ctx context.Context, name, day string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_run WHERE job_name = ? AND run_day = ?`, name, day).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// finish records how a run ended, with the time it ended (a failed run is retried a
// while after that) and the short line for the status page.
func (s *Scheduler) finish(ctx context.Context, name string, day dates.Day, result, detail string) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE job_run SET result = ?, at = ?, detail = ? WHERE job_name = ? AND run_day = ?`,
		result, string(s.Clock.Now()), detail, name, string(day)); err != nil {
		return fmt.Errorf("record run of job %s: %w", name, err)
	}
	return nil
}

// RunRecord is the latest run of one job, for the status page. A job that has never run
// has an empty Day and Result.
type RunRecord struct {
	Name, Day, Result, At, Detail string
	Hour                          int // the hour of day the job is due, from the current Settings
}

// Runs lists the latest run of each job, in the order the jobs are scheduled.
func (s *Scheduler) Runs(ctx context.Context) ([]RunRecord, error) {
	out := make([]RunRecord, 0, len(s.Jobs))
	for _, j := range s.Jobs {
		rec := RunRecord{Name: j.Name, Hour: j.Hour(s.Policy())}
		err := s.DB.QueryRowContext(ctx, `SELECT run_day, result, at, detail FROM job_run WHERE job_name = ? ORDER BY run_day DESC LIMIT 1`, j.Name).
			Scan(&rec.Day, &rec.Result, &rec.At, &rec.Detail)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read runs of job %s: %w", j.Name, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// RunResult is what "Run now" reports.
type RunResult struct {
	Result, Detail string
	Outcome        Outcome
}

// RunNow runs one job at once, whatever the hour and whether or not today's run is done
// (the admin's button). Every job is safe to run again, so this changes nothing a second
// run would not. It refuses a job that is running right now, and one that started under
// half an hour ago; an older "running" row is a dead process and is taken over.
func (s *Scheduler) RunNow(ctx context.Context, name string) (RunResult, error) {
	var job *Job
	for i := range s.Jobs {
		if s.Jobs[i].Name == name {
			job = &s.Jobs[i]
		}
	}
	if job == nil {
		return RunResult{}, ErrUnknownJob
	}
	now := s.Clock.Now()
	taken, err := s.take(ctx, name, now)
	if err != nil {
		return RunResult{}, err
	}
	if !taken {
		return RunResult{}, ErrBusy
	}
	result, out, detail := s.run(ctx, *job)
	if err := s.finish(ctx, name, now.Day(), result, detail); err != nil {
		return RunResult{}, err
	}
	return RunResult{Result: result, Detail: detail, Outcome: out}, nil
}

// take claims today's row for a manual run. Unlike claim it also takes a row that is
// finished, because the point of "Run now" is to run again.
func (s *Scheduler) take(ctx context.Context, name string, now dates.Moment) (bool, error) {
	day := string(now.Day())
	var result, at string
	err := s.DB.QueryRowContext(ctx, `SELECT result, at FROM job_run WHERE job_name = ? AND run_day = ?`, name, day).Scan(&result, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s.claim(ctx, name, now) // nobody has it today: the same insert as the schedule
	case err != nil:
		return false, fmt.Errorf("read run of job %s: %w", name, err)
	case result == resultRunning && dates.Moment(at).AddMinutes(takeOverAfter) > now:
		return false, nil
	}
	return s.retake(ctx, name, day, result, at, now)
}

// hourOf reads the hour of a moment ("2026-11-10 06:05:00" is 6).
func hourOf(m dates.Moment) int {
	if len(m) < 13 {
		return 0
	}
	h, err := strconv.Atoi(string(m[11:13]))
	if err != nil {
		return 0
	}
	return h
}
