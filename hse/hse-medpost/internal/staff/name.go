package staff

import (
	"fmt"
	"strings"
)

// FormatStaffName joins an optional title before the name, the name, and an
// optional title after it. Titles are kept as typed. A title before the name
// gets a trailing period when it does not already end with one: "dr" + "John Doe"
// becomes "dr. John Doe". A title after the name is prefixed with a comma:
// "MKKK" becomes "dr. John Doe, MKKK". The result is stored in StaffName.
func FormatStaffName(before, name, after string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	before = strings.Join(strings.Fields(before), " ")
	after = strings.Join(strings.Fields(after), " ")
	if name == "" {
		return "", fmt.Errorf("staff name required")
	}
	if before != "" && !strings.HasSuffix(before, ".") {
		before += "."
	}
	display := name
	if before != "" {
		display = before + " " + name
	}
	if after != "" {
		display += ", " + after
	}
	return display, nil
}
