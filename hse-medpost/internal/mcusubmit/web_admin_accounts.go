package mcusubmit

import (
	"net/http"
	"strconv"
)

// The portal admin's account actions beyond Approve, Reject and Disable (owner, 2026-11-02): Enable,
// Archive, Restore, and a permanent delete that is asked about on its own page first.

// adminAction runs one of the one-step account actions and writes its audit line.
func (a *API) adminAction(w http.ResponseWriter, r *http.Request, do func(id int64) error, audit string) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if err := do(id); err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	a.audit(r, ActorSubmitter, admin.ID, EntitySubmitter, id, audit)
	http.Redirect(w, r, basePath+"/admin", http.StatusSeeOther)
}

// AdminEnable lets a Disabled account sign in again.
func (a *API) AdminEnable(w http.ResponseWriter, r *http.Request) {
	a.adminAction(w, r, func(id int64) error { return a.Store.Enable(r.Context(), id) }, "Enabled")
}

// AdminArchive hides an account from the list and blocks it, keeping its documents.
func (a *API) AdminArchive(w http.ResponseWriter, r *http.Request) {
	a.adminAction(w, r, func(id int64) error { return a.Store.Archive(r.Context(), id) }, "Archived")
}

// AdminRestore brings an archived account back, Disabled.
func (a *API) AdminRestore(w http.ResponseWriter, r *http.Request) {
	a.adminAction(w, r, func(id int64) error { return a.Store.Restore(r.Context(), id) }, "Restored")
}

// AdminDeletePage is the warning the admin reads before a permanent delete: what goes with the
// account (its documents, batches and stored PDFs) and what the cheaper alternatives are. A page of
// its own, not a script confirm box, so it also works without JavaScript and the counts are shown.
func (a *API) AdminDeletePage(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := parseID(r.URL.Query().Get("id"))
	if !ok {
		a.notFound(w, admin)
		return
	}
	u, err := a.Store.ByID(r.Context(), id)
	if err != nil || u.IsBuiltIn || u.ID == admin.ID {
		a.notFound(w, admin)
		return
	}
	foot, err := a.Store.Footprint(r.Context(), id)
	if err != nil {
		a.serverError(w, "account footprint", err)
		return
	}
	row := adminRow{ID: u.ID, Name: u.Name, Email: u.Email, Status: u.Status}
	p := a.newPage("Delete account")
	p.User, p.Target, p.Foot = admin, &row, foot
	a.render(w, http.StatusOK, "admindelete.html", p)
}

// AdminDeleteSubmit deletes the account after the warning page. The box must be ticked; without
// JavaScript that is the only guard, and the page cannot be skipped by posting the id alone.
func (a *API) AdminDeleteSubmit(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id, ok := a.adminTarget(w, r, admin)
	if !ok {
		return
	}
	if r.PostFormValue("understood") != "1" {
		http.Redirect(w, r, basePath+"/admin/delete?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	refs, _, err := a.Store.RemoveAccount(r.Context(), id, admin.ID)
	if err != nil {
		a.adminFailed(w, admin, err)
		return
	}
	if a.pipeline != nil {
		for _, ref := range refs {
			a.dropFile(ref)
		}
	}
	http.Redirect(w, r, basePath+"/admin", http.StatusSeeOther)
}
