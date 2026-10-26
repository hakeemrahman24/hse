package entrymcu

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertificateOpenFilename(t *testing.T) {
	got := certificateOpenFilename("MedC0011", "HSE", "John Doe", "2027-03-15")
	if got != "MedC0011-HSE-JohnDoe-20270315.pdf" {
		t.Fatalf("filename = %q", got)
	}
	titled := certificateOpenFilename("MedC0007", "SITE", "dr. Edric Sugito, MKKK", "2026-10-01")
	if titled != "MedC0007-SITE-drEdricSugitoMKKK-20261001.pdf" {
		t.Fatalf("titled filename = %q", titled)
	}
	blank := certificateOpenFilename("  ", "", "  ", "")
	if blank != "checkup-NA-Unknown-nodate.pdf" {
		t.Fatalf("blank filename = %q", blank)
	}
}

func TestFitnessCertificateWording(t *testing.T) {
	d := certData{
		CheckUpID:      "MedC0011",
		Name:           "Siti Aminah",
		DateOfBirth:    "1992-04-05",
		CompanyName:    "Acme Contractors",
		WorkPosition:   "Rigger",
		ResultCriteria: "FitWithNote",
		ResultNotes:    "",
		ValidUntil:     "2027-03-01",
		ReviewDate:     "2026-03-02",
		DateCheckUp:    "2026-03-01",
		AuthorityName:  "dr. Lina Kusuma",
	}
	cfg := CertConfig{
		Secret:   "test-secret",
		BaseURL:  "http://medpost.test",
		LogoPath: "../../data/uploads/_MedpostWide/common/logo.png",
	}
	pdfBytes, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfPlainText(pdfBytes)
	for _, want := range []string{
		"FITNESS-TO-WORK RECOMMENDATION CERTIFICATE",
		"Certificate ID:",
		"MedC0011",
		"PT LAYAR NUSANTARA GAS",
		"A member of the Genting Group | Incorporated in Indonesia",
		"01 March 2026",
		"Siti Aminah",
		"05 April 1992",
		"Acme Contractors",
		"Rigger",
		"Fit with Recommendations / Notes",
		"None",
		"01 March 2027",
		"internal activities of PT LNG",
		"scanning the QR code",
		"Developed by dr. Arif Rahman Hakim",
		"Issued under authority of",
		"Monday, 02 March 2026",
		"dr. Lina Kusuma",
		"Chief Medical Officer",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pdf missing %q", want)
		}
	}
	if strings.Contains(text, "on behalf of") || strings.Contains(text, "Document No.") || strings.Contains(text, "Issued under the authority of") {
		t.Fatalf("unexpected wording in %s", text)
	}
	d.Reviewer = "dr. Sari Wulandari"
	signed, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	signedText := pdfPlainText(signed)
	if strings.Contains(signedText, "dr. Sari Wulandari") {
		t.Fatal("certificate printed the reviewer")
	}
	if !strings.Contains(signedText, "Issued under authority of") || !strings.Contains(signedText, "dr. Lina Kusuma") || !strings.Contains(signedText, "Chief Medical Officer") {
		t.Fatal("another doctor's certificate should name the chief medical officer")
	}
	d.Reviewer = "dr. Lina Kusuma"
	own, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ownText := pdfPlainText(own)
	if !strings.Contains(ownText, "dr. Lina Kusuma") || !strings.Contains(ownText, "Chief Medical Officer") {
		t.Fatal("the chief medical officer's own review should show name and position")
	}
	if strings.Contains(ownText, "Issued under authority of") {
		t.Fatal("the chief medical officer's own review should not use the authority line")
	}
}

