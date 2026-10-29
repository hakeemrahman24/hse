// Certificate PDF generation — ports Code.js's processUnifiedIDs (the
// "Certificate Dispatcher" sidebar feature) and generateMasterSeal. The
// original converts an HTML template (CertTemplate.html) to PDF via Apps
// Script's built-in Chromium renderer and a remote QR-code API; neither
// exists in Go, so this draws the same content directly with a pure-Go PDF
// library (github.com/go-pdf/fpdf) and a local QR encoder
// (github.com/skip2/go-qrcode) — no external binary or network dependency,
// which matters on the orangepi3b's ARM CPU. Layout mirrors
// CertTemplate.html's regions (navy header rule, logo top-left, cert-id
// line, underlined title, paragraph, key-value table, signature block
// bottom-right, QR + tiny gray seal fingerprint bottom-left) rather than
// reusing its CSS, since fpdf draws coordinates, not HTML.
package entrymcu

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"hse-medpost/internal/dates"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"hse-medpost/internal/middleware"
	"hse-medpost/internal/staff"
	"hse-medpost/internal/storage"
)

type CertConfig struct {
	Secret        string
	LogoPath      string
	SignaturePath string
	UploadDir     string // where generated certificate PDFs are persisted (storage.McuCertificateDir)
	SigningDir    string // doctor and CMO .p12 files, used to sign each new certificate
	BaseURL       string // public origin encoded in the certificate QR code
}

// certData is what one certificate needs, resolved from McuRecord + Patient
// + Company + CheckUpCriteriaTable + TaskCategoryTable — the same fields
// processUnifiedIDs pulled off the flat sheet row, now via joins instead of
// column indexes.
type certData struct {
	CheckUpID        string
	Name             string
	DateOfBirth      string
	CompanyName      string
	CompanyAbbrev    string
	WorkPosition     string
	ResultCriteria   string
	TaskCategory     string
	ResultNotes      string
	ValidUntil       string
	Reviewer         string
	CheckUpStatus    string
	HcValidityStatus string // raw stored value — displayHcValidityStatus resolves the live ladder from this, not this field directly
	ReviewDate       string
	DateCheckUp      string
	CheckUpPurpose   string
	AuthorityName    string // active Chief Medical Officer at the time this data is loaded

	PatientID    int64  // for McuCertificateLog.FK_PatientID
	CertFilePath string // McuRecord.HealthCertificateFileName — "" until first generated, or after an edit invalidates it
}

