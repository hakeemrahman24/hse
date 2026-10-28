package mcusubmit

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

// ---- register and verify ----------------------------------------------------------------------

func (a *API) registerForm(w http.ResponseWriter, r *http.Request, status int, form map[string]string, errMsg string) {
	p := a.newPage("Request an account")
	p.Form, p.Error = form, errMsg
	companies, err := a.companies(r.Context())
	if err != nil {
		a.serverError(w, "companies", err)
		return
	}
	p.Companies = companies
	a.render(w, status, "register.html", p)
}

// RegisterPage shows the request form.
func (a *API) RegisterPage(w http.ResponseWriter, r *http.Request) {
	a.registerForm(w, r, http.StatusOK, map[string]string{}, "")
}

// RegisterSubmit takes a request for an account (plan section 4, step 1). Whatever happens to a
// well-formed request, the visitor sees the same "check your email" page, so the form cannot be
// used to find out which addresses already have an account.
func (a *API) RegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		a.message(w, http.StatusBadRequest, "Invalid request", "The form could not be read.", nil)
		return
	}
	ip := a.clientIP(r)
	if a.lim.Count("reg:"+ip, registerWindow) >= a.limits.RegisterPerIP {
		a.message(w, http.StatusTooManyRequests, "Too many requests", "Please wait a while before trying again.", nil)
		return
	}
	a.lim.Add("reg:" + ip)

	form := map[string]string{
		"name": r.PostFormValue("name"), "email": r.PostFormValue("email"),
		"position": r.PostFormValue("position"), "company_id": r.PostFormValue("company_id"),
	}
	if !a.passedCheck(r) {
		a.registerForm(w, r, http.StatusBadRequest, form, "The security check failed. Please try again.")
		return
	}
	name, position := SanitizeName(form["name"]), SanitizePosition(form["position"])
	email, emailErr := ParseEmail(form["email"])
	companyID, okCompany := parseID(form["company_id"])
	switch {
	case len([]rune(name)) < 2:
		a.registerForm(w, r, http.StatusBadRequest, form, "Enter your full name (letters only).")
		return
	case emailErr != nil:
		a.registerForm(w, r, http.StatusBadRequest, form, emailErr.Error())
		return
	case position == "":
		a.registerForm(w, r, http.StatusBadRequest, form, "Enter your position.")
		return
	case !okCompany:
		a.registerForm(w, r, http.StatusBadRequest, form, "Choose your company from the list.")
		return
	}
	if _, err := a.companyName(r.Context(), companyID); err != nil {
		a.registerForm(w, r, http.StatusBadRequest, form, "Choose your company from the list. If it is missing, ask the medpost administrator to add it.")
		return
	}
	if !a.MailDomainOK(r.Context(), email[strings.LastIndex(email, "@")+1:]) {
		a.registerForm(w, r, http.StatusBadRequest, form, "That email domain cannot receive mail. Check the address.")
		return
	}
	// Past this point the reply never depends on the address.
	reply := func() {
		a.message(w, http.StatusOK, "Check your email",
			"If this address can register, we sent a link to confirm it. The link works for 24 hours.", nil)
	}
	if a.lim.Count("rege:"+email, registerWindow) >= a.limits.RegisterPerEmail {
		reply()
		return
	}
	a.lim.Add("rege:" + email)

	id, err := a.Store.Register(r.Context(), email, name, companyID, position)
	switch {
	case err == nil:
		a.mailVerifyLink(r, id, email)
		a.audit(r, ActorSubmitter, id, EntitySubmitter, id, "Registered")
	case errors.Is(err, ErrEmailTaken):
		existing, lookupErr := a.Store.ByEmail(r.Context(), email)
		switch {
		case lookupErr != nil:
			log.Printf("mcusubmit: lookup after duplicate: %v", lookupErr)
		case existing.Status == StatusRequested && existing.EmailVerifiedAt == "":
			a.mailVerifyLink(r, existing.ID, email) // they lost the first mail: send a fresh link
		default:
			a.sendMail(r.Context(), email, "You already have an account", alreadyRegisteredMail(a.baseURL))
		}
	default:
		a.serverError(w, "register", err)
		return
	}
	reply()
}

