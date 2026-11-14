package mcusubmit

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
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
	if got := filterEmployees(all, "santoso", ""); len(got) != 2 {
		t.Errorf("name filter kept %d, want 2", len(got))
	}
	if got := filterEmployees(all, "", ValValid); len(got) != 2 {
		t.Errorf("status filter kept %d, want 2", len(got))
	}
	if got := filterEmployees(all, "adi santoso", ValExpired); len(got) != 1 {
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
	for _, want := range []string{"Budi Santoso", "2099-01-01", ">Valid<", "2 employees, page 1 of 1", `href="/mcu-submit/employees"`, "No checkup on record"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	one := e.get("/mcu-submit/employees?status=Valid", cookie).Body.String()
	if !strings.Contains(one, "1 employee, page 1 of 1") || strings.Contains(one, "Nobody Yet") {
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
	if n := strings.Count(body, `<summary class="btn-ghost">More</summary>`); n != 2 {
		t.Errorf("%d More buttons, want 2 (follow-up and unfit with words)", n)
	}
}
