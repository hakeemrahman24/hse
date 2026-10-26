package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RecompressOptions drives RecompressTree.
type RecompressOptions struct {
	Root      string // uploads folder to walk
	BackupDir string // originals are copied here, mirroring their path under Root
	MinBytes  int64  // only PDFs larger than this are touched
	Apply     bool   // false: work out the savings but change nothing
}

// RecompressResult describes one PDF. After equals Before when the file
// could not be made smaller; NewHash is the SHA-256 of the replacement.
type RecompressResult struct {
	Path     string
	Before   int64
	After    int64
	Replaced bool
	NewHash  string
	Err      error
}

// RecompressTree runs CompressPDF over every PDF under opts.Root that is
// larger than opts.MinBytes. With Apply set, each original is copied to
// BackupDir first and then replaced in place (same name, so no database path
// changes). each is called once per PDF that was looked at, in path order.
func RecompressTree(opts RecompressOptions, each func(RecompressResult)) error {
	backupAbs, _ := filepath.Abs(opts.BackupDir)
	return filepath.WalkDir(opts.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if abs, _ := filepath.Abs(path); backupAbs != "" && abs == backupAbs {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() <= opts.MinBytes || !isPDFFile(path) {
			return nil
		}
		each(recompressOne(opts, path, info))
		return nil
	})
}

func recompressOne(opts RecompressOptions, path string, info fs.FileInfo) RecompressResult {
	res := RecompressResult{Path: path, Before: info.Size(), After: info.Size()}
	src, err := os.Open(path)
	if err != nil {
		res.Err = err
		return res
	}
	defer src.Close()
	packed, err := CompressPDF(src)
	if err != nil {
		res.Err = err
		return res
	}
	defer packed.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".recompress-*.tmp")
	if err != nil {
		res.Err = err
		return res
	}
	defer os.Remove(tmp.Name()) // gone after a successful rename too
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), packed)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		res.Err = err
		return res
	}
	if n >= info.Size() {
		return res // nothing gained: leave the file alone
	}
	res.After, res.NewHash = n, hex.EncodeToString(h.Sum(nil))
	if !opts.Apply {
		return res
	}
	rel, err := filepath.Rel(opts.Root, path)
	if err != nil {
		res.Err = err
		return res
	}
	if err := copyFile(path, filepath.Join(opts.BackupDir, rel), info.Mode()); err != nil {
		res.Err = err
		return res
	}
	if err := os.Chmod(tmp.Name(), info.Mode()); err != nil {
		res.Err = err
		return res
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		res.Err = err
		return res
	}
	res.Replaced = true
	return res
}

func copyFile(from, to string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
