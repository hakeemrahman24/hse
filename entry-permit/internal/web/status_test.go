package web

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/jobs"
	"entry-permit/internal/mail"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

// fakeJobs stands in for the scheduler.
type fakeJobs struct {
	runs []jobs.RunRecord
	ran  []string
	res  jobs.RunResult
	err  error
}

func (f *fakeJobs) Runs(context.Context) ([]jobs.RunRecord, error) { return f.runs, nil }
func (f *fakeJobs) RunNow(_ context.Context, name string) (jobs.RunResult, error) {
	if f.err != nil {
		return jobs.RunResult{}, f.err
	}
	f.ran = append(f.ran, name)
	return f.res, nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func healthyInput() statusInput {
	every := map[staff.Role]int{staff.Admin: 1, staff.Stage1: 2, staff.Doctor: 1, staff.Final: 1}
	return statusInput{
		Now: "2026-11-10 09:00:00", Pol: policy.Default(), MailConfigured: true, Mailboxes: every, GateConfigured: true,
		Sync: SyncNote{At: "2026-11-10 08:58:00"},
		Runs: []jobs.RunRecord{
			{Name: "permit_expiry", Day: "2026-11-10", Result: "ok", Hour: 6},
			{Name: "stale_reminder", Day: "2026-11-10", Result: "ok", Hour: 7},
		},
	}
}

func TestStatusWarningsStaySilentWhenAllIsWell(t *testing.T) {
	if w := statusWarnings(healthyInput()); len(w) != 0 {
		t.Fatalf("warnings on a healthy system: %v", w)
	}
}

func TestStatusWarningsNameTheProblemAndWhatToDoAboutIt(t *testing.T) {
	cases := []struct {
		name   string
		change func(*statusInput)
		want   string
	}{
		{"no mail server", func(i *statusInput) { i.MailConfigured = false; i.Mail.Pending = 3 }, "3 e-mails are waiting"},
		{"no mail server and nothing waiting", func(i *statusInput) { i.MailConfigured = false }, "No mail server is set"},
		{"failed mail", func(i *statusInput) { i.Mail.Failed = 2 }, "2 e-mails gave up"},
		{"no admin mailbox", func(i *statusInput) { i.Mailboxes[staff.Admin] = 0 }, "admin digest"},
		{"no doctor mailbox", func(i *statusInput) { i.Mailboxes[staff.Doctor] = 0 }, "Doctor"},
		{"job failed today", func(i *statusInput) { i.Runs[0].Result, i.Runs[0].Detail = "failed", "failed: database is locked" }, "Permit expiry check failed: database is locked"},
		{"job skipped records today", func(i *statusInput) { i.Runs[0].Result, i.Runs[0].Detail = "partial", "3 changed, 2 skipped" }, "2 skipped"},
		{"job late", func(i *statusInput) { i.Runs[1].Day = "2026-11-09" }, "Reviewer reminder has not run today"},
		{"job never ran", func(i *statusInput) { i.Runs[1].Day, i.Runs[1].Result = "", "" }, "Reviewer reminder has never run"},
		{"gate sync off", func(i *statusInput) { i.GateConfigured = false }, "Gate sync is off"},
		{"gate never synced", func(i *statusInput) { i.Sync = SyncNote{Never: true} }, "never synced"},
		{"gate stale", func(i *statusInput) { i.Sync = SyncNote{At: "2026-11-10 07:00:00", Stale: true} }, "2026-11-10 07:00:00"},
	}
	for _, c := range cases {
		in := healthyInput()
		c.change(&in)
		got := strings.Join(statusWarnings(in), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: warnings %q lack %q", c.name, got, c.want)
		}
	}
}

func TestAJobNotYetDueIsNotLate(t *testing.T) {
	in := healthyInput()
	in.Now = "2026-11-10 06:30:00" // 06:30: the 07:00 job has not been due yet; the 06:00 one is within its first hour
	in.Runs = []jobs.RunRecord{{Name: "stale_reminder", Day: "2026-11-09", Result: "ok", Hour: 7}, {Name: "permit_expiry", Day: "2026-11-09", Result: "ok", Hour: 6}}
	if w := statusWarnings(in); len(w) != 0 {
		t.Fatalf("early in the morning: %v", w)
	}
}

func (e *testEnv) withJobs(f *fakeJobs) { e.app.Jobs = f }

func TestStatusPageIsAdminOnlyAndShowsJobsMailAndWarnings(t *testing.T) {
	e := newEnv(t)
	e.withJobs(&fakeJobs{runs: []jobs.RunRecord{
		{Name: "permit_expiry", Day: "2026-11-06", Result: "ok", At: "2026-11-06 06:00:01", Detail: "2 changed", Hour: 6},
		{Name: "admin_digest", Hour: 8},
	}})
	if r, _ := e.get(t, "/staff/admin/status", nil); r.StatusCode != 303 {
		t.Fatalf("anonymous got %d", r.StatusCode)
	}
	gate, _ := e.gateAdminSession(t)
	if r, _ := e.get(t, "/staff/admin/status", gate); r.StatusCode != 403 {
		t.Fatalf("gate admin got %d", r.StatusCode)
	}
	admin, csrf := e.adminSession(t)
	ctx := context.Background()
	m := mail.Message{Kind: "wep-approved", SourceKey: "WEP-ONE01", To: []string{"rina@example.com"}, Subject: "Approved Budi", Text: "t", HTML: "<p>h</p>"}
	failID, _ := e.app.Outbox.Enqueue(ctx, m, "2026-11-06 07:00:00")
	_ = e.app.Outbox.Deliver(ctx, &fakeMail{err: errors.New("550 mailbox full")}, failID, "2026-11-06 07:00:00", mail.Retry{MaxAttempts: 1, BaseMinutes: 1})
	_, _ = e.app.Outbox.Enqueue(ctx, m, "2026-11-06 07:30:00") // still waiting

	r, body := e.get(t, "/staff/admin/status", admin)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{
		"System status", "Permit expiry check", "2 changed", "Never run", "Admin digest", "Run now",
		"550 mailbox full", "Retry", "Approved Budi", "Waiting to be sent",
		"Needs attention", "Stage 1", // the test environment has no Stage 1 mailbox
		csrf,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestRunNowChecksTheTokenAuditsFirstAndReportsTheResult(t *testing.T) {
	e := newEnv(t)
	f := &fakeJobs{res: jobs.RunResult{Result: "ok", Detail: "4 changed"}}
	e.withJobs(f)
	admin, csrf := e.adminSession(t)

	if r := e.post(t, "/staff/admin/status/run/permit_expiry", admin, url.Values{}); r.StatusCode != 403 || len(f.ran) != 0 {
		t.Fatalf("without the token: %d, ran %v", r.StatusCode, f.ran)
	}
	r := e.post(t, "/staff/admin/status/run/permit_expiry", admin, url.Values{"_csrf": {csrf}})
	loc := r.Header.Get("Location")
	if r.StatusCode != 303 || len(f.ran) != 1 || f.ran[0] != "permit_expiry" || !strings.Contains(loc, "4+changed") || !strings.HasPrefix(loc, "/staff/admin/status") {
		t.Fatalf("run now -> %d %s ran %v", r.StatusCode, loc, f.ran)
	}
	entries, err := audit.Recent(context.Background(), e.db, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, en := range entries {
		if en.Action == "job.run_now" && en.Target == "job:permit_expiry" && en.Actor == "arif" {
			found = true
		}
	}
	if !found {
		t.Errorf("no audit row for the run: %+v", entries)
	}
	// a failed job is reported as an error, not as success
	f.res = jobs.RunResult{Result: "failed", Detail: "failed: down"}
	r = e.post(t, "/staff/admin/status/run/permit_expiry", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "err=") || !strings.Contains(loc, "failed%3A+down") {
		t.Errorf("failed run -> %s", loc)
	}
}

func TestRunNowSaysSoWhenTheJobIsBusyOrUnknown(t *testing.T) {
	e := newEnv(t)
	f := &fakeJobs{}
	e.withJobs(f)
	admin, csrf := e.adminSession(t)
	f.err = jobs.ErrBusy
	r := e.post(t, "/staff/admin/status/run/admin_digest", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "running+right+now") {
		t.Errorf("busy -> %s", loc)
	}
	f.err = jobs.ErrUnknownJob
	r = e.post(t, "/staff/admin/status/run/nope", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "no+job") {
		t.Errorf("unknown -> %s", loc)
	}
	// an internal error must not leak into the page
	f.err = errors.New("dial tcp 10.0.0.5:3306: refused")
	r = e.post(t, "/staff/admin/status/run/admin_digest", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); strings.Contains(loc, "10.0.0.5") || !strings.Contains(loc, "err=") {
		t.Errorf("internal error -> %s", loc)
	}
	e.app.Jobs = nil
	r = e.post(t, "/staff/admin/status/run/admin_digest", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "err=") {
		t.Errorf("no scheduler -> %s", loc)
	}
}

func TestRetryResendsAFailedMailAndAuditsIt(t *testing.T) {
	e := newEnv(t)
	e.withJobs(&fakeJobs{})
	admin, csrf := e.adminSession(t)
	ctx := context.Background()
	m := mail.Message{Kind: "wep-approved", SourceKey: "WEP-ONE01", To: []string{"rina@example.com"}, Subject: "Approved Budi", Text: "t", HTML: "<p>h</p>"}
	id, _ := e.app.Outbox.Enqueue(ctx, m, "2026-11-06 07:00:00")
	_ = e.app.Outbox.Deliver(ctx, &fakeMail{err: errors.New("550 mailbox full")}, id, "2026-11-06 07:00:00", mail.Retry{MaxAttempts: 1, BaseMinutes: 1})
	path := "/staff/admin/status/mail/" + itoa(id) + "/retry"

	if r := e.post(t, path, admin, url.Values{}); r.StatusCode != 403 || len(e.mailer.sent) != 0 {
		t.Fatalf("without the token: %d", r.StatusCode)
	}
	r := e.post(t, path, admin, url.Values{"_csrf": {csrf}})
	if r.StatusCode != 303 || len(e.mailer.sent) != 1 || e.mailer.sent[0].Subject != "Approved Budi" {
		t.Fatalf("retry -> %d, sent %d", r.StatusCode, len(e.mailer.sent))
	}
	var status string
	if err := e.db.QueryRow(`SELECT status FROM email_outbox WHERE id = ?`, id).Scan(&status); err != nil || status != "Sent" {
		t.Errorf("status %q err %v", status, err)
	}
	entries, _ := audit.Recent(ctx, e.db, 10)
	found := false
	for _, en := range entries {
		found = found || (en.Action == "mail.retry" && en.Target == "mail:"+itoa(id))
	}
	if !found {
		t.Error("no audit row for the retry")
	}
	// the same mail is not Failed any more: a second press is an error, and nothing is sent twice
	r = e.post(t, path, admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "err=") || len(e.mailer.sent) != 1 {
		t.Errorf("second retry -> %s, sent %d", loc, len(e.mailer.sent))
	}
	if r := e.post(t, "/staff/admin/status/mail/abc/retry", admin, url.Values{"_csrf": {csrf}}); r.StatusCode != 404 {
		t.Errorf("a non-numeric id gave %d", r.StatusCode)
	}
}

func TestRetryWithNoMailServerRequeuesAndSaysItIsWaiting(t *testing.T) {
	e := newEnv(t)
	e.withJobs(&fakeJobs{})
	admin, csrf := e.adminSession(t)
	ctx := context.Background()
	m := mail.Message{Kind: "wep-approved", SourceKey: "WEP-ONE01", To: []string{"rina@example.com"}, Subject: "Approved Budi", Text: "t", HTML: "<p>h</p>"}
	id, _ := e.app.Outbox.Enqueue(ctx, m, "2026-11-06 07:00:00")
	_ = e.app.Outbox.Deliver(ctx, &fakeMail{err: errors.New("boom")}, id, "2026-11-06 07:00:00", mail.Retry{MaxAttempts: 1, BaseMinutes: 1})
	e.app.Mailer = nil
	r := e.post(t, "/staff/admin/status/mail/"+itoa(id)+"/retry", admin, url.Values{"_csrf": {csrf}})
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "ok=") || !strings.Contains(loc, "waiting") {
		t.Errorf("retry without a mail server -> %s", loc)
	}
	var status string
	_ = e.db.QueryRow(`SELECT status FROM email_outbox WHERE id = ?`, id).Scan(&status)
	if status != "Pending" {
		t.Errorf("status %q, want Pending", status)
	}
}

func TestAdminOverviewLinksToStatusAndCountsWarnings(t *testing.T) {
	e := newEnv(t)
	e.withJobs(&fakeJobs{})
	admin, _ := e.adminSession(t)
	_, body := e.get(t, "/staff/admin", admin)
	if !strings.Contains(body, `href="/staff/admin/status"`) || !strings.Contains(body, "System status") {
		t.Error("the overview has no status tile")
	}
}
