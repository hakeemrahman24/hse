package main

import (
	"database/sql"
	"net/http"
)

type loginHandlers struct {
	db    *sql.DB
	store *SessionStore
}

// fetchUsernames fills the "Select user" list on the sign-in page. Administrator accounts are left
// out on purpose; they sign in with "Type username".
func (h *loginHandlers) fetchUsernames() []string {
	rows, err := h.db.Query(`SELECT username FROM admin_users WHERE role <> 'admin' AND archived_at IS NULL ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var usernames []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			usernames = append(usernames, u)
		}
	}
	return usernames
}

// show is the login page at "/". Like hse-medpost, "/" is the login page and a signed-in
// visitor is sent on to the home page instead of seeing the form again.
func (h *loginHandlers) show(w http.ResponseWriter, r *http.Request) {
	if _, err := h.store.Get(r); err == nil {
		http.Redirect(w, r, "/start", http.StatusSeeOther)
		return
	}
	renderLogin(w, "", h.fetchUsernames())
}

func (h *loginHandlers) submit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	sess, err := VerifyAdminCredentials(h.db, username, password)
	if err != nil {
		renderLogin(w, "Invalid username or password.", h.fetchUsernames())
		return
	}
	if err := h.store.Set(w, sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/start", http.StatusSeeOther)
}

func (h *loginHandlers) logout(w http.ResponseWriter, r *http.Request) {
	h.store.Clear(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type homeHandlers struct {
	auth *authContext
}

// start is the home page: the module cards for the signed-in user's role.
func (h *homeHandlers) start(w http.ResponseWriter, r *http.Request) {
	sess := h.auth.session(r)
	renderHome(w, "start", PageData{Title: "Home", Session: sess, Modules: modulesFor(sess.Role)})
}

// workPermit is the only Work Permit Register page: the module is still under development, so the
// card on the home page opens a blank "under development" page. (The JSON API in
// handlers_workpermitregister.go is separate and unchanged.)
func (h *homeHandlers) workPermit(w http.ResponseWriter, r *http.Request) {
	renderHome(w, "workpermit", PageData{Title: "Work Permit Register", Session: h.auth.session(r)})
}

// settings is the admin-only Settings page (route is wrapped in requireAdmin).
func (h *homeHandlers) settings(w http.ResponseWriter, r *http.Request) {
	render(w, "settings", PageData{
		Title: "Settings", Heading: "Settings", Subheading: "Administrators only.",
		Active: "settings", Session: h.auth.session(r),
	})
}
