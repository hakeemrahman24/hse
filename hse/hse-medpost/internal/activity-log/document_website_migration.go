package activitylog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EnsureWebsiteDocumentType lets DocumentTable.DocumentsType hold 'Website' on SQLite.
//
// SQLite cannot change a CHECK constraint in place, so the table is rebuilt: copy, drop, rename.
// It looks at the live schema first and does nothing once 'Website' is allowed, so it is safe at
// every startup. DocumentVersion and DocumentSection point at DocumentTable, so foreign keys are
// switched off for the rebuild on one pinned connection (the pragma is per connection and cannot
// change inside a transaction); the rows keep their ids, so those links stay valid.
// MariaDB gets the same change by hand: migrations/mysql/039_document_website.sql.
func EnsureWebsiteDocumentType(database *sql.DB) error {
	var createSQL string
	err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'DocumentTable'`).Scan(&createSQL)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read DocumentTable schema: %w", err)
	}
	if strings.Contains(createSQL, "'Website'") {
		return nil
	}

	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for the Website document type: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for the Website document type: %w", err)
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps := []string{
		`CREATE TABLE DocumentTable_website_new (
			DocumentID    INTEGER PRIMARY KEY AUTOINCREMENT,
			FK_ProcID     INTEGER,
			DocumentsType TEXT NOT NULL
				CHECK (DocumentsType IN ('Procedure', 'SocializationMaterial', 'Forms', 'Template', 'Website')),
			DocumentTitle TEXT NOT NULL,
			FormLink      TEXT,
			FileLink      TEXT,
			Notes         TEXT,
			FOREIGN KEY (FK_ProcID) REFERENCES HealthProcedureTable(ProcID)
				ON UPDATE CASCADE ON DELETE RESTRICT,
			CHECK (
				(DocumentsType = 'Template' AND FK_ProcID IS NULL)
				OR (DocumentsType <> 'Template' AND FK_ProcID IS NOT NULL)
			)
		)`,
		`INSERT INTO DocumentTable_website_new (DocumentID, FK_ProcID, DocumentsType, DocumentTitle, FormLink, FileLink, Notes)
			SELECT DocumentID, FK_ProcID, DocumentsType, DocumentTitle, FormLink, FileLink, Notes FROM DocumentTable`,
		`DROP TABLE DocumentTable`,
		`ALTER TABLE DocumentTable_website_new RENAME TO DocumentTable`,
	}
	for _, s := range steps {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("rebuild DocumentTable for the Website type: %w", err)
		}
	}
	return tx.Commit()
}
