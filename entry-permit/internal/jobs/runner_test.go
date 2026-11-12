package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"entry-permit/internal/dashboard"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/mail"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiverlog"
	"entry-permit/migrations"
)

const today = "2026-11-10"

type tclock struct{ now dates.Moment }

func (c *tclock) Now() dates.Moment { return c.now }
func (c *tclock) Today() dates.Day  { return c.now.Day() }

type fakeMailer struct {
	mu   sync.Mutex // the drain loop sends from its own goroutine
	sent []mail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

// fakeStaff is the mailbox list per role.
type fakeStaff map[staff.Role][]string

func (f fakeStaff) EmailsForRole(_ context.Context, r staff.Role) ([]string, error) { return f[r], nil }

type env struct {
	staff  fakeStaff
	t      *testing.T
	db     *sql.DB
	clock  *tclock
	pol    policy.Policy
	mailer *fakeMailer
	kicks  int
	r      *Runner
}

func newEnv(t *testing.T) *env {
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
	e := &env{t: t, db: d, clock: &tclock{now: today + " 06:00:00"}, pol: policy.Default(), mailer: &fakeMailer{}, staff: fakeStaff{}}
	e.r = &Runner{
		Tickets: ticketlog.Store{DB: d}, Permits: permitlog.Store{DB: d}, Waivers: waiverlog.Store{DB: d},
		Outbox: mail.Outbox{DB: d}, Mailer: e.mailer, Clock: e.clock,
		Policy:  func() policy.Policy { return e.pol },
		Reviews: reviewlog.Store{DB: d}, Dash: dashboard.Store{DB: d}, Staff: e.staff,
		BaseURL: "https://permit.example", GateKick: func() { e.kicks++ },
		Logf: func(string, ...any) {},
	}
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
}

func (e *env) permit(lep, status, validUntil string) {
	e.t.Helper()
	e.exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, nationality, requester_name, requester_email,
		work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES ('WEP-' || ?, ?, ?, 1, 'Budi Santoso', 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore', '2027-03-31', ?, '2026-06-01 08:00:00')`,
		lep[4:], lep, status, validUntil)
}

// ticket stores an Approved work ticket; recordStart is the first check-in ("" for none).
func (e *env) ticket(id, lep, arrival, departure, recordStart string) {
	e.t.Helper()
	n := ticketlog.NewTicket{ID: id, LEP: lep, Work: true, Status: "Approved", Arrival: arrival, Departure: departure, Location: "Onshore",
		Pattern: "Normal", RotationNo: 1, DoctorReview: "Approved", DoctorNote: "_", McuResult: "Fit",
		RequesterEmail: "rina@example.com", SubmittedAt: "2026-09-01 08:00:00"}
	if err := e.r.Tickets.Create(context.Background(), n); err != nil {
		e.t.Fatal(err)
	}
	if recordStart != "" {
		e.exec(`UPDATE ticket SET record_start = ? WHERE ticket_id = ?`, recordStart, id)
	}
}

func (e *env) waiver(id, status, to, ticketStatus string) {
	e.t.Helper()
	e.exec(`INSERT INTO waiver (request_id, status, applicant_type, company_id, company, visitor_name, gender, dob, nationality, nik, position,
		department, visit_location, purpose, visit_from, visit_to, days, requester_name, requester_email, phone, risk, precaution, prevent, declared_at, created_at)
		VALUES (?, ?, 'VisitorCompany', 1, 'Visit Co', 'Visitor Vic', 'M', '1980-01-01', 'Indonesia', '123', 'Auditor', 'HSE', 'Onshore', 'Audit',
		'2026-11-05', ?, 3, 'Req', 'r@x.id', '0812', 'r', 'p', 'v', '2026-06-01 08:00:00', '2026-06-01 08:00:00')`, id, status, to)
	n := ticketlog.NewTicket{ID: "TKT-" + id[4:], LEP: id, Work: false, Status: ticketStatus, Arrival: "2026-11-05", Departure: to,
		Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-01 08:00:00"}
	if err := e.r.Tickets.Create(context.Background(), n); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var v string
	if err := e.db.QueryRow(q, args...).Scan(&v); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
	return v
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
	return n
}

func (e *env) mailKinds() []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT kind FROM email_outbox ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func (e *env) locked(ticket string) bool {
	return e.count(`SELECT lep_locked FROM ticket WHERE ticket_id = ?`, ticket) == 1
}

// ── permit expiry ────────────────────────────────────────────────────────────

func TestExpiryCheckMovesPermitsThroughGraceToExpiredAndLocksTheirTickets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.permit("LEP-AAAA1", "Approved", "2026-11-09") // one day past: grace
	e.permit("LEP-AAAA2", "Approved", "2026-10-01") // 40 days past: expired
	e.permit("LEP-AAAA3", "Approved", today)        // valid through today
	e.permit("LEP-AAAA4", "Expired in grace period", "2026-01-01")
	e.permit("LEP-AAAA5", "Resigned", "2026-01-01") // never touched
	e.ticket("TKT-00001", "LEP-AAAA1", "2026-10-20", "2026-12-20", "")
	e.ticket("TKT-00003", "LEP-AAAA3", "2026-10-20", "2026-12-20", "")

	out, err := e.r.ExpiryCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed != 3 || out.Errors != 0 {
		t.Errorf("outcome = %+v", out)
	}
	for lep, want := range map[string]string{"LEP-AAAA1": "Expired in grace period", "LEP-AAAA2": "Expired", "LEP-AAAA3": "Approved", "LEP-AAAA4": "Expired", "LEP-AAAA5": "Resigned"} {
		if got := e.str(`SELECT status FROM permit WHERE lep_no = ?`, lep); got != want {
			t.Errorf("%s is %q, want %q", lep, got, want)
		}
	}
	if !e.locked("TKT-00001") || e.locked("TKT-00003") {
		t.Error("only the expired permit's ticket should be locked")
	}
	if e.kicks != 1 {
		t.Errorf("the gate was asked to sync %d times, want once for the whole run", e.kicks)
	}
}

func TestExpiryCheckRunAgainChangesNothingAndDoesNotKickTheGate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.permit("LEP-AAAA1", "Approved", "2026-11-09")
	if _, err := e.r.ExpiryCheck(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := e.r.ExpiryCheck(ctx)
	if err != nil || out.Changed != 0 || e.kicks != 1 {
		t.Errorf("second run: %+v err=%v kicks=%d", out, err, e.kicks)
	}
}

// ── departure check: work tickets ────────────────────────────────────────────

func TestDepartureCheckClosesATicketPastItsDeparture(t *testing.T) {
	e := newEnv(t)
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-10-01", "2026-11-09", "")
	out, err := e.r.DepartureCheck(context.Background())
	if err != nil || out.Changed != 1 {
		t.Fatalf("outcome %+v err=%v", out, err)
	}
	if e.str(`SELECT status FROM ticket WHERE ticket_id = 'TKT-00001'`) != "Closed" ||
		e.str(`SELECT closed_reason FROM ticket WHERE ticket_id = 'TKT-00001'`) != "Planned departure passed" ||
		e.str(`SELECT closed_at FROM ticket WHERE ticket_id = 'TKT-00001'`) != today+" 06:00:00" {
		t.Error("the ticket was not closed with the planned-departure reason and the job's time")
	}
	if len(e.mailKinds()) != 0 {
		t.Error("a closed ticket must not send a reminder")
	}
}

func TestDepartureCheckQueuesTheNextRotationReminderOnceWithALinkToTheTicketPage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-10-20", "2026-11-15", "") // 5 days left, the lead is 7

	if out, err := e.r.DepartureCheck(ctx); err != nil || out.Changed != 1 {
		t.Fatalf("outcome %+v err=%v", out, err)
	}
	if kinds := e.mailKinds(); len(kinds) != 1 || kinds[0] != mail.KindTicketReminder {
		t.Fatalf("mails = %v", kinds)
	}
	subject := e.str(`SELECT subject FROM email_outbox`)
	body := e.str(`SELECT text_body FROM email_outbox`)
	to := e.str(`SELECT to_addrs FROM email_outbox`)
	if to != "rina@example.com" || !strings.Contains(subject, "5 days") || !strings.Contains(body, "https://permit.example/ticket?lep=LEP-AAAAA") ||
		!strings.Contains(body, "15 Nov 2026") || !strings.Contains(body, "Budi Santoso") {
		t.Errorf("mail to %q, subject %q, body:\n%s", to, subject, body)
	}
	if e.count(`SELECT reminder_notified FROM ticket`) != 1 {
		t.Error("the reminder flag was not set")
	}
	// the same day, again: no second mail
	if out, _ := e.r.DepartureCheck(ctx); out.Changed != 0 || len(e.mailKinds()) != 1 {
		t.Errorf("second run: %+v, %d mails", out, len(e.mailKinds()))
	}
}

