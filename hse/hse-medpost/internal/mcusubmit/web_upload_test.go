package mcusubmit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// uploadEnv is a portal with the upload pipeline switched on (in-memory file store, real
// Ghostscript) and one signed-in submitter.
type uploadEnv struct {
	*env
	files  *memStore
	cookie *http.Cookie
	userID int64
}

func newUploadEnv(t *testing.T) *uploadEnv {
	t.Helper()
	hasGS(t)
	e := newEnv(t)
	rb, err := NewRebuilder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := newMemStore()
	e.api.SetPipeline(&Pipeline{Store: files, Rebuilder: rb})
	for _, n := range []string{"Indonesia", "Philippines"} {
		if _, err := e.store.DB.Exec(`INSERT INTO NationalityTable (Name) VALUES (?)`, n); err != nil {
			t.Fatal(err)
		}
	}
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	if cookie == nil {
		t.Fatal("sign-in gave no cookie")
	}
	return &uploadEnv{env: e, files: files, cookie: cookie, userID: id}
}

// fields is a valid form for one worker; tests override single values.
func (u *uploadEnv) fields(over map[string]string) map[string]string {
	f := map[string]string{
		"name": "Siti Aminah", "dob": "1990-05-17", "position": "Welder",
		"company_id": itoa(u.companyID), "nationality": "Indonesia", "gender": "Female", "id_number": "3201012345678901",
	}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func (u *uploadEnv) upload(cookie *http.Cookie, fields map[string]string, pdf []byte) *httptest.ResponseRecorder {
	u.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if pdf != nil {
		fw, _ := w.CreateFormFile("file", "result.pdf")
		_, _ = fw.Write(pdf)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/mcu-submit/batch/add", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	u.mux.ServeHTTP(rec, req)
	return rec
}

func (u *uploadEnv) lastSubmissionID() int64 {
	u.t.Helper()
	var id int64
	if err := u.store.DB.QueryRow(`SELECT SubmissionID FROM McuSubmission ORDER BY SubmissionID DESC LIMIT 1`).Scan(&id); err != nil {
		u.t.Fatal(err)
	}
	return id
}

func (u *uploadEnv) rowCount() int {
	var n int
	_ = u.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmission`).Scan(&n)
	return n
}

func (u *uploadEnv) uploadOK() int64 {
	u.t.Helper()
	rec := u.upload(u.cookie, u.fields(nil), plainPDF("", ""))
	if rec.Code != http.StatusSeeOther {
		u.t.Fatalf("upload = %d\n%s", rec.Code, rec.Body)
	}
	return u.lastSubmissionID()
}

// patient seeds an existing worker; the picker path takes every detail from this row.
func (u *uploadEnv) patient(name, dob string) int64 {
	u.t.Helper()
	var nat int64
	_ = u.store.DB.QueryRow(`SELECT NationalityID FROM NationalityTable WHERE Name = 'Indonesia'`).Scan(&nat)
	res, err := u.store.DB.Exec(`INSERT INTO Patient (Name, DateOfBirth, WorkPosition, FK_CompanyID, FK_NationalityID) VALUES (?, ?, 'Rigger', ?, ?)`,
		name, dob, u.companyID, nat)
	if err != nil {
		u.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (u *uploadEnv) confirm(id int64) {
	u.t.Helper()
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/confirm", id), nil, u.cookie); rec.Code != http.StatusSeeOther {
		u.t.Fatalf("confirm = %d\n%s", rec.Code, rec.Body)
	}
}

func (u *uploadEnv) uploadConfirmed() int64 {
	u.t.Helper()
	id := u.uploadOK()
	u.confirm(id)
	return id
}

func (u *uploadEnv) onboardSecond() {
	u.t.Helper()
	u.onboard("hr2@example.com", "hr.other", "a-long-passphrase-2")
}

func (u *uploadEnv) sendBatch() {
	u.t.Helper()
	var batch int64
	if err := u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission ORDER BY SubmissionID DESC LIMIT 1`).Scan(&batch); err != nil {
		u.t.Fatal(err)
	}
	if rec := u.post("/mcu-submit/batch/send", url.Values{"batch": {itoa(batch)}, "confirm": {"yes"}}, u.cookie); rec.Code != http.StatusSeeOther {
		u.t.Fatalf("send = %d\n%s", rec.Code, rec.Body)
	}
}

func TestUploadPagesNeedASession(t *testing.T) {
	u := newUploadEnv(t)
	for _, path := range []string{"/mcu-submit/batch", "/mcu-submit/documents", "/mcu-submit/documents/1/file", "/mcu-submit/documents/1/review", "/mcu-submit/workers?q=abc"} {
		if rec := u.get(path); rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s without a session = %d, want a redirect to sign-in", path, rec.Code)
		}
	}
	if rec := u.upload(nil, u.fields(nil), plainPDF("", "")); rec.Code != http.StatusSeeOther {
		t.Errorf("upload without a session = %d, want a redirect", rec.Code)
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatal("an anonymous upload left something behind")
	}
}

