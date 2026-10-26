package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type AdminUser struct {
	ID        int64
	Username  string
	FullName  string
	Role      string
	CreatedAt time.Time
	Archived  bool
}

type adminUserHandlers struct {
	db   *sql.DB
	auth *authContext
}

func (h *adminUserHandlers) load(r *http.Request, id int64) (*AdminUser, error) {
	var u AdminUser
	err := h.db.QueryRowContext(r.Context(), `SELECT id, username, full_name, role, created_at, archived_at IS NOT NULL FROM admin_users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.FullName, &u.Role, &u.CreatedAt, &u.Archived)
	return &u, err
}

func userID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// flashes are the confirmations shown after an action (?msg=...).
func (h *adminUserHandlers) flash(r *http.Request) string {
	q := r.URL.Query()
	who := q.Get("who")
	switch q.Get("msg") {
	case "created":
		return "User added."
	case "updated":
		return "Saved."
	case "archived":
		return who + " is archived. They can no longer sign in; nothing was deleted. Use Restore to bring them back."
	case "restored":
		return who + " is restored and can sign in again."
	case "deleted":
		return who + " was deleted."
	case "reset":
		return "Password of " + who + " is now the default (" + defaultPassword() + "). Tell them; they must choose a new one when they sign in."
	}
	return ""
}

func (h *adminUserHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.listPage(w, r, http.StatusOK, h.flash(r), "success")
}

func (h *adminUserHandlers) listPage(w http.ResponseWriter, r *http.Request, status int, flash, kind string) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT id, username, full_name, role, created_at, archived_at IS NOT NULL FROM admin_users ORDER BY archived_at IS NOT NULL, username`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	users := []*AdminUser{}
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.FullName, &u.Role, &u.CreatedAt, &u.Archived); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		users = append(users, &u)
	}
	renderStatus(w, status, "users_list", PageData{
		Title: "Users", Heading: "Users", Active: "users",
		Session: h.auth.session(r), Users: users, Flash: flash, FlashKind: kind,
	})
}

func (h *adminUserHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	render(w, "users_form", PageData{
		Title: "Add user", Heading: "Add user", Active: "users-new",
		Session: h.auth.session(r),
	})
}

// formPage shows the add/edit form again with a message (the typed values are kept).
func (h *adminUserHandlers) formPage(w http.ResponseWriter, r *http.Request, status int, u *AdminUser, msg string) {
	title := "Add user"
	if u != nil && u.ID != 0 {
		title = "Edit user"
	}
	renderStatus(w, status, "users_form", PageData{
		Title: title, Heading: title, Active: "users", Session: h.auth.session(r),
		EditUser: u, Flash: msg, FlashKind: "error",
	})
}

