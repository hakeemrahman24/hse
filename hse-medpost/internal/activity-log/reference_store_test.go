package activitylog

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openReferenceTestDB is the activity test database plus migration 025, with
// one program (id 1) and one staff member (id 1) so documents can be filed.
func openReferenceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database := openActivityTestDB(t)
	applyReferenceMigration(t, database)
	for _, s := range []string{
		`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Malaria Program')`,
		`INSERT INTO MedpostStaffTable (StaffName) VALUES ('Admin One')`,
	} {
		if _, err := database.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func newReferenceAPI(t *testing.T) *API {
	t.Helper()
	return &API{DB: openReferenceTestDB(t), UploadDir: t.TempDir()}
}

func pdfUpload(name, content string) ReferenceUpload {
	return ReferenceUpload{Original: name, Data: strings.NewReader(content)}
}

func intPtr(n int64) *int64 { return &n }

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func mustAddDoc(t *testing.T, a *API, title, content string) int64 {
	t.Helper()
	id, err := a.AddDocumentWithFile(intPtr(1), "Forms", title, "", pdfUpload(title+".pdf", content), 1)
	if err != nil {
		t.Fatalf("AddDocumentWithFile(%q): %v", title, err)
	}
	return id
}

func TestAddDocumentWithFileStoresFileHashAndVersionOne(t *testing.T) {
	a := newReferenceAPI(t)

	id := mustAddDoc(t, a, "Intake form", "pdf bytes one")

	var link, hash string
	var versionNo int
	err := a.DB.QueryRow(`
		SELECT d.FileLink, v.FileHash, v.VersionNo
		FROM DocumentTable d JOIN DocumentVersion v ON v.FK_DocumentID = d.DocumentID
		WHERE d.DocumentID = ?`, id).Scan(&link, &hash, &versionNo)
	if err != nil {
		t.Fatal(err)
	}
	if versionNo != 1 || len(hash) != 64 {
		t.Fatalf("version %d hash %q", versionNo, hash)
	}
	got, err := os.ReadFile(link)
	if err != nil || string(got) != "pdf bytes one" {
		t.Fatalf("stored file = %q, %v", got, err)
	}
}

func TestStoredHashIsLowercaseSHA256OfTheUploadedBytes(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Intake form", "hash me")
	var hash string
	_ = a.DB.QueryRow(`SELECT FileHash FROM DocumentVersion WHERE FK_DocumentID = ?`, id).Scan(&hash)

	// Compared with a hash of the plain bytes, so a regression in what is hashed
	// (for example hashing after a transform) shows up as a mismatch. Lowercase so
	// two equal files always compare equal.
	if want := sha256Hex([]byte("hash me")); hash != want || hash != strings.ToLower(hash) {
		t.Fatalf("hash = %s, want %s", hash, want)
	}
}

func TestAddDocumentWithFileRejectsExactDuplicateAndLeavesNoFile(t *testing.T) {
	a := newReferenceAPI(t)
	mustAddDoc(t, a, "Intake form", "same bytes")
	before := len(filesUnder(t, a.UploadDir))

	_, err := a.AddDocumentWithFile(intPtr(1), "Forms", "Renamed copy", "", pdfUpload("copy.pdf", "same bytes"), 1)

	var dup *DuplicateFileError
	if !errors.As(err, &dup) {
		t.Fatalf("err = %v, want *DuplicateFileError", err)
	}
	if dup.Title != "Intake form" {
		t.Fatalf("duplicate names %q, want the existing document", dup.Title)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n)
	if n != 1 {
		t.Fatalf("documents = %d, the rejected upload still created a row", n)
	}
	if after := len(filesUnder(t, a.UploadDir)); after != before {
		t.Fatalf("files on disk %d -> %d, the rejected upload was left behind", before, after)
	}
}

func TestReplaceDocumentFileKeepsOldVersionAndPointsAtNew(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Intake form", "first")
	var oldLink string
	_ = a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&oldLink)

	no, err := a.ReplaceDocumentFile(id, pdfUpload("intake-v2.pdf", "second"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if no != 2 {
		t.Fatalf("version = %d, want 2", no)
	}

	var current string
	_ = a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&current)
	if current == oldLink {
		t.Fatal("FileLink still points at the old version")
	}
	if b, _ := os.ReadFile(current); string(b) != "second" {
		t.Fatalf("current file = %q", b)
	}
	if b, err := os.ReadFile(oldLink); err != nil || string(b) != "first" {
		t.Fatalf("old version must stay on disk: %q, %v", b, err)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentVersion WHERE FK_DocumentID = ?`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("versions = %d, want 2", n)
	}
}

func TestReplaceDocumentFileRejectsTheSameBytesAsAnyStoredVersion(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Intake form", "first")
	if _, err := a.ReplaceDocumentFile(id, pdfUpload("v2.pdf", "second"), 1); err != nil {
		t.Fatal(err)
	}

	// Re-uploading the OLD file as a "new" version is also a duplicate.
	_, err := a.ReplaceDocumentFile(id, pdfUpload("v1-again.pdf", "first"), 1)

	var dup *DuplicateFileError
	if !errors.As(err, &dup) {
		t.Fatalf("err = %v, want *DuplicateFileError", err)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentVersion WHERE FK_DocumentID = ?`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("versions = %d, want 2", n)
	}
}

// The pre-insert hash check can be beaten by two uploads of the same file
// arriving together. This skips that check and inserts straight through the
// real unique index, as the loser of such a race would, then confirms the error
// the user would see is the same "already in Reference" one.
func TestUniqueIndexStopsARacingDuplicateAndIsReportedAsDuplicate(t *testing.T) {
	a := newReferenceAPI(t)
	mustAddDoc(t, a, "Winner", "raced bytes")
	other := mustAddDoc(t, a, "Other", "other bytes")

	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, insertErr := insertVersion(tx, other, "/up/loser.pdf", "loser.pdf", sha256Hex([]byte("raced bytes")), 1)
	if insertErr == nil {
		t.Fatal("the unique index let a second copy of the same hash in")
	}

	var dup *DuplicateFileError
	if got := a.classifyInsertError(insertErr, sha256Hex([]byte("raced bytes"))); !errors.As(got, &dup) || dup.Title != "Winner" {
		t.Fatalf("classified as %v, want a duplicate naming Winner", got)
	}
	if msg := dup.Error(); !strings.Contains(msg, "already in Reference") || !strings.Contains(msg, "Winner") {
		t.Fatalf("message = %q", msg)
	}
}

func TestClassifyInsertErrorLeavesOtherErrorsAlone(t *testing.T) {
	a := newReferenceAPI(t)
	plain := errors.New("disk full")
	if got := a.classifyInsertError(plain, "abc"); got != plain {
		t.Fatalf("got %v, want the original error unchanged", got)
	}
	// A unique failure with no matching hash is a version-number race, not a duplicate file.
	uniqueNoHash := errors.New("UNIQUE constraint failed: DocumentVersion.FK_DocumentID, DocumentVersion.VersionNo")
	if got := a.classifyInsertError(uniqueNoHash, "nothing-stored-with-this"); !errors.Is(got, ErrVersionConflict) {
		t.Fatalf("got %v, want ErrVersionConflict", got)
	}
}

func TestAddLinkDocumentStoresOnlyWebLinks(t *testing.T) {
	a := newReferenceAPI(t)

	id, err := a.AddLinkDocument(intPtr(1), "Procedure", "  WHO   malaria page ", "see section 3", " https://example.org/who ")
	if err != nil {
		t.Fatal(err)
	}
	var title, link string
	var versions int
	_ = a.DB.QueryRow(`SELECT DocumentTitle, FormLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&title, &link)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentVersion WHERE FK_DocumentID = ?`, id).Scan(&versions)
	if title != "WHO malaria page" || link != "https://example.org/who" || versions != 0 {
		t.Fatalf("title %q link %q versions %d (a link has no file, so no version)", title, link, versions)
	}

	// Anything that is not a plain web address is refused: a javascript: link
	// would run in the page of every user who clicks it.
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,x", "ftp://example.org/a", "example.org/no-scheme", "https://", ""} {
		if _, err := a.AddLinkDocument(intPtr(1), "Procedure", "Bad", "", bad); !errors.Is(err, ErrBadLink) {
			t.Fatalf("link %q: err = %v, want ErrBadLink", bad, err)
		}
	}
	if _, err := a.AddLinkDocument(nil, "Forms", "No program", "", "https://example.org"); !errors.Is(err, ErrDocumentScope) {
		t.Fatalf("program rule must apply to links too: err = %v", err)
	}
}