func TestCertificateQRUsesFaviconAndVerifyURL(t *testing.T) {
	seal := generateMasterSeal("Siti Aminah", "Acme Contractors", "FitWithNote", "2027-03-01", "test-secret")
	link := certificateVerifyURL("http://medpost.test/", "MedC0011", seal)
	if link != "http://medpost.test/verification/MedC0011/"+seal {
		t.Fatalf("url = %s", link)
	}
	local := certificateVerifyURL("http://127.0.0.1:8092", "MedC0011", seal)
	if local != "https://medical-activities.ayahaif.my.id/verification/MedC0011/"+seal {
		t.Fatalf("loopback url = %s", local)
	}
	pngBytes, err := certificateQRCode(link, "../../web/static/favicon.png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	cx, cy := b.Dx()/2, b.Dy()/2
	navy := 0
	for y := cy - 60; y <= cy+60; y++ {
		for x := cx - 60; x <= cx+60; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy < 28*28 || dx*dx+dy*dy > 58*58 {
				continue
			}
			r, g, bl, _ := img.At(x, y).RGBA()
			if bl > r+4000 && bl > g+2000 && bl > 8000 {
				navy++
			}
		}
	}
	if navy == 0 {
		t.Fatal("favicon badge is not drawn in the middle of the QR code")
	}
	if _, err := exec.LookPath("zbarimg"); err != nil {
		return
	}
	path := filepath.Join(t.TempDir(), "qr.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("zbarimg", "-q", "--raw", path).Output()
	if err != nil {
		t.Fatalf("qr did not scan: %v", err)
	}
	if strings.TrimSpace(string(out)) != link {
		t.Fatalf("scanned %q", out)
	}
}

func TestFormatCertDateAndFitnessLabel(t *testing.T) {
	if got := formatCertDate("2026-10-03"); got != "03 October 2026" {
		t.Fatalf("date = %q", got)
	}
	if got := formatCertDate(""); got != "" {
		t.Fatalf("blank date = %q", got)
	}
	if notesOrNone("  ") != "None" || notesOrNone("No night shift") != "No night shift" {
		t.Fatal("notes")
	}
	if fitnessStatusLabel("TemporaryUnfit") != "Temporarily Unfit" || fitnessStatusLabel("Fit") != "Fit" || fitnessStatusLabel("Unfit") != "Unfit" {
		t.Fatal("fitness labels")
	}
}

func TestAdverseCertificateIsRedOpinion(t *testing.T) {
	d := certData{
		CheckUpID:      "MedC0099",
		Name:           "Budi Santoso",
		ResultCriteria: "TemporaryUnfit",
		ResultNotes:    "Blood pressure above the work limit",
		CheckUpStatus:  "Open",
		ReviewDate:     "2026-03-02",
		AuthorityName:  "dr. Lina Kusuma",
	}
	cfg := CertConfig{Secret: "test-secret", BaseURL: "http://medpost.test", LogoPath: "../../data/uploads/_MedpostWide/common/logo.png"}
	pdfBytes, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfPlainText(pdfBytes)
	for _, want := range []string{"Temporarily Unfit", "Blood pressure above the work limit", "not a clearance to work", "temporarily unfit"} {
		if !strings.Contains(text, want) {
			t.Errorf("pdf missing %q", want)
		}
	}
	if strings.Contains(text, "This certificate is valid until") {
		t.Fatal("temporary unfit certificate claimed a validity date")
	}
	if !strings.Contains(text, "0.706 0.000 0.000 rg") {
		t.Fatal("temporary unfit certificate text is not red")
	}
	view := verifyViewFrom(d, time.Now())
	if view.Tone != "unfit" || !strings.Contains(view.Banner, "not a clearance to work") {
		t.Fatalf("verify view = %+v", view)
	}
}

func TestVerifyPageShowsSummaryNotMedicalDetails(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Company (Name, Abbreviation) VALUES ('Acme Contractors', 'ACME')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO CheckUpCriteriaTable (Name) VALUES ('Annual')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, FK_CompanyID, WorkPosition) VALUES ('Siti Aminah', '1992-04-05', 1, 'Rigger')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES ('dr. Retired', 'Chief Medical Officer', 'NotActive'), ('dr. Lina Kusuma', 'Chief Medical Officer', 'Active')`); err != nil {
		t.Fatal(err)
	}
	const medical = "SECRET-HIV-RESULT-XYZZY"
	if _, err := database.Exec(`
		INSERT INTO McuRecord (
			FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer, FK_CheckUpCriteriaID,
			WorkPosition, DateCheckUp, ResultCriteria, ResultValidUntil, ResultNotes,
			CheckUpStatus, HivCriteria, BpNotes, Recommendation
		) VALUES (1, '2026-03-02T00:00:00', '2026-03-02', 'MedC0011', 'dr. Hidden Reviewer', 1,
			'Rigger', '2026-03-01', 'FitWithNote', '2027-03-01', 'No work at height',
			'Close', ?, 'SECRET-BP-NOTE-XYZZY', 'SECRET-RECOMMEND-XYZZY')`, medical); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database}
	cfg := CertConfig{Secret: "test-secret", BaseURL: "http://medpost.test"}
	d, ok := api.fetchCertDataByCheckUpID("MedC0011")
	if !ok {
		t.Fatal("checkup not loaded")
	}
	if d.AuthorityName != "dr. Lina Kusuma" {
		t.Fatalf("authority = %q", d.AuthorityName)
	}
	seal := generateMasterSeal(d.Name, d.CompanyName, d.ResultCriteria, d.ValidUntil, cfg.Secret)
	handler := api.VerifyCertificate(cfg)

	okRec := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodGet, "/verification/MedC0011/"+seal, nil)
	okReq.SetPathValue("checkUpID", "MedC0011")
	okReq.SetPathValue("seal", seal)
	handler(okRec, okReq)
	body := okRec.Body.String()
	if okRec.Code != http.StatusOK {
		t.Fatalf("status %d", okRec.Code)
	}
	for _, want := range []string{"Siti Aminah", "Fit with Recommendations / Notes", "No work at height", "Annual", "dr. Lina Kusuma", "dr. Hidden Reviewer", "01 March 2026", "does not show medical examination details", `src="/static/logo.png"`, "Developed by dr. Arif Rahman Hakim"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, hidden := range []string{medical, "SECRET-BP-NOTE-XYZZY", "SECRET-RECOMMEND-XYZZY", "dr. Retired"} {
		if strings.Contains(body, hidden) {
			t.Errorf("page leaked %q", hidden)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("unescaped markup")
	}

	bad := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodGet, "/verification/MedC0011/WRONGSEAL1", nil)
	badReq.SetPathValue("checkUpID", "MedC0011")
	badReq.SetPathValue("seal", "WRONGSEAL1")
	handler(bad, badReq)
	if strings.Contains(bad.Body.String(), "Siti Aminah") || strings.Contains(bad.Body.String(), medical) {
		t.Fatal("wrong seal revealed the checkup")
	}
	if !strings.Contains(bad.Body.String(), "could not be verified") {
		t.Fatal("wrong seal should say the certificate could not be verified")
	}

	view := verifyViewFrom(d, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC))
	if view.Tone != "ok" {
		t.Fatalf("tone = %s (%s)", view.Tone, view.Banner)
	}
}

func TestCertificateContentDispositionOpensInline(t *testing.T) {
	got := certificateContentDisposition("MedC0011-HSE-JohnDoe-20270315.pdf")
	if !strings.HasPrefix(got, "inline;") {
		t.Fatalf("disposition = %q", got)
	}
	if !strings.Contains(got, `filename="MedC0011-HSE-JohnDoe-20270315.pdf"`) {
		t.Fatalf("disposition = %q", got)
	}
}
