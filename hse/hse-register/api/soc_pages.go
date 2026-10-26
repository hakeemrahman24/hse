package main

import (
	"errors"
	"html/template"
	"log"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var socPages = map[string]*template.Template{}

func loadSOCPages(dir string) {
	for _, name := range []string{"soc_signin", "soc_form", "soc_wizard", "soc_thanks", "soc_mine", "soc_locked", "soc_after"} {
		t, err := template.New("").Funcs(template.FuncMap{
			"categoryLabel": categoryLabel, "stage": socStage, "tr": tr, "heading": headingHTML,
		}).ParseFiles(dir+"/soc_base.html", dir+"/"+name+".html")
		if err != nil {
			log.Fatalf("failed to parse template %s: %v", name, err)
		}
		socPages[name] = t
	}
}

func categoryLabel(v string) string {
	for _, c := range socCategories {
		if c.Value == v {
			return c.Label
		}
	}
	return v
}

// socStage is the word the employee sees on "My submissions" (plan section 8.2).
func socStage(it socListItem) string {
	switch {
	case it.Status == statusClosed && it.Verification == verifVerified:
		return "Closed and verified"
	case it.Status == statusClosed:
		return "Closed, waiting for HSE verification"
	case it.Status == statusInProgress:
		return "In progress"
	case it.Verification == verifVerified:
		return "Verified"
	default:
		return "Received"
	}
}

type socPageData struct {
	Title        string // English; the page translates it
	Lang         string // langID or langEN
	HTMLLang     string
	Path         string // this page, where the language switcher returns to
	Langs        []socLang
	Sitekey      string
	DevStubs     bool
	Session      *socSession
	Input        socInput
	Errors       map[string]string
	Categories   []struct{ Value, Label string }
	TopicGroups  []topicGroup
	Wiz          *wizardPage // set on the wizard page
	HazardClass  string      // class of the hazard category the topic maps to, shown read-only
	Areas        []string
	Companies    []string
	Today        string
	Ref          string
	EditID       int64 // set on the edit page: the post being edited
	PhotoSlots   int   // how many more photos can be added
	Notice       string
	Photos       []int64
	Items        []socListItem
	ServerFailed bool
	Closed       bool // the locked page: the post is closed
	NeedsAfter   bool // closing still needs an After photo
	Positive     bool // a Positive Finding can be closed without After photos
}

func renderSOC(w http.ResponseWriter, r *http.Request, status int, page string, data socPageData) {
	// Every page knows its language, where to come back to after the switcher, and its errors are
	// shown in that language.
	data.Lang = langOf(r)
	data.HTMLLang = htmlLang(data.Lang)
	data.Path = r.URL.Path
	data.Langs = socLangs
	data.Errors = translateErrors(data.Lang, data.Errors)
	t, ok := socPages[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError) // a programming error, not for visitors
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "socbase", data); err != nil {
		log.Printf("template error (%s): %v", page, err)
	}
}

func (h *socHandlers) signinPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.session(r); ok {
		http.Redirect(w, r, "/soc/new", http.StatusSeeOther)
		return
	}
	renderSOC(w, r, http.StatusOK, "soc_signin", socPageData{Title: "Sign in", Sitekey: h.sitekey, DevStubs: h.devStubs})
}

// formData fills everything the form page needs. The lookup lists come from the database (cached);
// if they cannot be loaded the form cannot be filled in, so the caller shows an error instead.
func (h *socHandlers) formData(r *http.Request, sess *socSession, in socInput, errs map[string]string) (socPageData, error) {
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		return socPageData{}, err
	}
	return socPageData{
		Title: "Observation", Session: sess, Input: in, Errors: errs,
		Categories: socCategories, TopicGroups: snap.TopicGroups(), HazardClass: socHazardClass(snap, in),
		Areas: snap.Areas, Companies: h.companies.List(r.Context()),
		Today: time.Now().Format("2006-01-02"),
	}, nil
}

