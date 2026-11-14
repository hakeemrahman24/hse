package mcusubmit

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestValidityOf(t *testing.T) {
	today := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name, result, until, want string
		days                      int
		hasValidity               bool
	}{
		{"far away", "Fit", "2027-06-01", ValValid, 200, true},
		{"inside window", "FitWithNote", "2026-12-01", ValExpiring, 18, true},
		{"last day is not expired", "Fit", "2026-11-13", ValExpiring, 0, true},
		{"yesterday", "Fit", "2026-11-12", ValExpired, -1, true},
		{"fit without date", "Fit", "", ValInProgress, 0, false},
		{"unfit ignores its date", "Unfit", "2027-06-01", ValUnfit, 0, false},
		{"temporary unfit", "TemporaryUnfit", "2027-06-01", ValTempUnfit, 0, false},
		{"follow up", "NeedFollowUp", "", ValFollowUp, 0, false},
		{"no result yet", "", "", ValInProgress, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, days, has := validityOf(c.result, c.until, today, 30)
			if got != c.want || days != c.days || has != c.hasValidity {
				t.Errorf("validityOf(%q, %q) = %q, %d, %v; want %q, %d, %v", c.result, c.until, got, days, has, c.want, c.days, c.hasValidity)
			}
		})
	}
}

func TestCompanyEmployeesListsOnlyTheCompanyWithTheirLatestCheckup(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	res, err := s.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Other')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	old := seedWorker(t, s, "Old And New", "1990-01-01", co)
	left := seedWorker(t, s, "Has Left", "1991-01-01", co)
	seedWorker(t, s, "Never Checked", "1992-01-01", co)
	seedWorker(t, s, "Someone Else", "1993-01-01", other)
	// Two checkups: the newer one (higher id, same date) decides.
	addRecord(t, s, old, "MedC-1", "Fit", "2020-01-01", "Close", 1)
	addRecord(t, s, old, "MedC-2", "Fit", "2099-01-01", "Open", 1)
	addRecord(t, s, left, "MedC-3", "Fit", "2099-01-01", "Archived / Exit", 1)

	got, err := s.CompanyEmployees(ctx, []int64{co}, 30)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, e := range got {
		status[e.Name] = e.Status
	}
	want := map[string]string{"Old And New": ValValid, "Never Checked": ValNone}
	if len(status) != len(want) || status["Old And New"] != ValValid || status["Never Checked"] != ValNone {
		t.Errorf("employees = %v, want %v (no other company, nobody who left)", status, want)
	}
}

func TestFilterEmployees(t *testing.T) {
	all := []Employee{{Name: "Budi Santoso", Status: ValValid}, {Name: "Santoso Adi", Status: ValExpired}, {Name: "Rina", Status: ValValid}}
	if got := filterEmployees(all, "santoso", "", ""); len(got) != 2 {
		t.Errorf("name filter kept %d, want 2", len(got))
	}
	if got := filterEmployees(all, "", ValValid, ""); len(got) != 2 {
		t.Errorf("status filter kept %d, want 2", len(got))
	}
	if got := filterEmployees(all, "adi santoso", ValExpired, ""); len(got) != 1 {
		t.Errorf("combined filter kept %d, want 1", len(got))
	}
}

