package correctiveaction

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/storage"
)

// maxDocumentNameRunes keeps a pasted-in long file name from overflowing the column or the page.
const maxDocumentNameRunes = 120

// documentName is the name shown for an upload: the base name only (a browser may send a path), no control
// characters, and never empty. It is display text, never used to build a path on disk.
func documentName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' {
			return -1
		}
		return r
	}, name)
	if r := []rune(name); len(r) > maxDocumentNameRunes {
		name = string(r[:maxDocumentNameRunes])
	}
	if name = strings.TrimSpace(name); name == "" || name == "." {
		return "document.pdf"
	}
	return name
}

// PrepareDocument reads one uploaded document and checks it is a PDF (sniffed from the bytes, not the file
// name) within MaxDocumentBytes. Unlike a photo it is stored untouched: shrinking a PDF would mean
// re-drawing its pages as pictures, which loses the text a signed report is kept for.
func PrepareDocument(src io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(src, MaxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}
	if len(data) == 0 {
		return nil, ValidationError("the document file is empty")
	}
	if len(data) > MaxDocumentBytes {
		return nil, ValidationError("a document can be at most 10 MB")
	}
	if _, err := storage.RequirePDF(bytes.NewReader(data)); err != nil {
		return nil, ValidationError("a document must be a PDF file")
	}
	return data, nil
}

// AddDocument records one stored PDF. The count limit is checked inside the transaction, like photos.
func (s *Store) AddDocument(actionID int64, fileName, fileLink string, staffID int64) (int64, error) {
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM CorrectiveActionTable WHERE ActionID = ?`, actionID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("find corrective action %d: %w", actionID, err)
		}
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionDocumentTable WHERE FK_ActionID = ?`, actionID).Scan(&count); err != nil {
			return fmt.Errorf("count documents: %w", err)
		}
		if count >= MaxDocumentsPerAction {
			return ValidationError(fmt.Sprintf("a finding can have at most %d documents", MaxDocumentsPerAction))
		}
		res, err := tx.Exec(`
			INSERT INTO CorrectiveActionDocumentTable (FK_ActionID, FileName, FileLink, UploadedBy, UploadedAt)
			VALUES (?, ?, ?, ?, ?)`, actionID, fileName, fileLink, staffID, dates.NowStamp())
		if err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("read new document id: %w", err)
		}
		return logChange(tx, actionID, staffID, "document_added", "", fileName)
	})
	return id, err
}

// DeleteDocument removes one record and returns its file path for the caller to delete.
func (s *Store) DeleteDocument(documentID, staffID int64) (string, error) {
	var link string
	err := s.inTx(func(tx *sql.Tx) error {
		var actionID int64
		var name string
		err := tx.QueryRow(`SELECT FK_ActionID, FileName, FileLink FROM CorrectiveActionDocumentTable WHERE DocumentID = ?`, documentID).Scan(&actionID, &name, &link)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find document %d: %w", documentID, err)
		}
		if _, err := tx.Exec(`DELETE FROM CorrectiveActionDocumentTable WHERE DocumentID = ?`, documentID); err != nil {
			return fmt.Errorf("delete document %d: %w", documentID, err)
		}
		return logChange(tx, actionID, staffID, "document_removed", name, "")
	})
	if err != nil {
		return "", err
	}
	return link, nil
}

// DocumentPath returns the stored path and shown name of one document, for the handler that serves it.
func (s *Store) DocumentPath(documentID int64) (path, name string, err error) {
	err = s.DB.QueryRow(`SELECT FileLink, FileName FROM CorrectiveActionDocumentTable WHERE DocumentID = ?`, documentID).Scan(&path, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("look up document %d: %w", documentID, err)
	}
	return path, name, nil
}

// attachDocuments loads the documents of all given findings in one query.
func attachDocuments(q querier, actions []Action) error {
	if len(actions) == 0 {
		return nil
	}
	index, args, marks := indexActions(actions)
	rows, err := q.Query(`
		SELECT DocumentID, FK_ActionID, FileName, FileLink, UploadedAt
		FROM CorrectiveActionDocumentTable
		WHERE FK_ActionID IN (`+marks+`)
		ORDER BY DocumentID`, args...)
	if err != nil {
		return fmt.Errorf("load corrective action documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d Document
		var actionID int64
		if err := rows.Scan(&d.ID, &actionID, &d.FileName, &d.FileLink, &d.UploadedAt); err != nil {
			return fmt.Errorf("scan corrective action document: %w", err)
		}
		i := index[actionID]
		actions[i].Documents = append(actions[i].Documents, d)
	}
	return rows.Err()
}

// UploadDocuments: POST /api/corrective-actions/{id}/documents, multipart with PDFs in "documents".
func (a *API) UploadDocuments(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxDocumentsPerAction*MaxDocumentBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		badRequest(w, "the upload is invalid or too large")
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["documents"]
	if len(files) == 0 {
		badRequest(w, "choose at least one PDF")
		return
	}
	action, err := a.Store.Get(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	dir := filepath.Join(a.UploadDir, storage.FolderSlug(action.ProgramKey), "corrective-actions", "Documents")
	for _, header := range files {
		if err := a.storeDocument(header, dir, id, staffID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	a.respondWithAction(w, r, http.StatusCreated, id)
}

func (a *API) storeDocument(header *multipart.FileHeader, dir string, actionID, staffID int64) error {
	file, err := header.Open()
	if err != nil {
		return fmt.Errorf("open uploaded document: %w", err)
	}
	defer file.Close()
	data, err := PrepareDocument(file)
	if err != nil {
		return err
	}
	token := make([]byte, 4)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("make document name: %w", err)
	}
	// The stored name is generated: the uploaded name is only text to show, never part of a path.
	stored := fmt.Sprintf("%s_car%d_doc_%s.pdf", dates.Today(), actionID, hex.EncodeToString(token))
	path, err := storage.Save(dir, stored, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("save document: %w", err)
	}
	if _, err := a.Store.AddDocument(actionID, documentName(header.Filename), path, staffID); err != nil {
		a.removeFile(path)
		return err
	}
	return nil
}

// ServeDocument streams one stored PDF inline. WHY there is no "Content-Security-Policy: sandbox": Edge
// and Chrome will not start their built-in PDF viewer inside a sandboxed response ("This page has been
// blocked by Microsoft Edge"). The fixed application/pdf type plus nosniff already stop the file from
// being treated as a page.
func (a *API) ServeDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	path, name, err := a.Store.DocumentPath(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !a.insideUploadDir(path) {
		log.Printf("corrective action document %d points outside the upload folder: %q", id, path)
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(photoCacheSeconds))
	http.ServeFile(w, r, path)
}

func (a *API) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	path, err := a.Store.DeleteDocument(id, staffID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.removeFile(path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
