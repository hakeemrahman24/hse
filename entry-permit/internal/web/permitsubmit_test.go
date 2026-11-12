package web

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"entry-permit/internal/worker"
)

var tokenRe = regexp.MustCompile(`name="token" value="([^"]+)"`)

var (
	pdfBytes = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n")
	jpgBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
)

// stepOne runs part 1 and returns the signed token that part 2 must carry.
func (e *testEnv) stepOne(t *testing.T) string {
	t.Helper()
	e.workers.people = []worker.Person{budi}
	code, body := e.permitPost(t, nil)
	if code != 200 {
		t.Fatalf("step 1: %d %s", code, alertText(body))
	}
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("step 1 gave no token: %s", body)
	}
	return m[1]
}

// goodFields is a part-2 form that passes for an Indonesian worker.
func goodFields(token string) map[string]string {
	return map[string]string{
		"token": token, "phone": "081234567890", "emergency_name": "Siti Santoso", "emergency_phone": "081298765432",
		"regency": "11.01", "district": "Tapaktuan", "village": "Pasar", "street": "Jl. Merdeka 1", "declaration": "on",
		"cert_until": "01/05/2027", // a Welder needs a certificate (position list)
	}
}

func goodFiles() map[string][]byte {
	return map[string][]byte{"id_doc": pdfBytes, "id_photo": jpgBytes, "cert_doc": pdfBytes}
}

func (e *testEnv) submit(t *testing.T, fields map[string]string, files map[string][]byte) (int, string) {
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
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/permit/submit", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *testEnv) permitRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM permit`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *testEnv) uploadFolders(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(e.uploads)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestStepOneOffersPartTwo(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	_, step1 := e.permitPost(t, nil)
	for _, want := range []string{`action="/permit/submit"`, `enctype="multipart/form-data"`, `name="phone"`, `name="emergency_name"`,
		`name="regency"`, "Kabupaten Aceh Selatan", `name="street"`, `name="declaration"`, `name="id_doc"`, `name="id_photo"`} {
		if !strings.Contains(step1, want) {
			t.Errorf("part 2 lacks %q", want)
		}
	}
	if strings.Contains(step1, `name="visa_type"`) {
		t.Error("an Indonesian worker was shown the visa fields")
	}
	if token == "" || strings.Contains(step1, "1988") {
		t.Error("token missing or the date of birth is echoed")
	}
}

func foreignEnv(t *testing.T) *testEnv {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	e.workers.employees = map[string]worker.Employee{"Budi Santoso": {PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05",
		Nationality: "Vietnam", Company: "PT Maju", Entity: "Contractor", Position: "Welder", McuResult: "Fit", McuValidUntil: "2027-06-30"}}
	return e
}

func TestStepOneShowsForeignFields(t *testing.T) {
	e := foreignEnv(t)
	_, body := e.permitPost(t, nil)
	for _, want := range []string{`name="visa_type"`, `name="rptka_expiry"`, `name="immigration_until"`, `name="police_until"`,
		`name="passport_until"`, `name="visa_doc"`, `name="rptka_doc"`, `name="immigration_doc"`, `name="police_doc"`} {
		if !strings.Contains(body, want) {
			t.Errorf("foreign part 2 lacks %q", want)
		}
	}
}

func TestSubmitSavesAPendingPermitWithItsFiles(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	code, body := e.submit(t, goodFields(token), goodFiles())
	if code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(body))
	}
	m := regexp.MustCompile(`WEP-[A-Z0-9]{5}`).FindString(body)
	if m == "" {
		t.Fatalf("no request id shown: %s", body)
	}
	var status, company, entity, pos, phone, regency, province, idDoc, until, mcu, requester string
	err := e.db.QueryRow(`SELECT status, company, entity, work_position, phone, regency, province, id_doc, lep_valid_until, mcu_valid_until, requester_email
		FROM permit WHERE request_id = ?`, m).Scan(&status, &company, &entity, &pos, &phone, &regency, &province, &idDoc, &until, &mcu, &requester)
	if err != nil {
		t.Fatal(err)
	}
	if status != "PendingStage1" || company != "PT Maju" || entity != "Contractor" || pos != "Welder" {
		t.Errorf("snapshot wrong: %s %s %s %s", status, company, entity, pos)
	}
	if phone != "6281234567890" || regency != "Kabupaten Aceh Selatan" || province != "Aceh" {
		t.Errorf("address/phone wrong: %s %s %s", phone, regency, province)
	}
	if until != "2027-01-17" || mcu != "2027-01-31" || requester != "rina@example.com" {
		t.Errorf("dates wrong: until %s mcu %s requester %s", until, mcu, requester)
	}
	if idDoc != m+"/id_doc.pdf" {
		t.Errorf("id_doc path = %q", idDoc)
	}
	if _, err := os.Stat(filepath.Join(e.uploads, m, "id_photo.jpg")); err != nil {
		t.Errorf("photo not stored: %v", err)
	}
	if !strings.Contains(body, "2027-01-17") {
		t.Error("the page does not show the valid-until date")
	}
	if !strings.Contains(strings.ToLower(body), "email") {
		t.Error("the page does not say anything about the email")
	}
	// Once at part 1 and once again at submit (the signed step lasts hours).
	if len(e.mx.domains) != 2 || e.mx.domains[0] != "example.com" {
		t.Errorf("mail domain checks = %v", e.mx.domains)
	}
}

