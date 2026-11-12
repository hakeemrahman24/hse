package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"entry-permit/internal/permit"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/staff"
	"entry-permit/internal/waiver"
)

// stageRoles says which role may decide at each stage. WHY one table: the page a
// reviewer sees and the check on the POST must always agree.
var stageRoles = map[string]staff.Role{
	"doctor": staff.Doctor,
	"stage1": staff.Stage1,
	"final":  staff.Final,
}

// reviewRoles are the staff who may open uploaded documents.
var reviewRoles = []staff.Role{staff.Doctor, staff.Stage1, staff.Final, staff.Admin}

// ReviewData feeds a reviewer's queue page.
type ReviewData struct {
	Stage   string
	Permits []reviewlog.PermitItem
	Waivers []reviewlog.WaiverItem
	Flash   string
	Err     string
	// Pending says which stages have requests waiting, so a reviewer sees at a glance that for
	// example Stage 1 has cleared its queue and the Final reviewer now has work. Counts only.
	Pending []PendingCount
}

// PendingCount is the number of requests waiting at one stage. Own marks the page's own stage.
type PendingCount struct {
	Label string
	Count int
	Own   bool
}

// stageOrder is the order the notice lists the stages in.
var stageOrder = []struct{ slug, label string }{{"stage1", "Stage 1"}, {"doctor", "Doctor"}, {"final", "Final"}}

// pendingCounts counts what waits at every stage and lists only the stages with something waiting.
// WHY counts and not the requests: a reviewer may not open another stage's queue, so the notice
// must not carry names, ids or documents. The page's own queue is reused, not read twice.
func (a *App) pendingCounts(r *http.Request, own string, ownData ReviewData) ([]PendingCount, error) {
	var out []PendingCount
	for _, s := range stageOrder {
		d := ownData
		if s.slug != own {
			var err error
			if d, err = a.reviewQueues(r, s.slug); err != nil {
				return nil, err
			}
		}
		if n := len(d.Permits) + len(d.Waivers); n > 0 {
			out = append(out, PendingCount{Label: s.label, Count: n, Own: s.slug == own})
		}
	}
	return out, nil
}

// canOpenDocs says whether this person may open uploaded documents: reviewers and admins only.
func canOpenDocs(u *staff.User) bool {
	for _, role := range reviewRoles {
		if u.HasRole(role) {
			return true
		}
	}
	return false
}

// isReviewPage reports whether a role's page is a review queue.
func isReviewPage(role staff.Role) bool {
	_, ok := stageRoles[role.Slug()]
	return ok
}

// reviewQueues loads what is waiting for one stage: work permits and waivers in
// the status that stage acts on. The doctor only sees waivers (a work permit has
// no doctor stage).
func (a *App) reviewQueues(r *http.Request, stage string) (ReviewData, error) {
	d := ReviewData{Stage: stage}
	var err error
	switch stage {
	case "doctor":
		d.Waivers, err = a.Reviews.WaiverQueue(r.Context(), waiver.StatusPendingDoctor)
	case "stage1":
		if d.Permits, err = a.Reviews.PermitQueue(r.Context(), permit.StatusPendingStage1); err == nil {
			d.Waivers, err = a.Reviews.WaiverQueue(r.Context(), waiver.StatusPendingStage1)
		}
	case "final":
		if d.Permits, err = a.Reviews.PermitQueue(r.Context(), permit.StatusPendingFinal); err == nil {
			d.Waivers, err = a.Reviews.WaiverQueue(r.Context(), waiver.StatusPendingSiteManager)
		}
	}
	return d, err
}

