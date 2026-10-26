package company

import (
	"database/sql"
	"fmt"
)

// EnsureCompanyArchivedColumn adds Company.Archived to a SQLite database that
// predates it. The MySQL database gets the same column from
// migrations/mysql/012_company_archived.sql, which is run by hand.
func EnsureCompanyArchivedColumn(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('Company') WHERE name = 'Archived'`).Scan(&n); err != nil {
		return fmt.Errorf("check Company schema: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := database.Exec(`ALTER TABLE Company ADD COLUMN Archived INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add Company.Archived: %w", err)
	}
	return nil
}
