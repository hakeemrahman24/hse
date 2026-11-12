package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"entry-permit/internal/company"
	"entry-permit/internal/reflist"
	"entry-permit/internal/staff"
	"entry-permit/internal/waiver"
)

// seedCompanies puts three companies in the mirror: a live contractor, a
// government office and an archived one that must never be offered.
func (e *testEnv) seedCompanies(t *testing.T) {
	t.Helper()
	e.companies.list = []company.Company{
		{ID: 1, Name: "PT Tamu", Entity: "Contractor"},
		{ID: 2, Name: "Dinas Kerja", Entity: "Government", Gov: true},
		{ID: 3, Name: "PT Lama", Entity: "Contractor", Archived: true},
	}
	st := &company.Store{DB: e.db, Clock: e.clock}
	if _, err := st.Sync(context.Background(), e.companies, "test"); err != nil {
		t.Fatal(err)
	}
}

// goodWaiver is a visitor-company waiver that passes every rule: 10 to 12 Nov 2026.
func goodWaiver() map[string]string {
	f := map[string]string{
		"cf-turnstile-response": "dev-pass", "applicant_type": "VisitorCompany", "company_id": "1",
		"name": "Tamu Satu", "gender": "Male", "dob": "02/04/1990", "nationality": "Indonesia", "nik": "3201 0203 0490 0001",
		"position": "Auditor", "department": "HSE", "visit_location": "Site A", "purpose": "Annual audit",
		"from": "10/11/2026", "to": "12/11/2026",
		"requester_name": "Rina Requester", "requester_email": "rina@example.com", "phone": "081234567890",
		"risk": "Heavy equipment", "precaution": "PPE worn", "prevent": "Escorted at all times", "declaration": "on",
	}
	// Every Risk Exposure and Health Declaration question answered No, as the form pre-selects.
	for _, q := range waiver.RiskQuestions {
		f["risk_"+q.Key] = "No"
	}
	for _, q := range waiver.HealthQuestions {
		f["health_"+q.Key] = "No"
	}
	return f
}

func goodWaiverFiles() map[string][]byte {
	return map[string][]byte{"id_doc": pdfBytes, "id_photo": jpgBytes, "letter_doc": pdfBytes}
}

func (e *testEnv) postMultipart(t *testing.T, path string, fields map[string]string, files map[string][]byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for k, data := range files {
		fw, _ := mw.CreateFormFile(k, k+".bin")
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *testEnv) waiverRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM waiver`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *testEnv) sendWaiver(t *testing.T, edit func(map[string]string)) (int, string) {
	t.Helper()
	f := goodWaiver()
	if edit != nil {
		edit(f)
	}
	return e.postMultipart(t, "/waiver", f, goodWaiverFiles())
}

func TestWaiverPageShowsTheFormAndOnlyLiveCompanies(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	r, body := e.get(t, "/waiver", nil)
	if r.StatusCode != 200 || strings.Contains(body, "coming soon") {
		t.Fatalf("status %d", r.StatusCode)
	}
	for _, want := range []string{`action="/waiver"`, `enctype="multipart/form-data"`, `name="applicant_type"`, `name="company_id"`,
		"PT Tamu", "Dinas Kerja", `name="nik"`, `name="from"`, `name="to"`, `name="risk"`, `name="declaration"`,
		`name="id_doc"`, `name="id_photo"`, `name="letter_doc"`, `name="cf-turnstile-response"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form lacks %q", want)
		}
	}
	if strings.Contains(body, "PT Lama") {
		t.Error("an archived company is offered")
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "challenges.cloudflare.com") {
		t.Error("the Turnstile widget cannot load on /waiver")
	}
}

