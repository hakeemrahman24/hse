// Package migrations embeds the SQL files into the binary.
//
// WHY two folders: SQLite (dev) and MariaDB (production) differ in auto
// increment and types, so each has its own file with the same number and the
// same tables. A migration is applied once and recorded in schema_migrations.
package migrations

import "embed"

//go:embed sqlite/*.sql mysql/*.sql
var FS embed.FS

// Dir returns the folder for a driver name ("mysql" or anything else = sqlite).
func Dir(driver string) string {
	if driver == "mysql" {
		return "mysql"
	}
	return "sqlite"
}
