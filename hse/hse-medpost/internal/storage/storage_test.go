package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolderSlug(t *testing.T) {
	if got := FolderSlug("FitnessForWork"); got != "FitnessForWork" {
		t.Fatalf("got %q", got)
	}
	if got := FolderSlug("Fatigue Management"); got != "FatigueManagement" {
		t.Fatalf("got %q", got)
	}
	if got := FolderSlug(""); got != medpostWideDir {
		t.Fatalf("got %q", got)
	}
}

func TestEvidenceDirAndName(t *testing.T) {
	dir := EvidenceDir("/data/uploads", "FitnessForWork", "Monitoring/Inspection", "Photos")
	want := filepath.Join("/data/uploads", "FitnessForWork", "activities", "Monitoring-Inspection", "Photos")
	if dir != want {
		t.Fatalf("dir=%q want=%q", dir, want)
	}
	name := EvidenceFilename("2026-08-22", 42, 3, "Photos", 1, "Gate Check.JPG")
	if !strings.HasPrefix(name, "2026-08-22_act42_staff3_Photos_01_gate-check_") {
		t.Fatalf("name=%q", name)
	}
	if !strings.HasSuffix(name, ".jpg") {
		t.Fatalf("ext=%q", name)
	}
}

func TestDocumentDirTemplate(t *testing.T) {
	dir := DocumentDir("/u", "FitnessForWork", "Template")
	want := filepath.Join("/u", medpostWideDir, "documents", "Template")
	if dir != want {
		t.Fatalf("dir=%q want=%q", dir, want)
	}
}

func TestMcuCertificateFilename(t *testing.T) {
	name := McuCertificateFilename("2026-09-27", "MedC0011", "Ahmad bin Ali", "2027-03-15")
	if !strings.HasPrefix(name, "2026-09-27_MedC0011_AhmadbinAli_20270315_") {
		t.Fatalf("name=%q", name)
	}
	if !strings.HasSuffix(name, ".pdf") {
		t.Fatalf("name=%q", name)
	}
	undated := McuCertificateFilename("nope", "MedC0011", "  ", "")
	if !strings.HasPrefix(undated, "undated_MedC0011_Unknown_undated_") {
		t.Fatalf("name=%q", undated)
	}
}

func TestCommonDir(t *testing.T) {
	dir := CommonDir("/data/uploads")
	want := filepath.Join("/data/uploads", medpostWideDir, "common")
	if dir != want {
		t.Fatalf("dir=%q want=%q", dir, want)
	}
}

func TestFindCommonFile(t *testing.T) {
	root := t.TempDir()
	if _, ok := FindCommonFile(root, "logo"); ok {
		t.Fatal("expected no logo yet")
	}

	dir := CommonDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jpg := filepath.Join(dir, "logo.jpg")
	if err := os.WriteFile(jpg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := FindCommonFile(root, "logo")
	if !ok || got != jpg {
		t.Fatalf("got %q ok=%v", got, ok)
	}

	png := filepath.Join(dir, "logo.png")
	if err := os.WriteFile(png, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = FindCommonFile(root, "logo")
	if !ok || got != png {
		t.Fatalf("prefer png, got %q ok=%v", got, ok)
	}
}
