package activitylog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/middleware"
	"hse-medpost/internal/storage"
)

type subHeader struct {
	SubHeaderID int64  `json:"subHeaderId"`
	ProcID      int64  `json:"procId"`
	Name        string `json:"subHeaderName"`
	Status      string `json:"status"`
	AuditLocked bool   `json:"auditLocked"`
}

// ListSubHeaders returns a program's sub-headers. Everyone sees Active ones;
// admin also sees Archived, same split as AdminListPrograms vs ListPrograms.
func (a *API) ListSubHeaders(w http.ResponseWriter, r *http.Request) {
	procID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	sess := middleware.Session(r)
	q := `SELECT SubHeaderID, FK_ProcID, SubHeaderName, Status, AuditLocked FROM SubHeaderTable WHERE FK_ProcID = ?`
	args := []any{procID}
	if sess.Role != "admin" {
		q += ` AND Status = 'Active'`
	}
	q += ` ORDER BY SubHeaderName`
	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []subHeader{}
	for rows.Next() {
		var s subHeader
		if err := rows.Scan(&s.SubHeaderID, &s.ProcID, &s.Name, &s.Status, &s.AuditLocked); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateSubHeader(w http.ResponseWriter, r *http.Request) {
	procID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Name        string `json:"subHeaderName"`
		AuditLocked bool   `json:"auditLocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	name := strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subHeaderName required"})
		return
	}
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ?`, procID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
		return
	}
	res, err := a.DB.Exec(
		`INSERT INTO SubHeaderTable (FK_ProcID, SubHeaderName, Status, AuditLocked) VALUES (?, ?, 'Active', ?)`,
		procID, name, body.AuditLocked,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "subHeaderId": id, "subHeaderName": name})
}

