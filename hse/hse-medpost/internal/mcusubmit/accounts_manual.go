package mcusubmit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
)

// Accounts the admin adds by hand (owner, 2026-11-13). The admin types who the person is, the system
// makes a random temporary password and shows it to the admin once, and the person must replace it
// before they can use anything else. No fixed "default" password exists: a password every new
// account shares would be the first one anybody tries. The password is not mailed (mail is not a
// safe place for a password); the admin hands it over the way they already reach the person.

// tempPasswordAlphabet leaves out look-alikes (0 O 1 l I) so it can be read over a phone or typed
// from a WhatsApp message without mistakes.
const (
	tempPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	tempPasswordLen      = 12
)

// NewTempPassword returns a random temporary password from crypto/rand.
func NewTempPassword() (string, error) {
	out := make([]byte, tempPasswordLen)
	max := big.NewInt(int64(len(tempPasswordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("temporary password: %w", err)
		}
		out[i] = tempPasswordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// EnsureMustChangePasswordColumn adds Submitter.MustChangePassword to an older SQLite database;
// MariaDB gets it from migrations/mysql/048, run by hand BEFORE the build that reads it is deployed.
func EnsureMustChangePasswordColumn(database *sql.DB) error {
	rows, err := database.Query(`PRAGMA table_info(Submitter)`)
	if err != nil {
		return fmt.Errorf("read Submitter columns: %w", err)
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var col, ctype string
		var dflt any
		if err := rows.Scan(&cid, &col, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		has = has || col == "MustChangePassword"
	}
	rows.Close()
	if err := rows.Err(); err != nil || has {
		return err
	}
	_, err = database.Exec(`ALTER TABLE Submitter ADD COLUMN MustChangePassword INTEGER NOT NULL DEFAULT 0`)
	return err
}

// CreateByAdmin makes an Active account straight away: the admin vouches for the person, so there is
// no email confirmation, no request queue and no setup link. The email is marked verified for the
// same reason. MustChangePassword makes the first sign-in end at the change-password page.
func (s *Store) CreateByAdmin(ctx context.Context, adminID int64, email, name string, companyID int64, position, username, passwordHash string) (int64, error) {
	email, username = normalizeEmail(email), strings.ToLower(strings.TrimSpace(username))
	if err := ValidUsername(username); err != nil {
		return 0, err
	}
	if _, err := s.ByEmail(ctx, email); err == nil {
		return 0, ErrEmailTaken
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	if _, err := s.ByUsername(ctx, username); err == nil {
		return 0, ErrUsernameTaken
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	if room, err := s.CompanyHasRoom(ctx, companyID); err != nil {
		return 0, err
	} else if !room {
		return 0, ErrCompanyFull
	}
	now := s.stamp()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO Submitter (Email, Username, PasswordHash, Name, FK_CompanyID, PositionText, Status, EmailVerifiedAt,
		                        ApprovedAt, FK_ApprovedBySubmitterID, MustChangePassword, CreatedAt)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		email, username, passwordHash, name, companyID, position, StatusActive, now, now, adminID, now)
	if isUniqueViolation(err) {
		return 0, ErrEmailTaken // a second request raced past the checks above; either key may have been the clash
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MustChangePassword says whether the account still has the temporary password. A failed lookup is the
// caller's to handle (requireUser lets the person in and logs, like the ROMIF check: a database
// hiccup must not lock everybody out).
func (s *Store) MustChangePassword(ctx context.Context, id int64) (bool, error) {
	var flag int
	err := s.DB.QueryRowContext(ctx, `SELECT MustChangePassword FROM Submitter WHERE SubmitterID = ?`, id).Scan(&flag)
	return flag == 1, err
}

// AdminCreate handles the admin's "Add account" form and shows the temporary password once.
func (a *API) AdminCreate(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.message(w, http.StatusBadRequest, "Invalid request", "The form could not be read.", admin)
		return
	}
	name, position := SanitizeName(r.PostFormValue("name")), SanitizeJobTitle(r.PostFormValue("position"))
	email, emailErr := ParseEmail(r.PostFormValue("email"))
	companyID, okCompany := parseID(r.PostFormValue("company"))
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	switch {
	case name == "" || emailErr != nil || !okCompany || position == "":
		a.message(w, http.StatusBadRequest, "Check the form", "Name, a valid email, company and position are all needed.", admin)
		return
	case ValidUsername(username) != nil:
		a.message(w, http.StatusBadRequest, "Check the form", ValidUsername(username).Error()+".", admin)
		return
	}
	temp, err := NewTempPassword()
	if err != nil {
		a.serverError(w, "temporary password", err)
		return
	}
	hash, err := HashPassword(temp)
	if err != nil {
		a.serverError(w, "hash temporary password", err)
		return
	}
	id, err := a.Store.CreateByAdmin(r.Context(), admin.ID, email, name, companyID, position, username, hash)
	switch {
	case errors.Is(err, ErrEmailTaken):
		a.message(w, http.StatusConflict, "Already used", "An account with that email already exists.", admin)
		return
	case errors.Is(err, ErrUsernameTaken):
		a.message(w, http.StatusConflict, "Already used", "That username is taken. Choose another.", admin)
		return
	case err != nil:
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, "CreatedByAdmin") // never the password
	log.Printf("mcusubmit: admin %d created account %d", admin.ID, id)
	// The password is on this one response only. no-store keeps it out of the browser's cache.
	w.Header().Set("Cache-Control", "no-store")
	a.message(w, http.StatusOK, "Account created",
		"Username: "+username+" - Temporary password: "+temp+" - Give both to "+name+". This is the only time the password is shown. They must choose their own password at the first sign-in.", admin)
}

// PasswordChangePage and PasswordChangeSubmit are the first stop of an account that still has its
// temporary password: nothing else in the portal opens until it is replaced.
func (a *API) PasswordChangePage(w http.ResponseWriter, r *http.Request) {
	u := a.current(r)
	if u == nil {
		http.Redirect(w, r, basePath+"/signin", http.StatusSeeOther)
		return
	}
	if must, err := a.Store.MustChangePassword(r.Context(), u.ID); err == nil && !must {
		http.Redirect(w, r, basePath+"/home", http.StatusSeeOther)
		return
	}
	a.passwordChangeForm(w, http.StatusOK, "")
}

func (a *API) passwordChangeForm(w http.ResponseWriter, status int, errMsg string) {
	p := a.newPage("Choose your password")
	p.Error, p.Signing = errMsg, true
	a.render(w, status, "passwordchange.html", p)
}

func (a *API) PasswordChangeSubmit(w http.ResponseWriter, r *http.Request) {
	u := a.current(r)
	if u == nil {
		http.Redirect(w, r, basePath+"/signin", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r) {
		a.passwordChangeForm(w, http.StatusBadRequest, "The form could not be read.")
		return
	}
	password := r.PostFormValue("password")
	old, err := a.Store.passwordHash(r.Context(), u.ID)
	switch {
	case err != nil:
		a.serverError(w, "read password", err)
		return
	case password != r.PostFormValue("confirm"):
		a.passwordChangeForm(w, http.StatusBadRequest, "The two passwords are not the same.")
		return
	case CheckPassword(old, password):
		a.passwordChangeForm(w, http.StatusBadRequest, "Choose a password different from the temporary one.")
		return
	}
	if err := ValidatePassword(password, u.Username, u.Email); err != nil {
		a.passwordChangeForm(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, "hash password", err)
		return
	}
	if err := a.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		a.serverError(w, "set password", err)
		return
	}
	fresh, err := a.Store.ByID(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "reload account", err)
		return
	}
	if err := a.startSession(w, fresh); err != nil {
		a.serverError(w, "start session", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "PasswordChanged")
	http.Redirect(w, r, basePath+"/home", http.StatusSeeOther)
}
