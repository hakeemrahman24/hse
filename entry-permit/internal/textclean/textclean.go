// Package textclean cleans and checks what people type into the request forms: email addresses and
// person names. The browser tidies the same way while they type (web/static/inputs.js), but this is the
// version that counts, because the server never trusts the browser.
package textclean

import (
	"regexp"
	"strings"
	"unicode"
)

// invisible are the characters that paste in from chat apps and web pages and break an address without
// showing: zero-width spaces and joiners, the byte order mark, soft hyphen, and the no-break spaces.
var invisible = strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "", "\u00ad", "", "\u2060", "",
	"\u00a0", "", " ", "", "\u3000", "", "\uff20", "@", "\u3002", ".", "\uff0e", ".")

// Email is the clean-up for an address someone typed or pasted: every space and invisible character is
// removed, it is lower-cased, a "mailto:" in front, angle brackets or quotes around it and sentence
// punctuation after it are dropped. It does not decide whether the result is valid; see ValidEmail.
func Email(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(invisible.Replace(s)), ""))
	s = strings.TrimPrefix(s, "mailto:")
	s = strings.TrimLeft(s, "<\"'`([")
	return strings.TrimRight(s, ">\"'`)].,;:")
}

var (
	localChars  = regexp.MustCompile("^[a-z0-9!#$%&'*+/=?^_`{|}~.-]+$")
	domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	tldShape    = regexp.MustCompile(`^([a-z]{2,63}|xn--[a-z0-9-]{2,59})$`)
)

