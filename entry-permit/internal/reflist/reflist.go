// Package reflist is the generic editor behind the admin-editable lists:
// nationality, ring, malaria regions, purpose area, gate names, checkout
// reasons and notification emails.
//
// WHY one generic table: these lists are small, similar and change rarely, so
// writing a table, a page and a test for each would be seven times the code.
// A list is described once in Defs (its columns, which columns make an item
// unique) and the same store and page serve all of them.
package reflist

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxColumns  = 4   // c1..c4 in the table
	maxValueLen = 160 // matches the VARCHAR(160) columns
)

// Column describes one field of a list item.
type Column struct {
	Key, Label string
	Kind       string // "text" (default) or "email"
	Required   bool
	Options    []string // when set, the value must be one of these (shown as a dropdown)
}

// Def describes a list.
type Def struct {
	Key, Label, Help string
	Columns          []Column
	KeyCols          []int // columns that together must be unique
	SortBy           []int // columns to sort by; empty keeps the order items were added
	// CustomPath, when set, is a purpose-built admin page for this list. The
	// generic free-text editor is then switched off for it, so values can only
	// come from that page (the malaria list must use the regions medpost serves).
	CustomPath string
	// Gate marks the lists the limited Gate admin role may edit (gate activities only).
	Gate bool
}

// Path is the admin page for the list.
func (d Def) Path() string {
	if d.CustomPath != "" {
		return d.CustomPath
	}
	return "/staff/admin/lists/" + d.Key
}

// Defs is every editable list, in the order the admin menu shows them.
var Defs = []Def{
	{
		Key: "nationality", Label: "Nationality",
		Help:    "The choices in the nationality dropdown of the permit forms.",
		Columns: []Column{{Key: "name", Label: "Nationality", Required: true}},
		KeyCols: []int{0},
	},
	{
		Key: "ring", Label: "Ring / region",
		Help: "Ring, its label and the region it covers. Level and code can stay empty (for example Indonesian and Foreign).",
		Columns: []Column{
			{Key: "ring", Label: "Ring", Required: true},
			{Key: "label", Label: "Label", Required: true},
			{Key: "region_level", Label: "Region level"},
			{Key: "region_code", Label: "Region code"},
		},
		KeyCols: []int{0},
	},
	{
		Key: "malaria", Label: "Malaria endemicity",
		Help: "Endemicity per regency. The regencies come from hse-medpost, so names and provinces always match the permit forms.",
		Columns: []Column{
			{Key: "endemicity", Label: "Endemicity", Required: true, Options: []string{"Rendah", "Sedang", "Tinggi"}},
			{Key: "regency", Label: "Regency / city", Required: true},
			{Key: "province", Label: "Province", Required: true},
			{Key: "code", Label: "Region code"}, // the API's regency code; survives renames
		},
		KeyCols:    []int{1, 2},
		SortBy:     []int{2, 1},
		CustomPath: "/staff/admin/malaria",
	},
	{
		Key: "purpose_area", Label: "Purpose area",
		Help:    "Areas a visit or permit can be for. Starts empty; add values when they are needed.",
		Columns: []Column{{Key: "name", Label: "Purpose area", Required: true}},
		KeyCols: []int{0},
		Gate:    true,
	},
	{
		Key: "gate_name", Label: "Gate names",
		Help:    "The gates officers can pick at check-in. Sent to the gate by the sync.",
		Columns: []Column{{Key: "name", Label: "Gate", Required: true}},
		KeyCols: []int{0},
		Gate:    true,
	},
	{
		Key: "checkout_reason", Label: "Checkout reasons",
		Help:    "The reasons an officer can pick at check-out. Sent to the gate by the sync.",
		Columns: []Column{{Key: "name", Label: "Reason", Required: true}},
		KeyCols: []int{0},
	},
	{
		Key: "role_email", Label: "Notification emails",
		Help: "Mailboxes that receive notifications for a role. These are shared mailboxes, not the same as staff accounts.",
		Columns: []Column{
			{Key: "role", Label: "Role", Required: true, Options: []string{"admin", "doctor", "stage1", "final"}},
			{Key: "email", Label: "Email", Kind: "email", Required: true},
		},
		KeyCols: []int{0, 1},
		SortBy:  []int{0, 1},
	},
}

// Find returns the list with that key.
func Find(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// ValidationError is a message that is safe to show to the admin.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

var (
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// Validate cleans and checks the values of one item: trims, collapses spaces,
// enforces required, option lists, email format and length.
func Validate(d Def, values []string) ([]string, error) {
	values = padOptional(d, values) // optional trailing columns may be left out
	if len(values) != len(d.Columns) {
		return nil, ValidationError{"The form does not match the list."}
	}
	out := make([]string, len(values))
	for i, c := range d.Columns {
		v := strings.TrimSpace(spaceRe.ReplaceAllString(values[i], " "))
		switch {
		case v == "" && c.Required:
			return nil, ValidationError{fmt.Sprintf("%s is required.", c.Label)}
		case len([]rune(v)) > maxValueLen:
			return nil, ValidationError{fmt.Sprintf("%s is too long (at most %d characters).", c.Label, maxValueLen)}
		}
		if v != "" && len(c.Options) > 0 {
			canonical, ok := matchOption(c.Options, v)
			if !ok {
				return nil, ValidationError{fmt.Sprintf("%s must be one of: %s.", c.Label, strings.Join(c.Options, ", "))}
			}
			v = canonical
		}
		if v != "" && c.Kind == "email" {
			v = strings.ToLower(v)
			if !emailRe.MatchString(v) {
				return nil, ValidationError{fmt.Sprintf("%s is not a valid email address.", c.Label)}
			}
		}
		out[i] = v
	}
	return out, nil
}

func matchOption(options []string, v string) (string, bool) {
	for _, o := range options {
		if strings.EqualFold(o, v) {
			return o, true
		}
	}
	return "", false
}

// itemKey is the lower-cased key column values, so "Papua" and "papua" clash.
func itemKey(d Def, values []string) string {
	parts := make([]string, len(d.KeyCols))
	for i, c := range d.KeyCols {
		parts[i] = strings.ToLower(values[c])
	}
	return strings.Join(parts, "|")
}
