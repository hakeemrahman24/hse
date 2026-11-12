// Package card holds the pure rules behind the LEP card and its public page:
// how a name is masked, which parent is shown, whether the card still counts,
// and how the QR code is drawn. It reads no database and no request, so every
// rule is tested without a server.
package card

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
)

// State is what a scanner is told about a card.
type State int

const (
	// Inactive: the permit is not (or not yet) a working LEP.
	Inactive State = iota
	// Valid: Approved and today is on or before the valid-until date.
	Valid
	// Expired: the validity date has passed, or the status says expired.
	Expired
	// Replaced: a newer LEP took over ("Renewed by ...").
	Replaced
)

// StateOf decides the state from the stored status and the validity date.
//
// WHY the date is checked here as well as the status: the status only flips to
// an expired value when the daily sweep runs, so a card can be Approved in the
// database for some hours after its last day. The scanner must not trust that.
func StateOf(status string, validUntil, today dates.Day) State {
	switch {
	case permit.IsRenewed(status):
		return Replaced
	case permit.IsExpiredLike(status):
		return Expired
	case status != permit.StatusApproved || validUntil == "":
		return Inactive
	case validUntil < today: // yyyy-mm-dd strings sort in date order
		return Expired
	}
	return Valid
}

// Label is the short word the page shows for a state.
func (s State) Label() string {
	switch s {
	case Valid:
		return "Valid"
	case Expired:
		return "Expired"
	case Replaced:
		return "Replaced by a newer permit"
	}
	return "Not valid"
}

// MaskName shows the first word in full and only the initial of each other
// word ("Budi Santoso" becomes "Budi S."), so a card photographed in public
// does not give away a whole name.
func MaskName(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return ""
	}
	out := []string{words[0]}
	for _, w := range words[1:] {
		r, _ := utf8.DecodeRuneInString(w)
		out = append(out, string(unicode.ToUpper(r))+".")
	}
	return strings.Join(out, " ")
}

// The two card colours that apply to a work permit (the waiver card, when it
// is added, has a third).
const (
	ThemeCompany    = "company"
	ThemeContractor = "contractor"
)

// ThemeFor picks the card colour from the stored entity type: contractor-side
// entities get the contractor colour, everything else the company colour. The
// old system did the same, so existing cards keep their colour.
func ThemeFor(entity string) string {
	key := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, entity)
	if strings.Contains(key, "contractor") {
		return ThemeContractor
	}
	return ThemeCompany
}

var parentPattern = regexp.MustCompile(`^(.*?)\s*\((\d+)\)$`)

// LowestParent picks the nearest parent company from the stored text
// ("PT LNG (2); PT Karya (1)" gives "PT Karya"). The lowest depth number wins,
// the first listed wins a tie, and a parent without a depth ranks last.
func LowestParent(stored string) string {
	best, bestDepth := "", math.MaxInt
	for _, part := range strings.Split(stored, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, depth := part, math.MaxInt-1 // unnumbered still beats nothing
		if m := parentPattern.FindStringSubmatch(part); m != nil {
			if n, err := strconv.Atoi(m[2]); err == nil {
				name, depth = strings.TrimSpace(m[1]), n
			}
		}
		if depth < bestDepth {
			best, bestDepth = name, depth
		}
	}
	return best
}

// ShortDate writes a day as "30 Jun 2027"; a bad date gives an empty string.
func ShortDate(d dates.Day) string {
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil {
		return ""
	}
	return t.Format("02 Jan 2006")
}

// Payload is the text inside the QR code: the same address shape the old
// system printed, so cards already in circulation and new cards behave alike.
//
// WHY only the LEP number is inside: the code carries no name, no birth date
// and no secret. A photographed card therefore leaks nothing, and the server
// decides what each scanner may see.
func Payload(base, lep string) string {
	return fmt.Sprintf("%s/?page=ticket&lep=%s", strings.TrimRight(base, "/"), lep)
}
