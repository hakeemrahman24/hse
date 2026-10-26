// Package emr holds the clinical records of the Medical Record page: visits and
// the screening parts that hang off them. Malaria is first (malaria.go).
package emr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxHeadOfHouseholdLength = 150

// CleanHeadOfHousehold is the rule the server applies to the head-of-household
// name: keep letters, spaces, '.', ”' and '-'; collapse repeated spaces; trim;
// Title Case. The form mirrors this as the user types (malaria.js), but this
// function is the rule.
func CleanHeadOfHousehold(s string) string {
	var kept strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || r == ' ' || r == '.' || r == '\'' || r == '-' {
			kept.WriteRune(r)
		}
	}
	words := strings.Fields(kept.String())
	for i, w := range words {
		words[i] = titleWord(w)
	}
	return strings.Join(words, " ")
}

// titleWord upper-cases the first letter of a word and of each part after a
// hyphen, and lower-cases the rest. An apostrophe does not start a new part,
// so "ba'u" stays "Ba'u".
func titleWord(w string) string {
	var out strings.Builder
	upperNext := true
	for _, r := range w {
		switch {
		case r == '-':
			out.WriteRune(r)
			upperNext = true
		case upperNext && unicode.IsLetter(r):
			out.WriteRune(unicode.ToUpper(r))
			upperNext = false
		default:
			out.WriteRune(unicode.ToLower(r))
			if unicode.IsLetter(r) {
				upperNext = false
			}
		}
	}
	return out.String()
}

// TestID is the identifier shown for a malaria test: "MAL-" and the record number
// padded to five digits ("MAL-00042"). It is made from the record number, so it
// never needs storing and can never differ from it.
func TestID(malariaID int64) string {
	return fmt.Sprintf("MAL-%05d", malariaID)
}

// AgeAt is the whole years between a date of birth and a day, both YYYY-MM-DD.
// ok is false when the date of birth is missing, unreadable or after the day.
func AgeAt(dateOfBirth, onDate string) (years int, ok bool) {
	dob, err := time.Parse("2006-01-02", dateOfBirth)
	if err != nil {
		return 0, false
	}
	on, err := time.Parse("2006-01-02", onDate)
	if err != nil || on.Before(dob) {
		return 0, false
	}
	years = on.Year() - dob.Year()
	if on.Month() < dob.Month() || (on.Month() == dob.Month() && on.Day() < dob.Day()) {
		years--
	}
	return years, true
}

// ISOWeek is the ISO 8601 week of a YYYY-MM-DD day as "2026-W43" (the week-year,
// which differs from the calendar year around New Year). Empty when unreadable.
// It is computed for reports, never stored.
func ISOWeek(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

const (
	slideLetters  = 3
	maxSlideDigit = 9
)

// SlideInitialCandidates lists, best first, the codes that can be written on a
// patient's blood slide: 3 letters made from the name plus 1 digit, such as
// BSU1.
//
// The letters are the first letter of each word (up to three words), topped up
// with the following letters of the name, so "Budi Santoso" gives BSU. People
// with the same letters on the same day are told apart by the digit: BSU1,
// BSU2, ... BSU9. Only then does the code move to the next letter variant
// (BSD1, BSI1, ...), and last to the first letter plus any two letters, so the
// list always has more entries than a day has patients.
func SlideInitialCandidates(name string) []string {
	var initials, rest []byte
	for _, word := range strings.Fields(name) {
		letters := asciiUpperLetters(word)
		if len(letters) == 0 {
			continue
		}
		if len(initials) < slideLetters {
			initials = append(initials, letters[0])
			rest = append(rest, letters[1:]...)
		} else {
			rest = append(rest, letters...)
		}
	}
	base := append([]byte{}, initials...)
	for _, c := range rest {
		if len(base) >= slideLetters {
			break
		}
		base = append(base, c)
	}
	for len(base) < slideLetters {
		base = append(base, 'X')
	}

	seenPrefix := map[string]bool{}
	var prefixes []string
	addPrefix := func(p []byte) {
		if s := string(p); !seenPrefix[s] {
			seenPrefix[s] = true
			prefixes = append(prefixes, s)
		}
	}
	addPrefix(base)
	for _, c := range rest {
		variant := append([]byte{}, base...)
		variant[slideLetters-1] = c
		addPrefix(variant)
	}
	for a := byte('A'); a <= 'Z'; a++ {
		for b := byte('A'); b <= 'Z'; b++ {
			addPrefix([]byte{base[0], a, b})
		}
	}

	out := make([]string, 0, len(prefixes)*maxSlideDigit)
	for _, p := range prefixes {
		for d := 1; d <= maxSlideDigit; d++ {
			out = append(out, p+strconv.Itoa(d))
		}
	}
	return out
}

// asciiUpperLetters keeps only A-Z (after upper-casing). Slides are written by
// hand, so accented letters are dropped rather than transliterated.
func asciiUpperLetters(s string) []byte {
	var out []byte
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' {
			out = append(out, byte(r))
		}
	}
	return out
}
