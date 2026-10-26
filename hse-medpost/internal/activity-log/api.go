package activitylog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

type API struct {
	DB        *sql.DB
	Store     *auth.Store
	UploadDir string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) ListPrograms(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT ProcID, HealthProcedure, Status
		FROM HealthProcedureTable
		WHERE Status = 'Active'
		ORDER BY HealthProcedure`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type prog struct {
		ProcID          int64  `json:"procId"`
		HealthProcedure string `json:"healthProcedure"`
		Title           string `json:"title"`
		Status          string `json:"status"`
	}
	out := []prog{}
	for rows.Next() {
		var p prog
		if err := rows.Scan(&p.ProcID, &p.HealthProcedure, &p.Status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		p.Title = display.ProgramTitle(p.HealthProcedure)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type photo struct {
	EvidenceID int64  `json:"evidenceId"`
	URL        string `json:"url"`
	UploadDate string `json:"uploadDate,omitempty"`
	UploadedBy string `json:"uploadedBy,omitempty"`
	FileHash   string `json:"fileHash,omitempty"`
}

type document struct {
	EvidenceID   int64  `json:"evidenceId"`
	URL          string `json:"url"`
	Filename     string `json:"filename"`
	EvidenceType string `json:"evidenceType"`
	IsPDF        bool   `json:"isPdf"`
	UploadDate   string `json:"uploadDate,omitempty"`
	UploadedBy   string `json:"uploadedBy,omitempty"`
	FileHash     string `json:"fileHash,omitempty"`
}

type activityView struct {
	ActivityID      int64      `json:"activityId"`
	ActivityType    string     `json:"activityType"`
	ActivityTitle   string     `json:"activityTitle"`
	ActivitySummary string     `json:"activitySummary"`
	ActivityDate    string     `json:"activityDate"`
	Location        string     `json:"location"`
	Notes           string     `json:"notes"`
	ProcID          int64      `json:"procId,omitempty"`
	ProgramTitle    string     `json:"programTitle,omitempty"`
	UploadedBy      string     `json:"uploadedBy,omitempty"`
	LoggedByStaffID int64      `json:"loggedByStaffId"`
	Photos          []photo    `json:"photos"`
	Documents       []document `json:"documents"`
}

// attachEvidence fills in Photos and Documents for each activity, in one query per activity.
func (a *API) attachEvidence(activities []activityView) error {
	for i := range activities {
		erows, err := a.DB.Query(`
			SELECT e.EvidenceID, e.EvidenceType, COALESCE(e.FileLink,''), COALESCE(e.UploadDate,''), COALESCE(s.StaffName,'')
			FROM EvidenceTable e
			LEFT JOIN MedpostStaffTable s ON s.StaffID = e.FK_StaffID
			WHERE e.FK_IDActivity = ?
			ORDER BY e.EvidenceID`, activities[i].ActivityID)
		if err != nil {
			return err
		}
		for erows.Next() {
			var eid int64
			var etype, link, uploadDate, uploadedBy string
			if err := erows.Scan(&eid, &etype, &link, &uploadDate, &uploadedBy); err != nil {
				erows.Close()
				return err
			}
			url := fmt.Sprintf("/api/uploads/%d", eid)
			if etype != "Photos" {
				url = openFileURL("/api/uploads", eid, link)
			}
			if etype == "Photos" {
				activities[i].Photos = append(activities[i].Photos, photo{
					EvidenceID: eid,
					URL:        url,
					UploadDate: uploadDate,
					UploadedBy: uploadedBy,
				})
				continue
			}
			filename := filepath.Base(link)
			activities[i].Documents = append(activities[i].Documents, document{
				EvidenceID:   eid,
				URL:          url,
				Filename:     filename,
				EvidenceType: etype,
				IsPDF:        strings.HasSuffix(strings.ToLower(filename), ".pdf"),
				UploadDate:   uploadDate,
				UploadedBy:   uploadedBy,
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

func (a *API) ProgramActivitiesView(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	procID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}

	from, err := dates.Normalize(r.URL.Query().Get("from"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from: " + err.Error()})
		return
	}
	to, err := dates.Normalize(r.URL.Query().Get("to"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to: " + err.Error()})
		return
	}
	if from != "" && to != "" && from > to {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be <= to"})
		return
	}

	var name, status string
	err = a.DB.QueryRow(
		`SELECT HealthProcedure, Status FROM HealthProcedureTable WHERE ProcID = ?`, procID,
	).Scan(&name, &status)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	q := `
		SELECT a.ActivityID, a.ActivityType, a.ActivityTitle, COALESCE(a.ActivitySummary,''),
		       COALESCE(a.ActivityDate,''), COALESCE(a.Location,''), COALESCE(a.Notes,''), COALESCE(s.StaffName,''), a.FK_StaffID
		FROM ActivityTable a
		LEFT JOIN MedpostStaffTable s ON s.StaffID = a.FK_StaffID
		WHERE a.FK_ProcID = ? AND a.FK_SubHeaderID IS NULL`
	args := []any{procID}
	if from != "" {
		q += ` AND a.ActivityDate >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND a.ActivityDate <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY a.ActivityType, a.ActivityDate DESC, a.ActivityID DESC`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	activities := []activityView{}
	for rows.Next() {
		var arow activityView
		arow.Photos = []photo{}
		arow.Documents = []document{}
		if err := rows.Scan(&arow.ActivityID, &arow.ActivityType, &arow.ActivityTitle,
			&arow.ActivitySummary, &arow.ActivityDate, &arow.Location, &arow.Notes, &arow.UploadedBy, &arow.LoggedByStaffID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		activities = append(activities, arow)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if err := a.attachEvidence(activities); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"procId":          procID,
		"healthProcedure": name,
		"title":           display.ProgramTitle(name),
		"status":          status,
		"from":            from,
		"to":              to,
		"activities":      activities,
	})
}

// AllActivitiesView is the "Home" feed: every activity across every program,
// most recent first, with the same from/to date filter as the per-program view.
func (a *API) AllActivitiesView(w http.ResponseWriter, r *http.Request) {
	from, err := dates.Normalize(r.URL.Query().Get("from"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from: " + err.Error()})
		return
	}
	to, err := dates.Normalize(r.URL.Query().Get("to"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to: " + err.Error()})
		return
	}
	if from != "" && to != "" && from > to {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be <= to"})
		return
	}

	q := `
		SELECT a.ActivityID, a.ActivityType, a.ActivityTitle, COALESCE(a.ActivitySummary,''),
		       COALESCE(a.ActivityDate,''), COALESCE(a.Location,''), COALESCE(a.Notes,''),
		       a.FK_ProcID, COALESCE(p.HealthProcedure,''), COALESCE(s.StaffName,''), a.FK_StaffID
		FROM ActivityTable a
		JOIN HealthProcedureTable p ON p.ProcID = a.FK_ProcID
		LEFT JOIN MedpostStaffTable s ON s.StaffID = a.FK_StaffID
		WHERE a.FK_SubHeaderID IS NULL`
	args := []any{}
	if from != "" {
		q += ` AND a.ActivityDate >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND a.ActivityDate <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY a.ActivityDate DESC, a.ActivityID DESC`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	activities := []activityView{}
	for rows.Next() {
		var arow activityView
		var healthProcedure string
		arow.Photos = []photo{}
		arow.Documents = []document{}
		if err := rows.Scan(&arow.ActivityID, &arow.ActivityType, &arow.ActivityTitle,
			&arow.ActivitySummary, &arow.ActivityDate, &arow.Location, &arow.Notes,
			&arow.ProcID, &healthProcedure, &arow.UploadedBy, &arow.LoggedByStaffID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		arow.ProgramTitle = display.ProgramTitle(healthProcedure)
		activities = append(activities, arow)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if err := a.attachEvidence(activities); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from":       from,
		"to":         to,
		"activities": activities,
	})
}

func (a *API) Upload(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("evidenceId")
	eid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var link, etype string
	err = a.DB.QueryRow(
		`SELECT COALESCE(FileLink,''), EvidenceType FROM EvidenceTable WHERE EvidenceID = ?`, eid,
	).Scan(&link, &etype)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	// Demo placeholders until real files are uploaded.
	if strings.HasPrefix(link, "placeholder:") {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300" viewBox="0 0 400 300">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0%%" stop-color="#1a5f4a"/><stop offset="1%%" stop-color="#0d2f28"/>
  </linearGradient></defs>
  <rect width="400" height="300" fill="url(#g)"/>
  <text x="200" y="150" text-anchor="middle" fill="#c8e6d8" font-family="system-ui" font-size="18">Photo %d</text>
</svg>`, eid)
		return
	}

	// no-store keeps browsers from reusing a stale copy of this response
	// (with a stale filename) from before this header existed.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", contentDisposition(filepath.Base(link)))
	http.ServeFile(w, r, link)
}