func TestVersionFileReturnsAnOldVersionsPathAndName(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Intake form", "first")
	if _, err := a.ReplaceDocumentFile(id, pdfUpload("v2.pdf", "second"), 1); err != nil {
		t.Fatal(err)
	}

	path, name, err := a.VersionFile(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "first" || name != "Intake form.pdf" {
		t.Fatalf("version 1 = %q named %q", b, name)
	}
	if _, _, err := a.VersionFile(id, 9); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("unknown version: err = %v", err)
	}
}

func TestReplaceDocumentFileUnknownDocument(t *testing.T) {
	a := newReferenceAPI(t)
	_, err := a.ReplaceDocumentFile(99, pdfUpload("x.pdf", "x"), 1)
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("err = %v, want ErrDocumentNotFound", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestUploadRejectsWrongTypeEmptyAndOversizeFiles(t *testing.T) {
	a := newReferenceAPI(t)

	_, err := a.AddDocumentWithFile(intPtr(1), "Forms", "Bad", "", pdfUpload("run.exe", "MZ"), 1)
	if !errors.Is(err, ErrFileType) {
		t.Fatalf("exe: err = %v, want ErrFileType", err)
	}
	_, err = a.AddDocumentWithFile(intPtr(1), "Forms", "Empty", "", pdfUpload("empty.pdf", ""), 1)
	if !errors.Is(err, ErrFileEmpty) {
		t.Fatalf("empty: err = %v, want ErrFileEmpty", err)
	}
	big := ReferenceUpload{Original: "big.pdf", Data: io.LimitReader(zeroReader{}, MaxReferenceFileBytes+1)}
	_, err = a.AddDocumentWithFile(intPtr(1), "Forms", "Big", "", big, 1)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("big: err = %v, want ErrFileTooLarge", err)
	}
	exactly := ReferenceUpload{Original: "exact.pdf", Data: io.LimitReader(zeroReader{}, MaxReferenceFileBytes)}
	if _, err := a.AddDocumentWithFile(intPtr(1), "Forms", "Exact", "", exactly, 1); err != nil {
		t.Fatalf("a file of exactly the cap must be accepted: %v", err)
	}

	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n)
	if n != 1 {
		t.Fatalf("documents = %d, want only the one accepted file", n)
	}
	if files := filesUnder(t, a.UploadDir); len(files) != 1 {
		t.Fatalf("files on disk = %v, rejected uploads left files behind", files)
	}
}

