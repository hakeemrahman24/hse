package main

import (
	"strings"
	"testing"
)

func TestValidateAccountChange(t *testing.T) {
	cases := []struct {
		name string
		in   accountChange
		want string // part of the message; "" means valid
	}{
		{"rename only", accountChange{Username: "siti"}, ""},
		{"rename and password", accountChange{Username: "siti", NewPassword: "longenough1", Confirm: "longenough1"}, ""},
		{"empty username", accountChange{}, "cannot be empty"},
		{"spaces in username", accountChange{Username: "si ti"}, "spaces"},
		{"username too long", accountChange{Username: strings.Repeat("a", 65)}, "64"},
		{"short password", accountChange{Username: "siti", NewPassword: "short", Confirm: "short"}, "at least 8"},
		{"mismatch", accountChange{Username: "siti", NewPassword: "longenough1", Confirm: "longenough2"}, "do not match"},
		{"confirm without password", accountChange{Username: "siti", Confirm: "x"}, "do not match"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateAccountChange(c.in)
			if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to contain %q", got, c.want)
			}
		})
	}
}

func TestEveryRoleSeesMyAccountButOnlyAdminSeesSettings(t *testing.T) {
	has := func(role, title string) bool {
		for _, m := range modulesFor(role) {
			if m.Title == title {
				return true
			}
		}
		return false
	}
	for _, role := range []string{roleUser, roleAdmin} {
		if !has(role, "My account") {
			t.Errorf("%s should see My account", role)
		}
	}
	if has(roleUser, "Settings") || !has(roleAdmin, "Settings") {
		t.Error("Settings is for administrators only")
	}
}

func TestDefaultPasswordUsesTheEnvironmentWhenLongEnough(t *testing.T) {
	t.Setenv("DEFAULT_USER_PASSWORD", "")
	if defaultPassword() != fallbackDefaultPassword {
		t.Error("without the setting the fallback is used")
	}
	t.Setenv("DEFAULT_USER_PASSWORD", "short")
	if defaultPassword() != fallbackDefaultPassword {
		t.Error("a value under 8 characters is ignored")
	}
	t.Setenv("DEFAULT_USER_PASSWORD", "Site#Default9")
	if defaultPassword() != "Site#Default9" {
		t.Error("the configured default should be used")
	}
}

func TestUsersPagesShowActionsAndTheDeleteWarning(t *testing.T) {
	loadTestTemplates()
	admin := &Session{UserID: 1, Username: "arif", Role: roleAdmin}
	users := []*AdminUser{{ID: 1, Username: "arif", Role: roleAdmin}, {ID: 2, Username: "budi", Role: roleUser}, {ID: 3, Username: "siti", Role: roleUser, Archived: true}}
	list := renderToString(t, "users_list", PageData{Title: "Users", Heading: "Users", Session: admin, Users: users, ObsNav: nil})
	for _, want := range []string{`href="/users/2/edit"`, `action="/users/2/reset-password"`, `action="/users/2/archive"`, `action="/users/3/restore"`, `href="/users/2/delete"`, "Archived"} {
		if !strings.Contains(list, want) {
			t.Errorf("users list is missing %q", want)
		}
	}
	if strings.Contains(list, `action="/users/1/archive"`) || strings.Contains(list, `/users/1/delete`) || strings.Contains(list, `/users/1/reset-password`) {
		t.Error("you cannot archive, delete or reset yourself from the list")
	}
	del := renderToString(t, "users_delete", PageData{Title: "Delete", Heading: "Delete budi?", Session: admin, EditUser: users[1], PostCount: 3})
	for _, want := range []string{"cannot be undone", "3 observations they entered stay", "only an administrator can edit them", "Archive</em> instead", `name="confirm" value="yes"`, "Archive instead"} {
		if !strings.Contains(del, want) {
			t.Errorf("delete page is missing %q", want)
		}
	}
	none := renderToString(t, "users_delete", PageData{Title: "Delete", Heading: "Delete budi?", Session: admin, EditUser: users[1]})
	if !strings.Contains(none, "They entered no observations") {
		t.Error("with no posts the page says so")
	}
	edit := renderToString(t, "users_form", PageData{Title: "Edit user", Heading: "Edit user", Session: admin, EditUser: users[1]})
	if !strings.Contains(edit, `action="/users/2/edit"`) || !strings.Contains(edit, `value="budi"`) || !strings.Contains(edit, `name="new_password"`) {
		t.Error("edit form should post to the edit route with the current values")
	}
	add := renderToString(t, "users_form", PageData{Title: "Add user", Heading: "Add user", Session: admin})
	if !strings.Contains(add, `action="/users/new"`) || !strings.Contains(add, `name="password"`) {
		t.Error("add form")
	}
}
