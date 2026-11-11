package mcusubmit

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCerts stands in for the clinical side: it hands out a small file, or refuses.
type fakeCerts struct {
	dir    string
	refuse string
	asked  []int64
}

func (f *fakeCerts) Certificate(_ context.Context, id int64) (CertificateFile, error) {
	f.asked = append(f.asked, id)
	if f.refuse != "" {
		return CertificateFile{}, &CertificateUnavailable{Reason: f.refuse}
	}
	path := filepath.Join(f.dir, "cert.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4 certificate\n%%EOF"), 0o600); err != nil {
		return CertificateFile{}, err
	}
	return CertificateFile{Path: path, Filename: "MedC-1-PT-SitiAminah-20271001.pdf"}, nil
}

// closedDoc makes a document of the signed-in submitter that staff closed against a real record.
func (u *uploadEnv) closedDoc(name string) int64 {
	u.t.Helper()
	id := sentDoc(u.t, u.store, u.userID, name)
	addRecord(u.t, u.store, seedWorker(u.t, u.store, name, "1990-04-12", u.companyID), "MedC-"+itoa(id), "Fit", "2027-01-01", "Open", 1)
	if err := closeWithRecord(u.store, context.Background(), id, testStaffID, "MedC-"+itoa(id)); err != nil {
		u.t.Fatal(err)
	}
	return id
}

func TestSubmitterDownloadsTheCertificateOfTheirOwnClosedDocument(t *testing.T) {
	u := newUploadEnv(t)
	certs := &fakeCerts{dir: t.TempDir()}
	u.api.SetCertificates(certs)
	id := u.closedDoc("Siti Aminah")

	rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/certificate", id), u.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d\n%s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/pdf" || !strings.Contains(h.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(h.Get("Content-Disposition"), "SitiAminah") || h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", h)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Errorf("body is not the PDF: %q", rec.Body.String())
	}
	if len(certs.asked) != 1 {
		t.Errorf("the certificate source was asked %d times, want 1", len(certs.asked))
	}
	if !hasEvent(t, u.store, EntitySubmission, id, "CertificateDownloaded") {
		t.Error("the download was not logged")
	}
	// The documents list offers the button on this row.
	if body := u.get("/mcu-submit/documents", u.cookie).Body.String(); !strings.Contains(body, fmt.Sprintf("/mcu-submit/documents/%d/certificate", id)) {
		t.Error("the documents list has no Download certificate link on a Closed row")
	}
}

func TestCertificateIsOnlyForTheOwnersClosedDocumentWithARecord(t *testing.T) {
	u := newUploadEnv(t)
	u.api.SetCertificates(&fakeCerts{dir: t.TempDir()})
	closed := u.closedDoc("Siti Aminah")
	pending := sentDoc(t, u.store, u.userID, "Budi Hartono")
	// Closed, but linked to nothing: the page must not even offer it.
	noRecord := sentDoc(t, u.store, u.userID, "Joko Susilo")
	if _, err := u.store.DB.Exec(`UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ?`, SubClosed, noRecord); err != nil {
		t.Fatal(err)
	}
	other := u.env.onboard("hr2@example.com", "hr.two", "a-long-passphrase-2")
	_, otherCookie := u.env.signIn("hr.two", "a-long-passphrase-2")
	_ = other
	// Colleagues of one company share documents now (owner, 2026-11-05), so "somebody else" must
	// belong to another company.
	if _, err := u.store.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Elsewhere')`); err != nil {
		t.Fatal(err)
	}
	if _, err := u.store.DB.Exec(`UPDATE Submitter SET FK_CompanyID = (SELECT CompanyID FROM Company WHERE Name = 'PT Elsewhere') WHERE SubmitterID = ?`, other); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		id     int64
		cookie *http.Cookie
	}{
		"a Pending document":       {pending, u.cookie},
		"a Closed one, no record":  {noRecord, u.cookie},
		"somebody else's document": {closed, otherCookie},
		"a missing document":       {99999, u.cookie},
	} {
		if rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/certificate", c.id), c.cookie); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", name, rec.Code)
		}
	}
	if rec := u.get(fmt.Sprintf("/mcu-submit/documents/%d/certificate", closed)); rec.Code == http.StatusOK {
		t.Error("an anonymous caller got a certificate")
	}
	if body := u.get("/mcu-submit/documents", u.cookie).Body.String(); strings.Contains(body, fmt.Sprintf("documents/%d/certificate", pending)) || strings.Contains(body, fmt.Sprintf("documents/%d/certificate", noRecord)) {
		t.Error("the list offers a certificate where none can exist")
	}
}

