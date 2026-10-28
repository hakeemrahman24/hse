package mcusubmit

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"regexp"
	"strconv"
)

// Errors of the upload pipeline. The web layer maps them to messages for the submitter.
var (
	ErrNotPDF          = errors.New("mcusubmit: the file is not a PDF")
	ErrEncrypted       = errors.New("mcusubmit: the PDF is password protected")
	ErrUnsafeContent   = errors.New("mcusubmit: the PDF contains active content")
	ErrTooLarge        = errors.New("mcusubmit: the file is too large")
	ErrRebuild         = errors.New("mcusubmit: the PDF could not be rebuilt")
	ErrCannotShrink    = errors.New("mcusubmit: the PDF cannot be made small enough")
	ErrInfected        = errors.New("mcusubmit: the file is infected")
	ErrScanUnavailable = errors.New("mcusubmit: the virus scan could not run")
)

// maxInflateTotal caps how many bytes all object streams together may expand to while we look
// inside them. A few KB of zlib can expand to gigabytes (a "zip bomb"); without a cap the check
// itself would be the attack.
const maxInflateTotal = 32 << 20

// UnsafeError says which kind of active content made a PDF unacceptable. It matches
// ErrUnsafeContent with errors.Is.
type UnsafeError struct{ Feature string }

func (e *UnsafeError) Error() string {
	return "mcusubmit: the PDF contains active content (" + e.Feature + ")"
}

func (e *UnsafeError) Is(target error) bool { return target == ErrUnsafeContent }

// unsafeFeatures are PDF names that run code, start programs, pull in other files or talk to the
// network. /OpenAction and /AA on their own are not listed: harmless files use them to open on a
// page, and the dangerous part (the action type inside) is caught by the names below. The
// Ghostscript rebuild drops whatever is left over.
var unsafeFeatures = []string{
	"JavaScript", "JS", "Launch", "EmbeddedFile", "EmbeddedFiles", "RichMedia",
	"XFA", "SubmitForm", "ImportData", "GoToE",
}

var nameEscape = regexp.MustCompile(`#([0-9A-Fa-f]{2})`)

// normalizeNames turns #xx escapes into the byte they stand for. PDF allows /J#61vaScript to mean
// /JavaScript, which a plain text search would miss. Decoding every #xx (also inside strings) can
// only make the check stricter, never looser.
func normalizeNames(b []byte) []byte {
	if !bytes.Contains(b, []byte("#")) {
		return b
	}
	return nameEscape.ReplaceAllFunc(b, func(m []byte) []byte {
		n, _ := strconv.ParseUint(string(m[1:]), 16, 8)
		return []byte{byte(n)}
	})
}

// isRegular reports whether c can be part of a PDF name (not whitespace or a delimiter).
func isRegular(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ', '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return false
	}
	return true
}

// hasName reports whether /name appears as a whole name (so /JS does not match /JSomething).
func hasName(b []byte, name string) bool {
	key := []byte("/" + name)
	for off := 0; ; {
		i := bytes.Index(b[off:], key)
		if i < 0 {
			return false
		}
		end := off + i + len(key)
		if end >= len(b) || !isRegular(b[end]) {
			return true
		}
		off += i + 1
	}
}

func findUnsafe(b []byte) string {
	b = normalizeNames(b)
	for _, f := range unsafeFeatures {
		if hasName(b, f) {
			return f
		}
	}
	return ""
}

// CheckStructure refuses PDFs that carry active content or that we cannot look into. It is the
// cheap early gate; the Ghostscript rebuild is the real protection, because it writes a new file
// from what was drawn and keeps none of the original's structure.
func CheckStructure(data []byte) error {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return ErrNotPDF
	}
	norm := normalizeNames(data)
	if hasName(norm, "Encrypt") {
		return ErrEncrypted
	}
	if f := findUnsafe(norm); f != "" {
		return &UnsafeError{Feature: f}
	}
	return checkObjectStreams(norm)
}

// checkObjectStreams looks inside /ObjStm streams: since PDF 1.5 dictionaries (including actions)
// can be stored compressed there, invisible to a text search of the raw file.
func checkObjectStreams(data []byte) error {
	remaining := maxInflateTotal
	marker := []byte("/ObjStm")
	for off := 0; ; {
		i := bytes.Index(data[off:], marker)
		if i < 0 {
			return nil
		}
		pos := off + i + len(marker)
		off = pos
		body, dict, ok := streamAfter(data, pos)
		if !ok {
			continue
		}
		content := body
		if bytes.Contains(dict, []byte("/FlateDecode")) {
			var err error
			content, err = inflateLimited(body, remaining)
			if errors.Is(err, errInflateLimit) {
				return &UnsafeError{Feature: "oversized compressed data"}
			}
			remaining -= len(content)
		}
		if f := findUnsafe(content); f != "" {
			return &UnsafeError{Feature: f}
		}
	}
}

var errInflateLimit = errors.New("inflate limit reached")

// inflateLimited returns up to limit bytes of the inflated data. A corrupt stream is not an error
// here: whatever inflated so far is still checked, and Ghostscript decides about the rest.
func inflateLimited(body []byte, limit int) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, nil
	}
	defer zr.Close()
	out, _ := io.ReadAll(io.LimitReader(zr, int64(limit)+1))
	if len(out) > limit {
		return out[:limit], errInflateLimit
	}
	return out, nil
}

// streamAfter finds the stream that belongs to the dictionary containing the /ObjStm name at pos.
// It returns the raw stream bytes and the text between pos and the stream keyword (the rest of the
// dictionary, where /FlateDecode would be).
func streamAfter(data []byte, pos int) (body, dict []byte, ok bool) {
	i := bytes.Index(data[pos:], []byte("stream"))
	if i < 0 {
		return nil, nil, false
	}
	start := pos + i + len("stream")
	switch {
	case bytes.HasPrefix(data[start:], []byte("\r\n")):
		start += 2
	case bytes.HasPrefix(data[start:], []byte("\n")):
		start++
	default:
		return nil, nil, false
	}
	end := bytes.Index(data[start:], []byte("endstream"))
	if end < 0 {
		return nil, nil, false
	}
	return data[start : start+end], data[pos : pos+i], true
}
