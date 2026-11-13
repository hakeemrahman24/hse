// Package datasync keeps two reference datasets fresh without anyone running a script:
//
//   - the Indonesia region list (the Region table), refreshed from GitHub every N months;
//   - the Kemenkes malaria endemicity map, scraped every N months into a staging table that an
//     admin reviews and applies.
//
// Both are driven by one row each in DataSyncSetting, which the Settings page edits.
package datasync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"hse-medpost/internal/dates"
)

// Source names, as stored in DataSyncSetting.Source.
const (
	SourceRegion     = "region"
	SourceEndemicity = "endemicity"
)

// Intervals are the months an admin may choose between refreshes.
var Intervals = []int{1, 3, 6, 12}

// timestampLayout is how every time in these tables is stored (UTC, no zone suffix), the same
// text on SQLite and MariaDB.
const timestampLayout = dates.TimestampLayout

// retryAfterFailure is how long a failed source waits before the hourly check tries it again.
// WHY: without it a source that is down (GitHub, the Kemenkes site) would be hit every hour.
const retryAfterFailure = 24 * time.Hour

// ErrBadSetting is returned for a source or interval that is not allowed.
var ErrBadSetting = errors.New("datasync: unknown source, or interval is not 1, 3, 6 or 12 months")

// Setting is one DataSyncSetting row.
type Setting struct {
	Source         string
	Enabled        bool
	IntervalMonths int
	LastRunAt      string // last success, "" = never
	LastAttemptAt  string // last try, success or not
	LastStatus     string // "", "ok" or "error"
	LastMessage    string
	AppliedAt      string // endemicity only
}

// Store reads and writes the settings. Now is replaceable so tests control the clock.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func validSource(source string) bool { return source == SourceRegion || source == SourceEndemicity }

func validInterval(months int) bool {
	for _, m := range Intervals {
		if m == months {
			return true
		}
	}
	return false
}

// Get returns the setting for a source.
func (s *Store) Get(ctx context.Context, source string) (Setting, error) {
	if !validSource(source) {
		return Setting{}, ErrBadSetting
	}
	var st Setting
	var enabled int
	err := s.DB.QueryRowContext(ctx, `SELECT Source, Enabled, IntervalMonths, COALESCE(LastRunAt,''), COALESCE(LastAttemptAt,''),
		LastStatus, LastMessage, COALESCE(AppliedAt,'') FROM DataSyncSetting WHERE Source = ?`, source).
		Scan(&st.Source, &enabled, &st.IntervalMonths, &st.LastRunAt, &st.LastAttemptAt, &st.LastStatus, &st.LastMessage, &st.AppliedAt)
	if err != nil {
		return Setting{}, fmt.Errorf("read %s setting: %w", source, err)
	}
	st.Enabled = enabled != 0
	return st, nil
}

// SaveSchedule stores the switch and the interval.
func (s *Store) SaveSchedule(ctx context.Context, source string, enabled bool, months int) error {
	if !validSource(source) || !validInterval(months) {
		return ErrBadSetting
	}
	on := 0
	if enabled {
		on = 1
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE DataSyncSetting SET Enabled = ?, IntervalMonths = ? WHERE Source = ?`, on, months, source)
	return err
}

// recordAttempt stores the outcome of a run. Only a success moves LastRunAt.
func (s *Store) recordAttempt(ctx context.Context, source string, runErr error, message string) error {
	stamp := s.now().Format(timestampLayout)
	if runErr != nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE DataSyncSetting SET LastAttemptAt = ?, LastStatus = 'error', LastMessage = ? WHERE Source = ?`,
			stamp, clip(runErr.Error()), source)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE DataSyncSetting SET LastAttemptAt = ?, LastRunAt = ?, LastStatus = 'ok', LastMessage = ? WHERE Source = ?`,
		stamp, stamp, clip(message), source)
	return err
}

// markApplied stores when an admin applied the reviewed endemicity data.
func (s *Store) markApplied(ctx context.Context, source string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE DataSyncSetting SET AppliedAt = ? WHERE Source = ?`, s.now().Format(timestampLayout), source)
	return err
}

// clip keeps a message inside the MariaDB column (1000 characters).
func clip(msg string) string {
	r := []rune(msg)
	if len(r) > 900 {
		return string(r[:900]) + "…"
	}
	return msg
}

// NextRun is when the source is next due, or the zero time when it never ran (due now).
func (st Setting) NextRun() time.Time {
	last, err := time.Parse(timestampLayout, st.LastRunAt)
	if err != nil {
		return time.Time{}
	}
	return last.AddDate(0, st.IntervalMonths, 0)
}

// Due is true when the source is switched on, its interval has passed since the last success, and
// the last failed attempt (if any) is more than a day old.
func (st Setting) Due(now time.Time) bool {
	if !st.Enabled || now.Before(st.NextRun()) {
		return false
	}
	if at, err := time.Parse(timestampLayout, st.LastAttemptAt); err == nil && st.LastStatus == "error" && now.Before(at.Add(retryAfterFailure)) {
		return false
	}
	return true
}
