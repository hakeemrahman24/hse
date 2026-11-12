package jobs

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/policy"
	"entry-permit/migrations"
)

// probe is a job that counts its runs and returns what the test tells it to.
type probe struct {
	mu    sync.Mutex
	runs  int
	order *[]string
	name  string
	out   Outcome
	errs  []error // the error of run 1, run 2, ...; nil (success) after the list ends
	panic bool
}

func (p *probe) job(hour int) Job {
	return Job{Name: p.name, Hour: func(policy.Policy) int { return hour }, Run: func(context.Context) (Outcome, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.runs++
		if p.order != nil {
			*p.order = append(*p.order, p.name)
		}
		if p.panic {
			panic("boom")
		}
		if p.runs <= len(p.errs) {
			return Outcome{}, p.errs[p.runs-1]
		}
		return p.out, nil
	}}
}

func (p *probe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs
}

func newSched(t *testing.T, jobs ...Job) (*Scheduler, *tclock, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	clock := &tclock{now: "2026-11-10 05:59:00"}
	s := &Scheduler{DB: d, Clock: clock, Policy: policy.Default, Jobs: jobs, Interval: time.Minute, Logf: func(string, ...any) {}}
	return s, clock, d
}

func result(t *testing.T, d *sql.DB, job, day string) string {
	t.Helper()
	var r string
	if err := d.QueryRow(`SELECT result FROM job_run WHERE job_name = ? AND run_day = ?`, job, day).Scan(&r); err != nil {
		t.Fatalf("no run recorded for %s on %s: %v", job, day, err)
	}
	return r
}

func tick(t *testing.T, s *Scheduler, c *tclock, at string) {
	t.Helper()
	c.now = dates.Moment(at)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick at %s: %v", at, err)
	}
}

func TestAJobWaitsForItsHourThenRunsOncePerDay(t *testing.T) {
	p := &probe{name: "departure"}
	s, c, d := newSched(t, p.job(6))
	tick(t, s, c, "2026-11-10 05:59:00")
	if p.count() != 0 {
		t.Fatal("the job ran before its hour")
	}
	tick(t, s, c, "2026-11-10 06:00:00")
	tick(t, s, c, "2026-11-10 06:01:00")
	tick(t, s, c, "2026-11-10 23:59:00")
	if p.count() != 1 {
		t.Fatalf("ran %d times in one day", p.count())
	}
	if result(t, d, "departure", "2026-11-10") != "ok" {
		t.Error("the run was not recorded as ok")
	}
	tick(t, s, c, "2026-11-11 06:00:00")
	if p.count() != 2 {
		t.Errorf("ran %d times over two days", p.count())
	}
}

func TestAMissedHourStillRunsLaterThatDayAndNeverTwice(t *testing.T) {
	p := &probe{name: "expiry"}
	s, c, _ := newSched(t, p.job(6))
	tick(t, s, c, "2026-11-10 09:30:00") // the service was down at 06:00
	tick(t, s, c, "2026-11-10 09:31:00")
	if p.count() != 1 {
		t.Errorf("ran %d times", p.count())
	}
}

func TestARestartAfterTheRunDoesNotRunTheJobAgain(t *testing.T) {
	p := &probe{name: "expiry"}
	s, c, d := newSched(t, p.job(6))
	tick(t, s, c, "2026-11-10 06:00:00")
	// a new process: a new scheduler on the same database
	s2 := &Scheduler{DB: d, Clock: c, Policy: policy.Default, Jobs: []Job{p.job(6)}, Interval: time.Minute, Logf: func(string, ...any) {}}
	tick(t, s2, c, "2026-11-10 06:05:00")
	if p.count() != 1 {
		t.Errorf("ran %d times across a restart", p.count())
	}
}

func TestAFailedRunIsRetriedAfterTenMinutesNotBefore(t *testing.T) {
	p := &probe{name: "departure", errs: []error{errors.New("db down"), errors.New("db still down")}}
	s, c, d := newSched(t, p.job(6))
	tick(t, s, c, "2026-11-10 06:00:00") // run 1 fails
	if p.count() != 1 || result(t, d, "departure", "2026-11-10") != "failed" {
		t.Fatalf("run 1: count %d", p.count())
	}
	tick(t, s, c, "2026-11-10 06:05:00") // too soon
	if p.count() != 1 {
		t.Error("a failed run was retried after 5 minutes")
	}
	tick(t, s, c, "2026-11-10 06:11:00") // run 2 fails again
	if p.count() != 2 {
		t.Fatalf("no retry after 10 minutes: %d runs", p.count())
	}
	tick(t, s, c, "2026-11-10 06:22:00") // run 3 succeeds
	if p.count() != 3 || result(t, d, "departure", "2026-11-10") != "ok" {
		t.Fatalf("run 3: count %d", p.count())
	}
	tick(t, s, c, "2026-11-10 07:30:00")
	if p.count() != 3 {
		t.Error("the job ran again after it had succeeded")
	}
}