// showForm renders the form page with the given status, or a plain error if the lists are missing.
func (h *socHandlers) showForm(w http.ResponseWriter, r *http.Request, status int, sess *socSession, in socInput, errs map[string]string, serverFailed bool) {
	data, err := h.formData(r, sess, in, errs)
	if err != nil {
		log.Printf("SOC form lists: %v", err)
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	data.ServerFailed = serverFailed
	data.PhotoSlots = maxPhotosPerPost
	renderSOC(w, r, status, "soc_form", data)
}

func (h *socHandlers) formPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	h.showForm(w, r, http.StatusOK, sess, socInput{Date: time.Now().Format("2006-01-02")}, nil, false)
}

func (h *socHandlers) formSubmit(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	// Room for the text fields plus up to 4 photos at the upload limit each. Files bigger than
	// 1 MB are spooled to disk by the parser instead of being held in memory.
	r.Body = http.MaxBytesReader(w, r.Body, 2*maxPhotosPerPost*photoMaxUploadBytes+(256<<10))
	// ErrNotMultipart is fine: a form without photos may arrive as a plain URL-encoded post, and
	// ParseMultipartForm has already parsed its fields by the time it returns that error.
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, tr(langOf(r), "The form or the photos are too large."), http.StatusRequestEntityTooLarge)
		return
	}
	var uploads, afterUploads []*multipart.FileHeader
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll() // delete the parser's temporary files
		uploads = r.MultipartForm.File["photos"]
		afterUploads = r.MultipartForm.File["after_photos"]
	}
	in := socInput{
		Date: r.FormValue("date"), Category: r.FormValue("category"), Description: r.FormValue("description"),
		TopicID: r.FormValue("topic_id"), HazardID: r.FormValue("hazard_id"),
		Area: r.FormValue("area"), ResponsibleCompany: r.FormValue("responsible_company"),
		ActionTaken: r.FormValue("action_taken"),
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		log.Printf("SOC form lists: %v", err)
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	sub, errs := validateSOC(in, time.Now().UTC().Truncate(24*time.Hour), snap)
	if len(errs) > 0 {
		h.showForm(w, r, http.StatusUnprocessableEntity, sess, in, errs, false)
		return
	}
	// WHY: reporter fields come from the verified session, never from the form, so a visitor
	// cannot file a report under someone else's name.
	sub.EmployeeID, sub.ReporterName, sub.Designation, sub.Company = sess.EmployeeID, sess.Name, sess.Designation, sess.Company

	h.saveWithPhotos(w, r, sub, uploads, afterUploads, func(status int, errs map[string]string, serverFailed bool) {
		h.showForm(w, r, status, sess, in, errs, serverFailed)
	})
}

// failFunc shows the form again with a status, field errors and whether the server failed.
type failFunc func(status int, errs map[string]string, serverFailed bool)

// saveWithPhotos stores the photos of a validated submission and saves it, then sends the employee to
// the thank-you page. Shared by the classic form and the wizard; fail shows the right form again.
func (h *socHandlers) saveWithPhotos(w http.ResponseWriter, r *http.Request, sub socSubmission, uploads, afterUploads []*multipart.FileHeader, fail failFunc) {
	// Photos: at most 4 Before and 4 After, each compressed to 500 KB. They are processed before the
	// observation is saved, so a bad photo gives an error message instead of a half-saved post.
	// The reporter may close the finding right away: it needs an After photo, except a Positive Finding.
	if r.FormValue("close") == "1" {
		if closeBlocked(sub.Category, statusClosed, len(afterUploads)) {
			fail(http.StatusUnprocessableEntity, map[string]string{"after_photos": "Add at least one After photo before closing."}, false)
			return
		}
		sub.Status = statusClosed
	}
	stored, err := storeUploads(r.Context(), h.evidenceDir, uploads, 0)
	if err != nil {
		fail(http.StatusUnprocessableEntity, map[string]string{"photos": photoMessage(langOf(r), err)}, false)
		return
	}
	storedAfter, err := storeUploads(r.Context(), h.evidenceDir, afterUploads, 0)
	if err != nil {
		removePhotoFiles(h.evidenceDir, namesOf(stored))
		fail(http.StatusUnprocessableEntity, map[string]string{"after_photos": photoMessage(langOf(r), err)}, false)
		return
	}
	records := make([]photoRecord, 0, len(stored)+len(storedAfter))
	names := make([]string, 0, len(stored)+len(storedAfter))
	for _, p := range stored {
		records = append(records, photoRecord{Kind: "before", StoredName: p.Name, Size: p.Size})
		names = append(names, p.Name)
	}
	for _, p := range storedAfter {
		records = append(records, photoRecord{Kind: "after", StoredName: p.Name, Size: p.Size})
		names = append(names, p.Name)
	}
	id, err := h.sink.Save(r.Context(), sub, records)
	if err != nil {
		removePhotoFiles(h.evidenceDir, names) // do not leave pictures behind for a post that was not saved
		log.Printf("SOC save failed: %v", err) // the date of birth is not in sub, so it cannot reach this log
		fail(http.StatusInternalServerError, nil, true)
		return
	}
	http.Redirect(w, r, "/soc/thanks?ref="+socRef(id), http.StatusSeeOther)
}