// The page renders end to end: sidebar link, validity chips, the table, and only this company's workers.
func TestMyEmployeesPage(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	mine := seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	addRecord(t, e.store, mine, "MedC-9", "Fit", "2099-01-01", "Open", 1)
	seedWorker(t, e.store, "Nobody Yet", "1991-01-01", e.companyID)

	body := e.get("/mcu-submit/employees", cookie).Body.String()
	for _, want := range []string{"Budi Santoso", "2099-01-01", ">Valid<", "2 employees", `href="/mcu-submit/employees"`, "No checkup on record"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	one := e.get("/mcu-submit/employees?status=Valid", cookie).Body.String()
	if !strings.Contains(one, ">1 employee<") || strings.Contains(one, "Nobody Yet") {
		t.Errorf("status filter wrong")
	}
	if code := e.get("/mcu-submit/employees?status=Bogus", cookie).Code; code != 400 {
		t.Errorf("unknown status gave %d, want 400", code)
	}
}

func TestEmployeeCheckupsPageIsTranslated(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	worker := seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	addRecord(t, e.store, worker, "MedC-9", "Fit", "2099-01-01", "Open", 1)
	for _, lang := range []struct{ code, want string }{{"id", "Masa berlaku"}, {"zh", "有效状态"}} {
		resp := e.get("/mcu-submit/employees", cookie, &http.Cookie{Name: langCookie, Value: lang.code})
		if body := resp.Body.String(); !strings.Contains(body, lang.want) || strings.Contains(body, "page 1 of 1") {
			t.Errorf("%s page not translated", lang.code)
		}
	}
}

// Subcontractors' workers appear only when asked for, and only for an account that has subcontractors.
func TestEmployeeCheckupsIncludeSubcontractorsOnlyOnRequest(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	seedWorker(t, e.store, "Own Worker", "1990-01-01", e.companyID)
	sub := company(t, e.store, "PT Sub", e.companyID)
	seedWorker(t, e.store, "Sub Worker", "1991-01-01", sub)

	plain := e.get("/mcu-submit/employees", cookie).Body.String()
	if strings.Contains(plain, "Sub Worker") || !strings.Contains(plain, "Include subcontractors") {
		t.Errorf("default list must hold only the own company and offer the option")
	}
	withSubs := e.get("/mcu-submit/employees?subs=1", cookie).Body.String()
	if !strings.Contains(withSubs, "Sub Worker") || !strings.Contains(withSubs, "PT Sub") || !strings.Contains(withSubs, "Own Worker") {
		t.Errorf("subs=1 must add the subcontractor's workers with their company")
	}
}

// Employee checkups offers a certificate link for a worker whose latest checkup has one (Fit once Close,
// Unfit always), serves it to the own company and subcontractors, and gives another company's worker,
// a worker with no certificate and an unknown id the same 404.
func TestEmployeeCheckupsCertificateLinkAndDownload(t *testing.T) {
	u := newUploadEnv(t)
	certs := &fakeCerts{dir: t.TempDir()}
	u.api.SetCertificates(certs)

	fit := seedWorker(t, u.store, "Fit Closed", "1990-01-01", u.companyID)
	addRecord(t, u.store, fit, "E-1", "Fit", "2099-01-01", "Close", 1)
	open := seedWorker(t, u.store, "Fit Still Open", "1990-01-02", u.companyID)
	addRecord(t, u.store, open, "E-2", "Fit", "2099-01-01", "Open", 1)
	unfit := seedWorker(t, u.store, "Unfit One", "1990-01-03", u.companyID)
	addRecord(t, u.store, unfit, "E-3", "Unfit", "", "Open", 0)
	res, err := u.store.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Other')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	stranger := seedWorker(t, u.store, "Stranger", "1990-01-04", other)
	addRecord(t, u.store, stranger, "E-4", "Fit", "2099-01-01", "Close", 1)
	sub := company(t, u.store, "PT Sub", u.companyID)
	subWorker := seedWorker(t, u.store, "Sub Worker", "1990-01-05", sub)
	addRecord(t, u.store, subWorker, "E-5", "Fit", "2099-01-01", "Close", 1)

	body := u.get("/mcu-submit/employees", u.cookie).Body.String()
	link := func(id int64) string { return fmt.Sprintf("/mcu-submit/employees/%d/certificate", id) }
	for id, want := range map[int64]bool{fit: true, open: false, unfit: true} {
		if got := strings.Contains(body, link(id)+`?view=1`); got != want {
			t.Errorf("worker %d link shown = %v, want %v", id, got, want)
		}
	}

	rec := u.get(link(fit), u.cookie)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download = %d, %v", rec.Code, rec.Header())
	}
	if v := u.get(link(unfit)+"?view=1", u.cookie); v.Code != http.StatusOK || !strings.Contains(v.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("view = %d, %v", v.Code, v.Header())
	}
	if got := u.get(link(subWorker), u.cookie).Code; got != http.StatusOK {
		t.Errorf("a subcontractor's worker = %d, want 200", got)
	}
	for name, id := range map[string]int64{"another company": stranger, "no certificate yet": open, "unknown worker": 999999} {
		if got := u.get(link(id), u.cookie).Code; got != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", name, got)
		}
	}
	if got := u.get(link(fit)).Code; got == http.StatusOK { // no cookie: signed out
		t.Errorf("signed out = 200")
	}
}

// The More button shows the Recommendation of an incomplete/follow-up result and the notes of an unfit
// one, as written; the notes of a Fit result stay hidden, and a worker with nothing to show has no button.
func TestEmployeeCheckupsMoreShowsTheReviewersWordsByResult(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	for _, w := range []struct{ name, result, notes, rec string }{
		{"Follow Up Worker", "NeedFollowUp", "private notes A", "See a cardiologist"},
		{"Unfit Worker", "Unfit", "Avoid heights <b>", "private rec B"},
		{"Fit Worker", "Fit", "private notes C", "private rec C"},
		{"Unfit Silent", "TemporaryUnfit", "", ""},
	} {
		id := seedWorker(t, e.store, w.name, "1990-01-01", e.companyID)
		addRecord(t, e.store, id, "N-"+w.name, w.result, "2099-01-01", "Open", 1)
		if _, err := e.store.DB.Exec(`UPDATE McuRecord SET ResultNotes = ?, Recommendation = ? WHERE CheckUpID = ?`, w.notes, w.rec, "N-"+w.name); err != nil {
			t.Fatal(err)
		}
	}
	body := e.get("/mcu-submit/employees", cookie).Body.String()
	for _, want := range []string{"See a cardiologist", "Recommendation", "Avoid heights &lt;b&gt;", "Notes"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, hidden := range []string{"private notes A", "private rec B", "private notes C", "private rec C", "heights <b>"} {
		if strings.Contains(body, hidden) {
			t.Errorf("page shows %q", hidden)
		}
	}
	// Every worker has a More button now (it also holds position and nationality), but only the
	// follow-up and the unfit worker with words carry a note in it.
	if n := strings.Count(body, `class="mp-more-btn"`); n != 4 {
		t.Errorf("%d More buttons, want 4 (one per worker)", n)
	}
	if n := len(regexp.MustCompile(`data-note="[^"]+"`).FindAllString(body, -1)); n != 2 {
		t.Errorf("%d rows carry a note, want 2 (follow-up and unfit with words)", n)
	}
}

func TestEmployeesExportIsFilteredScopedAndAnXlsx(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	mine := seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	addRecord(t, e.store, mine, "MedC-9", "Fit", "2099-01-01", "Open", 1)
	seedWorker(t, e.store, "Nobody Yet", "1991-01-01", e.companyID)

	resp := e.get("/mcu-submit/employees/export.xlsx?status=Valid", cookie)
	if resp.Code != 200 || !strings.Contains(resp.Header().Get("Content-Type"), "spreadsheetml") {
		t.Fatalf("export gave %d %q", resp.Code, resp.Header().Get("Content-Type"))
	}
	f, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a workbook: %v", err)
	}
	rows, _ := f.GetRows("Employees")
	if len(rows) != 2 || rows[1][0] != "Budi Santoso" || rows[1][7] != "Valid" {
		t.Errorf("rows = %v, want header + Budi only (status filter applies)", rows)
	}
	if code := e.get("/mcu-submit/employees/export.xlsx?status=Bogus", cookie).Code; code != 400 {
		t.Errorf("unknown status gave %d, want 400", code)
	}
	if code := e.get("/mcu-submit/employees/export.xlsx").Code; code == 200 {
		t.Errorf("export open without sign-in")
	}
	// The page's export panel repeats the filter in hidden fields, which "Displayed records" uses.
	if !strings.Contains(e.get("/mcu-submit/employees?status=Valid", cookie).Body.String(), `<input type="hidden" name="status" value="Valid" />`) {
		t.Errorf("page export panel does not carry the filter")
	}
}

