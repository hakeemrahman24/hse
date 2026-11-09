package mcusubmit

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildPDF assembles a PDF from object bodies (object 1 is the first). The xref offsets are exact,
// so the file is what a well-behaved generator would write.
func buildPDF(objects ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func stream(dict, data string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
}

const helloContent = "BT /F1 24 Tf 72 700 Td (Hello MCU) Tj ET"

// plainPDF is a one-page file with some text; catalogExtra and pageExtra add entries to the
// catalog and page dictionaries (an action, an annotation list...).
func plainPDF(catalogExtra, pageExtra string, more ...string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R " + catalogExtra + " >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> " + pageExtra + " >>",
		stream("", helloContent),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	return buildPDF(append(objs, more...)...)
}

func hasGS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("ghostscript is not installed")
	}
}

// ---- structure check (option B) ---------------------------------------------------------------

func TestCheckStructureAcceptsAnOrdinaryPDF(t *testing.T) {
	if err := CheckStructure(plainPDF("", "")); err != nil {
		t.Fatalf("an ordinary PDF was refused: %v", err)
	}
	// An OpenAction that only goes to a page is common in harmless files and must not bounce them.
	if err := CheckStructure(plainPDF("/OpenAction [3 0 R /Fit]", "")); err != nil {
		t.Fatalf("a page-open action was refused: %v", err)
	}
}

func TestCheckStructureRefusesActiveContent(t *testing.T) {
	cases := map[string]string{
		"javascript":         "/OpenAction << /S /JavaScript /JS (app.alert(1)) >>",
		"JS key alone":       "/AA << /O << /JS (x) >> >>",
		"launch":             "/OpenAction << /S /Launch /F (cmd.exe) >>",
		"embedded file":      "/Names << /EmbeddedFiles << /Names [] >> >>",
		"rich media":         "/Names << /RichMedia 1 >>",
		"xfa form":           "/AcroForm << /XFA [] >>",
		"submit form":        "/OpenAction << /S /SubmitForm /F (http://x) >>",
		"obfuscated by #hex": "/OpenAction << /S /J#61vaScript >>",
		"obfuscated all hex": "/OpenAction << /S /#4A#61#76#61#53#63#72#69#70#74 >>",
		"obfuscated launch":  "/OpenAction << /S /L#61unch >>",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckStructure(plainPDF(extra, ""))
			if !errors.Is(err, ErrUnsafeContent) {
				t.Fatalf("error = %v, want ErrUnsafeContent", err)
			}
		})
	}
}

// Object streams hold dictionaries inside a compressed blob, so a plain byte search cannot see
// them. The check inflates them before looking.
func TestCheckStructureLooksInsideCompressedObjectStreams(t *testing.T) {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write([]byte("<< /S /JavaScript /JS (app.alert(1)) >>"))
	_ = w.Close()
	objStm := fmt.Sprintf("<< /Type /ObjStm /N 1 /First 5 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", z.Len(), z.String())

	err := CheckStructure(plainPDF("", "", objStm))
	if !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("JavaScript hidden in an object stream got through: %v", err)
	}
}

func TestCheckStructureRefusesAnObjectStreamThatInflatesWithoutEnd(t *testing.T) {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	chunk := make([]byte, 1<<20)
	for i := 0; i < maxInflateTotal/len(chunk)+8; i++ { // more than the limit, but tiny when compressed
		_, _ = w.Write(chunk)
	}
	_ = w.Close()
	objStm := fmt.Sprintf("<< /Type /ObjStm /N 1 /First 5 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", z.Len(), z.String())

	if err := CheckStructure(plainPDF("", "", objStm)); !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("a decompression bomb was not refused: %v", err)
	}
}

func TestCheckStructureRefusesEncryptedAndNonPDF(t *testing.T) {
	enc := bytes.Replace(plainPDF("", ""), []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 9 0 R"), 1)
	if err := CheckStructure(enc); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("encrypted = %v, want ErrEncrypted", err)
	}
	if err := CheckStructure([]byte("hello, this is not a pdf")); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("text = %v, want ErrNotPDF", err)
	}
	if err := CheckStructure([]byte("MZ\x90\x00 pretend executable %PDF- later")); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("a file that only mentions %%PDF- later = %v, want ErrNotPDF", err)
	}
}

