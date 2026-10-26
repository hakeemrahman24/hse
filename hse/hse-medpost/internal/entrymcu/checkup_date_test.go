package entrymcu

import (
	"testing"
	"time"

	"hse-medpost/internal/dates"
)

func TestValidateCheckupDateRange(t *testing.T) {
	today := dates.ClinicNow()
	daysAgo := func(n int) string { return today.AddDate(0, 0, -n).Format(dateOnly) }
	tomorrow := today.AddDate(0, 0, 1).Format(dateOnly)

	cases := []struct {
		name    string
		date    string
		wantErr bool
	}{
		{"today is allowed", today.Format(dateOnly), false},
		{"future date is rejected", tomorrow, true},
		{"360 days ago is allowed", daysAgo(360), false},
		{"361 days ago is rejected", daysAgo(361), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCheckupDate(tc.date, true)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateCheckupDate(%s) err = %v, wantErr %v", tc.date, err, tc.wantErr)
			}
		})
	}
}

func TestValidateSubmissionDateRange(t *testing.T) {
	today := dates.ClinicNow()
	daysAgo := func(n int) string { return today.AddDate(0, 0, -n).Format(dateOnly) }
	tomorrow := today.AddDate(0, 0, 1).Format(dateOnly)

	cases := []struct {
		name    string
		date    string
		wantErr bool
	}{
		{"today is allowed", today.Format(dateOnly), false},
		{"future date is rejected", tomorrow, true},
		{"360 days ago is allowed", daysAgo(360), false},
		{"361 days ago is rejected", daysAgo(361), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSubmissionDate(tc.date)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSubmissionDate(%s) err = %v, wantErr %v", tc.date, err, tc.wantErr)
			}
		})
	}
}

// The server clock is UTC but the clinic is UTC+9: at 06:22 clinic time on
// 18 Oct the UTC date is still 17 Oct, and a form dated 18 Oct is today.
func TestClinicTodayIsAheadOfUTCInTheMorning(t *testing.T) {
	instant := time.Date(2026, 10, 17, 23, 22, 0, 0, time.UTC).In(dates.ClinicLocation)
	today := instant.Format(dateOnly)
	if today != "2026-10-18" {
		t.Fatalf("clinic date = %s, want 2026-10-18", today)
	}
	if err := validateSubmissionDateOn("2026-10-18", today); err != nil {
		t.Fatalf("submission date on the clinic's today rejected: %v", err)
	}
	if err := validateCheckupDateOn("2026-10-18", true, today); err != nil {
		t.Fatalf("checkup date on the clinic's today rejected: %v", err)
	}
	if err := validateSubmissionDateOn("2026-10-19", today); err == nil {
		t.Fatal("clinic tomorrow should still be rejected")
	}
	if err := validateSubmissionDateOn("2026-02-30", today); err == nil {
		t.Fatal("impossible calendar date should be rejected")
	}
}