func TestUploadStoresTheRebuiltFileAndAScannedDraftRow(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()

	var status, scan, name, nik, path, sha string
	var bytesN int
	var passport *string
	err := u.store.DB.QueryRow(`SELECT Status, ScanStatus, PatName, PatNik, PatPassportNo, FilePath, FileSha256, FileBytes FROM McuSubmission WHERE SubmissionID = ?`, id).
		Scan(&status, &scan, &name, &nik, &passport, &path, &sha, &bytesN)
	if err != nil {
		t.Fatal(err)
	}
	if status != SubReview || scan != ScanClean || name != "Siti Aminah" || nik != "3201012345678901" || passport != nil {
		t.Fatalf("row = %s %s %q %q %v", status, scan, name, nik, passport)
	}
	if len(sha) != 64 || bytesN <= 0 || u.files.count() != 1 {
		t.Fatalf("sha %q, bytes %d, stored files %d", sha, bytesN, u.files.count())
	}
	page := u.get("/mcu-submit/batch", u.cookie).Body.String()
	for _, want := range []string{"Siti Aminah", "Review", "still need your review"} {
		if !strings.Contains(page, want) {
			t.Errorf("batch page lacks %q", want)
		}
	}
	if strings.Contains(page, path) {
		t.Error("the storage path leaked into the page")
	}
}

func TestUploadRefusesBadFieldsAndStoresNothing(t *testing.T) {
	u := newUploadEnv(t)
	cases := map[string]map[string]string{
		"future birth date":       {"dob": "2030-01-01"},
		"under 15":                {"dob": "2020-01-01"},
		"over 80":                 {"dob": "1930-01-01"},
		"not a date":              {"dob": "17/05/1990"},
		"empty name":              {"name": "   "},
		"empty NIK":               {"id_number": ""},
		"blank NIK":               {"id_number": "   "},
		"empty passport":          {"nationality": "Philippines", "id_number": ""},
		"NIK too short":           {"id_number": "12345"},
		"NIK with letters":        {"id_number": "32010123456789AB"},
		"passport for Indonesian": {"id_number": "P1234567"},
		"NIK for foreigner":       {"nationality": "Philippines", "id_number": "3201012345678901"},
		"passport too short":      {"nationality": "Philippines", "id_number": "P12"},
		"unknown nationality":     {"nationality": "Atlantis"},
		"no gender":               {"gender": ""},
		"gender not on the list":  {"gender": "Other"},
		"gender in lower case":    {"gender": "male"},
		"company not in the list": {"company_id": "99999"},
		"company is not a number": {"company_id": "abc"},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			rec := u.upload(u.cookie, u.fields(over), plainPDF("", ""))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "mp-error") {
				t.Fatalf("= %d, want 400 with an error\n%s", rec.Code, rec.Body)
			}
		})
	}
	if rec := u.upload(u.cookie, u.fields(nil), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no file = %d, want 400", rec.Code)
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatalf("refused uploads left %d rows and %d files", u.rowCount(), u.files.count())
	}
}

func TestUploadStoresTheGenderTheSubmitterChose(t *testing.T) {
	u := newUploadEnv(t)
	if rec := u.upload(u.cookie, u.fields(map[string]string{"gender": "Male"}), plainPDF("", "")); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	var gender string
	if err := u.store.DB.QueryRow(`SELECT PatGender FROM McuSubmission`).Scan(&gender); err != nil || gender != "Male" {
		t.Fatalf("stored gender = %q, %v; want Male", gender, err)
	}
	// The review page shows it, so the submitter can check it before confirming.
	id := u.lastSubmissionID()
	if body := u.get(fmt.Sprintf("/mcu-submit/documents/%d/review", id), u.cookie).Body.String(); !strings.Contains(body, "<dt>Gender</dt><dd>Male</dd>") {
		t.Errorf("the review page does not show the gender\n%s", body)
	}
}