func TestWaiverSubmitSavesAPendingDoctorWaiverWithItsFiles(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	code, body := e.sendWaiver(t, nil)
	if code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(body))
	}
	id := regexp.MustCompile(`WVR-[A-Z0-9]{5}`).FindString(body)
	if id == "" {
		t.Fatalf("no request id shown: %s", body)
	}
	var status, typ, company, nik, from, to, idDoc, letter string
	var days int
	if err := e.db.QueryRow(`SELECT status, applicant_type, company, nik, visit_from, visit_to, days, id_doc, letter_doc FROM waiver WHERE request_id = ?`, id).
		Scan(&status, &typ, &company, &nik, &from, &to, &days, &idDoc, &letter); err != nil {
		t.Fatal(err)
	}
	if status != "PendingDoctor" || typ != "VisitorCompany" || company != "PT Tamu" || nik != "3201020304900001" ||
		from != "2026-11-10" || to != "2026-11-12" || days != 3 {
		t.Errorf("stored %s %s %s %s %s-%s %d", status, typ, company, nik, from, to, days)
	}
	if !strings.HasPrefix(idDoc, id+"/") || !strings.HasPrefix(letter, id+"/") {
		t.Errorf("file paths %q %q", idDoc, letter)
	}
	if e.uploadFolders(t) != 1 {
		t.Errorf("upload folders = %d", e.uploadFolders(t))
	}
	if strings.Contains(body, "3201") {
		t.Error("the confirmation page echoes the NIK")
	}
}

func TestWaiverSubmitAlsoCreatesTheVisitorsTicket(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	code, body := e.sendWaiver(t, nil)
	if code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(body))
	}
	id := regexp.MustCompile(`WVR-[A-Z0-9]{5}`).FindString(body)
	var tid, status, arrival, departure, pattern string
	var work, rot int
	if err := e.db.QueryRow(`SELECT ticket_id, is_work, status, arrival, departure, pattern, rotation_no FROM ticket WHERE lep_no = ?`, id).
		Scan(&tid, &work, &status, &arrival, &departure, &pattern, &rot); err != nil {
		t.Fatalf("no ticket for %s: %v", id, err)
	}
	// Permits doc 8: the waiver ticket exists from submit, PendingDoctor, rotation 1.
	if !strings.HasPrefix(tid, "TKT-") || work != 0 || status != "PendingDoctor" || arrival != "2026-11-10" || departure != "2026-11-12" || pattern != "Normal" || rot != 1 {
		t.Errorf("ticket %s work %d %s %s-%s %s %d", tid, work, status, arrival, departure, pattern, rot)
	}
	// A refused waiver makes no ticket.
	if code, _ := e.sendWaiver(t, nil); code != 409 || e.ticketRows(t) != 1 {
		t.Errorf("second (blocked) waiver: %d, tickets %d", code, e.ticketRows(t))
	}
}

func TestPermitSubmitStoresTheBirthDateForTheTicketPage(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	if code, body := e.submit(t, goodFields(token), goodFiles()); code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(body))
	}
	var dob string
	if err := e.db.QueryRow(`SELECT dob FROM permit`).Scan(&dob); err != nil || dob != "1988-03-05" {
		t.Errorf("stored dob %q err %v", dob, err)
	}
}

// The id rule follows the nationality: Indonesia needs the 16-digit NIK, any other country the
// passport number, and each refusal names the right one.
func TestWaiverIdFollowsTheNationality(t *testing.T) {
	cases := map[string]struct {
		nationality, nik, want string
	}{
		"Indonesian with a passport number":   {"Indonesia", "P1234567", "NIK"},
		"Indonesian with a short NIK":         {"Indonesia", "320102030490", "NIK"},
		"foreign visitor with a NIK":          {"Vietnam", "3201020304900001", "passport"},
		"foreign visitor with a short number": {"Vietnam", "AB12", "passport"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.seedCompanies(t)
			code, body := e.sendWaiver(t, func(f map[string]string) {
				f["nationality"], f["nik"] = c.nationality, c.nik
				f["passport_until"], f["visa_until"] = "01/05/2027", "01/02/2027"
			})
			if code != 400 || !strings.Contains(alertText(body), c.want) {
				t.Fatalf("status %d alert %q; want a refusal that mentions %q", code, alertText(body), c.want)
			}
			if e.waiverRows(t) != 0 {
				t.Error("a refused waiver left a row behind")
			}
		})
	}
}

