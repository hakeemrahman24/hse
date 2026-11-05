package mcusubmit

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	resetTTL       = time.Hour
	emailChangeTTL = 24 * time.Hour
	// A signed-in person may start this many email changes an hour. Each one sends two mails.
	emailChangePerHour = 5
)

// ---- forgot and reset password ----------------------------------------------------------------

func (a *API) forgotForm(w http.ResponseWriter, status int, email, errMsg string) {
	p := a.newPage("Forgot password")
	p.Error, p.Form = errMsg, map[string]string{"email": email}
	a.render(w, status, "forgot.html", p)
}

// ForgotPage asks for the email address of the account.
func (a *API) ForgotPage(w http.ResponseWriter, r *http.Request) {
	if a.signUpClosed(w, r) {
		return
	}
	a.forgotForm(w, http.StatusOK, "", "")
}

// ForgotSubmit mails a reset link to an active account. The reply is the same for every address,
// known or not, so the form cannot be used to find out who has an account (same rule as
// registration); the work behind it is rate-limited per address and per visitor.
func (a *API) ForgotSubmit(w http.ResponseWriter, r *http.Request) {
	if a.signUpClosed(w, r) {
		return
	}
	if !parseForm(w, r) {
		a.forgotForm(w, http.StatusBadRequest, "", "The form could not be read.")
		return
	}
	if !a.passedCheck(r) {
		a.forgotForm(w, http.StatusBadRequest, r.PostFormValue("email"), "The security check failed. Please try again.")
		return
	}
	reply := func() {
		a.message(w, http.StatusOK, "Check your email",
			"If that address belongs to an active account, we have sent a link to choose a new password. It works for one hour.", nil)
	}
	ip := a.clientIP(r)
	if a.lim.Count("fg:"+ip, registerWindow) >= a.limits.ForgotPerIP {
		reply()
		return
	}
	a.lim.Add("fg:" + ip)
	email, err := ParseEmail(r.PostFormValue("email"))
	if err != nil {
		reply()
		return
	}
	if a.lim.Count("fge:"+email, registerWindow) >= a.limits.ForgotPerEmail {
		reply()
		return
	}
	a.lim.Add("fge:" + email)

	u, err := a.Store.ByEmail(r.Context(), email)
	if err == nil && u.Status == StatusActive && !u.IsBuiltIn {
		token, err := a.Store.IssueToken(r.Context(), u.ID, PurposeReset, "", resetTTL)
		if err != nil {
			log.Printf("mcusubmit: issue reset token: %v", err)
		} else {
			a.sendMail(r.Context(), u.Email, "Reset your password", resetMail(a.baseURL, token))
			a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "ResetRequested")
		}
	}
	reply()
}

func (a *API) resetForm(w http.ResponseWriter, status int, token, errMsg string) {
	p := a.newPage("Choose a new password")
	p.Token, p.Error = token, errMsg
	a.render(w, status, "reset.html", p)
}

// ResetPage shows the new-password form for a valid link. It does not use the link up.
func (a *API) ResetPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	if _, err := a.Store.PeekToken(r.Context(), token, PurposeReset); err != nil {
		invalidLink(a, w)
		return
	}
	a.resetForm(w, http.StatusOK, token, "")
}

// ResetSubmit checks everything first and uses the link last, so a typo or a weak password never
// costs the person their one-time link. SetPassword also signs out every session and clears a lockout.
func (a *API) ResetSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		invalidLink(a, w)
		return
	}
	token, password := r.PostFormValue("token"), r.PostFormValue("password")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	tok, err := a.Store.PeekToken(r.Context(), token, PurposeReset)
	if err != nil {
		invalidLink(a, w)
		return
	}
	u, err := a.Store.ByID(r.Context(), tok.SubmitterID)
	if err != nil || u.Status != StatusActive || u.IsBuiltIn {
		invalidLink(a, w)
		return
	}
	if password != r.PostFormValue("confirm") {
		a.resetForm(w, http.StatusBadRequest, token, "The two passwords are not the same.")
		return
	}
	if err := ValidatePassword(password, u.Username, u.Email); err != nil {
		a.resetForm(w, http.StatusBadRequest, token, err.Error())
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, "hash password", err)
		return
	}
	if _, err := a.Store.ConsumeToken(r.Context(), token, PurposeReset); err != nil {
		invalidLink(a, w)
		return
	}
	if err := a.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		a.serverError(w, "reset password", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "PasswordChanged")
	a.message(w, http.StatusOK, "Password changed", "Your password was changed and every device was signed out. You can sign in with the new password.", nil)
}

