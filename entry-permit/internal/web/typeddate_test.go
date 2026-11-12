package web

import "testing"

// The permit and waiver forms read dates the same forgiving way as the date of birth lookup.
func TestFormDaysAcceptRunTogetherDigitsAndOtherSeparators(t *testing.T) {
	for _, in := range []string{"24071983", "24/071983", "24-07-1983", "24.07.1983", "24/07/1983", " 24 07 1983 "} {
		got, err := parseFormDay(in)
		if err != nil || got != "1983-07-24" {
			t.Errorf("parseFormDay(%q) = %q, %v; want 1983-07-24", in, got, err)
		}
	}
	if got, err := parseFormDay("1/2/2020"); err != nil || got != "2020-02-01" {
		t.Errorf("parseFormDay(1/2/2020) = %q, %v", got, err)
	}
	if got, err := parseFormDay(""); err != nil || got != "" {
		t.Errorf("a blank day must stay blank, got %q, %v", got, err)
	}
	for _, in := range []string{"31041983", "20240724", "2024-07-24", "abc", "24/07"} {
		if got, err := parseFormDay(in); err == nil {
			t.Errorf("parseFormDay(%q) = %q; want an error", in, got)
		}
	}
}
