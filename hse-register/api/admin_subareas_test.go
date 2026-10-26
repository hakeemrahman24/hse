package main

import (
	"strings"
	"testing"
)

func TestCleanSubAreaName(t *testing.T) {
	long := strings.Repeat("a", maxSubAreaName+1)
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"  Mess   hall ", "Mess hall", true},
		{"", "", false},
		{"   ", "", false},
		{"bad\x07name", "", false},
		{long, "", false},
		{strings.Repeat("a", maxSubAreaName), strings.Repeat("a", maxSubAreaName), true},
	}
	for _, tt := range tests {
		got, err := cleanSubAreaName(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("cleanSubAreaName(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestSubAreasURL(t *testing.T) {
	if got := subAreasURL(7, ""); got != "/admin/settings/areas?sub=7" {
		t.Errorf("got %q", got)
	}
	if got := subAreasURL(7, "sub-added"); got != "/admin/settings/areas?sub=7&done=sub-added" {
		t.Errorf("got %q", got)
	}
}

func TestSubAreaPanelOpensOnlyForTheChosenArea(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}
	rows := []areaRow{{ID: 7, Name: "Camp", Subs: []subAreaRow{{ID: 3, AreaID: 7, Name: "Mess hall"}}}, {ID: 8, Name: "Port"}}
	data := PageData{Title: "t", Heading: "t", Session: admin, AreaRows: rows}

	closed := renderToString(t, "settings_areas", data)
	if strings.Contains(closed, "subareas/3/rename") || !strings.Contains(closed, "Mess hall") {
		t.Error("closed: the sub-areas are listed as text, with no edit forms")
	}
	if !strings.Contains(closed, `href="/admin/settings/areas?sub=7"`) {
		t.Error("missing the Sub-areas link")
	}
	data.SubOf = 7
	open := renderToString(t, "settings_areas", data)
	for _, want := range []string{"areas/7/subareas", "subareas/3/rename", "subareas/3/delete"} {
		if !strings.Contains(open, want) {
			t.Errorf("open: missing %q", want)
		}
	}
}
