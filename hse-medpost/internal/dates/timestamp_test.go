package dates

import (
	"regexp"
	"testing"
	"time"
)

func TestNormalizeTimestamp(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"browser toISOString", "2026-10-07T09:00:00.000Z", "2026-10-07T09:00:00", false},
		{"offset converts to UTC", "2026-10-07T18:00:00+09:00", "2026-10-07T09:00:00", false},
		{"offset crosses midnight", "2026-10-08T03:00:00+09:00", "2026-10-07T18:00:00", false},
		{"already normalized is read as UTC", "2026-10-07T09:00:00", "2026-10-07T09:00:00", false},
		{"surrounding spaces are trimmed", "  2026-10-07T09:00:00Z ", "2026-10-07T09:00:00", false},
		{"empty stays empty", "", "", false},
		{"garbage", "yesterday", "", true},
		{"month 13", "2026-13-01T00:00:00Z", "", true},
		{"date only", "2026-10-07", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeTimestamp(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("NormalizeTimestamp(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("NormalizeTimestamp(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStampConvertsToUTC(t *testing.T) {
	wit := time.FixedZone("WIT", 9*60*60)
	got := Stamp(time.Date(2026, 10, 7, 18, 30, 5, 123_000_000, wit))
	if want := "2026-10-07T09:30:05"; got != want {
		t.Errorf("Stamp = %q, want %q", got, want)
	}
}

func TestNowStampLayout(t *testing.T) {
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`).MatchString(NowStamp()) {
		t.Errorf("NowStamp() = %q, want YYYY-MM-DDTHH:MM:SS", NowStamp())
	}
}
