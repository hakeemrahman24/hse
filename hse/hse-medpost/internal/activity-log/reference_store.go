package activitylog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/storage"
)

// This file is the store behind the Reference tab on the medical record page
// (plan/emr-plan.md section 11): uploading a document's file, keeping every
// version of it, and refusing a file that is already in the library. The
// section links, listings and delete live in reference_sections.go. Nothing
// here speaks HTTP, so the rules can be tested without a request and the
// handlers (a later step) stay thin.
//
// DocumentTable remains the one library of documents. DocumentTable.FileLink
// always points at the CURRENT version's file, so the activity log's existing
// Documents page keeps working without knowing versions exist.

// MaxReferenceFileBytes is the size cap for an uploaded reference file
// (10 MB, owner's decision 2026-11-05). CreateDocument's ParseMultipartForm
// limit only bounds memory, not the upload, so the cap is enforced here, on
// the bytes actually read, whatever the caller does.
const MaxReferenceFileBytes = 10 << 20

// referenceExts are the file types a reference document may be. Anything
// else (scripts, executables, archives) is refused: these files are served
// back to every signed-in user.
var referenceExts = map[string]bool{
	".pdf": true, ".docx": true, ".xlsx": true, ".png": true, ".jpg": true, ".jpeg": true,
}

var (
	ErrFileType         = errors.New("file type not allowed: use PDF, DOCX, XLSX, PNG or JPG")
	ErrFileEmpty        = errors.New("the file is empty")
	ErrFileTooLarge     = errors.New("the file is larger than 10 MB")
	ErrDocumentNotFound = errors.New("document not found")
	ErrDocumentScope    = errors.New("invalid document type or program")
	ErrTitleRequired    = errors.New("title required")
	ErrBadLink          = errors.New("the link must be a web address starting with http:// or https://")
	// ErrVersionConflict means two uploads for the same document raced and this
	// one lost; trying again makes it the next version.
	ErrVersionConflict = errors.New("another upload changed this document at the same time, try again")
)

// DuplicateFileError is returned when the uploaded bytes already exist in the
// library (any version of any document). The owner chose a hard reject with no
// override, so the message names where the file already is.
type DuplicateFileError struct {
	DocumentID int64
	Title      string
}

func (e *DuplicateFileError) Error() string {
	return fmt.Sprintf("This file is already in Reference as %q", e.Title)
}

// ReferenceUpload is one file being added: the name the user's browser sent
// (used for the extension and for showing in History) and its bytes.
type ReferenceUpload struct {
	Original string
	Data     io.Reader
}

// ReferenceVersion is one row of a document's History.
type ReferenceVersion struct {
	VersionNo    int    `json:"versionNo"`
	OriginalName string `json:"originalName"`
	UploadDate   string `json:"uploadDate"`
	UploadedBy   string `json:"uploadedBy"`
	IsCurrent    bool   `json:"isCurrent"`
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// nullStaff stores "no staff member" as NULL, not 0, so the foreign key holds.
func nullStaff(staffID int64) any {
	if staffID <= 0 {
		return nil
	}
	return staffID
}

// referenceScope applies the DocumentTable rule that the table's CHECK also
// enforces: every type except Template belongs to a program, and a Template
// belongs to none. Checking here gives a readable error instead of a bare
// constraint failure, and returns the program name that names the folder.
func (a *API) referenceScope(procID *int64, dtype string) (string, error) {
	if !docTypes[dtype] {
		return "", fmt.Errorf("%w: unknown type %q", ErrDocumentScope, dtype)
	}
	if dtype == "Template" {
		if procID != nil {
			return "", fmt.Errorf("%w: a template has no program", ErrDocumentScope)
		}
		return "", nil
	}
	if procID == nil {
		return "", fmt.Errorf("%w: choose a program", ErrDocumentScope)
	}
	name, err := a.procedureName(*procID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: program not found", ErrDocumentScope)
	}
	return name, err
}

// saveReferenceFile writes the upload to dir and returns its path and SHA-256.
// The hash is computed in the same pass as the write (io.TeeReader), over the
// exact bytes stored, so it can be compared with later uploads and with the
// file on disk. The size cap reads one byte past the limit: a file of exactly
// 10 MB is accepted, one byte more is refused and its partial file removed.
func (a *API) saveReferenceFile(dir, name string, up ReferenceUpload) (path, hash string, err error) {
	if !referenceExts[strings.ToLower(filepath.Ext(up.Original))] {
		return "", "", ErrFileType
	}
	limited := &io.LimitedReader{R: up.Data, N: MaxReferenceFileBytes + 1}
	h := sha256.New()
	saved, err := storage.Save(dir, name, io.TeeReader(limited, h))
	if err != nil {
		return "", "", fmt.Errorf("save reference file: %w", err)
	}
	read := MaxReferenceFileBytes + 1 - limited.N
	switch {
	case read == 0:
		_ = os.Remove(saved)
		return "", "", ErrFileEmpty
	case read > MaxReferenceFileBytes:
		_ = os.Remove(saved)
		return "", "", ErrFileTooLarge
	}
	return saved, hex.EncodeToString(h.Sum(nil)), nil
}

// findDocumentByHash returns the document that already holds these bytes in
// any of its versions, or nil.
func (a *API) findDocumentByHash(hash string) (*DuplicateFileError, error) {
	var dup DuplicateFileError
	err := a.DB.QueryRow(`
		SELECT d.DocumentID, d.DocumentTitle
		FROM DocumentVersion v JOIN DocumentTable d ON d.DocumentID = v.FK_DocumentID
		WHERE v.FileHash = ?`, hash).Scan(&dup.DocumentID, &dup.Title)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dup, nil
}

// classifyInsertError turns a unique-index failure into the error the user
// should see. The pre-insert hash check can be beaten by a simultaneous upload
// of the same file; the unique index on FileHash is what really stops it, and
// this maps that failure back to the same "already in Reference" message.
func (a *API) classifyInsertError(err error, hash string) error {
	if !isUniqueViolation(err) {
		return err
	}
	if dup, lookupErr := a.findDocumentByHash(hash); lookupErr == nil && dup != nil {
		return dup
	}
	return ErrVersionConflict
}

// insertVersion adds the next version row for docID inside tx and points
// DocumentTable.FileLink at it, in one transaction so the two never disagree.
func insertVersion(tx *sql.Tx, docID int64, path, original, hash string, staffID int64) (int, error) {
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(VersionNo), 0) + 1 FROM DocumentVersion WHERE FK_DocumentID = ?`, docID).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
		INSERT INTO DocumentVersion (FK_DocumentID, VersionNo, FileLink, OriginalName, FileHash, FK_StaffID, UploadDate)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		docID, next, path, filepath.Base(original), hash, nullStaff(staffID), dates.Today(),
	); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE DocumentTable SET FileLink = ? WHERE DocumentID = ?`, path, docID); err != nil {
		return 0, err
	}
	return next, nil
}

