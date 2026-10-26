package screening

import (
	"database/sql"
	"fmt"
)

// renamedTables are the two result tables, which were first released as
// DrugAlcoholSubmission and FatigueSubmission. "Screening" says what a row is,
// one screening test, and not how it arrived: the same table takes an offline
// form uploaded from a worksite and, later, an entry made in the clinic. A
// Source column will record which. MariaDB is renamed by hand with
// migrations/mysql/019_rename_screening_tables.sql; this list is the SQLite
// (development and test) equivalent.
var renamedTables = []struct{ old, current string }{
	{"DrugAlcoholSubmission", "DrugAlcoholScreening"},
	{"FatigueSubmission", "FatigueScreening"},
}

// fatigueAddedColumns are the raw hours the form scores from (X, Y and Z in
// the form's own words). The older Score24h, Score48h and ScoreAwake columns
// hold the points computed from them, not the hours. The form has always sent
// the hours (sleep_24h, sleep_48h, awake_hours); until these columns existed
// they were kept only inside RawJSON.
// migrations/mysql/020_fatigue_sleep_hours.sql creates the same list for MariaDB.
var fatigueAddedColumns = []string{"Sleep24h", "Sleep48h", "AwakeHours"}

// EnsureScreeningTableNames renames a SQLite database's old result tables
// to their current names. It must run before the migrations in migrations/sqlite
// (011 and 012 create the current names with IF NOT EXISTS, so running them
// first would leave an empty new table beside the old one that holds the rows).
//
// SQLite rewrites foreign keys that point at a renamed table, so
// ActivityTable.FK_SubmissionID follows the rename. It does nothing on a fresh
// database or one that was already renamed, so it is safe on every start. If
// both names exist it stops instead of choosing one: either could hold results.
func EnsureScreeningTableNames(database *sql.DB) error {
	for _, r := range renamedTables {
		oldExists, err := hasTable(database, r.old)
		if err != nil {
			return err
		}
		if !oldExists {
			continue
		}
		newExists, err := hasTable(database, r.current)
		if err != nil {
			return err
		}
		if newExists {
			return fmt.Errorf("both %s and %s exist; check which one holds the results and drop the other", r.old, r.current)
		}
		if _, err := database.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", r.old, r.current)); err != nil {
			return fmt.Errorf("rename %s to %s: %w", r.old, r.current, err)
		}
	}
	return nil
}

// EnsureFatigueColumns adds the raw sleep-hour columns to a SQLite database
// that was created before them. MariaDB is migrated by hand (see the README).
func EnsureFatigueColumns(database *sql.DB) error {
	return ensureTextColumns(database, "FatigueScreening", fatigueAddedColumns)
}

func hasTable(database *sql.DB, name string) (bool, error) {
	var n int
	err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

// ensureTextColumns adds each missing column as TEXT NOT NULL with an empty
// string default, the same shape as the other result columns. SQLite's ADD COLUMN cannot be
// repeated, so each column is checked first. Table and column names come from
// the package's own constants, never from a request.
func ensureTextColumns(database *sql.DB, table string, columns []string) error {
	existing := map[string]bool{}
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, col := range columns {
		if existing[col] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT NOT NULL DEFAULT ''", table, col)
		if _, err := database.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
