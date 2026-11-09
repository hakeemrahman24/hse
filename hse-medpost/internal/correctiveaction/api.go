package correctiveaction

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/middleware"
	"hse-medpost/internal/storage"
)

const (
	// maxJSONBytes bounds a JSON request body. The longest field is 4000 characters, so 1 MB is generous.
	maxJSONBytes = 1 << 20
	// maxUploadRequestBytes bounds a whole photo upload: three files at the per-file cap plus form overhead.
	maxUploadRequestBytes = 3*maxUploadBytes + 1<<20
	// photoCacheSeconds lets a browser reuse a photo for an hour. A photo id never changes its file.
	photoCacheSeconds = 3600
)

// API serves the corrective action endpoints. UploadDir is the same root the activity evidence uses.
type API struct {
	Store     *Store
	UploadDir string
}

// Register adds every route. auth wraps routes any signed-in staff may use; admin wraps the one that
// deletes a finding. They are passed in so this package does not decide the role rules and tests can
// supply their own.
func (a *API) Register(mux *http.ServeMux, auth, admin func(http.Handler) http.Handler) {
	route := func(pattern string, wrap func(http.Handler) http.Handler, h http.HandlerFunc) {
		mux.Handle(pattern, wrap(h))
	}
	route("GET /api/corrective-actions", auth, a.List)
	route("GET /api/corrective-actions/employees", auth, a.ListEmployees)
	route("GET /api/corrective-actions.xlsx", auth, a.Export)
	route("POST /api/corrective-actions", auth, a.Create)
	route("GET /api/corrective-actions/{id}", auth, a.Get)
	route("PUT /api/corrective-actions/{id}", auth, a.Update)
	route("DELETE /api/corrective-actions/{id}", admin, a.Delete)
	route("POST /api/corrective-actions/{id}/close", auth, a.Close)
	route("POST /api/corrective-actions/{id}/reopen", auth, a.Reopen)
	route("GET /api/corrective-actions/{id}/history", auth, a.History)
	route("POST /api/corrective-actions/{id}/photos", auth, a.UploadPhotos)
	route("POST /api/corrective-actions/{id}/follow-ups", auth, a.AddFollowUp)
	route("GET /api/corrective-action-photos/{id}", auth, a.ServePhoto)
	route("DELETE /api/corrective-action-photos/{id}", auth, a.DeletePhoto)
	route("PUT /api/corrective-action-follow-ups/{id}", auth, a.UpdateFollowUp)
	route("DELETE /api/corrective-action-follow-ups/{id}", auth, a.DeleteFollowUp)
	route("POST /api/corrective-action-follow-ups/{id}/done", auth, a.CompleteFollowUp)
	route("POST /api/corrective-action-follow-ups/{id}/reopen", auth, a.ReopenFollowUp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError turns a store error into a response. A ValidationError is the user's to fix, so its text is
// shown; anything else is a server fault, whose real error goes to the log and never to the browser.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid ValidationError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": invalid.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		log.Printf("corrective action %s %s: %v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
	}
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		badRequest(w, "invalid request")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequest(w, "bad id")
		return 0, false
	}
	return id, true
}

// caller returns the signed-in staff member. The routes sit behind RequireAuth, so a missing session only
// happens if a route was registered without it; fail closed.
func caller(w http.ResponseWriter, r *http.Request) (staffID int64, name string, ok bool) {
	sess := middleware.Session(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return 0, "", false
	}
	return sess.StaffID, sess.Name, true
}

// withURLs gives every photo the address the browser loads it from. The file path stays on the server.
func withURLs(a *Action) {
	for _, list := range [][]Photo{a.BeforePhotos, a.AfterPhotos} {
		for i := range list {
			list[i].URL = "/api/corrective-action-photos/" + strconv.FormatInt(list[i].ID, 10)
		}
	}
}

