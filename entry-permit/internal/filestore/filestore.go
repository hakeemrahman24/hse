// Package filestore keeps uploaded permit documents on disk, outside the web
// root, and decides what a file is from its bytes.
//
// WHY from the bytes: a browser's file name and content type are typed by the
// sender. A ".pdf" that is really a script must not be stored as a document, so
// the kind is read from the first bytes and the extension is chosen here.
package filestore

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// idPattern is the shape of a request id (WEP-xxxxx). Only this shape may name a folder.
var idPattern = regexp.MustCompile(`^[A-Z]{3}-[A-Z0-9]{5}$`)

// kindPattern limits the file label to simple lower-case words.
var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// Store writes under Dir/<request id>/<kind>.<ext>.
type Store struct{ Dir string }

// These errors are written for the requester: they say what to fix.
var (
	ErrEmpty   = errors.New("the file is empty")
	ErrType    = errors.New("the file must be a PDF, JPG or PNG")
	ErrTooBig  = errors.New("the file is too large")
	errBadName = errors.New("unsafe request id or file label")
)

// detect returns the extension for an accepted document, or "".
func detect(data []byte) string {
	if len(data) >= 5 && string(data[:5]) == "%PDF-" {
		return ".pdf"
	}
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	}
	return ""
}

// Save stores one document and returns its path relative to Dir, which is what
// goes in the database. maxBytes is the limit from Settings.
func (s Store) Save(requestID, kind string, data []byte, maxBytes int) (string, error) {
	if !idPattern.MatchString(requestID) || !kindPattern.MatchString(kind) {
		return "", errBadName
	}
	switch {
	case len(data) == 0:
		return "", ErrEmpty
	case maxBytes > 0 && len(data) > maxBytes:
		return "", ErrTooBig
	}
	ext := detect(data)
	if ext == "" {
		return "", ErrType
	}
	dir := filepath.Join(s.Dir, requestID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create upload folder: %w", err)
	}
	rel := requestID + "/" + kind + ext
	if err := os.WriteFile(filepath.Join(s.Dir, rel), data, 0o600); err != nil {
		return "", fmt.Errorf("write upload: %w", err)
	}
	return rel, nil
}

// relPattern is exactly what Save writes: <request id>/<label>.<pdf|jpg|png>.
var relPattern = regexp.MustCompile(`^[A-Z]{3}-[A-Z0-9]{5}/[a-z][a-z0-9_]{0,30}\.(pdf|jpg|png)$`)

var contentTypes = map[string]string{".pdf": "application/pdf", ".jpg": "image/jpeg", ".png": "image/png"}

// Path returns the file on disk for a stored relative path, and its content type.
//
// WHY so strict: the path comes from a URL a reviewer clicked, so it is treated
// as hostile. Only the exact shape Save writes is accepted, which leaves no room
// for "..", slashes, absolute paths or odd extensions.
func (s Store) Path(rel string) (string, string, error) {
	if !relPattern.MatchString(rel) {
		return "", "", errBadName
	}
	return filepath.Join(s.Dir, rel), contentTypes[filepath.Ext(rel)], nil
}

// Remove deletes a request's folder. The submit handler calls it when saving the
// permit fails, so no orphan files stay behind.
func (s Store) Remove(requestID string) error {
	if !idPattern.MatchString(requestID) {
		return errBadName
	}
	if err := os.RemoveAll(filepath.Join(s.Dir, requestID)); err != nil {
		return fmt.Errorf("remove upload folder: %w", err)
	}
	return nil
}