func TestAPickedWorkersIDNumberIsCopiedWhenThePatientHasOne(t *testing.T) {
	u := newUploadEnv(t)
	with := u.patient("Joko Widodo", "1982-03-04")
	without := u.patient("Dewi Sartika", "1985-06-07")
	if _, err := u.store.DB.Exec(`UPDATE Patient SET PersonalID = '3201 0123-4567 8901' WHERE PatientID = ?`, with); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int64{with, without} {
		if rec := u.upload(u.cookie, map[string]string{"patient_id": itoa(pid)}, plainPDF("", "")); rec.Code != http.StatusSeeOther {
			t.Fatalf("= %d\n%s", rec.Code, rec.Body)
		}
	}
	got := map[int64]string{}
	rows, err := u.store.DB.Query(`SELECT FK_PatientID, COALESCE(PatNik, '') || '|' || COALESCE(PatPassportNo, '') FROM McuSubmission`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var ids string
		if err := rows.Scan(&pid, &ids); err != nil {
			t.Fatal(err)
		}
		got[pid] = ids
	}
	if got[with] != "3201012345678901|" {
		t.Errorf("a patient with an ID number: stored %q, want the NIK without spaces or dashes", got[with])
	}
	if got[without] != "|" {
		t.Errorf("a patient with no ID number: stored %q, want nothing", got[without])
	}
}

func TestAPickedWorkersGenderComesFromTheirPatientRecord(t *testing.T) {
	u := newUploadEnv(t)
	pid := u.patient("Joko Widodo", "1982-03-04")
	if _, err := u.store.DB.Exec(`UPDATE Patient SET Gender = 'Male' WHERE PatientID = ?`, pid); err != nil {
		t.Fatal(err)
	}
	// A tampered form cannot change it: the picker sends no gender at all, and one sent is ignored.
	if rec := u.upload(u.cookie, map[string]string{"patient_id": itoa(pid), "gender": "Female"}, plainPDF("", "")); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	var gender string
	if err := u.store.DB.QueryRow(`SELECT COALESCE(PatGender, '') FROM McuSubmission`).Scan(&gender); err != nil || gender != "Male" {
		t.Fatalf("stored gender = %q, %v; want the patient's Male", gender, err)
	}
}

func TestUploadAcceptsAForeignerWithAPassport(t *testing.T) {
	u := newUploadEnv(t)
	rec := u.upload(u.cookie, u.fields(map[string]string{"nationality": "Philippines", "id_number": "p1234567"}), plainPDF("", ""))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	var nik *string
	var passport string
	if err := u.store.DB.QueryRow(`SELECT PatNik, PatPassportNo FROM McuSubmission`).Scan(&nik, &passport); err != nil || nik != nil || passport != "P1234567" {
		t.Fatalf("nik %v passport %q err %v", nik, passport, err)
	}
}

func TestUploadRefusesAnUnsafePDFWithAClearMessage(t *testing.T) {
	u := newUploadEnv(t)
	cases := map[string]struct {
		pdf  []byte
		want string
	}{
		"not a pdf":  {[]byte("hello"), "not a PDF"},
		"javascript": {plainPDF("/OpenAction << /S /JavaScript /JS (x) >>", ""), "active content"},
		"encrypted":  {bytes.Replace(plainPDF("", ""), []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 9 0 R"), 1), "password"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := u.upload(u.cookie, u.fields(nil), c.pdf)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("= %d, want 400 mentioning %q\n%s", rec.Code, c.want, rec.Body)
			}
		})
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatal("an unsafe upload left a row or a file")
	}
}

func TestUploadOverTheSizeLimitGetsA413(t *testing.T) {
	u := newUploadEnv(t)
	big := append([]byte("%PDF-1.4\n"), make([]byte, MaxUploadBytes+2<<20)...)
	if rec := u.upload(u.cookie, u.fields(nil), big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("= %d, want 413", rec.Code)
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatal("an oversize upload left a row or a file")
	}
}

// The daily cap is checked before Ghostscript runs, so a hammering account cannot burn the Pi's CPU.
func TestUploadDailyLimitIsCheckedBeforeAnyWork(t *testing.T) {
	u := newUploadEnv(t)
	for i := 0; i < maxUploadsPerDay; i++ {
		if err := u.store.LogEvent(context.Background(), Event{ActorType: ActorSubmitter, ActorID: u.userID, EntityType: EntitySubmission, EntityID: int64(i + 1), Action: "Uploaded"}); err != nil {
			t.Fatal(err)
		}
	}
	rec := u.upload(u.cookie, u.fields(nil), plainPDF("", ""))
	if rec.Code != http.StatusTooManyRequests || u.files.count() != 0 || u.rowCount() != 0 {
		t.Fatalf("= %d, files %d, rows %d; want 429 and nothing stored", rec.Code, u.files.count(), u.rowCount())
	}
}

