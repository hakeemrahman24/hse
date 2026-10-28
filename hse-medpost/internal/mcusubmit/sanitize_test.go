package mcusubmit

import (
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Budi Santoso", "Budi Santoso"},
		{"trims and collapses spaces", "  Budi    Santoso  ", "Budi Santoso"},
		{"keeps apostrophe hyphen and dot", "Anne-Marie O'Neil Jr.", "Anne-Marie O'Neil Jr."},
		{"turns a curly apostrophe into a plain one", "O’Neil", "O'Neil"},
		{"strips markup characters and digits", "Budi <script>alert(1)</script>", "Budi scriptalertscript"},
		{"strips digits and symbols", "Budi 123 $%^", "Budi"},
		{"strips control characters", "Bu\x00di\x07 Santoso\n", "Budi Santoso"},
		{"keeps non-latin letters", "Müller Åsa 李雷", "Müller Åsa 李雷"},
		// "e" followed by a combining acute and the single character "é" are the same name; NFC
		// makes them one string so the unique checks and the search treat them alike.
		{"normalises to NFC", "José", "José"},
		{"empty stays empty", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeName(c.in); got != c.want {
				t.Fatalf("SanitizeName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeNameCapsTheLength(t *testing.T) {
	got := SanitizeName(strings.Repeat("a", 400))
	if n := len([]rune(got)); n != maxNameRunes {
		t.Fatalf("length = %d, want %d", n, maxNameRunes)
	}
}

func TestSanitizePositionKeepsCommonPunctuation(t *testing.T) {
	got := SanitizePosition("  HSE / Safety Officer (Site-A), Level 2 & more <b>  ")
	want := "HSE / Safety Officer (Site-A), Level 2 & more b"
	if got != want {
		t.Fatalf("SanitizePosition = %q, want %q", got, want)
	}
	if n := len([]rune(SanitizePosition(strings.Repeat("x", 300)))); n != maxPositionRunes {
		t.Fatalf("length = %d, want %d", n, maxPositionRunes)
	}
}

func TestParseEmail(t *testing.T) {
	good := map[string]string{
		"budi@example.com":       "budi@example.com",
		"  Budi@Example.COM  ":   "budi@example.com",
		"hr.team+mcu@pt-x.co.id": "hr.team+mcu@pt-x.co.id",
	}
	for in, want := range good {
		got, err := ParseEmail(in)
		if err != nil || got != want {
			t.Errorf("ParseEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "plain", "a@b", "@example.com", "budi@", "Budi <budi@example.com>", "a b@example.com",
		"budi@example.com\r\nBcc: x@y.com", "a@@example.com", strings.Repeat("a", 250) + "@example.com",
	}
	for _, in := range bad {
		if got, err := ParseEmail(in); err == nil {
			t.Errorf("ParseEmail(%q) accepted as %q, want an error", in, got)
		}
	}
}
