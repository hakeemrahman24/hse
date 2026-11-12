package importer

import (
	"fmt"
	"strings"
)

// textField is a stored text value and the width of the column it goes into.
type textField struct {
	name string
	val  *string
	max  int
}

// fit cuts every listed value to its column width and reports each cut.
//
// WHY: SQLite does not enforce column widths but MariaDB does, so one overlong cell in the
// sheet would stop the whole import in production. Cutting is the smaller harm, and the report
// says which field of which request was cut.
func fit(bad func(field, msg string, fatal bool), fields []textField) {
	for _, f := range fields {
		if len([]rune(*f.val)) <= f.max {
			continue
		}
		*f.val = string([]rune(*f.val)[:f.max])
		bad(f.name, fmt.Sprintf("%s was longer than %d characters and was cut", f.name, f.max), false)
	}
}

// truthy reads the sheet's yes/no cells ("Y", "yes", "TRUE", "1").
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1":
		return true
	}
	return false
}

// dayOfAny reads a cell that holds either a date or a date with a time (the ticket sheet shows
// its planned dates as midnight timestamps) and returns the day.
func dayOfAny(s string) (string, bool) {
	if d, ok := Day(s); ok {
		return d, true
	}
	if m, ok := Moment(s); ok && len(m) >= 10 {
		return m[:10], true
	}
	return "", false
}

// stampOfDay turns a date-only cell into a moment at the start of that day. The old sheet kept
// only the day for first check-in, closing and the notification times; the exact time is in the
// gate Worker and replaces this at the first sync.
func stampOfDay(s string) (string, bool) {
	d, ok := Day(s)
	if !ok || d == "" {
		return "", ok
	}
	return d + " 00:00:00", true
}
