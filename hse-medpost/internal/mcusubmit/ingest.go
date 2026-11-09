package mcusubmit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxUploadBytes is the largest file we accept; a bigger one is refused outright. The portal does
// not shrink files to fit (owner, 2026-11-09): the limit is 10 MB because larger transfers stall on
// the way to the Pi, and the submitter splits or rescans a bigger file. The original is read into
// memory first, so this also bounds that.
const MaxUploadBytes = 10 << 20

// PortalTargetBytes is the size limit of a stored checkup PDF. It equals the upload limit on
// purpose: the Ghostscript rebuild is kept for safety (it drops scripts and attachments), but it
// writes the pages back losslessly and the stronger shrinking passes only run if that rebuild
// itself came out above the limit, which is rare. Nothing is squeezed to a small size any more.
const PortalTargetBytes = MaxUploadBytes

// Scanner is an optional virus scanner. It returns the signature name when the file is infected
// and "" when it is clean. Any error means "could not tell", which is never treated as clean.
type Scanner interface {
	Scan(ctx context.Context, path string) (signature string, err error)
}

// InfectedError carries the signature the scanner reported. It matches ErrInfected.
type InfectedError struct{ Signature string }

func (e *InfectedError) Error() string {
	return "mcusubmit: the file is infected (" + e.Signature + ")"
}

func (e *InfectedError) Is(target error) bool { return target == ErrInfected }

// Stored describes the file that was kept.
type Stored struct {
	Ref    string // FileStore reference, kept in the database
	Sha256 string // hash of the stored (rebuilt) file
	Bytes  int
	Pages  int
}

// Pipeline is everything that happens to an upload before it is kept:
//
//	size cap -> %PDF- header -> structure check -> (optional virus scan) -> Ghostscript rebuild -> store
//
// The upload itself is never stored. Only the rebuilt file is, so whatever a crafted PDF hides in
// its own structure does not reach the reviewers' machines. The scan, when configured, looks at
// the original: it is an alarm that tells us someone is sending malware, not the protection.
type Pipeline struct {
	Store     FileStore
	Rebuilder *Rebuilder
	Scanner   Scanner
	// TargetBytes is the size limit of the stored file; zero means PortalTargetBytes.
	TargetBytes int64
}

// Ingest runs the whole pipeline. On any error nothing is stored and no temporary file is left.
func (p *Pipeline) Ingest(ctx context.Context, r io.Reader) (Stored, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return Stored{}, fmt.Errorf("reading upload: %w", err)
	}
	if len(data) > MaxUploadBytes {
		return Stored{}, ErrTooLarge
	}
	if err := CheckStructure(data); err != nil {
		return Stored{}, err
	}

	// The working folder is private (0700) and removed on every path out of this function; the
	// sandboxed converter can see this folder and nothing else.
	dir, err := os.MkdirTemp("", "mcu-pdf-")
	if err != nil {
		return Stored{}, fmt.Errorf("working folder: %w", err)
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return Stored{}, fmt.Errorf("working copy: %w", err)
	}

	if p.Scanner != nil {
		sig, err := p.Scanner.Scan(ctx, in)
		if err != nil {
			return Stored{}, fmt.Errorf("%w: %v", ErrScanUnavailable, err)
		}
		if sig != "" {
			return Stored{}, &InfectedError{Signature: sig}
		}
	}

	target := p.TargetBytes
	if target <= 0 {
		target = PortalTargetBytes
	}
	out, pages, err := p.Rebuilder.Rebuild(ctx, dir, in, target)
	if err != nil {
		return Stored{}, err
	}
	rebuilt, err := os.ReadFile(out)
	if err != nil {
		return Stored{}, fmt.Errorf("reading rebuilt file: %w", err)
	}
	ref, err := p.Store.Put(ctx, bytes.NewReader(rebuilt))
	if err != nil {
		return Stored{}, fmt.Errorf("storing file: %w", err)
	}
	sum := sha256.Sum256(rebuilt)
	return Stored{Ref: ref, Sha256: hex.EncodeToString(sum[:]), Bytes: len(rebuilt), Pages: pages}, nil
}