func (a *API) UpdateSubHeader(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Name        *string `json:"subHeaderName"`
		Status      *string `json:"status"`
		AuditLocked *bool   `json:"auditLocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.AuditLocked != nil {
		if !*body.AuditLocked {
			// Unlocking is only safe while the sub-header has no entries yet —
			// otherwise lock→edit→relock would defeat the whole point of
			// AuditLocked (see its doc comment in migrate.go).
			var wasLocked bool
			var entries int
			if err := a.DB.QueryRow(`SELECT AuditLocked FROM SubHeaderTable WHERE SubHeaderID = ?`, id).Scan(&wasLocked); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sub-header not found"})
				return
			}
			_ = a.DB.QueryRow(`SELECT COUNT(*) FROM ActivityTable WHERE FK_SubHeaderID = ?`, id).Scan(&entries)
			if wasLocked && entries > 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot unlock an audit-locked sub-header that already has entries"})
				return
			}
		}
		if _, err := a.DB.Exec(`UPDATE SubHeaderTable SET AuditLocked = ? WHERE SubHeaderID = ?`, *body.AuditLocked, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Name != nil {
		name := strings.Join(strings.Fields(strings.TrimSpace(*body.Name)), " ")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subHeaderName required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE SubHeaderTable SET SubHeaderName = ? WHERE SubHeaderID = ?`, name, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Status != nil {
		st := strings.TrimSpace(*body.Status)
		if st != "Active" && st != "Archived" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be Active or Archived"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE SubHeaderTable SET Status = ? WHERE SubHeaderID = ?`, st, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteSubHeader collects its entries' evidence file paths (to remove from
// disk) before deleting the sub-header — the ActivityTable/EvidenceTable
// rows themselves cascade automatically via FK_SubHeaderID's ON DELETE
// CASCADE (and EvidenceTable's existing cascade from ActivityID), same as
// DeleteProgram relies on FK cascades for the tables that support it.
func (a *API) DeleteSubHeader(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var name string
	var auditLocked bool
	err = a.DB.QueryRow(`SELECT SubHeaderName, AuditLocked FROM SubHeaderTable WHERE SubHeaderID = ?`, id).Scan(&name, &auditLocked)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	links, err := a.subHeaderEvidenceFileLinks(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	var entries int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM ActivityTable WHERE FK_SubHeaderID = ?`, id).Scan(&entries)

	// Deleting a locked sub-header's entries would cascade-remove supposedly
	// immutable records, so block it outright while any exist (see
	// AuditLocked's doc comment in migrate.go).
	if auditLocked && entries > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete an audit-locked sub-header that has entries"})
		return
	}

	res, err := a.DB.Exec(`DELETE FROM SubHeaderTable WHERE SubHeaderID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete sub-header"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	for _, link := range links {
		if link != "" && !strings.HasPrefix(link, "placeholder:") {
			_ = os.Remove(link)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "subHeaderName": name, "entries": entries})
}

// subHeaderEntry is ActivityTable's shape reprojected under the field names
// the sub-header UI already expects (entryId/entryDate/title/description) —
// EntryID is literally that row's ActivityID.
type subHeaderEntry struct {
	EntryID         int64      `json:"entryId"`
	SubHeaderID     int64      `json:"subHeaderId"`
	LoggedByStaffID int64      `json:"loggedByStaffId"`
	EntryDate       string     `json:"entryDate"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	FormLink        string     `json:"formLink"`
	StaffName       string     `json:"staffName"`
	Photos          []photo    `json:"photos"`
	Documents       []document `json:"documents"`
	Locked          bool       `json:"locked"`
	Code            string     `json:"code,omitempty"`
	SubmissionID    *int64     `json:"submissionId,omitempty"`
}

func (a *API) ListSubHeaderEntries(w http.ResponseWriter, r *http.Request) {
	subHeaderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	rows, err := a.DB.Query(`
		SELECT act.ActivityID, act.FK_SubHeaderID, act.FK_StaffID, COALESCE(act.ActivityDate,''),
		       act.ActivityTitle, COALESCE(act.ActivitySummary,''), COALESCE(act.FormLink,''), COALESCE(s.StaffName,''),
		       sh.AuditLocked, sh.SubHeaderName, act.FK_SubmissionID
		FROM ActivityTable act
		LEFT JOIN MedpostStaffTable s ON s.StaffID = act.FK_StaffID
		JOIN SubHeaderTable sh ON sh.SubHeaderID = act.FK_SubHeaderID
		WHERE act.FK_SubHeaderID = ?
		ORDER BY act.ActivityDate DESC, act.ActivityID DESC`, subHeaderID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []subHeaderEntry{}
	for rows.Next() {
		var e subHeaderEntry
		var subHeaderName string
		var submissionID sql.NullInt64
		if err := rows.Scan(&e.EntryID, &e.SubHeaderID, &e.LoggedByStaffID, &e.EntryDate, &e.Title,
			&e.Description, &e.FormLink, &e.StaffName, &e.Locked, &subHeaderName, &submissionID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if e.Locked {
			e.Code = documentCode(subHeaderName, e.EntryID)
		}
		if submissionID.Valid {
			e.SubmissionID = &submissionID.Int64
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := a.attachEntryEvidence(out); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// attachEntryEvidence fills in Photos and Documents for each entry from
// EvidenceTable (EntryID is that row's ActivityID) — same query shape as
// attachEvidence for regular activities, just a different struct/URL prefix
// to keep, since Go doesn't make sharing this trivial across two field-
// incompatible result types without more abstraction than it's worth here.
func (a *API) attachEntryEvidence(entries []subHeaderEntry) error {
	for i := range entries {
		erows, err := a.DB.Query(`
			SELECT e.EvidenceID, e.EvidenceType, COALESCE(e.FileLink,''), COALESCE(e.UploadDate,''), COALESCE(s.StaffName,''), COALESCE(e.FileHash,'')
			FROM EvidenceTable e
			LEFT JOIN MedpostStaffTable s ON s.StaffID = e.FK_StaffID
			WHERE e.FK_IDActivity = ?
			ORDER BY e.EvidenceID`, entries[i].EntryID)
		if err != nil {
			return err
		}
		for erows.Next() {
			var eid int64
			var etype, link, uploadDate, uploadedBy, fileHash string
			if err := erows.Scan(&eid, &etype, &link, &uploadDate, &uploadedBy, &fileHash); err != nil {
				erows.Close()
				return err
			}
			url := fmt.Sprintf("/api/uploads/%d", eid)
			if etype != "Photos" {
				url = openFileURL("/api/uploads", eid, link)
			}
			if etype == "Photos" {
				entries[i].Photos = append(entries[i].Photos, photo{
					EvidenceID: eid,
					URL:        url,
					UploadDate: uploadDate,
					UploadedBy: uploadedBy,
					FileHash:   fileHash,
				})
				continue
			}
			filename := filepath.Base(link)
			entries[i].Documents = append(entries[i].Documents, document{
				EvidenceID:   eid,
				URL:          url,
				Filename:     filename,
				EvidenceType: etype,
				IsPDF:        strings.HasSuffix(strings.ToLower(filename), ".pdf"),
				UploadDate:   uploadDate,
				UploadedBy:   uploadedBy,
				FileHash:     fileHash,
			})
		}
		if err := erows.Err(); err != nil {
			erows.Close()
			return err
		}
		erows.Close()
	}
	return nil
}

// subHeaderMediaItem is one row of the program-level aggregated gallery
// across every sub-header's entries (GET /api/programs/{id}/subheader-media).
type subHeaderMediaItem struct {
	EvidenceID    int64  `json:"evidenceId"`
	URL           string `json:"url"`
	EvidenceType  string `json:"evidenceType"`
	IsPDF         bool   `json:"isPdf"`
	UploadDate    string `json:"uploadDate,omitempty"`
	UploadedBy    string `json:"uploadedBy,omitempty"`
	SubHeaderID   int64  `json:"subHeaderId"`
	SubHeaderName string `json:"subHeaderName"`
	EntryID       int64  `json:"entryId"`
	EntryTitle    string `json:"entryTitle"`
}

// ListSubHeaderMedia aggregates every photo/document across all of a
// program's sub-headers, most recent first — same idea as the Home feed
// aggregating activities across programs.
func (a *API) ListSubHeaderMedia(w http.ResponseWriter, r *http.Request) {
	procID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	rows, err := a.DB.Query(`
		SELECT e.EvidenceID, e.EvidenceType, COALESCE(e.FileLink,''), COALESCE(e.UploadDate,''),
		       COALESCE(s.StaffName,''), sh.SubHeaderID, sh.SubHeaderName, act.ActivityID, act.ActivityTitle
		FROM EvidenceTable e
		JOIN ActivityTable act ON act.ActivityID = e.FK_IDActivity
		JOIN SubHeaderTable sh ON sh.SubHeaderID = act.FK_SubHeaderID
		LEFT JOIN MedpostStaffTable s ON s.StaffID = e.FK_StaffID
		WHERE sh.FK_ProcID = ?
		ORDER BY e.EvidenceID DESC`, procID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []subHeaderMediaItem{}
	for rows.Next() {
		var m subHeaderMediaItem
		var link string
		if err := rows.Scan(&m.EvidenceID, &m.EvidenceType, &link, &m.UploadDate, &m.UploadedBy,
			&m.SubHeaderID, &m.SubHeaderName, &m.EntryID, &m.EntryTitle); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		m.URL = fmt.Sprintf("/api/uploads/%d", m.EvidenceID)
		if m.EvidenceType != "Photos" {
			m.URL = openFileURL("/api/uploads", m.EvidenceID, link)
			m.IsPDF = strings.HasSuffix(strings.ToLower(link), ".pdf")
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// entrySubHeaderActivityType is the fixed ActivityType backfilled onto every
// sub-header entry — entries never had a type concept of their own (unlike
// top-level activities' Monitoring/Inspection vs Socialization split), and
// ActivityTable's ActivityType column is NOT NULL with a CHECK, so a value
// is required. Chosen once here rather than exposed to the sub-header entry
// UI, which never asked for one.
const entrySubHeaderActivityType = "Monitoring/Inspection"

func (a *API) CreateSubHeaderEntry(w http.ResponseWriter, r *http.Request) {
	subHeaderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	sess := middleware.Session(r)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		a.createSubHeaderEntryMultipart(w, r, subHeaderID, sess.StaffID)
		return
	}

	var body struct {
		EntryDate    string `json:"entryDate"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		FormLink     string `json:"formLink"`
		SubmissionID string `json:"submissionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	entryDate, title, err := normalizeEntryFields(body.EntryDate, body.Title)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	submissionID, err := a.normalizeSubmissionID(body.SubmissionID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	procID, err := a.subHeaderProcID(subHeaderID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sub-header not found"})
		return
	}
	res, err := a.DB.Exec(`
		INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, FK_SubHeaderID, ActivityType, ActivityTitle, ActivitySummary, ActivityDate, FormLink, FK_SubmissionID)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		procID, sess.StaffID, subHeaderID, entrySubHeaderActivityType, title,
		nullIfEmpty(strings.TrimSpace(body.Description)), entryDate, nullIfEmpty(strings.TrimSpace(body.FormLink)), submissionID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "entryId": id})
}