func TestAddDocumentWithFileEnforcesTheProgramRule(t *testing.T) {
	a := newReferenceAPI(t)

	if _, err := a.AddDocumentWithFile(nil, "Forms", "No program", "", pdfUpload("a.pdf", "a"), 1); !errors.Is(err, ErrDocumentScope) {
		t.Fatalf("non-Template without a program: err = %v", err)
	}
	if _, err := a.AddDocumentWithFile(intPtr(1), "Template", "With program", "", pdfUpload("b.pdf", "b"), 1); !errors.Is(err, ErrDocumentScope) {
		t.Fatalf("Template with a program: err = %v", err)
	}
	if _, err := a.AddDocumentWithFile(intPtr(77), "Forms", "Ghost program", "", pdfUpload("c.pdf", "c"), 1); !errors.Is(err, ErrDocumentScope) {
		t.Fatalf("unknown program: err = %v", err)
	}
	if _, err := a.AddDocumentWithFile(intPtr(1), "Bogus", "Bad type", "", pdfUpload("d.pdf", "d"), 1); !errors.Is(err, ErrDocumentScope) {
		t.Fatalf("unknown type: err = %v", err)
	}
	if _, err := a.AddDocumentWithFile(nil, "Template", "Site template", "", pdfUpload("e.pdf", "e"), 1); err != nil {
		t.Fatalf("Template with no program must work: %v", err)
	}
	if _, err := a.AddDocumentWithFile(intPtr(1), "Forms", "   ", "", pdfUpload("f.pdf", "f"), 1); !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("blank title: err = %v", err)
	}
}

