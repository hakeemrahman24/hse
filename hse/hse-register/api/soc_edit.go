package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

// An employee can edit what they sent from "My submissions" until HSE verifies the post or it is
// closed (socOwnerCanEdit). The ownership check is in the database query, not only in the page:
// the post id in the URL is never enough on its own.

var errNotEditable = errors.New("the post can no longer be edited")

// socPost is an employee's own post as the edit page needs it.
type socPost struct {
	ID                       int64
	Date                     time.Time
	Category, Description    string
	TopicID, HazardID        int64
	Area, ResponsibleCompany string
	Status, Verification     string
	ActionTaken              string  // remarks / recommendation, filled in the After section
	PhotoIDs                 []int64 // Evidence (before) photos
	AfterIDs                 []int64 // After photos
}

func (p *socPost) editable() bool { return socOwnerCanEdit(p.Status, p.Verification) }

func (s *observationSink) GetOwn(ctx context.Context, id int64, employeeID string) (*socPost, error) {
	p := &socPost{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT observed_at, category, description, COALESCE(topic_id,0), COALESCE(hazard_id,0),
		location, COALESCE(responsible_company,''), status, COALESCE(verification,'Not yet'), COALESCE(action_taken,'')
		FROM observations_observation WHERE id = ? AND source = 'SOC' AND employee_id = ?`, id, employeeID).
		Scan(&p.Date, &p.Category, &p.Description, &p.TopicID, &p.HazardID, &p.Area, &p.ResponsibleCompany, &p.Status, &p.Verification, &p.ActionTaken)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind FROM observation_files WHERE observation_id = ? AND file_type = 'photo' ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var kind string
		if err := rows.Scan(&pid, &kind); err != nil {
			return nil, err
		}
		if kind == "after" {
			p.AfterIDs = append(p.AfterIDs, pid)
		} else {
			p.PhotoIDs = append(p.PhotoIDs, pid)
		}
	}
	return p, rows.Err()
}

// UpdateOwn saves the employee's changes in one transaction and returns the stored names of the
// photos that were removed (the caller deletes those files after the commit). The row is locked
// while its state is checked, so a post verified or closed a moment earlier is refused.
func (s *observationSink) UpdateOwn(ctx context.Context, id int64, employeeID string, sub socSubmission, remove []int64, add []photoRecord) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var status, verification string
	err = tx.QueryRowContext(ctx, `SELECT status, COALESCE(verification,'Not yet') FROM observations_observation
		WHERE id = ? AND source = 'SOC' AND employee_id = ? FOR UPDATE`, id, employeeID).Scan(&status, &verification)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !socOwnerCanEdit(status, verification)) {
		return nil, errNotEditable
	}
	if err != nil {
		return nil, err
	}
	// WHY: topic_pick / suggestions_shown record how the reporter chose at first sending. They are
	// replaced only when the topic itself changes, so rewording the text does not skew the accuracy
	// measure and a pre-v8 post stays NULL. They must come BEFORE topic_id in SET: UPDATE assigns
	// left to right, so topic_id still holds the old value when they are compared.
	if _, err := tx.ExecContext(ctx, `UPDATE observations_observation SET observed_at = ?, category = ?, description = ?,
		topic_pick = IF(topic_id <=> NULLIF(?,0), topic_pick, NULLIF(?,'')),
		suggestions_shown = IF(topic_id <=> NULLIF(?,0), suggestions_shown, NULLIF(?,'')),
		topic_id = NULLIF(?,0), hazard_id = NULLIF(?,0),
		location = ?, responsible_company = NULLIF(?,''), updated_at = NOW()
		WHERE id = ?`, sub.FindingDate, sub.Category, sub.Description,
		sub.TopicID, sub.TopicPick, sub.TopicID, sub.SuggestionsShown,
		sub.TopicID, sub.HazardID, sub.Area, sub.ResponsibleCompany, id); err != nil {
		return nil, err
	}
	var removed []string
	for _, pid := range remove {
		var name string
		// Matching observation, kind and type means an id from another post cannot be removed here.
		err := tx.QueryRowContext(ctx, `SELECT stored_name FROM observation_files WHERE id = ? AND observation_id = ? AND kind = 'before' AND file_type = 'photo'`, pid, id).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM observation_files WHERE id = ?`, pid); err != nil {
			return nil, err
		}
		removed = append(removed, name)
	}
	var have int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_files WHERE observation_id = ? AND kind = 'before' AND file_type = 'photo'`, id).Scan(&have); err != nil {
		return nil, err
	}
	if err := checkPhotoCount(have, len(add)); err != nil {
		return nil, err
	}
	for _, p := range add {
		if _, err := tx.ExecContext(ctx, `INSERT INTO observation_files (observation_id, kind, file_type, stored_name, size_bytes) VALUES (?, 'before', 'photo', ?, ?)`,
			id, p.StoredName, p.Size); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.organize(ctx, id)
	return removed, nil
}

// ---- pages ----

func (h *socHandlers) editID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// editInput turns a saved post into the form values.
func editInput(p *socPost) socInput {
	in := socInput{Date: p.Date.Format("2006-01-02"), Category: p.Category, Description: p.Description,
		Area: p.Area, ResponsibleCompany: p.ResponsibleCompany, KeepHazard: p.HazardID}
	if p.TopicID != 0 {
		in.TopicID = strconv.FormatInt(p.TopicID, 10)
	}
	if p.HazardID != 0 {
		in.HazardID = strconv.FormatInt(p.HazardID, 10)
	}
	return in
}

func (h *socHandlers) showEdit(w http.ResponseWriter, r *http.Request, status int, sess *socSession, p *socPost, in socInput, errs map[string]string) {
	data, err := h.formData(r, sess, in, errs)
	if err != nil {
		log.Printf("SOC edit lists: %v", err)
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	data.Title = "Edit"
	data.EditID, data.Ref = p.ID, socRef(p.ID)
	data.Photos = p.PhotoIDs
	data.PhotoSlots = max(0, maxPhotosPerPost-len(p.PhotoIDs))
	renderSOC(w, r, status, "soc_form", data)
}

// ownEditable loads the post for editing, or writes the answer (sign-in redirect, 404 or "locked")
// and returns nil.
func (h *socHandlers) ownEditable(w http.ResponseWriter, r *http.Request) (*socSession, *socPost) {
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
		log.Printf("SOC edit lookup failed: %v", err)
		http.Error(w, tr(langOf(r), "Could not load the report."), http.StatusInternalServerError)
		return nil, nil
	}
	if p == nil {
		http.NotFound(w, r) // someone else's post looks the same as a post that does not exist
		return nil, nil
	}
	if !p.editable() {
		renderSOC(w, r, http.StatusForbidden, "soc_locked", socPageData{Title: "Cannot edit", Session: sess, Ref: socRef(p.ID)})
		return nil, nil
	}
	return sess, p
}

func (h *socHandlers) editPage(w http.ResponseWriter, r *http.Request) {
	sess, p := h.ownEditable(w, r)
	if p == nil {
		return
	}
	h.showEdit(w, r, http.StatusOK, sess, p, editInput(p), nil)
}

func (h *socHandlers) editSubmit(w http.ResponseWriter, r *http.Request) {
	sess, p := h.ownEditable(w, r)
	if p == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotosPerPost*photoMaxUploadBytes+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, tr(langOf(r), "The form or the photos are too large."), http.StatusRequestEntityTooLarge)
		return
	}
	var uploads []*multipart.FileHeader
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
		uploads = r.MultipartForm.File["photos"]
	}
	in := socInput{
		Date: r.FormValue("date"), Category: r.FormValue("category"), Description: r.FormValue("description"),
		TopicID: r.FormValue("topic_id"), HazardID: r.FormValue("hazard_id"),
		Area: r.FormValue("area"), ResponsibleCompany: r.FormValue("responsible_company"),
		KeepHazard: p.HazardID,
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	sub, errs := validateSOC(in, time.Now().UTC().Truncate(24*time.Hour), snap)
	if len(errs) > 0 {
		h.showEdit(w, r, http.StatusUnprocessableEntity, sess, p, in, errs)
		return
	}

	// Only ids that belong to this post count as removed; anything else in the form is ignored.
	own := map[int64]bool{}
	for _, id := range p.PhotoIDs {
		own[id] = true
	}
	var remove []int64
	for _, v := range r.Form["remove_photo"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && own[id] {
			remove = append(remove, id)
			delete(own, id) // a repeated id counts once
		}
	}
	stored, err := storeUploads(r.Context(), h.evidenceDir, uploads, len(p.PhotoIDs)-len(remove))
	if err != nil {
		h.showEdit(w, r, http.StatusUnprocessableEntity, sess, p, in, map[string]string{"photos": photoMessage(langOf(r), err)})
		return
	}
	add := make([]photoRecord, len(stored))
	newNames := make([]string, len(stored))
	for i, f := range stored {
		add[i] = photoRecord{Kind: "before", StoredName: f.Name, Size: f.Size}
		newNames[i] = f.Name
	}
	removedNames, err := h.sink.UpdateOwn(r.Context(), p.ID, sess.EmployeeID, sub, remove, add)
	if err != nil {
		removePhotoFiles(h.evidenceDir, newNames) // the new files belong to nothing now
		if errors.Is(err, errNotEditable) {
			renderSOC(w, r, http.StatusForbidden, "soc_locked", socPageData{Title: "Cannot edit", Session: sess, Ref: socRef(p.ID)})
			return
		}
		log.Printf("SOC update failed: %v", err)
		http.Error(w, tr(langOf(r), "Could not save your changes. Please try again."), http.StatusInternalServerError)
		return
	}
	removePhotoFiles(h.evidenceDir, removedNames) // only after the database change is saved
	http.Redirect(w, r, "/soc/mine?saved="+socRef(p.ID), http.StatusSeeOther)
}