// The admin has no company, so Employee checkups lists the workers of every company (owner,
// 2026-11-14) with the Company column on, and the company accounts keep seeing only their own tree.
func TestAllEmployeesListsEveryCompany(t *testing.T) {
	s, _, co := newTestStore(t)
	other := company(t, s, "PT Other", 0)
	seedWorker(t, s, "Mine", "1990-01-01", co)
	seedWorker(t, s, "Theirs", "1991-01-01", other)

	got, err := s.AllEmployees(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, e := range got {
		names[e.Name] = e.Company
	}
	if len(names) != 2 || names["Theirs"] != "PT Other" || names["Mine"] == "" {
		t.Errorf("all employees = %v, want both companies with their names", names)
	}
}

func TestAdminSeesEveryCompanyOnEmployeeCheckupsPageAndExport(t *testing.T) {
	e := newEnv(t)
	other := company(t, e.store, "PT Other", 0)
	seedWorker(t, e.store, "Mine Worker", "1990-01-01", e.companyID)
	seedWorker(t, e.store, "Other Worker", "1991-01-01", other)
	_, admin := e.signIn("admin", "pelican-orbit-77")

	page := e.get("/mcu-submit/employees", admin)
	body := page.Body.String()
	if page.Code != 200 {
		t.Fatalf("admin page = %d, want 200", page.Code)
	}
	for _, want := range []string{"Mine Worker", "Other Worker", "PT Other", `>Company</a></th>`, "2 employees", "every company"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page lacks %q", want)
		}
	}
	// The link is in the admin's sidebar on another admin page too, not only on this one.
	if side := e.get("/mcu-submit/admin", admin).Body.String(); !strings.Contains(side, `href="/mcu-submit/employees"`) {
		t.Errorf("admin sidebar has no Employee checkups link")
	}

	resp := e.get("/mcu-submit/employees/export.xlsx", admin)
	if resp.Code != 200 {
		t.Fatalf("admin export = %d, want 200", resp.Code)
	}
	f, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a workbook: %v", err)
	}
	if rows, _ := f.GetRows("Employees"); len(rows) != 3 {
		t.Errorf("rows = %v, want header + both workers", rows)
	}
}

// A company account must still not see another company's workers, now that an admin can.
func TestCompanyAccountStillSeesOnlyItsOwnEmployees(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	other := company(t, e.store, "PT Other", 0)
	seedWorker(t, e.store, "Mine Worker", "1990-01-01", e.companyID)
	seedWorker(t, e.store, "Other Worker", "1991-01-01", other)

	body := e.get("/mcu-submit/employees", cookie).Body.String()
	if !strings.Contains(body, "Mine Worker") || strings.Contains(body, "Other Worker") || strings.Contains(body, `>Company</a></th>`) {
		t.Errorf("company account list wrong: must hold only its own workers and no Company column")
	}
}

// The admin may open any company's certificate; a company account may not (see the scoped test above).
func TestEmployeeCertificateRecordAdminBypassesScope(t *testing.T) {
	s, _, co := newTestStore(t)
	other := company(t, s, "PT Other", 0)
	w := seedWorker(t, s, "Stranger", "1990-01-01", other)
	addRecord(t, s, w, "A-1", "Fit", "2099-01-01", "Close", 1)
	ctx, mine := context.Background(), Scope{CompanyIDs: []int64{co}}

	if _, err := s.EmployeeCertificateRecord(ctx, mine, w, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("company account got %v, want ErrNotFound for another company's worker", err)
	}
	if id, err := s.EmployeeCertificateRecord(ctx, Scope{}, w, true); err != nil || id == 0 {
		t.Errorf("admin got id=%d err=%v, want the record", id, err)
	}
}