func TestWaiverRefusalsSaveNothing(t *testing.T) {
	cases := map[string]func(map[string]string){
		"no declaration":   func(f map[string]string) { delete(f, "declaration") },
		"bad NIK":          func(f map[string]string) { f["nik"] = "12" },
		"date format":      func(f map[string]string) { f["from"] = "2026-11-10" },
		"too many days":    func(f map[string]string) { f["to"] = "30/11/2026" },
		"unknown company":  func(f map[string]string) { f["company_id"] = "99" },
		"archived company": func(f map[string]string) { f["company_id"] = "3" },
		"no company":       func(f map[string]string) { delete(f, "company_id") },
		"no turnstile":     func(f map[string]string) { delete(f, "cf-turnstile-response") },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.seedCompanies(t)
			code, body := e.sendWaiver(t, edit)
			if code != 400 || alertText(body) == "" {
				t.Fatalf("status %d alert %q", code, alertText(body))
			}
			if e.waiverRows(t) != 0 || e.uploadFolders(t) != 0 {
				t.Error("a refused waiver left a row or files behind")
			}
		})
	}
}

func TestWaiverRefusesAMissingFileAndKeepsNothing(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	files := goodWaiverFiles()
	delete(files, "letter_doc")
	code, body := e.postMultipart(t, "/waiver", goodWaiver(), files)
	if code != 400 || !strings.Contains(alertText(body), "appointment letter") {
		t.Fatalf("status %d %s", code, alertText(body))
	}
	if e.waiverRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Error("left something behind")
	}
	bad := goodWaiverFiles()
	bad["id_doc"] = []byte("not a document")
	if code, _ := e.postMultipart(t, "/waiver", goodWaiver(), bad); code != 400 || e.waiverRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Errorf("a file that is not PDF/JPG/PNG: status %d rows %d", code, e.waiverRows(t))
	}
}

func TestWaiverForeignVisitorNeedsTheVisa(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	foreign := func(f map[string]string) {
		f["nationality"], f["nik"] = "Vietnam", "P1234567"
		f["passport_until"], f["visa_until"] = "01/05/2027", "01/02/2027"
	}
	if code, body := e.sendWaiver(t, foreign); code != 400 || !strings.Contains(alertText(body), "ITAS") {
		t.Fatalf("no visa file: %d %s", code, alertText(body))
	}
	files := goodWaiverFiles()
	files["visa_doc"] = pdfBytes
	f := goodWaiver()
	foreign(f)
	if code, body := e.postMultipart(t, "/waiver", f, files); code != 200 {
		t.Fatalf("complete foreign visitor: %d %s", code, alertText(body))
	}
	var visa, passport string
	if err := e.db.QueryRow(`SELECT visa_doc, passport_until FROM waiver`).Scan(&visa, &passport); err != nil || visa == "" || passport != "2027-05-01" {
		t.Errorf("stored visa %q passport %q err %v", visa, passport, err)
	}
	// An Indonesian visitor's visa file is ignored and never stored.
	e2 := newEnv(t)
	e2.seedCompanies(t)
	files2 := goodWaiverFiles()
	files2["visa_doc"] = pdfBytes
	if code, _ := e2.postMultipart(t, "/waiver", goodWaiver(), files2); code != 200 {
		t.Fatal("indonesian with a stray visa file was refused")
	}
	var v2 string
	_ = e2.db.QueryRow(`SELECT visa_doc FROM waiver`).Scan(&v2)
	if v2 != "" {
		t.Errorf("a visa file was stored for an Indonesian: %q", v2)
	}
}