// ---- settings ---------------------------------------------------------------------------------

func (a *API) settingsPage(w http.ResponseWriter, status int, u *Submitter, errMsg, notice string) {
	p := a.newPage("Settings")
	p.User, p.Error, p.Notice = u, errMsg, notice
	a.render(w, status, "settings.html", p)
}

// SettingsPage shows the three forms: password, email, reminders.
func (a *API) SettingsPage(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	a.settingsPage(w, http.StatusOK, u, "", "")
}

// checkCurrentPassword makes a sensitive change prove that the person at the keyboard knows the
// password, so a stolen session cookie alone cannot lock the owner out or move the account to
// another mailbox. A wrong answer counts towards the same five-strikes lockout as sign-in, so the
// form cannot be used to guess the password either. It returns a message when the change must stop.
func (a *API) checkCurrentPassword(ctx context.Context, u *Submitter, given string) string {
	if locked, err := a.Store.IsLocked(ctx, u.ID); err != nil || locked {
		return "This account is locked for a few minutes after too many wrong passwords."
	}
	hash, err := a.Store.passwordHash(ctx, u.ID)
	if err != nil || hash == "" || !CheckPassword(hash, given) {
		if err := a.Store.RecordLoginFailure(ctx, u.ID); err != nil {
			log.Printf("mcusubmit: record failure: %v", err)
		}
		return "The current password is not correct."
	}
	return ""
}

// ChangePassword sets a new password. Every other session is signed out (SessionVersion goes up),
// so this one gets a fresh cookie to stay signed in.
func (a *API) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !parseForm(w, r) {
		a.settingsPage(w, http.StatusBadRequest, u, "The form could not be read.", "")
		return
	}
	if msg := a.checkCurrentPassword(r.Context(), u, r.PostFormValue("current")); msg != "" {
		a.settingsPage(w, http.StatusBadRequest, u, msg, "")
		return
	}
	password := r.PostFormValue("password")
	if password != r.PostFormValue("confirm") {
		a.settingsPage(w, http.StatusBadRequest, u, "The two new passwords are not the same.", "")
		return
	}
	if err := ValidatePassword(password, u.Username, u.Email); err != nil {
		a.settingsPage(w, http.StatusBadRequest, u, err.Error(), "")
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, "hash password", err)
		return
	}
	if err := a.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		a.serverError(w, "change password", err)
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
	a.settingsPage(w, http.StatusOK, &fresh, "", "Password changed. Your other devices were signed out.")
}

// RequestEmailChange starts an email change. The login email does not change until the link sent
// to the new address is opened and confirmed; the old address gets a warning right away, so a
// stolen session cannot move the account silently.
func (a *API) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if u.IsBuiltIn { // the hidden admin has no mailbox; its address can never receive mail
		a.notFound(w, u)
		return
	}
	if !parseForm(w, r) {
		a.settingsPage(w, http.StatusBadRequest, u, "The form could not be read.", "")
		return
	}
	if msg := a.checkCurrentPassword(r.Context(), u, r.PostFormValue("current")); msg != "" {
		a.settingsPage(w, http.StatusBadRequest, u, msg, "")
		return
	}
	email, err := ParseEmail(r.PostFormValue("email"))
	if err != nil {
		a.settingsPage(w, http.StatusBadRequest, u, "Enter a valid email address.", "")
		return
	}
	if email == u.Email {
		a.settingsPage(w, http.StatusBadRequest, u, "That is already your email address.", "")
		return
	}
	if !a.MailDomainOK(r.Context(), email[strings.LastIndex(email, "@")+1:]) {
		a.settingsPage(w, http.StatusBadRequest, u, "That email domain cannot receive mail. Check the address.", "")
		return
	}
	if _, err := a.Store.ByEmail(r.Context(), email); err == nil {
		a.settingsPage(w, http.StatusBadRequest, u, "That address is already used by another account.", "")
		return
	} else if !errors.Is(err, ErrNotFound) {
		a.serverError(w, "email lookup", err)
		return
	}
	key := "ec:" + strconv.FormatInt(u.ID, 10)
	if a.lim.Count(key, registerWindow) >= emailChangePerHour {
		a.settingsPage(w, http.StatusTooManyRequests, u, "Too many email change requests. Please wait an hour.", "")
		return
	}
	a.lim.Add(key)
	token, err := a.Store.IssueToken(r.Context(), u.ID, PurposeEmailChange, email, emailChangeTTL)
	if err != nil {
		a.serverError(w, "issue email token", err)
		return
	}
	a.sendMail(r.Context(), email, "Confirm your new email address", emailChangeMail(a.baseURL, token))
	a.sendMail(r.Context(), u.Email, "Your email change request", emailChangeNoticeMail(a.baseURL))
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "EmailChangeRequested")
	a.settingsPage(w, http.StatusOK, u, "", "We sent a confirmation link to the new address. Your login email changes when you open it.")
}

