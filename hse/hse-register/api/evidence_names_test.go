package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var created = time.Date(2026, time.October, 16, 10, 30, 0, 0, time.UTC)

func TestReadableNameIsTraceableAndSortable(t *testing.T) {
	cases := []struct {
		kind, fileType, display string
		seq                     int
		want                    string
	}{
		{"before", "photo", "", 1, "2026/10/OBS-000123/OBS-000123_before_photo_01.jpg"},
		{"after", "photo", "", 12, "2026/10/OBS-000123/OBS-000123_after_photo_12.jpg"},
		{"before", "pdf", "Scaffold Inspection #4 (Final).pdf", 2, "2026/10/OBS-000123/OBS-000123_before_pdf_02_scaffold-inspection-4-final.pdf"},
		{"after", "pdf", "", 1, "2026/10/OBS-000123/OBS-000123_after_pdf_01.pdf"},
		{"after", "pdf", "Laporan Perbaikan Tangga.pdf", 1, "2026/10/OBS-000123/OBS-000123_after_pdf_01_laporan-perbaikan-tangga.pdf"},
	}
	for _, c := range cases {
		if got := readableName(123, created, c.kind, c.fileType, c.seq, c.display); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	// January sorts after nothing it should not: months are two digits.
	if got := readableName(5, time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC), "before", "photo", 1, ""); !strings.HasPrefix(got, "2027/01/OBS-000005/") {
		t.Errorf("month is zero padded: %q", got)
	}
}

func TestSlugOfKeepsASCIIWordsOnly(t *testing.T) {
	cases := map[string]string{
		"Report.pdf":                     "report",
		"../../etc/passwd":               "etc-passwd",
		"  ***  ":                        "",
		"Ünïcode name.pdf":               "n-code-name",
		strings.Repeat("a", 80) + ".pdf": strings.Repeat("a", 40),
	}
	for in, want := range cases {
		if got := slugOf(in); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanMovesNumbersAfterTheExistingFilesAndSkipsTakenNames(t *testing.T) {
	rows := []evidenceRow{
		{1, "before", "photo", "2026/10/OBS-000007/OBS-000007_before_photo_01.jpg", ""},
		{2, "before", "photo", "aaaa.jpg", ""},
		{3, "before", "photo", "bbbb.jpg", ""},
		{4, "after", "photo", "cccc.jpg", ""},
		{5, "before", "pdf", "dddd.pdf", "Plan A.pdf"},
	}
	taken := map[string]bool{"2026/10/OBS-000007/OBS-000007_before_photo_03.jpg": true} // already on disk
	moves := planMoves(7, created, rows, func(rel string) bool { return taken[rel] })
	want := []string{
		"2026/10/OBS-000007/OBS-000007_before_photo_02.jpg",
		"2026/10/OBS-000007/OBS-000007_before_photo_04.jpg", // 03 is taken
		"2026/10/OBS-000007/OBS-000007_after_photo_01.jpg",
		"2026/10/OBS-000007/OBS-000007_before_pdf_01_plan-a.pdf",
	}
	if len(moves) != len(want) {
		t.Fatalf("moves = %+v", moves)
	}
	for i, m := range moves {
		if m.To != want[i] {
			t.Errorf("move %d: %q, want %q", i, m.To, want[i])
		}
	}
	if moves[0].From != "aaaa.jpg" || moves[0].RowID != 2 {
		t.Errorf("first move is for the first old file: %+v", moves[0])
	}
}

func TestEvidencePathRefusesAnythingOutsideTheFolder(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "..", "../x.jpg", "a/../../x.jpg", "/etc/passwd", "a\x00b"} {
		if _, ok := evidencePath(dir, bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, good := range []string{"abc123.jpg", "2026/10/OBS-000001/OBS-000001_before_photo_01.jpg"} {
		p, ok := evidencePath(dir, good)
		if !ok || !strings.HasPrefix(p, dir+string(filepath.Separator)) {
			t.Errorf("%q should resolve inside the folder, got %q %v", good, p, ok)
		}
	}
}

func TestRemovingTheLastFileRemovesTheEmptyFolders(t *testing.T) {
	dir := t.TempDir()
	rel := "2026/10/OBS-000009/OBS-000009_before_photo_01.jpg"
	other := "2026/10/OBS-000010/OBS-000010_before_photo_01.jpg"
	for _, r := range []string{rel, other} {
		p, _ := evidencePath(dir, r)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	removePhotoFiles(dir, []string{rel, "../outside.jpg"})
	if _, err := os.Stat(filepath.Join(dir, "2026/10/OBS-000009")); !os.IsNotExist(err) {
		t.Error("the emptied observation folder should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, other)); err != nil {
		t.Error("another observation's file must stay")
	}
	if _, err := os.Stat(filepath.Join(dir, "2026/10")); err != nil {
		t.Error("a folder that still holds something must stay")
	}
}
