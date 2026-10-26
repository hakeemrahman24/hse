package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// The employee who sent a SOC post can add After photos and close it from "My submissions". HSE
// verification is separate and waits for HSE staff: it can be given after the post is closed. The
// same rules as for staff apply to closing: a post needs at least one After photo, unless it is a
// Positive Finding. Ownership is checked in the database query, as for editing.

var errNeedsAfter = errors.New("closing needs at least one After photo")

// AddAfterOwn stores After photos on the employee's own post and, when closeIt is set, closes it.
// The row is locked while its state is checked, so a post closed a moment earlier is refused
// (errNotEditable) and a close without After evidence is refused (errNeedsAfter).
func (s *observationSink) AddAfterOwn(ctx context.Context, id int64, employeeID string, add []photoRecord, remarks *string, closeIt bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, category string
	err = tx.QueryRowContext(ctx, `SELECT status, category FROM observations_observation
		WHERE id = ? AND source = 'SOC' AND employee_id = ? FOR UPDATE`, id, employeeID).Scan(&status, &category)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !socOwnerCanAddAfter(status)) {
		return errNotEditable
	}
	if err != nil {
		return err
	}
	var have int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_files WHERE observation_id = ? AND kind = 'after' AND file_type = 'photo'`, id).Scan(&have); err != nil {
		return err
	}
	if err := checkPhotoCount(have, len(add)); err != nil {
		return err
	}
	if closeIt && closeBlocked(category, statusClosed, have+len(add)) {
		return errNeedsAfter
	}
	for _, p := range add {
		if _, err := tx.ExecContext(ctx, `INSERT INTO observation_files (observation_id, kind, file_type, stored_name, size_bytes) VALUES (?, 'after', 'photo', ?, ?)`,
			id, p.StoredName, p.Size); err != nil {
			return err
		}
	}
	if remarks != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE observations_observation SET action_taken = NULLIF(?,'') WHERE id = ?`, *remarks, id); err != nil {
			return err
		}
	}
	if closeIt {
		if _, err := tx.ExecContext(ctx, `UPDATE observations_observation SET status = 'closed', updated_at = NOW() WHERE id = ?`, id); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE observations_observation SET updated_at = NOW() WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.organize(ctx, id)
	return nil
}

// ---- pages ----

// ownOpen loads the employee's own post for the After page, or answers (sign-in redirect, 404 or
// the "closed" page) and returns nil.
func (h *socHandlers) ownOpen(w http.ResponseWriter, r *http.Request) (*socSession, *socPost) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return nil, nil
	}
	id, ok := h.editID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, nil
	}
	p, err := h.sink.GetOwn(r.Context(), id, sess.EmployeeID)
	if err != nil {
		log.Printf("SOC after lookup failed: %v", err)
		http.Error(w, tr(langOf(r), "Could not load the report."), http.StatusInternalServerError)
		return nil, nil
	}
	if p == nil {
		http.NotFound(w, r)
		return nil, nil
	}
	if !socOwnerCanAddAfter(p.Status) {
		renderSOC(w, r, http.StatusForbidden, "soc_locked", socPageData{Title: "Closed", Session: sess, Ref: socRef(p.ID), Closed: true})
		return nil, nil
	}
	return sess, p
}

func (h *socHandlers) showAfter(w http.ResponseWriter, r *http.Request, status int, sess *socSession, p *socPost, errs map[string]string) {
	renderSOC(w, r, status, "soc_after", socPageData{
		Title: "Close the finding", Session: sess, Ref: socRef(p.ID), EditID: p.ID,
		Photos: p.AfterIDs, PhotoSlots: max(0, maxPhotosPerPost-len(p.AfterIDs)), Errors: errs,
		NeedsAfter: closeBlocked(p.Category, statusClosed, len(p.AfterIDs)), Positive: isPositive(p.Category),
		Input: socInput{ActionTaken: p.ActionTaken},
	})
}

func (h *socHandlers) afterPage(w http.ResponseWriter, r *http.Request) {
	sess, p := h.ownOpen(w, r)
	if p == nil {
		return
	}
	h.showAfter(w, r, http.StatusOK, sess, p, nil)
}

func (h *socHandlers) afterSubmit(w http.ResponseWriter, r *http.Request) {
	sess, p := h.ownOpen(w, r)
	if p == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotosPerPost*photoMaxUploadBytes+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, tr(langOf(r), "The form or the photos are too large."), http.StatusRequestEntityTooLarge)
		return
	}
	var stored []storedPhoto
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
		var err error
		stored, err = storeUploads(r.Context(), h.evidenceDir, r.MultipartForm.File["photos"], len(p.AfterIDs))
		if err != nil {
			h.showAfter(w, r, http.StatusUnprocessableEntity, sess, p, map[string]string{"photos": photoMessage(langOf(r), err)})
			return
		}
	}
	add := make([]photoRecord, len(stored))
	for i, f := range stored {
		add[i] = photoRecord{Kind: "after", StoredName: f.Name, Size: f.Size}
	}
	closeIt := r.FormValue("close") == "1"
	// The remarks are replaced only when the form sent them; an empty value clears them.
	var remarks *string
	if r.Form.Has("action_taken") {
		v := strings.TrimSpace(r.FormValue("action_taken"))
		if tooLongText(v, maxObsText) {
			removePhotoFiles(h.evidenceDir, namesOf(stored))
			shown := *p
			shown.ActionTaken = v
			h.showAfter(w, r, http.StatusUnprocessableEntity, sess, &shown, map[string]string{"action_taken": fmt.Sprintf("Maximum %d characters.", maxObsText)})
			return
		}
		remarks = &v
	}
	if err := h.sink.AddAfterOwn(r.Context(), p.ID, sess.EmployeeID, add, remarks, closeIt); err != nil {
		removePhotoFiles(h.evidenceDir, namesOf(stored)) // the new files belong to nothing now
		switch {
		case errors.Is(err, errNotEditable):
			renderSOC(w, r, http.StatusForbidden, "soc_locked", socPageData{Title: "Closed", Session: sess, Ref: socRef(p.ID), Closed: true})
		case errors.Is(err, errNeedsAfter):
			h.showAfter(w, r, http.StatusUnprocessableEntity, sess, p, map[string]string{"photos": "Add at least one After photo before closing."})
		default:
			log.Printf("SOC after failed: %v", err)
			http.Error(w, tr(langOf(r), "Could not save. Please try again."), http.StatusInternalServerError)
		}
		return
	}
	done := "after"
	if closeIt {
		done = "closed"
	}
	http.Redirect(w, r, "/soc/mine?"+done+"="+socRef(p.ID), http.StatusSeeOther)
}