// createSubHeaderEntryMultipart supports attaching photos (multiple) and/or
// one document at creation time, same shape as CreateActivity's initial
// evidence handling. More can be added later via AddSubHeaderEntryEvidence.
func (a *API) createSubHeaderEntryMultipart(w http.ResponseWriter, r *http.Request, subHeaderID, staffID int64) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	entryDate, title, err := normalizeEntryFields(r.FormValue("entryDate"), r.FormValue("title"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	description := strings.TrimSpace(r.FormValue("description"))
	formLink := strings.TrimSpace(r.FormValue("formLink"))

	submissionID, err := a.normalizeSubmissionID(r.FormValue("submissionId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	procID, err := a.subHeaderProcID(subHeaderID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sub-header not found"})
		return
	}
	program, err := a.procedureName(procID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, FK_SubHeaderID, ActivityType, ActivityTitle, ActivitySummary, ActivityDate, FormLink, FK_SubmissionID)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		procID, staffID, subHeaderID, entrySubHeaderActivityType, title,
		nullIfEmpty(description), entryDate, nullIfEmpty(formLink), submissionID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	entryID, _ := res.LastInsertId()
	today := dates.Today()

	if files := r.MultipartForm.File["photos"]; len(files) > 0 {
		for i, hdr := range files {
			f, err := hdr.Open()
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad photo"})
				return
			}
			checked, err := storage.RequireImage(f)
			if err != nil {
				f.Close()
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			saved, hash, err := a.saveEvidenceWithHash(checked, hdr.Filename, program, entrySubHeaderActivityType, "Photos", entryDate, entryID, staffID, i+1)
			f.Close()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "photo upload failed"})
				return
			}
			if _, err := tx.Exec(`
				INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate, FileHash)
				VALUES (?, ?, 'Photos', ?, ?, ?)`, entryID, staffID, saved, today, hash); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
		}
	}

	// 'Document' has no EvidenceTable CHECK entry of its own — it shares
	// 'ReportAttachment', the existing generic-attachment bucket.
	if file, hdr, ferr := r.FormFile("document"); ferr == nil {
		saved, hash, err := a.saveEvidenceWithHash(file, hdr.Filename, program, entrySubHeaderActivityType, "ReportAttachment", entryDate, entryID, staffID, 1)
		file.Close()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "document upload failed"})
			return
		}
		if _, err := tx.Exec(`
			INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate, FileHash)
			VALUES (?, ?, 'ReportAttachment', ?, ?, ?)`, entryID, staffID, saved, today, hash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	} else if ferr != http.ErrMissingFile {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad document"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "entryId": entryID})
}

