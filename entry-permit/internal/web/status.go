package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/jobs"
	"entry-permit/internal/mail"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
)

// JobControl is what the status page needs from the scheduler.
type JobControl interface {
	Runs(ctx context.Context) ([]jobs.RunRecord, error)
	RunNow(ctx context.Context, name string) (jobs.RunResult, error)
}

// jobLabels are the names people see; the key is the job's name in job_run.
var jobLabels = map[string]string{
	"permit_expiry":   "Permit expiry check",
	"departure_check": "Departure and ticket check",
	"stale_reminder":  "Reviewer reminder",
	"admin_digest":    "Admin digest",
}

func jobLabel(name string) string {
	if l, ok := jobLabels[name]; ok {
		return l
	}
	return name
}

// pendingShown caps the "waiting to be sent" list, so a large backlog cannot make the page huge.
const pendingShown = 50

// JobView is one row of the jobs table.
type JobView struct {
	Name, Label, Day, At, Result, Detail string
	Never                                bool
}

// StatusData is what the status page receives.
type StatusData struct {
	Warnings       []string
	Jobs           []JobView
	JobsOff        bool // no scheduler is running in this process
	Mail           mail.Count
	Pending        []mail.PendingRow
	Failed         []mail.FailedRow
	MailConfigured bool
	GateConfigured bool
	Sync           SyncNote
	Flash, Err     string
}

// statusInput is everything the warning rules look at, so they can be tested without a server.
type statusInput struct {
	Now            dates.Moment
	Pol            policy.Policy
	Runs           []jobs.RunRecord
	MailConfigured bool
	Mail           mail.Count
	Mailboxes      map[staff.Role]int // active users with an address, by role
	GateConfigured bool
	Sync           SyncNote
}

// mailRoles are the roles that receive scheduled mail, in the order the warnings list them.
var mailRoles = []staff.Role{staff.Admin, staff.Stage1, staff.Doctor, staff.Final}

// statusWarnings lists what needs an admin's attention, each as a sentence that says what is
// wrong and what to do. A healthy system gives an empty list, so a warning always means something.
func statusWarnings(in statusInput) []string {
	var w []string
	switch {
	case !in.MailConfigured && in.Mail.Pending > 0:
		w = append(w, fmt.Sprintf("%s waiting, but no mail server is set (SMTP_HOST and MAIL_FROM in .env). They are sent once it is.", countOf(in.Mail.Pending, "e-mail is", "e-mails are")))
	case !in.MailConfigured:
		w = append(w, "No mail server is set (SMTP_HOST and MAIL_FROM in .env): no e-mail can be sent.")
	}
	if in.Mail.Failed > 0 {
		w = append(w, fmt.Sprintf("%s after the maximum number of attempts. Retry them below once the cause is fixed.", countOf(in.Mail.Failed, "e-mail gave up", "e-mails gave up")))
	}
	for _, r := range mailRoles {
		if in.Mailboxes[r] > 0 {
			continue
		}
		if r == staff.Admin {
			w = append(w, "No active admin has an e-mail address, so the admin digest is not sent. Set one on the Users page.")
		} else {
			w = append(w, fmt.Sprintf("No active user with the %s role has an e-mail address, so its reviewer reminders are not sent. Set one on the Users page.", r.Label()))
		}
	}
	today, hour := in.Now.Day(), hourOfMoment(in.Now)
	for _, run := range in.Runs {
		label := jobLabel(run.Name)
		switch {
		case run.Day == string(today) && run.Result == "failed":
			w = append(w, jobMessage(label, run.Detail, " failed."))
		case run.Day == string(today) && run.Result == "partial":
			w = append(w, fmt.Sprintf("%s skipped some records (%s). The server log says which.", label, run.Detail))
		case run.Day != string(today) && hour > run.Hour && run.Result == "":
			w = append(w, label+" has never run.")
		case run.Day != string(today) && hour > run.Hour:
			w = append(w, fmt.Sprintf("%s has not run today (it is due at %02d:00).", label, run.Hour))
		}
	}
	switch {
	case !in.GateConfigured:
		w = append(w, "Gate sync is off (GATE_WORKER_URL and GATE_SHARED_SECRET are not set): the gate receives no updates.")
	case in.Sync.Never:
		w = append(w, "The gate has never synced.")
	case in.Sync.Stale:
		w = append(w, fmt.Sprintf("The gate has not synced since %s.", in.Sync.At))
	}
	return w
}