func TestARunThatCrashedHalfwayIsTakenOverAfterThirtyMinutes(t *testing.T) {
	p := &probe{name: "departure"}
	s, c, d := newSched(t, p.job(6))
	if _, err := d.Exec(`INSERT INTO job_run (job_name, run_day, result, at) VALUES ('departure', '2026-11-10', 'running', '2026-11-10 06:00:00')`); err != nil {
		t.Fatal(err)
	}
	tick(t, s, c, "2026-11-10 06:20:00")
	if p.count() != 0 {
		t.Fatal("a run that may still be going was started a second time")
	}
	tick(t, s, c, "2026-11-10 06:31:00")
	if p.count() != 1 || result(t, d, "departure", "2026-11-10") != "ok" {
		t.Errorf("the crashed run was not taken over: %d runs", p.count())
	}
}

func TestARunWithSomeBadRecordsCountsAsDoneForTheDay(t *testing.T) {
	p := &probe{name: "departure", out: Outcome{Changed: 5, Errors: 2}}
	s, c, d := newSched(t, p.job(6))
	tick(t, s, c, "2026-11-10 06:00:00")
	tick(t, s, c, "2026-11-10 07:00:00")
	if p.count() != 1 {
		t.Errorf("a partly successful run was repeated %d times", p.count())
	}
	if result(t, d, "departure", "2026-11-10") != "partial" {
		t.Error("the run was not recorded as partial")
	}
}

func TestTwoSchedulersAtTheSameMomentRunAJobOnce(t *testing.T) {
	p := &probe{name: "departure"}
	s, c, _ := newSched(t, p.job(6))
	c.now = "2026-11-10 06:00:00"
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Tick(context.Background())
		}()
	}
	wg.Wait()
	if p.count() != 1 {
		t.Errorf("a job ran %d times when six ticks raced", p.count())
	}
}

func TestAPanickingJobIsRecordedAsFailedAndDoesNotStopTheNextJob(t *testing.T) {
	var order []string
	bad := &probe{name: "expiry", panic: true, order: &order}
	good := &probe{name: "departure", order: &order}
	s, c, d := newSched(t, bad.job(6), good.job(6))
	tick(t, s, c, "2026-11-10 06:00:00")
	if result(t, d, "expiry", "2026-11-10") != "failed" || good.count() != 1 {
		t.Errorf("panic not contained: good job ran %d times", good.count())
	}
	if len(order) != 2 || order[0] != "expiry" || order[1] != "departure" {
		t.Errorf("jobs due together must run in the order listed: %v", order)
	}
}

func TestEachJobUsesItsOwnHourFromTheCurrentSettings(t *testing.T) {
	hour := 8
	p := &probe{name: "departure"}
	job := p.job(0)
	job.Hour = func(policy.Policy) int { return hour } // the admin changes this setting while the service runs
	s, c, _ := newSched(t, job)
	tick(t, s, c, "2026-11-10 07:59:00")
	if p.count() != 0 {
		t.Fatal("ran before the configured hour")
	}
	hour = 7 // moved earlier at 07:59
	tick(t, s, c, "2026-11-10 08:00:00")
	if p.count() != 1 {
		t.Errorf("the changed hour was not honoured: %d runs", p.count())
	}
}

func TestRunKeepsTickingUntilTheContextEnds(t *testing.T) {
	p := &probe{name: "departure"}
	s, c, _ := newSched(t, p.job(6))
	c.now = "2026-11-10 06:00:00"
	s.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for p.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("Run never ran the due job")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when the context ended")
	}
}

func TestATickWithADatabaseErrorReportsItAndDoesNotPanic(t *testing.T) {
	p := &probe{name: "departure"}
	s, c, d := newSched(t, p.job(6))
	d.Close()
	c.now = "2026-11-10 06:00:00"
	if err := s.Tick(context.Background()); err == nil {
		t.Error("Tick hid a database error")
	}
	if p.count() != 0 {
		t.Error("a job ran although it could not be recorded")
	}
}