func TestDepartureCheckSendsBothWeekMailsAtTheCapAndLocksNothingByDefault(t *testing.T) {
	e := newEnv(t)
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-09-10", "2026-12-20", "2026-09-15 08:00:00") // 8 weeks on site, onshore cap is 8

	if out, err := e.r.DepartureCheck(context.Background()); err != nil || out.Changed != 1 {
		t.Fatalf("outcome %+v err=%v", out, err)
	}
	kinds := e.mailKinds()
	if len(kinds) != 2 || kinds[0] != mail.KindTicketWeekN1 || kinds[1] != mail.KindTicketWeekN0 {
		t.Fatalf("mails = %v", kinds)
	}
	if e.count(`SELECT weeks_on_site FROM ticket`) != 8 || e.count(`SELECT fatigue_flag FROM ticket`) != 1 {
		t.Error("weeks on site or the fatigue flag was not stored")
	}
	if e.locked("TKT-00001") || e.kicks != 0 {
		t.Error("reaching the cap locked the ticket although the block-at-cap switch is off")
	}
	if !strings.Contains(e.str(`SELECT subject FROM email_outbox WHERE kind = 'ticket-week-n0'`), "ROTATION CAP") {
		t.Error("the cap mail has the wrong subject")
	}
}

func TestDepartureCheckLocksAtTheCapAndKicksTheGateWhenTheOwnerSwitchesThatOn(t *testing.T) {
	e := newEnv(t)
	e.pol.BlockGateAtCap = true
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-09-10", "2026-12-20", "2026-09-15 08:00:00")
	if _, err := e.r.DepartureCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.locked("TKT-00001") || e.kicks != 1 {
		t.Errorf("locked=%v kicks=%d", e.locked("TKT-00001"), e.kicks)
	}
}