// jobMessage joins a job's label and its detail line ("failed: database is locked").
func jobMessage(label, detail, fallback string) string {
	if detail == "" {
		return label + fallback
	}
	return label + " " + detail
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func hourOfMoment(m dates.Moment) int {
	if len(m) < 13 {
		return 0
	}
	h, err := strconv.Atoi(string(m[11:13]))
	if err != nil {
		return 0
	}
	return h
}

// statusData reads everything the page shows. An error reading the database fails the page:
// a status page that shows a made-up "all well" is worse than none.
func (a *App) statusData(ctx context.Context) (StatusData, error) {
	d := StatusData{MailConfigured: a.Mailer != nil, GateConfigured: a.GateKick != nil}
	var runs []jobs.RunRecord
	if a.Jobs == nil {
		d.JobsOff = true
	} else {
		var err error
		if runs, err = a.Jobs.Runs(ctx); err != nil {
			return d, err
		}
	}
	for _, r := range runs {
		d.Jobs = append(d.Jobs, JobView{Name: r.Name, Label: jobLabel(r.Name), Day: r.Day, At: r.At, Result: r.Result, Detail: r.Detail, Never: r.Result == ""})
	}
	if a.Outbox != nil {
		var err error
		if d.Mail, err = a.Outbox.Counts(ctx); err != nil {
			return d, err
		}
		if d.Pending, err = a.Outbox.Pending(ctx, pendingShown); err != nil {
			return d, err
		}
		if d.Failed, err = a.Outbox.Failed(ctx); err != nil {
			return d, err
		}
	}
	d.Sync = a.syncNote(ctx)
	boxes := map[staff.Role]int{}
	for _, r := range mailRoles {
		list, err := a.Store.EmailsForRole(ctx, r)
		if err != nil {
			return d, err
		}
		boxes[r] = len(list)
	}
	d.Warnings = statusWarnings(statusInput{
		Now: a.Clock.Now(), Pol: a.policy(), Runs: runs, MailConfigured: d.MailConfigured, Mail: d.Mail,
		Mailboxes: boxes, GateConfigured: d.GateConfigured, Sync: d.Sync,
	})
	return d, nil
}

// adminStatus is /staff/admin/status: what the scheduled work is doing, the mail backlog,
// and anything that needs attention, with Run now and Retry buttons.
func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	d, err := a.statusData(r.Context())
	if err != nil {
		log.Printf("admin status: %v", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	d.Flash, d.Err = flash(r, "ok"), flash(r, "err")
	v := a.adminView(r, u, "System status", AdminData{})
	v.Status = d
	a.render(w, "admin_status", http.StatusOK, v)
}

func statusRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	http.Redirect(w, r, "/staff/admin/status?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

// adminStatusRun runs one job now. The audit row is written first and a run that cannot be
// recorded does not start, the same rule as the other admin actions.
func (a *App) adminStatusRun(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	name := r.PathValue("job")
	if a.Jobs == nil {
		statusRedirect(w, r, "err", "The job scheduler is not running.")
		return
	}
	if err := audit.Write(r.Context(), a.SQL, a.Clock.Now(), u.Username, "job.run_now", "job:"+name, "", "started"); err != nil {
		log.Printf("admin status: audit run now: %v", err)
		statusRedirect(w, r, "err", "The action could not be recorded, so the job was not started.")
		return
	}
	res, err := a.Jobs.RunNow(r.Context(), name)
	switch {
	case errors.Is(err, jobs.ErrBusy):
		statusRedirect(w, r, "err", "That job is running right now. Try again in a few minutes.")
	case errors.Is(err, jobs.ErrUnknownJob):
		statusRedirect(w, r, "err", "There is no job with that name.")
	case err != nil:
		log.Printf("admin status: run %s: %v", name, err)
		statusRedirect(w, r, "err", "The job could not be started. The server log has the reason.")
	case res.Result == "failed":
		statusRedirect(w, r, "err", jobLabel(name)+": "+res.Detail)
	default:
		msg := jobLabel(name) + " finished: " + res.Detail + "."
		if name == "admin_digest" && res.Outcome.Changed == 0 && res.Outcome.Errors == 0 {
			msg += " (The digest is only sent in its scheduled week.)"
		}
		statusRedirect(w, r, "ok", msg)
	}
}

// adminStatusRetry gives a Failed mail a fresh start and tries to send it at once.
func (a *App) adminStatusRetry(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 || a.Outbox == nil {
		http.NotFound(w, r)
		return
	}
	now := a.Clock.Now()
	if err := a.Outbox.Requeue(r.Context(), id, now); err != nil {
		statusRedirect(w, r, "err", "That e-mail is not in the failed list any more.")
		return
	}
	if err := audit.Write(r.Context(), a.SQL, now, u.Username, "mail.retry", "mail:"+itoa64(id), "Failed", "Pending"); err != nil {
		log.Printf("admin status: audit retry: %v", err) // the retry already happened; say so in the log
	}
	if a.Mailer == nil {
		statusRedirect(w, r, "ok", fmt.Sprintf("E-mail %d is back in the queue. It is waiting because no mail server is set.", id))
		return
	}
	pol := a.policy()
	if err := a.Outbox.Deliver(r.Context(), a.Mailer, id, now, mail.Retry{MaxAttempts: pol.OutboxMaxAttempts, BaseMinutes: pol.OutboxRetryMinutes}); err != nil {
		log.Printf("admin status: retry mail %d: %v", id, err)
		statusRedirect(w, r, "err", fmt.Sprintf("E-mail %d was queued again, but sending failed. It will be tried again later.", id))
		return
	}
	statusRedirect(w, r, "ok", fmt.Sprintf("E-mail %d was sent.", id))
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
