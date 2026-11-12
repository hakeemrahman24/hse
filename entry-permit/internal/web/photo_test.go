package web

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"entry-permit/internal/worker"
)

var photoTokenRe = regexp.MustCompile(`name="token" value="([^"]+)"`)

// bigJPEG is a picture a phone could take: large and detailed.
func bigJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * y), uint8(x ^ y), uint8(x + y), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// photoToken looks Budi up and returns the token the photo form carries.
func (e *testEnv) photoToken(t *testing.T) string {
	t.Helper()
	e.workers.people = []worker.Person{budi}
	code, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	if code != 200 {
		t.Fatalf("lookup: %d", code)
	}
	m := photoTokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the result page has no photo form: %s", body)
	}
	return m[1]
}

func (e *testEnv) photoPost(t *testing.T, token string, file []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("token", token)
	if file != nil {
		fw, _ := mw.CreateFormFile("photo", "me.jpg")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/my/photo", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *testEnv) idPhoto(t *testing.T, id string) string {
	t.Helper()
	var p string
	if err := e.db.QueryRow(`SELECT id_photo FROM permit WHERE request_id = ?`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookupResultOffersToChangeThePhotoWithoutEchoingTheBirthDate(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	_, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	for _, want := range []string{`action="/my/photo"`, `enctype="multipart/form-data"`, `name="photo"`, `name="token"`, `data-autosubmit`} {
		if !strings.Contains(body, want) {
			t.Errorf("result page lacks %q", want)
		}
	}
	if strings.Contains(body, "1988") || strings.Contains(body, "05/03") {
		t.Error("the date of birth is in the page")
	}
}

func TestChangingThePhotoShrinksItAndReplacesTheOldOneOnEveryActivePermit(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	e.seedPermit(t, "WEP-BBBBB", "PendingStage1", "", "PT Maju", "2027-01-31", "2027-01-31")
	e.seedPermit(t, "WEP-CCCCC", "Rejected", "", "PT Maju", "2027-01-31", "2027-01-31")
	// The first permit already has an old PNG photo, which must not be left in storage.
	if err := os.MkdirAll(filepath.Join(e.uploads, "WEP-AAAAA"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(e.uploads, "WEP-AAAAA", "id_photo.png")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE permit SET id_photo = 'WEP-AAAAA/id_photo.png' WHERE request_id = 'WEP-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	tok := e.photoToken(t)
	in := bigJPEG(t)

	code, body := e.photoPost(t, tok, in)
	if code != 200 || !strings.Contains(body, "Photo updated") || !strings.Contains(body, "2 permits") {
		t.Fatalf("status %d: %s", code, alertText(body))
	}
	for _, id := range []string{"WEP-AAAAA", "WEP-BBBBB"} {
		if got := e.idPhoto(t, id); got != id+"/id_photo.jpg" {
			t.Errorf("%s id_photo = %q", id, got)
		}
		fi, err := os.Stat(filepath.Join(e.uploads, id, "id_photo.jpg"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > 300_000 || int(fi.Size())*10 > len(in) {
			t.Errorf("%s is %d bytes from %d: not shrunk", id, fi.Size(), len(in))
		}
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("the old photo is still in storage")
	}
	if got := e.idPhoto(t, "WEP-CCCCC"); got != "" {
		t.Errorf("a rejected permit's photo was changed: %q", got)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'photo.change'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("audit rows = %d, err = %v", n, err)
	}
}

func TestPhotoChangeNeedsAGenuineUnexpiredToken(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	for name, bad := range map[string]string{"none": "", "garbage": "abc.def", "tampered": tok[:len(tok)-2] + "xx"} {
		if code, _ := e.photoPost(t, bad, bigJPEG(t)); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	e.clock.now = "2026-11-06 12:30:00" // 4.5 hours later: past the four-hour proof (photoTokenHours)
	if code, body := e.photoPost(t, tok, bigJPEG(t)); code != 400 || !strings.Contains(body, "look yourself up again") {
		t.Errorf("expired: %d %s", code, alertText(body))
	}
	if got := e.idPhoto(t, "WEP-AAAAA"); got != "" {
		t.Errorf("a refused upload changed the permit: %q", got)
	}
}

func TestAnUnusableFileIsRefusedAndTheOldPhotoStays(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	if code, _ := e.photoPost(t, tok, bigJPEG(t)); code != 200 {
		t.Fatal("setup upload failed")
	}
	before, _ := os.ReadFile(filepath.Join(e.uploads, "WEP-AAAAA", "id_photo.jpg"))
	for name, file := range map[string][]byte{"pdf": pdfBytes, "text": []byte("hello"), "none": nil} {
		code, body := e.photoPost(t, tok, file)
		if code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
		if name != "none" && !strings.Contains(body, "JPG or PNG") {
			t.Errorf("%s: message %q", name, alertText(body))
		}
		if !strings.Contains(body, `name="photo"`) {
			t.Errorf("%s: the form is not offered again", name)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(e.uploads, "WEP-AAAAA", "id_photo.jpg")); !bytes.Equal(before, after) {
		t.Error("a refused upload changed the stored photo")
	}
}

func TestAWorkerWithNoCurrentPermitIsToldSo(t *testing.T) {
	e := newEnv(t)
	tok := e.photoToken(t)
	code, body := e.photoPost(t, tok, bigJPEG(t))
	if code != 404 || !strings.Contains(body, "no current permit") {
		t.Fatalf("status %d: %s", code, alertText(body))
	}
	if entries, _ := os.ReadDir(e.uploads); len(entries) != 0 {
		t.Fatal("files were written for a worker with no permit")
	}
}

func TestPhotoUploadsAreRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	small := []byte("not a picture")
	for i := 0; i < 10; i++ {
		if code, _ := e.photoPost(t, tok, small); code == 429 {
			t.Fatalf("limited too early at try %d", i+1)
		}
	}
	if code, _ := e.photoPost(t, tok, small); code != 429 {
		t.Fatalf("the 11th try got %d, want 429", code)
	}
}

func TestAPhotoOverTheInputLimitIsRefusedWithoutBeingDecoded(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	huge := append([]byte("\xff\xd8\xff\xe0"), make([]byte, 9<<20)...)
	if code, body := e.photoPost(t, tok, huge); code != 413 || !strings.Contains(body, "too large") {
		t.Fatalf("status %d: %s", code, alertText(body))
	}
	if got := e.idPhoto(t, "WEP-AAAAA"); got != "" {
		t.Fatalf("permit changed: %q", got)
	}
}

func TestResultPageShowsTheMedicalCheckupAndEachPermitWithItsLinks(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	e.seedPermit(t, "WEP-BBBBB", "PendingStage1", "", "PT Maju", "2027-01-31", "2027-01-31")
	e.seedPermit(t, "WEP-CCCCC", "Expired", "LEP-CCCCC", "PT Maju", "2025-01-31", "2025-01-31")
	e.workers.people = []worker.Person{budi}
	_, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	for _, want := range []string{
		"Medical checkup", "Fit", "2027-01-31",
		"LEP-AAAAA", "Valid", `href="/c/LEP-AAAAA"`, `href="/ticket?lep=LEP-AAAAA"`,
		"Waiting for review",
		"LEP-CCCCC", "Expired",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("result page lacks %q", want)
		}
	}
	if n := strings.Count(body, `action="/my/permit.pdf"`); n != 2 {
		t.Errorf("%d download buttons, want 2 (every permit with a number)", n)
	}
	// The sidebar option is the action (owner request 2026-11-08): one link to the newest Valid permit's
	// schedule, no second "Propose a working ticket" button inside the page.
	if n := strings.Count(body, `href="/ticket?lep=`); n != 1 {
		t.Errorf("%d working ticket links, want 1 (only the Valid permit)", n)
	}
	if strings.Contains(body, `action="/my/ticket"`) || strings.Contains(body, "Propose a working ticket") {
		t.Error("a duplicate working ticket button is still on the page")
	}
	if strings.Contains(body, "WEP-") {
		t.Error("a request id is shown")
	}
}

func TestResultPageSaysSoWhenThereIsNoPermitAndNoValidCheckup(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{{Name: "Budi Santoso", DOB: "1988-03-05", Company: "PT Maju", Position: "Welder"}}
	_, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	for _, want := range []string{"No permit yet", `href="/permit"`} {
		if !strings.Contains(body, want) {
			t.Errorf("result page lacks %q", want)
		}
	}
}

func (e *testEnv) myTicket(t *testing.T, token, lep string) (int, string) {
	t.Helper()
	return e.postBody(t, "/my/ticket", nil, url.Values{"token": {token}, "lep": {lep}})
}

func TestTheTicketPageOpensStraightFromTheResultPageWithoutAskingForTheBirthDateAgain(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	code, body := e.myTicket(t, tok, "LEP-AAAAA")
	if code != 200 {
		t.Fatalf("status %d: %s", code, alertText(body))
	}
	for _, want := range []string{"Budi Santoso", `action="/ticket/propose"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the opened ticket page lacks %q", want)
		}
	}
	if strings.Contains(body, `name="dob"`) {
		t.Error("the page asks for the birth date again")
	}
}

func TestMyTicketRefusesAnotherWorkersPermitAnExpiredOneAndABadToken(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	e.seedPermit(t, "WEP-CCCCC", "Expired", "LEP-CCCCC", "PT Maju", "2025-01-31", "2025-01-31")
	if _, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, requester_name, requester_email, work_location, lep_valid_until, created_at)
		VALUES ('WEP-DDDDD', 'LEP-DDDDD', 'Approved', 99, 'Other Person', 'PT X', 'R', 'r@example.com', 'Onshore', '2027-01-31', '2026-06-01 08:00:00')`); err != nil {
		t.Fatal(err)
	}
	tok := e.photoToken(t)
	cases := map[string][2]string{
		"someone else's permit": {tok, "LEP-DDDDD"},
		"expired permit":        {tok, "LEP-CCCCC"},
		"unknown number":        {tok, "LEP-ZZZZZ"},
		"bad shape":             {tok, "../etc"},
		"bad token":             {"abc.def", "LEP-AAAAA"},
	}
	for name, c := range cases {
		code, body := e.myTicket(t, c[0], c[1])
		if code != 400 && code != 404 {
			t.Errorf("%s: status %d", name, code)
		}
		if strings.Contains(body, `action="/ticket/propose"`) || strings.Contains(body, "Other Person") {
			t.Errorf("%s: the ticket page opened", name)
		}
	}
}

func TestMyTicketIsRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	tok := e.photoToken(t)
	for i := 0; i < ticketTriesPerIP; i++ {
		if code, _ := e.myTicket(t, tok, "LEP-ZZZZZ"); code == 429 {
			t.Fatalf("limited too early at try %d", i+1)
		}
	}
	if code, _ := e.myTicket(t, tok, "LEP-ZZZZZ"); code != 429 {
		t.Fatalf("got %d, want 429", code)
	}
}

func (e *testEnv) myPDF(t *testing.T, token, lep string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/my/permit.pdf", strings.NewReader(url.Values{"token": {token}, "lep": {lep}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestTheWorkerDownloadsTheirPermitAsAPDFWithTheirPhoto(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	tok := e.photoToken(t)
	if code, _ := e.photoPost(t, tok, bigJPEG(t)); code != 200 {
		t.Fatal("photo setup failed")
	}
	resp, body := e.myPDF(t, tok, "LEP-AAAAA")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("status %d type %q: %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "LEP-AAAAA.pdf") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the file may be cached or sniffed")
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("/Subtype /Image")) {
		t.Error("not a PDF with the photo in it")
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'permit.download'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows = %d, err = %v", n, err)
	}
}

func TestAnExpiredPermitCanStillBeDownloadedAndSaysSo(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-CCCCC", "Expired", "LEP-CCCCC", "PT Maju", "2025-01-31", "2025-01-31")
	resp, body := e.myPDF(t, e.photoToken(t), "LEP-CCCCC")
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPDFDownloadRefusesAnotherWorkersPermitABadTokenAndAWaitingPermit(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	if _, err := e.db.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, requester_name, requester_email, work_location, lep_valid_until, created_at)
		VALUES ('WEP-DDDDD', 'LEP-DDDDD', 'Approved', 99, 'Other Person', 'PT X', 'R', 'r@example.com', 'Onshore', '2027-01-31', '2026-06-01 08:00:00')`); err != nil {
		t.Fatal(err)
	}
	tok := e.photoToken(t)
	for name, c := range map[string][2]string{
		"someone else's": {tok, "LEP-DDDDD"}, "unknown": {tok, "LEP-ZZZZZ"}, "bad shape": {tok, "../x"}, "bad token": {"abc.def", "LEP-AAAAA"}, "blank": {tok, ""},
	} {
		resp, body := e.myPDF(t, c[0], c[1])
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
		if bytes.HasPrefix(body, []byte("%PDF")) || bytes.Contains(body, []byte("Other Person")) {
			t.Errorf("%s: a file or a name was returned", name)
		}
	}
}

func TestPDFDownloadIsRateLimitedPerAddress(t *testing.T) {
	e := newEnv(t)
	tok := e.photoToken(t)
	for i := 0; i < pdfTriesPerHour; i++ {
		if resp, _ := e.myPDF(t, tok, "LEP-ZZZZZ"); resp.StatusCode == 429 {
			t.Fatalf("limited too early at try %d", i+1)
		}
	}
	if resp, _ := e.myPDF(t, tok, "LEP-ZZZZZ"); resp.StatusCode != 429 {
		t.Fatalf("got %d, want 429", resp.StatusCode)
	}
}

func (e *testEnv) lookupAs(t *testing.T, emp worker.Employee) (int, string) {
	t.Helper()
	e.workers.people = []worker.Person{budi}
	e.workers.employees = map[string]worker.Employee{"Budi Santoso": emp}
	return e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
}

func budiRecord(result, note, until string) worker.Employee {
	return worker.Employee{PatientID: 1, Name: "Budi Santoso", DOB: "1988-03-05", Nationality: "Indonesia", Company: "PT Maju", Position: "Welder",
		McuResult: result, McuNote: note, McuValidUntil: until}
}

func TestTheWorkerSeesTheirOwnMedicalNote(t *testing.T) {
	e := newEnv(t)
	code, body := e.lookupAs(t, budiRecord("Fit with note", "Wear corrective lenses while working", "2027-01-31"))
	if code != 200 || !strings.Contains(body, "Medical note") || !strings.Contains(body, "Wear corrective lenses while working") {
		t.Fatalf("status %d: the note is not on the page", code)
	}
}

func TestTheNoteIsEscapedAndAbsentWhenThereIsNone(t *testing.T) {
	e := newEnv(t)
	_, body := e.lookupAs(t, budiRecord("Fit with note", "<script>alert(1)</script>", "2027-01-31"))
	if strings.Contains(body, "<script>alert(1)") {
		t.Error("the note was not escaped")
	}
	_, body = e.lookupAs(t, budiRecord("Fit", "", "2027-01-31"))
	if strings.Contains(body, "Medical note") {
		t.Error("an empty note shows a heading")
	}
}

func TestAWorkerWhoseMedicalCheckupHasExpiredCannotOpenTheirPage(t *testing.T) {
	e := newEnv(t) // the clock says 2026-11-06
	for _, until := range []string{"2026-11-05", "2025-01-01"} {
		code, body := e.lookupAs(t, budiRecord("Fit", "", until))
		if code != 403 || !strings.Contains(alertText(body), "Your permit validity has ended") {
			t.Errorf("valid until %s: status %d: %s", until, code, alertText(body))
		}
		if strings.Contains(body, "Budi Santoso</h2>") {
			t.Errorf("valid until %s: the record was shown", until)
		}
	}
	// The last valid day itself still works.
	if code, _ := e.lookupAs(t, budiRecord("Fit", "", "2026-11-06")); code != 200 {
		t.Errorf("on the last valid day: status %d", code)
	}
}

func TestOnlyTheMyPermitLookupAsksForExpiredWorkers(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	if code, _ := e.permitPost(t, nil); code != 200 {
		t.Fatalf("permit form: %d", code)
	}
	if e.workers.expiredAsks != 0 {
		t.Fatal("the request form asked for expired workers: a permit could be requested with no valid checkup")
	}
	if code, _ := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass"); code != 200 {
		t.Fatalf("lookup: %d", code)
	}
	if e.workers.expiredAsks != 1 {
		t.Fatalf("the My permit lookup asked %d times, want 1", e.workers.expiredAsks)
	}
}