var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]`)
var nonDigitRe = regexp.MustCompile(`\D`)

// generateMasterSeal ports Code.js's function of the same name exactly:
// clean(name)+clean(company)+clean(result)+datePart+secret, MD5'd,
// base64'd, first 10 chars, non-alnum replaced with 'Z', uppercased.
func generateMasterSeal(name, company, result, validUntil, secret string) string {
	clean := func(s string) string {
		return nonAlnumRe.ReplaceAllString(strings.ToLower(s), "")
	}
	datePart := nonDigitRe.ReplaceAllString(validUntil, "")
	if datePart == "" {
		datePart = "NODATE"
	}
	raw := clean(name) + clean(company) + clean(result) + datePart + secret
	sum := md5.Sum([]byte(raw))
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	if len(b64) > 10 {
		b64 = b64[:10]
	}
	seal := nonAlnumRe.ReplaceAllString(strings.ToLower(b64), "Z")
	return strings.ToUpper(seal)
}

func (a *API) fetchCertData(mcuRecordID int64) (certData, bool) {
	return a.queryCertData(`WHERE m.McuRecordID = ?`, mcuRecordID)
}

func (a *API) fetchCertDataByCheckUpID(checkUpID string) (certData, bool) {
	return a.queryCertData(`WHERE m.CheckUpID = ?`, strings.TrimSpace(checkUpID))
}

// queryCertData loads the certificate summary. Clinical exam columns
// (labs, imaging, vitals, serology) are intentionally not selected: the
// public verification page is built only from this struct.
func (a *API) queryCertData(where string, arg any) (certData, bool) {
	var d certData
	err := a.DB.QueryRow(`
		SELECT m.CheckUpID, p.PatientID, p.Name, COALESCE(p.DateOfBirth,''), COALESCE(co.Name,''), COALESCE(co.Abbreviation,''),
		       COALESCE(m.WorkPosition, p.WorkPosition, ''), COALESCE(m.ResultCriteria,''),
		       COALESCE(m.TaskCategoryNames,''), COALESCE(m.ResultNotes,''), COALESCE(m.ResultValidUntil,''),
		       COALESCE(m.Reviewer,''), COALESCE(m.CheckUpStatus,''), COALESCE(m.HealthCertificateFileName,''),
		       COALESCE(m.HcValidityStatus,''), COALESCE(m.ReviewDate,''),
		       COALESCE(m.DateCheckUp,''), COALESCE(cr.Name,'')
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		LEFT JOIN Company co ON co.CompanyID = COALESCE(m.FK_CompanyID, p.FK_CompanyID)
		LEFT JOIN CheckUpCriteriaTable cr ON cr.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		`+where, arg).Scan(
		&d.CheckUpID, &d.PatientID, &d.Name, &d.DateOfBirth, &d.CompanyName, &d.CompanyAbbrev,
		&d.WorkPosition, &d.ResultCriteria, &d.TaskCategory, &d.ResultNotes, &d.ValidUntil,
		&d.Reviewer, &d.CheckUpStatus, &d.CertFilePath, &d.HcValidityStatus, &d.ReviewDate,
		&d.DateCheckUp, &d.CheckUpPurpose,
	)
	if err != nil {
		return certData{}, false
	}
	d.AuthorityName = a.activeChiefMedicalOfficer()
	return d, true
}

// activeChiefMedicalOfficer is the staff member who currently holds that
// position and is Active. When more than one row matches, the earliest
// StaffID is used so the name does not change between requests.
func (a *API) activeChiefMedicalOfficer() string {
	var name string
	err := a.DB.QueryRow(`
		SELECT StaffName FROM MedpostStaffTable
		WHERE Status = 'Active' AND lower(trim(Position)) = 'chief medical officer'
		ORDER BY StaffID
		LIMIT 1`).Scan(&name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// logCertificateAction is deliberately best-effort: the file on disk and
// McuRecord.HealthCertificateFileName are the source of truth for "does a
// certificate exist", not this log, so a logging failure shouldn't turn a
// successful generate/download into a user-facing error.
func (a *API) logCertificateAction(mcuRecordID, patientID int64, staffID int64, action, filePath string) {
	// 0 means "not a staff member" (a submitter on the portal): NULL, not a staff id that does not exist.
	var staff any
	if staffID != 0 {
		staff = staffID
	}
	_, _ = a.DB.Exec(
		`INSERT INTO McuCertificateLog (FK_McuRecordID, FK_PatientID, FK_StaffID, Action, FilePath, CreatedAt)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		mcuRecordID, patientID, staff, action, filePath, time.Now().Format("2006-01-02 15:04:05"),
	)
}

// InvalidateCertificatesForPatient clears every one of this patient's
// McuRecord rows back to "no saved certificate" — wired (see main.go) as a
// callback off internal/patient's UpdatePatient, since a corrected Name/DOB/
// Company/WorkPosition is embedded in any certificate already generated for
// that patient, not just the McuRecord being edited directly.
func (a *API) InvalidateCertificatesForPatient(patientID int64) error {
	_, err := a.DB.Exec(`UPDATE McuRecord SET HealthCertificateFileName = NULL WHERE FK_PatientID = ?`, patientID)
	return err
}

// ClearCachedCertificates deletes every health-certificate PDF saved on
// this server and forgets the stored path, so the next View builds a new
// file. Files outside the certificate folder are left on disk.
func (a *API) ClearCachedCertificates(cfg CertConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		removed, err := a.clearCachedCertificates(cfg.UploadDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not clear certificates"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"cleared": removed})
	}
}