func (a *API) mailVerifyLink(r *http.Request, id int64, email string) {
	raw, err := a.Store.IssueToken(r.Context(), id, PurposeVerify, "", verifyTTL)
	if err != nil {
		log.Printf("mcusubmit: issue verify token: %v", err)
		return
	}
	a.sendMail(r.Context(), email, "Confirm your email", verifyMail(a.baseURL, raw))
}

// audit writes an event and only logs a failure: the trail must not break the action it records.
func (a *API) audit(r *http.Request, actorType string, actorID int64, entityType string, entityID int64, action string) {
	if err := a.Store.LogEvent(r.Context(), Event{
		ActorType: actorType, ActorID: actorID, EntityType: entityType, EntityID: entityID, Action: action,
	}); err != nil {
		log.Printf("mcusubmit: audit %s: %v", action, err)
	}
}

func invalidLink(a *API, w http.ResponseWriter) {
	a.message(w, http.StatusBadRequest, "Link not valid", "This link is invalid, has already been used, or has expired.", nil)
}

// VerifyPage shows a Confirm button. It changes nothing: mail scanners and link previews open every
// link, and a GET that used the token up would leave the real person with a dead link.
func (a *API) VerifyPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	if _, err := a.Store.PeekToken(r.Context(), token, PurposeVerify); err != nil {
		invalidLink(a, w)
		return
	}
	p := a.newPage("Confirm your email")
	p.Token = token
	a.render(w, http.StatusOK, "verify.html", p)
}

// VerifySubmit uses the link: the email is confirmed and the request joins the admin's queue.
func (a *API) VerifySubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) || !tokenShape.MatchString(r.PostFormValue("token")) {
		invalidLink(a, w)
		return
	}
	tok, err := a.Store.ConsumeToken(r.Context(), r.PostFormValue("token"), PurposeVerify)
	if err != nil {
		invalidLink(a, w)
		return
	}
	if err := a.Store.MarkVerified(r.Context(), tok.SubmitterID); err != nil {
		a.serverError(w, "mark verified", err)
		return
	}
	a.audit(r, ActorSubmitter, tok.SubmitterID, EntitySubmitter, tok.SubmitterID, "Verified")
	a.message(w, http.StatusOK, "Email confirmed",
		"Thank you. A medpost administrator will review your request. You will get an email when it is decided.", nil)
}

// ---- setup (username and password after approval) ---------------------------------------------

func (a *API) setupForm(w http.ResponseWriter, status int, token, username, errMsg string) {
	p := a.newPage("Create your login")
	p.Token, p.Error, p.Form = token, errMsg, map[string]string{"username": username}
	a.render(w, status, "setup.html", p)
}

// SetupPage shows the username and password form for a valid setup link.
func (a *API) SetupPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	if _, err := a.Store.PeekToken(r.Context(), token, PurposeSetup); err != nil {
		invalidLink(a, w)
		return
	}
	a.setupForm(w, http.StatusOK, token, "", "")
}

