package entrymcu

import "database/sql"

// withTx runs a seed's data writes as one unit. Seeds only do DML here; schema
// changes (ALTER TABLE) stay outside, since MySQL commits DDL implicitly.
func withTx(database *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
