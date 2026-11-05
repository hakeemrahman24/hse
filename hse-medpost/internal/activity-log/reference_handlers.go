package activitylog

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"hse-medpost/internal/middleware"
)

// HTTP side of the Reference tab (plan/emr-plan.md section 11). Each handler
// only reads the request, calls the store in reference_store.go /
// reference_sections.go, and maps its errors to a status code. The rules live
// in the store.

// referenceRequestLimit caps the whole request body: the 10 MB file plus room
// for the other form fields and multipart framing. MaxBytesReader stops
// reading at the limit, so a huge upload is refused without being stored or
// buffered, which the store's own file-size check alone could not guarantee.
const referenceRequestLimit = MaxReferenceFileBytes + 1<<20

// errBadRequest marks a malformed request (missing file, bad number, unknown
// kind) so the status mapping can answer 400 with the message as written.
var errBadRequest = errors.New("bad request")

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, args...))
}

// RegisterReferenceRoutes adds the Reference routes to mux. Everyone signed in
// may read a tab and open its current files (the open itself reuses
// GET /api/documents/{id}/file). Every change, the library list, History and
// old-version downloads are admin only, using the same check as the existing
// POST/PUT/DELETE /api/documents routes (RequireProgramManager = admin role).
func (a *API) RegisterReferenceRoutes(mux *http.ServeMux) {
	anyUser := func(h http.HandlerFunc) http.Handler { return middleware.RequireAuth(h) }
	admin := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(middleware.RequireProgramManager(h))
	}
	mux.Handle("GET /api/reference/{section}", anyUser(a.ReferenceList))
	mux.Handle("GET /api/reference/{section}/library", admin(a.ReferenceLibrary))
	mux.Handle("POST /api/reference/{section}/documents", admin(a.ReferenceAdd))
	mux.Handle("POST /api/reference/{section}/pick", admin(a.ReferencePick))
	mux.Handle("DELETE /api/reference/{section}/documents/{id}", admin(a.ReferenceRemove))
	mux.Handle("DELETE /api/reference/documents/{id}", admin(a.ReferenceDelete))
	mux.Handle("POST /api/reference/documents/{id}/file", admin(a.ReferenceReplace))
	mux.Handle("GET /api/reference/documents/{id}/versions", admin(a.ReferenceVersions))
	mux.Handle("GET /api/reference/documents/{id}/versions/{no}/file", admin(a.ReferenceVersionFile))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// writeReferenceError maps a store error to a status code. Messages of the
// errors listed as 400/404/409/413 are written for users and are sent as they
// are; anything else is logged in full and answered with a bare 500 so internal
// detail (paths, SQL) never reaches the browser.
func writeReferenceError(w http.ResponseWriter, err error) {
	var dup *DuplicateFileError
	var shared *SharedDocumentError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &dup):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":       dup.Error(),
			"duplicateOf": map[string]any{"documentId": dup.DocumentID, "title": dup.Title},
		})
	case errors.As(err, &shared):
		writeJSON(w, http.StatusConflict, map[string]any{"error": shared.Error(), "sections": shared.Sections})
	case errors.As(err, &tooBig), errors.Is(err, ErrFileTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": ErrFileTooLarge.Error()})
	case errors.Is(err, ErrAlreadyInSection), errors.Is(err, ErrVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrUnknownSection), errors.Is(err, ErrDocumentNotFound), errors.Is(err, ErrNotInSection):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrFileType), errors.Is(err, ErrFileEmpty), errors.Is(err, ErrDocumentScope),
		errors.Is(err, ErrTitleRequired), errors.Is(err, ErrBadLink), errors.Is(err, errBadRequest):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		log.Printf("reference: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
	}
}

func staffIDOf(r *http.Request) int64 {
	if sess := middleware.Session(r); sess != nil {
		return sess.StaffID
	}
	return 0
}

func docIDFrom(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("bad document id")
	}
	return id, nil
}

// parseReferenceForm reads a multipart request under the size limit. On
// success the caller must defer r.MultipartForm.RemoveAll() so large uploads
// parked in temp files are cleaned up.
func parseReferenceForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, referenceRequestLimit)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || strings.Contains(err.Error(), "request body too large") {
			return ErrFileTooLarge
		}
		return badRequest("invalid form")
	}
	return nil
}

// ReferenceList: GET /api/reference/{section}. canManage tells the page
// whether to show the admin buttons; the server enforces the rule either way.
func (a *API) ReferenceList(w http.ResponseWriter, r *http.Request) {
	items, err := a.ListSectionDocuments(r.PathValue("section"))
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	sess := middleware.Session(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"canManage": sess != nil && sess.Role == "admin",
	})
}