// ValidEmail is the common format rule for an address already cleaned by Email: one @; a local part of
// at most 64 characters made of the usual letters, digits and symbols, with no dot at either end and no
// two dots in a row; a domain of at least two labels (letters, digits and hyphens, none starting or ending
// with a hyphen, at most 63 long) ending in a top-level domain of letters; at most 254 characters in all.
// Quoted local parts, comments and IP-literal domains are refused: nobody at a company uses them, and a
// mistyped address often looks like one.
func ValidEmail(s string) bool {
	if s == "" || len(s) > 254 || strings.Count(s, "@") != 1 {
		return false
	}
	at := strings.IndexByte(s, '@')
	local, domain := s[:at], s[at+1:]
	if local == "" || len(local) > 64 || !localChars.MatchString(local) ||
		strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for i, l := range labels {
		if l == "" || len(l) > 63 {
			return false
		}
		if i == len(labels)-1 {
			if !tldShape.MatchString(l) {
				return false
			}
		} else if !domainLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// popular are the mail domains most mistyped, used only to offer "Did you mean ...?".
var popular = []string{"gmail.com", "yahoo.com", "yahoo.co.id", "hotmail.com", "outlook.com", "icloud.com", "live.com", "proton.me", "protonmail.com"}

// EmailSuggestion returns a corrected address when the domain is one letter away from a popular one
// (gmial.com, gmail.co, hotmial.com), or "" when there is nothing to suggest. It is a hint, never a rule:
// the person decides.
func EmailSuggestion(clean string) string {
	at := strings.LastIndexByte(clean, '@')
	if at < 1 {
		return ""
	}
	domain := clean[at+1:]
	for _, p := range popular {
		if domain == p {
			return ""
		}
	}
	for _, p := range popular {
		if oneEditAway(domain, p) {
			return clean[:at+1] + p
		}
	}
	return ""
}

// oneEditAway is true when a and b differ by one inserted, removed or changed letter, or one swapped pair.
func oneEditAway(a, b string) bool {
	if a == b {
		return false
	}
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > 1 || d < -1 {
		return false
	}
	if len(ra) == len(rb) {
		var diff []int
		for i := range ra {
			if ra[i] != rb[i] {
				diff = append(diff, i)
			}
		}
		if len(diff) == 1 {
			return true
		}
		return len(diff) == 2 && diff[1] == diff[0]+1 && ra[diff[0]] == rb[diff[1]] && ra[diff[1]] == rb[diff[0]]
	}
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	i := 0
	for i < len(rb) && ra[i] == rb[i] {
		i++
	}
	return string(ra[:i])+string(ra[i+1:]) == string(rb)
}

// particles stay lower case inside a name ("Abdul Rahman bin Ahmad", "Ludwig van Beethoven").
var particles = map[string]bool{"bin": true, "binti": true, "bte": true, "binte": true, "van": true, "von": true, "de": true, "del": true,
	"della": true, "da": true, "dos": true, "das": true, "di": true, "du": true, "la": true, "le": true, "ter": true, "ten": true, "al": true, "el": true}

var romanNumeral = regexp.MustCompile(`^(ii|iii|iv|vi|vii|viii|ix)$`)

// Name is the clean-up for a person's name: only letters, spaces, apostrophes, hyphens, dots and commas are
// kept (digits, @, brackets and other symbols are dropped), curly quotes and long dashes become plain ones,
// repeated spaces and punctuation collapse, and the name is put in title style: "BUDI SANTOSO" and
// "budi santoso" become "Budi Santoso"; particles such as bin, van and de stay small; initials ("A. B.")
// and Roman numerals ("III") stay capital; and a word typed with an inner capital ("McDonald", "DeShawn")
// is left as typed. Names that are empty after cleaning give "".
func Name(s string) string {
	var b strings.Builder
	for _, r := range invisible.Replace(s) {
		switch {
		case unicode.IsLetter(r), unicode.Is(unicode.Mn, r):
			b.WriteRune(r)
		case r == '’' || r == '‘' || r == '`' || r == '´' || r == '\'':
			b.WriteRune('\'')
		case r == '–' || r == '—' || r == '-':
			b.WriteRune('-')
		case r == '.' || r == ',':
			b.WriteRune(r)
		default: // spaces and everything else: a gap, so "Budi_Santoso" and "Budi/Santoso" do not glue together
			b.WriteRune(' ')
		}
	}
	text := collapse(b.String())
	if text == "" {
		return ""
	}
	words := strings.Fields(text)
	for i, w := range words {
		words[i] = titleWord(w, i == 0)
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

// collapse squeezes runs of spaces and repeated punctuation and trims punctuation off the ends.
func collapse(s string) string {
	for _, p := range []string{"..", ",,", "--", "''"} {
		for strings.Contains(s, p) {
			s = strings.ReplaceAll(s, p, p[:1])
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " ,", ",")
	return strings.Trim(s, " .,-'")
}

func titleWord(w string, first bool) string {
	lower := strings.ToLower(w)
	trimmed := strings.Trim(lower, ".,")
	if !first && particles[trimmed] {
		return lower
	}
	if romanNumeral.MatchString(trimmed) {
		return strings.ToUpper(w)
	}
	if hasInnerCapital(w) {
		return w
	}
	// Capital after the word start and after every hyphen or apostrophe: "o'neil" -> "O'Neil", "anne-marie" -> "Anne-Marie".
	rs := []rune(lower)
	up := true
	for i, r := range rs {
		if up && unicode.IsLetter(r) {
			rs[i] = unicode.ToUpper(r)
			up = false
		}
		if r == '-' || r == '\'' || r == '.' {
			up = true
		}
	}
	return string(rs)
}

// hasInnerCapital is true for a word that starts with a capital and has another capital after a small
// letter ("McDonald", "DeShawn"): the person typed it that way on purpose.
func hasInnerCapital(w string) bool {
	rs := []rune(w)
	if len(rs) < 3 || !unicode.IsUpper(rs[0]) {
		return false
	}
	seenLower := false
	for _, r := range rs[1:] {
		if unicode.IsLower(r) {
			seenLower = true
		} else if unicode.IsUpper(r) && seenLower {
			return true
		}
	}
	return false
}