// ---- order, tiles, nationality and the credit bar (owner, 2026-11-14) ----

func TestSortEmployeesDefaultOrderIsLatestCheckupAndBlanksGoLast(t *testing.T) {
	list := []Employee{
		{PatientID: 1, Name: "Old", CheckUpDate: "2025-01-01"},
		{PatientID: 2, Name: "None"}, // no checkup at all
		{PatientID: 3, Name: "New", CheckUpDate: "2026-06-01"},
	}
	names := func(l []Employee) string {
		var out []string
		for _, e := range l {
			out = append(out, e.Name)
		}
		return strings.Join(out, ",")
	}
	key, dir, explicit := parseSort(nil)
	if key != sortCheckup || dir != dirDesc || explicit {
		t.Fatalf("default = %s %s explicit=%v, want checkup desc, not explicit", key, dir, explicit)
	}
	if got := names(sortEmployees(list, key, dir)); got != "New,Old,None" {
		t.Errorf("latest first = %s, want New,Old,None", got)
	}
	// Ascending flips the dated rows but the row with no date still goes last.
	if got := names(sortEmployees(list, sortCheckup, dirAsc)); got != "Old,New,None" {
		t.Errorf("oldest first = %s, want Old,New,None", got)
	}
	if list[0].Name != "Old" {
		t.Errorf("sortEmployees changed the caller's list")
	}
}

func TestParseSortIgnoresUnknownWords(t *testing.T) {
	for _, q := range []string{"sort=password", "sort=name;drop", "dir=asc", "sort=&dir=up"} {
		vals, _ := url.ParseQuery(q)
		if key, dir, explicit := parseSort(vals); key != defaultSortKey || dir != defaultSortDir || explicit {
			t.Errorf("%q gave %s %s explicit=%v, want the default", q, key, dir, explicit)
		}
	}
	vals, _ := url.ParseQuery("sort=name&dir=sideways")
	if key, dir, _ := parseSort(vals); key != sortName || dir != dirAsc {
		t.Errorf("a bad dir should fall back to the column's first direction, got %s %s", key, dir)
	}
}

func setCheckupDate(t *testing.T, s *Store, checkUpID, date string) {
	t.Helper()
	if _, err := s.DB.Exec(`UPDATE McuRecord SET DateCheckUp = ? WHERE CheckUpID = ?`, date, checkUpID); err != nil {
		t.Fatal(err)
	}
}

// The headings are links that sort; the column in use says which way (aria-sort) and links to the opposite.
func TestEmployeeCheckupsHeadingsSortAndShowTheArrow(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	for _, w := range []struct{ name, id, date string }{
		{"Alpha Old", "S-1", "2025-01-01"}, {"Charlie New", "S-2", "2026-06-01"}, {"Bravo Mid", "S-3", "2025-09-01"},
	} {
		id := seedWorker(t, e.store, w.name, "1990-01-01", e.companyID)
		addRecord(t, e.store, id, w.id, "Fit", "2099-01-01", "Close", 1)
		setCheckupDate(t, e.store, w.id, w.date)
	}
	order := func(body string) []string {
		var got []string
		for _, m := range regexp.MustCompile(`class="c-name"[^>]*>([^<]+)<`).FindAllStringSubmatch(body, -1) {
			got = append(got, m[1])
		}
		return got
	}

	def := e.get("/mcu-submit/employees", cookie).Body.String()
	if got := strings.Join(order(def), ","); got != "Charlie New,Bravo Mid,Alpha Old" {
		t.Errorf("default order = %s, want latest checkup first", got)
	}
	if !strings.Contains(def, `<th scope="col" aria-sort="descending"><a class="mp-sort" href="/mcu-submit/employees?dir=asc&amp;sort=checkup">Last checkup`) {
		t.Errorf("Last checkup heading should show descending and link to ascending")
	}

	byName := e.get("/mcu-submit/employees?sort=name&dir=asc", cookie).Body.String()
	if got := strings.Join(order(byName), ","); got != "Alpha Old,Bravo Mid,Charlie New" {
		t.Errorf("name A-Z = %s", got)
	}
	if !strings.Contains(byName, `aria-sort="ascending"><a class="mp-sort" href="/mcu-submit/employees?dir=desc&amp;sort=name">Employee`) {
		t.Errorf("Employee heading should show ascending and link to descending")
	}
	if strings.Count(byName, "aria-sort=") != 1 {
		t.Errorf("only the column in use may carry aria-sort")
	}
	if got := strings.Join(order(e.get("/mcu-submit/employees?sort=name&dir=desc", cookie).Body.String()), ","); got != "Charlie New,Bravo Mid,Alpha Old" {
		t.Errorf("name Z-A = %s", got)
	}
	if got := strings.Join(order(e.get("/mcu-submit/employees?sort=bogus", cookie).Body.String()), ","); got != "Charlie New,Bravo Mid,Alpha Old" {
		t.Errorf("an unknown sort must fall back to the default, got %s", got)
	}
}

