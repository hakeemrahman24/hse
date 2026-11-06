package db

import "strings"

// apostrophes are the marks a person name may carry (O'Connor, As'adul) that a
// searcher may or may not type, and that a phone keyboard types as a curly one.
var apostropheStripper = strings.NewReplacer("'", "", "’", "", "‘", "")

// NameLikeSQL is a SQL condition matching column against one NameLikeArg
// placeholder, ignoring apostrophes in the stored name. Works on SQLite and
// MariaDB. column is a fixed identifier written in code, never user input.
func NameLikeSQL(column string) string {
	return "REPLACE(REPLACE(REPLACE(" + column + ", '''', ''), '’', ''), '‘', '') LIKE ?"
}

// NameLikeArg is the placeholder value for NameLikeSQL: the typed text without
// apostrophes, wrapped for a substring match. "as'adul", "as’adul" and "asadul"
// all find "As'adul"; the stored name is untouched.
func NameLikeArg(typed string) string {
	return "%" + apostropheStripper.Replace(typed) + "%"
}
