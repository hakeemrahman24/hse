package activitylog

import "testing"

func TestEnsureWebsiteDocumentTypeKeepsRowsAndAllowsWebsite(t *testing.T) {
	database := openActivityTestDB(t)
	seedReferenceDocuments(t, database)
	applyReferenceMigration(t, database) // versions + sections point at DocumentTable

	if _, err := database.Exec(`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink) VALUES (1, 'Website', 'x', 'https://x.org')`); err == nil {
		t.Fatal("Website was accepted before the migration")
	}
	for i := 0; i < 2; i++ { // twice: it runs at every startup
		if err := EnsureWebsiteDocumentType(database); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after rebuild = %d, %v; want 2", n, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM DocumentVersion`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("versions after rebuild = %d, %v; want 1", n, err)
	}
	if _, err := database.Exec(`INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink) VALUES (1, 'Website', 'Ministry page', 'https://x.org')`); err != nil {
		t.Fatalf("Website rejected after the migration: %v", err)
	}
}

func TestWebsiteProblem(t *testing.T) {
	cases := []struct {
		dtype, link string
		file        bool
		bad         bool
	}{
		{"Website", "https://a.org", false, false},
		{"Website", "", false, true},
		{"Website", "javascript:alert(1)", false, true},
		{"Website", "https://a.org", true, true},
		{"Forms", "", true, false},
	}
	for _, c := range cases {
		if got := websiteProblem(c.dtype, c.link, c.file) != ""; got != c.bad {
			t.Errorf("%+v: problem=%v, want %v", c, got, c.bad)
		}
	}
}