func detail(t *testing.T, d *sql.DB, job, day string) string {
	t.Helper()
	var v string
	if err := d.QueryRow(`SELECT detail FROM job_run WHERE job_name = ? AND run_day = ?`, job, day).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEveryRunLeavesAShortLineSayingWhatItDid(t *testing.T) {
	ok := &probe{name: "ok", out: Outcome{Changed: 2}}
	part := &probe{name: "part", out: Outcome{Changed: 3, Errors: 1}}
	bad := &probe{name: "bad", errs: []error{errors.New("database is locked")}}
	boom := &probe{name: "boom", panic: true}
	s, clock, d := newSched(t, ok.job(6), part.job(6), bad.job(6), boom.job(6))
	clock.now = "2026-11-10 06:00:00"
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for job, want := range map[string]string{
		"ok": "2 changed", "part": "3 changed, 1 skipped", "bad": "failed: database is locked", "boom": "failed: panic: boom",
	} {
		if got := detail(t, d, job, "2026-11-10"); got != want {
			t.Errorf("%s detail %q, want %q", job, got, want)
		}
	}
}

func TestRunsShowsTheLatestRunOfEachJobInTheOrderTheyAreScheduled(t *testing.T) {
	a, b := &probe{name: "a"}, &probe{name: "b"}
	s, _, d := newSched(t, a.job(6), b.job(7))
	for _, row := range [][]string{
		{"a", "2026-11-08", "failed", "2026-11-08 06:00:00", "old"},
		{"a", "2026-11-09", "ok", "2026-11-09 06:00:02", "1 changed"},
	} {
		if _, err := d.Exec(`INSERT INTO job_run (job_name, run_day, result, at, detail) VALUES (?, ?, ?, ?, ?)`, row[0], row[1], row[2], row[3], row[4]); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.Runs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Name != "a" || runs[1].Name != "b" {
		t.Fatalf("runs %+v", runs)
	}
	if runs[0].Day != "2026-11-09" || runs[0].Result != "ok" || runs[0].Detail != "1 changed" || runs[0].At != "2026-11-09 06:00:02" {
		t.Errorf("a = %+v, want the 9 Nov run", runs[0])
	}
	if runs[1].Result != "" || runs[1].Day != "" {
		t.Errorf("b has never run, got %+v", runs[1])
	}
	// the hour each job is due, from the current Settings, so the page can say "should have run by now"
	if runs[0].Hour != 6 || runs[1].Hour != 7 {
		t.Errorf("hours %d and %d, want 6 and 7", runs[0].Hour, runs[1].Hour)
	}
}

func TestRunNowRunsTheJobEvenWhenTodaysRunIsAlreadyDone(t *testing.T) {
	p := &probe{name: "p", out: Outcome{Changed: 4}}
	s, clock, d := newSched(t, p.job(6))
	clock.now = "2026-11-10 06:00:00"
	if err := s.Tick(context.Background()); err != nil || p.count() != 1 {
		t.Fatalf("scheduled run: %v, %d runs", err, p.count())
	}
	clock.now = "2026-11-10 09:30:00"
	got, err := s.RunNow(context.Background(), "p")
	if err != nil || p.count() != 2 {
		t.Fatalf("run now: %v, %d runs", err, p.count())
	}
	if got.Result != "ok" || got.Outcome.Changed != 4 || got.Detail != "4 changed" {
		t.Errorf("result %+v", got)
	}
	if r := result(t, d, "p", "2026-11-10"); r != "ok" {
		t.Errorf("stored result %q", r)
	}
	// and before its hour, too: an admin may need it any time
	clock.now = "2026-11-11 02:00:00"
	if _, err := s.RunNow(context.Background(), "p"); err != nil || p.count() != 3 {
		t.Fatalf("run now before the hour: %v, %d runs", err, p.count())
	}
}

func TestRunNowRefusesAJobThatIsRunningAndAnUnknownJob(t *testing.T) {
	p := &probe{name: "p"}
	s, clock, d := newSched(t, p.job(6))
	clock.now = "2026-11-10 09:00:00"
	if _, err := d.Exec(`INSERT INTO job_run (job_name, run_day, result, at) VALUES ('p', '2026-11-10', 'running', '2026-11-10 08:59:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNow(context.Background(), "p"); !errors.Is(err, ErrBusy) || p.count() != 0 {
		t.Fatalf("a job that started a minute ago must not run twice: %v, %d runs", err, p.count())
	}
	// a run that has been "running" for over half an hour is a dead process: take it over
	clock.now = "2026-11-10 09:31:00"
	if _, err := s.RunNow(context.Background(), "p"); err != nil || p.count() != 1 {
		t.Fatalf("take over: %v, %d runs", err, p.count())
	}
	if _, err := s.RunNow(context.Background(), "nope"); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("unknown job: %v", err)
	}
}

func TestRunNowReportsAFailedJobAndKeepsTheScheduleRetrying(t *testing.T) {
	p := &probe{name: "p", errs: []error{errors.New("down")}}
	s, clock, d := newSched(t, p.job(6))
	clock.now = "2026-11-10 09:00:00"
	got, err := s.RunNow(context.Background(), "p")
	if err != nil || got.Result != "failed" || got.Detail != "failed: down" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if r := result(t, d, "p", "2026-11-10"); r != "failed" {
		t.Errorf("stored %q", r)
	}
	// a failed run is still retried by the scheduler after the wait
	clock.now = "2026-11-10 09:11:00"
	if err := s.Tick(context.Background()); err != nil || p.count() != 2 {
		t.Fatalf("retry: %v, %d runs", err, p.count())
	}
}
