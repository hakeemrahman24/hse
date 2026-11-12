package position

import (
	"testing"

	"entry-permit/internal/policy"
)

func testResolver() *Resolver {
	return NewResolver(
		[]Canonical{
			{Name: "Banksman / Signalman", Category: "Operations", RiskTier: "HIGH", Certificate: "Banksman cert", CertRequired: "Y"},
			{Name: "Paramedic", Category: "Medical", RiskTier: "MED", Certificate: "Paramedic cert", CertRequired: "Y"},
			{Name: "Welder", Category: "Fabrication", RiskTier: "HIGH", Certificate: "", CertRequired: "N"},
		},
		[]Alias{
			{Alias: "Signal Man", Canonical: "Banksman / Signalman"},
			{Alias: "paramedic", Canonical: "Paramedic"},
		},
		[]KeywordRule{
			{Keywords: "banks, rigger", Canonical: "Banksman / Signalman", RiskTier: "HIGH"},
			{Keywords: "weld", Canonical: "Welder", RiskTier: "HIGH"},
			{Keywords: "ghost", Canonical: "Not In Canonical", RiskTier: "LOW"},
		},
		[]ClassRule{
			{Keywords: "manager, director", Classification: "Management", RiskTier: "LOW"},
			{Keywords: "hr", Classification: "Support", RiskTier: "LOW"},
		},
	)
}

func TestExactAliasIsCaseAndSpaceInsensitive(t *testing.T) {
	r := testResolver().Resolve("  SIGNAL man ", policy.Default())
	if r.Canonical != "Banksman / Signalman" || r.CertRequired != "Y" || r.Certificate != "Banksman cert" {
		t.Errorf("got %+v", r)
	}
	if r.Category != "Operations" || r.RiskTier != "HIGH" {
		t.Errorf("got %+v", r)
	}
}

func TestExactAliasWinsOverKeyword(t *testing.T) {
	// "paramedic" is an alias; it must not fall to a keyword rule.
	r := testResolver().Resolve("Paramedic", policy.Default())
	if r.Canonical != "Paramedic" {
		t.Errorf("got %+v", r)
	}
}

func TestKeywordContainsSemantics(t *testing.T) {
	// WHY: legacy matches substrings, not words ("banks" inside "Banksmanx").
	r := testResolver().Resolve("Senior Banksmanx", policy.Default())
	if r.Canonical != "Banksman / Signalman" || r.CertRequired != "Y" {
		t.Errorf("got %+v", r)
	}
	r = testResolver().Resolve("Pipe WELDER 2", policy.Default())
	if r.Canonical != "Welder" || r.CertRequired != "N" || r.Certificate != "" {
		t.Errorf("got %+v", r)
	}
}

func TestKeywordToUnknownCanonicalHasBlankCert(t *testing.T) {
	r := testResolver().Resolve("ghost operator", policy.Default())
	if r.Canonical != "Not In Canonical" || r.CertRequired != "" || r.Category != "" {
		t.Errorf("got %+v", r)
	}
}

func TestClassificationLayerNeverRequiresCert(t *testing.T) {
	r := testResolver().Resolve("Operations Manager", policy.Default())
	if r.Canonical != "Management" || r.CertRequired != "N" || r.RiskTier != "LOW" || r.Category != "Management" {
		t.Errorf("got %+v", r)
	}
	// Substring quirk is kept: "hr" matches inside "Throttle".
	r = testResolver().Resolve("Throttle", policy.Default())
	if r.Canonical != "Support" {
		t.Errorf("quirk lost: %+v", r)
	}
}

func TestFallbackKeepsTypedTitle(t *testing.T) {
	r := testResolver().Resolve("Zebra Keeper", policy.Default())
	if r.Canonical != "Zebra Keeper" || r.CertRequired != "N#A" || r.RiskTier != "" {
		t.Errorf("got %+v", r)
	}
}

func TestBlankTitle(t *testing.T) {
	r := testResolver().Resolve("   ", policy.Default())
	if r.CertRequired != "" || r.Canonical != "   " {
		t.Errorf("got %+v", r)
	}
}

func TestPerpetualCertValidity(t *testing.T) {
	r := testResolver().Resolve("paramedic", policy.Default())
	if r.CertValidUntilFixed != PerpetualCertValidUntil {
		t.Errorf("got %+v", r)
	}
	r = testResolver().Resolve("welder", policy.Default())
	if r.CertValidUntilFixed != "" {
		t.Errorf("welder should not be perpetual: %+v", r)
	}
}

func TestEmptyKeywordPiecesIgnored(t *testing.T) {
	r := NewResolver(nil, nil, []KeywordRule{{Keywords: " , ,", Canonical: "X"}}, nil).
		Resolve("anything", policy.Default())
	if r.Canonical != "anything" {
		t.Errorf("empty keyword matched everything: %+v", r)
	}
}
