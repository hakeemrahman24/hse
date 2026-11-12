// Package position resolves a typed job title to a canonical position.
//
// WHY: the legacy sheet resolved titles in three layers (exact alias, keyword
// rules, classification rules) with "contains" substring matching. This port
// copies that behaviour exactly so imported permits resolve the same way
// (plan section 3b); any cleanup, such as word-boundary matching, is a later
// owner decision, not something to change by accident here.
package position

import (
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// CertRequired values: "Y", "N", "N#A" (no match anywhere) or "" (unknown).
const certNotApplicable = "N#A"

// PerpetualCertValidUntil is the fixed "never expires" certificate date.
const PerpetualCertValidUntil dates.Day = "2090-12-31"

// Canonical is one official position. CertRequired lives here, once, instead
// of on every alias row (the legacy rows disagreed with each other).
type Canonical struct {
	Name, Category, RiskTier, Certificate, CertRequired string
}

// Alias maps one typed spelling to a canonical position name.
type Alias struct{ Alias, Canonical string }

// KeywordRule maps comma-separated keywords to a canonical position. Rules
// are tried in slice order, because order decides the winner.
type KeywordRule struct{ Keywords, Canonical, RiskTier string }

// ClassRule maps keywords to a classification (Executive, Management, ...).
type ClassRule struct{ Keywords, Classification, RiskTier string }

// Result is what a permit stores for the position.
type Result struct {
	Canonical, Category, RiskTier, CertRequired, Certificate string
	CertValidUntilFixed                                      dates.Day
}

// Resolver holds the lookup tables; build it once per request/job.
type Resolver struct {
	canon    []Canonical
	aliases  []Alias
	keywords []KeywordRule
	classes  []ClassRule
}

// NewResolver copies nothing: callers must treat the slices as read-only.
func NewResolver(c []Canonical, a []Alias, k []KeywordRule, cl []ClassRule) *Resolver {
	return &Resolver{canon: c, aliases: a, keywords: k, classes: cl}
}

// normalize mirrors the legacy normalize_: trim and lower-case.
func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// matchesAny reports whether any comma-separated keyword is contained in key.
// Blank pieces are skipped, otherwise "" would match every title.
func matchesAny(keywords, key string) bool {
	for _, kw := range strings.Split(keywords, ",") {
		kw = normalize(kw)
		if kw != "" && strings.Contains(key, kw) {
			return true
		}
	}
	return false
}

func (r *Resolver) findCanonical(name string) (Canonical, bool) {
	want := normalize(name)
	for _, c := range r.canon {
		if normalize(c.Name) == want {
			return c, true
		}
	}
	return Canonical{}, false
}

// withCert builds a result from a canonical row. The certificate name is only
// kept when a certificate is required, as in the legacy code.
func withCert(c Canonical, risk string) Result {
	res := Result{Canonical: c.Name, Category: c.Category, RiskTier: risk, CertRequired: c.CertRequired}
	if c.CertRequired == "Y" {
		res.Certificate = c.Certificate
	}
	return res
}

// Resolve applies the three layers in order, then the perpetual-cert rule.
func (r *Resolver) Resolve(raw string, p policy.Policy) Result {
	res := r.resolve(raw)
	if p.IsPerpetualCertPosition(res.Canonical) {
		res.CertValidUntilFixed = PerpetualCertValidUntil
	}
	return res
}

func (r *Resolver) resolve(raw string) Result {
	key := normalize(raw)
	if key == "" {
		return Result{Canonical: raw}
	}

	// Layer 1: exact alias.
	for _, a := range r.aliases {
		if normalize(a.Alias) == key {
			if c, ok := r.findCanonical(a.Canonical); ok {
				return withCert(c, c.RiskTier)
			}
		}
	}

	// Layer 2: keyword rule; cert info comes from the canonical row, and is
	// blank when the rule points at a name that is not in the canonical list.
	for _, k := range r.keywords {
		if !matchesAny(k.Keywords, key) {
			continue
		}
		if c, ok := r.findCanonical(k.Canonical); ok {
			return withCert(c, k.RiskTier)
		}
		return Result{Canonical: k.Canonical, RiskTier: k.RiskTier}
	}

	// Layer 3: classification; these roles never require a certificate.
	for _, c := range r.classes {
		if matchesAny(c.Keywords, key) {
			return Result{Canonical: c.Classification, Category: c.Classification, RiskTier: c.RiskTier, CertRequired: "N"}
		}
	}

	// Fallback: keep what was typed and mark certificate "not applicable".
	return Result{Canonical: raw, CertRequired: certNotApplicable}
}