func TestUploadIsOffWithoutAPipeline(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	if rec := e.get("/mcu-submit/batch", cookie); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("batch page = %d, want 503", rec.Code)
	}
}

// ---- the file route ---------------------------------------------------------------------------

func TestOwnFileIsServedAsASandboxedPDFAndTheViewIsLogged(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()
	rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/file", id), u.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	want := map[string]string{
		"Content-Type": "application/pdf", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
		"Content-Disposition": "inline",
	}
	// A CSP "sandbox" header makes Edge and Chrome refuse their built-in PDF viewer
	// ("This page has been blocked by Microsoft Edge"), so a PDF must not carry it.
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("Content-Security-Policy = %q, want none on a PDF", csp)
	}
	for k, v := range want {
		if !strings.HasPrefix(rec.Header().Get(k), v) {
			t.Errorf("%s = %q, want %q", k, rec.Header().Get(k), v)
		}
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatal("body is not a PDF")
	}
	var n int
	_ = u.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmissionEvent WHERE Action = 'FileViewed' AND EntityID = ?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("%d FileViewed events, want 1", n)
	}
}

// Another submitter's document, a missing one and a withdrawn one must look identical, so ids
// cannot be probed.
func TestFileRouteGivesTheSame404ForOthersMissingAndWithdrawn(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadConfirmed()
	u.onboardSecond()
	_, other := u.signIn("hr.other", "a-long-passphrase-2")

	others := u.get(fmt.Sprintf("/mcu-submit/documents/%d/file", id), other)
	missing := u.get("/mcu-submit/documents/9999/file", u.cookie)
	if others.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || others.Body.String() != missing.Body.String() {
		t.Fatalf("others %d, missing %d; want identical 404s", others.Code, missing.Code)
	}
	u.sendBatch()
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/withdraw", id), nil, u.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("withdraw = %d\n%s", rec.Code, rec.Body)
	}
	if rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/file", id), u.cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("withdrawn file = %d, want 404", rec.Code)
	}
}

// ---- remove, send, withdraw -------------------------------------------------------------------

func TestRemovingADraftDeletesTheRowAndTheFile(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/remove", id), nil, u.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatalf("rows %d, files %d after removing the draft", u.rowCount(), u.files.count())
	}
}

func TestSendMovesDraftsToPendingAndLocksThem(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadConfirmed()
	u.sendBatch()
	var status string
	_ = u.store.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status)
	if status != SubPending {
		t.Fatalf("status = %s, want Pending", status)
	}
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/remove", id), nil, u.cookie); rec.Code != http.StatusConflict {
		t.Errorf("removing a sent document = %d, want 409", rec.Code)
	}
	if u.files.count() != 1 {
		t.Fatalf("files = %d, a sent document's file must stay", u.files.count())
	}
	id2 := u.uploadConfirmed()
	var b1, b2 int64
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&b1)
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, id2).Scan(&b2)
	if b1 == b2 {
		t.Fatal("a sent batch accepted another document")
	}
}

func TestWithdrawDeletesTheFileAndKeepsTheRow(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadConfirmed()
	u.sendBatch()
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/withdraw", id), nil, u.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	var status string
	var path *string
	_ = u.store.DB.QueryRow(`SELECT Status, FilePath FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status, &path)
	if status != SubWithdrawn || path != nil || u.files.count() != 0 {
		t.Fatalf("status %s, path %v, files %d", status, path, u.files.count())
	}
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/withdraw", id), nil, u.cookie); rec.Code != http.StatusConflict {
		t.Errorf("withdrawing twice = %d, want 409", rec.Code)
	}
}

func TestSubmittersCannotTouchEachOthersDocuments(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()
	other2 := u.onboard("hr2@example.com", "hr.other", "a-long-passphrase-2")
	// Colleagues of one company may withdraw each other's Pending documents (owner, 2026-11-05), so
	// the "somebody else" here belongs to another company.
	if _, err := u.store.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Elsewhere')`); err != nil {
		t.Fatal(err)
	}
	if _, err := u.store.DB.Exec(`UPDATE Submitter SET FK_CompanyID = (SELECT CompanyID FROM Company WHERE Name = 'PT Elsewhere') WHERE SubmitterID = ?`, other2); err != nil {
		t.Fatal(err)
	}
	_, other := u.signIn("hr.other", "a-long-passphrase-2")
	for _, action := range []string{"remove", "withdraw"} {
		if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/%s", id, action), nil, other); rec.Code != http.StatusNotFound {
			t.Errorf("%s by another submitter = %d, want 404", action, rec.Code)
		}
	}
	if u.rowCount() != 1 || u.files.count() != 1 {
		t.Fatal("another submitter changed the document")
	}
}

