package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func titles(ms []Module) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Title
	}
	return out
}

func TestModulesForUserHasTheThreeModulesAndMyAccountButNoSettings(t *testing.T) {
	got := titles(modulesFor(roleUser))
	want := []string{"Medical Activities", "Work Permit Register", "HSE Observation", "My account"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("card %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestModulesForAdminAddsSettingsLast(t *testing.T) {
	ms := modulesFor(roleAdmin)
	if len(ms) != 5 {
		t.Fatalf("admin should see 5 cards, got %v", titles(ms))
	}
	last := ms[len(ms)-1]
	if last.Title != "Settings" || !last.Settings || last.Href != "/admin/settings" {
		t.Fatalf("last card = %+v, want the Settings card", last)
	}
}

func TestModulesForUnknownRoleIsTreatedAsUser(t *testing.T) {
	// An old cookie issued before roles existed has an empty role; it must not gain Settings.
	if n := len(modulesFor("")); n != 4 {
		t.Fatalf("empty role should see 4 cards, got %d", n)
	}
}

func TestRequireAdmin(t *testing.T) {
	store := NewSessionStore("test-secret")
	a := &authContext{store: store}
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	h := a.requireAdmin(ok)

	cases := []struct {
		name string
		sess *Session
		code int
		loc  string
	}{
		{"no session goes to login", nil, http.StatusSeeOther, "/"},
		{"user is refused", &Session{UserID: 1, Username: "u", Role: roleUser}, http.StatusForbidden, ""},
		{"old cookie without role is refused", &Session{UserID: 1, Username: "u"}, http.StatusForbidden, ""},
		{"admin passes", &Session{UserID: 2, Username: "a", Role: roleAdmin}, http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
			if c.sess != nil {
				rec := httptest.NewRecorder()
				if err := store.Set(rec, *c.sess); err != nil {
					t.Fatal(err)
				}
				for _, ck := range rec.Result().Cookies() {
					req.AddCookie(ck)
				}
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != c.code {
				t.Fatalf("code = %d, want %d", rec.Code, c.code)
			}
			if c.loc != "" && rec.Header().Get("Location") != c.loc {
				t.Fatalf("location = %q, want %q", rec.Header().Get("Location"), c.loc)
			}
		})
	}
}

func TestRequireAuthRedirectsToLoginRoot(t *testing.T) {
	a := &authContext{store: NewSessionStore("test-secret")}
	h := a.requireAuth(func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/start", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("got %d %q, want 303 to /", rec.Code, rec.Header().Get("Location"))
	}
}