func TestWaiverApplicantTypeRules(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	// Government must pick a Government company; a visitor company must not.
	if code, _ := e.sendWaiver(t, func(f map[string]string) { f["applicant_type"] = "Government" }); code != 400 {
		t.Errorf("government with a contractor company: %d", code)
	}
	if code, _ := e.sendWaiver(t, func(f map[string]string) { f["company_id"] = "2" }); code != 400 {
		t.Errorf("visitor company with a government office: %d", code)
	}
	gov := func(f map[string]string) { f["applicant_type"], f["company_id"] = "Government", "2" }
	if code, body := e.sendWaiver(t, gov); code != 200 {
		t.Fatalf("government waiver: %d %s", code, alertText(body))
	}
	// The visitor switch in Settings turns visitor companies off, not Government.
	e.setSetting(t, "waiver_visitor_company_enabled", "off")
	code, body := e.sendWaiver(t, func(f map[string]string) { f["name"], f["nik"] = "Tamu Dua", "3201020304900002" })
	if code != 400 || !strings.Contains(alertText(body), "switched off") {
		t.Errorf("visitor company while off: %d %s", code, alertText(body))
	}
	_, page := e.get(t, "/waiver", nil)
	if strings.Contains(page, `value="VisitorCompany"`) {
		t.Error("the form still offers visitor companies while they are off")
	}
}

func TestWaiverBlocksASecondOpenRequestForTheSamePerson(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	if code, _ := e.sendWaiver(t, nil); code != 200 {
		t.Fatal("first waiver refused")
	}
	code, body := e.sendWaiver(t, nil)
	if code != 409 || !strings.Contains(alertText(body), "WVR-") {
		t.Fatalf("second waiver: %d %s", code, alertText(body))
	}
	if e.waiverRows(t) != 1 || e.uploadFolders(t) != 1 {
		t.Errorf("rows %d folders %d", e.waiverRows(t), e.uploadFolders(t))
	}
}

func TestWaiverYearlyCompanyCapAppliesToVisitorCompaniesOnly(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	week := func(name, nik string) func(map[string]string) {
		return func(f map[string]string) { f["name"], f["nik"], f["to"] = name, nik, "16/11/2026" } // 7 days
	}
	for i, p := range [][2]string{{"Tamu Satu", "3201020304900001"}, {"Tamu Dua", "3201020304900002"}} {
		if code, body := e.sendWaiver(t, week(p[0], p[1])); code != 200 {
			t.Fatalf("waiver %d: %d %s", i+1, code, alertText(body))
		}
	}
	// 14 days used of 14: one more day is over the cap.
	code, body := e.sendWaiver(t, func(f map[string]string) {
		f["name"], f["nik"], f["from"], f["to"] = "Tamu Tiga", "3201020304900003", "20/11/2026", "20/11/2026"
	})
	if code != 400 || !strings.Contains(alertText(body), "yearly") {
		t.Fatalf("over the cap: %d %s", code, alertText(body))
	}
	// Government is exempt.
	code, body = e.sendWaiver(t, func(f map[string]string) {
		f["applicant_type"], f["company_id"] = "Government", "2"
		f["name"], f["nik"], f["from"], f["to"] = "Tamu Empat", "3201020304900004", "20/11/2026", "20/11/2026"
	})
	if code != 200 {
		t.Fatalf("government over the visitor cap: %d %s", code, alertText(body))
	}
}

func TestWaiverChecksTheMailDomain(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	e.mx.err = errors.New("no mx")
	code, body := e.sendWaiver(t, nil)
	if code != 400 || !strings.Contains(alertText(body), "cannot receive mail") || e.waiverRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Fatalf("%d %s rows %d", code, alertText(body), e.waiverRows(t))
	}
	if len(e.mx.domains) != 1 || e.mx.domains[0] != "example.com" {
		t.Errorf("checked %v", e.mx.domains)
	}
	e.setSetting(t, "email_mx_check", "off")
	if code, _ := e.sendWaiver(t, nil); code != 200 {
		t.Errorf("with the check switched off: %d", code)
	}
}

func TestWaiverSubmitIsRateLimited(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	for i := 0; i < submitsPerHour; i++ {
		e.sendWaiver(t, func(f map[string]string) { delete(f, "declaration") })
	}
	if code, _ := e.sendWaiver(t, nil); code != 429 {
		t.Errorf("attempt %d: status %d, want 429", submitsPerHour+1, code)
	}
	if e.waiverRows(t) != 0 {
		t.Error("a limited attempt was saved")
	}
}

