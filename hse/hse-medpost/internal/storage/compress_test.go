package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

// scanLikePDF builds a text page over a large, slightly noisy picture, which
// is what a phone scan looks like: big as a PDF, tiny once resampled.
func scanLikePDF(t *testing.T) []byte {
	t.Helper()
	const w, h = 2400, 3200
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			base := 150 + (x+y)/40
			n := rng.Intn(13) - 6
			img.Set(x, y, color.RGBA{uint8(base + n), uint8(base + n/2), uint8(base - n), 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.RegisterImageOptionsReader("scan", fpdf.ImageOptions{ImageType: "PNG"}, &pngBuf)
	pdf.ImageOptions("scan", 0, 0, 210, 297, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	pdf.SetFont("Helvetica", "", 18)
	pdf.Text(20, 30, "Attendance list HSE toolbox talk")
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func readAll(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCompressPDFShrinksLargeScanUnderTarget(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("ghostscript not installed")
	}
	in := scanLikePDF(t)
	if int64(len(in)) <= PDFTargetBytes {
		t.Fatalf("test PDF is only %d bytes, make it bigger", len(in))
	}
	rc, err := CompressPDF(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, rc)
	if int64(len(out)) > PDFTargetBytes {
		t.Fatalf("compressed to %d bytes, want <= %d (from %d)", len(out), PDFTargetBytes, len(in))
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("output is not a PDF: %q", out[:8])
	}
	if _, err := exec.LookPath("pdftotext"); err == nil {
		cmd := exec.Command("pdftotext", "-", "-")
		cmd.Stdin = bytes.NewReader(out)
		text, err := cmd.Output()
		if err != nil || !strings.Contains(string(text), "Attendance list") {
			t.Fatalf("text lost in compression: %q err=%v", text, err)
		}
	}
}

func TestCompressPDFLeavesOtherInputAlone(t *testing.T) {
	small := []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
	notPDF := bytes.Repeat([]byte("x"), int(PDFTargetBytes)+10)
	for name, in := range map[string][]byte{"small pdf": small, "large non-pdf": notPDF, "empty": {}} {
		rc, err := CompressPDF(bytes.NewReader(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := readAll(t, rc); !bytes.Equal(got, in) {
			t.Errorf("%s changed: %d -> %d bytes", name, len(in), len(got))
		}
	}
}

func TestCompressPDFKeepsUploadWhenGhostscriptMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	in := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("a"), int(PDFTargetBytes)+10)...)
	rc, err := CompressPDF(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc); !bytes.Equal(got, in) {
		t.Fatalf("upload changed without ghostscript: %d -> %d bytes", len(in), len(got))
	}
}

func TestRecompressTreeBacksUpAndReplaces(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("ghostscript not installed")
	}
	root, backup := t.TempDir(), t.TempDir()
	big := scanLikePDF(t)
	sub := filepath.Join(root, "prog", "activities")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bigPath := filepath.Join(sub, "big.pdf")
	smallPath := filepath.Join(sub, "small.pdf")
	photoPath := filepath.Join(sub, "photo.jpg")
	small := []byte("%PDF-1.4\n%%EOF\n")
	for path, data := range map[string][]byte{bigPath: big, smallPath: small, photoPath: big} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := RecompressOptions{Root: root, BackupDir: backup, MinBytes: 2_000_000}

	var seen []RecompressResult
	collect := func(r RecompressResult) { seen = append(seen, r) }

	// Dry run: reports the saving, touches nothing.
	if err := RecompressTree(opts, collect); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Path != bigPath || seen[0].After >= seen[0].Before || seen[0].Replaced {
		t.Fatalf("dry run results = %+v", seen)
	}
	if got, _ := os.ReadFile(bigPath); !bytes.Equal(got, big) {
		t.Fatal("dry run changed the file")
	}

	// Apply: original saved in backup, file replaced, hash is of the new bytes.
	seen = nil
	opts.Apply = true
	if err := RecompressTree(opts, collect); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !seen[0].Replaced || seen[0].Err != nil {
		t.Fatalf("apply results = %+v", seen)
	}
	saved, _ := os.ReadFile(filepath.Join(backup, "prog", "activities", "big.pdf"))
	if !bytes.Equal(saved, big) {
		t.Fatal("backup is not the original")
	}
	now, _ := os.ReadFile(bigPath)
	if int64(len(now)) != seen[0].After || len(now) >= len(big) {
		t.Fatalf("file is %d bytes, result says %d, original %d", len(now), seen[0].After, len(big))
	}
	sum := sha256.Sum256(now)
	if hex.EncodeToString(sum[:]) != seen[0].NewHash {
		t.Fatal("NewHash does not match the stored bytes")
	}
	if left, _ := filepath.Glob(filepath.Join(sub, ".recompress-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
