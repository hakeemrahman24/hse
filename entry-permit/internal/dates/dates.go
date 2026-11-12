// Package dates implements the date rule from plan section 2b.
//
// WHY strings: every day is "yyyy-mm-dd" and every moment is
// "yyyy-mm-dd hh:mm:ss", both in Jakarta wall-clock time, with no Z, offset or
// "T". Because of the fixed-width, zero-padded layout, plain string comparison
// (<, ==) is correct chronological comparison, and there is no timezone to
// shift by accident. time.Time is used only inside this package, in UTC, for
// calendar arithmetic (adding days), never for wall-clock conversion.
package dates

import (
	"fmt"
	"strings"
	"time"
)

const (
	dayLayout    = "2006-01-02"
	momentLayout = "2006-01-02 15:04:05"
	// jakartaOffsetSeconds: Jakarta is UTC+7 all year (no daylight saving).
	// A fixed zone avoids depending on tzdata being installed on the Pi.
	jakartaOffsetSeconds = 7 * 3600
)

// CleanTyped tidies a date a person typed so the strict d/m/yyyy parsers can read it: spaces, dashes
// and dots become slashes, and eight digits (with or without separators, so 24071983 and 24/071983)
// become dd/mm/yyyy. WHY eight digits are always ddmmyyyy: a day and a month with one digit each
// make six digits and with two make eight, so a correctly separated eight-digit date is exactly
// that shape. A date that starts with a four-digit year (2024-07-24) and anything with a letter or
// another symbol come back unchanged, so they are still refused rather than guessed at.
func CleanTyped(s string) string {
	s = strings.TrimSpace(s)
	var digits, out strings.Builder
	lastSeparator := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
			out.WriteRune(r)
			lastSeparator = false
		case r == '/' || r == '-' || r == '.' || r == ' ':
			if !lastSeparator {
				out.WriteByte('/')
			}
			lastSeparator = true
		default:
			return s
		}
	}
	groups := strings.Split(out.String(), "/")
	if len(groups) == 3 && len(groups[0]) == 4 {
		return s
	}
	if d := digits.String(); len(d) == 8 {
		return d[0:2] + "/" + d[2:4] + "/" + d[4:]
	}
	return out.String()
}

// Day is a calendar day, "yyyy-mm-dd".
type Day string

// Moment is a Jakarta wall-clock instant, "yyyy-mm-dd hh:mm:ss".
type Moment string

// ParseDay validates s (after trimming) and returns it as a Day.
// time.Parse rejects impossible dates such as 2026-02-30.
func ParseDay(s string) (Day, error) {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(dayLayout, s); err != nil {
		return "", fmt.Errorf("invalid day %q: want yyyy-mm-dd", s)
	}
	return Day(s), nil
}

// ParseMoment validates s (after trimming) and returns it as a Moment.
func ParseMoment(s string) (Moment, error) {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(momentLayout, s); err != nil {
		return "", fmt.Errorf("invalid moment %q: want yyyy-mm-dd hh:mm:ss", s)
	}
	return Moment(s), nil
}

// Day returns the calendar day part of a moment.
func (m Moment) Day() Day {
	if len(m) < 10 {
		return ""
	}
	return Day(m[:10])
}

// AddMinutes returns the moment n minutes later (n may be negative). A
// malformed moment is returned unchanged.
func (m Moment) AddMinutes(n int) Moment {
	t, err := time.Parse(momentLayout, string(m))
	if err != nil {
		return m
	}
	return Moment(t.Add(time.Duration(n) * time.Minute).Format(momentLayout))
}

// AddHours returns the moment n hours later.
func (m Moment) AddHours(n int) Moment { return m.AddMinutes(n * 60) }

// HoursBetween is the whole hours from one moment to a later one. A moment that is not
// later counts as 0 hours; ok is false when either one cannot be read, so the caller
// can skip the record instead of showing a made-up wait.
func HoursBetween(from, to Moment) (hours int, ok bool) {
	a, errA := time.Parse(momentLayout, string(from))
	b, errB := time.Parse(momentLayout, string(to))
	if errA != nil || errB != nil {
		return 0, false
	}
	if h := int(b.Sub(a).Hours()); h > 0 {
		return h, true
	}
	return 0, true
}

func (d Day) time() time.Time {
	t, _ := time.Parse(dayLayout, string(d))
	return t
}

// AddDays returns the day n calendar days later (n may be negative).
func (d Day) AddDays(n int) Day {
	return Day(d.time().AddDate(0, 0, n).Format(dayLayout))
}

// DaysBetween is to minus from, in whole calendar days.
func DaysBetween(from, to Day) int {
	return int(to.time().Sub(from.time()).Hours() / 24)
}

// InclusiveDays counts both endpoints: the same day is 1 day.
func InclusiveDays(from, to Day) int {
	return DaysBetween(from, to) + 1
}

// FromUTC converts a UTC clock reading to a Jakarta Moment. It exists for
// NowJakarta and for the one-time import of old UTC gate timestamps.
func FromUTC(y int, mo time.Month, d, h, mi, s int) Moment {
	t := time.Date(y, mo, d, h, mi, s, 0, time.UTC).Add(jakartaOffsetSeconds * time.Second)
	return Moment(t.Format(momentLayout))
}

// NowJakarta is the real current Jakarta moment.
func NowJakarta() Moment {
	return Moment(time.Now().UTC().Add(jakartaOffsetSeconds * time.Second).Format(momentLayout))
}

// Clock is the single injected source of "now" so rules are testable.
type Clock interface {
	Now() Moment
	Today() Day
}

// SystemClock reads the real time.
type SystemClock struct{}

func (SystemClock) Now() Moment { return NowJakarta() }
func (SystemClock) Today() Day  { return NowJakarta().Day() }

// FixedClock returns a clock frozen at m, for tests.
func FixedClock(m Moment) Clock { return fixedClock{m} }

type fixedClock struct{ m Moment }

func (c fixedClock) Now() Moment { return c.m }
func (c fixedClock) Today() Day  { return c.m.Day() }
