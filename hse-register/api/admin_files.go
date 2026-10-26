package main

import (
	"database/sql"
	"errors"
	"log"
	"mime"
	"net/http"
	"strconv"
)

// File kinds: "before" files are the evidence of the finding; "after" files document the finished
// action. Both follow the same rules: photos at most 4 per kind and 500 KB each (photos.go), PDFs
// at most 2 per kind and 5 MB each (documents.go).
const (
	photoBefore = "before"
	photoAfter  = "after"

	fileTypePhoto = "photo"
	fileTypePDF   = "pdf"

	// Largest request the upload endpoint reads: 4 photos + 2 PDFs at their upload limits.
	maxUploadRequest = maxPhotosPerPost*photoMaxUploadBytes + maxPDFsPerKind*pdfMaxBytes + (256 << 10)
)

// FileView is what the edit page needs to show one stored file.
type FileView struct {
	ID      int64
	Kind    string
	Type    string
	Display string // PDF label
	Size    int
}

// fileSection is one block of the edit page: the before files or the after files.
type fileSection struct {
	Kind, Title string
	Photos      []FileView
	PDFs        []FileView
}

func (s fileSection) CanAddPhoto() bool { return len(s.Photos) < maxPhotosPerPost }

// PhotoSlots is how many more photos can be added (the photo boxes shown in the upload form).
func (s fileSection) PhotoSlots() int {
	return max(0, maxPhotosPerPost-len(s.Photos))
}
func (s fileSection) CanAddPDF() bool { return len(s.PDFs) < maxPDFsPerKind }
func (s fileSection) Count() int      { return len(s.Photos) + len(s.PDFs) }

// sectionsOf groups files into the "before" and "after" sections (both always present).
func sectionsOf(files []FileView) []fileSection {
	before := fileSection{Kind: photoBefore, Title: "Evidence / Bukti (before)"}
	after := fileSection{Kind: photoAfter, Title: "After / Sesudah (action done)"}
	for _, f := range files {
		s := &before
		if f.Kind == photoAfter {
			s = &after
		}
		if f.Type == fileTypePDF {
			s.PDFs = append(s.PDFs, f)
		} else {
			s.Photos = append(s.Photos, f)
		}
	}
	return []fileSection{before, after}
}

type fileHandlers struct {
	db   *sql.DB
	auth *authContext
	dir  string // evidence folder
}

func validFileKind(k string) bool { return k == photoBefore || k == photoAfter }

// listFiles returns the files of one observation, oldest first.
func listFiles(db *sql.DB, observationID int64) ([]FileView, error) {
	rows, err := db.Query(`SELECT id, kind, file_type, COALESCE(display_name,''), size_bytes FROM observation_files WHERE observation_id = ? ORDER BY id`, observationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileView
	for rows.Next() {
		var f FileView
		if err := rows.Scan(&f.ID, &f.Kind, &f.Type, &f.Display, &f.Size); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// fileNames returns the stored file names of one observation, so the files can be removed when the
// observation is deleted (the database cascade removes only the rows).
func fileNames(db *sql.DB, observationID int64) []string {
	rows, err := db.Query(`SELECT stored_name FROM observation_files WHERE observation_id = ?`, observationID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	return names
}

// serve streams one stored file to a signed-in staff member. The file name comes from the
// database, never from the URL, so the URL cannot be used to read other files. A PDF is always sent
// as a download, never displayed inside the site.
func (h *fileHandlers) serve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var name, fileType, display string
	if err := h.db.QueryRow(`SELECT stored_name, file_type, COALESCE(display_name,'') FROM observation_files WHERE id = ?`, id).Scan(&name, &fileType, &display); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if fileType == fileTypePDF {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": display}))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	path, ok := evidencePath(h.dir, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

type fileCounts struct{ photos, pdfs int }

func (h *fileHandlers) counts(obsID int64, kind string) (fileCounts, error) {
	var c fileCounts
	err := h.db.QueryRow(`SELECT COALESCE(SUM(file_type = 'photo'),0), COALESCE(SUM(file_type = 'pdf'),0)
		FROM observation_files WHERE observation_id = ? AND kind = ?`, obsID, kind).Scan(&c.photos, &c.pdfs)
	return c, err
}

// upload adds photos ("photos" field) and PDFs ("documents" field) of one kind to an existing
// observation. The per-kind limits count the files already there.
func (h *fileHandlers) upload(w http.ResponseWriter, r *http.Request) {
	obsID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	kind := r.URL.Query().Get("kind")
	if !validFileKind(kind) {
		http.Error(w, "kind must be before or after", http.StatusBadRequest)
		return
	}
	o, err := getObservation(r.Context(), h.db, obsID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	perms := permsFor(o, viewer{Username: h.auth.session(r).Username, Role: h.auth.session(r).Role})
	if (kind == photoBefore && !perms.CanEditEvidence) || (kind == photoAfter && !perms.CanAddAfter) {
		http.Error(w, "You cannot add these files to this observation (it may be closed, or the evidence belongs to the person who entered it).", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequest)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "The files are too large.", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.MultipartForm.RemoveAll()

	have, err := h.counts(obsID, kind)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	up, err := prepareUploads(r.Context(), h.dir, r.MultipartForm, have)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err := up.insert(r.Context(), h.db, obsID, kind); err != nil {
		up.discard(h.dir)
		log.Printf("file insert failed: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	organizeQuietly(r.Context(), h.db, h.dir, obsID)
	// After files are managed on the observation page, evidence on the edit page.
	if kind == photoAfter {
		http.Redirect(w, r, "/observations/"+strconv.FormatInt(obsID, 10)+"?done=uploaded", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(obsID, 10)+"/edit", http.StatusSeeOther)
}

func namesOf(ps []storedPhoto) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func docNames(ds []storedDoc) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

// remove deletes one file (row and file on disk). A closed observation is locked, so its files
// cannot be removed (permsFor).
func (h *fileHandlers) remove(w http.ResponseWriter, r *http.Request) {
	obsID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	fileID, err2 := strconv.ParseInt(r.PathValue("fileId"), 10, 64)
	if err := errors.Join(err1, err2); err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var name, kind string
	// Matching the observation id too means a file cannot be deleted through another post's page.
	if err := h.db.QueryRow(`SELECT stored_name, kind FROM observation_files WHERE id = ? AND observation_id = ?`, fileID, obsID).Scan(&name, &kind); err != nil {
		http.NotFound(w, r)
		return
	}
	o, err := getObservation(r.Context(), h.db, obsID)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	perms := permsFor(o, viewer{Username: h.auth.session(r).Username, Role: h.auth.session(r).Role})
	if (kind == photoBefore && !perms.CanEditEvidence) || (kind == photoAfter && !perms.CanAddAfter) {
		http.Error(w, "You cannot remove files from this observation (it may be closed, or the evidence belongs to the person who entered it).", http.StatusForbidden)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM observation_files WHERE id = ?`, fileID); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	removePhotoFiles(h.dir, []string{name})
	if kind == photoAfter {
		http.Redirect(w, r, "/observations/"+strconv.FormatInt(obsID, 10), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/observations/"+strconv.FormatInt(obsID, 10)+"/edit", http.StatusSeeOther)
}
