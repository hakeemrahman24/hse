package mcusubmit

import (
	"context"
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

	got, err := s.CompanyEmployees(ctx, co, 30)
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
