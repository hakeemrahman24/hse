// Package importer reads the old Google Sheet (an Excel download) into the new tables.
//
// The old sheet is hand-edited and has years of mixed formats in it, so everything here
// is strict about what it accepts and loud about what it does not: a cell that cannot be
// read is reported, never guessed.
package importer

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"entry-permit/internal/phone"
)

// minYear and maxYear catch a typo such as "20266" or "0026" without refusing an old birth date.
const (
	minYear = 1900
	maxYear = 2100
)

// Blank reports whether a cell holds nothing: empty, or one of the placeholders people and
// formulas left behind ("_", "-", an Excel error).
func Blank(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "_", "-", "#N/A", "N#A":
		return true
	}
	return false
}

// dayLayouts are the date shapes found in the sheet. WHY month first for "9/20/2026" and
// "09-20-26": the sheet was kept in a US locale (a "20" can only be a day), and a day-first
// reading is never tried, because "8/7/2026" would silently be wrong.
var dayLayouts = []string{"2006-01-02", "2 Jan 2006", "1/2/2006", "01-02-06"}

var momentLayouts = []string{"2006-01-02 15:04:05", "2 Jan 2006 15:04:05", "1/2/2006 15:04:05"}

// serial matches an Excel date stored as a number ("46331" or "46331.48128"): days since
// 30 Dec 1899. A cell with no date format shows it that way.
var serial = regexp.MustCompile(`^\d{5}(\.\d+)?$`)

// minSerial is 1 Jan 1970. WHY a floor: a five-digit number in a date cell is only taken as a
// date when it lands in a believable range, so a stray number is refused, not turned into a date.
const minSerial = 25569

// fromSerial converts an Excel serial number to a time. Seconds are cut, not rounded: a sheet shows 11:33:02.7 as 11:33:02.
func fromSerial(s string) (time.Time, bool) {
	if !serial.MatchString(s) {
		return time.Time{}, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < minSerial {
		return time.Time{}, false
	}
	t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Add(time.Duration(math.Floor(f*86400)) * time.Second)
	return t, sane(t)
}

// Day turns a sheet date into yyyy-mm-dd. A blank cell is ("", true); an unreadable one is
// ("", false), so the caller can report it.
func Day(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if Blank(s) {
		return "", true
	}
	for _, layout := range dayLayouts {
		if t, err := time.Parse(layout, s); err == nil && sane(t) {
			return t.Format("2006-01-02"), true
		}
	}
	if t, ok := fromSerial(s); ok {
		return t.Format("2006-01-02"), true
	}
	return "", false
}

// Moment turns a sheet timestamp into yyyy-mm-dd hh:mm:ss. The hour is not shifted: sheet
// cells are already Jakarta wall-clock time.
func Moment(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if Blank(s) {
		return "", true
	}
	for _, layout := range momentLayouts {
		if t, err := time.Parse(layout, s); err == nil && sane(t) {
			return t.Format("2006-01-02 15:04:05"), true
		}
	}
	if t, ok := fromSerial(s); ok {
		return t.Format("2006-01-02 15:04:05"), true
	}
	return "", false
}

func sane(t time.Time) bool { return t.Year() >= minYear && t.Year() <= maxYear }

// scientific matches "8.12345678901E+11": the mantissa and the exponent.
var scientific = regexp.MustCompile(`^(\d(?:\.\d+)?)[eE]\+?(\d+)$`)

// Phone returns the digits of a phone cell, in the form the forms store (a leading 0 becomes
// 62). The sheet kept many numbers in numeric cells, which Sheets wrote as "8.12345678901E+11";
// that is turned back into digits. lossy is true when fewer digits were written than the
// number has, so the last ones may have been rounded away and the number may be wrong.
func Phone(raw string) (digits string, lossy bool) {
	raw = strings.TrimSpace(raw)
	if Blank(raw) {
		return "", false
	}
	if m := scientific.FindStringSubmatch(raw); m != nil {
		f, err := strconv.ParseFloat(raw, 64)
		if err == nil && f > 0 {
			written := len(strings.Replace(m[1], ".", "", 1)) // digits the cell wrote
			exp, _ := strconv.Atoi(m[2])
			return phone.Normalize(strconv.FormatFloat(math.Round(f), 'f', 0, 64)), written < exp+1
		}
	}
	return phone.Normalize(raw), false
}