func TestDepartureCheckLocksTheTicketOfAPermitThatExpiredBeforeTheExpiryJobRan(t *testing.T) {
	e := newEnv(t)
	e.permit("LEP-AAAAA", "Approved", "2026-11-01")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-10-20", "2026-12-20", "")
	if _, err := e.r.DepartureCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.locked("TKT-00001") || e.kicks != 1 {
		t.Errorf("locked=%v kicks=%d", e.locked("TKT-00001"), e.kicks)
	}
	if len(e.mailKinds()) != 0 {
		t.Error("a locked ticket gets no reminder")
	}
}

func TestOneTicketThatCannotBeHandledDoesNotStopTheOthers(t *testing.T) {
	e := newEnv(t)
	e.permit("LEP-AAAA1", "Approved", "2027-03-17")
	e.permit("LEP-AAAA2", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAA1", "2026-10-20", "2026-11-15", "")
	e.ticket("TKT-00002", "LEP-AAAA2", "2026-10-20", "2026-11-15", "")
	e.exec(`UPDATE ticket SET requester_email = 'not an address' WHERE ticket_id = 'TKT-00001'`)

	out, err := e.r.DepartureCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed != 1 || out.Errors != 1 {
		t.Errorf("outcome %+v", out)
	}
	if e.count(`SELECT reminder_notified FROM ticket WHERE ticket_id = 'TKT-00001'`) != 0 {
		t.Error("the flag stayed set although its mail could not be queued, so the reminder would never be retried")
	}
	if e.count(`SELECT reminder_notified FROM ticket WHERE ticket_id = 'TKT-00002'`) != 1 || len(e.mailKinds()) != 1 {
		t.Error("the good ticket was not handled")
	}
}

// ── departure check: visits that are over ────────────────────────────────────

func TestDepartureCheckEndsVisitsThatAreOver(t *testing.T) {
	e := newEnv(t)
	e.waiver("WVR-00001", "Approved", "2026-11-09", "Approved")
	e.waiver("WVR-00002", "PendingDoctor", "2026-11-09", "PendingDoctor")
	e.waiver("WVR-00003", "Approved", today, "Approved") // the visit ends today: not over yet

	out, err := e.r.DepartureCheck(context.Background())
	if err != nil || out.Changed != 2 {
		t.Fatalf("outcome %+v err=%v", out, err)
	}
	for id, want := range map[string]string{"WVR-00001": "Completed", "WVR-00002": "Cancelled", "WVR-00003": "Approved"} {
		if got := e.str(`SELECT status FROM waiver WHERE request_id = ?`, id); got != want {
			t.Errorf("%s is %q, want %q", id, got, want)
		}
	}
	if e.str(`SELECT closed_reason FROM ticket WHERE ticket_id = 'TKT-00001'`) != "Planned departure passed" ||
		e.str(`SELECT status FROM ticket WHERE ticket_id = 'TKT-00002'`) != "Cancelled" ||
		e.str(`SELECT closed_reason FROM ticket WHERE ticket_id = 'TKT-00002'`) != "Visit window ended before approval" {
		t.Error("the visit tickets were not ended with the right reasons")
	}
	if e.kicks != 1 {
		t.Errorf("gate kicks = %d: completed waivers must be deactivated at the gate", e.kicks)
	}
}

// ── mail leaves right after the job ──────────────────────────────────────────

func TestMailQueuedByTheJobIsSentAtOnceWhenAMailerIsConfigured(t *testing.T) {
	e := newEnv(t)
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-10-20", "2026-11-15", "")
	if _, err := e.r.DepartureCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.mailer.sent) != 1 || e.str(`SELECT status FROM email_outbox`) != "Sent" {
		t.Errorf("sent %d, status %q", len(e.mailer.sent), e.str(`SELECT status FROM email_outbox`))
	}
}

