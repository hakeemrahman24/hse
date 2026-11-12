package directory

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"hse-medpost/internal/display"
)

// Evidence kinds as Medpost stores them. "Photos" are shown as pictures; every other kind
// (attendance list, report attachment) is proof that is opened as a document.
const evidencePhotos = "Photos"

// EvidenceFile is one uploaded photo or proof file of an activity. Who uploaded it is not
// shared; only the stored file name and the type.
type EvidenceFile struct {
	EvidenceID int64  `json:"evidenceId"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Date       string `json:"date"`
}

// ActivityFiles is the photos and the proof files of one activity.
type ActivityFiles struct {
	Photos []EvidenceFile `json:"photos"`
	Proof  []EvidenceFile `json:"proof"`
}

// Document is one reference document of a health program (procedure, socialization material,
// form or template). The file itself is fetched from DocumentFile.
type Document struct {
	DocumentID int64  `json:"documentId"`
	Program    string `json:"program"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Notes      string `json:"notes"`
	FormLink   string `json:"formLink"`
	FileName   string `json:"fileName"`
}

// browserRenderable lists the extensions a browser shows inline. Anything else is sent as a
// download so it is saved under its real name.
var browserRenderable = map[string]bool{
	".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".bmp": true, ".txt": true, ".mp4": true, ".webm": true, ".mp3": true, ".wav": true, ".ogg": true,
}

// ActivityFiles lists the photos and proof of one activity (GET /api/directory/activities/{id}/files).
// Placeholder demo photos (no real file) are left out.
func (a *API) ActivityFiles(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var one int
	if err := a.DB.QueryRow(`SELECT 1 FROM ActivityTable WHERE ActivityID = ?`, id).Scan(&one); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	rows, err := a.DB.Query(`SELECT EvidenceID, EvidenceType, COALESCE(FileLink,''), COALESCE(UploadDate,'')
		FROM EvidenceTable WHERE FK_IDActivity = ? ORDER BY EvidenceID`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := ActivityFiles{Photos: []EvidenceFile{}, Proof: []EvidenceFile{}}
	for rows.Next() {
		var f EvidenceFile
		var link string
		if err := rows.Scan(&f.EvidenceID, &f.Type, &link, &f.Date); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if link == "" || strings.HasPrefix(link, "placeholder:") {
			continue
		}
		f.Name = filepath.Base(link)
		if f.Type == evidencePhotos {
			out.Photos = append(out.Photos, f)
		} else {
			out.Proof = append(out.Proof, f)
		}
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// attachFiles fills Photos and Proof of the given activities with one query. Placeholder demo
// photos are skipped, as in ActivityFiles.
func (a *API) attachFiles(acts []Activity) error {
	if len(acts) == 0 {
		return nil
	}
	index := make(map[int64]int, len(acts))
	marks := make([]string, len(acts))
	args := make([]any, len(acts))
	for i, act := range acts {
		index[act.ActivityID] = i
		marks[i], args[i] = "?", act.ActivityID
	}
	rows, err := a.DB.Query(`SELECT FK_IDActivity, EvidenceID, EvidenceType, COALESCE(FileLink,''), COALESCE(UploadDate,'')
		FROM EvidenceTable WHERE FK_IDActivity IN (`+strings.Join(marks, ",")+`) ORDER BY EvidenceID`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var actID int64
		var f EvidenceFile
		var link string
		if err := rows.Scan(&actID, &f.EvidenceID, &f.Type, &link, &f.Date); err != nil {
			return err
		}
		if link == "" || strings.HasPrefix(link, "placeholder:") {
			continue
		}
		f.Name = filepath.Base(link)
		i := index[actID]
		if f.Type == evidencePhotos {
			acts[i].Photos = append(acts[i].Photos, f)
		} else {
			acts[i].Proof = append(acts[i].Proof, f)
		}
	}
	return rows.Err()
}

// EvidenceFile streams one evidence file (GET /api/directory/evidence/{id}[/{filename}]).
func (a *API) EvidenceFile(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var link sql.NullString
	err = a.DB.QueryRow(`SELECT FileLink FROM EvidenceTable WHERE EvidenceID = ?`, id).Scan(&link)
	if err == sql.ErrNoRows || (err == nil && (!link.Valid || link.String == "" || strings.HasPrefix(link.String, "placeholder:"))) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	serveStored(w, r, link.String)
}

// Documents lists the program reference documents (GET /api/directory/documents).
func (a *API) Documents(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	rows, err := a.DB.Query(`SELECT d.DocumentID, COALESCE(p.HealthProcedure,''), d.DocumentsType, d.DocumentTitle,
			COALESCE(d.Notes,''), COALESCE(d.FormLink,''), COALESCE(d.FileLink,'')
		FROM DocumentTable d LEFT JOIN HealthProcedureTable p ON p.ProcID = d.FK_ProcID
		ORDER BY p.HealthProcedure, d.DocumentsType, d.DocumentTitle`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var d Document
		var program, link string
		if err := rows.Scan(&d.DocumentID, &program, &d.Type, &d.Title, &d.Notes, &d.FormLink, &link); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if program != "" {
			d.Program = display.ProgramTitle(program)
		}
		if link != "" {
			d.FileName = filepath.Base(link)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DocumentFile streams one document file (GET /api/directory/documents/{id}[/{filename}]).
func (a *API) DocumentFile(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var link sql.NullString
	err = a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&link)
	if err == sql.ErrNoRows || (err == nil && (!link.Valid || link.String == "")) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	serveStored(w, r, link.String)
}

// serveStored sends a stored upload. The sandbox policy and nosniff keep a file that an
// uploader made to look like a page (an .svg or .html) from running scripts in the viewer's
// browser when it is opened through another application.
func serveStored(w http.ResponseWriter, r *http.Request, path string) {
	name := filepath.Base(path)
	disposition := "attachment"
	if browserRenderable[strings.ToLower(filepath.Ext(name))] {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// WHY a PDF is left out: Edge and Chrome refuse their built-in PDF viewer inside a sandboxed
	// response ("This page has been blocked by Microsoft Edge"). A .pdf is still sent with nosniff and
	// its own type, so it cannot run as a page; every other type keeps the sandbox.
	if strings.ToLower(filepath.Ext(name)) != ".pdf" {
		h.Set("Content-Security-Policy", "sandbox")
	}
	h.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, name))
	http.ServeFile(w, r, path)
}
