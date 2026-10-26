package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const tinyPDF = "%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"

func TestProcessPDFAcceptsAPlainPDF(t *testing.T) {
	if _, err := processPDF(strings.NewReader(tinyPDF)); err != nil {
		t.Fatalf("plain PDF refused: %v", err)
	}
}

func TestProcessPDFRefusals(t *testing.T) {
	cases := []struct{ name, data string }{
		{"empty", ""},
		{"text renamed to pdf", "hello, I am not a pdf\n%%EOF"},
		{"png bytes", "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 100) + "%%EOF"},
		{"junk before the marker", "junk%PDF-1.4\n%%EOF"},
		{"truncated, no EOF marker", "%PDF-1.4\n1 0 obj\n<< >>\nendobj\n"},
		{"javascript", "%PDF-1.4\n1 0 obj\n<< /S /JavaScript /JS (app.alert(1)) >>\nendobj\n%%EOF"},
		{"short js key", "%PDF-1.4\n<< /JS (x) >>\n%%EOF"},
		{"launch", "%PDF-1.4\n<< /S /Launch /F (cmd.exe) >>\n%%EOF"},
		{"embedded file", "%PDF-1.4\n<< /Type /EmbeddedFile >>\n%%EOF"},
		{"too big", "%PDF-1.4\n" + strings.Repeat("a", pdfMaxBytes) + "\n%%EOF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := processPDF(strings.NewReader(c.data)); err == nil {
				t.Error("was accepted")
			}
		})
	}
}

func TestProcessPDFAllowsWordsThatOnlyLookLikeTheKeys(t *testing.T) {
	// The words appear in text without a leading slash name, so they are not the action keys.
	data := "%PDF-1.4\n<< /Producer (A javascript guide) /Title (JS tips) >>\n%%EOF"
	if _, err := processPDF(strings.NewReader(data)); err != nil {
		t.Errorf("harmless text refused: %v", err)
	}
}

func TestSafeDisplayName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":                    "report.pdf",
		"../../etc/passwd":              "passwd.pdf",
		`C:\Users\me\audit "final".PDF`: "audit _final_.pdf",
		"":                              "document.pdf",
		"...":                           "document.pdf",
		"a\x00b\nc.pdf":                 "abc.pdf",
	}
	for in, want := range cases {
		if got := safeDisplayName(in); got != want {
			t.Errorf("safeDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
	long := safeDisplayName(strings.Repeat("é", 300) + ".pdf")
	if n := len([]rune(long)); n > displayMaxLen {
		t.Errorf("long name has %d characters, want at most %d", n, displayMaxLen)
	}
}

func TestCheckPDFCount(t *testing.T) {
	if err := checkPDFCount(1, 1); err != nil {
		t.Errorf("2 PDFs refused: %v", err)
	}
	if err := checkPDFCount(2, 1); err == nil {
		t.Error("3 PDFs accepted")
	}
}

// pdfHeaders builds multipart file headers for storeDocuments.
func pdfHeaders(t *testing.T, contents ...string) []*multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i, c := range contents {
		w, err := mw.CreateFormFile("documents", "file"+string(rune('a'+i))+".pdf")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(c))
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	return req.MultipartForm.File["documents"]
}

func TestStoreDocumentsSavesAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	saved, err := storeDocuments(dir, pdfHeaders(t, tinyPDF, tinyPDF), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || !strings.HasSuffix(saved[0].Name, ".pdf") || saved[0].Display != "filea.pdf" {
		t.Errorf("saved = %+v", saved)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d files on disk, want 2", len(entries))
	}

	// A bad second file must remove the first one again: all or nothing.
	dir2 := t.TempDir()
	if _, err := storeDocuments(dir2, pdfHeaders(t, tinyPDF, "not a pdf"), 0); err == nil {
		t.Fatal("a bad document was accepted")
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Errorf("%d files left behind after a failed upload", len(entries))
	}

	// Existing documents count towards the limit.
	if _, err := storeDocuments(t.TempDir(), pdfHeaders(t, tinyPDF, tinyPDF), 1); err == nil {
		t.Error("exceeding 2 PDFs per kind was accepted")
	}
}
