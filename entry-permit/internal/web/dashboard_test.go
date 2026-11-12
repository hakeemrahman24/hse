package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/staff"
)

// dashGate puts a worker at the gate, as gatesync would have stored it.
func (e *testEnv) dashGate(t *testing.T, lep, status, name string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO gate_state (lep_no, status, last_action, last_action_at, officer, gate, first_checkin, last_checkout, reason, worker_updated_at, received_at)
		VALUES (?, ?, 'checkin', '2026-11-06 07:00:00', 'Sari Gate', 'Gate 1', '2026-11-06 07:00:00', '', '', '2026-11-06T00:00:00.000Z', '2026-11-06 07:05:00')`, lep, status); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO gate_log (worker_id, at, lep_no, worker_name, company, action, reason, officer, session_id, day, gate, received_at)
		VALUES ((SELECT COUNT(*) + 1 FROM gate_log), '2026-11-06 07:00:00', ?, ?, 'PT Maju', 'checkin', '', 'Sari Gate', 's', '2026-11-06', 'Gate 1', '2026-11-06 07:05:00')`, lep, name); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) dashSynced(t *testing.T, ok int, at string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO gate_sync_run (job, ran_at, ok, detail) VALUES ('gate_sync', ?, ?, 'x')`, at, ok); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardNeedsASignedInStaffMember(t *testing.T) {
	e := newEnv(t)
	for _, page := range []string{"/staff/dashboard", "/staff/gate"} {
		r, _ := e.get(t, page, nil)
		if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/login" {
			t.Errorf("%s signed out: %d %s", page, r.StatusCode, r.Header.Get("Location"))
		}
	}
}

func TestDashboardOpensOnActiveWithACountOnEveryTab(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.reviewPermit(t, "WEP-00002", "PendingStage1", 2, "", "", "2027-01-31")
	e.dashGate(t, "LEP-AAAAA", "INSIDE", "Budi Santoso")
	cookies := e.reviewer(t, "viewer", staff.Dashboard)

	r, body := e.get(t, "/staff/dashboard", cookies)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{"Budi Santoso", "LEP-AAAAA", "Inside", `href="/staff/dashboard?tab=pending"`, "Pending (1)", "Registered (1)", "Active (1)", "Scheduled (0)", "Gate (1)"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// the dashboard is open to the shared Viewer account: it never carries a birth date, e-mail or document path
	for _, never := range []string{"rina@example.com", "id_doc.pdf", "1990"} {
		if strings.Contains(body, never) {
			t.Errorf("page leaks %q", never)
		}
	}
}

func TestDashboardTabsShowTheirOwnList(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.reviewPermit(t, "WEP-00002", "PendingFinal", 2, "", "", "2027-01-31")
	cookies := e.reviewer(t, "viewer", staff.Dashboard)

	_, pending := e.get(t, "/staff/dashboard?tab=pending", cookies)
	if !strings.Contains(pending, "WEP-00002") || !strings.Contains(pending, "Waiting for Final") || strings.Contains(pending, "LEP-AAAAA") {
		t.Errorf("pending tab: %s", pending)
	}
	_, registered := e.get(t, "/staff/dashboard?tab=registered", cookies)
	if !strings.Contains(registered, "LEP-AAAAA") || strings.Contains(registered, "WEP-00002") {
		t.Errorf("registered tab: %s", registered)
	}
	r, fallback := e.get(t, "/staff/dashboard?tab=nonsense", cookies) // an unknown tab is the default, not an error
	if r.StatusCode != 200 || !strings.Contains(fallback, "Active (") {
		t.Errorf("unknown tab: %d", r.StatusCode)
	}
}

func TestDashboardSearchFiltersAndEscapesWhatWasTyped(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	cookies := e.reviewer(t, "viewer", staff.Dashboard)

	_, hit := e.get(t, "/staff/dashboard?tab=registered&q=santoso", cookies)
	if !strings.Contains(hit, "LEP-AAAAA") {
		t.Error("search for a name found nothing")
	}
	_, miss := e.get(t, "/staff/dashboard?tab=registered&q=nobody", cookies)
	if strings.Contains(miss, "LEP-AAAAA") || !strings.Contains(miss, "No one matches") {
		t.Error("search with no match should say so")
	}
	_, xss := e.get(t, "/staff/dashboard?tab=registered&q=%3Cscript%3Ealert(1)%3C/script%3E", cookies)
	if strings.Contains(xss, "<script>alert") {
		t.Error("the search text came back unescaped")
	}
}

func TestDashboardShowsWhenTheGateWasLastSyncedAndWarnsWhenItIsOld(t *testing.T) {
	e := newEnv(t) // the clock reads 2026-11-06 08:00:00; the default sync interval is 5 minutes
	cookies := e.reviewer(t, "viewer", staff.Dashboard)

	_, never := e.get(t, "/staff/dashboard?tab=gate", cookies)
	if !strings.Contains(never, "not been synced") {
		t.Error("a gate that never synced should say so")
	}
	e.dashSynced(t, 1, "2026-11-06 07:58:00")
	_, fresh := e.get(t, "/staff/dashboard?tab=gate", cookies)
	if !strings.Contains(fresh, "2026-11-06 07:58:00") || strings.Contains(fresh, "out of date") {
		t.Errorf("fresh sync: %s", fresh)
	}
	e.dashSynced(t, 0, "2026-11-06 07:59:00") // a failed run does not count as a sync
	_, still := e.get(t, "/staff/dashboard?tab=gate", cookies)
	if !strings.Contains(still, "2026-11-06 07:58:00") {
		t.Error("a failed run replaced the last good sync time")
	}
	e.clock.now = "2026-11-06 09:00:00"
	_, stale := e.get(t, "/staff/dashboard?tab=gate", cookies)
	if !strings.Contains(stale, "out of date") {
		t.Error("an old sync should be flagged")
	}
}

func TestGatePageIsForGateOfficersAndShowsWhoIsInside(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.dashGate(t, "LEP-AAAAA", "INSIDE", "Budi Santoso")

	viewer := e.reviewer(t, "viewer", staff.Dashboard)
	if r, _ := e.get(t, "/staff/gate", viewer); r.StatusCode != http.StatusForbidden {
		t.Errorf("viewer on the gate page: %d", r.StatusCode)
	}
	officer := e.reviewer(t, "officer", staff.GateOfficer)
	r, body := e.get(t, "/staff/gate", officer)
	if r.StatusCode != 200 || !strings.Contains(body, "Budi Santoso") || !strings.Contains(body, "Inside now (1)") {
		t.Errorf("gate page: %d %s", r.StatusCode, body)
	}
}

func TestGatePageLooksUpOnePermitFromTheScanLink(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.dashGate(t, "LEP-AAAAA", "INSIDE", "Budi Santoso")
	if _, err := e.db.Exec(`UPDATE permit SET lep_valid_until = '2027-01-17'`); err != nil {
		t.Fatal(err)
	}
	officer := e.reviewer(t, "officer", staff.GateOfficer)

	_, found := e.get(t, "/staff/gate?lep=lep-aaaaa", officer) // any case
	for _, want := range []string{"LEP-AAAAA", "Budi Santoso", "Valid", "checkin"} {
		if !strings.Contains(found, want) {
			t.Errorf("lookup lacks %q", want)
		}
	}
	_, unknown := e.get(t, "/staff/gate?lep=LEP-ZZZZZ", officer)
	if !strings.Contains(unknown, "No permit with that number") {
		t.Error("an unknown number should say so")
	}
	_, bad := e.get(t, "/staff/gate?lep=%3Cscript%3Ealert(1)%3C/script%3E", officer) // wrong shape: never echoed raw
	if strings.Contains(bad, "<script>alert") || !strings.Contains(bad, "LEP-ABCDE") {
		t.Error("a malformed number was echoed or not explained")
	}
}

func TestTheScanLinkLandsOnTheGatePage(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	officer := e.reviewer(t, "officer", staff.GateOfficer)
	r, _ := e.get(t, "/c/LEP-AAAAA", officer)
	loc := r.Header.Get("Location")
	if r.StatusCode != http.StatusSeeOther || loc != "/staff/gate?lep=LEP-AAAAA" {
		t.Fatalf("card for a gate officer: %d %s", r.StatusCode, loc)
	}
	if r2, _ := e.get(t, loc, officer); r2.StatusCode != 200 {
		t.Errorf("the redirect target gave %d", r2.StatusCode)
	}
}

// ── PDF export ───────────────────────────────────────────────────────────────

func TestPDFExportNeedsASignedInStaffMember(t *testing.T) {
	e := newEnv(t)
	r, _ := e.get(t, "/staff/dashboard.pdf?tab=registered", nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/login" {
		t.Errorf("signed out: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
}

func TestPDFExportIsAnAttachmentThatIsNeverCached(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	cookies := e.reviewer(t, "viewer", staff.Dashboard)

	r, body := e.get(t, "/staff/dashboard.pdf?tab=registered", cookies)
	if r.StatusCode != 200 || !strings.HasPrefix(body, "%PDF-") {
		t.Fatalf("status %d, body starts %q", r.StatusCode, body[:min(8, len(body))])
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); cd != `attachment; filename="dashboard-registered-2026-11-06.pdf"` {
		t.Errorf("content disposition %q", cd)
	}
	if cc := r.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("cache control %q: a list of people must not be cached", cc)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	// an unknown tab exports the default tab, and its name is a fixed word, never what was typed
	r2, _ := e.get(t, "/staff/dashboard.pdf?tab=../../etc", cookies)
	if cd := r2.Header.Get("Content-Disposition"); r2.StatusCode != 200 || !strings.Contains(cd, "dashboard-active-2026-11-06.pdf") {
		t.Errorf("unknown tab: %d %q", r2.StatusCode, cd)
	}
}

func TestPDFExportCarriesTheTabTheSearchAndWhoAskedForIt(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.dashGate(t, "LEP-AAAAA", "INSIDE", "Budi Santoso")
	e.dashSynced(t, 1, "2026-11-06 07:58:00")
	u := &staff.User{Username: "viewer", DisplayName: "Viewer"}

	hit, err := e.app.dashData(context.Background(), url.Values{"tab": {"registered"}, "q": {" santoso "}})
	if err != nil {
		t.Fatal(err)
	}
	doc := exportDoc(hit, u, "2026-11-06 08:00:00")
	if doc.Tab != "registered" || doc.Title != "Dashboard: Registered" || doc.Query != "santoso" || doc.By != "Viewer" ||
		doc.GeneratedAt != "2026-11-06 08:00:00" || len(doc.Rows) != 1 || !strings.Contains(doc.Sync, "2026-11-06 07:58:00") {
		t.Errorf("doc = %+v", doc)
	}
	miss, _ := e.app.dashData(context.Background(), url.Values{"tab": {"registered"}, "q": {"nobody"}})
	if got := exportDoc(miss, u, "x"); len(got.Rows) != 0 {
		t.Errorf("a search with no match still has %d rows", len(got.Rows))
	}
	gate, _ := e.app.dashData(context.Background(), url.Values{"tab": {"gate"}, "q": {""}})
	if g := exportDoc(gate, u, "x"); len(g.Inside) != 1 || len(g.Activity) != 1 || g.Rows != nil {
		t.Errorf("gate doc = %+v", g)
	}
	e.clock.now = "2026-11-06 09:00:00" // an old sync is said to be old in the file too
	stale, _ := e.app.dashData(context.Background(), url.Values{"tab": {"gate"}, "q": {""}})
	if g := exportDoc(stale, u, "x"); !strings.Contains(g.Sync, "out of date") {
		t.Errorf("stale sync line = %q", g.Sync)
	}
	if never := syncLine(SyncNote{Never: true}); !strings.Contains(never, "not been synced") {
		t.Errorf("never line = %q", never)
	}
}

func TestPDFExportIsWrittenToTheAuditLog(t *testing.T) {
	e := newEnv(t)
	cookies := e.reviewer(t, "viewer", staff.Dashboard)
	if r, _ := e.get(t, "/staff/dashboard.pdf?tab=registered&q=santoso", cookies); r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	var actor, action, target, search string
	if err := e.db.QueryRow(`SELECT actor, action, target, new_value FROM audit_log WHERE action = 'dashboard.export_pdf'`).Scan(&actor, &action, &target, &search); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	if actor != "viewer" || target != "dashboard:registered" || search != "santoso" {
		t.Errorf("audit row = %q %q %q %q", actor, action, target, search)
	}
}

func TestPDFExportIsRefusedWhenItCannotBeRecorded(t *testing.T) {
	e := newEnv(t)
	cookies := e.reviewer(t, "viewer", staff.Dashboard)
	if _, err := e.db.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatal(err)
	}
	r, body := e.get(t, "/staff/dashboard.pdf?tab=registered", cookies)
	if r.StatusCode != http.StatusInternalServerError || strings.HasPrefix(body, "%PDF") {
		t.Errorf("an export that cannot be recorded was served: %d", r.StatusCode)
	}
}

func TestTheDashboardOffersThePDFOfWhatIsOnScreen(t *testing.T) {
	e := newEnv(t)
	cookies := e.reviewer(t, "viewer", staff.Dashboard)
	_, body := e.get(t, "/staff/dashboard?tab=registered&q=as%27adul+haq", cookies)
	if !strings.Contains(body, `href="/staff/dashboard.pdf?`) || !strings.Contains(body, "tab=registered") || !strings.Contains(body, "Download PDF") {
		t.Errorf("no PDF link carrying the tab:\n%s", body)
	}
}

