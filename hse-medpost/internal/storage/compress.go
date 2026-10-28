package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// PDFTargetBytes is the size an uploaded PDF is squeezed to when it is larger.
const PDFTargetBytes = 1_000_000

const (
	compressTimeout = 120 * time.Second
	pdfMagic        = "%PDF-"
)

// pdfPass is one Ghostscript attempt. Passes go from gentle to strong and
// stop at the first one that reaches PDFTargetBytes, so a document is never
// squeezed harder than it needs to be. 100 dpi is the floor: a scanned page
// stays legible there, below it small print starts to smear.
type pdfPass struct {
	dpi     int
	qFactor string // Ghostscript JPEG quality: lower is better quality
}

var pdfPasses = []pdfPass{
	{dpi: 150, qFactor: "0.40"},
	{dpi: 120, qFactor: "0.76"},
	{dpi: 100, qFactor: "1.30"},
}

// gsLimit lets one compression run at a time: Ghostscript on a large scan
// takes a few hundred MB, and the Pi has 2 GB shared with the database.
var gsLimit = make(chan struct{}, 1)

// CompressPDF reads src and returns a reader over the bytes to store.
//
// A PDF over PDFTargetBytes is rewritten with Ghostscript into a flat
// copy: images downsampled and recompressed, transparency flattened, form
// fields and annotations printed into the page. Text and vector drawing stay
// sharp. Anything else comes back unchanged: other file types, a PDF already
// under the target, a missing Ghostscript, a failed or timed-out run, or a
// result that is not smaller. Compression never blocks an upload.
//
// Close the result: it removes the temporary files.
func CompressPDF(src io.Reader) (io.ReadCloser, error) {
	dir, err := os.MkdirTemp("", "pdfz")
	if err != nil {
		return nil, err
	}
	in := filepath.Join(dir, "in.pdf")
	if err := spool(in, src); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	chosen := in
	if best, ok := compressFile(dir, in); ok {
		chosen = best
	}
	f, err := os.Open(chosen)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &tempFile{File: f, dir: dir}, nil
}

type tempFile struct {
	*os.File
	dir string
}

func (t *tempFile) Close() error {
	err := t.File.Close()
	os.RemoveAll(t.dir)
	return err
}

func spool(path string, src io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// compressFile returns the path of a smaller rewrite of in, if one exists.
func compressFile(dir, in string) (string, bool) {
	info, err := os.Stat(in)
	if err != nil || info.Size() <= PDFTargetBytes || !isPDFFile(in) {
		return "", false
	}
	gs, err := exec.LookPath("gs")
	if err != nil {
		log.Printf("pdf compress: ghostscript not installed, keeping %d bytes as uploaded", info.Size())
		return "", false
	}
	gsLimit <- struct{}{}
	defer func() { <-gsLimit }()

	best, bestSize := "", info.Size()
	for i, p := range pdfPasses {
		out := filepath.Join(dir, fmt.Sprintf("out%d.pdf", i))
		if err := runGhostscript(gs, in, out, p); err != nil {
			log.Printf("pdf compress: %d dpi pass failed: %v", p.dpi, err)
			continue
		}
		size, ok := validPDFSize(out)
		if !ok {
			continue
		}
		// A rewrite that lost pages is worse than a big file.
		if before, ok := pdfPageCount(gs, in); ok {
			if after, ok := pdfPageCount(gs, out); !ok || after != before {
				log.Printf("pdf compress: %d dpi pass changed page count %d -> %d, discarded", p.dpi, before, after)
				continue
			}
		}
		if size < bestSize {
			best, bestSize = out, size
		}
		if size <= PDFTargetBytes {
			break
		}
	}
	if best == "" {
		log.Printf("pdf compress: no smaller result for %d bytes, keeping the upload", info.Size())
		return "", false
	}
	log.Printf("pdf compress: %d -> %d bytes", info.Size(), bestSize)
	return best, true
}

// PDFPassCount is how many shrinking passes there are, gentlest first (see GhostscriptCommand).
func PDFPassCount() int { return len(pdfPasses) }

// GhostscriptCommand builds the Ghostscript command for shrinking pass number pass (0 is the
// gentlest). It is exported so the medical checkup portal runs exactly the same rewrite as the
// activity uploads, but inside its own sandbox and failing closed instead of keeping the upload.
func GhostscriptCommand(ctx context.Context, gs, in, out string, pass int) *exec.Cmd {
	return gsCommand(ctx, gs, in, out, pdfPasses[pass])
}

func runGhostscript(gs, in, out string, p pdfPass) error {
	ctx, cancel := context.WithTimeout(context.Background(), compressTimeout)
	defer cancel()
	cmd := gsCommand(ctx, gs, in, out, p)
	output, err := cmd.CombinedOutput()
	if err != nil {
		output = bytes.TrimSpace(output)
		if len(output) > 300 {
			output = output[:300]
		}
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}

func gsCommand(ctx context.Context, gs, in, out string, p pdfPass) *exec.Cmd {
	dpi := strconv.Itoa(p.dpi)
	monoDPI := strconv.Itoa(p.dpi * 2)
	jpeg := "/QFactor " + p.qFactor + " /Blend 1 /HSamples [2 1 1 2] /VSamples [2 1 1 2]"
	ps := "<< /ColorACSImageDict << " + jpeg + " >> /GrayACSImageDict << " + jpeg + " >> >> setdistillerparams"
	cmd := exec.CommandContext(ctx, gs,
		"-sDEVICE=pdfwrite", "-dNOPAUSE", "-dBATCH", "-dQUIET", "-dSAFER",
		// 1.4 keeps one flat layer of drawing; forms and annotations are
		// printed into the page rather than carried along as live widgets.
		"-dCompatibilityLevel=1.4", "-dPrinted=true", "-dPreserveAnnots=false",
		"-dDownsampleColorImages=true", "-dColorImageDownsampleType=/Bicubic", "-dColorImageResolution="+dpi,
		"-dDownsampleGrayImages=true", "-dGrayImageDownsampleType=/Bicubic", "-dGrayImageResolution="+dpi,
		"-dDownsampleMonoImages=true", "-dMonoImageDownsampleType=/Subsample", "-dMonoImageResolution="+monoDPI,
		"-dAutoFilterColorImages=false", "-dColorImageFilter=/DCTEncode",
		"-dAutoFilterGrayImages=false", "-dGrayImageFilter=/DCTEncode",
		"-dDetectDuplicateImages=true", "-dSubsetFonts=true", "-dCompressFonts=true",
		"-sOutputFile="+out,
		"-c", ps, "-f", in,
	)
	return cmd
}

// pdfPageCount asks Ghostscript for a file's page count. The path goes in as
// a -s string, not into the PostScript, so it needs no escaping.
func pdfPageCount(gs, path string) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gs, "-q", "-dNODISPLAY", "-dNOSAFER", "-sFile="+path,
		"-c", "File (r) file runpdfbegin pdfpagecount = quit").Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(bytes.TrimSpace(out)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func isPDFFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return string(head) == pdfMagic
}

// validPDFSize reports the size of a Ghostscript result that starts like a
// PDF and ends with its %%EOF marker, so a half-written file is never kept.
func validPDFSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 || !isPDFFile(path) {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	tail := make([]byte, 64)
	n := int64(len(tail))
	if info.Size() < n {
		n = info.Size()
	}
	if _, err := f.ReadAt(tail[:n], info.Size()-n); err != nil && err != io.EOF {
		return 0, false
	}
	if !bytes.Contains(tail[:n], []byte("%%EOF")) {
		return 0, false
	}
	return info.Size(), true
}
