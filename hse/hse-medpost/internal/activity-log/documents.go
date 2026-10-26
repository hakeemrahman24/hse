package activitylog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
	"hse-medpost/internal/storage"
)

var uploadDatePrefix = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_`)

// browserRenderableExt are extensions mainstream browsers actually display
// inline. Everything else (docx, xlsx, heic, ...) always falls back to a
// download regardless of the disposition we send, and browsers are
// inconsistent about honoring the filename= hint on an "inline" response
// once they've decided to download it anyway — so for those we send
// "attachment" instead, which every browser reliably names from filename=.
var browserRenderableExt = map[string]bool{
	".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".txt": true,
	".mp4": true, ".webm": true, ".mp3": true, ".wav": true, ".ogg": true,
}

// contentDisposition builds the Content-Disposition header value for
// serving filename: inline (browser renders it) when the browser can
// actually display that extension, attachment (browser always saves it,
// but reliably under the real name) otherwise.
func contentDisposition(filename string) string {
	disposition := "attachment"
	if browserRenderableExt[strings.ToLower(filepath.Ext(filename))] {
		disposition = "inline"
	}
	return fmt.Sprintf(`%s; filename="%s"`, disposition, filename)
}

// openFileURL ends with the stored filename. A browser that saves an opened
// file uses the last part of the address, so /file or a bare id would save
// as "file" or "12" instead of the document name.
func openFileURL(prefix string, id int64, fileLink string) string {
	name := filepath.Base(fileLink)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return fmt.Sprintf("%s/%d", prefix, id)
	}
	return fmt.Sprintf("%s/%d/%s", prefix, id, url.PathEscape(name))
}

// uploadDateFromLink pulls the YYYY-MM-DD prefix that storage.DocumentFilename
// and storage.EvidenceFilename embed in every generated filename.
func uploadDateFromLink(fileLink string) string {
	if fileLink == "" {
		return ""
	}
	m := uploadDatePrefix.FindStringSubmatch(filepath.Base(fileLink))
	if m == nil {
		return ""
	}
	return m[1]
}

var docTypes = map[string]bool{
	"Procedure":             true,
	"SocializationMaterial": true,
	"Forms":                 true,
	"Template":              true,
}

func (a *API) ListDocuments(w http.ResponseWriter, r *http.Request) {
	procQ := strings.TrimSpace(r.URL.Query().Get("procId"))
	dtype := strings.TrimSpace(r.URL.Query().Get("type"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))

	q := `
		SELECT d.DocumentID, d.FK_ProcID, d.DocumentsType, d.DocumentTitle,
		       COALESCE(d.FormLink,''), COALESCE(d.FileLink,''), COALESCE(d.Notes,''),
		       COALESCE(p.HealthProcedure,'')
		FROM DocumentTable d
		LEFT JOIN HealthProcedureTable p ON p.ProcID = d.FK_ProcID
		WHERE 1=1`
	args := []any{}

	if scope == "common" || dtype == "Template" {
		q += ` AND d.DocumentsType = 'Template' AND d.FK_ProcID IS NULL`
	} else if procQ != "" {
		procID, err := strconv.ParseInt(procQ, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad procId"})
			return
		}
		q += ` AND d.FK_ProcID = ?`
		args = append(args, procID)
	}
	if dtype != "" && dtype != "Template" {
		if !docTypes[dtype] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad type"})
			return
		}
		q += ` AND d.DocumentsType = ?`
		args = append(args, dtype)
	}
	q += ` ORDER BY d.DocumentsType, d.DocumentTitle`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type doc struct {
		DocumentID      int64  `json:"documentId"`
		ProcID          *int64 `json:"procId"`
		DocumentsType   string `json:"documentsType"`
		DocumentTitle   string `json:"documentTitle"`
		FormLink        string `json:"formLink"`
		HasFile         bool   `json:"hasFile"`
		FileURL         string `json:"fileUrl,omitempty"`
		UploadDate      string `json:"uploadDate,omitempty"`
		Notes           string `json:"notes"`
		ProgramTitle    string `json:"programTitle,omitempty"`
		HealthProcedure string `json:"healthProcedure,omitempty"`
	}
	out := []doc{}
	for rows.Next() {
		var d doc
		var procID sql.NullInt64
		var fileLink, procName string
		if err := rows.Scan(&d.DocumentID, &procID, &d.DocumentsType, &d.DocumentTitle,
			&d.FormLink, &fileLink, &d.Notes, &procName); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if procID.Valid {
			v := procID.Int64
			d.ProcID = &v
			d.HealthProcedure = procName
			d.ProgramTitle = display.ProgramTitle(procName)
		}
		d.HasFile = fileLink != ""
		if d.HasFile {
			d.FileURL = openFileURL("/api/documents", d.DocumentID, fileLink)
			d.UploadDate = uploadDateFromLink(fileLink)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// Oldest first within each type — filenames already embed the upload
	// date (see storage.DocumentFilename), so this is just surfacing it.
	// Undated entries (link-only docs, or ones uploaded before this
	// convention existed) sort after dated ones.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DocumentsType != out[j].DocumentsType {
			return out[i].DocumentsType < out[j].DocumentsType
		}
		di, dj := out[i].UploadDate, out[j].UploadDate
		if di == "" && dj != "" {
			return false
		}
		if di != "" && dj == "" {
			return true
		}
		if di != dj {
			return di < dj
		}
		return out[i].DocumentTitle < out[j].DocumentTitle
	})

	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateDocument(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		a.createDocumentMultipart(w, r)
		return
	}

	var body struct {
		ProcID        *int64 `json:"procId"`
		DocumentsType string `json:"documentsType"`
		DocumentTitle string `json:"documentTitle"`
		FormLink      string `json:"formLink"`
		Notes         string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	a.insertDocument(w, body.ProcID, body.DocumentsType, body.DocumentTitle, body.FormLink, "", body.Notes)
}

func (a *API) createDocumentMultipart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	dtype := strings.TrimSpace(r.FormValue("documentsType"))
	title := strings.TrimSpace(r.FormValue("documentTitle"))
	formLink := strings.TrimSpace(r.FormValue("formLink"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	var procID *int64
	if raw := strings.TrimSpace(r.FormValue("procId")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad procId"})
			return
		}
		procID = &id
	}

	fileLink := ""
	file, hdr, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		program := ""
		if procID != nil {
			name, lookupErr := a.procedureName(*procID)
			if lookupErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
				return
			}
			program = name
		}
		pid := int64(0)
		if procID != nil {
			pid = *procID
		}
		dir := storage.DocumentDir(a.UploadDir, program, dtype)
		name := storage.DocumentFilename(dates.Today(), pid, dtype, title, hdr.Filename)
		saved, err := storage.Save(dir, name, file)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
			return
		}
		fileLink = saved
	} else if err != http.ErrMissingFile {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad file"})
		return
	}

	a.insertDocument(w, procID, dtype, title, formLink, fileLink, notes)
}

func (a *API) insertDocument(w http.ResponseWriter, procID *int64, dtype, title, formLink, fileLink, notes string) {
	dtype = strings.TrimSpace(dtype)
	title = strings.TrimSpace(title)
	formLink = strings.TrimSpace(formLink)
	notes = strings.TrimSpace(notes)

	if !docTypes[dtype] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "documentsType required"})
		return
	}
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "documentTitle required"})
		return
	}
	if formLink == "" && fileLink == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provide formLink or file"})
		return
	}
	if dtype == "Template" {
		if procID != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "templates must not have procId"})
			return
		}
	} else {
		if procID == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "procId required for program documents"})
			return
		}
		var exists int
		if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ?`, *procID).Scan(&exists); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
			return
		}
	}

	var procArg any
	if procID != nil {
		procArg = *procID
	}
	res, err := a.DB.Exec(`
		INSERT INTO DocumentTable (FK_ProcID, DocumentsType, DocumentTitle, FormLink, FileLink, Notes)
		VALUES (?, ?, ?, ?, ?, ?)`,
		procArg, dtype, title, nullIfEmpty(formLink), nullIfEmpty(fileLink), nullIfEmpty(notes),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "documentId": id})
}