// UpdateSubHeaderEntry lets the entry's own logger, or an admin, edit its
// fields — same ownership rule AddEvidence already uses for activities.
func (a *API) UpdateSubHeaderEntry(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	owner, err := a.activityOwner(id)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if sess.Role != "admin" && sess.StaffID != owner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if locked, err := a.subHeaderLocked(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	} else if locked {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "this entry is audit-locked and cannot be edited"})
		return
	}

	var body struct {
		EntryDate   *string `json:"entryDate"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		FormLink    *string `json:"formLink"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.EntryDate != nil {
		d, err := dates.Require(*body.EntryDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivityDate = ? WHERE ActivityID = ?`, d, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Title != nil {
		t := strings.TrimSpace(*body.Title)
		if t == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivityTitle = ? WHERE ActivityID = ?`, t, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Description != nil {
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivitySummary = ? WHERE ActivityID = ?`,
			nullIfEmpty(strings.TrimSpace(*body.Description)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.FormLink != nil {
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET FormLink = ? WHERE ActivityID = ?`,
			nullIfEmpty(strings.TrimSpace(*body.FormLink)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AddSubHeaderEntryEvidence adds one more photo or document to an existing
// entry — same shape and ownership rule as AddEvidence for activities.
func (a *API) AddSubHeaderEntryEvidence(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	entryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	etype := strings.TrimSpace(r.FormValue("evidenceType"))
	if etype != "Photos" && etype != "Document" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "evidenceType must be Photos or Document"})
		return
	}
	// 'Document' shares EvidenceTable's 'ReportAttachment' bucket — see
	// createSubHeaderEntryMultipart's comment.
	storedType := etype
	if storedType == "Document" {
		storedType = "ReportAttachment"
	}

	owner, program, activityType, activityDate, err := a.entryContext(entryID)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if sess.Role != "admin" && sess.StaffID != owner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if locked, err := a.subHeaderLocked(entryID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	} else if locked {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "this entry is audit-locked and cannot be changed"})
		return
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file required"})
		return
	}
	defer file.Close()
	var checked io.Reader = file
	if etype == "Photos" {
		checked, err = storage.RequireImage(file)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	date := activityDate
	if date == "" {
		date = dates.Today()
	}
	saved, hash, err := a.saveEvidenceWithHash(checked, hdr.Filename, program, activityType, storedType, date, entryID, sess.StaffID, 1)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	res, err := a.DB.Exec(`
		INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate, FileHash)
		VALUES (?, ?, ?, ?, ?, ?)`, entryID, sess.StaffID, storedType, saved, dates.Today(), hash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	eid, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "evidenceId": eid})
}

