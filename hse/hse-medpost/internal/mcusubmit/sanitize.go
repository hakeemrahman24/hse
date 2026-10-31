package mcusubmit

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	maxNameRunes     = 150
	maxPositionRunes = 128
	maxEmailBytes    = 254 // the length limit of an address in the SMTP standard
	maxEmailLocal    = 64
)

// cleanText normalises to NFC, turns every run of whitespace into one space, drops every rune keep
// rejects (control characters included), and cuts to max runes. It is for data quality: the
// templates still HTML-escape every value, because escaping is what keeps a page safe.
func cleanText(s string, keep func(r rune) bool, max int) string {
	var b strings.Builder
	for _, r := range norm.NFC.String(s) {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case r == '‘' || r == '’':
			b.WriteRune('\'') // curly apostrophes become the plain one, so "O'Neil" is one spelling
		case keep(r):
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if runes := []rune(out); len(runes) > max {
		out = strings.TrimSpace(string(runes[:max]))
	}
	return out
}

// SanitizeName keeps letters, combining marks, space, apostrophe, hyphen and dot (plan section 4),
// collapses extra spaces, and writes the name in title style (owner, 2026-10-31).
func SanitizeName(s string) string {
	return titleName(cleanText(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsMark(r) || strings.ContainsRune("'-.", r)
	}, maxNameRunes))
}

// titleName upper-cases the first letter of every word and lower-cases the rest, so "BUDI santoso"
// and "budi SANTOSO" are one spelling for the unique checks and the search. A word starts after a
// space, hyphen or dot ("Nur-Aini", "M.Yusuf"). After an apostrophe the next letter is capital only
// when the apostrophe follows a single letter ("O'Neil", "D'Angelo"); in "Ma'ruf" or "Sa'id" it
// belongs inside the word and stays lower case. Combining marks never start a word.
func titleName(s string) string {
	var b strings.Builder
	upperNext := true
	lettersInWord := 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			if upperNext {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
			upperNext = false
			lettersInWord++
		case r == '\'':
			b.WriteRune(r)
			upperNext = lettersInWord == 1
		case unicode.IsMark(r):
			b.WriteRune(r)
		default: // space, hyphen, dot
			b.WriteRune(r)
			upperNext = true
			lettersInWord = 0
		}
	}
	return b.String()
}

// SanitizePosition is SanitizeName plus digits and the punctuation job titles use. It does not
// change the case, so it also serves free text such as the admin's reject reason and the
// reviewer's optional name.
func SanitizePosition(s string) string {
	return cleanText(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || strings.ContainsRune("'-./&,()", r)
	}, maxPositionRunes)
}

// SanitizeJobTitle is SanitizePosition written in the same title style as a name (owner,
// 2026-10-31). It is for the position fields only: a sentence such as a reject reason must keep
// its own capitals.
func SanitizeJobTitle(s string) string {
	return titlePosition(SanitizePosition(s))
}

// titlePosition applies titleName to every word of a job title, where a word is a run of letters,
// marks and apostrophes (digits and punctuation end it: "Level 2", "Site-A", "QA/QC"). A word that
// is already all capitals and 2 to 4 letters long is an acronym and stays as typed, so "HSE Officer"
// and "IT Support" are not turned into "Hse" and "It". "WELDER" is longer, so it becomes "Welder".
func titlePosition(s string) string {
	var out, word strings.Builder
	flush := func() {
		w := word.String()
		word.Reset()
		if w == "" {
			return
		}
		if isAcronym(w) {
			out.WriteString(w)
			return
		}
		out.WriteString(titleName(w))
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsMark(r) || r == '\'' {
			word.WriteRune(r)
			continue
		}
		flush()
		out.WriteRune(r)
	}
	flush()
	return out.String()
}

func isAcronym(w string) bool {
	n := 0
	for _, r := range w {
		if !unicode.IsUpper(r) {
			return false
		}
		n++
	}
	return n >= 2 && n <= 4
}

// ParseEmail lower-cases and checks the syntax of an address. It accepts only a bare address: a
// display name, a second address or a line break is refused, so the value is safe to put in a mail
// header and to use as the account's unique key.
func ParseEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > maxEmailBytes || strings.ContainsAny(s, " \t\r\n<>,;\"") {
		return "", errors.New("enter a valid email address")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", errors.New("enter a valid email address")
	}
	at := strings.LastIndex(s, "@")
	local, domain := s[:at], s[at+1:]
	if len(local) > maxEmailLocal || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.New("enter a valid email address")
	}
	return s, nil
}
