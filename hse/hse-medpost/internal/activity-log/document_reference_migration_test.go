package activitylog

import (
	"database/sql"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

// applyReferenceMigration runs migrations/sqlite/025 the way cmd/server does
// at every startup: as plain SQL, which must therefore be safe to run twice.
func applyReferenceMigration(t *testing.T, database *sql.DB) {
	t.Helper()
	sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", "025_document_reference.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database, sqlText); err != nil {
		t.Fatalf("025_document_reference.sql: %v", err)
	}
}

func seedReferenceDocuments(t *testing.T, database *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Malaria Program')`,
		`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FileLink) VALUES (1, 'Forms', 'Intake form', '/up/intake.pdf')`,
		`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink) VALUES (1, 'Procedure', 'WHO page', 'https://example.org/who')`,
	}
	for _, s := range stmts {
		if _, err := database.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceMigrationBackfillsVersionOneForUploadedFilesOnly(t *testing.T) {
	database := openActivityTestDB(t)
	seedReferenceDocuments(t, database)

	applyReferenceMigration(t, database)

	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM DocumentVersion`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("versions = %d, want 1 (only the document with an uploaded file)", n)
	}
	var docID, versionNo int
	var link string
	if err := database.QueryRow(`SELECT FK_DocumentID, VersionNo, FileLink FROM DocumentVersion`).Scan(&docID, &versionNo, &link); err != nil {
		t.Fatal(err)
	}
	if docID != 1 || versionNo != 1 || link != "/up/intake.pdf" {
		t.Fatalf("version row = doc %d v%d %q", docID, versionNo, link)
	}
}

func TestReferenceMigrationIsSafeToRunTwice(t *testing.T) {
	database := openActivityTestDB(t)
	seedReferenceDocuments(t, database)

	applyReferenceMigration(t, database)
	applyReferenceMigration(t, database)

	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM DocumentVersion`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("versions after second run = %d, want 1 (the backfill must not add a copy)", n)
	}
}

func TestReferenceMigrationRejectsDuplicateHashAndDuplicateVersionNumber(t *testing.T) {
	database := openActivityTestDB(t)
	seedReferenceDocuments(t, database)
	applyReferenceMigration(t, database)

	insert := `INSERT INTO DocumentVersion (FK_DocumentID, VersionNo, FileLink, FileHash, UploadDate) VALUES (?, ?, ?, ?, '2026-11-05')`
	if _, err := database.Exec(insert, 2, 1, "/up/a.pdf", "abc"); err != nil {
		t.Fatal(err)
	}
	// Same bytes under another document: this is the duplicate the owner wants refused.
	if _, err := database.Exec(insert, 1, 2, "/up/b.pdf", "abc"); err == nil {
		t.Fatal("a second version with the same hash was accepted")
	}
	// Same document and number: two racing uploads must not both become version 2.
	if _, err := database.Exec(insert, 1, 1, "/up/c.pdf", "def"); err == nil {
		t.Fatal("a second version 1 of the same document was accepted")
	}
	// Rows without a hash (backfilled files not hashed yet) may coexist.
	if _, err := database.Exec(insert, 2, 2, "/up/d.pdf", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(insert, 2, 3, "/up/e.pdf", nil); err != nil {
		t.Fatalf("two unhashed versions must be allowed: %v", err)
	}
}

func TestDocumentSectionKeepsOneRowPerDocumentAndSection(t *testing.T) {
	database := openActivityTestDB(t)
	seedReferenceDocuments(t, database)
	applyReferenceMigration(t, database)

	add := `INSERT INTO DocumentSection (FK_DocumentID, Section, AddedDate) VALUES (?, ?, '2026-11-05')`
	if _, err := database.Exec(add, 1, "malaria"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(add, 1, "fatigue"); err != nil {
		t.Fatalf("one document in two sections must be allowed: %v", err)
	}
	if _, err := database.Exec(add, 1, "malaria"); err == nil {
		t.Fatal("the same document was added to the same section twice")
	}
}
