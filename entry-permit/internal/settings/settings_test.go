package settings

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/policy"
	"entry-permit/migrations"
)

type tick struct{ now dates.Moment }

func (t *tick) Now() dates.Moment { return t.now }
func (t *tick) Today() dates.Day  { return t.now.Day() }

func newSvc(t *testing.T) (*Service, *tick, *sql.DB) {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	c := &tick{now: "2026-11-06 10:00:00"}
	s := New(d, c)
	if err := s.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, c, d
}

func TestEveryDefaultMatchesPolicyDefault(t *testing.T) {
	// The code defaults and the settings defaults must never drift apart.
	vals := map[string]string{}
	for _, d := range Defs {
		vals[d.Key] = d.Default
	}
	got, bad := Build(vals)
	if len(bad) != 0 {
		t.Fatalf("defaults rejected: %v", bad)
	}
	want := policy.Default()
	if got.OnshoreWeeks != want.OnshoreWeeks || got.McuLepGraceUnit != want.McuLepGraceUnit ||
		got.SessionHours != want.SessionHours || got.BlockGateAtCap != want.BlockGateAtCap ||
		len(got.PerpetualCertPositions) != len(want.PerpetualCertPositions) {
		t.Fatalf("Build(defaults) = %+v want %+v", got, want)
	}
}

func TestSeedKeepsAdminValues(t *testing.T) {
	s, _, _ := newSvc(t)
	ctx := context.Background()
	if err := s.Set(ctx, "arif", "onshore_weeks", "10"); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(ctx); err != nil { // a restart
		t.Fatal(err)
	}
	if got := s.Policy().OnshoreWeeks; got != 10 {
		t.Fatalf("OnshoreWeeks=%d after reseed, want 10", got)
	}
}

func TestSetValidates(t *testing.T) {
	s, _, _ := newSvc(t)
	ctx := context.Background()
	cases := []struct{ key, val string }{
		{"onshore_weeks", "0"},       // below min
		{"onshore_weeks", "999"},     // above max
		{"onshore_weeks", "abc"},     // not a number
		{"mcu_lep_grace_unit", "x"},  // not an option
		{"block_gate_at_cap", "yes"}, // not on/off
		{"perpetual_cert_positions", "  "},
		{"no_such_key", "1"},
		{"mcu_portal_url", "medical-activities.example.com"}, // no scheme
		{"mcu_portal_url", "ftp://example.com/x"},
		{"mcu_portal_url", "https://exa mple.com"},
	}
	for _, c := range cases {
		err := s.Set(ctx, "arif", c.key, c.val)
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("Set(%s,%q) err=%v, want ValidationError", c.key, c.val, err)
		}
	}
	if got := s.Policy().OnshoreWeeks; got != 8 {
		t.Fatalf("rejected values changed the policy: %d", got)
	}
}

func TestSetAppliesNextCallAndAudits(t *testing.T) {
	s, _, d := newSvc(t)
	ctx := context.Background()
	_ = s.Policy() // warm the cache
	if err := s.Set(ctx, "arif", "mcu_lep_grace_unit", "days"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "arif", "block_gate_at_cap", "on"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "arif", "perpetual_cert_positions", "Nurse, Doctor / Site Physician"); err != nil {
		t.Fatal(err)
	}
	p := s.Policy()
	if p.McuLepGraceUnit != "days" || !p.BlockGateAtCap || len(p.PerpetualCertPositions) != 2 || p.PerpetualCertPositions[1] != "Doctor / Site Physician" {
		t.Fatalf("policy not updated at once: %+v", p)
	}
	rows, err := audit.Recent(ctx, d, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("audit rows=%d err=%v", len(rows), err)
	}
	if rows[2].Target != "setting:mcu_lep_grace_unit" || rows[2].Old != "weeks" || rows[2].New != "days" || rows[2].Actor != "arif" {
		t.Fatalf("audit row = %+v", rows[2])
	}
}

func TestWaiverVisitorToggleIsOnByDefaultAndSwitchesOff(t *testing.T) {
	// Production value in the Reference sheet: waiverVisitorCompanyEnabled = Y.
	s, _, _ := newSvc(t)
	if !s.Policy().WaiverVisitorEnabled {
		t.Fatal("visitor-company waivers must be on by default")
	}
	if err := s.Set(context.Background(), "arif", "waiver_visitor_company_enabled", "off"); err != nil {
		t.Fatal(err)
	}
	if s.Policy().WaiverVisitorEnabled {
		t.Fatal("the switch did not reach the policy")
	}
}

func TestTicketAccessLimitsAreSettings(t *testing.T) {
	// Production values from the legacy code (tickets doc section 6): 5 tries, 15 minutes, 30-minute session.
	s, _, _ := newSvc(t)
	p := s.Policy()
	if p.TicketMaxTries != 5 || p.TicketLockMinutes != 15 || p.TicketSessionMinutes != 30 {
		t.Fatalf("defaults %d %d %d", p.TicketMaxTries, p.TicketLockMinutes, p.TicketSessionMinutes)
	}
	if err := s.Set(context.Background(), "arif", "ticket_access_max_tries", "3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().TicketMaxTries; got != 3 {
		t.Fatalf("max tries = %d after the change", got)
	}
}

