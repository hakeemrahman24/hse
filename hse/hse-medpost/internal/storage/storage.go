package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const medpostWideDir = "_MedpostWide"

var (
	nonSlug   = regexp.MustCompile(`[^A-Za-z0-9]+`)
	fileSafe  = regexp.MustCompile(`[^a-z0-9]+`)
	multiDash = regexp.MustCompile(`-+`)
)

// FolderSlug is a program folder name with no spaces (FitnessForWork stays as-is).
func FolderSlug(program string) string {
	s := strings.TrimSpace(program)
	if s == "" {
		return medpostWideDir
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return medpostWideDir
	}
	return out
}

// ActivityTypeDir maps ActivityType enum to a folder (no slash).
func ActivityTypeDir(activityType string) string {
	s := strings.TrimSpace(activityType)
	s = strings.ReplaceAll(s, "/", "-")
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "Unknown"
	}
	return s
}

// EvidenceTypeDir uses EvidenceTable.EvidenceType names.
func EvidenceTypeDir(evidenceType string) string {
	switch evidenceType {
	case "Photos", "AttendanceList", "ReportAttachment", "Document":
		return evidenceType
	default:
		return "Other"
	}
}

// DocumentTypeDir uses DocumentTable.DocumentsType names.
func DocumentTypeDir(docType string) string {
	switch docType {
	case "Procedure", "SocializationMaterial", "Forms", "Template":
		return docType
	default:
		return "Other"
	}
}

// EvidenceDir: {program}/activities/{ActivityType}/{EvidenceType}/
func EvidenceDir(uploadRoot, program, activityType, evidenceType string) string {
	return filepath.Join(
		uploadRoot,
		FolderSlug(program),
		"activities",
		ActivityTypeDir(activityType),
		EvidenceTypeDir(evidenceType),
	)
}

// DocumentDir: {program}/documents/{DocumentsType}/ or _MedpostWide/documents/Template/
func DocumentDir(uploadRoot, program, docType string) string {
	root := FolderSlug(program)
	if docType == "Template" || program == "" {
		root = medpostWideDir
		docType = "Template"
	}
	return filepath.Join(uploadRoot, root, "documents", DocumentTypeDir(docType))
}

// McuCertificateDir: _MedpostWide/mcu-certificates/ — MCU certificates aren't
// tied to a Program the way activity-log's evidence/documents are (McuRecord
// has no FK_ProcID), so they always live under the medpost-wide root rather
// than a per-program folder.
func McuCertificateDir(uploadRoot string) string {
	return filepath.Join(uploadRoot, medpostWideDir, "mcu-certificates")
}

// CommonDir: _MedpostWide/common/ — site-wide images that are not program
// evidence and not generated certificates. Drop files in by these names:
//
//	logo.png                     flame logo on the fitness certificate and the public verification page
//	favicon.png                  browser tab icon, also drawn in the certificate QR code
//
// Called from cmd/server/main.go (mkdir + favicon route) and
// entrymcu.brandImage (certificate logo and signature).
func CommonDir(uploadRoot string) string {
	return filepath.Join(uploadRoot, medpostWideDir, "common")
}

// commonExts is the order FindCommonFile searches. PNG wins when both a PNG
// and a JPEG of the same name are present. go-pdf/fpdf draws PNG, JPEG, and GIF.
var commonExts = []string{".png", ".jpg", ".jpeg", ".gif"}

// FindCommonFile returns the first existing file named {name}{ext} under
// CommonDir. name is a bare stem such as "logo", never a path.
func FindCommonFile(uploadRoot, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	dir := CommonDir(uploadRoot)
	for _, ext := range commonExts {
		p := filepath.Join(dir, name+ext)
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// McuCertificateFilename: 2026-09-27_MedC0011_AhmadbinAli_20270315_a1b2c3.pdf
// date is the day the file is saved (YYYY-MM-DD). validUntil is the
// certificate's ResultValidUntil, written as YYYYMMDD.
func McuCertificateFilename(date, checkUpID, patientName, validUntil string) string {
	return fmt.Sprintf("%s_%s_%s_%s_%s.pdf",
		safeDate(date),
		checkupFileID(checkUpID),
		nameWithoutSpace(patientName),
		compactDate(validUntil),
		shortToken(6),
	)
}

func checkupFileID(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func nameWithoutSpace(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Unknown"
	}
	rs := []rune(b.String())
	if len(rs) > 60 {
		return string(rs[:60])
	}
	return b.String()
}

func compactDate(date string) string {
	d := safeDate(date)
	if d == "undated" {
		return d
	}
	return d[0:4] + d[5:7] + d[8:10]
}

// EvidenceFilename: 2026-08-22_act42_staff3_Photos_01_gate-check_a1b2c3.jpg
func EvidenceFilename(date string, activityID, staffID int64, evidenceType string, seq int, original string) string {
	return fmt.Sprintf("%s_act%d_staff%d_%s_%02d_%s_%s%s",
		safeDate(date),
		activityID,
		staffID,
		EvidenceTypeDir(evidenceType),
		seq,
		stemSlug(original),
		shortToken(6),
		safeExt(original),
	)
}

// DocumentFilename: 2026-08-22_proc1_Procedure_sop-malaria_a1b2c3.pdf
func DocumentFilename(date string, procID int64, docType, title, original string) string {
	label := stemSlug(title)
	if label == "file" {
		label = stemSlug(original)
	}
	return fmt.Sprintf("%s_proc%d_%s_%s_%s%s",
		safeDate(date),
		procID,
		DocumentTypeDir(docType),
		label,
		shortToken(6),
		safeExt(original),
	)
}

// Move relocates an already-saved file into newDir (created if needed),
// keeping its existing filename. Used when an activity is reassigned to a
// different program after upload, so its evidence files follow it into the
// new program's folder. Returns the file's unchanged path if it already
// lives in newDir.
func Move(oldPath, newDir string) (string, error) {
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return "", err
	}
	newPath := filepath.Join(newDir, filepath.Base(oldPath))
	if newPath == oldPath {
		return oldPath, nil
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", err
	}
	return newPath, nil
}

func Save(dir, filename string, src io.Reader) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dstPath := filepath.Join(dir, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(dstPath)
		return "", err
	}
	return dstPath, nil
}

func safeDate(date string) string {
	date = strings.TrimSpace(date)
	if len(date) == 10 && date[4] == '-' && date[7] == '-' {
		return date
	}
	return "undated"
}

func stemSlug(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	s := strings.ToLower(strings.TrimSpace(base))
	s = fileSafe.ReplaceAllString(s, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "file"
	}
	if len(s) > 40 {
		s = s[:40]
		s = strings.Trim(s, "-")
	}
	return s
}

func safeExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" || len(ext) > 8 {
		return ""
	}
	for _, r := range ext[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return ext
}

func shortToken(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return "x"
	}
	s := hex.EncodeToString(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}