func TestWaiverNationalityMustComeFromTheListWhenThereIsOne(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	def, _ := reflist.Find("nationality")
	lists := reflist.NewStore(e.db, e.clock)
	for _, n := range []string{"Indonesia", "Vietnam"} {
		if _, err := lists.Add(context.Background(), "test", def, []string{n}); err != nil {
			t.Fatal(err)
		}
	}
	_, page := e.get(t, "/waiver", nil)
	if !strings.Contains(page, `<option value="Vietnam"`) {
		t.Error("the nationality list is not offered")
	}
	if code, _ := e.sendWaiver(t, func(f map[string]string) { f["nationality"] = "Atlantis" }); code != 400 {
		t.Errorf("a nationality outside the list: %d", code)
	}
	if code, body := e.sendWaiver(t, nil); code != 200 {
		t.Errorf("a listed nationality: %d %s", code, alertText(body))
	}
}

// The visitor form takes the same optional spare email as the work permit form.
func TestWaiverSpareEmailIsCheckedAndStoredWithTheMainAddress(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	if code, body := e.sendWaiver(t, func(f map[string]string) { f["spare_email"] = "bad@nodot" }); code != 400 || !strings.Contains(alertText(body), "spare email") {
		t.Errorf("bad spare: %d %q", code, alertText(body))
	}
	if code, body := e.sendWaiver(t, func(f map[string]string) { f["spare_email"] = " Backup @Example.COM " }); code != 200 {
		t.Fatalf("good spare: %d %q", code, alertText(body))
	}
	var stored string
	if err := e.db.QueryRow(`SELECT requester_email FROM waiver`).Scan(&stored); err != nil || stored != "rina@example.com,backup@example.com" {
		t.Errorf("stored %q, %v", stored, err)
	}
}

// The form shows every question of the old Risk Exposure and Health Declaration cards, with No
// pre-selected, and the answers are stored and shown to the reviewer (Yes answers and the flag).
func TestWaiverFormShowsAllQuestionsAndTheAnswersReachTheReviewer(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	_, body := e.get(t, "/waiver", nil)
	for _, want := range []string{"Type of non-conformance", "Bypassing safety control", "Working at height", "Peptic ulceration",
		"Pregnancy", `name="risk_hotwork" value="No" checked`, `name="health_asthma" value="Yes"`, `name="medicine3"`, `name="medicine_notes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	if code, _ := e.sendWaiver(t, func(f map[string]string) { delete(f, "health_asthma") }); code != 400 {
		t.Errorf("a missing answer: %d, want 400", code)
	}
	if code, body := e.sendWaiver(t, func(f map[string]string) {
		f["risk_confined"], f["health_asthma"] = "Yes", "N/A"
		f["takes_medicine"], f["medicine1"], f["dosage1"], f["medicine2"], f["dosage2"] = "on", "Paracetamol", "500", "Vitamin C", "100"
		f["medicine_notes"] = "After meals"
	}); code != 200 {
		t.Fatalf("send: %d %q", code, alertText(body))
	}
	var risk, health, med2 string
	if err := e.db.QueryRow(`SELECT risk_answers, health_answers, medicine2 FROM waiver`).Scan(&risk, &health, &med2); err != nil ||
		risk != "NYNNNNNNN" || health != "NNNNNNNNNANNNNN" || med2 != "Vitamin C" {
		t.Errorf("stored %q %q %q, %v", risk, health, med2, err)
	}
	// The doctor's page lists the Yes answers, the flag and every medicine.
	c := e.reviewer(t, "doc", staff.Doctor)
	_, page := e.get(t, "/staff/doctor", c)
	for _, want := range []string{"Confined space", "Paracetamol (500); Vitamin C (100)", "After meals", "High risk"} {
		if !strings.Contains(page, want) {
			t.Errorf("doctor page lacks %q", want)
		}
	}
}