// ChangeUsername sets a new username after the current password is given again, like the email
// change: a stolen session cookie alone must not be able to rename the account.
func (a *API) ChangeUsername(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !parseForm(w, r) {
		a.settingsPage(w, http.StatusBadRequest, u, "The form could not be read.", "")
		return
	}
	if msg := a.checkCurrentPassword(r.Context(), u, r.PostFormValue("current")); msg != "" {
		a.settingsPage(w, http.StatusBadRequest, u, msg, "")
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	if err := ValidUsername(username); err != nil {
		msg := "3 to 40 letters, digits, dots or underscores."
		if strings.EqualFold(username, builtInUsername) {
			msg = "That username is taken. Choose another."
		}
		a.settingsPage(w, http.StatusBadRequest, u, msg, "")
		return
	}
	err := a.Store.ChangeUsername(r.Context(), u.ID, username)
	switch {
	case errors.Is(err, ErrUsernameTaken):
		a.settingsPage(w, http.StatusConflict, u, "That username is taken. Choose another.", "")
		return
	case errors.Is(err, ErrNotFound):
		a.notFound(w, u)
		return
	case err != nil:
		a.serverError(w, "change username", err)
		return
	}
	fresh, err := a.Store.ByID(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "reload account", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "UsernameChanged")
	a.settingsPage(w, http.StatusOK, &fresh, "", "Username changed. Use it the next time you sign in.")
}

// SaveReminders stores the checkbox. A browser sends nothing for an unchecked box, so "absent" is "off".
func (a *API) SaveReminders(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !parseForm(w, r) {
		a.settingsPage(w, http.StatusBadRequest, u, "The form could not be read.", "")
		return
	}
	on := r.PostFormValue("reminders") == "on"
	if err := a.Store.SetReminders(r.Context(), u.ID, on); err != nil {
		a.serverError(w, "set reminders", err)
		return
	}
	fresh, err := a.Store.ByID(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "reload account", err)
		return
	}
	a.settingsPage(w, http.StatusOK, &fresh, "", "Saved.")
}

// ---- email change link ------------------------------------------------------------------------

// EmailChangePage shows a Confirm button for a valid link and changes nothing: mail scanners open
// every link, so only the button's POST may use it.
func (a *API) EmailChangePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	tok, err := a.Store.PeekToken(r.Context(), token, PurposeEmailChange)
	if err != nil {
		invalidLink(a, w)
		return
	}
	p := a.newPage("Confirm your new email")
	p.Token, p.Message = token, tok.NewEmail
	a.render(w, http.StatusOK, "emailchange.html", p)
}

// EmailChangeSubmit applies the change. The address is checked again here, because someone else
// may have registered it since the link was sent; the link is used up only when the change can go through.
func (a *API) EmailChangeSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		invalidLink(a, w)
		return
	}
	token := r.PostFormValue("token")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	tok, err := a.Store.PeekToken(r.Context(), token, PurposeEmailChange)
	if err != nil {
		invalidLink(a, w)
		return
	}
	u, err := a.Store.ByID(r.Context(), tok.SubmitterID)
	if err != nil || u.Status != StatusActive || u.IsBuiltIn {
		invalidLink(a, w)
		return
	}
	if other, err := a.Store.ByEmail(r.Context(), tok.NewEmail); err == nil && other.ID != u.ID {
		a.message(w, http.StatusConflict, "Address already used", "Another account now uses that email address, so the change cannot be made.", nil)
		return
	}
	if _, err := a.Store.ConsumeToken(r.Context(), token, PurposeEmailChange); err != nil {
		invalidLink(a, w)
		return
	}
	if err := a.Store.ChangeEmail(r.Context(), u.ID, tok.NewEmail); err != nil {
		if errors.Is(err, ErrEmailTaken) {
			a.message(w, http.StatusConflict, "Address already used", "Another account now uses that email address, so the change cannot be made.", nil)
			return
		}
		a.serverError(w, "change email", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "EmailChanged")
	a.message(w, http.StatusOK, "Email changed", "Your login email is now "+tok.NewEmail+".", nil)
}