func TestSetSameValueWritesNoAudit(t *testing.T) {
	s, _, d := newSvc(t)
	if err := s.Set(context.Background(), "arif", "onshore_weeks", "8"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := audit.Recent(context.Background(), d, 10); len(rows) != 0 {
		t.Fatalf("unchanged save wrote %d audit rows", len(rows))
	}
}

func TestResetReturnsToDefault(t *testing.T) {
	s, _, _ := newSvc(t)
	ctx := context.Background()
	_ = s.Set(ctx, "arif", "onshore_weeks", "10")
	if err := s.Reset(ctx, "arif", "onshore_weeks"); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().OnshoreWeeks; got != 8 {
		t.Fatalf("after reset OnshoreWeeks=%d", got)
	}
}

func TestBadStoredValueFallsBackToDefault(t *testing.T) {
	s, _, d := newSvc(t)
	// Simulates a hand edit in the database: the rules must not break.
	if _, err := d.Exec(`UPDATE setting SET value='oops' WHERE setting_key='onshore_weeks'`); err != nil {
		t.Fatal(err)
	}
	s.Invalidate()
	if got := s.Policy().OnshoreWeeks; got != 8 {
		t.Fatalf("bad stored value gave %d, want default 8", got)
	}
}

func TestCacheExpiresAfterTTL(t *testing.T) {
	s, c, d := newSvc(t)
	_ = s.Policy()
	// Change behind the service's back (another instance): visible after the TTL only.
	if _, err := d.Exec(`UPDATE setting SET value='9' WHERE setting_key='onshore_weeks'`); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().OnshoreWeeks; got != 8 {
		t.Fatalf("cache bypassed too early: %d", got)
	}
	c.now = "2026-11-06 10:02:00"
	if got := s.Policy().OnshoreWeeks; got != 9 {
		t.Fatalf("cache not refreshed after TTL: %d", got)
	}
}

func TestGroupsListsEverySettingOnce(t *testing.T) {
	s, _, _ := newSvc(t)
	groups, err := s.Groups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, g := range groups {
		n += len(g.Items)
	}
	if n != len(Defs) {
		t.Fatalf("groups hold %d settings, want %d", n, len(Defs))
	}
}

func TestJobHoursAreSettingsThatStartAtSixInTheMorning(t *testing.T) {
	// Legacy triggers: the departure check and the permit expiry check both ran at 06:00 Jakarta.
	s, _, _ := newSvc(t)
	p := s.Policy()
	if p.JobHourDeparture != 6 || p.JobHourExpiry != 6 {
		t.Fatalf("defaults %d %d", p.JobHourDeparture, p.JobHourExpiry)
	}
	ctx := context.Background()
	if err := s.Set(ctx, "arif", "job_hour_departure_check", "7"); err != nil {
		t.Fatal(err)
	}
	if got := s.Policy().JobHourDeparture; got != 7 {
		t.Fatalf("departure hour = %d after the change", got)
	}
	// an hour of the day: 0 to 23 only
	for _, bad := range []string{"24", "-1", "six"} {
		if err := s.Set(ctx, "arif", "job_hour_expiry_check", bad); err == nil {
			t.Errorf("job hour %q was accepted", bad)
		}
	}
}

func TestReminderDigestAndDrainSettingsKeepTheLegacySchedule(t *testing.T) {
	// Legacy: stale reminder 07:00 daily, digest 08:00 every 2nd Monday with a 30 day window,
	// 7 days "urgent", 3 days of grace "urgent"; the outbox drain ran hourly.
	s, _, _ := newSvc(t)
	p := s.Policy()
	got := []int{p.JobHourStale, p.JobHourDigest, p.DigestWeeks, p.DigestWeekday, p.DigestExpiryDays, p.DigestUrgentDays, p.DigestGraceUrgentDays, p.OutboxDrainMinutes}
	want := []int{7, 8, 2, 1, 30, 7, 3, 60}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("defaults %v, want %v", got, want)
		}
	}
	ctx := context.Background()
	if err := s.Set(ctx, "arif", "digest_weekday", "3"); err != nil {
		t.Fatal(err)
	}
	if s.Policy().DigestWeekday != 3 {
		t.Fatal("weekday change not applied")
	}
	// weekday is 1 (Monday) to 7 (Sunday); weeks between digests is at least 1; the drain is never faster than a minute
	for _, c := range [][2]string{{"digest_weekday", "0"}, {"digest_weekday", "8"}, {"digest_weeks", "0"}, {"outbox_drain_minutes", "0"}, {"job_hour_stale_reminder", "24"}} {
		if err := s.Set(ctx, "arif", c[0], c[1]); err == nil {
			t.Errorf("%s=%s was accepted", c[0], c[1])
		}
	}
}

func TestMcuPortalAddressIsASettingAndCanBeEmptied(t *testing.T) {
	s, _, _ := newSvc(t)
	ctx := context.Background()
	const url = "https://medical-activities.ayahaif.my.id/mcu-submit/signin"
	if err := s.Set(ctx, "arif", "mcu_portal_url", " "+url+" "); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if got := s.Policy().McuPortalURL; got != url {
		t.Fatalf("McuPortalURL = %q, want %q", got, url)
	}
	if err := s.Set(ctx, "arif", "mcu_portal_url", ""); err != nil {
		t.Fatalf("an empty address must be allowed (it hides the links): %v", err)
	}
	if got := s.Policy().McuPortalURL; got != "" {
		t.Fatalf("McuPortalURL = %q after clearing, want empty", got)
	}
}
