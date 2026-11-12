package card

import (
	"strings"
	"testing"

	"entry-permit/internal/dates"
)

func TestMaskNameKeepsTheFirstWordAndInitialsOfTheRest(t *testing.T) {
	cases := map[string]string{
		"Budi Santoso":        "Budi S.",
		"Ahmad Rahman Hakim":  "Ahmad R. H.",
		"  As'adul   bin Ali": "As'adul B. A.",
		"Madonna":             "Madonna",
		"":                    "",
		"   ":                 "",
		"Çelik Özdemir":       "Çelik Ö.",
	}
	for in, want := range cases {
		if got := MaskName(in); got != want {
			t.Errorf("MaskName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLowestParentIsTheNearestOne(t *testing.T) {
	cases := map[string]string{
		"PT LNG (2); PT Karya (1)": "PT Karya",
		"PT LNG (1)":               "PT LNG",
		"PT A (3); PT B (2)":       "PT B",
		"PT First (1); PT Tie (1)": "PT First",
		"PT NoDepth":               "PT NoDepth",
		"PT NoDepth; PT Deep (2)":  "PT Deep",
		"":                         "",
	}
	for in, want := range cases {
		if got := LowestParent(in); got != want {
			t.Errorf("LowestParent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStateFollowsStatusAndValidityDate(t *testing.T) {
	today := dates.Day("2026-11-07")
	cases := []struct {
		name, status string
		until        dates.Day
		want         State
	}{
		{"approved inside validity", "Approved", "2027-06-30", Valid},
		{"approved on its last day", "Approved", "2026-11-07", Valid},
		{"approved but date passed", "Approved", "2026-11-06", Expired},
		{"approved with no date", "Approved", "", Inactive},
		{"renewed", "Renewed by LEP-ZZZZZ", "2027-06-30", Replaced},
		{"waiting for review", "Pending Stage 1", "", Inactive},
		{"rejected", "Rejected", "", Inactive},
	}
	for _, c := range cases {
		if got := StateOf(c.status, c.until, today); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestThemeIsOrangeOnlyForContractorSideEntities(t *testing.T) {
	cases := map[string]string{
		"Contractor":        ThemeContractor,
		"Main Contractor":   ThemeContractor,
		"Subcontractor":     ThemeContractor,
		"Sub-subcontractor": ThemeContractor,
		"sub subcontractor": ThemeContractor,
		"Company":           ThemeCompany,
		"Client":            ThemeCompany,
		"Manpower Supply":   ThemeCompany,
		"Government":        ThemeCompany,
		"":                  ThemeCompany,
	}
	for in, want := range cases {
		if got := ThemeFor(in); got != want {
			t.Errorf("ThemeFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortDate(t *testing.T) {
	if got := ShortDate("2027-06-30"); got != "30 Jun 2027" {
		t.Errorf("got %q", got)
	}
	if got := ShortDate("garbage"); got != "" {
		t.Errorf("a bad date shows nothing, got %q", got)
	}
}

func TestPayloadIsTheLegacyUrlShape(t *testing.T) {
	got := Payload("https://z.example.id/", "LEP-ABCDE")
	if got != "https://z.example.id/?page=ticket&lep=LEP-ABCDE" {
		t.Errorf("payload %q", got)
	}
}

func TestQRSVGDrawsAScannableSquare(t *testing.T) {
	svg, err := QRSVG("https://z.example.id/?page=ticket&lep=LEP-ABCDE")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<svg", `viewBox="0 0 `, "<path", `shape-rendering="crispEdges"`, "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg lacks %q", want)
		}
	}
	// A QR code draws only from the page's own markup: no script, no outside image.
	for _, bad := range []string{"<script", "href=", "<image", "onload"} {
		if strings.Contains(svg, bad) {
			t.Errorf("svg must not contain %q", bad)
		}
	}
	again, _ := QRSVG("https://z.example.id/?page=ticket&lep=LEP-ABCDE")
	if svg != again {
		t.Error("the same text must draw the same code")
	}
	other, _ := QRSVG("https://z.example.id/?page=ticket&lep=LEP-ZZZZZ")
	if svg == other {
		t.Error("different text must draw a different code")
	}
}