// DeleteSubHeaderEntry removes an entry and its evidence files (if any).
// Allowed for the entry's own logger, or an admin — same ownership rule
// UpdateSubHeaderEntry/AddSubHeaderEntryEvidence already use for this entry.
func (a *API) DeleteSubHeaderEntry(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	owner, err := a.activityOwner(id)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if sess.Role != "admin" && sess.StaffID != owner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if locked, err := a.subHeaderLocked(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	} else if locked {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "this entry is audit-locked and cannot be deleted"})
		return
	}

	links, err := a.evidenceFileLinks(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if _, err := a.DB.Exec(`DELETE FROM ActivityTable WHERE ActivityID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	for _, link := range links {
		if link != "" && !strings.HasPrefix(link, "placeholder:") {
			_ = os.Remove(link)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func normalizeEntryFields(rawDate, rawTitle string) (entryDate, title string, err error) {
	title = strings.TrimSpace(rawTitle)
	if title == "" {
		return "", "", fmt.Errorf("title required")
	}
	entryDate, err = dates.Normalize(rawDate)
	if err != nil {
		return "", "", err
	}
	if entryDate == "" {
		entryDate = dates.Today()
	}
	return entryDate, title, nil
}

func (a *API) subHeaderProcID(subHeaderID int64) (int64, error) {
	var procID int64
	err := a.DB.QueryRow(`SELECT FK_ProcID FROM SubHeaderTable WHERE SubHeaderID = ?`, subHeaderID).Scan(&procID)
	return procID, err
}

// documentCode derives a stable, human-readable code for a locked
// sub-header's entry from the sub-header's own name plus the entry's own
// ActivityID, e.g. "Reasonable Suspicion Checklist" + 7 -> "RSC-000007".
// Nothing is stored for this — it's recomputed on every read from data that
// already exists, so there's no separate sequence to keep in sync.
func documentCode(subHeaderName string, entryID int64) string {
	var b strings.Builder
	for _, word := range strings.Fields(subHeaderName) {
		for _, r := range word {
			if unicode.IsLetter(r) {
				b.WriteRune(unicode.ToUpper(r))
				break
			}
		}
		if b.Len() >= 3 {
			break
		}
	}
	abbrev := b.String()
	if abbrev == "" {
		abbrev = "DOC"
	}
	return fmt.Sprintf("%s-%06d", abbrev, entryID)
}

// subHeaderLocked reports whether the given sub-header entry's parent
// sub-header is AuditLocked — callers that mutate or remove an entry must
// check this first and refuse when true (see AuditLocked's doc comment in
// migrate.go for why).
func (a *API) subHeaderLocked(entryID int64) (bool, error) {
	var locked bool
	err := a.DB.QueryRow(`
		SELECT sh.AuditLocked
		FROM ActivityTable act
		JOIN SubHeaderTable sh ON sh.SubHeaderID = act.FK_SubHeaderID
		WHERE act.ActivityID = ?`, entryID).Scan(&locked)
	return locked, err
}

// normalizeSubmissionID validates an optional "link to a test" value from a
// sub-header entry's create form: empty means no link, otherwise it must be
// a real DrugAlcoholScreening row. This is what gives a locked entry (e.g.
// a Consent + Result scan) a formal link to its exact test record instead
// of a human matching donor name/date between the two.
func (a *API) normalizeSubmissionID(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("submissionId must be a number")
	}
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM DrugAlcoholScreening WHERE SubmissionID = ?`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("submission not found")
	}
	return &id, nil
}

