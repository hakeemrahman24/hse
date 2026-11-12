package main

import (
	"context"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

type noRecipients struct{}

func (noRecipients) EmailsForRole(context.Context, staff.Role) ([]string, error) { return nil, nil }

func TestDailyJobsRunInTheOrderThatLetsEachSeeTheLastOnesWork(t *testing.T) {
	// expiry first (it locks tickets), then the departure check, then the reminders and the digest;
	// each at the hour the admin set.
	pol := policy.Default()
	pol.JobHourExpiry, pol.JobHourDeparture, pol.JobHourStale, pol.JobHourDigest = 1, 2, 3, 4
	s, _ := dailyJobs(nil, dates.SystemClock{}, func() policy.Policy { return pol }, "https://permit.example", nil, nil, noRecipients{})
	want := []struct {
		name string
		hour int
	}{{"permit_expiry", 1}, {"departure_check", 2}, {"stale_reminder", 3}, {"admin_digest", 4}}
	if len(s.Jobs) != len(want) {
		t.Fatalf("%d jobs, want %d", len(s.Jobs), len(want))
	}
	for i, w := range want {
		if s.Jobs[i].Name != w.name || s.Jobs[i].Hour(pol) != w.hour || s.Jobs[i].Run == nil {
			t.Errorf("job %d = %s at %d, want %s at %d", i, s.Jobs[i].Name, s.Jobs[i].Hour(pol), w.name, w.hour)
		}
	}
}
