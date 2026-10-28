package mcusubmit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"hse-medpost/internal/storage"
)

// rebuildTimeout bounds one Ghostscript run. A scan of many pages on the Pi can take a minute;
// anything near this limit is more likely a crafted file than a real document.
const rebuildTimeout = 120 * time.Second

// Rebuilder turns an uploaded PDF into a new one with Ghostscript. Ghostscript interprets the
// whole file and writes a fresh PDF from what it drew: page content, fonts and images survive;
// scripts, launch actions, embedded files, live form fields, link annotations and the original's
// odd structure do not. (A catalog-level URI action can survive in Ghostscript 10.x; it only
// opens a web address after the viewer asks, and CheckStructure still runs on the output.) That is why the rebuild always runs, even for a small file, and why a failure
// refuses the upload instead of keeping the original (CompressPDF in internal/storage does the
// opposite on purpose: it must never block an activity upload).
type Rebuilder struct {
	gs      string
	sandbox Sandbox
	slot    chan struct{}
}

// NewRebuilder finds Ghostscript and decides how to sandbox it: bubblewrap when a trial run
// works, otherwise resource limits only (logged, because it is a weaker setup).
func NewRebuilder(ctx context.Context) (*Rebuilder, error) {
	gs, err := exec.LookPath("gs")
	if err != nil {
		return nil, fmt.Errorf("mcusubmit: ghostscript is required for the upload pipeline: %w", err)
	}
	r := &Rebuilder{gs: gs, slot: make(chan struct{}, 1)} // one run at a time: the Pi has little RAM
	if bwrap, err := exec.LookPath("bwrap"); err == nil && bwrapWorks(bwrap) {
		r.sandbox = Sandbox{Bwrap: bwrap}
	} else {
		log.Printf("mcusubmit: bubblewrap is not usable, running ghostscript with resource limits only")
	}
	return r, nil
}

// losslessArgs rewrites the file without touching image quality. Together with the shrinking
// passes it means a small, clean file is not degraded, while a big one is squeezed only as far
// as needed.
func losslessArgs(in, out string) []string {
	return []string{
		"-sDEVICE=pdfwrite", "-dNOPAUSE", "-dBATCH", "-dQUIET", "-dSAFER",
		"-dCompatibilityLevel=1.4", "-dPrinted=true", "-dPreserveAnnots=false",
		"-dDownsampleColorImages=false", "-dDownsampleGrayImages=false", "-dDownsampleMonoImages=false",
		"-dDetectDuplicateImages=true", "-dSubsetFonts=true", "-dCompressFonts=true",
		"-sOutputFile=" + out, in,
	}
}

// Rebuild writes a rebuilt copy of in (a file inside workDir) and returns its path and page
// count. It tries the lossless pass first, then the shrinking passes of the activity uploads,
// stopping at the first result within target and otherwise keeping the smallest. Every result must
// start and end like a PDF, have the same number of pages as the input and pass CheckStructure.
func (r *Rebuilder) Rebuild(ctx context.Context, workDir, in string, target int64) (string, int, error) {
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	pages := r.countPages(ctx, workDir, in)
	if pages < 1 {
		return "", 0, fmt.Errorf("%w: no pages found", ErrRebuild)
	}

	best, bestSize := "", int64(-1)
	for i := -1; i < storage.PDFPassCount(); i++ {
		out := filepath.Join(workDir, fmt.Sprintf("out%d.pdf", i+1))
		size, err := r.runPass(ctx, workDir, in, out, i, pages)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			log.Printf("mcusubmit: rebuild pass %d failed: %v", i+1, err)
			continue
		}
		if bestSize < 0 || size < bestSize {
			best, bestSize = out, size
		}
		if size <= target {
			break
		}
	}
	switch {
	case best == "":
		return "", 0, ErrRebuild
	case bestSize > target:
		return "", 0, fmt.Errorf("%w: smallest result is %d bytes, limit %d", ErrCannotShrink, bestSize, target)
	}
	return best, pages, nil
}

// runPass runs one Ghostscript pass (-1 is the lossless one) and validates the result.
func (r *Rebuilder) runPass(ctx context.Context, workDir, in, out string, pass, wantPages int) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, rebuildTimeout)
	defer cancel()

	args := losslessArgs(in, out)
	if pass >= 0 {
		// Same arguments as the activity uploads, so both paths shrink identically.
		args = storage.GhostscriptCommand(ctx, r.gs, in, out, pass).Args[1:]
	}
	if output, err := r.sandbox.Command(ctx, workDir, r.gs, args...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("ghostscript: %w: %s", err, bytes.TrimSpace(clip(output, 300)))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return 0, err
	}
	if !looksComplete(data) {
		return 0, errors.New("output is not a complete PDF")
	}
	if err := CheckStructure(data); err != nil {
		return 0, fmt.Errorf("output refused: %w", err)
	}
	if got := r.countPages(ctx, workDir, out); got != wantPages {
		return 0, fmt.Errorf("page count changed %d -> %d", wantPages, got)
	}
	return int64(len(data)), nil
}

// countPages counts pages with Ghostscript's bbox device, which prints one %%BoundingBox line per
// page. It does not depend on the PostScript PDF-interpreter internals that changed between
// Ghostscript 10.00 and later versions.
func (r *Rebuilder) countPages(ctx context.Context, workDir, path string) int {
	ctx, cancel := context.WithTimeout(ctx, rebuildTimeout)
	defer cancel()
	out, _ := r.sandbox.Command(ctx, workDir, r.gs,
		"-q", "-dNOPAUSE", "-dBATCH", "-dSAFER", "-sDEVICE=bbox", "-r10", path).CombinedOutput()
	return bytes.Count(out, []byte("%%BoundingBox:"))
}

// looksComplete: starts with the PDF header and ends with %%EOF, so a half-written file is never kept.
func looksComplete(b []byte) bool {
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		return false
	}
	tail := b
	if len(tail) > 64 {
		tail = tail[len(tail)-64:]
	}
	return bytes.Contains(tail, []byte("%%EOF"))
}

func clip(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
