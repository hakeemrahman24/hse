package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"entry-permit/internal/staff"
)

// UsersData is what the Users and My account templates receive.
type UsersData struct {
	Accounts   []staff.Account
	Roles      []staff.Role
	DefaultPIN string
	Flash, Err string
}

func (a *App) userRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /staff/admin/users", a.adminUsers)
	mux.HandleFunc("POST /staff/admin/users", a.adminUserAdd)
	mux.HandleFunc("POST /staff/admin/users/{username}", a.adminUserAction)
	mux.HandleFunc("GET /account", a.accountPage)
	mux.HandleFunc("POST /account/profile", a.accountProfile)
	mux.HandleFunc("POST /account/pin", a.accountPIN)
}

// staffError turns a store error into a message the person can read. Unknown
// errors are logged and shown as a generic line.
func staffError(err error) string {
	for _, known := range []error{staff.ErrBadPIN, staff.ErrUsername, staff.ErrExists, staff.ErrNoRoles, staff.ErrNoName,
		staff.ErrNotFound, staff.ErrWrongPIN, staff.ErrLastAdmin, staff.ErrLocked, staff.ErrMismatch} {
		if errors.Is(err, known) {
			s := known.Error()
			return strings.ToUpper(s[:1]) + s[1:] + "."
		}
	}
	log.Printf("staff save: %v", err)
	return "Could not save. Please try again."
}

func redirectMsg(w http.ResponseWriter, r *http.Request, path string, err error, done string) {
	q := url.Values{"ok": {done}}
	if err != nil {
		q = url.Values{"err": {staffError(err)}}
	}
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	accounts, err := a.Store.All(r.Context())
	if err != nil {
		log.Printf("admin users: %v", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	v := a.adminView(r, u, "Users", AdminData{})
	v.Users = UsersData{Accounts: accounts, Roles: staff.Priority, DefaultPIN: staff.DefaultPIN, Flash: flash(r, "ok"), Err: flash(r, "err")}
	a.render(w, "admin_users", http.StatusOK, v)
}

func rolesFromForm(r *http.Request) []staff.Role {
	var roles []staff.Role
	for _, v := range r.PostForm["role"] {
		roles = append(roles, staff.Role(v)) // validated by the store
	}
	return roles
}

func (a *App) adminUserAdd(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	_, err := a.Store.CreateByAdmin(r.Context(), u.Username, r.PostFormValue("username"), r.PostFormValue("display"), r.PostFormValue("email"), rolesFromForm(r))
	redirectMsg(w, r, "/staff/admin/users", err, "Added. They sign in with the default PIN "+staff.DefaultPIN+" and can change it under My account.")
}

func (a *App) adminUserAction(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	name, ctx := r.PathValue("username"), r.Context()
	var err error
	var done string
	switch r.PostFormValue("action") {
	case "save":
		err, done = a.Store.AdminUpdate(ctx, u.Username, name, r.PostFormValue("display"), r.PostFormValue("email"), rolesFromForm(r)), "Saved."
	case "reset_pin":
		err, done = a.Store.ResetPIN(ctx, u.Username, name), "PIN reset to the default "+staff.DefaultPIN+"."
	case "unlock":
		err, done = a.Store.AdminUnlock(ctx, u.Username, name), "Unlocked."
	case "off":
		err, done = a.Store.AdminSetActive(ctx, u.Username, name, false), "Switched off."
	case "on":
		err, done = a.Store.AdminSetActive(ctx, u.Username, name, true), "Switched on."
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	redirectMsg(w, r, "/staff/admin/users", err, done)
}

// userPost is the signed-in check plus form parsing and the CSRF token, for
// pages every staff member may use.
func (a *App) userPost(w http.ResponseWriter, r *http.Request) *staff.User {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	if err := r.ParseForm(); err != nil || !a.csrfOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	return u
}

func (a *App) accountPage(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "account", http.StatusOK, View{Title: "My account", User: u, Links: menu(*u, ""), CSRF: a.csrfToken(r),
		Users: UsersData{Flash: flash(r, "ok"), Err: flash(r, "err")}})
}

func (a *App) accountProfile(w http.ResponseWriter, r *http.Request) {
	u := a.userPost(w, r)
	if u == nil {
		return
	}
	err := a.Store.UpdateProfile(r.Context(), u.ID, r.PostFormValue("username"), r.PostFormValue("display"))
	redirectMsg(w, r, "/account", err, "Saved.")
}

func (a *App) accountPIN(w http.ResponseWriter, r *http.Request) {
	u := a.userPost(w, r)
	if u == nil {
		return
	}
	if r.PostFormValue("new") != r.PostFormValue("again") {
		redirectMsg(w, r, "/account", staff.ErrMismatch, "")
		return
	}
	err := a.Store.ChangeOwnPIN(r.Context(), u.ID, r.PostFormValue("current"), r.PostFormValue("new"))
	redirectMsg(w, r, "/account", err, "PIN changed.")
}
