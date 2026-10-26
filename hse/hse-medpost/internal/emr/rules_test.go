package emr

import (
	"strings"
	"testing"
)

func TestCleanHeadOfHousehold(t *testing.T) {
	cases := map[string]string{
		"  budi   SANTOSO ":  "Budi Santoso",
		"budi123 santoso!":   "Budi Santoso",
		"ba'u":               "Ba'u",
		"anak-agung  made":   "Anak-Agung Made",
		"h. abdul r.":        "H. Abdul R.",
		"<script>x</script>": "Scriptxscript",
		"":                   "",
		"1234 !!!":           "",
		"yohanes pétrus":     "Yohanes Pétrus",
	}
	for in, want := range cases {
		if got := CleanHeadOfHousehold(in); got != want {
			t.Errorf("CleanHeadOfHousehold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgeAt(t *testing.T) {
	cases := []struct {
		dob, on string
		want    int
		ok      bool
	}{
		{"1990-06-15", "2026-06-14", 35, true},
		{"1990-06-15", "2026-06-15", 36, true},
		{"2000-02-29", "2026-02-28", 25, true},
		{"2026-10-20", "2026-10-20", 0, true},
		{"", "2026-10-20", 0, false},
		{"2030-01-01", "2026-10-20", 0, false},
		{"garbage", "2026-10-20", 0, false},
	}
	for _, c := range cases {
		got, ok := AgeAt(c.dob, c.on)
		if got != c.want || ok != c.ok {
			t.Errorf("AgeAt(%q,%q) = %d,%v want %d,%v", c.dob, c.on, got, ok, c.want, c.ok)
		}
	}
}

func TestISOWeek(t *testing.T) {
	cases := map[string]string{
		"2026-01-01": "2026-W01",
		"2026-10-20": "2026-W43",
		"2027-01-01": "2026-W53",
		"bad":        "",
	}
	for in, want := range cases {
		if got := ISOWeek(in); got != want {
			t.Errorf("ISOWeek(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlideInitialCandidates(t *testing.T) {
	got := SlideInitialCandidates("Budi Santoso")
	if got[0] != "BSU1" || got[1] != "BSU2" || got[8] != "BSU9" || got[9] != "BSD1" {
		t.Errorf("Budi Santoso starts %v", got[:10])
	}
	if got := SlideInitialCandidates("Ahmad Yani Putra Wijaya"); got[0] != "AYP1" {
		t.Errorf("four words start %q, want AYP1", got[0])
	}
	if got := SlideInitialCandidates("Budi"); got[0] != "BUD1" {
		t.Errorf("one word starts %q, want BUD1", got[0])
	}
	for _, name := range []string{"Budi Santoso", "Li", "123", "", "Ño Ñoño"} {
		seen := map[string]bool{}
		for i, c := range SlideInitialCandidates(name) {
			letters, digit := c[:3], c[3]
			if len(c) != 4 || strings.Trim(letters, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" || digit < '1' || digit > '9' {
				t.Fatalf("%q candidate %d = %q: want 3 capital letters and a digit 1-9", name, i, c)
			}
			if seen[c] {
				t.Fatalf("%q repeats %q", name, c)
			}
			seen[c] = true
		}
		if len(seen) < 5000 {
			t.Errorf("%q has only %d candidates; the fallback should give plenty", name, len(seen))
		}
	}
}

func TestTestID(t *testing.T) {
	cases := map[int64]string{1: "MAL-00001", 42: "MAL-00042", 12345: "MAL-12345", 123456: "MAL-123456"}
	for id, want := range cases {
		if got := TestID(id); got != want {
			t.Errorf("TestID(%d) = %q, want %q", id, got, want)
		}
	}
}