var refShape = regexp.MustCompile(`^SOC-\d{6}$`)

func (h *socHandlers) thanksPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	ref := r.URL.Query().Get("ref")
	if !refShape.MatchString(ref) {
		http.Redirect(w, r, "/soc/new", http.StatusSeeOther)
		return
	}
	data := socPageData{Title: "Thank you", Session: sess, Ref: ref}
	// Thumbnails of the photos just sent. They come from this employee's own list, so a reference
	// that is not theirs shows no pictures.
	if items, err := h.sink.ListByEmployee(r.Context(), sess.EmployeeID); err == nil {
		for _, it := range items {
			if it.Ref == ref {
				data.Photos = it.Photos
			}
		}
	}
	renderSOC(w, r, http.StatusOK, "soc_thanks", data)
}

// photoFile serves one photo to the employee who sent it. The id in the URL alone is not enough:
// the database must say the photo belongs to a post of the signed-in employee.
func (h *socHandlers) photoFile(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	name, owned, err := h.sink.OwnPhoto(r.Context(), id, sess.EmployeeID)
	if err != nil {
		log.Printf("SOC photo lookup failed: %v", err)
		http.Error(w, tr(langOf(r), "Could not load the photo."), http.StatusInternalServerError)
		return
	}
	if !owned {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=600")
	path, ok := evidencePath(h.evidenceDir, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (h *socHandlers) minePage(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	items, err := h.sink.ListByEmployee(r.Context(), sess.EmployeeID)
	if err != nil {
		log.Printf("SOC list failed: %v", err)
		http.Error(w, tr(langOf(r), "Could not load your submissions."), http.StatusInternalServerError)
		return
	}
	data := socPageData{Title: "My submissions", Session: sess, Items: items}
	if ref := r.URL.Query().Get("saved"); refShape.MatchString(ref) {
		data.Notice = tr(langOf(r), "Changes saved") + ": " + ref
	}
	if ref := r.URL.Query().Get("after"); refShape.MatchString(ref) {
		data.Notice = tr(langOf(r), "After photos saved") + ": " + ref
	}
	if ref := r.URL.Query().Get("closed"); refShape.MatchString(ref) {
		data.Notice = tr(langOf(r), "Finding closed; waiting for HSE verification") + ": " + ref
	}
	renderSOC(w, r, http.StatusOK, "soc_mine", data)
}

func (h *socHandlers) signout(w http.ResponseWriter, r *http.Request) {
	h.store.ClearToken(w, cookieSOCSession, h.secureCookie)
	h.store.ClearToken(w, cookieSOCCheck, h.secureCookie)
	http.Redirect(w, r, "/soc", http.StatusSeeOther)
}

// routes registers only the SOC pages and what they need, for the public listener. Nothing else
// is registered there, so the staff pages do not exist at that address (plan section 8.1b).
func (h *socHandlers) routes(static http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /soc", h.signinPage)
	mux.HandleFunc("GET /soc/lang", h.setLanguage)
	mux.HandleFunc("POST /soc/api/check", h.apiCheck)
	mux.HandleFunc("GET /soc/api/names", h.apiNames)
	mux.HandleFunc("POST /soc/api/signin", h.apiSignin)
	mux.HandleFunc("POST /soc/api/suggest", h.apiSuggest)
	mux.HandleFunc("GET /soc/new", h.wizardPage)
	mux.HandleFunc("POST /soc/new", h.wizardSubmit)
	mux.HandleFunc("GET /soc/form", h.formPage)
	mux.HandleFunc("POST /soc/form", h.formSubmit)
	mux.HandleFunc("GET /soc/thanks", h.thanksPage)
	mux.HandleFunc("GET /soc/mine", h.minePage)
	mux.HandleFunc("GET /soc/photo/{id}", h.photoFile)
	mux.HandleFunc("GET /soc/edit/{id}", h.editPage)
	mux.HandleFunc("POST /soc/edit/{id}", h.editSubmit)
	mux.HandleFunc("GET /soc/after/{id}", h.afterPage)
	mux.HandleFunc("POST /soc/after/{id}", h.afterSubmit)
	mux.HandleFunc("POST /soc/signout", h.signout)
	mux.Handle("GET /static/", static)
	// WHY: the Cloudflare tunnel sends only the /soc path to this listener, so a page that asked for
	// /static/... on the public host never reached it and rendered without styles. The pages use this
	// prefixed copy; /static/ stays for the local address.
	mux.Handle("GET /soc/static/", http.StripPrefix("/soc", static))
	return mux
}

// photoMessage is what the visitor sees when a photo is refused, in their language.
func photoMessage(lang string, err error) string {
	return tr(lang, "Photo not accepted") + ": " + photoReason(lang, err) + ". " + tr(lang, "Max 4 photos: JPEG, PNG, WebP or GIF.")
}

// photoReason translates the reason inside a photo error. The messages come from photos.go; one with
// a number or a wrapped cause is matched by its start, and anything unknown stays as written.
func photoReason(lang string, err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "a post can have at most") {
		return trf(lang, "a post can have at most %d photos", maxPhotosPerPost)
	}
	for _, known := range []string{"the photo is too big", "the photo is empty", "use a JPEG, PNG, WebP or GIF photo", "the file is not a valid picture",
		"the photo is too large in pixels", "the photo could not be compressed enough", "could not read the photo"} {
		if strings.HasPrefix(msg, known) {
			return tr(lang, known)
		}
	}
	return msg
}

var maxChars = regexp.MustCompile(`^Maximum (\d+) characters\.$`)

// translateErrors shows the form errors (written in English by the validation) in the visitor's
// language. "Maximum N characters." carries a number, so it is rebuilt around it.
func translateErrors(lang string, errs map[string]string) map[string]string {
	if errs == nil || lang == langEN {
		return errs
	}
	out := make(map[string]string, len(errs))
	for k, v := range errs {
		if m := maxChars.FindStringSubmatch(v); m != nil {
			n, _ := strconv.Atoi(m[1])
			out[k] = trf(lang, "Maximum %d characters.", n)
			continue
		}
		out[k] = tr(lang, v)
	}
	return out
}

// socHazardClass is the class shown under the Topic: that of the chosen topic's hazard category,
// or of the hazard the form already carries (an edit page), "None" for a topic without a hazard
// category, or "" before anything is chosen.
func socHazardClass(snap *refSnapshot, in socInput) string {
	if id, ok := parseID(in.TopicID); ok && id != 0 {
		if c := snap.HazardClassOf(snap.topicByID[id].HazardID); c != "" {
			return c
		}
		if _, found := snap.topicByID[id]; found {
			return "None" // a control topic such as PPE has no hazard category; the page translates it
		}
	}
	if id, ok := parseID(in.HazardID); ok && id != 0 {
		return snap.HazardClassOf(id)
	}
	return ""
}