func TestDocumentsTableListsOnlyTheSubmittersOwnRows(t *testing.T) {
	u := newUploadEnv(t)
	u.uploadOK()
	u.onboardSecond()
	_, other := u.signIn("hr.other", "a-long-passphrase-2")
	if body := u.get("/mcu-submit/documents", other).Body.String(); strings.Contains(body, "Siti Aminah") {
		t.Fatal("another submitter's worker is listed")
	}
	body := u.get("/mcu-submit/documents", u.cookie).Body.String()
	if !strings.Contains(body, "Siti Aminah") || !strings.Contains(body, "/file") {
		t.Fatalf("own row or file link missing\n%s", body)
	}
}

// ---- worker picker, review step, new employees -----------------------------------------------

func TestPickedWorkerDetailsComeFromTheDatabaseNotTheForm(t *testing.T) {
	u := newUploadEnv(t)
	pid := u.patient("Joko Widodo", "1982-03-04")
	rec := u.upload(u.cookie, map[string]string{
		"patient_id": itoa(pid), "name": "Tampered Name", "dob": "2001-01-01", "position": "Hacker",
	}, plainPDF("", ""))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "/review") {
		t.Fatalf("redirect = %q, want the review page", loc)
	}
	var name, dob, pos, company, nat string
	var fk int64
	var isNew int
	err := u.store.DB.QueryRow(`SELECT PatName, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality, FK_PatientID, IsNewEmployee FROM McuSubmission`).
		Scan(&name, &dob, &pos, &company, &nat, &fk, &isNew)
	if err != nil || name != "Joko Widodo" || dob != "1982-03-04" || pos != "Rigger" || company != "PT Example" || nat != "Indonesia" || fk != pid || isNew != 0 {
		t.Fatalf("row = %q %q %q %q %q %d %d, %v", name, dob, pos, company, nat, fk, isNew, err)
	}
}

func TestUploadRefusesAnUnknownPatientAndNoWorkerAtAll(t *testing.T) {
	u := newUploadEnv(t)
	for name, f := range map[string]map[string]string{
		"unknown patient": {"patient_id": "99999"},
		"not a number":    {"patient_id": "abc"},
		"nothing chosen":  {},
	} {
		if rec := u.upload(u.cookie, f, plainPDF("", "")); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatal("a refused upload left a row or a file")
	}
}