// SetupSubmit checks everything first and uses the link last, so a typo or a weak password never
// costs the person their one-time link.
func (a *API) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		invalidLink(a, w)
		return
	}
	token := r.PostFormValue("token")
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	password := r.PostFormValue("password")
	if !tokenShape.MatchString(token) {
		invalidLink(a, w)
		return
	}
	tok, err := a.Store.PeekToken(r.Context(), token, PurposeSetup)
	if err != nil {
		invalidLink(a, w)
		return
	}
	u, err := a.Store.ByID(r.Context(), tok.SubmitterID)
	if err != nil || u.Status != StatusApproved {
		a.message(w, http.StatusBadRequest, "Cannot create the login", "This account is not waiting for a login. Contact the medpost administrator.", nil)
		return
	}
	if err := ValidUsername(username); err != nil {
		a.setupForm(w, http.StatusBadRequest, token, username, err.Error())
		return
	}
	if password != r.PostFormValue("confirm") {
		a.setupForm(w, http.StatusBadRequest, token, username, "The two passwords are not the same.")
		return
	}
	if err := ValidatePassword(password, username, u.Email); err != nil {
		a.setupForm(w, http.StatusBadRequest, token, username, err.Error())
		return
	}
	if _, err := a.Store.ByUsername(r.Context(), username); err == nil {
		a.setupForm(w, http.StatusBadRequest, token, username, "That username is taken. Choose another.")
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		a.serverError(w, "hash password", err)
		return
	}
	if _, err := a.Store.ConsumeToken(r.Context(), token, PurposeSetup); err != nil {
		invalidLink(a, w)
		return
	}
	if err := a.Store.CompleteSetup(r.Context(), u.ID, username, hash); err != nil {
		if errors.Is(err, ErrUsernameTaken) {
			// Lost a race for the name after the link was used: ask the admin for a fresh link.
			a.message(w, http.StatusConflict, "Username taken", "Someone took that username a moment ago. Ask the medpost administrator for a new link.", nil)
			return
		}
		a.serverError(w, "complete setup", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "LoginCreated")
	a.message(w, http.StatusOK, "Your login is ready", "You can now sign in with your username and password.", nil)
}

// ---- sign in ----------------------------------------------------------------------------------

func (a *API) signInForm(w http.ResponseWriter, status int, username, errMsg string) {
	p := a.newPage("Sign in")
	p.Error, p.Form = errMsg, map[string]string{"username": username}
	a.render(w, status, "signin.html", p)
}

// SignInPage shows the form, or goes straight on when already signed in.
func (a *API) SignInPage(w http.ResponseWriter, r *http.Request) {
	if a.current(r) != nil {
		http.Redirect(w, r, basePath+"/home", http.StatusSeeOther)
		return
	}
	a.signInForm(w, http.StatusOK, "", "")
}

const signInFailed = "Wrong username or password, or the account is not available. Five wrong passwords lock an account for 15 minutes."

// SignInSubmit checks username and password. Every failure shows the same words and takes the same
// work (a bcrypt comparison even for a username that does not exist), so the form cannot be used to
// learn which usernames exist, or which are locked or not yet active.
func (a *API) SignInSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		a.signInForm(w, http.StatusBadRequest, "", "The form could not be read.")
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	ip := a.clientIP(r)
	if a.lim.Count("si:"+ip, signInWindow) >= a.limits.SignInPerIP || a.lim.Count("su:"+username, signInWindow) >= a.limits.SignInPerUser {
		a.signInForm(w, http.StatusTooManyRequests, username, "Too many attempts. Please wait a few minutes.")
		return
	}
	a.lim.Add("si:" + ip)
	a.lim.Add("su:" + username)
	if !a.passedCheck(r) {
		a.signInForm(w, http.StatusBadRequest, username, "The security check failed. Please try again.")
		return
	}

	hash, eligible := fakeHash(), false
	u, err := a.Store.ByUsername(r.Context(), username)
	if err == nil {
		locked, lockErr := a.Store.IsLocked(r.Context(), u.ID)
		if lockErr == nil && !locked && u.Status == StatusActive {
			if h, hashErr := a.Store.passwordHash(r.Context(), u.ID); hashErr == nil && h != "" {
				hash, eligible = h, true
			}
		}
	}
	matched := CheckPassword(hash, r.PostFormValue("password"))
	if !eligible || !matched {
		if eligible {
			if err := a.Store.RecordLoginFailure(r.Context(), u.ID); err != nil {
				log.Printf("mcusubmit: record login failure: %v", err)
			}
			a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "LoginFailed")
		}
		a.signInForm(w, http.StatusUnauthorized, username, signInFailed)
		return
	}
	if err := a.Store.RecordLoginSuccess(r.Context(), u.ID); err != nil {
		a.serverError(w, "record login", err)
		return
	}
	// RecordLoginSuccess does not change SessionVersion, so the version read above is current.
	if err := a.startSession(w, u); err != nil {
		a.serverError(w, "start session", err)
		return
	}
	a.audit(r, ActorSubmitter, u.ID, EntitySubmitter, u.ID, "LoggedIn")
	http.Redirect(w, r, basePath+"/home", http.StatusSeeOther)
}
