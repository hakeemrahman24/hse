package mcusubmit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestNoticeSettingsStartWithTheDefaultsAndRefuseBadSchedules(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	c := s.NoticeSettings(ctx)
	if c.IntervalHours != 12 || c.StartHour != 8 || c.Timezone != "WIB" || !c.CopyReviewers || c.LastRunAt != "" {
		t.Fatalf("defaults = %+v, want 12 h from 08:00 WIB with copies on", c)
	}
	for _, bad := range []NoticeConfig{
		{IntervalHours: 5, StartHour: 8, Timezone: "WIB"},
		{IntervalHours: 12, StartHour: 24, Timezone: "WIB"},
		{IntervalHours: 12, StartHour: -1, Timezone: "WIB"},
		{IntervalHours: 12, StartHour: 8, Timezone: "Asia/Tokyo"},
		{IntervalHours: 12, StartHour: 8, Timezone: ""},
	} {
		if err := s.SaveNoticeSettings(ctx, bad); !errors.Is(err, ErrBadNoticeSetting) {
			t.Errorf("%+v: %v, want ErrBadNoticeSetting", bad, err)
		}
	}
	if got := s.NoticeSettings(ctx); got.IntervalHours != 12 || got.StartHour != 8 {
		t.Fatal("a refused schedule changed the settings")
	}
	want := NoticeConfig{IntervalHours: 24, StartHour: 6, Timezone: "WIT", CopyReviewers: false}
	if err := s.SaveNoticeSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got := s.NoticeSettings(ctx); got.IntervalHours != 24 || got.StartHour != 6 || got.Timezone != "WIT" || got.CopyReviewers {
		t.Fatalf("saved = %+v, want %+v", got, want)
	}
}

// Slots are every IntervalHours counted from StartHour in the zone: 12 h from 08:00 WIB is 08:00 and
// 20:00 WIB, which is 01:00 and 13:00 UTC.
func TestNoticeSlotsFollowTheStartHourAndTheZone(t *testing.T) {
	cases := []struct {
		name string
		cfg  NoticeConfig
		now  time.Time
		want time.Time
	}{
		{"12h WIB, just before 08:00", NoticeConfig{IntervalHours: 12, StartHour: 8, Timezone: "WIB"}, utc(2026, 11, 10, 0, 59), utc(2026, 11, 9, 13, 0)},
		{"12h WIB, at 08:00", NoticeConfig{IntervalHours: 12, StartHour: 8, Timezone: "WIB"}, utc(2026, 11, 10, 1, 0), utc(2026, 11, 10, 1, 0)},
		{"12h WIB, mid day", NoticeConfig{IntervalHours: 12, StartHour: 8, Timezone: "WIB"}, utc(2026, 11, 10, 9, 0), utc(2026, 11, 10, 1, 0)},
		{"12h WIB, after 20:00", NoticeConfig{IntervalHours: 12, StartHour: 8, Timezone: "WIB"}, utc(2026, 11, 10, 13, 5), utc(2026, 11, 10, 13, 0)},
		{"24h WIT from 06:00", NoticeConfig{IntervalHours: 24, StartHour: 6, Timezone: "WIT"}, utc(2026, 11, 10, 10, 0), utc(2026, 11, 9, 21, 0)},
		{"6h UTC from 00:00", NoticeConfig{IntervalHours: 6, StartHour: 0, Timezone: "UTC"}, utc(2026, 11, 10, 13, 59), utc(2026, 11, 10, 12, 0)},
		{"48h WITA from 08:00, a skipped day", NoticeConfig{IntervalHours: 48, StartHour: 8, Timezone: "WITA"}, utc(2026, 11, 10, 0, 0), utc(2026, 11, 9, 0, 0)},
	}
	for _, c := range cases {
		if got := c.cfg.slotAtOrBefore(c.now); !got.Equal(c.want) {
			t.Errorf("%s: slot %v, want %v", c.name, got.UTC(), c.want)
		}
	}
	// 48 hours stays on one every-second-day grid counted from the fixed anchor (2026-01-01): 9 and
	// 11 November are slots, 10 November is not.
	cfg := NoticeConfig{IntervalHours: 48, StartHour: 8, Timezone: "WITA"}
	if got := cfg.slotAtOrBefore(utc(2026, 11, 11, 23, 0)); !got.Equal(utc(2026, 11, 11, 0, 0)) {
		t.Errorf("48h grid: %v, want 11 November 08:00 WITA", got.UTC())
	}
}

