package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Accounts the admin adds by hand (owner, 2026-11-13). Every such account starts with the same
// default password, DefaultPassword, and must replace it before anything else opens (the portal
// minimum is 6 characters, so the owner chose a simple six-digit one). The person gets a welcome
// email with their account details and a link to choose a new password straight away, so in the
// normal case the default password is never needed.
//
// The default is known to everyone, so the account is only as safe as how soon the person changes
// it: the welcome link works for welcomeTTL, and the admin page says who still has not.
const (
	DefaultPassword = "123456"
	welcomeTTL      = setupTTL // the same 3 days as the link a approved request gets
)

// welcomeMail tells a person their account was made and how to get in. It carries the default
// password because the person needs it if they sign in without using the link.
func welcomeMail(baseURL, name, username, company, position, token string) string {
	return fmt.Sprintf(`Hello %s,

An account was created for you on the medical checkup submission portal.

Name:      %s
Company:   %s
Position:  %s
Username:  %s
Password:  %s (temporary, you must change it at your first sign-in)

To choose your own password now, open this link (it works for 3 days and only once):

%s/mcu-submit/reset?token=%s

Or sign in at %s/mcu-submit/signin with the username and temporary password above; you will be asked for a new password straight away.

If you did not expect this account, contact the medpost administrator.
`, name, name, company, position, username, DefaultPassword, baseURL, token, baseURL)
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
	hash, err := HashPassword(DefaultPassword)
	if err != nil {
		a.serverError(w, "hash default password", err)
		return
	}
	id, err := a.Store.CreateByAdmin(r.Context(), admin.ID, email, name, companyID, position, username, hash)
	switch {
	case errors.Is(err, ErrEmailTaken):
		// Tell the person too (owner, 2026-11-13), same as the request form does for an existing address.
		a.sendMail(r.Context(), email, "You already have an account", alreadyHasAccountMail(a.baseURL))
		a.message(w, http.StatusConflict, "Already used", "An account with that email already exists. A note was sent to "+email+" telling them where to sign in.", admin)
		return
	case errors.Is(err, ErrUsernameTaken):
		a.message(w, http.StatusConflict, "Already used", "That username is taken. Choose another.", admin)
		return
	case err != nil:
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, "CreatedByAdmin")
	// The welcome mail is best effort, like every mail here: sendMail only logs a failure. The account
	// works either way, so the admin is told to pass the details on if the mail does not arrive.
	if token, err := a.Store.IssueToken(r.Context(), id, PurposeReset, "", welcomeTTL); err != nil {
		log.Printf("mcusubmit: account %d created but no welcome link: %v", id, err)
	} else {
		company := a.allCompanies(r.Context())[companyID].Name
		a.sendMail(r.Context(), email, "Your medical checkup portal account", welcomeMail(a.baseURL, name, username, company, position, token))
	}
	a.message(w, http.StatusOK, "Account created",
		"The account for "+name+" is active. Username: "+username+", default password: "+DefaultPassword+". A welcome email with the details and a link to choose a password was sent to "+email+"; if it does not arrive, give them the username and default password yourself. They must choose their own password at the first sign-in.", admin)
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
