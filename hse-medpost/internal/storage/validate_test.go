package storage

import (
	"bytes"
	"io"
	"testing"
)

var pngHeader = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

func TestRequireImageAcceptsPNG(t *testing.T) {
	src := bytes.NewReader(append(pngHeader, []byte("rest of the fake png bytes")...))
	out, err := RequireImage(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(out)
	if !bytes.HasPrefix(data, pngHeader) {
		t.Fatalf("rewound reader lost the sniffed bytes: %q", data)
	}
}

func TestRequireImageRejectsHTML(t *testing.T) {
	src := bytes.NewReader([]byte("<html><script>alert(1)</script></html>"))
	if _, err := RequireImage(src); err == nil {
		t.Fatal("expected error for non-image content disguised as a photo upload")
	}
}

func TestRequirePDFAcceptsPDF(t *testing.T) {
	src := bytes.NewReader([]byte("%PDF-1.4\n%rest of a fake pdf body"))
	out, err := RequirePDF(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(out)
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) {
		t.Fatalf("rewound reader lost the sniffed bytes: %q", data)
	}
}

func TestRequirePDFRejectsImage(t *testing.T) {
	src := bytes.NewReader(pngHeader)
	if _, err := RequirePDF(src); err == nil {
		t.Fatal("expected error for a non-PDF upload to the attendance/report fields")
	}
}
