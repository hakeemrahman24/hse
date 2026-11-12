package gatesync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/policy"
	"entry-permit/internal/reflist"
	"entry-permit/internal/waiverlog"
	"entry-permit/migrations"
)

var ctx = context.Background()

type tclock struct{ now dates.Moment }

func (c *tclock) Now() dates.Moment { return c.now }
func (c *tclock) Today() dates.Day  { return c.now.Day() }

type env struct {
	t     *testing.T
	d     *sql.DB
	w     *fakeWorker
	s     *Syncer
	clock *tclock
	pol   policy.Policy
	lists *reflist.Store
}

// newEnv builds a Pi database, a fake Worker and a Syncer wired to both.
// withRefs fills the officer, gate and reason lists the gate needs.
func newEnv(t *testing.T, withRefs bool) *env {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	clock := &tclock{now: "2026-11-10 10:00:00"}
	e := &env{t: t, d: d, w: newFakeWorker(t), clock: clock, pol: policy.Default(), lists: reflist.NewStore(d, clock)}
	officers := gateofficer.New(d, clock).WithIterations(10)
	e.s = &Syncer{
		DB: d, Clock: clock, Officers: officers, Lists: e.lists,
		Client: &Client{BaseURL: e.w.srv.URL, Secret: testSecret, HTTP: e.w.srv.Client()},
		Policy: func() policy.Policy { return e.pol },
	}
	if withRefs {
		if _, err := officers.Add(ctx, "admin", "Budi Santoso", "budi", "abcd12"); err != nil {
			t.Fatal(err)
		}
		e.addList("gate_name", "Gate 1")
		e.addList("checkout_reason", "Tugas luar")
	}
	return e
}