func TestUnsafeErrorNamesTheFeature(t *testing.T) {
	err := CheckStructure(plainPDF("/OpenAction << /S /JavaScript >>", ""))
	var u *UnsafeError
	if !errors.As(err, &u) || u.Feature != "JavaScript" {
		t.Fatalf("error = %v, want an UnsafeError for JavaScript", err)
	}
}

// ---- file store -------------------------------------------------------------------------------

type memStore struct {
	mu    sync.Mutex
	files map[string][]byte
	next  int
}

func newMemStore() *memStore { return &memStore{files: map[string][]byte{}} }

func (m *memStore) Put(_ context.Context, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	ref := fmt.Sprintf("mem-%d", m.next)
	m.files[ref] = data
	return ref, nil
}

func (m *memStore) Open(_ context.Context, ref string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStore) Delete(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, ref)
	return nil
}

func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.files)
}

func TestLocalDiskStoreRoundTripPermissionsAndDelete(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcu-files")
	store, err := NewLocalDiskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref, err := store.Put(ctx, strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{32}\.pdf$`).MatchString(ref) {
		t.Fatalf("ref %q is not a random, name-free path", ref)
	}
	info, err := os.Stat(filepath.Join(root, ref))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stored file mode = %v, %v; want 0600", info, err)
	}
	rc, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "%PDF-1.4 fake" {
		t.Fatalf("read back %q", got)
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open after Delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("deleting twice must not fail: %v", err)
	}
}

// The reference is stored in the database. Even if a row were altered, it must not be able to
// point outside the storage folder.
func TestLocalDiskStoreRefusesAPathOutsideItsFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcu-files")
	store, _ := NewLocalDiskStore(root)
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	_ = os.WriteFile(secret, []byte("secret"), 0o600)

	for _, ref := range []string{"../secret.txt", "/etc/passwd", "ab/../../secret.txt", "ab/short.pdf", ""} {
		if _, err := store.Open(context.Background(), ref); err == nil {
			t.Errorf("Open(%q) succeeded", ref)
		}
		if err := store.Delete(context.Background(), ref); err == nil {
			t.Errorf("Delete(%q) succeeded", ref)
		}
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("a traversal reference deleted a file outside the folder")
	}
}

// ---- sandbox (option C) -----------------------------------------------------------------------

func TestSandboxHidesTheRestOfTheMachine(t *testing.T) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bubblewrap is not installed")
	}
	s := Sandbox{Bwrap: bwrap}
	secretDir := t.TempDir() // under /tmp, which the sandbox replaces with an empty one
	secret := filepath.Join(secretDir, "secret.txt")
	_ = os.WriteFile(secret, []byte("do not leak"), 0o600)
	work := t.TempDir()
	visible := filepath.Join(work, "visible.txt")
	_ = os.WriteFile(visible, []byte("shared with the job"), 0o600)

	out, err := s.Command(context.Background(), work, "/usr/bin/cat", visible).Output()
	if err != nil || string(out) != "shared with the job" {
		t.Fatalf("the working folder must be readable: %q, %v", out, err)
	}
	if out, err := s.Command(context.Background(), work, "/usr/bin/cat", secret).Output(); err == nil {
		t.Fatalf("the sandbox could read a file outside its working folder: %q", out)
	}
	// Whatever the job writes outside its folder goes into a throwaway filesystem.
	_ = s.Command(context.Background(), work, "/usr/bin/touch", "/tmp/escaped").Run()
	if _, err := os.Stat("/tmp/escaped"); err == nil {
		os.Remove("/tmp/escaped")
		t.Fatal("the sandbox wrote a file onto the real /tmp")
	}
}

func TestSandboxStopsAJobThatRunsTooLong(t *testing.T) {
	s := Sandbox{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Command(ctx, t.TempDir(), "/bin/sleep", "30").Run()
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("a long job was not stopped quickly: %v after %v", err, time.Since(start))
	}
}

// ---- rebuild and ingest (option A) ------------------------------------------------------------

type fakeScanner struct {
	signature string
	err       error
	calls     int
}

func (f *fakeScanner) Scan(context.Context, string) (string, error) {
	f.calls++
	return f.signature, f.err
}

func newPipeline(t *testing.T) (*Pipeline, *memStore) {
	t.Helper()
	hasGS(t)
	rb, err := NewRebuilder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	return &Pipeline{Store: store, Rebuilder: rb}, store
}

func ingest(t *testing.T, p *Pipeline, data []byte) (Stored, error) {
	t.Helper()
	return p.Ingest(context.Background(), bytes.NewReader(data))
}

func TestIngestStoresARebuiltPDFAndOnlyThat(t *testing.T) {
	p, store := newPipeline(t)
	original := plainPDF("", "")

	st, err := ingest(t, p, original)
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := store.Open(context.Background(), st.Ref)
	stored, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.HasPrefix(stored, []byte("%PDF-")) || st.Pages != 1 || st.Bytes != len(stored) {
		t.Fatalf("stored = %d bytes, result %+v", len(stored), st)
	}
	sum := sha256.Sum256(stored)
	if st.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatal("the recorded hash is not the hash of the stored file")
	}
	if bytes.Equal(stored, original) {
		t.Fatal("the stored file is the upload itself: the original must never be kept, only the rebuilt copy")
	}
	if store.count() != 1 {
		t.Fatalf("%d files stored, want exactly 1", store.count())
	}
}

// A page-open action that goes to a web address passes the structure check (it is not script).
// Ghostscript 10.x copies such a catalog-level URI action into its output (measured on 10.08), so
// the guarantee is narrower than "no actions": nothing that runs code or starts a program is
// stored, and link annotations on pages are gone. A viewer only opens the address after asking.
func TestRebuildKeepsNoCodeRunningActionsAndNoLinkAnnotations(t *testing.T) {
	p, store := newPipeline(t)
	link := "<< /Type /Annot /Subtype /Link /Rect [0 0 100 20] /A << /S /URI /URI (http://tracker.example/link) >> >>"
	st, err := ingest(t, p, plainPDF("/OpenAction << /S /URI /URI (http://tracker.example/x) >>", "/Annots [6 0 R]", link))
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := store.Open(context.Background(), st.Ref)
	out, _ := io.ReadAll(rc)
	rc.Close()
	for _, bad := range []string{"/Annots", "/Link", "tracker.example/link", "/JavaScript", "/JS", "/Launch"} {
		if bytes.Contains(out, []byte(bad)) {
			t.Errorf("the rebuilt file still contains %q", bad)
		}
	}
}

// A filled-in form field is printed into the page and the live field is gone, so the value stays
// readable but nothing in the file can run or be edited.
func TestRebuildFlattensFormFieldsButKeepsTheirText(t *testing.T) {
	p, store := newPipeline(t)
	widget := "<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V (FILLED-VALUE) /Rect [72 600 300 630] /AP << /N 7 0 R >> /F 4 >>"
	appearance := stream("/Type /XObject /Subtype /Form /BBox [0 0 228 30] /Resources << /Font << /F1 5 0 R >> >>", "BT /F1 12 Tf 2 10 Td (FILLED-VALUE) Tj ET")
	st, err := ingest(t, p, plainPDF("/AcroForm << /Fields [6 0 R] >>", "/Annots [6 0 R]", widget, appearance))
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := store.Open(context.Background(), st.Ref)
	out, _ := io.ReadAll(rc)
	rc.Close()
	for _, bad := range []string{"/Widget", "/AcroForm", "/Annots"} {
		if bytes.Contains(out, []byte(bad)) {
			t.Errorf("the rebuilt file still has %q: the form was not flattened", bad)
		}
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Log("pdftotext missing: skipping the check that the text survived")
		return
	}
	path := filepath.Join(t.TempDir(), "out.pdf")
	_ = os.WriteFile(path, out, 0o600)
	text, err := exec.Command("pdftotext", path, "-").Output()
	if err != nil || !strings.Contains(string(text), "FILLED-VALUE") || !strings.Contains(string(text), "Hello MCU") {
		t.Fatalf("the visible text did not survive the rebuild: %q, %v", text, err)
	}
}

func TestIngestRefusesBadUploadsAndStoresNothing(t *testing.T) {
	p, store := newPipeline(t)
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"plain text", []byte("not a pdf"), ErrNotPDF},
		{"empty", nil, ErrNotPDF},
		{"javascript", plainPDF("/OpenAction << /S /JavaScript /JS (x) >>", ""), ErrUnsafeContent},
		{"encrypted", bytes.Replace(plainPDF("", ""), []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 9 0 R"), 1), ErrEncrypted},
		{"too large", append([]byte("%PDF-1.4\n"), make([]byte, MaxUploadBytes)...), ErrTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ingest(t, p, c.data); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
	if store.count() != 0 {
		t.Fatalf("%d files were stored from refused uploads", store.count())
	}
}

func TestIngestRefusesAFileThatGhostscriptCannotRead(t *testing.T) {
	p, store := newPipeline(t)
	broken := []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ngarbage without pages\n")
	if _, err := ingest(t, p, broken); !errors.Is(err, ErrRebuild) {
		t.Fatalf("error = %v, want ErrRebuild (fail closed, never keep the upload)", err)
	}
	if store.count() != 0 {
		t.Fatal("a file that could not be rebuilt was stored")
	}
}

func TestIngestFailsClosedWhenTheRebuildCannotReachTheSizeLimit(t *testing.T) {
	p, store := newPipeline(t)
	p.TargetBytes = 200 // smaller than any real PDF, so no pass can reach it
	if _, err := ingest(t, p, plainPDF("", "")); !errors.Is(err, ErrCannotShrink) {
		t.Fatalf("error = %v, want ErrCannotShrink", err)
	}
	if store.count() != 0 {
		t.Fatal("an over-size file was stored")
	}
}

func TestIngestScannerVerdictsAreFinalAndFailClosed(t *testing.T) {
	p, store := newPipeline(t)

	p.Scanner = &fakeScanner{signature: "Eicar-Test-Signature"}
	_, err := ingest(t, p, plainPDF("", ""))
	var inf *InfectedError
	if !errors.As(err, &inf) || inf.Signature != "Eicar-Test-Signature" || !errors.Is(err, ErrInfected) {
		t.Fatalf("error = %v, want an InfectedError carrying the signature", err)
	}
	p.Scanner = &fakeScanner{err: errors.New("scanner down")}
	if _, err := ingest(t, p, plainPDF("", "")); !errors.Is(err, ErrScanUnavailable) {
		t.Fatalf("a scanner failure = %v, want ErrScanUnavailable (never treated as clean)", err)
	}
	if store.count() != 0 {
		t.Fatal("something was stored although the scan did not pass")
	}
	clean := &fakeScanner{}
	p.Scanner = clean
	if _, err := ingest(t, p, plainPDF("", "")); err != nil || clean.calls != 1 {
		t.Fatalf("clean scan = %v after %d calls", err, clean.calls)
	}
}

// The stored-file limit equals the upload limit, so the portal never has to squeeze a file that was
// accepted; this pins that and the unset-TargetBytes default.
func TestIngestDefaultLimitIsThePortalTarget(t *testing.T) {
	if PortalTargetBytes != MaxUploadBytes {
		t.Fatalf("PortalTargetBytes = %d, want it equal to MaxUploadBytes (%d)", PortalTargetBytes, MaxUploadBytes)
	}
	p, _ := newPipeline(t)
	if p.TargetBytes != 0 {
		t.Fatal("the pipeline must use the portal default when TargetBytes is unset")
	}
	if _, err := ingest(t, p, plainPDF("", "")); err != nil {
		t.Fatalf("default limit rejected an ordinary file: %v", err)
	}
}

func TestIngestWorksWithoutAScanner(t *testing.T) {
	p, _ := newPipeline(t)
	p.Scanner = nil
	if _, err := ingest(t, p, plainPDF("", "")); err != nil {
		t.Fatalf("the pipeline must work with no scanner: %v", err)
	}
}

func TestIngestCleansUpItsTemporaryFiles(t *testing.T) {
	p, _ := newPipeline(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	_, _ = ingest(t, p, plainPDF("", ""))
	_, _ = ingest(t, p, []byte("not a pdf"))
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Fatalf("%d temporary entries were left behind (the uploaded original must not linger)", len(entries))
	}
}