func TestARefusedCertificateIsExplainedAndNoSourceMeansNoRoute(t *testing.T) {
	u := newUploadEnv(t)
	id := u.closedDoc("Siti Aminah")
	path := fmt.Sprintf("/mcu-submit/documents/%d/certificate", id)

	if rec := u.get(path, u.cookie); rec.Code != http.StatusNotFound {
		t.Errorf("no certificate source = %d, want 404", rec.Code)
	}
	u.api.SetCertificates(&fakeCerts{dir: t.TempDir(), refuse: "certificate is only available for Fit and Fit with note"})
	rec := u.get(path, u.cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "only available for Fit and Fit with note") {
		t.Fatalf("refused = %d\n%s", rec.Code, rec.Body)
	}
	if hasEvent(t, u.store, EntitySubmission, id, "CertificateDownloaded") {
		t.Error("a refused certificate was logged as downloaded")
	}
}

func TestCertificatesZipHoldsEveryClosedCertificateInTheScopeAndNothingElse(t *testing.T) {
	u := newUploadEnv(t)
	u.api.SetCertificates(&fakeCerts{dir: t.TempDir()})
	one := u.closedDoc("Siti Aminah")
	two := u.closedDoc("Budi Hartono")
	sentDoc(t, u.store, u.userID, "Still Pending")

	rec := u.get("/mcu-submit/documents/certificates.zip?status=Closed", u.cookie)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("= %d %v", rec.Code, rec.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 || !strings.HasPrefix(zr.File[0].Name, itoa(two)+"-") && !strings.HasPrefix(zr.File[0].Name, itoa(one)+"-") {
		t.Errorf("zip holds %d files: %v", len(zr.File), zr.File)
	}
	if !hasEvent(t, u.store, EntitySubmission, one, "CertificateDownloaded") {
		t.Error("the zip download was not logged")
	}
	// The list offers the button.
	if body := u.get("/mcu-submit/documents", u.cookie).Body.String(); !strings.Contains(body, "certificates.zip") {
		t.Error("no Download all certificates link on the documents page")
	}
	// Signed out: no zip.
	if rec := u.get("/mcu-submit/documents/certificates.zip"); rec.Code == http.StatusOK {
		t.Error("the zip was served without signing in")
	}
}

// reviewedDoc makes a document of the signed-in submitter that staff reviewed against a real record
// with this result, but that is not Closed yet: the submitter has not had the 12-hourly notice.
func (u *uploadEnv) reviewedDoc(name, result string) int64 {
	u.t.Helper()
	id := sentDoc(u.t, u.store, u.userID, name)
	addRecord(u.t, u.store, seedWorker(u.t, u.store, name, "1990-04-12", u.companyID), "MedC-"+itoa(id), result, "2027-01-01", "Open", 1)
	if err := u.store.LinkRecord(context.Background(), id, testStaffID, "MedC-"+itoa(id)); err != nil {
		u.t.Fatal(err)
	}
	return id
}

// Owner, 2026-11-12: a worker who is eligible (Fit or Fit with note) gets the certificate on the
// submitter's page as soon as the result is saved, without waiting for the notice mail to close it.
func TestCertificateIsOfferedAsSoonAsTheResultIsEligibleEvenBeforeTheDocumentCloses(t *testing.T) {
	u := newUploadEnv(t)
	u.api.SetCertificates(&fakeCerts{dir: t.TempDir()})
	fit := u.reviewedDoc("Siti Aminah", "Fit")
	note := u.reviewedDoc("Budi Hartono", "FitWithNote")
	unfit := u.reviewedDoc("Joko Susilo", "Unfit")
	followUp := u.reviewedDoc("Agus Salim", "NeedFollowUp")

	list := u.get("/mcu-submit/documents", u.cookie).Body.String()
	for name, c := range map[string]struct {
		id   int64
		want bool
	}{"Fit": {fit, true}, "Fit with note": {note, true}, "Unfit": {unfit, false}, "Needs follow-up": {followUp, false}} {
		path := fmt.Sprintf("/mcu-submit/documents/%d/certificate", c.id)
		if got := strings.Contains(list, path); got != c.want {
			t.Errorf("%s: list offers the certificate = %v, want %v", name, got, c.want)
		}
		wantCode := http.StatusNotFound
		if c.want {
			wantCode = http.StatusOK
		}
		if rec := u.get(path, u.cookie); rec.Code != wantCode {
			t.Errorf("%s: download = %d, want %d", name, rec.Code, wantCode)
		}
	}
}