// reviewPage shows one reviewer queue, with an optional message.
func (a *App) reviewPage(w http.ResponseWriter, r *http.Request, u *staff.User, role staff.Role, status int, msg string) {
	if a.Reviews == nil {
		a.render(w, "staff", http.StatusOK, View{Title: role.Label(), User: u, Links: menu(*u, role.Slug()), Message: "Reviews are not available right now."})
		return
	}
	d, err := a.reviewQueues(r, role.Slug())
	if err != nil {
		log.Printf("review queue %s: %v", role.Slug(), err)
		a.render(w, "staff", http.StatusServiceUnavailable, View{Title: role.Label(), User: u, Links: menu(*u, role.Slug()), Message: "The queue could not be read. Please try again."})
		return
	}
	// The notice is a courtesy: if counting another stage fails, the page still shows its own queue.
	if d.Pending, err = a.pendingCounts(r, role.Slug(), d); err != nil {
		log.Printf("review pending counts: %v", err)
	}
	d.Flash, d.Err = flash(r, "ok"), flash(r, "err")
	if msg != "" {
		d.Err = msg
	}
	a.render(w, "review", status, View{Title: role.Label(), User: u, Links: menu(*u, role.Slug()), CSRF: a.csrfToken(r), Review: d})
}

// reviewSubmit records one decision. Every check runs on the server: sign-in,
// CSRF, that this person holds the role for that stage, and the request's own status.
func (a *App) reviewSubmit(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil || !a.csrfOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	stage := r.PostFormValue("stage")
	role, ok := stageRoles[stage]
	if !ok {
		http.Error(w, "Unknown review stage", http.StatusBadRequest)
		return
	}
	if !u.HasRole(role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var approve bool
	switch r.PostFormValue("action") {
	case "approve":
		approve = true
	case "reject":
	default:
		a.reviewPage(w, r, u, role, http.StatusBadRequest, "Choose Approve or Reject.")
		return
	}
	if a.Reviews == nil {
		http.Error(w, "Reviews are not available", http.StatusServiceUnavailable)
		return
	}
	d := reviewlog.Decision{Stage: stage, ID: strings.ToUpper(strings.TrimSpace(r.PostFormValue("subject"))), Approve: approve,
		Note: clip(strings.TrimSpace(r.PostFormValue("note")), 500), Reviewer: u.DisplayName, At: string(a.Clock.Now())}
	var out reviewlog.Outcome
	var err error
	switch {
	case strings.HasPrefix(d.ID, "WEP-") && stage != "doctor": // a work permit has no doctor stage
		out, err = a.Reviews.DecidePermit(r.Context(), a.policy(), d)
	case strings.HasPrefix(d.ID, "WVR-"):
		out, err = a.Reviews.DecideWaiver(r.Context(), a.policy(), d)
	default:
		err = reviewlog.ErrNotFound
	}
	if err != nil {
		status, msg := reviewError(err)
		a.reviewPage(w, r, u, role, status, msg)
		return
	}
	a.notifyDecision(r.Context(), d, out)
	if out.LEP != "" && a.GateKick != nil {
		a.GateKick() // a newly approved worker should reach the gate now, not at the next tick
	}
	done := fmt.Sprintf("%s: %s.", d.ID, out.Status)
	if out.LEP != "" {
		done = fmt.Sprintf("%s approved. LEP %s issued.", d.ID, out.LEP)
		if out.Renewed > 0 {
			done += fmt.Sprintf(" %d earlier permit(s) marked renewed.", out.Renewed)
		}
	}
	http.Redirect(w, r, "/staff/"+role.Slug()+"?ok="+url.QueryEscape(done), http.StatusSeeOther)
}

// reviewError turns a store error into a status and a message for the reviewer.
func reviewError(err error) (int, string) {
	switch {
	case errors.Is(err, reviewlog.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, permit.ErrAlreadyActioned):
		return http.StatusConflict, err.Error()
	case errors.Is(err, permit.ErrNoteTooShort), errors.Is(err, permit.ErrNoValidity), errors.Is(err, permit.ErrUnknownStage):
		return http.StatusBadRequest, err.Error()
	}
	log.Printf("review: %v", err)
	return http.StatusInternalServerError, "The decision could not be saved. Please try again."
}

// reviewFile serves one uploaded document to a reviewer. The files hold ID cards
// and medical papers, so nothing here is cached and the path is checked hard.
func (a *App) reviewFile(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canOpenDocs(u) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	path, ctype, err := a.Uploads.Path(r.URL.Query().Get("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", "inline")
	http.ServeContent(w, r, "", time.Time{}, f)
}
