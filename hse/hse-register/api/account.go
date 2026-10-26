package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxUsernameLen = 64 // admin_users.username is VARCHAR(64)
	minPasswordLen = 8
)

// accountChange is what the "My account" form asks for.
type accountChange struct {
	Username, NewPassword, Confirm string
}

// validateAccountChange checks the new values. It returns the message to show, or "".
// An empty new password means "keep the password".
func validateAccountChange(c accountChange) string {
	switch {
	case c.Username == "":
		return "The username cannot be empty."
	case len(c.Username) > maxUsernameLen:
		return "The username can be at most 64 characters."
	case strings.ContainsAny(c.Username, " \t\r\n"):
		return "The username cannot contain spaces."
	case c.NewPassword != "" && len(c.NewPassword) < minPasswordLen:
		return "The new password must be at least 8 characters."
	case c.NewPassword != c.Confirm:
		return "The new password and its confirmation do not match."
	}
	return ""
}

type accountHandlers struct {
	db   *sql.DB
	auth *authContext
	lim  *limiter
}

func (h *accountHandlers) page(w http.ResponseWriter, r *http.Request, status int, flash, kind string) {
	renderStatus(w, status, "account", PageData{
		Title: "My account", Heading: "My account", Active: "account",
		Subheading: "Change your own username or password. Your current password is always needed.",
		Session:    h.auth.session(r), Flash: flash, FlashKind: kind,
	})
}

func (h *accountHandlers) show(w http.ResponseWriter, r *http.Request) {
	flash, kind := "", "success"
	switch {
	case h.auth.session(r).MustChange:
		flash, kind = "Your password was reset. Choose a new password to continue.", "warning"
	case r.URL.Query().Get("done") == "saved":
		flash = "Saved."
	}
	h.page(w, r, http.StatusOK, flash, kind)
}

// submit changes the signed-in user's own username and/or password. The current password is
// always required, so a borrowed, unlocked session cannot take the account over.
func (h *accountHandlers) submit(w http.ResponseWriter, r *http.Request) {
	sess := h.auth.session(r)
	key := "account:" + sess.Username
	if h.lim.locked(key) {
		h.page(w, r, http.StatusTooManyRequests, "Too many wrong passwords. Try again in a few minutes.", "error")
		return
	}
	var hash string
	err := h.db.QueryRowContext(r.Context(), `SELECT password_hash FROM admin_users WHERE id = ?`, sess.UserID).Scan(&hash)
	if err != nil {
		log.Printf("account lookup: %v", err)
		http.Error(w, "Could not load your account.", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("current_password"))) != nil {
		h.lim.fail(key, 5, 10*time.Minute, 15*time.Minute)
		h.page(w, r, http.StatusUnprocessableEntity, "The current password is not correct.", "error")
		return
	}
	change := accountChange{
		Username:    strings.TrimSpace(r.FormValue("username")),
		NewPassword: r.FormValue("new_password"), Confirm: r.FormValue("confirm_password"),
	}
	if msg := validateAccountChange(change); msg != "" {
		h.page(w, r, http.StatusUnprocessableEntity, msg, "error")
		return
	}
	if sess.MustChange && change.NewPassword == "" {
		h.page(w, r, http.StatusUnprocessableEntity, "Choose a new password to continue.", "error")
		return
	}
	var newHash string
	if change.NewPassword != "" {
		if newHash, err = HashPassword(change.NewPassword); err != nil {
			http.Error(w, "Could not save the password.", http.StatusInternalServerError)
			return
		}
	}
	upd := accountUpdate{ID: sess.UserID, OldUsername: sess.Username, Username: change.Username}
	if change.NewPassword != "" {
		upd.PasswordHash = newHash
	}
	if err := updateAccount(r.Context(), h.db, upd); err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			h.page(w, r, http.StatusUnprocessableEntity, "That username is already taken.", "error")
			return
		}
		log.Printf("account update: %v", err)
		http.Error(w, "Could not save your account.", http.StatusInternalServerError)
		return
	}
	// The cookie carries the username, so it must be reissued or the page would show the old one.
	renewed := *sess
	renewed.Username = change.Username
	if err := h.auth.store.Set(w, renewed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/account?done=saved", http.StatusSeeOther)
}