// AddDocumentWithFile creates a new library document with an uploaded file as
// its version 1. A file whose bytes are already stored anywhere is refused
// with *DuplicateFileError and nothing is left on disk or in the database.
func (a *API) AddDocumentWithFile(procID *int64, dtype, title, notes string, up ReferenceUpload, staffID int64) (int64, error) {
	title = normalizeTitle(title)
	if title == "" {
		return 0, ErrTitleRequired
	}
	dtype = strings.TrimSpace(dtype)
	program, err := a.referenceScope(procID, dtype)
	if err != nil {
		return 0, err
	}
	var pid int64
	var procArg any
	if procID != nil {
		pid, procArg = *procID, *procID
	}
	path, hash, err := a.saveReferenceFile(
		storage.DocumentDir(a.UploadDir, program, dtype),
		storage.DocumentFilename(dates.Today(), pid, dtype, title, up.Original),
		up,
	)
	if err != nil {
		return 0, err
	}
	docID, err := a.insertNewFileDocument(procArg, dtype, title, strings.TrimSpace(notes), path, up.Original, hash, staffID)
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	return docID, nil
}

func (a *API) insertNewFileDocument(procArg any, dtype, title, notes, path, original, hash string, staffID int64) (int64, error) {
	if dup, err := a.findDocumentByHash(hash); err != nil {
		return 0, err
	} else if dup != nil {
		return 0, dup
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FileLink, Notes)
		VALUES (?, ?, ?, ?, ?)`, procArg, dtype, title, path, nullIfEmpty(notes))
	if err != nil {
		return 0, err
	}
	docID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := insertVersion(tx, docID, path, original, hash, staffID); err != nil {
		return 0, a.classifyInsertError(err, hash)
	}
	if err := tx.Commit(); err != nil {
		return 0, a.classifyInsertError(err, hash)
	}
	return docID, nil
}

// ReplaceDocumentFile adds a new version to an existing document and makes it
// the current one. The old file stays on disk and in History. Returns the new
// version number.
func (a *API) ReplaceDocumentFile(docID int64, up ReferenceUpload, staffID int64) (int, error) {
	var procID sql.NullInt64
	var dtype, title string
	err := a.DB.QueryRow(`SELECT FK_ProcID, DocumentsType, DocumentTitle FROM DocumentTable WHERE DocumentID = ?`, docID).
		Scan(&procID, &dtype, &title)
	if err == sql.ErrNoRows {
		return 0, ErrDocumentNotFound
	}
	if err != nil {
		return 0, err
	}
	var program string
	var pid int64
	if procID.Valid {
		pid = procID.Int64
		if program, err = a.procedureName(pid); err != nil {
			return 0, err
		}
	}
	path, hash, err := a.saveReferenceFile(
		storage.DocumentDir(a.UploadDir, program, dtype),
		storage.DocumentFilename(dates.Today(), pid, dtype, title, up.Original),
		up,
	)
	if err != nil {
		return 0, err
	}
	no, err := a.insertReplacement(docID, path, up.Original, hash, staffID)
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	return no, nil
}

func (a *API) insertReplacement(docID int64, path, original, hash string, staffID int64) (int, error) {
	if dup, err := a.findDocumentByHash(hash); err != nil {
		return 0, err
	} else if dup != nil {
		return 0, dup
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	no, err := insertVersion(tx, docID, path, original, hash, staffID)
	if err != nil {
		return 0, a.classifyInsertError(err, hash)
	}
	if err := tx.Commit(); err != nil {
		return 0, a.classifyInsertError(err, hash)
	}
	return no, nil
}

// AddLinkDocument creates a library document that is only a web link (no file,
// so no version). Only http and https addresses are accepted: the link is
// rendered as something every user can click, and a javascript: or data: address
// would run script in the page.
func (a *API) AddLinkDocument(procID *int64, dtype, title, notes, rawLink string) (int64, error) {
	title = normalizeTitle(title)
	if title == "" {
		return 0, ErrTitleRequired
	}
	link := strings.TrimSpace(rawLink)
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return 0, ErrBadLink
	}
	dtype = strings.TrimSpace(dtype)
	if _, err := a.referenceScope(procID, dtype); err != nil {
		return 0, err
	}
	var procArg any
	if procID != nil {
		procArg = *procID
	}
	res, err := a.DB.Exec(`
		INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink, Notes)
		VALUES (?, ?, ?, ?, ?)`, procArg, dtype, title, link, nullIfEmpty(strings.TrimSpace(notes)))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// VersionFile returns the stored path and the original upload name of one
// version of a document, for the History download.
func (a *API) VersionFile(docID int64, versionNo int) (path, original string, err error) {
	err = a.DB.QueryRow(`SELECT FileLink, OriginalName FROM DocumentVersion WHERE FK_DocumentID = ? AND VersionNo = ?`,
		docID, versionNo).Scan(&path, &original)
	if err == sql.ErrNoRows {
		return "", "", ErrDocumentNotFound
	}
	return path, original, err
}

// ListDocumentVersions is a document's History, newest first. The newest row
// is the current version (DocumentTable.FileLink points at it).
func (a *API) ListDocumentVersions(docID int64) ([]ReferenceVersion, error) {
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM DocumentTable WHERE DocumentID = ?`, docID).Scan(&exists); err == sql.ErrNoRows {
		return nil, ErrDocumentNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(`
		SELECT v.VersionNo, v.OriginalName, v.UploadDate, COALESCE(s.StaffName, '')
		FROM DocumentVersion v LEFT JOIN MedpostStaffTable s ON s.StaffID = v.FK_StaffID
		WHERE v.FK_DocumentID = ?
		ORDER BY v.VersionNo DESC`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReferenceVersion{}
	for rows.Next() {
		var v ReferenceVersion
		if err := rows.Scan(&v.VersionNo, &v.OriginalName, &v.UploadDate, &v.UploadedBy); err != nil {
			return nil, err
		}
		v.IsCurrent = len(out) == 0
		out = append(out, v)
	}
	return out, rows.Err()
}

// EnsureDocumentHashes fills in FileHash for versions that have none: the
// version 1 rows migration 025 created for files uploaded before hashing
// existed. Without it an old file could be uploaded again unnoticed. Safe to
// run at every startup; it only touches rows still missing a hash.
//
// A file that is missing from disk, or whose bytes equal another stored file
// (the unique index refuses the second hash), is logged and left unhashed
// rather than stopping the server from starting. Returns how many were filled.
func EnsureDocumentHashes(database *sql.DB) (int, error) {
	rows, err := database.Query(`SELECT VersionID, FileLink FROM DocumentVersion WHERE FileHash IS NULL`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id   int64
		link string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.link); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	// Read everything before updating: some drivers do not allow writes while a
	// cursor on the same table is still open.
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	filled := 0
	for _, p := range todo {
		if strings.HasPrefix(p.link, "placeholder:") {
			continue
		}
		hash, err := hashFile(p.link)
		if err != nil {
			log.Printf("reference hashes: version %d: cannot read %s: %v", p.id, p.link, err)
			continue
		}
		if _, err := database.Exec(`UPDATE DocumentVersion SET FileHash = ? WHERE VersionID = ?`, hash, p.id); err != nil {
			if isUniqueViolation(err) {
				log.Printf("reference hashes: version %d has the same bytes as another stored file; left unhashed", p.id)
				continue
			}
			return filled, err
		}
		filled++
	}
	return filled, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
