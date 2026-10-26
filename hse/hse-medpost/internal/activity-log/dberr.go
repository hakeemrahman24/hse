package activitylog

import "strings"

// isValidRole reports whether role is one of the three app roles.
// admin: full access, including staff/login/position management.
// staff: view/add activities only.
func isValidRole(role string) bool {
	return role == "admin" || role == "staff"
}

// isUniqueViolation reports whether err is a unique/duplicate-key constraint
// failure, across both supported drivers: SQLite says "UNIQUE constraint
// failed", MariaDB/MySQL says "Duplicate entry '...' for key '...'".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "Duplicate entry")
}
