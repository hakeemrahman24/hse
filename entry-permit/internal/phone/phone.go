// Package phone validates phone numbers the way the legacy system did.
//
// WHY copied exactly: the legacy rule splits a number into a country code and
// a local part by the longest known country code, treats a leading 0 as
// Indonesian, and checks length per country. Existing permits were accepted
// under this rule, so a new rule would reject numbers already on file.
package phone

import (
	"sort"
	"strings"
)

const (
	indonesiaCode   = "62"
	maxCountryCode  = 3
	idLocalMin      = 10 // Indonesian number with its leading 0
	idLocalMax      = 13
	otherLocalMin   = 7
	otherLocalMax   = 15
	minLocalAfterCC = 5 // a split needs more than cc + 5 digits, else it is not a split
)

// ccCodes is the legacy list; sorted longest first (stable, so equal-length
// codes keep their list order) so "852" wins over "85".
var ccCodes = func() []string {
	list := strings.Split(
		"880,886,852,853,855,856,971,972,973,974,975,976,977,992,993,994,995,996,998,"+
			"351,352,353,354,355,356,357,358,359,370,371,372,373,374,375,376,377,380,381,382,385,386,387,389,420,421,"+
			"212,213,216,218,220,234,249,254,255,256,260,263,264,265,266,267,268,269,291,"+
			"30,31,32,33,34,36,39,40,41,43,44,45,46,47,48,49,"+
			"51,52,53,54,55,56,57,58,60,61,62,63,64,65,66,81,82,84,86,90,91,92,93,94,95,98,"+
			"20,27,1,7", ",")
	sort.SliceStable(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	return list
}()

// Digits keeps only the digits of s.
func Digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Split separates a stored number into country code and local digits.
// Blank gives ("62", ""); a leading 0 means Indonesia.
func Split(stored string) (cc, local string) {
	d := Digits(stored)
	if d == "" {
		return indonesiaCode, ""
	}
	if d[0] == '0' {
		return indonesiaCode, d
	}
	for _, c := range ccCodes {
		if !strings.HasPrefix(d, c) || len(d) <= len(c)+minLocalAfterCC {
			continue
		}
		rest := d[len(c):]
		if c == indonesiaCode && rest[0] != '0' {
			rest = "0" + rest
		}
		return c, rest
	}
	return indonesiaCode, d
}

// LocalValid checks length rules for a country code and local number.
func LocalValid(cc, local string) bool {
	cc, local = Digits(cc), Digits(local)
	if cc == "" || local == "" || len(cc) > maxCountryCode {
		return false
	}
	if cc == indonesiaCode {
		if local[0] != '0' {
			local = "0" + local
		}
		return len(local) >= idLocalMin && len(local) <= idLocalMax
	}
	return len(local) >= otherLocalMin && len(local) <= otherLocalMax
}

// Valid reports whether a merged number (country code + local) is acceptable.
func Valid(merged string) bool {
	cc, local := Split(merged)
	return LocalValid(cc, local)
}

// Normalize returns digits only, with a leading 0 promoted to 62.
func Normalize(raw string) string {
	d := Digits(raw)
	if d != "" && d[0] == '0' {
		return indonesiaCode + d[1:]
	}
	return d
}
