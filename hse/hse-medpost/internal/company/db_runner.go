package company

import "database/sql"

// dbRunner is satisfied by *sql.DB and *sql.Tx, so the relation depth
// recompute can run inside the relation update's transaction.
type dbRunner interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}
