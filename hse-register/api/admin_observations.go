package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// formDateTimeLayout is the format of an <input type="datetime-local"> value.
const formDateTimeLayout = "2006-01-02T15:04"

type adminObservationHandlers struct {
	db          *sql.DB
	auth        *authContext
	evidenceDir string
	ref         *refData
	companies   *companyCache
}

func (h *adminObservationHandlers) viewer(r *http.Request) viewer {
	s := h.auth.session(r)
	return viewer{Username: s.Username, Role: s.Role}
}

// formPage renders the add/edit form. existing is nil for a new observation. errs is empty on the
// first view and holds the messages when the form is shown again after a failed check.
func (h *adminObservationHandlers) formPage(w http.ResponseWriter, r *http.Request, status int, existing *Observation, form obsForm, errs map[string]string) {
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		log.Printf("observation form: %v", err)
		http.Error(w, "The lookup lists could not be loaded. Did you run migrations 007-009?", http.StatusServiceUnavailable)
		return
	}
	data := PageData{
		Title: "Add observation", Heading: "Add observation", Active: "obs-list",
		Session: h.auth.session(r), Observation: &Observation{},
		Form: form, FormErrors: errs,
		Topics: snap.Topics, HazardGroups: snap.FormHazardGroups(existingHazard(existing)), Areas: snap.Areas,
		CompanyOptions:  h.companies.List(r.Context()),
		CategoryOptions: observationCategories, StatusOptions: observationStatuses,
		StaffSources: snap.SourceNames(existingSource(existing)), SourceDetailChoices: snap.SourceDetailChoices(existingSource(existing), existingDetail(existing)),
	}
	if existing != nil {
		files, err := listFiles(h.db, existing.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data.Title, data.Heading = "Edit observation", "Edit observation"
		data.Observation = existing
		// Only the "before" evidence is managed here; the After files live on the observation page.
		perms := permsFor(existing, h.viewer(r))
		data.Blocks = []fileBlock{{Section: sectionsOf(files)[0], ObsID: existing.ID, CanUpload: perms.CanEditEvidence, CanDelete: perms.CanEditEvidence}}
	}
	nav, err := buildNav(r.Context(), h.db, snap, "", "", dateRange{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.ObsNav = nav
	renderStatus(w, status, "observations_form", data)
}

// signedInName is the name shown as the submitter: the full name of the account, or the username
// when the account has none. It comes from the database, never from the form, so nobody can post
// under another name.
func (h *adminObservationHandlers) signedInName(r *http.Request) string {
	s := h.auth.session(r)
	var full string
	if err := h.db.QueryRowContext(r.Context(), `SELECT full_name FROM admin_users WHERE id = ?`, s.UserID).Scan(&full); err != nil && err != sql.ErrNoRows {
		log.Printf("observation form: could not read the full name of user %d: %v", s.UserID, err)
	}
	if full = strings.TrimSpace(full); full != "" {
		return full
	}
	return s.Username
}

func (h *adminObservationHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	form := obsForm{Status: statusOpen, ObservedAt: time.Now().Format(formDateTimeLayout), ObserverName: h.signedInName(r)}
	h.formPage(w, r, http.StatusOK, nil, form, nil)
}

func (h *adminObservationHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	o, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !permsFor(o, h.viewer(r)).CanEdit {
		http.Error(w, "You cannot edit this observation. Only the person who entered it can, and only until it is closed.", http.StatusForbidden)
		return
	}
	h.formPage(w, r, http.StatusOK, o, formFromObservation(o), nil)
}

func obsFormFromRequest(r *http.Request) obsForm {
	return obsForm{
		Source: r.FormValue("source"), SourceDetail: r.FormValue("source_detail"),
		ObserverName: r.FormValue("observer_name"), Designation: r.FormValue("designation"), Company: r.FormValue("company"),
		Category: r.FormValue("category"), Description: r.FormValue("description"),
		TopicID: r.FormValue("topic_id"), HazardID: r.FormValue("hazard_id"),
		Area: r.FormValue("area"), ResponsibleCompany: r.FormValue("responsible_company"),
		Verification: r.FormValue("verification"), Status: r.FormValue("status"), ObservedAt: r.FormValue("observed_at"),
		ActionTaken: r.FormValue("action_taken"),
	}
}

func (h *adminObservationHandlers) create(w http.ResponseWriter, r *http.Request) {
	// The form is multipart: it carries the evidence photos and PDFs together with the fields.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequest)
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "The form or its files are too large.", http.StatusRequestEntityTooLarge)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	form := obsFormFromRequest(r)
	form.Status, form.Verification = statusOpen, "" // a new post always starts Open
	form.ObserverName = h.signedInName(r)           // the signed-in user, whatever the form says
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		http.Error(w, "The lookup lists could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	o, errs := validateObservation(form, obsCheck{Ref: snap})
	if errs != nil {
		h.formPage(w, r, http.StatusUnprocessableEntity, nil, form, errs)
		return
	}
	o.CreatedBy = h.auth.session(r).Username
	up, err := prepareUploads(r.Context(), h.evidenceDir, r.MultipartForm, fileCounts{})
	if err != nil {
		h.formPage(w, r, http.StatusUnprocessableEntity, nil, form, map[string]string{"files": err.Error()})
		return
	}
	upAfter, err := prepareUploadsFrom(r.Context(), h.evidenceDir, r.MultipartForm, "after_photos", "after_documents", fileCounts{})
	if err != nil {
		up.discard(h.evidenceDir)
		h.formPage(w, r, http.StatusUnprocessableEntity, nil, form, map[string]string{"after_files": err.Error()})
		return
	}
	id, err := insertObservation(r.Context(), h.db, o)
	if err != nil {
		up.discard(h.evidenceDir)
		log.Printf("observation insert failed: %v", err)
		http.Error(w, "Could not save the observation.", http.StatusInternalServerError)
		return
	}
	err = up.insert(r.Context(), h.db, id, photoBefore)
	if err == nil {
		err = upAfter.insert(r.Context(), h.db, id, photoAfter)
	}
	if err != nil {
		// A post without its evidence would be misleading: take it back and let the user retry.
		up.discard(h.evidenceDir)
		upAfter.discard(h.evidenceDir)
		if _, derr := h.db.ExecContext(r.Context(), `DELETE FROM observations_observation WHERE id = ?`, id); derr != nil {
			log.Printf("could not remove observation %d after a failed file insert: %v", id, derr)
		}
		log.Printf("evidence insert failed: %v", err)
		http.Error(w, "Could not save the evidence files.", http.StatusInternalServerError)
		return
	}
	organizeQuietly(r.Context(), h.db, h.evidenceDir, id)
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(id, 10)+"?done=created", http.StatusSeeOther)
}

func (h *adminObservationHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	existing, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !permsFor(existing, h.viewer(r)).CanEdit {
		http.Error(w, "You cannot edit this observation. Only the person who entered it can, and only until it is closed.", http.StatusForbidden)
		return
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		http.Error(w, "The lookup lists could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	after, err := countAfterFiles(r.Context(), h.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	form := obsFormFromRequest(r)
	// Status and verification are never changed from this form: they have their own buttons.
	form.Status, form.Verification = existing.Status, existing.Verification
	if existing.Source != sourceSOC {
		form.ObserverName = existing.ObserverName // the name stays the one that was signed in at entry
	}
	o, errs := validateObservation(form, obsCheck{Ref: snap, Existing: existing, AfterFiles: after})
	if errs != nil {
		h.formPage(w, r, http.StatusUnprocessableEntity, existing, form, errs)
		return
	}
	if err := updateObservation(r.Context(), h.db, o); err != nil {
		log.Printf("observation update failed: %v", err)
		http.Error(w, "Could not save the observation.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (h *adminObservationHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	// Read the file names first: the database cascade removes the rows but not the files.
	files := fileNames(h.db, id)
	if _, err := h.db.Exec("DELETE FROM observations_observation WHERE id = ?", id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removePhotoFiles(h.evidenceDir, files)
	http.Redirect(w, r, "/observations", http.StatusSeeOther)
}