func (e *env) addList(key, value string) {
	e.t.Helper()
	def, _ := reflist.Find(key)
	if _, err := e.lists.Add(ctx, "admin", def, []string{value}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) run() (Report, error) {
	e.t.Helper()
	return e.s.RunOnce(ctx)
}

func (e *env) mustRun() Report {
	e.t.Helper()
	r, err := e.run()
	if err != nil {
		e.t.Fatalf("run: %v", err)
	}
	return r
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.d.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.d.QueryRow(q, args...).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) permit(id, lep, status string) {
	e.t.Helper()
	e.exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, nationality, requester_name, requester_email,
		work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES (?, ?, ?, 1, 'Budi Santoso', 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore', '2027-06-30', '2027-01-17', '2026-11-06 08:00:00')`,
		id, lep, status)
}

func (e *env) ticket(id, lep, status, recordStart string) {
	e.t.Helper()
	e.exec(`INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, rotation_no, record_start, submitted_at)
		VALUES (?, ?, 1, ?, '2026-11-10', '2026-12-01', 'Onshore', 1, ?, '2026-11-06 09:00:00')`, id, lep, status, recordStart)
}

func (e *env) waiver(id string) {
	e.t.Helper()
	w := waiverlog.NewWaiver{RequestID: id, Status: "Approved", ApplicantType: "VisitorCompany", CompanyID: 1, Company: "PT Tamu", Name: "Tamu Satu",
		Gender: "Male", DOB: "1990-04-02", Nationality: "Indonesia", NIK: "3201020304900001", Position: "Auditor", Department: "HSE",
		VisitLocation: "Site A", Purpose: "Audit", VisitFrom: "2026-11-10", VisitTo: "2026-11-12", Days: 3, RequesterName: "R", RequesterEmail: "r@x.id",
		Phone: "081234567890", Risk: "r", Precaution: "p", Prevent: "v", DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00"}
	if err := (waiverlog.Store{DB: e.d}).Create(ctx, w); err != nil {
		e.t.Fatal(err)
	}
}

func lepOf(i int) string { return fmt.Sprintf("LEP-%05d", i) }

// ── push ─────────────────────────────────────────────────────────────────────

func TestFirstRunPushesEverythingAndASecondRunPushesNothing(t *testing.T) {
	e := newEnv(t, true)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.permit("WEP-B0001", "", "PendingFinal") // no LEP yet: never sent
	e.waiver("WVR-CCCCC")
	e.ticket("TKT-00001", "LEP-AAAAA", "Approved", "")
	e.ticket("TKT-00002", "LEP-AAAAA", "Rejected", "") // not sent

	r := e.mustRun()
	if r.PermitsPushed != 2 || r.TicketsPushed != 1 || !r.RefsPushed {
		t.Fatalf("report %+v", r)
	}
	if got := e.w.pushedLeps(); len(got) != 2 || !got["LEP-AAAAA"] || !got["WVR-CCCCC"] {
		t.Errorf("pushed %v", got)
	}
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	var permit, waiver map[string]any
	for _, c := range e.w.pushCalls {
		for _, p := range c.Permits {
			if p["lep"] == "LEP-AAAAA" {
				permit = p
			}
			if p["lep"] == "WVR-CCCCC" {
				waiver = p
			}
		}
	}
	if permit["permitValidUntil"] != "2027-01-17" || permit["mcu"] != "2027-06-30" || permit["name"] != "Budi Santoso" || permit["status"] != "Approved" {
		t.Errorf("permit wire %v", permit)
	}
	if waiver["arrival"] != "2026-11-10" || waiver["departure"] != "2026-11-12" || waiver["permitValidUntil"] != "2026-11-12" || waiver["mcu"] != "" {
		t.Errorf("waiver wire %v", waiver)
	}
	if tk := e.w.tickets["TKT-00001"]; tk["status"] != "PLANNED" || tk["lep"] != "LEP-AAAAA" || tk["plannedArrival"] != "2026-11-10" {
		t.Errorf("ticket wire %v", tk)
	}
	officers, _ := e.w.refs["officers"].([]any)
	if len(officers) != 1 {
		t.Fatalf("officers %v", e.w.refs["officers"])
	}
	o := officers[0].(map[string]any)
	if o["name"] != "Budi Santoso" || !strings.HasPrefix(fmt.Sprint(o["verifier"]), "pbkdf2-sha256$") || strings.Contains(fmt.Sprint(o), "abcd12") {
		t.Errorf("officer wire %v", o)
	}
	if fmt.Sprint(e.w.refs["gates"]) != "[Gate 1]" || fmt.Sprint(e.w.refs["reasons"]) != "[Tugas luar]" ||
		fmt.Sprint(e.w.refs["completingReasons"]) != "[Sesuai jadwal keluar Selesai awal]" {
		t.Errorf("refs %v", e.w.refs)
	}
	if fmt.Sprint(e.w.refs["settings"]) != "map[gate_duplicate_scan_seconds:5]" {
		t.Errorf("settings %v", e.w.refs["settings"])
	}
}

func TestOnlyWhatChangedIsSentAgain(t *testing.T) {
	e := newEnv(t, true)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.permit("WEP-B0001", "LEP-BBBBB", "Approved")
	e.mustRun()
	calls := len(e.w.pushCalls)
	if r := e.mustRun(); r.PermitsPushed+r.TicketsPushed+r.Deactivated != 0 || r.RefsPushed || len(e.w.pushCalls) != calls {
		t.Fatalf("an unchanged second run sent something: %+v", r)
	}
	e.exec(`UPDATE permit SET lep_valid_until = '2026-12-31' WHERE lep_no = 'LEP-BBBBB'`)
	r := e.mustRun()
	if r.PermitsPushed != 1 || len(e.w.pushCalls) != calls+1 || e.w.pushCalls[calls].Permits[0]["lep"] != "LEP-BBBBB" {
		t.Errorf("only the changed permit goes again: %+v", r)
	}
}

func TestAPushOfManyPermitsGoesInChunksAndAFailedChunkIsRetriedAlone(t *testing.T) {
	e := newEnv(t, false)
	for i := 1; i <= 95; i++ {
		e.permit(fmt.Sprintf("WEP-%05d", i), lepOf(i), "Approved")
	}
	e.w.set(func() { e.w.failPushOnCall = 2 })
	r, err := e.run()
	if err == nil {
		t.Fatal("a failed chunk must be reported")
	}
	if r.PermitsPushed != 40 || e.count(`SELECT COUNT(*) FROM gate_push WHERE item_key LIKE 'permit:%'`) != 40 {
		t.Fatalf("only the chunk the Worker accepted is marked sent: %+v", r)
	}
	if e.count(`SELECT COUNT(*) FROM gate_sync_run WHERE ok = 0`) != 1 {
		t.Error("the failure is recorded")
	}
	e.w.set(func() { e.w.failPushOnCall = 0 })
	r = e.mustRun()
	if r.PermitsPushed != 55 {
		t.Errorf("the retry sends the other 55 only, got %d", r.PermitsPushed)
	}
	for _, c := range e.w.pushCalls {
		if len(c.Permits)+len(c.TripTickets) > 40 {
			t.Errorf("a call carried %d items", len(c.Permits)+len(c.TripTickets))
		}
	}
	if len(e.w.pushedLeps()) != 95 {
		t.Errorf("the gate has %d permits", len(e.w.pushedLeps()))
	}
}

func TestReferencesAreSentOnChangeAndAtLeastHourly(t *testing.T) {
	e := newEnv(t, true)
	refCalls := func() int {
		n := 0
		for _, c := range e.w.pushCalls {
			if c.References != nil {
				n++
			}
		}
		return n
	}
	e.mustRun()
	e.mustRun()
	if refCalls() != 1 {
		t.Fatalf("unchanged lists were sent %d times", refCalls())
	}
	e.addList("gate_name", "Gate 2")
	e.mustRun()
	if refCalls() != 2 {
		t.Errorf("a changed list is sent at once, calls %d", refCalls())
	}
	e.clock.now = "2026-11-10 10:30:00"
	e.mustRun()
	if refCalls() != 2 {
		t.Errorf("not yet an hour, calls %d", refCalls())
	}
	e.clock.now = "2026-11-10 11:01:00"
	e.mustRun()
	if refCalls() != 3 {
		t.Errorf("an hour later they are sent again (a wiped D1 gets them back), calls %d", refCalls())
	}
}

func TestAnEmptyListIsNeverSentAndTheRunSaysSo(t *testing.T) {
	e := newEnv(t, false)
	e.addList("gate_name", "Gate 1") // reasons and officers stay empty
	r := e.mustRun()
	if len(e.w.pushCalls) != 1 {
		t.Fatalf("calls %d", len(e.w.pushCalls))
	}
	refs := e.w.pushCalls[0].References
	for _, k := range []string{"officers", "reasons"} {
		if _, sent := refs[k]; sent {
			t.Errorf("an empty %s list must not be sent (it would be skipped, and the Pi would think it was)", k)
		}
	}
	if len(refs["gates"].([]any)) != 1 {
		t.Errorf("gates %v", refs["gates"])
	}
	joined := strings.Join(r.Warnings, " | ")
	if !strings.Contains(joined, "no officers") || !strings.Contains(joined, "no checkout reasons") {
		t.Errorf("warnings %q", joined)
	}
}

func TestDeactivationsGoOncePerPermitThatWasPushedAndNeverForOthers(t *testing.T) {
	e := newEnv(t, false)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.permit("WEP-B0001", "LEP-BBBBB", "Approved")
	e.mustRun()
	e.exec(`UPDATE permit SET status = 'Resigned' WHERE lep_no = 'LEP-AAAAA'`)
	e.exec(`UPDATE permit SET status = 'Renewed by LEP-ZZZZZ' WHERE lep_no = 'LEP-BBBBB'`)
	e.permit("WEP-N0001", "LEP-NEVER", "Resigned") // never reached the gate

	r := e.mustRun()
	if r.Deactivated != 2 {
		t.Fatalf("report %+v", r)
	}
	if fmt.Sprint(e.w.deactivated["Resigned"]) != "[LEP-AAAAA]" || fmt.Sprint(e.w.deactivated["Renewed by LEP-ZZZZZ"]) != "[LEP-BBBBB]" || len(e.w.deactivated) != 2 {
		t.Errorf("deactivated %v", e.w.deactivated)
	}
	calls := e.w.deactCalls
	e.mustRun()
	if e.w.deactCalls != calls {
		t.Error("a deactivation is sent once, not at every sync")
	}
}

// ── pull ─────────────────────────────────────────────────────────────────────

func TestGateScansAreStoredOnceEvenWhenTheAckIsLost(t *testing.T) {
	e := newEnv(t, false)
	e.w.addLog(1, "LEP-AAAAA", "checkin", "2026-11-10T01:30:00.000Z")
	e.w.addLog(2, "LEP-AAAAA", "checkout", "2026-11-10T09:00:00.000Z")
	e.w.addLog(3, "LEP-BBBBB", "checkin", "2026-11-10T02:00:00.000Z")
	e.w.set(func() { e.w.dropAcks = true })

	if r := e.mustRun(); r.LogsApplied != 3 {
		t.Fatalf("first run applied %d", r.LogsApplied)
	}
	// The Worker still thinks nothing was acked, so the same rows come back.
	if r := e.mustRun(); r.LogsApplied != 0 {
		t.Errorf("a repeat applied %d new rows", r.LogsApplied)
	}
	if e.count(`SELECT COUNT(*) FROM gate_log`) != 3 {
		t.Fatalf("rows %d: a lost ack must never duplicate a scan", e.count(`SELECT COUNT(*) FROM gate_log`))
	}
	e.w.set(func() { e.w.dropAcks = false })
	e.mustRun() // this time the ack lands
	r := e.mustRun()
	if r.LogsApplied != 0 || e.count(`SELECT COUNT(*) FROM gate_log`) != 3 {
		t.Errorf("after the ack nothing comes back: %+v", r)
	}
	if at := e.str(`SELECT at FROM gate_log WHERE worker_id = 1`); at != "2026-11-10 08:30:00" {
		t.Errorf("a Worker UTC time is stored as Jakarta time, got %q", at)
	}
	if day := e.str(`SELECT day FROM gate_log WHERE worker_id = 1`); day != "2026-11-10" {
		t.Errorf("day %q", day)
	}
}

func TestGateStateTheNewestUpdateWins(t *testing.T) {
	e := newEnv(t, false)
	e.w.addState("LEP-AAAAA", "INSIDE", "2026-11-10T03:00:00.000Z")
	e.mustRun()
	if s := e.str(`SELECT status FROM gate_state WHERE lep_no = 'LEP-AAAAA'`); s != "INSIDE" {
		t.Fatalf("status %s", s)
	}
	e.w.addState("LEP-AAAAA", "OUTSIDE", "2026-11-10T02:00:00.000Z") // older: a late duplicate
	e.mustRun()
	if s := e.str(`SELECT status FROM gate_state WHERE lep_no = 'LEP-AAAAA'`); s != "INSIDE" {
		t.Errorf("an older update overwrote a newer one: %s", s)
	}
	e.w.addState("LEP-AAAAA", "OUTSIDE", "2026-11-10T04:00:00.000Z")
	e.mustRun()
	if s := e.str(`SELECT status FROM gate_state WHERE lep_no = 'LEP-AAAAA'`); s != "OUTSIDE" {
		t.Errorf("a newer update must win: %s", s)
	}
}

func TestTheGateOwnsFirstCheckInAndClosingButNeverThePlan(t *testing.T) {
	e := newEnv(t, false)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.ticket("TKT-00001", "LEP-AAAAA", "Approved", "")
	e.w.addTicketChange("TKT-00001", "LEP-AAAAA", "ACTIVE", "2026-11-10T01:00:00.000Z", "", "")
	e.mustRun()
	if got := e.str(`SELECT record_start FROM ticket`); got != "2026-11-10 08:00:00" {
		t.Errorf("record_start %q", got)
	}
	if got := e.str(`SELECT status FROM ticket`); got != "Approved" {
		t.Errorf("the Pi ticket stays Approved while the worker is on site, got %q", got)
	}

	e.w.addTicketChange("TKT-00001", "LEP-AAAAA", "COMPLETED", "2026-11-10T01:00:00.000Z", "2026-11-28T03:00:00.000Z", "Sesuai jadwal keluar")
	e.mustRun()
	if e.str(`SELECT status FROM ticket`) != "Closed" || e.str(`SELECT closed_at FROM ticket`) != "2026-11-28 10:00:00" || e.str(`SELECT closed_reason FROM ticket`) != "Sesuai jadwal keluar" {
		t.Errorf("closing not applied: %s %s %s", e.str(`SELECT status FROM ticket`), e.str(`SELECT closed_at FROM ticket`), e.str(`SELECT closed_reason FROM ticket`))
	}
	if e.str(`SELECT arrival FROM ticket`) != "2026-11-10" || e.str(`SELECT departure FROM ticket`) != "2026-12-01" {
		t.Error("the plan belongs to the Pi and must not change")
	}
}

func TestATicketTheGateKnowsButTheAppDoesNotIsSkippedAndAcked(t *testing.T) {
	e := newEnv(t, false)
	e.w.addTicketChange("TKT-GHOST", "LEP-AAAAA", "ACTIVE", "2026-11-10T01:00:00.000Z", "", "")
	r := e.mustRun()
	if r.TicketsSkipped != 1 || r.TicketsApplied != 0 {
		t.Errorf("report %+v", r)
	}
	if r2 := e.mustRun(); r2.TicketsSkipped != 0 {
		t.Error("an unknown ticket must be acked, or it would block the queue for ever")
	}
}

func TestGateChangesAreReadBeforePushingSoAPushNeverUndoesThem(t *testing.T) {
	e := newEnv(t, false)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.ticket("TKT-00001", "LEP-AAAAA", "Approved", "")
	e.w.addTicketChange("TKT-00001", "LEP-AAAAA", "ACTIVE", "2026-11-10T01:00:00.000Z", "", "")
	e.mustRun()
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	tk := e.w.pushCalls[0].TripTickets[0]
	if tk["status"] != "ACTIVE" || tk["recordStart"] != "2026-11-10T01:00:00.000Z" {
		t.Errorf("the pushed ticket must already carry the gate's check-in: %v", tk)
	}
}

// ── failure, auth, status, loop ──────────────────────────────────────────────

func TestAWorkerThatIsDownLosesNothingAndTheNextRunCatchesUp(t *testing.T) {
	e := newEnv(t, true)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.w.set(func() { e.w.down = true })
	if _, err := e.run(); err == nil {
		t.Fatal("a down Worker must be an error")
	}
	if e.count(`SELECT COUNT(*) FROM gate_push`) != 0 {
		t.Error("nothing may be marked sent")
	}
	if e.count(`SELECT COUNT(*) FROM gate_sync_run WHERE ok = 0 AND detail <> ''`) != 1 {
		t.Error("the failure and its reason are recorded")
	}
	e.w.set(func() { e.w.down = false })
	r := e.mustRun()
	if r.PermitsPushed != 1 || !r.RefsPushed {
		t.Errorf("catch-up %+v", r)
	}
}

func TestTheSecretTravelsInTheHeaderOnlyAndNeverAppearsInAnError(t *testing.T) {
	e := newEnv(t, true)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.mustRun()
	if e.w.lastAuth != "Bearer "+testSecret {
		t.Errorf("header %q", e.w.lastAuth)
	}
	for _, c := range e.w.pushCalls {
		if c.SecretInBody != "" {
			t.Error("the secret must not be in the body")
		}
	}
	e.s.Client.Secret = "wrong-secret-value"
	_, err := e.run()
	if err == nil {
		t.Fatal("a wrong secret must fail")
	}
	if strings.Contains(err.Error(), "wrong-secret-value") || strings.Contains(err.Error(), testSecret) {
		t.Errorf("the error leaks the secret: %v", err)
	}
	if detail := e.str(`SELECT detail FROM gate_sync_run ORDER BY id DESC LIMIT 1`); strings.Contains(detail, "wrong-secret-value") {
		t.Error("the run log leaks the secret")
	}
}

func TestLastSuccessIgnoresFailedRuns(t *testing.T) {
	e := newEnv(t, false)
	if got, _ := LastSuccess(ctx, e.d); got != "" {
		t.Errorf("before any run: %q", got)
	}
	e.mustRun()
	first, _ := LastSuccess(ctx, e.d)
	if first != "2026-11-10 10:00:00" {
		t.Fatalf("last success %q", first)
	}
	e.clock.now = "2026-11-10 10:05:00"
	e.w.set(func() { e.w.down = true })
	_, _ = e.run()
	if got, _ := LastSuccess(ctx, e.d); got != first {
		t.Errorf("a failed run moved the last success to %q", got)
	}
}

func TestKickRunsASyncAtOnceInsteadOfWaitingForTheTick(t *testing.T) {
	e := newEnv(t, false)
	e.pol.GateSyncMinutes = 60
	runs := func() int { return e.count(`SELECT COUNT(*) FROM gate_sync_run`) }
	wait := func(want int) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if runs() >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("waited for %d runs, have %d", want, runs())
	}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.s.Run(cctx); close(done) }()
	wait(1) // the loop syncs once at start
	e.s.Kick()
	e.s.Kick() // a second kick while one is queued is harmless
	wait(2)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when its context ended")
	}
}

