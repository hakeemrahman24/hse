package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"hse-medpost/internal/dates"
)

// The admin's settings for the mail that tells submitters about reviewed and rejected documents
// (owner, 2026-11-10): how often it goes out, from which hour of the day, in which time zone, and
// whether the reviewers are copied. They live in one row, like the reviewer digest's.
//
// The schedule is a grid of "slots": one slot every IntervalHours, counted from StartHour in the
// chosen zone (12 hours from 08:00 WIB = 08:00 and 20:00 WIB; 24 hours = 08:00 daily; 48 hours =
// 08:00 every second day). A run is due when the latest slot that has passed has not been run yet,
// so a server that was off at 08:00 still runs once it starts.

// NoticeIntervals are the hours the admin may choose between runs.
var NoticeIntervals = []int{6, 12, 24, 48}

// noticeStartHours are the choices of the start hour (0 to 23).
var noticeStartHours = func() []int {
	h := make([]int, 24)
	for i := range h {
		h[i] = i
	}
	return h
}()

// NoticeZone is a time zone the admin can pick. Indonesia has no daylight saving, so a fixed offset
// is exact and needs no time zone database on the Pi.
type NoticeZone struct {
	Code   string // what is stored
	Label  string // what the admin reads
	Offset time.Duration
}

var NoticeZones = []NoticeZone{
	{"WIB", "WIB (Western Indonesia, UTC+7)", 7 * time.Hour},
	{"WITA", "WITA (Central Indonesia, UTC+8)", 8 * time.Hour},
	{"WIT", "WIT (Eastern Indonesia, UTC+9)", 9 * time.Hour},
	{"UTC", "UTC", 0},
}

const (
	defaultNoticeHours = 12
	defaultNoticeStart = 8
	defaultNoticeZone  = "WIB"
)

var ErrBadNoticeSetting = errors.New("mcusubmit: the notice schedule must be 6, 12, 24 or 48 hours, a start hour from 0 to 23, and a known time zone")

// NoticeConfig is the settings row.
type NoticeConfig struct {
	IntervalHours int
	StartHour     int    // 0 to 23, in Timezone
	Timezone      string // a NoticeZone code
	CopyReviewers bool   // reviewers get a copy of every notice
	LastRunAt     string // empty until the first run finished (UTC)
}

func defaultNoticeConfig() NoticeConfig {
	return NoticeConfig{IntervalHours: defaultNoticeHours, StartHour: defaultNoticeStart, Timezone: defaultNoticeZone, CopyReviewers: true}
}

func zoneOffset(code string) (time.Duration, bool) {
	for _, z := range NoticeZones {
		if z.Code == code {
			return z.Offset, true
		}
	}
	return 0, false
}

// NoticeSettings reads the row. A missing row, or a database that does not have the table yet
// (the MariaDB migration is run by hand), gives the defaults, so notices never stop because of it.
func (s *Store) NoticeSettings(ctx context.Context) NoticeConfig {
	c := defaultNoticeConfig()
	var copyOn int
	var got NoticeConfig
	err := s.DB.QueryRowContext(ctx,
		`SELECT IntervalHours, StartHour, Timezone, CopyReviewers, COALESCE(LastRunAt, '') FROM McuNoticeSetting WHERE McuNoticeSettingID = 1`).
		Scan(&got.IntervalHours, &got.StartHour, &got.Timezone, &copyOn, &got.LastRunAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c
	}
	if err != nil {
		log.Printf("mcusubmit: notice settings (using the defaults): %v", err)
		return c
	}
	got.CopyReviewers = copyOn == 1
	if _, ok := zoneOffset(got.Timezone); !ok || got.StartHour < 0 || got.StartHour > 23 || got.IntervalHours <= 0 {
		// Hand-edited garbage must not stop the mail or divide by zero below: keep the run history
		// but schedule with the defaults.
		def := defaultNoticeConfig()
		def.CopyReviewers, def.LastRunAt = got.CopyReviewers, got.LastRunAt
		return def
	}
	return got
}

// SaveNoticeSettings stores the schedule and the copy switch; LastRunAt is not touched.
func (s *Store) SaveNoticeSettings(ctx context.Context, c NoticeConfig) error {
	okInterval := false
	for _, h := range NoticeIntervals {
		okInterval = okInterval || h == c.IntervalHours
	}
	_, okZone := zoneOffset(c.Timezone)
	if !okInterval || !okZone || c.StartHour < 0 || c.StartHour > 23 {
		return ErrBadNoticeSetting
	}
	v := 0
	if c.CopyReviewers {
		v = 1
	}
	return s.oneRow(ctx,
		`UPDATE McuNoticeSetting SET IntervalHours = ?, StartHour = ?, Timezone = ?, CopyReviewers = ? WHERE McuNoticeSettingID = 1`,
		c.IntervalHours, c.StartHour, c.Timezone, v)
}

// anchor is a fixed day at StartHour in the zone; every slot is the anchor plus a whole number of
// intervals. Because the anchor never moves, 48-hour slots stay on the same every-second-day grid.
func (c NoticeConfig) anchor() time.Time {
	off, _ := zoneOffset(c.Timezone)
	return time.Date(2026, 1, 1, c.StartHour, 0, 0, 0, time.FixedZone(c.Timezone, int(off/time.Second)))
}

// slotAtOrBefore is the latest scheduled run time that is not after t.
func (c NoticeConfig) slotAtOrBefore(t time.Time) time.Time {
	step := time.Duration(c.IntervalHours) * time.Hour
	a := c.anchor()
	k := t.Sub(a) / step
	if t.Before(a) {
		k-- // Go truncates toward zero; a time before the anchor needs the slot below it
	}
	return a.Add(k * step)
}

// NextNoticeRun is the next scheduled run after now, in the configured zone, for the settings page.
func (s *Store) NextNoticeRun(c NoticeConfig) time.Time {
	off, _ := zoneOffset(c.Timezone)
	next := c.slotAtOrBefore(s.now()).Add(time.Duration(c.IntervalHours) * time.Hour)
	return next.In(time.FixedZone(c.Timezone, int(off/time.Second)))
}

// noticeDue is true when the latest slot that has passed has not been run yet.
func (s *Store) noticeDue(c NoticeConfig) bool {
	if c.LastRunAt == "" {
		return true
	}
	last, err := time.ParseInLocation(dates.TimestampLayout, c.LastRunAt, time.UTC)
	if err != nil {
		return true
	}
	return last.Before(c.slotAtOrBefore(s.now()))
}

// RunNoticesIfDue runs the submitter notices when a scheduled slot has passed since the last run.
// The server calls it every hour. The clock moves only after a run without errors, so a mail
// outage is retried on the next hourly check instead of waiting for the next slot; a run with
// nothing to say still moves it. It reports whether a run happened.
func (s *Store) RunNoticesIfDue(ctx context.Context, m Mailer, portalURL string) (mails, documents int, ran bool, err error) {
	if !s.noticeDue(s.NoticeSettings(ctx)) {
		return 0, 0, false, nil
	}
	mails, documents, err = s.RunClosedNotices(ctx, m, portalURL)
	if err != nil {
		return mails, documents, true, err
	}
	// The row may not exist yet (table not migrated): then every hourly check runs, which is
	// harmless because only documents not yet told about are ever mailed.
	_ = s.oneRow(ctx, `UPDATE McuNoticeSetting SET LastRunAt = ? WHERE McuNoticeSettingID = 1`, s.stamp())
	return mails, documents, true, nil
}