func TestMailStaysInTheOutboxWhenNoMailerIsConfiguredOrSendingFails(t *testing.T) {
	e := newEnv(t)
	e.r.Mailer = nil
	e.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e.ticket("TKT-00001", "LEP-AAAAA", "2026-10-20", "2026-11-15", "")
	if _, err := e.r.DepartureCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.str(`SELECT status FROM email_outbox`) != "Pending" {
		t.Error("with no mailer the mail must wait as Pending")
	}

	e2 := newEnv(t)
	e2.mailer.err = errors.New("smtp down")
	e2.permit("LEP-AAAAA", "Approved", "2027-03-17")
	e2.ticket("TKT-00001", "LEP-AAAAA", "2026-10-20", "2026-11-15", "")
	if out, err := e2.r.DepartureCheck(context.Background()); err != nil || out.Changed != 1 {
		t.Fatalf("a mail server outage must not fail the job: %+v %v", out, err)
	}
	if e2.str(`SELECT status FROM email_outbox`) != "Pending" || e2.count(`SELECT reminder_notified FROM ticket`) != 1 {
		t.Error("a failed send keeps the mail Pending for the retry and keeps the reminder flag")
	}
}

func TestJobsReportADatabaseErrorInsteadOfHidingIt(t *testing.T) {
	e := newEnv(t)
	e.db.Close()
	if _, err := e.r.ExpiryCheck(context.Background()); err == nil {
		t.Error("ExpiryCheck hid a database error")
	}
	if _, err := e.r.DepartureCheck(context.Background()); err == nil {
		t.Error("DepartureCheck hid a database error")
	}
}