func (a *API) respondWithAction(w http.ResponseWriter, r *http.Request, status int, id int64) {
	action, err := a.Store.Get(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	withURLs(&action)
	writeJSON(w, status, action)
}

// actionBody is the JSON for creating or editing a finding. picId is the picked employee's id, used only
// as a lookup key (see Input.PicPatientID).
type actionBody struct {
	ProcID         int64  `json:"procId"`
	ActivityID     int64  `json:"activityId"`
	InspectionDate string `json:"inspectionDate"`
	Location       string `json:"location"`
	Finding        string `json:"finding"`
	Recommendation string `json:"recommendation"`
	PicID          int64  `json:"picId"`
	DueDate        string `json:"dueDate"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
}

func (b actionBody) input() Input {
	return Input{
		ProcID: b.ProcID, ActivityID: b.ActivityID, InspectionDate: b.InspectionDate, Location: b.Location,
		Finding: b.Finding, Recommendation: b.Recommendation, PicPatientID: b.PicID, DueDate: b.DueDate,
		Severity: b.Severity, Status: b.Status,
	}
}

type followUpBody struct {
	Description string `json:"description"`
	PicID       int64  `json:"picId"`
	DueDate     string `json:"dueDate"`
}

func (b followUpBody) input() FollowUpInput {
	return FollowUpInput{Description: b.Description, PicPatientID: b.PicID, DueDate: b.DueDate}
}

// parseFilter reads the list filters from the query string, shared by the table and the Excel export so
// both always show the same findings. It answers the 400 itself and returns false when a value is bad.
func parseFilter(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	q := r.URL.Query()
	filter := Filter{
		Status: q.Get("status"), Severity: q.Get("severity"), Q: q.Get("q"), From: q.Get("from"), To: q.Get("to"),
	}
	var err error
	if v := q.Get("procId"); v != "" {
		if filter.ProcID, err = strconv.ParseInt(v, 10, 64); err != nil {
			badRequest(w, "bad program id")
			return filter, false
		}
	}
	if v := q.Get("limit"); v != "" {
		if filter.Limit, err = strconv.Atoi(v); err != nil {
			badRequest(w, "bad limit")
			return filter, false
		}
	}
	return filter, true
}

// List: GET /api/corrective-actions?procId=&status=&severity=&q=&from=&to=&limit=
func (a *API) List(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	actions, err := a.Store.List(filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for i := range actions {
		withURLs(&actions[i])
	}
	writeJSON(w, http.StatusOK, actions)
}

// ListEmployees: GET /api/corrective-actions/employees?q= feeds the PIC dropdown.
func (a *API) ListEmployees(w http.ResponseWriter, r *http.Request) {
	employees, err := a.Store.SearchEmployees(r.URL.Query().Get("q"), 10)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, employees)
}

func (a *API) Get(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		a.respondWithAction(w, r, http.StatusOK, id)
	}
}

func (a *API) Create(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	if !ok {
		return
	}
	var body actionBody
	if !readJSON(w, r, &body) {
		return
	}
	id, err := a.Store.Create(body.input(), staffID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.respondWithAction(w, r, http.StatusCreated, id)
}

func (a *API) Update(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	var body actionBody
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Store.Update(id, body.input(), staffID); err != nil {
		writeError(w, r, err)
		return
	}
	a.respondWithAction(w, r, http.StatusOK, id)
}

func (a *API) Close(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	var body struct {
		ClosingDate string `json:"closingDate"`
		ActionTaken string `json:"actionTaken"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := a.Store.Close(id, body.ClosingDate, body.ActionTaken, staffID); err != nil {
		writeError(w, r, err)
		return
	}
	a.respondWithAction(w, r, http.StatusOK, id)
}

func (a *API) Reopen(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	if err := a.Store.Reopen(id, staffID); err != nil {
		writeError(w, r, err)
		return
	}
	a.respondWithAction(w, r, http.StatusOK, id)
}

func (a *API) History(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.Store.Get(id); err != nil {
		writeError(w, r, err)
		return
	}
	history, err := a.Store.History(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// Delete removes a finding and its photo files. Admin only (enforced by the route's wrapper). The history
// goes with the finding, so one line in the server log keeps the deletion traceable.
func (a *API) Delete(w http.ResponseWriter, r *http.Request) {
	staffID, name, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	action, err := a.Store.Get(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	paths, err := a.Store.Delete(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	log.Printf("corrective action %d deleted by staff %d (%s): program %q, inspected %s, finding %q",
		id, staffID, name, action.ProgramName, action.InspectionDate, action.Finding)
	for _, p := range paths {
		a.removeFile(p)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// removeFile deletes a stored photo. A missing file is fine (already gone); anything else is logged. The
// database row is already deleted, so a leftover file wastes disk but never shows up in the app.
func (a *API) removeFile(path string) {
	if !a.insideUploadDir(path) {
		log.Printf("corrective action: refusing to delete %q outside the upload folder", path)
		return
	}
	// The small copy goes with the photo.
	for _, p := range []string{path, thumbPath(path)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("corrective action: could not remove %q: %v", p, err)
		}
	}
}

// thumbPath is where a photo's small copy lives: next to it, as "name.thumb.jpg". Keeping it on disk means
// the table and the Excel export read a few KB per photo instead of decoding and shrinking a 600 KB JPEG
// every time, which matters on the Pi.
func thumbPath(photoPath string) string {
	return strings.TrimSuffix(photoPath, filepath.Ext(photoPath)) + ".thumb.jpg"
}

// saveThumbnail writes the small copy of a photo. It is best effort: if it fails the photo is still stored,
// and thumbnailFor makes the copy the first time someone asks for it.
func (a *API) saveThumbnail(photoPath string, photo []byte) []byte {
	thumb, err := Thumbnail(photo, ThumbnailSide)
	if err != nil {
		log.Printf("corrective action thumbnail for %q: %v", photoPath, err)
		return nil
	}
	tp := thumbPath(photoPath)
	if _, err := storage.Save(filepath.Dir(tp), filepath.Base(tp), bytes.NewReader(thumb)); err != nil {
		log.Printf("corrective action: could not save thumbnail %q: %v", tp, err)
	}
	return thumb
}

// thumbnailFor returns the small copy of a stored photo, making and saving it first if it is missing (a
// photo stored before thumbnails were kept on disk, or whose copy failed to save). The caller has already
// checked the path is inside the upload folder.
func (a *API) thumbnailFor(photoPath string) ([]byte, error) {
	if thumb, err := os.ReadFile(thumbPath(photoPath)); err == nil {
		return thumb, nil
	}
	photo, err := os.ReadFile(photoPath)
	if err != nil {
		return nil, fmt.Errorf("read photo for thumbnail: %w", err)
	}
	if thumb := a.saveThumbnail(photoPath, photo); thumb != nil {
		return thumb, nil
	}
	return nil, fmt.Errorf("could not make a thumbnail for %q", photoPath)
}

// insideUploadDir guards every file read or delete: the path comes from our own database, but a path that
// resolves outside the upload folder is never touched, whatever the row says.
func (a *API) insideUploadDir(path string) bool {
	rel, err := filepath.Rel(a.UploadDir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// UploadPhotos: POST /api/corrective-actions/{id}/photos, multipart with phase=Before|After and up to three
// files in "photos". Each file is checked and reduced under 600 KB by PreparePhoto before it is stored.
func (a *API) UploadPhotos(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		badRequest(w, "the upload is invalid or too large")
		return
	}
	defer r.MultipartForm.RemoveAll()
	phase := strings.TrimSpace(r.FormValue("phase"))
	if phase != PhaseBefore && phase != PhaseAfter {
		badRequest(w, "phase must be Before or After")
		return
	}
	files := r.MultipartForm.File["photos"]
	if len(files) == 0 {
		badRequest(w, "choose at least one photo")
		return
	}
	if len(files) > MaxPhotosPerPhase {
		badRequest(w, fmt.Sprintf("choose at most %d photos at a time", MaxPhotosPerPhase))
		return
	}
	action, err := a.Store.Get(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	dir := filepath.Join(a.UploadDir, storage.FolderSlug(action.ProgramKey), "corrective-actions", phase)
	for _, header := range files {
		if err := a.storePhoto(header, dir, id, phase, staffID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	a.respondWithAction(w, r, http.StatusCreated, id)
}

// storePhoto checks, reduces and saves one uploaded file, then records it. If recording fails (for example
// the photo limit) the file is removed again so no orphan is left on disk.
func (a *API) storePhoto(header *multipart.FileHeader, dir string, actionID int64, phase string, staffID int64) error {
	file, err := header.Open()
	if err != nil {
		return fmt.Errorf("open uploaded photo: %w", err)
	}
	defer file.Close()
	data, err := PreparePhoto(file)
	if err != nil {
		return err
	}
	token := make([]byte, 4)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("make photo name: %w", err)
	}
	name := fmt.Sprintf("%s_car%d_%s_%s.jpg", dates.Today(), actionID, strings.ToLower(phase), hex.EncodeToString(token))
	path, err := storage.Save(dir, name, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("save photo: %w", err)
	}
	if _, err := a.Store.AddPhoto(actionID, phase, path, staffID); err != nil {
		a.removeFile(path)
		return err
	}
	a.saveThumbnail(path, data)
	return nil
}

// ServePhoto streams one stored photo to a signed-in user.
func (a *API) ServePhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	path, err := a.Store.PhotoPath(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !a.insideUploadDir(path) {
		log.Printf("corrective action photo %d points outside the upload folder: %q", id, path)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(photoCacheSeconds))
	if r.URL.Query().Get("thumb") == "1" {
		a.serveThumbnail(w, r, path)
		return
	}
	http.ServeFile(w, r, path)
}

// serveThumbnail answers ?thumb=1 with a small copy of the photo, so a table of findings loads a few KB
// per picture instead of up to 600 KB. The copy is made on the fly (a stored photo is already under
// 600 KB, so this is quick) and the browser keeps it for an hour.
func (a *API) serveThumbnail(w http.ResponseWriter, r *http.Request, path string) {
	thumb, err := a.thumbnailFor(path)
	if err != nil {
		// A stored file that is missing or not a readable image is a data problem, not the user's: log it
		// and fall back to the original so the page still shows something.
		log.Printf("corrective action thumbnail for %q: %v", path, err)
		http.ServeFile(w, r, path)
		return
	}
	_, _ = w.Write(thumb)
}

func (a *API) DeletePhoto(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	path, err := a.Store.DeletePhoto(id, staffID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.removeFile(path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) AddFollowUp(w http.ResponseWriter, r *http.Request) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	var body followUpBody
	if !readJSON(w, r, &body) {
		return
	}
	if _, err := a.Store.AddFollowUp(id, body.input(), staffID); err != nil {
		writeError(w, r, err)
		return
	}
	a.respondWithAction(w, r, http.StatusCreated, id)
}

// followUpAction runs one follow-up change and answers {"ok":true}; the page reloads the finding after it.
func (a *API) followUpAction(w http.ResponseWriter, r *http.Request, do func(id, staffID int64) error) {
	staffID, _, ok := caller(w, r)
	id, idOK := pathID(w, r)
	if !ok || !idOK {
		return
	}
	if err := do(id, staffID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) UpdateFollowUp(w http.ResponseWriter, r *http.Request) {
	var body followUpBody
	if !readJSON(w, r, &body) {
		return
	}
	a.followUpAction(w, r, func(id, staffID int64) error { return a.Store.UpdateFollowUp(id, body.input(), staffID) })
}

func (a *API) CompleteFollowUp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DoneDate    string `json:"doneDate"`
		ActionTaken string `json:"actionTaken"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	a.followUpAction(w, r, func(id, staffID int64) error {
		return a.Store.CompleteFollowUp(id, body.DoneDate, body.ActionTaken, staffID)
	})
}

func (a *API) ReopenFollowUp(w http.ResponseWriter, r *http.Request) {
	a.followUpAction(w, r, a.Store.ReopenFollowUp)
}

func (a *API) DeleteFollowUp(w http.ResponseWriter, r *http.Request) {
	a.followUpAction(w, r, a.Store.DeleteFollowUp)
}