func (a *API) clearCachedCertificates(uploadDir string) (int, error) {
	dir := storage.McuCertificateDir(uploadDir)
	removed, err := removeCertificateFiles(dir)
	if err != nil {
		return removed, err
	}
	if _, err := a.DB.Exec(`UPDATE McuRecord SET HealthCertificateFileName = NULL WHERE HealthCertificateFileName IS NOT NULL`); err != nil {
		return removed, err
	}
	return removed, nil
}

func removeCertificateFiles(dir string) (int, error) {
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !certificateInsideDir(dir, path) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func certificateInsideDir(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// GenerateCertificatePDF is implemented in certificate_layout.go.

// brandImage uses an explicit path when one is set (MCU_CERT_LOGO_PATH /
// MCU_CERT_SIGNATURE_PATH). Otherwise it looks in CommonDir for logo.* or
// signature.*. A missing file yields "" and the certificate omits that image.
func brandImage(explicit, uploadDir, name string) string {
	path := explicit
	if path == "" {
		found, ok := storage.FindCommonFile(uploadDir, name)
		if !ok {
			return ""
		}
		path = found
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// DownloadCertificate serves the generated PDF for one closed record — a
// per-record "Download certificate" action rather than the legacy sidebar's
// paste-an-ID-range batch generator (there's no spreadsheet UI here to
// select a range from). Same underlying capability — generate a cert for
// any closed record — exposed one at a time from the record's own detail
// view instead of a separate batch tool.
func (a *API) DownloadCertificate(cfg CertConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		var staffID int64
		if sess := middleware.Session(r); sess != nil {
			staffID = sess.StaffID
		}
		path, filename, err := a.CertificateFile(id, staffID, cfg)
		if err != nil {
			var ce *CertificateError
			if errors.As(err, &ce) {
				writeJSON(w, ce.Status, map[string]string{"error": ce.Message})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "pdf generation failed"})
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", certificateContentDisposition(filename))
		http.ServeFile(w, r, path)
	}
}

// CertificateError is a reason a certificate cannot be given. Status is the HTTP status the staff
// route answers with; Message is safe to show to the person asking.
type CertificateError struct {
	Status  int
	Message string
}

func (e *CertificateError) Error() string { return e.Message }

// CertificateFile makes sure the saved, signed certificate PDF for one record exists and returns
// where it is and the name to offer it under. staffID is who asked (0 when it is not a staff
// member, as for a submitter on the portal); it only goes into the certificate log. The rules are
// the same for everyone: a refusal is a *CertificateError, anything else is a server fault.
func (a *API) CertificateFile(id, staffID int64, cfg CertConfig) (path, filename string, err error) {
	d, ok := a.fetchCertData(id)
	if !ok {
		return "", "", &CertificateError{http.StatusNotFound, "not found"}
	}
	if adverseResult(d.ResultCriteria) {
		// Red written opinion. Open (Temporarily Unfit) and Archived /
		// Exit (Unfit) both still need a document the company can keep.
	} else if !certifiableResult(d.ResultCriteria) {
		return "", "", &CertificateError{http.StatusBadRequest, "certificate is only available for Fit and Fit with note"}
	} else if d.CheckUpStatus != "Close" {
		return "", "", &CertificateError{http.StatusBadRequest, "certificate is only available for closed records"}
	}

	// A closed record can still be "Archived / Exit" (Unfit, or a
	// closed record gone stale >180 days) or "ReEvaluate / Overdue"
	// (past ResultValidUntil but not yet 180 days stale) — see
	// displayHcValidityStatus, validity.go. Neither should be able to
	// mint a fresh certificate: an archived/exited record has no
	// business getting a new HC, and an overdue one needs a new
	// checkup before it can honestly claim current validity.
	if !adverseResult(d.ResultCriteria) {
		checkUpStatus, resultCriteria, resultValidUntil, storedHcValidity := d.CheckUpStatus, d.ResultCriteria, d.ValidUntil, d.HcValidityStatus
		liveStatus := displayHcValidityStatus(&storedHcValidity, &checkUpStatus, &resultValidUntil, &resultCriteria, dates.ClinicNow())
		if liveStatus == "Archived / Exit" || liveStatus == "ReEvaluate / Overdue" || strings.HasPrefix(liveStatus, "Renewed by ") {
			return "", "", &CertificateError{http.StatusBadRequest, "certificate is not available for records marked " + liveStatus}
		}
	}

	filename = certificateOpenFilename(d.CheckUpID, d.CompanyAbbrev, d.Name, d.ValidUntil)

	// d.CertFilePath is "" the first time, or after UpdateMcuRecord
	// invalidates it on any edit (see mcurecord.go) — so a typo fix
	// always regenerates fresh content on next download instead of
	// silently re-serving a stale cached PDF.
	if d.CertFilePath != "" {
		if _, statErr := os.Stat(d.CertFilePath); statErr == nil {
			a.logCertificateAction(id, d.PatientID, staffID, "downloaded", d.CertFilePath)
			return d.CertFilePath, filename, nil
		}
	}

	pdfBytes, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		return "", "", fmt.Errorf("generate certificate: %w", err)
	}
	pdfBytes, err = a.signCertificatePDF(cfg, d.Reviewer, pdfBytes)
	if errors.Is(err, staff.ErrNoSigningCertificate) {
		return "", "", &CertificateError{http.StatusBadRequest, err.Error()}
	}
	if err != nil {
		return "", "", fmt.Errorf("sign certificate: %w", err)
	}

	dir := storage.McuCertificateDir(cfg.UploadDir)
	savedName := storage.McuCertificateFilename(dates.Today(), d.CheckUpID, d.Name, d.ValidUntil)
	savedPath, err := storage.Save(dir, savedName, bytes.NewReader(pdfBytes))
	if err != nil {
		return "", "", fmt.Errorf("save certificate: %w", err)
	}
	if _, err := a.DB.Exec(`UPDATE McuRecord SET HealthCertificateFileName = ? WHERE McuRecordID = ?`, savedPath, id); err != nil {
		return "", "", fmt.Errorf("record certificate path: %w", err)
	}
	a.logCertificateAction(id, d.PatientID, staffID, "generated", savedPath)
	a.logCertificateAction(id, d.PatientID, staffID, "downloaded", savedPath)
	return savedPath, filename, nil
}

// certificateOpenFilename is CheckUpID-CompanyAbbrev-JohnDoe-yyyymmdd.pdf.
// JohnDoe is the patient name with spaces and punctuation removed. The date
// is ResultValidUntil as YYYYMMDD. The MCU data link ends with this same
// name: the browser PDF viewer saves whatever the address ends with.
// web/static/certificate-name.js builds the same string for the link.
func certificateOpenFilename(checkUpID, companyAbbrev, patientName, validUntil string) string {
	id := certificateToken(checkUpID, true)
	if id == "" {
		id = "checkup"
	}
	abbr := certificateToken(companyAbbrev, false)
	if abbr == "" {
		abbr = "NA"
	}
	name := certificateToken(patientName, false)
	if name == "" {
		name = "Unknown"
	}
	date := certificateDigits(validUntil)
	if date == "" {
		date = "nodate"
	}
	return id + "-" + abbr + "-" + name + "-" + date + ".pdf"
}

// certificateToken keeps letters and digits. keepHyphen is for the checkup
// id only, so a hyphen inside that id is not treated as a separator.
func certificateToken(value string, keepHyphen bool) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || (keepHyphen && r == '-') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	rs := []rune(out)
	if len(rs) > 60 {
		out = string(rs[:60])
	}
	return out
}

func certificateDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// inline so the new tab shows the PDF. attachment would save immediately
// and the tab would not open the certificate.
func certificateContentDisposition(filename string) string {
	return fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(filename, `"`, ""))
}