func TestSubmitForeignWorkerStoresVisaAndCapsTheDate(t *testing.T) {
	e := foreignEnv(t)
	_, body := e.permitPost(t, nil)
	token := tokenRe.FindStringSubmatch(body)[1]
	f := goodFields(token)
	for k, v := range map[string]string{"visa_type": "ITAS", "visa_code": "ITAS-9", "visa_expiry": "10/01/2027",
		"rptka_issued": "01/01/2026", "rptka_days": "365", "rptka_expiry": "01/01/2027",
		"immigration_until": "01/03/2027", "police_until": "01/03/2027", "passport_until": "01/01/2028"} {
		f[k] = v
	}
	files := goodFiles()
	for _, k := range []string{"visa_doc", "rptka_doc", "immigration_doc", "police_doc"} {
		files[k] = pdfBytes
	}
	code, out := e.submit(t, f, files)
	if code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(out))
	}
	var until, vt, vexp, rexp string
	var days int
	if err := e.db.QueryRow(`SELECT lep_valid_until, visa_type, visa_expiry, rptka_expiry, rptka_days FROM permit`).Scan(&until, &vt, &vexp, &rexp, &days); err != nil {
		t.Fatal(err)
	}
	if until != "2027-01-10" || vt != "ITAS" || vexp != "2027-01-10" || rexp != "2027-01-01" || days != 365 {
		t.Errorf("got until %s type %s visa %s rptka %s days %d", until, vt, vexp, rexp, days)
	}
	if !strings.Contains(out, "Work visa validity") {
		t.Errorf("the binding reason is not shown: %s", out)
	}
}

func TestSubmitRefusesAndSavesNothing(t *testing.T) {
	cases := []struct {
		name   string
		fields func(map[string]string)
		files  func(map[string][]byte)
		want   string
	}{
		{"no declaration", func(f map[string]string) { delete(f, "declaration") }, nil, "declaration"},
		{"same phones", func(f map[string]string) { f["emergency_phone"] = "081234567890" }, nil, "different"},
		{"unknown regency", func(f map[string]string) { f["regency"] = "99.99" }, nil, "address"},
		{"no photo", nil, func(m map[string][]byte) { delete(m, "id_photo") }, "photo"},
		{"text as id", nil, func(m map[string][]byte) { m["id_doc"] = []byte("not a document at all") }, "pdf, jpg or png"},
		{"script as photo", nil, func(m map[string][]byte) { m["id_photo"] = []byte("<script>alert(1)</script>") }, "pdf, jpg or png"},
		{"too large", nil, func(m map[string][]byte) {
			m["id_doc"] = append(append([]byte{}, pdfBytes...), make([]byte, 800*1024)...)
		}, "too large"},
	}
	for _, c := range cases {
		e := newEnv(t)
		token := e.stepOne(t)
		f, files := goodFields(token), goodFiles()
		if c.fields != nil {
			c.fields(f)
		}
		if c.files != nil {
			c.files(files)
		}
		code, body := e.submit(t, f, files)
		if code != 400 || !strings.Contains(strings.ToLower(alertText(body)), c.want) {
			t.Errorf("%s: got %d %q, want 400 with %q", c.name, code, alertText(body), c.want)
		}
		if e.permitRows(t) != 0 || e.uploadFolders(t) != 0 {
			t.Errorf("%s: something was saved (rows %d, folders %d)", c.name, e.permitRows(t), e.uploadFolders(t))
		}
	}
}

