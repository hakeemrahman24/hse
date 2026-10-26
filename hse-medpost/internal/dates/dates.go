// Package dates stores calendar days as YYYY-MM-DD text (no time, no timezone).
package dates

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// ISODate is calendar-day only: "2026-08-21". Never store time or zone.
var ISODate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Normalize trims and validates YYYY-MM-DD. Empty string is allowed (optional fields).
func Normalize(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !ISODate.MatchString(s) {
		return "", errors.New("date must be YYYY-MM-DD")
	}
	// Validate real calendar day without applying a timezone shift.
	if _, err := time.ParseInLocation("2006-01-02", s, time.UTC); err != nil {
		return "", errors.New("invalid calendar date")
	}
	return s, nil
}

// Require is Normalize but empty is rejected.
func Require(s string) (string, error) {
	s, err := Normalize(s)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", errors.New("date required")
	}
	return s, nil
}

// Today is the clinic's calendar day as YYYY-MM-DD. It is the only source of
// "today" for business rules: compare it as text (ISO days sort correctly).
func Today() string {
	return ClinicNow().Format("2006-01-02")
}

// DaysBetween is the whole number of calendar days from `from` to `to`
// (positive when `to` is later). Both must be YYYY-MM-DD.
func DaysBetween(from, to string) (int, error) {
	f, err := time.ParseInLocation("2006-01-02", from, time.UTC)
	if err != nil {
		return 0, err
	}
	t, err := time.ParseInLocation("2006-01-02", to, time.UTC)
	if err != nil {
		return 0, err
	}
	return int(t.Sub(f).Hours() / 24), nil
}

// ClinicLocation is the clinic's business-date zone, Asia/Jayapura (UTC+9),
// fixed here instead of read from the OS: the server clock runs UTC, so
// between 00:00 and 09:00 clinic time its date is a day behind the browser's.
// Falls back to a fixed +9 offset when the host has no IANA tzdata.
var ClinicLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jayapura"); err == nil {
		return loc
	}
	return time.FixedZone("WIT", 9*60*60)
}()

// TimestampLayout is how every stored timestamp (an instant: created,
// submitted, logged) is written: UTC, no milliseconds, no zone suffix. Five of
// the six existing writers already use it, and the server clock is UTC, so
// existing rows follow it too. A fixed 19 characters also means the column can
// become CHAR(19) later without rewriting rows.
const TimestampLayout = "2006-01-02T15:04:05"

// Stamp formats an instant as a stored timestamp, converting to UTC first.
func Stamp(t time.Time) string {
	return t.UTC().Format(TimestampLayout)
}

// NowStamp is Stamp(time.Now()). Use it instead of time.Now().Format(...) so
// no writer depends on the host's local zone.
func NowStamp() string {
	return Stamp(time.Now())
}

// NormalizeTimestamp turns a timestamp from outside (a browser's toISOString()
// is 24 characters with milliseconds and a "Z") into the stored layout.
// A value with a zone or offset is converted to UTC; a bare 19-character value
// is already in the stored layout and is read as UTC. Empty is allowed, like
// Normalize, so optional fields stay optional.
func NormalizeTimestamp(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return Stamp(t), nil
	}
	if t, err := time.ParseInLocation(TimestampLayout, s, time.UTC); err == nil {
		return Stamp(t), nil
	}
	return "", errors.New("timestamp must be like 2026-10-07T09:00:00Z")
}

// ClinicNow is the current instant expressed in the clinic zone.
func ClinicNow() time.Time {
	return time.Now().In(ClinicLocation)
}