// The filter bar's choices come in as query values; the page and the PDF read them the same way.
func TestDashboardFiltersChartsAndGateDay(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	e.reviewPermit(t, "WEP-00002", "Approved", 2, "", "LEP-BBBBB", "2027-01-31")
	if _, err := e.db.Exec(`UPDATE permit SET company = 'PT Lain', lep_valid_until = '2026-11-20' WHERE request_id = 'WEP-00002'`); err != nil {
		t.Fatal(err)
	}
	e.reviewPermit(t, "WEP-00003", "PendingStage1", 3, "", "", "2027-01-31")
	ctx := context.Background()

	d, err := e.app.dashData(ctx, url.Values{"tab": {"registered"}, "company": {"PT Lain", "PT Gone"}})
	if err != nil || len(d.Rows) != 1 || d.Rows[0].ID != "LEP-BBBBB" || d.CompanyLabel != "PT Lain" || !d.Filtered || d.Total != 2 {
		t.Fatalf("company filter: %+v %v", d, err)
	}
	if len(d.Slices) != 1 || d.Slices[0].Label != "PT Lain" || len(d.CompanyOpts) != 2 {
		t.Errorf("slices %+v options %+v", d.Slices, d.CompanyOpts)
	}
	soon, _ := e.app.dashData(ctx, url.Values{"tab": {"registered"}, "soon": {"1"}})
	if len(soon.Rows) != 1 || soon.SoonLabel != "Expiring in 1 month" {
		t.Errorf("soon: %+v", soon.Rows)
	}
	if none, _ := e.app.dashData(ctx, url.Values{"tab": {"pending"}, "soon": {"1"}, "kind": {"visitor"}}); len(none.Rows) != 1 || none.Soon {
		t.Errorf("pending ignores the tab-only filters: %+v", none)
	}
	p, _ := e.app.dashData(ctx, url.Values{"tab": {"pending"}})
	if len(p.Stages) != 3 || p.Stages[0].Count != 1 || len(p.Approval) == 0 {
		t.Errorf("pending summary: %+v %+v", p.Stages, p.Approval)
	}
	// The gate day: a future or broken date is today, and the PDF link keeps the filters.
	g, _ := e.app.dashData(ctx, url.Values{"tab": {"gate"}, "date": {"2999-01-01"}})
	if g.Day != "2026-11-06" || g.NextDay != "" || g.PrevDay != "2026-11-05" {
		t.Errorf("gate day %q prev %q next %q", g.Day, g.PrevDay, g.NextDay)
	}
	old, _ := e.app.dashData(ctx, url.Values{"tab": {"gate"}, "date": {"2026-11-01"}})
	if old.Day != "2026-11-01" || old.NextDay != "2026-11-02" || !old.Filtered {
		t.Errorf("old day: %+v", old)
	}
	if !strings.Contains(d.PDFHref, "company=PT+Lain") || !strings.Contains(d.PDFHref, "tab=registered") {
		t.Errorf("PDF link %q", d.PDFHref)
	}
	// And on screen: the pie is drawn without inline style (the page's policy forbids it).
	c := e.reviewer(t, "viewer", staff.Dashboard)
	_, body := e.get(t, "/staff/dashboard?tab=registered", c)
	if !strings.Contains(body, `class="pie"`) || strings.Contains(body, "style=") || !strings.Contains(body, `name="company"`) {
		t.Errorf("registered page lacks the chart or filters, or uses inline style")
	}
}

