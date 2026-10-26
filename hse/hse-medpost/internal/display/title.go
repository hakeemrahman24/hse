package display

import (
	"regexp"
	"strings"
)

var camelBreak = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// ProgramTitle inserts spaces between CamelCase / PascalCase words for HTML.
// Titles that already contain spaces are returned trimmed as-is.
func ProgramTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.Contains(s, " ") {
		return s
	}
	return camelBreak.ReplaceAllString(s, "$1 $2")
}