func (a *API) UpdateDocument(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		DocumentTitle *string `json:"documentTitle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.DocumentTitle == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "documentTitle required"})
		return
	}
	title := strings.Join(strings.Fields(strings.TrimSpace(*body.DocumentTitle)), " ")
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "documentTitle required"})
		return
	}
	res, err := a.DB.Exec(`UPDATE DocumentTable SET DocumentTitle = ? WHERE DocumentID = ?`, title, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var fileLink sql.NullString
	err = a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&fileLink)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if _, err := a.DB.Exec(`DELETE FROM DocumentTable WHERE DocumentID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if fileLink.Valid && fileLink.String != "" && !strings.HasPrefix(fileLink.String, "placeholder:") {
		_ = os.Remove(fileLink.String)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DocumentFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var fileLink sql.NullString
	err = a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, id).Scan(&fileLink)
	if err == sql.ErrNoRows || !fileLink.Valid || fileLink.String == "" {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	// Reference documents are meant to be viewed in the browser, not saved
	// locally — inline hints the browser to render rather than prompt a
	// download (it can't force this for formats the browser has no native
	// viewer for, e.g. .docx/.xlsx, which still fall back to a download).
	// no-store keeps browsers from reusing a stale copy of this response
	// (with a stale filename) from before this header existed.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", contentDisposition(filepath.Base(fileLink.String)))
	http.ServeFile(w, r, fileLink.String)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