func TestSectionLinksOneDocumentCanServeTwoSections(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Malaria SOP", "sop")

	if err := a.AddDocumentToSection(id, "malaria", 1); err != nil {
		t.Fatal(err)
	}
	if err := a.AddDocumentToSection(id, "fatigue", 1); err != nil {
		t.Fatal(err)
	}
	if err := a.AddDocumentToSection(id, "malaria", 1); !errors.Is(err, ErrAlreadyInSection) {
		t.Fatalf("second add to the same section: err = %v", err)
	}
	if err := a.AddDocumentToSection(id, "dentistry", 1); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("unknown section: err = %v", err)
	}
	if err := a.AddDocumentToSection(99, "malaria", 1); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("unknown document: err = %v", err)
	}

	items, err := a.ListSectionDocuments("malaria")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Malaria SOP" {
		t.Fatalf("items = %+v", items)
	}
	if got := items[0].AlsoIn; len(got) != 1 || got[0] != "fatigue" {
		t.Fatalf("AlsoIn = %v, want [fatigue]", got)
	}
	if items[0].VersionNo != 1 || !items[0].HasFile {
		t.Fatalf("item = %+v", items[0])
	}
}

func TestRemoveDocumentFromSectionKeepsTheDocument(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Malaria SOP", "sop")
	_ = a.AddDocumentToSection(id, "malaria", 1)
	_ = a.AddDocumentToSection(id, "fatigue", 1)

	if err := a.RemoveDocumentFromSection(id, "malaria"); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveDocumentFromSection(id, "malaria"); !errors.Is(err, ErrNotInSection) {
		t.Fatalf("removing twice: err = %v", err)
	}
	var docs, secs int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&docs)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentSection`).Scan(&secs)
	if docs != 1 || secs != 1 {
		t.Fatalf("documents %d sections %d, want 1 and 1", docs, secs)
	}
}

func TestListSectionDocumentsKeepsAddOrder(t *testing.T) {
	a := newReferenceAPI(t)
	second := mustAddDoc(t, a, "Zebra", "z")
	first := mustAddDoc(t, a, "Alpha", "a")
	_ = a.AddDocumentToSection(second, "malaria", 1)
	_ = a.AddDocumentToSection(first, "malaria", 1)

	items, err := a.ListSectionDocuments("malaria")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Title != "Zebra" || items[1].Title != "Alpha" {
		t.Fatalf("order = %+v (the order the admin added them in)", items)
	}
}

func TestLibraryListMarksWhatIsAlreadyInTheSection(t *testing.T) {
	a := newReferenceAPI(t)
	in := mustAddDoc(t, a, "Already here", "1")
	mustAddDoc(t, a, "Not here", "2")
	_ = a.AddDocumentToSection(in, "malaria", 1)
	if _, err := a.DB.Exec(`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink) VALUES (1, 'Procedure', 'WHO page', 'https://example.org')`); err != nil {
		t.Fatal(err)
	}

	items, err := a.ListLibraryDocuments("malaria")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("library = %d documents, want 3 (files and links)", len(items))
	}
	byTitle := map[string]ReferenceLibraryItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if !byTitle["Already here"].InSection || byTitle["Not here"].InSection {
		t.Fatalf("InSection flags wrong: %+v", byTitle)
	}
	if byTitle["WHO page"].HasFile || byTitle["Not here"].Program != "Malaria Program" {
		t.Fatalf("link or program wrong: %+v", byTitle)
	}
	// Programs are stored as CamelCase ("FitnessForWork") but shown with spaces everywhere else in
	// the app, including this dialog's program dropdown, so the library list must match.
	if _, err := a.DB.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('FitnessForWork')`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink) VALUES (2, 'Forms', 'Camel doc', 'https://example.org/c')`); err != nil {
		t.Fatal(err)
	}
	items, err = a.ListLibraryDocuments("malaria")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Title == "Camel doc" && it.Program != "Fitness For Work" {
			t.Fatalf("program shown as %q, want %q", it.Program, "Fitness For Work")
		}
	}
}

func TestDeleteReferenceDocumentIsBlockedWhileAnotherSectionUsesIt(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Shared SOP", "shared")
	_ = a.AddDocumentToSection(id, "malaria", 1)
	_ = a.AddDocumentToSection(id, "fatigue", 1)

	err := a.DeleteReferenceDocument(id, "malaria")

	var shared *SharedDocumentError
	if !errors.As(err, &shared) || len(shared.Sections) != 1 || shared.Sections[0] != "fatigue" {
		t.Fatalf("err = %v, want SharedDocumentError listing fatigue", err)
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n)
	if n != 1 {
		t.Fatal("a shared document was deleted")
	}
}

func TestDeleteReferenceDocumentRemovesRowsAndEveryVersionFile(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Old SOP", "v1")
	if _, err := a.ReplaceDocumentFile(id, pdfUpload("v2.pdf", "v2"), 1); err != nil {
		t.Fatal(err)
	}
	_ = a.AddDocumentToSection(id, "malaria", 1)
	if len(filesUnder(t, a.UploadDir)) != 2 {
		t.Fatalf("setup: want 2 files, got %v", filesUnder(t, a.UploadDir))
	}

	if err := a.DeleteReferenceDocument(id, "malaria"); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"DocumentTable", "DocumentVersion", "DocumentSection"} {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Fatalf("%s still has %d rows", table, n)
		}
	}
	if files := filesUnder(t, a.UploadDir); len(files) != 0 {
		t.Fatalf("version files left on disk: %v", files)
	}
	if err := a.DeleteReferenceDocument(id, "malaria"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("deleting twice: err = %v", err)
	}
}

func TestListDocumentVersionsNewestFirstWithUploaderAndCurrentFlag(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "Intake form", "v1")
	if _, err := a.ReplaceDocumentFile(id, pdfUpload("intake-v2.pdf", "v2"), 1); err != nil {
		t.Fatal(err)
	}

	versions, err := a.ListDocumentVersions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].VersionNo != 2 || versions[1].VersionNo != 1 {
		t.Fatalf("versions = %+v", versions)
	}
	if !versions[0].IsCurrent || versions[1].IsCurrent {
		t.Fatalf("current flag wrong: %+v", versions)
	}
	if versions[0].UploadedBy != "Admin One" || versions[0].OriginalName != "intake-v2.pdf" {
		t.Fatalf("newest = %+v", versions[0])
	}
}

func TestEnsureDocumentHashesFillsOldFilesAndSkipsProblems(t *testing.T) {
	a := newReferenceAPI(t)
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// What migration 025 leaves behind: version 1 rows with no hash.
	rows := []struct{ title, link string }{
		{"Fine", write("fine.pdf", "fine bytes")},
		{"Twin A", write("twin-a.pdf", "twin bytes")},
		{"Twin B", write("twin-b.pdf", "twin bytes")},
		{"Gone", filepath.Join(dir, "missing.pdf")},
	}
	for _, r := range rows {
		if _, err := a.DB.Exec(`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FileLink) VALUES (1, 'Forms', ?, ?)`, r.title, r.link); err != nil {
			t.Fatal(err)
		}
	}
	applyReferenceMigration(t, a.DB)

	filled, err := EnsureDocumentHashes(a.DB)
	if err != nil {
		t.Fatalf("a missing file or a twin must not stop startup: %v", err)
	}
	if filled != 2 {
		t.Fatalf("filled = %d, want 2 (Fine and one of the twins)", filled)
	}
	var hashed, unhashed int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentVersion WHERE FileHash IS NOT NULL`).Scan(&hashed)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentVersion WHERE FileHash IS NULL`).Scan(&unhashed)
	if hashed != 2 || unhashed != 2 {
		t.Fatalf("hashed %d unhashed %d, want 2 and 2", hashed, unhashed)
	}

	// A second run has nothing new to do beyond retrying the two unhashable rows.
	filled, err = EnsureDocumentHashes(a.DB)
	if err != nil || filled != 0 {
		t.Fatalf("second run filled %d, err %v", filled, err)
	}
}