// saveEvidenceWithHash is saveEvidence plus a SHA-256 of the exact bytes
// written to disk, computed in the same pass via io.TeeReader so the file
// is only read once. Used for sub-header entry uploads so a later mismatch
// between a stored file and this hash reveals tampering/corruption — see
// EvidenceTable.FileHash's doc comment in migrate.go.
func (a *API) saveEvidenceWithHash(src io.Reader, original, program, activityType, evidenceType, date string, activityID, staffID int64, seq int) (savedPath, hash string, err error) {
	dir := storage.EvidenceDir(a.UploadDir, program, activityType, evidenceType)
	name := storage.EvidenceFilename(date, activityID, staffID, evidenceType, seq, original)
	// Compress first: the hash must cover the bytes that end up on disk.
	stored, err := storage.CompressPDF(src)
	if err != nil {
		return "", "", err
	}
	defer stored.Close()
	h := sha256.New()
	saved, err := storage.Save(dir, name, io.TeeReader(stored, h))
	if err != nil {
		return "", "", err
	}
	return saved, hex.EncodeToString(h.Sum(nil)), nil
}

// entryContext fetches what AddSubHeaderEntryEvidence needs to save a file
// consistently with regular activity evidence: who owns it and the
// program/type/date its storage path is keyed on.
func (a *API) entryContext(entryID int64) (owner int64, program, activityType, activityDate string, err error) {
	err = a.DB.QueryRow(`
		SELECT act.FK_StaffID, p.HealthProcedure, act.ActivityType, COALESCE(act.ActivityDate,'')
		FROM ActivityTable act
		JOIN HealthProcedureTable p ON p.ProcID = act.FK_ProcID
		WHERE act.ActivityID = ? AND act.FK_SubHeaderID IS NOT NULL`, entryID,
	).Scan(&owner, &program, &activityType, &activityDate)
	return owner, program, activityType, activityDate, err
}

func (a *API) subHeaderEvidenceFileLinks(subHeaderID int64) ([]string, error) {
	rows, err := a.DB.Query(`
		SELECT COALESCE(e.FileLink,'')
		FROM EvidenceTable e
		JOIN ActivityTable act ON act.ActivityID = e.FK_IDActivity
		WHERE act.FK_SubHeaderID = ?`, subHeaderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []string
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