func TestTimeConversionBetweenTheWorkerAndThePi(t *testing.T) {
	if got := jakartaFromISO("2026-11-10T03:00:00.000Z"); got != "2026-11-10 10:00:00" {
		t.Errorf("got %q", got)
	}
	if got := jakartaFromISO("2026-11-10T20:00:00Z"); got != "2026-11-11 03:00:00" {
		t.Errorf("a late UTC evening is the next Jakarta day, got %q", got)
	}
	if got := isoFromJakarta("2026-11-10 10:00:00"); got != "2026-11-10T03:00:00.000Z" {
		t.Errorf("got %q", got)
	}
	for _, bad := range []string{"", "garbage", "2026-13-40T00:00:00Z"} {
		if jakartaFromISO(bad) != "" {
			t.Errorf("bad input %q must give an empty moment", bad)
		}
	}
	if isoFromJakarta("") != "" || isoFromJakarta("nope") != "" {
		t.Error("bad Jakarta moments give an empty string")
	}
}

// ── what the daily jobs change reaches the gate ──────────────────────────────

func TestATicketLockIsPushedAndPushedAgainWhenItChanges(t *testing.T) {
	e := newEnv(t, false)
	e.permit("WEP-A0001", "LEP-AAAAA", "Approved")
	e.ticket("TKT-AAAAA", "LEP-AAAAA", "Approved", "")
	e.mustRun()
	first := e.w.pushCalls[0].TripTickets[0]
	if first["lepLocked"] != false {
		t.Fatalf("an unlocked ticket was pushed as locked: %v", first["lepLocked"])
	}

	// the daily job locks it (the permit expired): the next sync must carry the lock
	e.exec(`UPDATE ticket SET lep_locked = 1 WHERE ticket_id = 'TKT-AAAAA'`)
	r := e.mustRun()
	if r.TicketsPushed != 1 {
		t.Fatalf("report %+v: a lock was not pushed", r)
	}
	last := e.w.pushCalls[len(e.w.pushCalls)-1].TripTickets[0]
	if last["lepLocked"] != true {
		t.Errorf("the pushed ticket is not locked: %v", last["lepLocked"])
	}
	if again := e.mustRun(); again.TicketsPushed != 0 {
		t.Error("an unchanged lock was pushed again")
	}
}

func TestACompletedWaiverIsDeactivatedAtTheGateOnce(t *testing.T) {
	e := newEnv(t, false)
	e.waiver("WVR-CCCCC")
	e.mustRun()                                                                     // the approved waiver reaches the gate
	e.exec(`UPDATE waiver SET status = 'Completed' WHERE request_id = 'WVR-CCCCC'`) // the departure job ended the visit

	r := e.mustRun()
	if r.Deactivated != 1 || fmt.Sprint(e.w.deactivated["Completed"]) != "[WVR-CCCCC]" {
		t.Fatalf("report %+v, deactivated %v", r, e.w.deactivated)
	}
	calls := e.w.deactCalls
	e.mustRun()
	if e.w.deactCalls != calls {
		t.Error("the deactivation was sent again")
	}
}
