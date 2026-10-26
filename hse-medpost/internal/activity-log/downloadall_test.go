package activitylog

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanDownloadAllBatchesSplitsWithinProgramByBytes(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "prog-a", "act-1")
	if err := os.MkdirAll(program, 0o755); err != nil {
		t.Fatal(err)
	}
	fileSize := int64(downloadAllBatchByteCap * 2 / 5)
	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf"} {
		if err := os.WriteFile(filepath.Join(program, name), make([]byte, fileSize), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	batches, err := planDownloadAllBatches(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(batches) != 2 {
		t.Fatalf("got %d batches, want 2 (three files at two-fifths of the cap cannot fit in one)", len(batches))
	}
	for _, b := range batches {
		if b.Bytes > downloadAllBatchByteCap {
			t.Errorf("batch %d is %d bytes, over the %d cap", b.Index, b.Bytes, downloadAllBatchByteCap)
		}
		if len(b.Programs) != 1 || b.Programs[0] != "prog-a" {
			t.Errorf("batch %d programs = %v, want [prog-a]", b.Index, b.Programs)
		}
	}
}

// Incompressible files packed right up to the raw cap used to produce zips
// over 10 MB once headers were added. Every multi-file part must stay under.
func TestPlanDownloadAllBatchesCountsZipOverhead(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "prog-a")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	// 40 files of 256 KB hold exactly the raw cap between them.
	for i := 0; i < 40; i++ {
		data := make([]byte, 256*1024)
		for j := range data {
			data[j] = byte((i*131 + j*j*7 + j>>3) % 251)
		}
		if err := os.WriteFile(filepath.Join(folder, string(rune('a'+i%26))+string(rune('a'+i/26))+".bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	batches, err := planDownloadAllBatches(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 2 {
		t.Fatalf("got %d batch, want the cap to split 40x256KB", len(batches))
	}
	for _, b := range batches {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, f := range b.entries {
			addFileToZipEntry(zw, f.rel, filepath.Join(dir, filepath.FromSlash(f.rel)))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if buf.Len() > downloadAllBatchByteCap {
			t.Errorf("batch %d zip is %d bytes, over the %d cap", b.Index, buf.Len(), downloadAllBatchByteCap)
		}
	}
}