// ReferenceLibrary: GET /api/reference/{section}/library, the "Pick existing" list.
func (a *API) ReferenceLibrary(w http.ResponseWriter, r *http.Request) {
	items, err := a.ListLibraryDocuments(r.PathValue("section"))
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ReferenceAdd: POST /api/reference/{section}/documents. Creates a new library
// document (an uploaded file or a web link) and shows it in this section.
func (a *API) ReferenceAdd(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	// Checked first so a bad section cannot leave a new document that no tab shows.
	if !referenceSections[section] {
		writeReferenceError(w, ErrUnknownSection)
		return
	}
	if err := parseReferenceForm(w, r); err != nil {
		writeReferenceError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()

	var procID *int64
	if raw := strings.TrimSpace(r.FormValue("procId")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeReferenceError(w, badRequest("bad procId"))
			return
		}
		procID = &id
	}
	dtype, title, notes := r.FormValue("documentsType"), r.FormValue("documentTitle"), r.FormValue("notes")
	staffID := staffIDOf(r)

	var docID int64
	var err error
	switch r.FormValue("kind") {
	case "link":
		docID, err = a.AddLinkDocument(procID, dtype, title, notes, r.FormValue("formLink"))
	case "file":
		file, hdr, ferr := r.FormFile("file")
		if ferr != nil {
			writeReferenceError(w, badRequest("choose a file"))
			return
		}
		defer file.Close()
		docID, err = a.AddDocumentWithFile(procID, dtype, title, notes, ReferenceUpload{Original: hdr.Filename, Data: file}, staffID)
	default:
		err = badRequest("kind must be link or file")
	}
	if err == nil {
		err = a.AddDocumentToSection(docID, section, staffID)
	}
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "documentId": docID})
}

// ReferencePick: POST /api/reference/{section}/pick with {"documentId": n}.
// Shows an existing library document in this section; nothing is uploaded.
func (a *API) ReferencePick(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var body struct {
		DocumentID int64 `json:"documentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DocumentID <= 0 {
		writeReferenceError(w, badRequest("documentId required"))
		return
	}
	if err := a.AddDocumentToSection(body.DocumentID, r.PathValue("section"), staffIDOf(r)); err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

// ReferenceRemove: DELETE /api/reference/{section}/documents/{id}. Takes the
// document out of this tab only.
func (a *API) ReferenceRemove(w http.ResponseWriter, r *http.Request) {
	id, err := docIDFrom(r)
	if err == nil {
		err = a.RemoveDocumentFromSection(id, r.PathValue("section"))
	}
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ReferenceDelete: DELETE /api/reference/documents/{id}?section=malaria.
// Deletes the document everywhere, unless another section still shows it.
func (a *API) ReferenceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := docIDFrom(r)
	if err == nil {
		err = a.DeleteReferenceDocument(id, r.URL.Query().Get("section"))
	}
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ReferenceReplace: POST /api/reference/documents/{id}/file. Adds a new version.
func (a *API) ReferenceReplace(w http.ResponseWriter, r *http.Request) {
	id, err := docIDFrom(r)
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	if err := parseReferenceForm(w, r); err != nil {
		writeReferenceError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeReferenceError(w, badRequest("choose a file"))
		return
	}
	defer file.Close()
	no, err := a.ReplaceDocumentFile(id, ReferenceUpload{Original: hdr.Filename, Data: file}, staffIDOf(r))
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "versionNo": no})
}

// ReferenceVersions: GET /api/reference/documents/{id}/versions (History).
func (a *API) ReferenceVersions(w http.ResponseWriter, r *http.Request) {
	id, err := docIDFrom(r)
	var versions []ReferenceVersion
	if err == nil {
		versions, err = a.ListDocumentVersions(id)
	}
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

// ReferenceVersionFile: GET /api/reference/documents/{id}/versions/{no}/file.
// Always a download (never rendered in the page) and with nosniff, so an
// uploaded file can only ever be saved, not interpreted by the browser as
// something else.
func (a *API) ReferenceVersionFile(w http.ResponseWriter, r *http.Request) {
	id, err := docIDFrom(r)
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	no, convErr := strconv.Atoi(r.PathValue("no"))
	if convErr != nil || no <= 0 {
		writeReferenceError(w, badRequest("bad version number"))
		return
	}
	path, original, err := a.VersionFile(id, no)
	if err != nil {
		writeReferenceError(w, err)
		return
	}
	name := original
	if name == "" {
		name = filepath.Base(path)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeFile(w, r, path)
}