func TestNoticesRunOncePerSlotAndRetryAfterAFailure(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.slot@example.com")
	// Default schedule: 08:00 and 20:00 WIB = 01:00 and 13:00 UTC. Start just after a slot.
	clock.t = utc(2026, 11, 10, 1, 30)
	closeFor(t, s, co, a, "First Worker")

	down := &fakeMailer{fail: errors.New("smtp down")}
	if _, _, ran, err := s.RunNoticesIfDue(ctx, down, "https://p"); !ran || err == nil {
		t.Fatalf("a due run with a failing mailer: ran=%v err=%v, want ran with an error", ran, err)
	}
	if s.NoticeSettings(ctx).LastRunAt != "" {
		t.Fatal("a failed run moved the clock")
	}
	m := &fakeMailer{}
	if mails, docs, ran, err := s.RunNoticesIfDue(ctx, m, "https://p"); err != nil || !ran || mails != 1 || docs != 1 {
		t.Fatalf("retry: ran=%v %d mails %d docs %v", ran, mails, docs, err)
	}
	clock.advance(30 * time.Minute)
	closeFor(t, s, co, a, "Second Worker")
	if _, _, ran, _ := s.RunNoticesIfDue(ctx, m, "https://p"); ran {
		t.Fatal("a second run in the same slot")
	}
	clock.t = utc(2026, 11, 10, 13, 0) // 20:00 WIB, the next slot
	if mails, docs, ran, err := s.RunNoticesIfDue(ctx, m, "https://p"); err != nil || !ran || mails != 1 || docs != 1 {
		t.Fatalf("next slot: ran=%v %d mails %d docs %v", ran, mails, docs, err)
	}
	// Changing the start hour changes the grid: with 14:00 WIB start a 12 h run is due at 14:00 and 02:00.
	if err := s.SaveNoticeSettings(ctx, NoticeConfig{IntervalHours: 12, StartHour: 14, Timezone: "WIB", CopyReviewers: true}); err != nil {
		t.Fatal(err)
	}
	clock.t = utc(2026, 11, 10, 14, 0) // 21:00 WIB: the 14:00 WIB slot (07:00 UTC) passed after the last run? last run was 13:00 UTC
	if _, _, ran, _ := s.RunNoticesIfDue(ctx, m, "https://p"); ran {
		t.Fatal("no new slot since the last run, but a run happened")
	}
	clock.t = utc(2026, 11, 10, 19, 0) // 02:00 WIB the next day
	if _, _, ran, _ := s.RunNoticesIfDue(ctx, m, "https://p"); !ran {
		t.Fatal("the 02:00 WIB slot passed, a run was due")
	}
}

func TestTheCopySwitchTurnsReviewerCopiesOff(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.sw@example.com")
	mustReviewer(t, s, "doc@example.com")
	if err := s.SaveNoticeSettings(ctx, NoticeConfig{IntervalHours: 12, StartHour: 8, Timezone: "WIB", CopyReviewers: false}); err != nil {
		t.Fatal(err)
	}
	closeFor(t, s, co, a, "Switch Worker")
	m := &fakeMailer{}
	if mails, _, err := s.RunClosedNotices(ctx, m, "https://p"); err != nil || mails != 1 {
		t.Fatalf("run: %d mails, %v", mails, err)
	}
	for _, msg := range m.sent {
		if msg.To == "doc@example.com" {
			t.Fatalf("a reviewer got a copy with the switch off: %+v", msg)
		}
	}
	if len(m.sent) != 1 {
		t.Fatalf("%d mails, want only the submitter's", len(m.sent))
	}
}

func TestAdminSavesTheNoticeSchedule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, member := e.signIn("hr.budi", "a-long-passphrase-1")
	if rec := e.post("/mcu-submit/admin/notices", url.Values{"hours": {"6"}, "start": {"9"}, "zone": {"WIB"}}, member); rec.Code != http.StatusNotFound {
		t.Errorf("a submitter saving the schedule = %d, want 404", rec.Code)
	}
	_, admin := e.signIn("admin", "pelican-orbit-77")
	page := e.get("/mcu-submit/admin/digest", admin)
	for _, want := range []string{`name="start"`, `name="zone"`, `value="WITA"`, `name="copy"`, "Next run"} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("the page lacks %q (status %d)", want, page.Code)
		}
	}
	if rec := e.post("/mcu-submit/admin/notices", url.Values{"hours": {"6"}, "start": {"9"}, "zone": {"WITA"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rec.Code)
	}
	if c := e.store.NoticeSettings(ctx); c.IntervalHours != 6 || c.StartHour != 9 || c.Timezone != "WITA" || c.CopyReviewers {
		t.Fatalf("saved = %+v, want 6 h from 09:00 WITA with copies off (the checkbox was left out)", c)
	}
	for _, bad := range []url.Values{
		{"hours": {"7"}, "start": {"9"}, "zone": {"WIB"}},
		{"hours": {"6"}, "start": {"25"}, "zone": {"WIB"}},
		{"hours": {"6"}, "start": {"x"}, "zone": {"WIB"}},
		{"hours": {"6"}, "start": {"9"}, "zone": {"Mars"}},
	} {
		if rec := e.post("/mcu-submit/admin/notices", bad, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%v = %d, want 400", bad, rec.Code)
		}
	}
	if c := e.store.NoticeSettings(ctx); c.IntervalHours != 6 || c.StartHour != 9 {
		t.Fatal("a refused value changed the schedule")
	}
}