func roleFrom(v string) string {
	// Only the two known roles are accepted; anything else falls back to the least-privileged one.
	if v == roleAdmin {
		return roleAdmin
	}
	return roleUser
}

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func (h *adminUserHandlers) create(w http.ResponseWriter, r *http.Request) {
	u := &AdminUser{Username: strings.TrimSpace(r.FormValue("username")), FullName: strings.TrimSpace(r.FormValue("full_name")), Role: roleFrom(r.FormValue("role"))}
	password := r.FormValue("password")
	if msg := validateAccountChange(accountChange{Username: u.Username, NewPassword: password, Confirm: password}); msg != "" || password == "" {
		if msg == "" {
			msg = "A password is required."
		}
		h.formPage(w, r, http.StatusUnprocessableEntity, u, msg)
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `INSERT INTO admin_users (username, full_name, role, password_hash) VALUES (?, ?, ?, ?)`, u.Username, u.FullName, u.Role, hash); err != nil {
		if isDuplicate(err) {
			h.formPage(w, r, http.StatusUnprocessableEntity, u, "That username is already taken.")
			return
		}
		log.Printf("user insert: %v", err)
		http.Error(w, "Could not add the user.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=created", http.StatusSeeOther)
}

func (h *adminUserHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	u, err := h.load(r, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	render(w, "users_form", PageData{Title: "Edit user", Heading: "Edit user", Active: "users", Session: h.auth.session(r), EditUser: u})
}

func (h *adminUserHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cur, err := h.load(r, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	edited := *cur
	edited.Username = strings.TrimSpace(r.FormValue("username"))
	edited.FullName = strings.TrimSpace(r.FormValue("full_name"))
	edited.Role = roleFrom(r.FormValue("role"))
	newPassword := r.FormValue("new_password")
	if msg := validateAccountChange(accountChange{Username: edited.Username, NewPassword: newPassword, Confirm: newPassword}); msg != "" {
		h.formPage(w, r, http.StatusUnprocessableEntity, &edited, msg)
		return
	}
	if cur.Role == roleAdmin && edited.Role != roleAdmin {
		if msg, err := h.mayLoseAdmin(r, cur, "remove the Admin role from"); err != nil || msg != "" {
			h.refuse(w, err, func() { h.formPage(w, r, http.StatusConflict, &edited, msg) })
			return
		}
	}
	upd := accountUpdate{ID: id, OldUsername: cur.Username, Username: edited.Username, FullName: &edited.FullName, Role: &edited.Role}
	if newPassword != "" {
		if upd.PasswordHash, err = HashPassword(newPassword); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := updateAccount(r.Context(), h.db, upd); err != nil {
		if isDuplicate(err) {
			h.formPage(w, r, http.StatusUnprocessableEntity, &edited, "That username is already taken.")
			return
		}
		log.Printf("user update: %v", err)
		http.Error(w, "Could not save the user.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=updated", http.StatusSeeOther)
}

// mayLoseAdmin explains why an administrator cannot be archived, deleted or demoted ("" = fine).
// You cannot do it to yourself, and the last administrator who can sign in must stay.
func (h *adminUserHandlers) mayLoseAdmin(r *http.Request, u *AdminUser, verb string) (string, error) {
	if sess := h.auth.session(r); sess != nil && sess.UserID == u.ID {
		return "You cannot " + verb + " the account you are signed in with. Ask another administrator.", nil
	}
	if u.Role != roleAdmin || u.Archived {
		return "", nil
	}
	n, err := otherActiveAdmins(r.Context(), h.db, u.ID)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "There must always be one administrator who can sign in. Make someone else an administrator first.", nil
	}
	return "", nil
}

func (h *adminUserHandlers) refuse(w http.ResponseWriter, err error, show func()) {
	if err != nil {
		log.Printf("user check: %v", err)
		http.Error(w, "Could not check the accounts.", http.StatusInternalServerError)
		return
	}
	show()
}

func (h *adminUserHandlers) target(w http.ResponseWriter, r *http.Request) (*AdminUser, bool) {
	id, ok := userID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	u, err := h.load(r, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return u, true
}

func (h *adminUserHandlers) archive(w http.ResponseWriter, r *http.Request) {
	u, ok := h.target(w, r)
	if !ok {
		return
	}
	if msg, err := h.mayLoseAdmin(r, u, "archive"); err != nil || msg != "" {
		h.refuse(w, err, func() { h.listPage(w, r, http.StatusConflict, msg, "error") })
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE admin_users SET archived_at = NOW() WHERE id = ? AND archived_at IS NULL`, u.ID); err != nil {
		http.Error(w, "Could not archive the user.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=archived&who="+url.QueryEscape(u.Username), http.StatusSeeOther)
}

func (h *adminUserHandlers) restore(w http.ResponseWriter, r *http.Request) {
	u, ok := h.target(w, r)
	if !ok {
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE admin_users SET archived_at = NULL WHERE id = ?`, u.ID); err != nil {
		http.Error(w, "Could not restore the user.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=restored&who="+url.QueryEscape(u.Username), http.StatusSeeOther)
}

// resetPassword sets the default password and makes the person choose a new one at next sign-in.
func (h *adminUserHandlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := h.target(w, r)
	if !ok {
		return
	}
	if sess := h.auth.session(r); sess != nil && sess.UserID == u.ID {
		h.listPage(w, r, http.StatusConflict, "To change your own password use My account.", "error")
		return
	}
	hash, err := HashPassword(defaultPassword())
	if err != nil {
		http.Error(w, "Could not reset the password.", http.StatusInternalServerError)
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE admin_users SET password_hash = ?, must_change_password = 1 WHERE id = ?`, hash, u.ID); err != nil {
		http.Error(w, "Could not reset the password.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=reset&who="+url.QueryEscape(u.Username), http.StatusSeeOther)
}

// postsBy counts the observations a person entered (they remember it by username).
func (h *adminUserHandlers) postsBy(r *http.Request, username string) (int, error) {
	var n int
	err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM observations_observation WHERE created_by = ?`, username).Scan(&n)
	return n, err
}

// confirmDelete is the page that spells out what deleting does, before anything is deleted.
func (h *adminUserHandlers) confirmDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := h.target(w, r)
	if !ok {
		return
	}
	if msg, err := h.mayLoseAdmin(r, u, "delete"); err != nil || msg != "" {
		h.refuse(w, err, func() { h.listPage(w, r, http.StatusConflict, msg, "error") })
		return
	}
	n, err := h.postsBy(r, u.Username)
	if err != nil {
		http.Error(w, "Could not count the posts.", http.StatusInternalServerError)
		return
	}
	render(w, "users_delete", PageData{Title: "Delete user", Heading: "Delete " + u.Username + "?", Active: "users", Session: h.auth.session(r), EditUser: u, PostCount: n})
}

// delete removes the account for good. The posts stay; they are detached from the account
// (created_by cleared) so that a new person who later takes the same username does not inherit them.
func (h *adminUserHandlers) delete(w http.ResponseWriter, r *http.Request) {
	u, ok := h.target(w, r)
	if !ok {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Redirect(w, r, "/users/"+strconv.FormatInt(u.ID, 10)+"/delete", http.StatusSeeOther)
		return
	}
	if msg, err := h.mayLoseAdmin(r, u, "delete"); err != nil || msg != "" {
		h.refuse(w, err, func() { h.listPage(w, r, http.StatusConflict, msg, "error") })
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "Could not delete the user.", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE observations_observation SET created_by = NULL WHERE created_by = ?`, u.Username); err != nil {
		http.Error(w, "Could not delete the user.", http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM admin_users WHERE id = ?`, u.ID); err != nil {
		http.Error(w, "Could not delete the user.", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Could not delete the user.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users?msg=deleted&who="+url.QueryEscape(u.Username), http.StatusSeeOther)
}
