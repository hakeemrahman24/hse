package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	pdf = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n")
	png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	jpg = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
)

func TestPathOnlyOpensWhatSaveWrote(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	rel, err := s.Save("WEP-AB12C", "id_doc", pdf, 1024)
	if err != nil {
		t.Fatal(err)
	}
	got, ctype, err := s.Path(rel)
	if err != nil || got != filepath.Join(s.Dir, rel) || ctype != "application/pdf" {
		t.Fatalf("path %q type %q err %v", got, ctype, err)
	}
	for _, c := range []struct{ rel, want string }{{"WEP-AB12C/a.jpg", "image/jpeg"}, {"WVR-AB12C/b_c.png", "image/png"}} {
		if _, ctype, err := s.Path(c.rel); err != nil || ctype != c.want {
			t.Errorf("%s: type %q err %v", c.rel, ctype, err)
		}
	}
	// Anything that is not exactly <request id>/<label>.<pdf|jpg|png> is refused, whatever it points at.
	for _, bad := range []string{
		"", "../etc/passwd", "WEP-AB12C/../../etc/passwd", "/etc/passwd", "WEP-AB12C/id_doc.exe", "WEP-AB12C/id_doc",
		"WEP-AB12C/sub/id_doc.pdf", "wep-ab12c/id_doc.pdf", "WEP-AB12C\\id_doc.pdf", "WEP-AB12C/ID_DOC.pdf", "WEP-AB12C/id_doc.pdf\x00.png",
	} {
		if _, _, err := s.Path(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestSaveKeepsPdfAndImagesAndNamesThemByContent(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, c := range []struct {
		data []byte
		ext  string
	}{{pdf, ".pdf"}, {png, ".png"}, {jpg, ".jpg"}} {
		rel, err := s.Save("WEP-AB12C", "id_doc", c.data, 1024)
		if err != nil {
			t.Fatalf("%s: %v", c.ext, err)
		}
		if rel != "WEP-AB12C/id_doc"+c.ext {
			t.Errorf("rel = %q, want extension %s", rel, c.ext)
		}
		got, err := os.ReadFile(filepath.Join(s.Dir, rel))
		if err != nil || string(got) != string(c.data) {
			t.Errorf("file not stored intact: %v", err)
		}
	}
	info, _ := os.Stat(filepath.Join(s.Dir, "WEP-AB12C"))
	if info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v, want 0700", info.Mode().Perm())
	}
}

func TestSaveRefusesBadFiles(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	cases := map[string][]byte{
		"empty":      {},
		"text":       []byte("hello, this is not a document"),
		"html":       []byte("<html><script>alert(1)</script></html>"),
		"exe":        []byte("MZ\x90\x00\x03\x00\x00\x00"),
		"too large":  append(append([]byte{}, pdf...), make([]byte, 2000)...),
		"pdf-in-txt": []byte("x%PDF-1.4"),
	}
	for name, data := range cases {
		if _, err := s.Save("WEP-AB12C", "id_doc", data, 1024); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(s.Dir, "WEP-AB12C")); err == nil && len(entries) != 0 {
		t.Error("a refused file left something behind")
	}
}

func TestSaveRefusesUnsafeNames(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, id := range []string{"", "../x", "WEP-AB12C/..", "wep-ab12c", "WEP AB12C"} {
		if _, err := s.Save(id, "id_doc", pdf, 1024); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
	for _, kind := range []string{"", "../a", "a/b", "ID DOC"} {
		if _, err := s.Save("WEP-AB12C", kind, pdf, 1024); err == nil {
			t.Errorf("kind %q accepted", kind)
		}
	}
}

func TestRemoveDeletesTheRequestFolderOnly(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save("WEP-AB12C", "id_doc", pdf, 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("WEP-ZZ999", "id_doc", pdf, 1024); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("WEP-AB12C"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "WEP-AB12C")); !os.IsNotExist(err) {
		t.Error("folder still there")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "WEP-ZZ999", "id_doc.pdf")); err != nil {
		t.Error("another request's file was touched")
	}
	if err := s.Remove("../.."); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("unsafe remove was allowed: %v", err)
	}
}
