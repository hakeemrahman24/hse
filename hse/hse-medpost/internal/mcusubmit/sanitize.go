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

// SanitizeName keeps letters, combining marks, space, apostrophe, hyphen and dot (plan section 4).
func SanitizeName(s string) string {
	return cleanText(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsMark(r) || strings.ContainsRune("'-.", r)
	}, maxNameRunes)
}

// SanitizePosition is SanitizeName plus digits and the punctuation job titles use.
func SanitizePosition(s string) string {
	return cleanText(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || strings.ContainsRune("'-./&,()", r)
	}, maxPositionRunes)
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
