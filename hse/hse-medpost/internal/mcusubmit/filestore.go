package mcusubmit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// FileStore keeps the stored PDFs. The database holds only the reference it returns, so moving
// the files elsewhere later (another disk, object storage) means writing a new FileStore, not
// changing the tables.
type FileStore interface {
	// Put stores the bytes from r and returns the reference to keep in the database.
	Put(ctx context.Context, r io.Reader) (string, error)
	// Open returns the stored bytes, or ErrNotFound.
	Open(ctx context.Context, ref string) (io.ReadCloser, error)
	// Delete removes the file. Deleting something that is already gone is not an error.
	Delete(ctx context.Context, ref string) error
}

// LocalDiskStore keeps files on this machine's disk under one folder.
type LocalDiskStore struct {
	root string
}

// errBadRef is returned for a reference this store could not have produced.
var errBadRef = errors.New("mcusubmit: invalid file reference")

// refPattern is the only shape of reference we hand out: a two-letter shard folder (so one folder
// never holds thousands of files) and a random name. Checking against it before touching the
// disk means a tampered database row cannot point at ../../etc/passwd.
var refPattern = regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{32}\.pdf$`)

// NewLocalDiskStore creates the folder (mode 0700) if needed.
func NewLocalDiskStore(root string) (*LocalDiskStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	return &LocalDiskStore{root: abs}, nil
}

func (s *LocalDiskStore) path(ref string) (string, error) {
	if !refPattern.MatchString(ref) {
		return "", errBadRef
	}
	return filepath.Join(s.root, ref), nil
}

// Put writes the bytes under a random name. The name says nothing about the person or the
// document, so a directory listing of the disk reveals nothing.
func (s *LocalDiskStore) Put(ctx context.Context, r io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("file store: %w", err)
	}
	name := hex.EncodeToString(raw)
	ref := name[:2] + "/" + name + ".pdf"
	full, err := s.path(ref)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return "", fmt.Errorf("file store: %w", err)
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("file store: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(full)
		return "", fmt.Errorf("file store: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(full)
		return "", fmt.Errorf("file store: %w", err)
	}
	return ref, nil
}

func (s *LocalDiskStore) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	full, err := s.path(ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	return f, nil
}

func (s *LocalDiskStore) Delete(ctx context.Context, ref string) error {
	full, err := s.path(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("file store: %w", err)
	}
	return nil
}