func TestEmployeeCheckupsTilesToggleTheirFilter(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	id := seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	addRecord(t, e.store, id, "T-1", "Fit", "2099-01-01", "Close", 1)

	off := e.get("/mcu-submit/employees", cookie).Body.String()
	if !strings.Contains(off, `class="mp-tile tone-valid" href="/mcu-submit/employees?status=Valid"`) {
		t.Errorf("an unused tile should link to the filter that turns it on")
	}
	on := e.get("/mcu-submit/employees?status=Valid", cookie).Body.String()
	if !strings.Contains(on, `class="mp-tile tone-valid on" href="/mcu-submit/employees"`) || !strings.Contains(on, `aria-current="true"`) {
		t.Errorf("the tile in use should link back to the unfiltered list")
	}
}

func TestEmployeeCheckupsShowNationalityInMoreAndExport(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	res, err := e.store.DB.Exec(`INSERT INTO NationalityTable (Name) VALUES ('Filipino')`)
	if err != nil {
		t.Fatal(err)
	}
	nat, _ := res.LastInsertId()
	id := seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	if _, err := e.store.DB.Exec(`UPDATE Patient SET FK_NationalityID = ? WHERE PatientID = ?`, nat, id); err != nil {
		t.Fatal(err)
	}
	if body := e.get("/mcu-submit/employees", cookie).Body.String(); !strings.Contains(body, `data-nationality="Filipino"`) || !strings.Contains(body, `data-position="Welder"`) {
		t.Errorf("More should carry the nationality and the position")
	}
	f, err := excelize.OpenReader(bytes.NewReader(e.get("/mcu-submit/employees/export.xlsx", cookie).Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := f.GetRows("Employees"); len(rows) != 2 || rows[0][3] != "Nationality" || rows[1][3] != "Filipino" {
		t.Errorf("export rows = %v, want a Nationality column holding Filipino", rows)
	}
}

// The credit bar is the staff pages' one-line "Developed by", on every portal page, and the list no
// longer pages: there is no Previous/Next, every worker is in the page.
func TestPortalShowsDeveloperCreditAndListHasNoPager(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	for i := 0; i < docPageSize+5; i++ {
		seedWorker(t, e.store, fmt.Sprintf("Worker %02d", i), "1990-01-01", e.companyID)
	}
	body := e.get("/mcu-submit/employees", cookie).Body.String()
	if !strings.Contains(body, `class="app-dev-credit"`) || !strings.Contains(body, "Developed by dr. Arif Rahman Hakim.") {
		t.Errorf("the developer credit is missing")
	}
	if n := strings.Count(body, `class="c-name"`); n != docPageSize+5 {
		t.Errorf("%d rows, want all %d in one scrollable list", n, docPageSize+5)
	}
	if strings.Contains(body, `rel="next"`) || strings.Contains(body, `rel="prev"`) {
		t.Errorf("the employee list must not page")
	}
	if home := e.get("/mcu-submit/home", cookie).Body.String(); !strings.Contains(home, "app-dev-credit") {
		t.Errorf("the credit should be on every portal page, not only this one")
	}
}

func TestEmployeeCheckupsDialogAndPopupWordsAreTranslated(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	for _, lang := range []struct{ code, want string }{{"id", "Unduh"}, {"zh", "下载"}} {
		body := e.get("/mcu-submit/employees", cookie, &http.Cookie{Name: langCookie, Value: lang.code}).Body.String()
		if !strings.Contains(body, lang.want) || strings.Contains(body, ">Download<") {
			t.Errorf("%s: the popup's Download is not translated", lang.code)
		}
	}
}

// ---- days expired, nationality filter, Reset, export choices, expanding More (owner, 2026-11-14) ----

func TestDaysExpiredIsHowLongAgoTheValidityRanOut(t *testing.T) {
	if got := (Employee{DaysLeft: -5}).DaysExpired(); got != 5 {
		t.Errorf("DaysExpired = %d, want 5", got)
	}
	if got := (Employee{DaysLeft: 12}).DaysExpired(); got != 0 {
		t.Errorf("a valid worker has DaysExpired = %d, want 0", got)
	}
}

func TestExpiredRowSaysForHowManyDays(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	id := seedWorker(t, e.store, "Late Renewal", "1990-01-01", e.companyID)
	addRecord(t, e.store, id, "X-1", "Fit", e.store.clinicToday().AddDate(0, 0, -5).Format("2006-01-02"), "Close", 1)
	body := e.get("/mcu-submit/employees", cookie).Body.String()
	// Five days ago by the store's own clock (the one the page uses), so the count is exact.
	if !regexp.MustCompile(`<strong>Expired</strong> <span class="mp-hint">for 5 days</span>`).MatchString(body) {
		i := strings.Index(body, "Late Renewal")
		t.Errorf("an expired row should read \"Expired for N days\"; the row is: %.700s", body[i:])
	}
	if strings.Contains(body, `<span class="mp-hint">expired</span>`) {
		t.Errorf("the old bare \"expired\" hint is still there")
	}
	if id := e.get("/mcu-submit/employees", cookie, &http.Cookie{Name: langCookie, Value: "id"}).Body.String(); !strings.Contains(id, "selama") {
		t.Errorf("\"for N days\" is not translated")
	}
}

func seedNational(t *testing.T, e *env, name, nationality string) {
	t.Helper()
	var nat int64
	if err := e.store.DB.QueryRow(`SELECT NationalityID FROM NationalityTable WHERE Name = ?`, nationality).Scan(&nat); err != nil {
		res, err := e.store.DB.Exec(`INSERT INTO NationalityTable (Name) VALUES (?)`, nationality)
		if err != nil {
			t.Fatal(err)
		}
		nat, _ = res.LastInsertId()
	}
	id := seedWorker(t, e.store, name, "1990-01-01", e.companyID)
	if _, err := e.store.DB.Exec(`UPDATE Patient SET FK_NationalityID = ? WHERE PatientID = ?`, nat, id); err != nil {
		t.Fatal(err)
	}
}

func TestEmployeeCheckupsNationalityFilterAndReset(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	seedNational(t, e, "Maria Filipina", "Filipino")
	seedNational(t, e, "Li Chinese", "Chinese")
	seedNational(t, e, "Jose Filipino", "Filipino")

	all := e.get("/mcu-submit/employees", cookie).Body.String()
	for _, want := range []string{`<option value="Filipino"`, `<option value="Chinese"`, "Maria Filipina", "Li Chinese", `>Reset</a>`} {
		if !strings.Contains(all, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Count(all, `<option value="Filipino"`) != 1 {
		t.Errorf("each nationality should be offered once")
	}
	one := e.get("/mcu-submit/employees?nationality=Filipino", cookie).Body.String()
	if !strings.Contains(one, "Maria Filipina") || !strings.Contains(one, "Jose Filipino") || strings.Contains(one, "Li Chinese") {
		t.Errorf("nationality filter kept the wrong workers")
	}
	if !strings.Contains(one, `<option value="Filipino" selected>`) || !strings.Contains(one, ">2 employees<") {
		t.Errorf("the chosen nationality should stay selected and the count follow it")
	}
	// Reset goes to the bare page address, which drops every filter, the order and the subcontractor tick.
	if !strings.Contains(one, `<a class="btn-ghost" href="/mcu-submit/employees">Reset</a>`) {
		t.Errorf("Reset should link to the plain page")
	}
	// The sort and tile links keep the nationality.
	if !strings.Contains(one, "nationality=Filipino") {
		t.Errorf("links should carry the nationality filter")
	}
}

func exportRows(t *testing.T, e *env, cookie *http.Cookie, query string) ([][]string, int) {
	t.Helper()
	resp := e.get("/mcu-submit/employees/export.xlsx"+query, cookie)
	if resp.Code != 200 {
		return nil, resp.Code
	}
	f, err := excelize.OpenReader(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a workbook: %v", err)
	}
	rows, _ := f.GetRows("Employees")
	return rows, 200
}

func TestEmployeesExportChoicesAllDisplayedAndLatest(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	for _, w := range []struct{ name, id, result, until, date string }{
		{"A Old", "L-1", "Fit", "2099-01-01", "2025-01-01"},
		{"B Newest", "L-2", "Fit", "2099-01-01", "2026-06-01"},
		{"C Mid", "L-3", "Fit", "2099-01-01", "2025-09-01"},
		{"D Unfit", "L-4", "Unfit", "", "2026-01-01"},
	} {
		id := seedWorker(t, e.store, w.name, "1990-01-01", e.companyID)
		addRecord(t, e.store, id, w.id, w.result, w.until, "Close", 1)
		setCheckupDate(t, e.store, w.id, w.date)
	}
	seedWorker(t, e.store, "E Never Checked", "1990-01-01", e.companyID)
	names := func(rows [][]string) string {
		var out []string
		for _, r := range rows[1:] {
			out = append(out, r[0])
		}
		return strings.Join(out, ",")
	}

	for _, c := range []struct{ query, want string }{
		{"", "B Newest,D Unfit,C Mid,A Old,E Never Checked"},                                   // no scope: the displayed list, default order
		{"?status=Valid", "B Newest,C Mid,A Old"},                                              // an old link still works
		{"?scope=displayed&status=Valid&sort=name&dir=desc", "C Mid,B Newest,A Old"},           // the page's filters and order
		{"?scope=all&status=Valid&worker=zzz", "B Newest,D Unfit,C Mid,A Old,E Never Checked"}, // all ignores the filters
		{"?scope=latest&n=2", "B Newest,D Unfit"},                                              // the two newest checkups
		{"?scope=latest&n=10&status=Valid", "B Newest,D Unfit,C Mid,A Old"},                    // filters ignored; nobody without a checkup
	} {
		rows, code := exportRows(t, e, cookie, c.query)
		if code != 200 {
			t.Errorf("%s gave %d", c.query, code)
			continue
		}
		if got := names(rows); got != c.want {
			t.Errorf("%s = %s, want %s", c.query, got, c.want)
		}
	}
	for _, bad := range []string{"?scope=latest&n=0", "?scope=latest&n=abc", "?scope=latest&n=", "?scope=latest&n=99999", "?scope=bogus"} {
		if _, code := exportRows(t, e, cookie, bad); code != http.StatusBadRequest {
			t.Errorf("%s gave %d, want 400", bad, code)
		}
	}
}

// The export panel offers the three choices with the two totals, repeats the filters now on screen, and
// More is a toggle whose detail list lives in one shared template.
func TestEmployeeCheckupsExportPanelAndExpandingMore(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	for _, n := range []string{"One", "Two", "Three"} {
		id := seedWorker(t, e.store, "Worker "+n, "1990-01-01", e.companyID)
		addRecord(t, e.store, id, "P-"+n, "Fit", "2099-01-01", "Close", 1)
	}
	body := e.get("/mcu-submit/employees?status=Valid&worker=worker&sort=name&dir=asc", cookie).Body.String()
	for _, want := range []string{
		`action="/mcu-submit/employees/export.xlsx"`,
		`name="scope" value="displayed" checked`, `name="scope" value="all"`, `name="scope" value="latest"`,
		`name="n" value="100"`,
		`<input type="hidden" name="status" value="Valid" />`, `<input type="hidden" name="sort" value="name" />`, `<input type="hidden" name="worker" value="worker" />`,
		`<span>All records</span> <span class="mp-hint">3</span>`,
		`aria-expanded="false"`, `<template id="mp-more-template">`, `mcu-portal-employees.js?v=2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "mp-dlg-more") {
		t.Errorf("More must no longer live in the popup")
	}
}

// ---- Needs action: Expired + Expiring soon, soonest expiry first (owner, 2026-11-14) ----

func TestNeedsActionFilterKeepsOnlyLapsedAndLapsingWorkers(t *testing.T) {
	for _, c := range []struct {
		filter, status string
		want           bool
	}{
		{"", ValValid, true}, {needsAction, ValExpired, true}, {needsAction, ValExpiring, true},
		{needsAction, ValValid, false}, {needsAction, ValUnfit, false}, {needsAction, ValNone, false},
		{ValValid, ValValid, true}, {ValValid, ValExpired, false},
	} {
		if got := statusMatches(c.filter, c.status); got != c.want {
			t.Errorf("statusMatches(%q, %q) = %v, want %v", c.filter, c.status, got, c.want)
		}
	}
	for _, ok := range []string{"", needsAction, ValValid, ValNone} {
		if !validStatusFilter(ok) {
			t.Errorf("%q should be a valid filter", ok)
		}
	}
	if validStatusFilter("Bogus") || validStatusFilter("needs action") {
		t.Errorf("only the exact words are accepted")
	}
}

func TestNeedsActionTileComesFirstSetsTheOrderAndTurnsOff(t *testing.T) {
	counts := map[string]int{ValExpired: 2, ValExpiring: 3, ValValid: 5}
	tiles := statusTiles(counts, map[string]string{"worker": "x"}, "")
	if len(tiles) == 0 || tiles[0].Label != needsAction || tiles[0].Count != 5 || tiles[0].Slug != "needs" || tiles[0].On {
		t.Fatalf("first tile = %+v, want Needs action with 5 (2 expired + 3 expiring), off", tiles)
	}
	if want := basePath + "/employees?dir=asc&sort=until&status=Needs+action&worker=x"; tiles[0].URL != want {
		t.Errorf("turn-on link = %s, want %s", tiles[0].URL, want)
	}
	// In use: the link clears the filter AND the order it set, and keeps the other filters.
	on := statusTiles(counts, map[string]string{"worker": "x", "status": needsAction, "sort": sortUntil, "dir": dirAsc}, needsAction)
	if !on[0].On || on[0].URL != basePath+"/employees?worker=x" {
		t.Errorf("turn-off = %+v, want On and a link without status, sort or dir", on[0])
	}
	// Nobody to act on: no tile, so it never offers an empty list.
	for _, tile := range statusTiles(map[string]int{ValValid: 4}, nil, "") {
		if tile.Label == needsAction {
			t.Errorf("Needs action offered with nobody to act on")
		}
	}
}

func TestNeedsActionPageAndExportListTheLongestOverdueFirst(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	today := e.store.clinicToday()
	for _, w := range []struct {
		name string
		days int // valid-until relative to the store's today
	}{
		{"Recently Expired", -3}, {"Long Expired", -30}, {"About To Expire", 10}, {"Fine For Long", 200},
	} {
		id := seedWorker(t, e.store, w.name, "1990-01-01", e.companyID)
		addRecord(t, e.store, id, "N-"+w.name, "Fit", today.AddDate(0, 0, w.days).Format("2006-01-02"), "Close", 1)
	}
	order := func(body string) string {
		var got []string
		for _, m := range regexp.MustCompile(`class="c-name"[^>]*>([^<]+)<`).FindAllStringSubmatch(body, -1) {
			got = append(got, m[1])
		}
		return strings.Join(got, ",")
	}

	// The tile's own link is what the person clicks: it carries the filter and the order together.
	home := e.get("/mcu-submit/employees", cookie).Body.String()
	if !strings.Contains(home, `class="mp-tile tone-needs" href="/mcu-submit/employees?dir=asc&amp;sort=until&amp;status=Needs&#43;action"`) {
		t.Errorf("the page should open with a Needs action tile that sets the filter and the order")
	}
	if !strings.Contains(home, `<option value="Needs action">Needs action</option>`) {
		t.Errorf("the validity box should offer Needs action")
	}

	body := e.get("/mcu-submit/employees?status=Needs+action&sort=until&dir=asc", cookie).Body.String()
	if got := order(body); got != "Long Expired,Recently Expired,About To Expire" {
		t.Errorf("Needs action = %s, want the longest overdue first and nobody who is fine", got)
	}
	for _, want := range []string{`mp-tile tone-needs on`, `<option value="Needs action" selected>`, ">3 employees<", `<input type="hidden" name="status" value="Needs action" />`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The chosen order shows on the heading too, and the tile in use links back to the plain page.
	if !strings.Contains(body, `<th scope="col" aria-sort="ascending"><a class="mp-sort" href="/mcu-submit/employees?dir=desc&amp;sort=until&amp;status=Needs&#43;action">Valid until`) {
		t.Errorf("Valid until should be the column in use, ascending")
	}
	if !strings.Contains(body, `class="mp-tile tone-needs on" href="/mcu-submit/employees"`) {
		t.Errorf("the tile in use should link back to the unfiltered, default-order page")
	}

	rows, code := exportRows(t, e, cookie, "?scope=displayed&status=Needs+action&sort=until&dir=asc")
	if code != 200 || len(rows) != 4 || rows[1][0] != "Long Expired" {
		t.Errorf("export = %d %v, want the three workers who need action, longest overdue first", code, rows)
	}
}

// ---- Download certificates: a zip of the latest N (owner, 2026-11-14) ----

func TestEmployeeCertificatesZipHoldsTheLatestNNewestFirst(t *testing.T) {
	u := newUploadEnv(t)
	certs := &fakeCerts{dir: t.TempDir()}
	u.api.SetCertificates(certs)
	record := func(checkUpID string) int64 {
		var id int64
		if err := u.store.DB.QueryRow(`SELECT McuRecordID FROM McuRecord WHERE CheckUpID = ?`, checkUpID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, w := range []struct{ name, id, status, date string }{
		{"Old Closed", "Z-1", "Close", "2025-01-01"},
		{"Newest Closed", "Z-2", "Close", "2026-06-01"},
		{"Mid Closed", "Z-3", "Close", "2025-09-01"},
		{"Newest But Open", "Z-4", "Open", "2027-01-01"}, // Fit still Open: no certificate yet, so never in the zip
	} {
		id := seedWorker(t, u.store, w.name, "1990-01-01", u.companyID)
		addRecord(t, u.store, id, w.id, "Fit", "2099-01-01", w.status, 1)
		setCheckupDate(t, u.store, w.id, w.date)
	}
	res, err := u.store.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Other')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	stranger := seedWorker(t, u.store, "Stranger", "1990-01-01", other)
	addRecord(t, u.store, stranger, "Z-5", "Fit", "2099-01-01", "Close", 1)
	setCheckupDate(t, u.store, "Z-5", "2027-06-01")

	rec := u.get("/mcu-submit/employees/certificates.zip?n=2", u.cookie)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || !strings.Contains(rec.Header().Get("Content-Disposition"), "latest-certificates.zip") {
		t.Fatalf("zip = %d %v", rec.Code, rec.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("%d files, want 2", len(zr.File))
	}
	// Asked for in newest-first order, only the closed own-company checkups: Newest Closed, then Mid Closed.
	if want := []int64{record("Z-2"), record("Z-3")}; len(certs.asked) != 2 || certs.asked[0] != want[0] || certs.asked[1] != want[1] {
		t.Errorf("certificates asked for = %v, want %v", certs.asked, want)
	}
	for i, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.HasPrefix(string(data), "%PDF") {
			t.Errorf("file %d (%s) is not a PDF", i, f.Name)
		}
	}
	// Each certificate handed over is one audit row with the record id and no name.
	var n int
	_ = u.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmissionEvent WHERE Action = 'CertificateDownloaded' AND Detail LIKE 'zip record %'`).Scan(&n)
	if n != 2 {
		t.Errorf("%d audit rows, want 2", n)
	}
	// More than there are: everything that qualifies and nothing else (3 own closed, not the open one, not the stranger).
	certs.asked = nil
	if rec := u.get("/mcu-submit/employees/certificates.zip?n=50", u.cookie); rec.Code != http.StatusOK || len(certs.asked) != 3 {
		t.Errorf("n=50 gave %d and asked for %d certificates, want 200 and 3", rec.Code, len(certs.asked))
	}
	for _, bad := range []string{"?n=0", "?n=abc", "?n=", "?n=101", ""} {
		if got := u.get("/mcu-submit/employees/certificates.zip"+bad, u.cookie).Code; got != http.StatusBadRequest {
			t.Errorf("%q gave %d, want 400", bad, got)
		}
	}
	if got := u.get("/mcu-submit/employees/certificates.zip?n=2").Code; got == http.StatusOK {
		t.Errorf("zip open without sign-in")
	}
	// The page offers the control, with a safe default, because certificates are wired in.
	page := u.get("/mcu-submit/employees", u.cookie).Body.String()
	for _, want := range []string{`action="/mcu-submit/employees/certificates.zip"`, `name="n" value="20" min="1" max="100"`, "Download certificates"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestEmployeeCertificatesZipNeedsTheClinicalSideAndAnyCertificate(t *testing.T) {
	e := newEnv(t) // no SetCertificates: a build without the clinical side
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	seedWorker(t, e.store, "Budi Santoso", "1990-01-01", e.companyID)
	if strings.Contains(e.get("/mcu-submit/employees", cookie).Body.String(), "certificates.zip") {
		t.Errorf("no certificate source, so no Download certificates control")
	}
	if got := e.get("/mcu-submit/employees/certificates.zip?n=5", cookie).Code; got != http.StatusNotFound {
		t.Errorf("without a certificate source the route gave %d, want 404", got)
	}
}
