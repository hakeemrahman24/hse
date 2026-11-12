package web

import (
	"net/http"
)

// SignInData feeds the sign-in templates. Step 1 is the staff form (username and PIN). Step 2 is
// the worker date of birth form, shown again after a failed "My Entry Permit" lookup.
type SignInData struct {
	Step     int
	Name     string // worker name kept after a failed lookup
	Username string // staff username kept after a wrong PIN, so only the PIN is retyped
	Next     string // page asked for on the home page, carried through the sign in
	Err      string
}

// renderSignIn shows the sign-in page. my carries the Turnstile settings and, after a failed
// lookup, the birth date the person typed.
func (a *App) renderSignIn(w http.ResponseWriter, r *http.Request, status int, my MyData, s SignInData) {
	a.render(w, "signin", status, View{Title: "Sign in", User: a.currentUser(r), My: my, SignIn: s})
}
