package mcusubmit

import (
	"database/sql"
	"fmt"
)

// Genders are the only values stored, spelled exactly as Patient.Gender is on the checkup form, so a
// submitted gender can pre-fill that form and an existing patient's can be copied back unchanged.
var Genders = []string{"Male", "Female"}

// cleanGender returns the canonical spelling of a typed gender, or "" when it is not one of Genders.
func cleanGender(v string) string {
	for _, g := range Genders {
		if v == g {
			return g
		}
	}
	return ""
}

// addSubmissionColumn adds one column to McuSubmission on a SQLite database created before it, and
// reports whether it was added. SQLite's ADD COLUMN cannot be repeated, so it cannot live in a
// migration file that runs at every start.
func addSubmissionColumn(database *sql.DB, name, definition string) (added bool, err error) {
	rows, err := database.Query(`PRAGMA table_info(McuSubmission)`)
	if err != nil {
		return false, fmt.Errorf("read McuSubmission columns: %w", err)
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var col, ctype string
		var dflt any
		if err := rows.Scan(&cid, &col, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return false, fmt.Errorf("scan column: %w", err)
		}
		has = has || col == name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	if has {
		return false, nil
	}
	if _, err := database.Exec(`ALTER TABLE McuSubmission ADD COLUMN ` + name + ` ` + definition); err != nil {
		return false, fmt.Errorf("add %s: %w", name, err)
	}
	return true, nil
}

// EnsureGenderColumn adds McuSubmission.PatGender to an older SQLite database. New databases get it
// from migrations/sqlite/017; MariaDB gets it from migrations/mysql/034, run by hand.
func EnsureGenderColumn(database *sql.DB) error {
	_, err := addSubmissionColumn(database, "PatGender", "TEXT NULL")
	return err
}