// Rest between tickets: the Rest tab lists each gap with the proposal rules' verdict, and Short
// breaks only keeps the ones that fall short of the required rest.
func TestDashboardRestTabListsTheGapsBetweenTickets(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	for i, tk := range []struct{ from, to string }{
		{"2026-01-01", "2026-01-20"}, {"2026-01-21", "2026-02-10"}, {"2026-02-20", "2026-03-10"}, {"2026-05-01", "2026-05-20"},
	} {
		if _, err := e.db.Exec(`INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, pattern, rotation_no, submitted_at)
			VALUES (?, 'LEP-AAAAA', 1, 'Closed', ?, ?, 'Onshore', 'Normal', ?, '2026-01-01 08:00:00')`, fmt.Sprintf("TKT-0000%d", i), tk.from, tk.to, i+1); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	d, err := e.app.dashData(ctx, url.Values{"tab": {"rest"}})
	if err != nil || len(d.Rows) != 3 || d.ShowKind {
		t.Fatalf("rest tab: %d rows, showKind %v, %v", len(d.Rows), d.ShowKind, err)
	}
	if d.Rows[0].RestKind != "consecutive" || d.Rows[1].RestKind != "short" || d.Rows[1].RestDays != 10 || d.Rows[2].RestKind != "ok" || d.Rows[1].PrevEnd != "2026-02-10" {
		t.Errorf("rows %+v", d.Rows)
	}
	for kind, days := range map[string]int{"short": 10, "consecutive": 1, "ok": 52} {
		got, _ := e.app.dashData(ctx, url.Values{"tab": {"rest"}, "rest": {kind}})
		if len(got.Rows) != 1 || got.Rows[0].RestKind != kind || got.RestKind != kind || !got.Filtered || got.Rows[0].RestDays != days {
			t.Errorf("rest=%s: %+v", kind, got.Rows)
		}
	}
	if bad, _ := e.app.dashData(ctx, url.Values{"tab": {"rest"}, "rest": {"nonsense"}}); len(bad.Rows) != 3 || bad.RestKind != "" {
		t.Errorf("an unknown kind must show everything: %d rows", len(bad.Rows))
	}
	c := e.reviewer(t, "viewer", staff.Dashboard)
	_, body := e.get(t, "/staff/dashboard?tab=rest", c)
	if !strings.Contains(body, "Rest before it") || !strings.Contains(body, "10 days: short break") || !strings.Contains(body, "Rest (3)") || !strings.Contains(body, `name="rest"`) || !strings.Contains(body, "Normal (full rest)") {
		t.Errorf("rest page lacks the gaps:\n%s", body)
	}
}

// The worker's own ticket page shows the same rest before each ticket, and when a full rest ends.
func TestWorkerTicketPageShowsTheRestBeforeEachTicket(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-00001", "Approved", 1, "", "LEP-AAAAA", "2027-01-31")
	for i, tk := range []struct{ from, to string }{{"2026-02-01", "2026-02-10"}, {"2026-02-20", "2026-03-10"}} {
		if _, err := e.db.Exec(`INSERT INTO ticket (ticket_id, lep_no, is_work, status, arrival, departure, location, pattern, rotation_no, submitted_at)
			VALUES (?, 'LEP-AAAAA', 1, 'Closed', ?, ?, 'Onshore', 'Normal', ?, '2026-01-01 08:00:00')`, fmt.Sprintf("TKT-0000%d", i), tk.from, tk.to, i+1); err != nil {
			t.Fatal(err)
		}
	}
	d, status, msg := e.app.ticketOpen(context.Background(), "LEP-AAAAA", "tok")
	if status != 0 {
		t.Fatalf("ticketOpen: %d %s", status, msg)
	}
	if len(d.Tickets) != 2 || d.Tickets[0].Rest != "First ticket" || !strings.Contains(d.Tickets[1].Rest, "10 days: short break") || d.Tickets[1].RestClass != "state-bad" {
		t.Errorf("tickets %+v", d.Tickets)
	}
	if !strings.Contains(d.RestNote, "2026-03-24") { // the last ticket ended 2026-03-10; 14 days of rest
		t.Errorf("rest note %q", d.RestNote)
	}
}

func TestDashboardPieCanShowTheRingFromTheRequestAddress(t *testing.T) {
	e := newEnv(t)
	cookies := e.reviewer(t, "dewi", staff.Dashboard)
	e.cardPermit(t)
	if _, err := e.db.Exec(`UPDATE permit SET nationality = 'Indonesia', province = 'Papua Barat', regency = 'Sorong' WHERE lep_no = ?`, testLEP); err != nil {
		t.Fatal(err)
	}
	_, body := e.get(t, "/staff/dashboard?tab=registered&group=ring", cookies)
	for _, want := range []string{"Ring distribution", "Ring4", `value="ring" selected`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	_, plain := e.get(t, "/staff/dashboard?tab=registered", cookies)
	if strings.Contains(plain, "Ring distribution") {
		t.Error("the company pie is the default")
	}
}
