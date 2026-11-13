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

// addRecord inserts one McuRecord the way the clinic's form would leave it.
func addRecord(t *testing.T, s *Store, patientID int64, checkUpID, result, validUntil, status string, terminal int) {
	t.Helper()
	_, err := s.DB.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, ResultCriteria, ResultValidUntil, CheckUpStatus, IsTerminal, WorkPosition)
		VALUES (?, '2026-01-01T00:00:00', '2026-01-01', ?, ?, ?, ?, ?, 'Welder')`,
		patientID, checkUpID, result, validUntil, status, terminal)
	if err != nil {
		t.Fatal(err)
	}
}

// sentFor submits and sends one document for an existing patient, so the submitter "owns" that worker.
func sentFor(t *testing.T, s *Store, sub, patientID int64, name string) {
	t.Helper()
	ctx := context.Background()
	in := sampleDoc(name)
	in.PatientID = patientID
	id, err := s.AddSubmission(ctx, sub, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetScanResult(ctx, id, ScanClean); err != nil {
		t.Fatal(err)
	}
	batch, err := s.OpenBatch(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendBatch(ctx, sub, batch); err != nil {
		t.Fatal(err)
	}
}

// reminderWorld: today is 2026-10-28. Budi's workers expire in 34 days (inside 60), Siti's is far off.
func reminderWorld(t *testing.T) (*Store, *fixedClock, int64, int64) {
	t.Helper()
	s, clock, co := newTestStore(t)
	hr := activeSubmitter(t, s, co, "hrone@example.com")
	other := activeSubmitter(t, s, co, "hrtwo@example.com")
	soon := seedWorker(t, s, "Soon Worker", "1990-01-01", co)
	late := seedWorker(t, s, "Late Worker", "1990-01-02", co)
	addRecord(t, s, soon, "C-1", "Fit", "2026-12-01", "Open", 1)
	addRecord(t, s, late, "C-2", "Fit", "2027-06-01", "Open", 1)
	return s, clock, hr, other
}

// companyOf is the company of a submitter account: the reminders and the lists work per company.
func companyOf(t *testing.T, s *Store, submitter int64) int64 {
	t.Helper()
	u, err := s.ByID(context.Background(), submitter)
	if err != nil {
		t.Fatal(err)
	}
	return u.CompanyID
}

func TestReminderSettingsDefaultsAndLimits(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	c, err := s.ReminderSettings(ctx)
	if err != nil || c.Enabled || c.DaysBefore != 60 || c.RepeatDays != 30 {
		t.Fatalf("defaults = %+v, %v; want off, 60, 30", c, err)
	}
	if err := s.SaveReminderSettings(ctx, ReminderConfig{Enabled: true, DaysBefore: 90, RepeatDays: 14}, 0); err != nil {
		t.Fatal(err)
	}
	if c, _ = s.ReminderSettings(ctx); !c.Enabled || c.DaysBefore != 90 || c.RepeatDays != 14 {
		t.Fatalf("saved = %+v", c)
	}
	for _, bad := range []ReminderConfig{{DaysBefore: 0, RepeatDays: 30}, {DaysBefore: 366, RepeatDays: 30}, {DaysBefore: 60, RepeatDays: 0}, {DaysBefore: 60, RepeatDays: 366}} {
		if err := s.SaveReminderSettings(ctx, bad, 0); !errors.Is(err, ErrBadReminderSetting) {
			t.Errorf("%+v = %v, want ErrBadReminderSetting", bad, err)
		}
	}
	if c, _ = s.ReminderSettings(ctx); c.DaysBefore != 90 {
		t.Error("a refused save changed the settings")
	}
}

func TestExpiringForCompanyListsOnlyThatCompanysCurrentCheckupsInTheWindow(t *testing.T) {
	s, _, hr, other := reminderWorld(t)
	ctx := context.Background()

	co := companyOf(t, s, hr)
	got, err := s.ExpiringForCompany(ctx, co, 60, 0)
	if err != nil || len(got) != 1 || got[0].Name != "Soon Worker" || got[0].ValidUntil != "2026-12-01" {
		t.Fatalf("hr sees %+v, %v; want only Soon Worker until 2026-12-01", got, err)
	}
	if got, _ = s.ExpiringForCompany(ctx, co, 300, 0); len(got) != 2 || got[0].Name != "Soon Worker" {
		t.Errorf("wide window = %+v, want both, soonest first", got)
	}
	if got, _ = s.ExpiringForCompany(ctx, companyOf(t, s, other)+1000, 300, 0); len(got) != 0 {
		t.Errorf("another company sees %+v", got)
	}
}

func TestExpiringForSkipsRecordsThatNeedNoReminder(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	activeSubmitter(t, s, co, "hrone@example.com")
	cases := []struct {
		result, status string
		terminal       int
	}{
		{"Unfit", "Open", 1}, {"Fit", "Close", 1}, {"Fit", "Archived / Exit", 1}, {"Fit", "Open", 0}, {"TemporaryUnfit", "Open", 1},
	}
	for i, c := range cases {
		name := "Skipped " + string(rune('A'+i))
		w := seedWorker(t, s, name, "1990-01-01", co)
		addRecord(t, s, w, "S-"+string(rune('A'+i)), c.result, "2026-12-01", c.status, c.terminal)
	}
	expired := seedWorker(t, s, "Already Expired", "1990-01-01", co)
	addRecord(t, s, expired, "S-X", "Fit", "2026-10-01", "Open", 1) // 27 days ago: only the mail's grace period keeps it
	if got, _ := s.ExpiringForCompany(ctx, co, 60, 0); len(got) != 0 {
		t.Fatalf("got %+v, want none: unfit, closed, archived, superseded and already expired are not chased", got)
	}
}

// Today is 2026-10-28. The mail keeps a checkup that expired less than 32 days ago (31 days ago is
// 2026-09-27) and skips anything older.
func TestReminderMailKeepsRecentlyLapsedCheckupsAndSkipsOlderOnes(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	activeSubmitter(t, s, co, "hrone@example.com")
	for _, w := range []struct{ name, until string }{
		{"Valid Soon", "2026-11-20"}, {"Lapsed 27 Days", "2026-10-01"}, {"Lapsed 31 Days", "2026-09-27"},
		{"Lapsed 32 Days", "2026-09-26"}, {"Lapsed Long Ago", "2026-01-01"},
	} {
		id := seedWorker(t, s, w.name, "1990-01-01", co)
		addRecord(t, s, id, "G-"+w.name, "Fit", w.until, "Open", 1)
	}
	got, err := s.ExpiringForCompany(ctx, co, 60, lapsedGraceDays)
	names := []string{}
	for _, e := range got {
		names = append(names, e.Name)
	}
	want := "Lapsed 31 Days,Lapsed 27 Days,Valid Soon"
	if err != nil || strings.Join(names, ",") != want {
		t.Errorf("mail list = %v, %v; want %s (oldest first, 32 days and more skipped)", names, err, want)
	}
	if got, _ := s.ExpiringForCompany(ctx, co, 60, 0); len(got) != 1 || got[0].Name != "Valid Soon" {
		t.Errorf("without the grace period only the still-valid one is listed, got %+v", got)
	}
}

// The mail has four stages and nothing else: expired (less than 32 days ago), within 2 weeks, within
// 30 days, within 60 days. A worker with more than 60 days left is not in it. Today is 2026-10-28.
func TestReminderStagesAreTwoWeeksThirtyDaysSixtyDaysAndExpired(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{{-31, StageExpired}, {-1, StageExpired}, {0, StageTwoWeeks}, {14, StageTwoWeeks}, {15, StageThirtyDays}, {30, StageThirtyDays}, {31, StageSixtyDays}, {60, StageSixtyDays}, {61, ""}}
	for _, c := range cases {
		if got := reminderStage(c.days); got != c.want {
			t.Errorf("%d days left = %q, want %q", c.days, got, c.want)
		}
	}
	s, _, co := newTestStore(t)
	ctx := context.Background()
	activeSubmitter(t, s, co, "hrone@example.com")
	for _, w := range []struct{ name, until string }{
		{"A Two Weeks", "2026-11-05"}, {"B Thirty", "2026-11-20"}, {"C Sixty", "2026-12-20"}, {"D Far Off", "2026-12-30"}, {"E Lapsed", "2026-10-10"},
	} {
		id := seedWorker(t, s, w.name, "1990-01-01", co)
		addRecord(t, s, id, "T-"+w.name, "Fit", w.until, "Open", 1)
	}
	got, err := s.ExpiringForCompany(ctx, co, reminderWindowDays, lapsedGraceDays)
	stages := map[string]string{}
	for _, e := range got {
		stages[e.Name] = e.Stage
	}
	want := map[string]string{"A Two Weeks": StageTwoWeeks, "B Thirty": StageThirtyDays, "C Sixty": StageSixtyDays, "E Lapsed": StageExpired}
	if err != nil || len(stages) != len(want) {
		t.Fatalf("stages = %v, %v; want %v (Far Off has more than 60 days)", stages, err, want)
	}
	for n, st := range want {
		if stages[n] != st {
			t.Errorf("%s = %q, want %q", n, stages[n], st)
		}
	}
}

func TestRemindersAreOffUntilTheAdminTurnsThemOn(t *testing.T) {
	s, _, _, _ := reminderWorld(t)
	m := &fakeMailer{}
	n, w, err := s.RunReminders(context.Background(), m, "https://portal.example")
	if err != nil || n != 0 || w != 0 || len(m.sent) != 0 {
		t.Fatalf("master switch off: %d submitters, %d workers, %v, %d mails", n, w, err, len(m.sent))
	}
	pn, pw, err := s.ReminderPreview(context.Background())
	if err != nil || pn != 2 || pw != 1 {
		t.Fatalf("preview = %d submitters, %d workers, %v; want both accounts of the company and 1 worker even while off", pn, pw, err)
	}
}

// Both accounts of the company get the mail, and a worker is mailed once per stage: the same stage is
// never repeated, a new stage (30 days, 2 weeks) mails again. Today is 2026-10-28; Soon Worker ends
// 2026-12-01 (34 days: "Within 60 days").
func TestRemindersMailEveryAccountOfTheCompanyOncePerStage(t *testing.T) {
	s, clock, _, _ := reminderWorld(t)
	ctx := context.Background()
	if err := s.SaveReminderSettings(ctx, ReminderConfig{Enabled: true, DaysBefore: 60, RepeatDays: 30}, 0); err != nil {
		t.Fatal(err)
	}
	m := &fakeMailer{}
	n, w, err := s.RunReminders(ctx, m, "https://portal.example")
	if err != nil || n != 2 || w != 1 || len(m.sent) != 2 {
		t.Fatalf("first run: %d accounts, %d workers, %v, %d mails; want both accounts, one worker", n, w, err, len(m.sent))
	}
	body := m.sent[0].Body
	for _, want := range []string{"Soon Worker", "2026-12-01", "Welder", "Within 60 days", "https://portal.example", "/settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("reminder lacks %q\n%s", want, body)
		}
	}
	for _, bad := range []string{"Late Worker", "3201011204900001", "1990-01-01"} {
		if strings.Contains(body, bad) {
			t.Errorf("reminder must not contain %q", bad)
		}
	}
	got := map[string]bool{m.sent[0].To: true, m.sent[1].To: true}
	if !got["hrone@example.com"] || !got["hrtwo@example.com"] {
		t.Errorf("sent to %v, want both accounts of the company", got)
	}

	// Same stage again: nothing, however many days pass inside it.
	clock.advance(2 * 24 * time.Hour) // 32 days left, still the 60 day stage
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 0 {
		t.Fatalf("mailed twice for the same stage")
	}
	clock.advance(3 * 24 * time.Hour) // 2026-11-02: 29 days left, "Within 30 days"
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 2 || !strings.Contains(m.sent[len(m.sent)-1].Body, "Within 30 days") {
		t.Fatalf("the 30 day stage was not mailed")
	}
	clock.advance(16 * 24 * time.Hour) // 2026-11-18: 13 days left, "Within 2 weeks"
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 2 || !strings.Contains(m.sent[len(m.sent)-1].Body, "Within 2 weeks") {
		t.Fatalf("the 2 week stage was not mailed")
	}
	clock.advance(14 * 24 * time.Hour) // 2026-12-02: expired yesterday
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 2 || !strings.Contains(m.sent[len(m.sent)-1].Body, "Expired") {
		t.Fatalf("the expired stage was not mailed")
	}
	clock.advance(24 * time.Hour)
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 0 {
		t.Fatalf("mailed again inside the expired stage")
	}
	if !hasEventAny(t, s, "ReminderSent") {
		t.Error("no ReminderSent event")
	}
}

// A document already waiting for review means the renewal is on its way: no reminder.
func TestRemindersSkipAWorkerWhoseRenewalIsWaitingForReview(t *testing.T) {
	s, _, hr, _ := reminderWorld(t)
	ctx := context.Background()
	_ = s.SaveReminderSettings(ctx, ReminderConfig{Enabled: true, DaysBefore: 60, RepeatDays: 30}, 0)
	var soon int64
	if err := s.DB.QueryRow(`SELECT PatientID FROM Patient WHERE Name = 'Soon Worker'`).Scan(&soon); err != nil {
		t.Fatal(err)
	}
	sentFor(t, s, hr, soon, "Soon Worker") // Pending
	m := &fakeMailer{}
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 0 || len(m.sent) != 0 {
		t.Fatalf("mailed about a worker whose renewal is pending: %d mails", len(m.sent))
	}
}

func hasEventAny(t *testing.T, s *Store, action string) bool {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmissionEvent WHERE Action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestRemindersSkipOptedOutBouncedAndInactiveSubmitters(t *testing.T) {
	for name, stop := range map[string]func(*testing.T, *Store, int64){
		"opted out": func(t *testing.T, s *Store, id int64) {
			if err := s.SetReminders(context.Background(), id, false); err != nil {
				t.Fatal(err)
			}
		},
		"bounced": func(t *testing.T, s *Store, id int64) {
			if _, err := s.MarkEmailBounced(context.Background(), "hrone@example.com", "hard_bounce"); err != nil {
				t.Fatal(err)
			}
		},
		"disabled": func(t *testing.T, s *Store, id int64) {
			if err := s.Disable(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _, hr, _ := reminderWorld(t)
			ctx := context.Background()
			_ = s.SaveReminderSettings(ctx, ReminderConfig{Enabled: true, DaysBefore: 60, RepeatDays: 30}, 0)
			stop(t, s, hr)
			m := &fakeMailer{}
			// The company's other account still gets the mail; only the stopped one must not.
			s.RunReminders(ctx, m, "https://portal.example")
			for _, mm := range m.sent {
				if mm.To == "hrone@example.com" {
					t.Fatalf("a %s submitter was mailed", name)
				}
			}
			if len(m.sent) != 1 {
				t.Fatalf("%s: %d mails, want 1 (the other account)", name, len(m.sent))
			}
		})
	}
}

func TestAFailedReminderMailIsRetriedNextRun(t *testing.T) {
	s, _, _, _ := reminderWorld(t)
	ctx := context.Background()
	_ = s.SaveReminderSettings(ctx, ReminderConfig{Enabled: true, DaysBefore: 60, RepeatDays: 30}, 0)
	m := &fakeMailer{fail: errors.New("smtp down")}
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 0 {
		t.Fatalf("counted a failed mail")
	}
	m.fail = nil
	if n, _, _ := s.RunReminders(ctx, m, "https://portal.example"); n != 2 || len(m.sent) != 2 {
		t.Fatalf("the failed reminder was not retried: %d, %d mails", n, len(m.sent))
	}
}

func TestAdminReminderPageSavesPreviewsAndSendsNow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hr := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, member := e.signIn("hr.budi", "a-long-passphrase-1")
	_, admin := e.signIn("admin", "pelican-orbit-77")

	if rec := e.get("/mcu-submit/admin/reminders", member); rec.Code != http.StatusNotFound {
		t.Fatalf("a submitter on the reminder page = %d, want 404", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/reminders/send", url.Values{}, member); rec.Code != http.StatusNotFound {
		t.Fatalf("a submitter pressing Send now = %d, want 404", rec.Code)
	}

	w := seedWorker(t, e.store, "Soon Worker", "1990-01-01", e.companyID)
	_ = hr
	addRecord(t, e.store, w, "C-1", "Fit", "2026-12-01", "Open", 1)

	if rec := e.post("/mcu-submit/admin/reminders/send", url.Values{}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("Send now while off = %d, want 409", rec.Code)
	}
	page := e.post("/mcu-submit/admin/reminders", url.Values{"enabled": {"on"}}, admin)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "1 accounts and 1 workers would be mailed now") {
		t.Fatalf("save = %d\n%s", page.Code, page.Body.String())
	}
	before := len(e.mail.sent)
	if rec := e.post("/mcu-submit/admin/reminders/send", url.Values{}, admin); rec.Code != http.StatusOK || len(e.mail.sent) != before+1 {
		t.Fatalf("Send now = %d, mails %d -> %d", rec.Code, before, len(e.mail.sent))
	}
	if cfg, _ := e.store.ReminderSettings(ctx); !cfg.Enabled {
		t.Fatal("settings not saved")
	}

	home := e.get("/mcu-submit/home", member)
	if !strings.Contains(home.Body.String(), "Soon Worker") || !strings.Contains(home.Body.String(), "2026-12-01") {
		t.Errorf("Home lacks the Expiring soon list:\n%s", home.Body.String())
	}
}
