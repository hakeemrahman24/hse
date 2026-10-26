package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PDF documents on an observation. Staff only: the public SOC form takes photos only, because a
// PDF can carry scripts and the public page is the least trusted place to accept one.
//
// Rules, for "before" (evidence) and "after" alike:
//   - at most 2 PDFs per kind,
//   - each at most 5 MB.
const (
	maxPDFsPerKind = 2
	pdfMaxBytes    = 5 << 20
	displayMaxLen  = 100
)

// pdfActive matches the PDF names that make a file do things when it is opened: scripts, launching
// programs, embedded files, rich media. A document for the register has no use for any of them.
// This is a plain byte search, so it is a safety net, not a full PDF scanner; the file is also
// only ever sent back as a download (never shown inside the site), with nosniff.
var pdfActive = regexp.MustCompile(`/(JavaScript|JS|Launch|EmbeddedFile|RichMedia)([^A-Za-z]|$)`)

// checkPDFCount enforces the 2-PDF limit for one post and one kind.
func checkPDFCount(have, adding int) error {
	if have+adding > maxPDFsPerKind {
		return fmt.Errorf("a post can have at most %d PDF documents", maxPDFsPerKind)
	}
	return nil
}

// processPDF reads an uploaded document and returns its bytes if it is an acceptable PDF. It looks
// at the content, not at the file name or the type the browser claimed.
func processPDF(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, pdfMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read the document: %w", err)
	}
	switch {
	case len(data) == 0:
		return nil, errors.New("the document is empty")
	case len(data) > pdfMaxBytes:
		return nil, errors.New("the PDF is bigger than 5 MB")
	}
	// The "%PDF-" marker must be at the very start (readers tolerate a little junk before it,
	// but this register does not).
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, errors.New("the file is not a PDF")
	}
	tail := data[max(0, len(data)-2048):]
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return nil, errors.New("the PDF is incomplete or damaged")
	}
	if pdfActive.Match(data) {
		return nil, errors.New("the PDF contains scripts or embedded files, which are not allowed")
	}
	return data, nil
}

// safeDisplayName turns an uploaded file name into a short, harmless label for the download:
// no folders, no control characters, no quotes, and always ending in .pdf.
func safeDisplayName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '/' || r == '\\' || r == ';' || r == '%' || r == '<' || r == '>':
			b.WriteByte('_')
		case unicode.IsControl(r) || !unicode.IsPrint(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimSuffix(strings.TrimSuffix(out, ".pdf"), ".PDF")
	out = strings.Trim(out, ". ")
	if out == "" {
		out = "document"
	}
	for utf8.RuneCountInString(out) > displayMaxLen-4 {
		_, size := utf8.DecodeLastRuneInString(out)
		out = out[:len(out)-size]
	}
	return out + ".pdf"
}

// storedDoc is a PDF that has been checked and written to the evidence folder.
type storedDoc struct {
	Name    string // random file name on disk
	Display string // label shown to staff
	Size    int
}

// storeDocuments checks and saves the uploaded PDFs of one post. have is how many PDFs of this kind
// the post already has. Either every file is saved or none is.
func storeDocuments(dir string, files []*multipart.FileHeader, have int) ([]storedDoc, error) {
	if err := checkPDFCount(have, len(files)); err != nil {
		return nil, err
	}
	var saved []storedDoc
	fail := func(err error) ([]storedDoc, error) {
		names := make([]string, len(saved))
		for i, d := range saved {
			names[i] = d.Name
		}
		removePhotoFiles(dir, names)
		return nil, err
	}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			return fail(errors.New("could not read the document"))
		}
		data, err := processPDF(f)
		f.Close()
		if err != nil {
			return fail(err)
		}
		name, err := saveFile(dir, data, ".pdf")
		if err != nil {
			return fail(fmt.Errorf("could not save the document: %w", err))
		}
		saved = append(saved, storedDoc{Name: name, Display: safeDisplayName(fh.Filename), Size: len(data)})
	}
	return saved, nil
}