func TestSubmitRefusesABadToken(t *testing.T) {
	e := newEnv(t)
	good := e.stepOne(t)
	for name, tok := range map[string]string{"empty": "", "garbage": "abc", "tampered": good[:len(good)-2] + "zz", "swapped": "x" + good} {
		code, body := e.submit(t, goodFields(tok), goodFiles())
		if code != 400 || !strings.Contains(strings.ToLower(alertText(body)), "start") {
			t.Errorf("%s: got %d %q", name, code, alertText(body))
		}
	}
	e.clock.now = "2026-11-06 20:00:00" // past the token's lifetime
	if code, _ := e.submit(t, goodFields(good), goodFiles()); code != 400 {
		t.Errorf("expired token accepted: %d", code)
	}
	if e.permitRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Error("a refused request saved something")
	}
}

func TestSubmitBlocksWhenAnActivePermitAppearedAfterStepOne(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	e.seedPermit(t, "WEP-OLD01", "PendingFinal", "", "PT Maju", "2027-01-31", "")
	code, body := e.submit(t, goodFields(token), goodFiles())
	if code != 409 || !strings.Contains(alertText(body), "WEP-OLD01") {
		t.Errorf("got %d %q, want a block naming the earlier request", code, alertText(body))
	}
	if e.permitRows(t) != 1 || e.uploadFolders(t) != 0 {
		t.Error("the blocked request saved something")
	}
}

func TestSubmitChecksTheMailDomain(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	e.mx.err = errors.New("no mx")
	code, body := e.submit(t, goodFields(token), goodFiles())
	if code != 400 || !strings.Contains(strings.ToLower(alertText(body)), "email") {
		t.Errorf("got %d %q", code, alertText(body))
	}
	if e.permitRows(t) != 0 {
		t.Error("saved despite a bad mail domain")
	}
	if _, err := e.db.Exec(`UPDATE setting SET value = 'off' WHERE setting_key = 'email_mx_check'`); err != nil {
		t.Fatal(err)
	}
	e.clock.now = "2026-11-06 08:10:00" // the policy cache lasts a few minutes; move past it
	if code, body := e.submit(t, goodFields(token), goodFiles()); code != 200 {
		t.Errorf("switch off did not skip the check: %d %q", code, alertText(body))
	}
}

func TestSubmitIsRateLimited(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	var last int
	for i := 0; i < submitsPerHour+1; i++ {
		last, _ = e.submit(t, goodFields(token), map[string][]byte{})
	}
	if last != 429 {
		t.Errorf("attempt %d got %d, want 429", submitsPerHour+1, last)
	}
}

func TestRenewalOffersTheEarlierDetailsAgain(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-OLD01", "Expired", "LEP-AAAAA", "PT Maju", "2026-09-01", "2026-08-01")
	if _, err := e.db.Exec(`UPDATE permit SET phone='6281211112222', emergency_name='Ibu Budi', street='Jl. Lama 9'`); err != nil {
		t.Fatal(err)
	}
	e.workers.people = []worker.Person{budi}
	_, body := e.permitPost(t, nil)
	for _, want := range []string{"6281211112222", "Ibu Budi", "Jl. Lama 9"} {
		if !strings.Contains(body, want) {
			t.Errorf("earlier detail %q not offered again", want)
		}
	}
}

