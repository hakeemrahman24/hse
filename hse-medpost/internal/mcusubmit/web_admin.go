package mcusubmit

import (
	"errors"
	"log"
	"net/http"
)

func (a *API) toRows(rows []Submitter, companies map[int64]string) []adminRow {
	out := make([]adminRow, 0, len(rows))
	for _, u := range rows {
		out = append(out, adminRow{
			ID: u.ID, Name: u.Name, Email: u.Email, Company: companies[u.CompanyID], Position: u.Position,
			Status: u.Status, CreatedAt: u.CreatedAt, RejectReason: u.RejectReason, BouncedAt: u.EmailBouncedAt,
		})
	}
	return out
}

// AdminPage lists the requests waiting for a decision (email confirmed, status Requested) and every
// other account. The built-in admin is never listed: List leaves it out.
func (a *API) AdminPage(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	all, err := a.Store.List(r.Context(), "")
	if err != nil {
		a.serverError(w, "list submitters", err)
		return
	}
	showArchived := r.URL.Query().Get("archived") == "1"
	var waiting, others []Submitter
	archived := 0
	for _, u := range all {
		switch {
		case u.Status == StatusArchived:
			archived++
			if showArchived {
				others = append(others, u)
			}
		case u.Status == StatusRequested && u.EmailVerifiedAt != "":
			waiting = append(waiting, u)
		case u.Status == StatusRequested:
			// Email not confirmed yet: not in the queue (plan section 4), and purged after 48 hours.
		default:
			others = append(others, u)
		}
	}
	names := a.allCompanyNames(r.Context())
	p := a.newPage("Admin")
	p.User, p.Pending, p.Accounts = admin, a.toRows(waiting, names), a.toRows(others, names)
	p.ShowArchived, p.ArchivedCount = showArchived, archived
	if counts, err := a.Store.DocumentCounts(r.Context()); err != nil {
		log.Printf("mcusubmit: document counts: %v", err) // the list still works without them
	} else {
		for i := range p.Accounts {
			p.Accounts[i].Docs = counts[p.Accounts[i].ID]
		}
	}
	if signed, err := a.Store.RomifSignedAt(r.Context()); err != nil {
		log.Printf("mcusubmit: romif signatures: %v", err) // the list still works without them
	} else {
		for _, rows := range [][]adminRow{p.Pending, p.Accounts} {
			for i := range rows {
				rows[i].RomifAt = signed[rows[i].ID]
			}
		}
	}
	a.render(w, http.StatusOK, "admin.html", p)
}

// adminTarget reads the submitter id from an admin form.
func (a *API) adminTarget(w http.ResponseWriter, r *http.Request, admin *Submitter) (int64, bool) {
	if !parseForm(w, r) {
		a.message(w, http.StatusBadRequest, "Invalid request", "The form could not be read.", admin)
		return 0, false
	}
	id, ok := parseID(r.PostFormValue("id"))
	if !ok {
		a.notFound(w, admin)
		return 0, false
	}
	return id, true
}

// adminFailed turns a store error into a page: ErrNotFound covers a missing id and the hidden
// built-in admin alike, ErrBadState is a decision that was already made.
func (a *API) adminFailed(w http.ResponseWriter, admin *Submitter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		a.notFound(w, admin)
	case errors.Is(err, ErrBadState):
		a.message(w, http.StatusConflict, "Already decided", "This account is not in a state where that is possible. Reload the Admin page.", admin)
	case errors.Is(err, ErrCompanyFull):
		a.message(w, http.StatusConflict, "Company is full", "This company already has two accounts. Archive or delete one of them first.", admin)
	default:
		a.serverError(w, "admin action", err)
	}
}

// AdminApprove approves a confirmed request and mails the one-time link for choosing a login.
func (a *API) AdminApprove(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if err := a.Store.Approve(r.Context(), id, admin.ID); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, "Approved")
	u, err := a.Store.ByID(r.Context(), id)
	if err != nil {
		a.serverError(w, "load approved submitter", err)
		return
	}
	raw, err := a.Store.IssueToken(r.Context(), id, PurposeSetup, "", setupTTL)
	if err != nil {
		// Approved but no link: the admin can Disable and ask them to register again. Log loudly.
		log.Printf("mcusubmit: approved submitter %d but could not issue the setup link: %v", id, err)
	} else {
		a.sendMail(r.Context(), u.Email, "Your account request was approved", approvedMail(a.baseURL, raw))
	}
	http.Redirect(w, r, basePath+"/admin", http.StatusSeeOther)
}

// AdminReject refuses a request. The reason is required and is sent to the submitter.
func (a *API) AdminReject(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	reason := SanitizePosition(r.PostFormValue("reason"))
	if reason == "" {
		a.message(w, http.StatusBadRequest, "Reason needed", "Enter a reason; the submitter is told why.", admin)
		return
	}
	if err := a.Store.Reject(r.Context(), id, reason); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, "Rejected")
	if u, err := a.Store.ByID(r.Context(), id); err == nil {
		a.sendMail(r.Context(), u.Email, "Your account request was not approved", rejectedMail(reason))
	}
	http.Redirect(w, r, basePath+"/admin", http.StatusSeeOther)
}

// AdminDisable blocks an account and ends its open sessions. The built-in admin answers 404.
func (a *API) AdminDisable(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if err := a.Store.Disable(r.Context(), id); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, "Disabled")
	http.Redirect(w, r, basePath+"/admin", http.StatusSeeOther)
}
