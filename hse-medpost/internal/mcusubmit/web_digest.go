package mcusubmit

import (
	"errors"
	"net/http"
	"strconv"
)

// DigestPage shows the reviewer digest settings: how often the mail goes out and who gets it.
// Admin only; everybody else gets the same 404 as a path that does not exist.
func (a *API) DigestPage(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	a.digestPage(w, r, http.StatusOK, admin, "")
}

func (a *API) digestPage(w http.ResponseWriter, r *http.Request, status int, admin *Submitter, errMsg string) {
	cfg, err := a.Store.DigestSettings(r.Context())
	if err != nil {
		a.serverError(w, "digest settings", err)
		return
	}
	list, err := a.Store.Reviewers(r.Context())
	if err != nil {
		a.serverError(w, "list reviewers", err)
		return
	}
	p := a.newPage("Reviewer mail")
	p.User, p.Digest, p.Reviewers, p.Intervals, p.Error = admin, cfg, list, DigestIntervals, errMsg
	a.render(w, status, "digest.html", p)
}

// DigestSave stores the chosen interval (24, 36 or 48 hours).
func (a *API) DigestSave(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.digestPage(w, r, http.StatusBadRequest, admin, "The form could not be read.")
		return
	}
	hours, _ := strconv.Atoi(r.PostFormValue("hours")) // a non-number becomes 0, which the store refuses
	if err := a.Store.SaveDigestInterval(r.Context(), hours); err != nil {
		if errors.Is(err, ErrBadInterval) {
			a.digestPage(w, r, http.StatusBadRequest, admin, "Choose 24, 36 or 48 hours.")
			return
		}
		a.serverError(w, "save digest interval", err)
		return
	}
	http.Redirect(w, r, basePath+"/admin/digest", http.StatusSeeOther)
}

// ReviewerAdd puts an address on the list.
func (a *API) ReviewerAdd(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.digestPage(w, r, http.StatusBadRequest, admin, "The form could not be read.")
		return
	}
	// Checked here so a bad address is a 400 with a plain message, and anything AddReviewer still
	// returns is a database failure.
	email, err := ParseEmail(r.PostFormValue("email"))
	if err != nil {
		a.digestPage(w, r, http.StatusBadRequest, admin, err.Error())
		return
	}
	name := SanitizePosition(r.PostFormValue("name"))
	if _, err := a.Store.AddReviewer(r.Context(), email, name); err != nil {
		if errors.Is(err, ErrEmailTaken) {
			a.digestPage(w, r, http.StatusConflict, admin, "That address is already on the list.")
			return
		}
		a.serverError(w, "add reviewer", err)
		return
	}
	http.Redirect(w, r, basePath+"/admin/digest", http.StatusSeeOther)
}

// ReviewerToggle pauses (on=0) or resumes (on=1) one address.
func (a *API) ReviewerToggle(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if err := a.Store.SetReviewerActive(r.Context(), id, r.PostFormValue("on") == "1"); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	http.Redirect(w, r, basePath+"/admin/digest", http.StatusSeeOther)
}

// ReviewerDelete removes one address from the list.
func (a *API) ReviewerDelete(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if err := a.Store.DeleteReviewer(r.Context(), id); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	http.Redirect(w, r, basePath+"/admin/digest", http.StatusSeeOther)
}