// setSetting changes a Settings value and moves the clock past the one-minute policy cache.
func (e *testEnv) setSetting(t *testing.T, key, value string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE setting SET value = ? WHERE setting_key = ?`, value, key); err != nil {
		t.Fatalf("setting %s: %v", key, err)
	}
	e.clock.now = e.clock.now.AddMinutes(2)
}

func (e *testEnv) setPosition(title string) {
	e.workers.people = []worker.Person{{Name: budi.Name, DOB: budi.DOB, Company: budi.Company, Position: title, McuValidUntil: budi.McuValidUntil}}
}

func TestPartTwoAsksForACertificateOnlyWhenThePositionNeedsOne(t *testing.T) {
	e := newEnv(t)
	e.setPosition("Op Crane") // alias of Crane Operator, certificate required
	_, body := e.permitPost(t, nil)
	if !strings.Contains(body, `name="cert_doc"`) || !strings.Contains(body, `name="cert_until"`) || !strings.Contains(body, "SIO Crane") {
		t.Errorf("a crane operator was not asked for the certificate: %s", alertText(body))
	}
	e.setPosition("Zzz Unlisted Job")
	_, body = e.permitPost(t, nil)
	if strings.Contains(body, `name="cert_doc"`) {
		t.Error("a position that needs no certificate was asked for one")
	}
	e.setSetting(t, "certificate_required", "off")
	e.setPosition("Op Crane")
	if _, body = e.permitPost(t, nil); strings.Contains(body, `name="cert_doc"`) {
		t.Error("the certificate was asked for although the setting is off")
	}
}

func TestSubmitStoresTheCertificateAndThePositionSnapshot(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	code, body := e.submit(t, goodFields(token), goodFiles())
	if code != 200 {
		t.Fatalf("%d %s", code, alertText(body))
	}
	var canon, risk, req, until, doc string
	if err := e.db.QueryRow(`SELECT position_canonical, position_risk, cert_required, cert_until, cert_doc FROM permit`).Scan(&canon, &risk, &req, &until, &doc); err != nil {
		t.Fatal(err)
	}
	if canon != "Welder" || req != "Y" || until != "2027-05-01" || !strings.HasSuffix(doc, "/cert_doc.pdf") || risk == "" {
		t.Errorf("got %q %q %q %q %q", canon, risk, req, until, doc)
	}
}

func TestSubmitCertificateRules(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	f := goodFields(token)
	f["cert_until"] = "05/11/2026" // yesterday
	if code, body := e.submit(t, f, goodFiles()); code != 400 || !strings.Contains(strings.ToLower(alertText(body)), "certificate") {
		t.Errorf("expired certificate: %d %q", code, alertText(body))
	}
	files := goodFiles()
	delete(files, "cert_doc")
	if code, body := e.submit(t, goodFields(token), files); code != 400 || !strings.Contains(strings.ToLower(alertText(body)), "certificate") {
		t.Errorf("missing certificate: %d %q", code, alertText(body))
	}
	if e.permitRows(t) != 0 || e.uploadFolders(t) != 0 {
		t.Error("a refused request saved something")
	}
}

func TestPerpetualCertificateNeedsNoDate(t *testing.T) {
	e := newEnv(t)
	e.setPosition("Nurse")
	_, body := e.permitPost(t, nil)
	if !strings.Contains(body, `name="cert_doc"`) || strings.Contains(body, `name="cert_until"`) {
		t.Fatalf("a nurse should be asked for the file only: %s", alertText(body))
	}
	token := tokenRe.FindStringSubmatch(body)[1]
	f := goodFields(token)
	delete(f, "cert_until")
	if code, out := e.submit(t, f, goodFiles()); code != 200 {
		t.Fatalf("%d %s", code, alertText(out))
	}
	var until string
	_ = e.db.QueryRow(`SELECT cert_until FROM permit`).Scan(&until)
	if until != "2090-12-31" {
		t.Errorf("cert_until = %q, want the fixed 2090-12-31", until)
	}
}

func TestContractAndBpjsFollowTheToggles(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	_, body := e.permitPost(t, nil) // production defaults: shown, not required
	for _, want := range []string{`name="contract_doc"`, `name="contract_until"`, `name="bpjs_kes"`, `name="bpjs_tk"`} {
		if !strings.Contains(body, want) {
			t.Errorf("default form lacks %s", want)
		}
	}
	e.setSetting(t, "contract_shown", "off")
	e.setSetting(t, "bpjs_kes_shown", "off")
	_, body = e.permitPost(t, nil)
	if strings.Contains(body, `name="contract_doc"`) || strings.Contains(body, `name="bpjs_kes"`) || !strings.Contains(body, `name="bpjs_tk"`) {
		t.Error("the toggles did not hide exactly the contract and BPJS Kesehatan")
	}
}

func TestRequiredContractAndBpjsAreEnforced(t *testing.T) {
	e := newEnv(t)
	e.setSetting(t, "contract_required", "on")
	e.setSetting(t, "bpjs_kes_required", "on")
	token := e.stepOne(t)
	code, body := e.submit(t, goodFields(token), goodFiles())
	if code != 400 || !strings.Contains(strings.ToLower(alertText(body)), "contract") {
		t.Fatalf("contract not enforced: %d %q", code, alertText(body))
	}
	files := goodFiles()
	files["contract_doc"] = pdfBytes
	f := goodFields(token)
	f["contract_until"] = "01/03/2027"
	if code, body = e.submit(t, f, files); code != 400 || !strings.Contains(body, "BPJS Kesehatan") {
		t.Fatalf("BPJS not enforced: %d %q", code, alertText(body))
	}
	f["bpjs_kes"] = "0001 2345 67890"
	if code, body = e.submit(t, f, files); code != 200 {
		t.Fatalf("complete form refused: %d %q", code, alertText(body))
	}
	var until, doc, kes, lep string
	if err := e.db.QueryRow(`SELECT contract_until, contract_doc, bpjs_kes, lep_valid_until FROM permit`).Scan(&until, &doc, &kes, &lep); err != nil {
		t.Fatal(err)
	}
	// The contract ends 2027-03-01, after the MCU-based 2027-01-17, so the MCU still binds.
	if until != "2027-03-01" || !strings.HasSuffix(doc, "/contract_doc.pdf") {
		t.Errorf("contract stored as %q %q", until, doc)
	}
	if kes != "0001234567890" {
		t.Errorf("BPJS number stored as %q, want the digits only", kes)
	}
	if lep != "2027-01-17" {
		t.Errorf("lep_valid_until = %s", lep)
	}
}

func TestAContractThatEndsBeforeTheMcuCapsThePermit(t *testing.T) {
	e := newEnv(t)
	token := e.stepOne(t)
	f := goodFields(token)
	f["contract_until"] = "01/01/2027"
	files := goodFiles()
	files["contract_doc"] = pdfBytes
	if code, body := e.submit(t, f, files); code != 200 || !strings.Contains(body, "Contract validity") || !strings.Contains(body, "2027-01-01") {
		t.Errorf("%d %q", code, alertText(body))
	}
}

func TestFilesForSectionsThatAreNotShownAreIgnored(t *testing.T) {
	e := newEnv(t)
	e.setSetting(t, "contract_shown", "off")
	token := e.stepOne(t)
	files := goodFiles()
	files["contract_doc"] = pdfBytes
	files["visa_doc"] = pdfBytes // an Indonesian has no visa section
	code, body := e.submit(t, goodFields(token), files)
	if code != 200 {
		t.Fatalf("%d %q", code, alertText(body))
	}
	m := regexp.MustCompile(`WEP-[A-Z0-9]{5}`).FindString(body)
	for _, name := range []string{"contract_doc.pdf", "visa_doc.pdf"} {
		if _, err := os.Stat(filepath.Join(e.uploads, m, name)); err == nil {
			t.Errorf("%s was stored although its section is not part of this form", name)
		}
	}
}

// The spare email is cleaned like the old system did (spaces out, lower case), checked for shape
// and for a mailbox domain, and stored with the main address as "main,spare".
func TestSpareEmailIsCleanedCheckedAndStoredWithTheMainAddress(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	for name, c := range map[string]struct{ spare, want string }{
		"bad shape": {"rina@nodot", "spare email"},
		"two dots":  {"a@b", "spare email"},
	} {
		code, body := e.permitPost(t, map[string]string{"spare_email": c.spare})
		if code != 400 || !strings.Contains(strings.ToLower(alertText(body)), c.want) {
			t.Errorf("%s: %d %q", name, code, alertText(body))
		}
	}
	// A bad domain on the spare address is refused, naming that address.
	e.mx.err, e.mx.badOnly = errors.New("no mx"), "nowhere.test"
	code, body := e.permitPost(t, map[string]string{"spare_email": "x@nowhere.test"})
	if code != 400 || !strings.Contains(alertText(body), "x@nowhere.test") {
		t.Errorf("bad spare domain: %d %q", code, alertText(body))
	}
	e.mx.err, e.mx.badOnly = nil, ""
	// Spaces and capitals are cleaned; the same address twice is one address.
	code, body = e.permitPost(t, map[string]string{"spare_email": " Rina@Example.COM "})
	if code != 200 {
		t.Fatalf("same as main: %d %s", code, alertText(body))
	}
	code, body = e.permitPost(t, map[string]string{"spare_email": " Backup @Example.COM "})
	if code != 200 {
		t.Fatalf("good spare: %d %s", code, alertText(body))
	}
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no token")
	}
	if code, body := e.submit(t, goodFields(m[1]), goodFiles()); code != 200 {
		t.Fatalf("submit: %d %s", code, alertText(body))
	}
	var stored string
	if err := e.db.QueryRow(`SELECT requester_email FROM permit`).Scan(&stored); err != nil || stored != "rina@example.com,backup@example.com" {
		t.Errorf("stored %q, %v", stored, err)
	}
}