func TestNewEmployeeAlreadyOnFileMustBePickedInstead(t *testing.T) {
	u := newUploadEnv(t)
	u.patient("Siti Aminah", "1990-05-17")
	rec := u.upload(u.cookie, u.fields(nil), plainPDF("", ""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already on file") {
		t.Fatalf("= %d, want 400 saying the worker is already on file\n%s", rec.Code, rec.Body)
	}
	if u.rowCount() != 0 || u.files.count() != 0 {
		t.Fatal("a duplicate worker was stored")
	}
}

func TestNewEmployeeIsFlaggedForStaff(t *testing.T) {
	u := newUploadEnv(t)
	u.uploadOK()
	var isNew int
	var fk *int64
	_ = u.store.DB.QueryRow(`SELECT IsNewEmployee, FK_PatientID FROM McuSubmission`).Scan(&isNew, &fk)
	if isNew != 1 || fk != nil {
		t.Fatalf("IsNewEmployee %d, patient %v; want 1 and none", isNew, fk)
	}
}

func TestReviewPageShowsTheDetailsAndConfirmAddsTheDocumentToTheBatch(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()
	page := u.get(fmt.Sprintf("/mcu-submit/documents/%d/review", id), u.cookie).Body.String()
	for _, want := range []string{"Siti Aminah", "1990-05-17", "Open the PDF in a new tab", "Confirm and add to batch", "Discard", "New employee"} {
		if !strings.Contains(page, want) {
			t.Errorf("review page lacks %q", want)
		}
	}
	// Send is blocked while the document is unconfirmed.
	if batch := u.get("/mcu-submit/batch", u.cookie).Body.String(); strings.Contains(batch, "Send batch of") {
		t.Error("the batch page offers Send with an unconfirmed document")
	}
	var batchID int64
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission`).Scan(&batchID)
	if rec := u.post("/mcu-submit/batch/send", url.Values{"batch": {itoa(batchID)}}, u.cookie); rec.Code != http.StatusConflict {
		t.Fatalf("send while in review = %d, want 409", rec.Code)
	}
	u.confirm(id)
	if batch := u.get("/mcu-submit/batch", u.cookie).Body.String(); !strings.Contains(batch, "Send batch of 1") {
		t.Error("after confirming, the batch page should offer Send")
	}
	u.sendBatch()
	var status string
	_ = u.store.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status)
	if status != SubPending {
		t.Fatalf("status = %s, want Pending", status)
	}
}

func TestSeveralDocumentsGoInOneBatch(t *testing.T) {
	u := newUploadEnv(t)
	first := u.uploadConfirmed()
	rec := u.upload(u.cookie, u.fields(map[string]string{"name": "Budi Santoso", "id_number": "3201012345678902"}), plainPDF("", ""))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second upload = %d\n%s", rec.Code, rec.Body)
	}
	second := u.lastSubmissionID()
	u.confirm(second)
	var b1, b2 int64
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, first).Scan(&b1)
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, second).Scan(&b2)
	if b1 != b2 {
		t.Fatal("two documents added one by one landed in different batches")
	}
	u.sendBatch()
	var pending int
	_ = u.store.DB.QueryRow(`SELECT COUNT(*) FROM McuSubmission WHERE Status = 'Pending'`).Scan(&pending)
	if pending != 2 {
		t.Fatalf("%d pending, want 2 sent together", pending)
	}
}

func TestOnlyTheOwnerCanReviewOrConfirm(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadOK()
	u.onboardSecond()
	_, other := u.signIn("hr.other", "a-long-passphrase-2")
	if rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/review", id), other); rec.Code != http.StatusNotFound {
		t.Errorf("review by another submitter = %d, want 404", rec.Code)
	}
	if rec := u.post(fmt.Sprintf("/mcu-submit/documents/%d/confirm", id), nil, other); rec.Code != http.StatusNotFound {
		t.Errorf("confirm by another submitter = %d, want 404", rec.Code)
	}
}

func TestWorkerSearchEndpoint(t *testing.T) {
	u := newUploadEnv(t)
	u.patient("Joko Widodo", "1982-03-04")
	rec := u.get("/mcu-submit/workers?q=jok", u.cookie)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("= %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got []workerJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Name != "Joko Widodo" || got[0].DateOfBirth != "1982-03-04" {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	if body := u.get("/mcu-submit/workers?q=jo", u.cookie).Body.String(); strings.TrimSpace(body) != "[]" {
		t.Errorf("a two-letter query returned %s, want []", body)
	}
}

func TestWorkerSearchIsRateLimitedPerAccount(t *testing.T) {
	u := newUploadEnv(t)
	u.api.limits.WorkerSearchPerUser = 3
	for i := 0; i < 3; i++ {
		if rec := u.get("/mcu-submit/workers?q=abc", u.cookie); rec.Code != http.StatusOK {
			t.Fatalf("search %d = %d", i, rec.Code)
		}
	}
	if rec := u.get("/mcu-submit/workers?q=abc", u.cookie); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit = %d, want 429", rec.Code)
	}
}

func TestSendAsksForConfirmationFirstAndSendsNothingUntilConfirmed(t *testing.T) {
	u := newUploadEnv(t)
	id := u.uploadConfirmed()
	var batch int64
	_ = u.store.DB.QueryRow(`SELECT FK_BatchID FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&batch)

	rec := u.post("/mcu-submit/batch/send", url.Values{"batch": {itoa(batch)}}, u.cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Yes, send 1") || !strings.Contains(rec.Body.String(), "Siti Aminah") {
		t.Fatalf("first POST = %d, want the confirmation page\n%s", rec.Code, rec.Body)
	}
	var status string
	_ = u.store.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status)
	if status != SubDraft {
		t.Fatalf("status = %s: asking must not send", status)
	}
	if rec := u.post("/mcu-submit/batch/send", url.Values{"batch": {itoa(batch)}, "confirm": {"yes"}}, u.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirmed send = %d", rec.Code)
	}
	_ = u.store.DB.QueryRow(`SELECT Status FROM McuSubmission WHERE SubmissionID = ?`, id).Scan(&status)
	if status != SubPending {
		t.Fatalf("status = %s after confirming", status)
	}
}
