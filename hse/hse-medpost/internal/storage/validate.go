package storage

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// allowedImageTypes covers image formats every mainstream browser renders
// natively. SVG is deliberately excluded since it can embed scripts.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// sniffContentType reads up to 512 bytes to detect the real content type,
// then returns a reader that replays those bytes before the rest of src so
// the caller can still save the full, untouched content.
func sniffContentType(src io.Reader) (contentType string, rewound io.Reader, err error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(src, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", nil, err
	}
	buf = buf[:n]
	return http.DetectContentType(buf), io.MultiReader(bytes.NewReader(buf), src), nil
}

// RequireImage checks that src is a browser-renderable image (JPEG, PNG,
// GIF, or WebP) by sniffing its actual bytes, not the client-supplied
// filename or Content-Type header. It returns a reader positioned at the
// start of src so the caller can still save the full content.
func RequireImage(src io.Reader) (io.Reader, error) {
	ct, rewound, err := sniffContentType(src)
	if err != nil {
		return nil, err
	}
	if !allowedImageTypes[ct] {
		return nil, fmt.Errorf("unsupported image type %q: must be JPEG, PNG, GIF, or WebP", ct)
	}
	return rewound, nil
}

// RequirePDF checks that src is a PDF by sniffing its actual bytes. It
// returns a reader positioned at the start of src so the caller can still
// save the full content.
func RequirePDF(src io.Reader) (io.Reader, error) {
	ct, rewound, err := sniffContentType(src)
	if err != nil {
		return nil, err
	}
	if ct != "application/pdf" {
		return nil, fmt.Errorf("unsupported file type %q: must be a PDF", ct)
	}
	return rewound, nil
}
