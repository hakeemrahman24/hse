package activitylog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/middleware"
	"hse-medpost/internal/storage"
)

var activityTypes = map[string]bool{
	"Monitoring/Inspection": true,
	"Socialization":         true,
}

var evidenceTypes = map[string]bool{
	"AttendanceList":   true,
	"ReportAttachment": true,
	"Photos":           true,
}

func (a *API) CreateActivity(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	procID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		// allow JSON fallback
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
			return
		}
		a.createActivityJSON(w, r, procID, sess.StaffID)
		return
	}

	atype := strings.TrimSpace(r.FormValue("activityType"))
	title := strings.TrimSpace(r.FormValue("activityTitle"))
	summary := strings.TrimSpace(r.FormValue("activitySummary"))
	location := strings.TrimSpace(r.FormValue("location"))
	date := strings.TrimSpace(r.FormValue("activityDate"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if !activityTypes[atype] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityType must be Monitoring/Inspection or Socialization"})
		return
	}
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityTitle required"})
		return
	}
	date, err = dates.Normalize(date)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if date == "" {
		date = dates.Today()
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ? AND Status = 'Active'`, procID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO ActivityTable
			(FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivitySummary, Location, ActivityDate, Notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		procID, sess.StaffID, atype, title, nullIfEmpty(summary), nullIfEmpty(location), date, nullIfEmpty(notes),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	activityID, _ := res.LastInsertId()
	today := dates.Today()
	program, err := a.procedureName(procID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// photos[]
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
			saved, err := a.saveEvidence(checked, hdr.Filename, program, atype, "Photos", date, activityID, sess.StaffID, i+1)
			f.Close()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "photo upload failed"})
				return
			}
			if _, err := tx.Exec(`
				INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
				VALUES (?, ?, 'Photos', ?, ?)`, activityID, sess.StaffID, saved, today); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
		}
	}

	// optional single attendance / report attachments
	seq := 1
	for _, pair := range []struct {
		field string
		etype string
	}{
		{"attendance", "AttendanceList"},
		{"report", "ReportAttachment"},
	} {
		file, hdr, err := r.FormFile(pair.field)
		if err == http.ErrMissingFile {
			continue
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad " + pair.field})
			return
		}
		checked, err := storage.RequirePDF(file)
		if err != nil {
			file.Close()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		saved, err := a.saveEvidence(checked, hdr.Filename, program, atype, pair.etype, date, activityID, sess.StaffID, seq)
		file.Close()
		seq++
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": pair.field + " upload failed"})
			return
		}
		if _, err := tx.Exec(`
			INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
			VALUES (?, ?, ?, ?, ?)`, activityID, sess.StaffID, pair.etype, saved, today); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "activityId": activityID})
}

func (a *API) createActivityJSON(w http.ResponseWriter, r *http.Request, procID, staffID int64) {
	var body struct {
		ActivityType    string `json:"activityType"`
		ActivityTitle   string `json:"activityTitle"`
		ActivitySummary string `json:"activitySummary"`
		Location        string `json:"location"`
		ActivityDate    string `json:"activityDate"`
		Notes           string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.ActivityType = strings.TrimSpace(body.ActivityType)
	body.ActivityTitle = strings.TrimSpace(body.ActivityTitle)
	body.ActivitySummary = strings.TrimSpace(body.ActivitySummary)
	body.Location = strings.TrimSpace(body.Location)
	body.Notes = strings.TrimSpace(body.Notes)

	activityDate, err := dates.Normalize(body.ActivityDate)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if activityDate == "" {
		activityDate = dates.Today()
	}

	if !activityTypes[body.ActivityType] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityType must be Monitoring/Inspection or Socialization"})
		return
	}
	if body.ActivityTitle == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityTitle required"})
		return
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ? AND Status = 'Active'`, procID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
		return
	}

	res, err := a.DB.Exec(`
		INSERT INTO ActivityTable
			(FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivitySummary, Location, ActivityDate, Notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		procID, staffID, body.ActivityType, body.ActivityTitle,
		nullIfEmpty(body.ActivitySummary), nullIfEmpty(body.Location), activityDate, nullIfEmpty(body.Notes),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "activityId": id})
}

func (a *API) AddEvidence(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	activityID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	etype := strings.TrimSpace(r.FormValue("evidenceType"))
	if !evidenceTypes[etype] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad evidenceType"})
		return
	}
	var owner int64
	var atype, activityDate, program string
	err = a.DB.QueryRow(`
		SELECT a.FK_StaffID, a.ActivityType, COALESCE(a.ActivityDate,''), p.HealthProcedure
		FROM ActivityTable a
		JOIN HealthProcedureTable p ON p.ProcID = a.FK_ProcID
		WHERE a.ActivityID = ?`, activityID).Scan(&owner, &atype, &activityDate, &program)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "activity not found"})
		return
	}
	if sess.Role != "admin" && sess.StaffID != owner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file required"})
		return
	}
	defer file.Close()
	date := activityDate
	if date == "" {
		date = dates.Today()
	}
	saved, err := a.saveEvidence(file, hdr.Filename, program, atype, etype, date, activityID, sess.StaffID, 1)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	today := dates.Today()
	res, err := a.DB.Exec(`
		INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
		VALUES (?, ?, ?, ?, ?)`, activityID, sess.StaffID, etype, saved, today)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	eid, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "evidenceId": eid})
}

func (a *API) activityOwner(activityID int64) (int64, error) {
	var owner int64
	err := a.DB.QueryRow(`SELECT FK_StaffID FROM ActivityTable WHERE ActivityID = ?`, activityID).Scan(&owner)
	return owner, err
}

// UpdateActivity lets the activity's own logger, or an admin, edit its
// fields — same ownership rule UpdateSubHeaderEntry uses for entries.
func (a *API) UpdateActivity(w http.ResponseWriter, r *http.Request) {
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

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		a.updateActivityForm(w, r, id, sess.StaffID)
		return
	}

	var body struct {
		ProcID          *int64  `json:"procId"`
		ActivityType    *string `json:"activityType"`
		ActivityTitle   *string `json:"activityTitle"`
		ActivitySummary *string `json:"activitySummary"`
		Location        *string `json:"location"`
		ActivityDate    *string `json:"activityDate"`
		Notes           *string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	// Fetch the current program/type before any field changes below, so a
	// procId and/or activityType change in the same request can be compared
	// against what evidence files were actually saved under.
	oldProcID, oldProgram, oldType, err := a.activityContext(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	newProcID, newProgram := oldProcID, oldProgram
	if body.ProcID != nil && *body.ProcID != oldProcID {
		if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ? AND Status = 'Active'`, *body.ProcID).Scan(new(int)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
			return
		}
		newProcID = *body.ProcID
		newProgram, err = a.procedureName(newProcID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET FK_ProcID = ? WHERE ActivityID = ?`, newProcID, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}

	newType := oldType
	if body.ActivityType != nil {
		t := strings.TrimSpace(*body.ActivityType)
		if !activityTypes[t] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityType must be Monitoring/Inspection or Socialization"})
			return
		}
		newType = t
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivityType = ? WHERE ActivityID = ?`, t, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.ActivityTitle != nil {
		t := strings.TrimSpace(*body.ActivityTitle)
		if t == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityTitle required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivityTitle = ? WHERE ActivityID = ?`, t, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.ActivitySummary != nil {
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivitySummary = ? WHERE ActivityID = ?`,
			nullIfEmpty(strings.TrimSpace(*body.ActivitySummary)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Location != nil {
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET Location = ? WHERE ActivityID = ?`,
			nullIfEmpty(strings.TrimSpace(*body.Location)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.ActivityDate != nil {
		d, err := dates.Require(*body.ActivityDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET ActivityDate = ? WHERE ActivityID = ?`, d, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Notes != nil {
		if _, err := a.DB.Exec(`UPDATE ActivityTable SET Notes = ? WHERE ActivityID = ?`,
			nullIfEmpty(strings.TrimSpace(*body.Notes)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if newProgram != oldProgram || newType != oldType {
		if err := a.relocateEvidence(a.DB, id, oldProgram, oldType, newProgram, newType); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// updateActivityForm applies the same fields as the new-activity form, and
// appends any new photos, attendance list, or report. Existing evidence stays.
func (a *API) updateActivityForm(w http.ResponseWriter, r *http.Request, id, staffID int64) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid form"})
		return
	}
	atype := strings.TrimSpace(r.FormValue("activityType"))
	title := strings.TrimSpace(r.FormValue("activityTitle"))
	summary := strings.TrimSpace(r.FormValue("activitySummary"))
	location := strings.TrimSpace(r.FormValue("location"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	date, err := dates.Require(r.FormValue("activityDate"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !activityTypes[atype] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityType must be Monitoring/Inspection or Socialization"})
		return
	}
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "activityTitle required"})
		return
	}

	oldProcID, oldProgram, oldType, err := a.activityContext(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// An empty procId leaves the activity under its current program — the
	// edit form always sends one (it's a required <select>), but the field
	// stays optional here for any other multipart caller.
	newProcID, newProgram := oldProcID, oldProgram
	if raw := strings.TrimSpace(r.FormValue("procId")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad procId"})
			return
		}
		if parsed != oldProcID {
			if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ? AND Status = 'Active'`, parsed).Scan(new(int)); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
				return
			}
			newProcID = parsed
			newProgram, err = a.procedureName(newProcID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
		}
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE ActivityTable
		SET FK_ProcID = ?, ActivityType = ?, ActivityTitle = ?, ActivitySummary = ?, Location = ?, ActivityDate = ?, Notes = ?
		WHERE ActivityID = ?`,
		newProcID, atype, title, nullIfEmpty(summary), nullIfEmpty(location), date, nullIfEmpty(notes), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	// Existing evidence follows the activity into its new program/type
	// folder before any newly-uploaded files below are saved directly
	// under the (already current) destination.
	if newProgram != oldProgram || atype != oldType {
		if err := a.relocateEvidence(tx, id, oldProgram, oldType, newProgram, atype); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if err := a.appendFormEvidence(tx, r, newProgram, atype, date, id, staffID); err != nil {
		status, msg := http.StatusInternalServerError, "upload failed"
		var fe formError
		if errors.As(err, &fe) {
			status, msg = fe.status, fe.msg
		}
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type formError struct {
	status int
	msg    string
}

func (e formError) Error() string { return e.msg }

func (a *API) appendFormEvidence(tx *sql.Tx, r *http.Request, program, atype, date string, activityID, staffID int64) error {
	today := dates.Today()
	if r.MultipartForm != nil {
		photoSeq, err := evidenceCount(tx, activityID, "Photos")
		if err != nil {
			return formError{http.StatusInternalServerError, "db error"}
		}
		for _, hdr := range r.MultipartForm.File["photos"] {
			if hdr.Size == 0 {
				continue
			}
			photoSeq++
			f, err := hdr.Open()
			if err != nil {
				return formError{http.StatusBadRequest, "bad photo"}
			}
			checked, err := storage.RequireImage(f)
			if err != nil {
				f.Close()
				return formError{http.StatusBadRequest, err.Error()}
			}
			saved, err := a.saveEvidence(checked, hdr.Filename, program, atype, "Photos", date, activityID, staffID, photoSeq)
			f.Close()
			if err != nil {
				return formError{http.StatusInternalServerError, "photo upload failed"}
			}
			if _, err := tx.Exec(`
				INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
				VALUES (?, ?, 'Photos', ?, ?)`, activityID, staffID, saved, today); err != nil {
				return formError{http.StatusInternalServerError, "db error"}
			}
		}
	}

	docSeq, err := evidenceCount(tx, activityID, "AttendanceList")
	if err != nil {
		return formError{http.StatusInternalServerError, "db error"}
	}
	reportSeq, err := evidenceCount(tx, activityID, "ReportAttachment")
	if err != nil {
		return formError{http.StatusInternalServerError, "db error"}
	}
	for _, pair := range []struct {
		field string
		etype string
		seq   *int
	}{
		{"attendance", "AttendanceList", &docSeq},
		{"report", "ReportAttachment", &reportSeq},
	} {
		file, hdr, err := r.FormFile(pair.field)
		if err == http.ErrMissingFile {
			continue
		}
		if err != nil {
			return formError{http.StatusBadRequest, "bad " + pair.field}
		}
		if hdr.Size == 0 {
			file.Close()
			continue
		}
		checked, err := storage.RequirePDF(file)
		if err != nil {
			file.Close()
			return formError{http.StatusBadRequest, err.Error()}
		}
		*pair.seq++
		saved, err := a.saveEvidence(checked, hdr.Filename, program, atype, pair.etype, date, activityID, staffID, *pair.seq)
		file.Close()
		if err != nil {
			return formError{http.StatusInternalServerError, pair.field + " upload failed"}
		}
		if _, err := tx.Exec(`
			INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
			VALUES (?, ?, ?, ?, ?)`, activityID, staffID, pair.etype, saved, today); err != nil {
			return formError{http.StatusInternalServerError, "db error"}
		}
	}
	return nil
}

func evidenceCount(tx *sql.Tx, activityID int64, etype string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM EvidenceTable WHERE FK_IDActivity = ? AND EvidenceType = ?`, activityID, etype).Scan(&n)
	return n, err
}

// DeleteActivity removes an activity and its evidence rows/files. Allowed
// for the activity's own logger, or an admin — same ownership rule as edit.
func (a *API) DeleteActivity(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	activityID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	owner, err := a.activityOwner(activityID)
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

	links, err := a.evidenceFileLinks(activityID)
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

	if _, err := tx.Exec(`DELETE FROM EvidenceTable WHERE FK_IDActivity = ?`, activityID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	res, err := tx.Exec(`DELETE FROM ActivityTable WHERE ActivityID = ?`, activityID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := tx.Commit(); err != nil {
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

// DeleteEvidence removes one photo or document. The activity's logger, or an
// admin, may delete it — the same ownership rule as editing the activity.
func (a *API) DeleteEvidence(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	evidenceID, err := strconv.ParseInt(r.PathValue("evidenceId"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var activityID int64
	var link string
	err = a.DB.QueryRow(`SELECT FK_IDActivity, COALESCE(FileLink,'') FROM EvidenceTable WHERE EvidenceID = ?`, evidenceID).Scan(&activityID, &link)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	owner, err := a.activityOwner(activityID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if sess.Role != "admin" && sess.StaffID != owner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM EvidenceTable WHERE EvidenceID = ?`, evidenceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if link != "" && !strings.HasPrefix(link, "placeholder:") {
		_ = os.Remove(link)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) evidenceFileLinks(activityID int64) ([]string, error) {
	rows, err := a.DB.Query(`SELECT COALESCE(FileLink,'') FROM EvidenceTable WHERE FK_IDActivity = ?`, activityID)
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

func (a *API) procedureName(procID int64) (string, error) {
	var name string
	err := a.DB.QueryRow(`SELECT HealthProcedure FROM HealthProcedureTable WHERE ProcID = ?`, procID).Scan(&name)
	return name, err
}

// activityContext fetches what a program/type reassignment needs to compare
// before vs. after: the activity's current program (both ID and name, since
// evidence folders are keyed on the name) and its ActivityType.
func (a *API) activityContext(activityID int64) (procID int64, program, activityType string, err error) {
	err = a.DB.QueryRow(`
		SELECT act.FK_ProcID, p.HealthProcedure, act.ActivityType
		FROM ActivityTable act
		JOIN HealthProcedureTable p ON p.ProcID = act.FK_ProcID
		WHERE act.ActivityID = ?`, activityID,
	).Scan(&procID, &program, &activityType)
	return procID, program, activityType, err
}

// execer is satisfied by both *sql.DB and *sql.Tx, so relocateEvidence can
// run either inside an existing transaction (updateActivityForm) or directly
// against the pool (UpdateActivity's JSON path, which has no transaction of
// its own).
type execer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

// relocateEvidence moves every evidence file an activity already owns from
// its old program/type folder to the new one, and repoints FileLink at the
// new path — called when CreateActivity's program (or type) is changed after
// the fact, so uploaded photos/documents follow the activity instead of
// being left behind under the program they were first filed under.
// Placeholder evidence (seed-demo photos with no real file) has nothing on
// disk to move and is skipped.
func (a *API) relocateEvidence(db execer, activityID int64, oldProgram, oldType, newProgram, newType string) error {
	rows, err := db.Query(`
		SELECT EvidenceID, EvidenceType, COALESCE(FileLink,'')
		FROM EvidenceTable WHERE FK_IDActivity = ?`, activityID)
	if err != nil {
		return err
	}
	type evidenceFile struct {
		id    int64
		etype string
		link  string
	}
	var files []evidenceFile
	for rows.Next() {
		var f evidenceFile
		if err := rows.Scan(&f.id, &f.etype, &f.link); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, f := range files {
		if f.link == "" || strings.HasPrefix(f.link, "placeholder:") {
			continue
		}
		newDir := storage.EvidenceDir(a.UploadDir, newProgram, newType, f.etype)
		newPath, err := storage.Move(f.link, newDir)
		if err != nil {
			return err
		}
		if newPath == f.link {
			continue
		}
		if _, err := db.Exec(`UPDATE EvidenceTable SET FileLink = ? WHERE EvidenceID = ?`, newPath, f.id); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) saveEvidence(src io.Reader, original, program, activityType, evidenceType, date string, activityID, staffID int64, seq int) (string, error) {
	dir := storage.EvidenceDir(a.UploadDir, program, activityType, evidenceType)
	name := storage.EvidenceFilename(date, activityID, staffID, evidenceType, seq, original)
	stored, err := storage.CompressPDF(src)
	if err != nil {
		return "", err
	}
	defer stored.Close()
	return storage.Save(dir, name, stored)
}
