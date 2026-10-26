package directory

import (
	"net/http"
	"strconv"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

const (
	defaultActivityLimit = 200
	maxActivityLimit     = 500
)

// Activity is one health-program activity as other applications may read it. It carries only
// what a viewer needs to see what was done: the program, the kind of activity, its title and
// summary, the date and the place. Notes, the staff member's name, photos and documents stay in
// Medpost.
type Activity struct {
	ActivityID int64  `json:"activityId"`
	Program    string `json:"program"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Date       string `json:"date"`
	Location   string `json:"location"`
	// SubHeaderID is set on entries of a program sub-header (only returned with ?subheaders=1).
	SubHeaderID int64 `json:"subHeaderId,omitempty"`
	// Photos and Proof are filled only when the caller asks with ?files=1.
	Photos []EvidenceFile `json:"photos,omitempty"`
	Proof  []EvidenceFile `json:"proof,omitempty"`
}

// Activities lists program activities, newest first, for read-only display elsewhere (the
// register's Medical Activities page). It accepts the same bearer tokens as the other directory
// routes. ?from= and ?to= (YYYY-MM-DD) bound the activity date; ?limit= caps the rows
// (default 200, at most 500). ?subheaders=1 also returns the entries of active program sub-headers (with their subHeaderId). ?files=1 adds each activity's photos and proof files (names only;
// the files themselves come from the evidence route). Entries that belong to a program sub-header are left out, the same
// as in Medpost's own activity feed.
func (a *API) Activities(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
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
	limit := defaultActivityLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive number"})
			return
		}
		limit = min(n, maxActivityLimit)
	}

	q := `
		SELECT a.ActivityID, p.HealthProcedure, a.ActivityType, a.ActivityTitle,
		       COALESCE(a.ActivitySummary,''), COALESCE(a.ActivityDate,''), COALESCE(a.Location,''),
		       COALESCE(a.FK_SubHeaderID, 0)
		FROM ActivityTable a
		JOIN HealthProcedureTable p ON p.ProcID = a.FK_ProcID
		WHERE a.FK_SubHeaderID IS NULL`
	if r.URL.Query().Get("subheaders") == "1" {
		// Entries of active sub-headers come too; the caller tells them apart by subHeaderId.
		q = `
		SELECT a.ActivityID, p.HealthProcedure, a.ActivityType, a.ActivityTitle,
		       COALESCE(a.ActivitySummary,''), COALESCE(a.ActivityDate,''), COALESCE(a.Location,''),
		       COALESCE(a.FK_SubHeaderID, 0)
		FROM ActivityTable a
		JOIN HealthProcedureTable p ON p.ProcID = a.FK_ProcID
		WHERE (a.FK_SubHeaderID IS NULL
		       OR a.FK_SubHeaderID IN (SELECT SubHeaderID FROM SubHeaderTable WHERE Status = 'Active'))`
	}
	args := []any{}
	if from != "" {
		q += ` AND a.ActivityDate >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND a.ActivityDate <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY a.ActivityDate DESC, a.ActivityID DESC LIMIT ?`
	args = append(args, limit)

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []Activity{}
	for rows.Next() {
		var act Activity
		var program string
		if err := rows.Scan(&act.ActivityID, &program, &act.Type, &act.Title, &act.Summary, &act.Date, &act.Location, &act.SubHeaderID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		act.Program = display.ProgramTitle(program)
		out = append(out, act)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if r.URL.Query().Get("files") == "1" {
		if err := a.attachFiles(out); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// SubHeader is one active program sub-header (for example "Monthly Resume" under a program).
type SubHeader struct {
	SubHeaderID int64  `json:"subHeaderId"`
	Program     string `json:"program"`
	Name        string `json:"name"`
}

// SubHeaders lists the active sub-headers of every program (GET /api/directory/subheaders), so
// the register can show them under their program. Archived ones are left out.
func (a *API) SubHeaders(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	rows, err := a.DB.Query(`SELECT s.SubHeaderID, p.HealthProcedure, s.SubHeaderName
		FROM SubHeaderTable s JOIN HealthProcedureTable p ON p.ProcID = s.FK_ProcID
		WHERE s.Status = 'Active' ORDER BY p.HealthProcedure, s.SubHeaderName`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := []SubHeader{}
	for rows.Next() {
		var s SubHeader
		var program string
		if err := rows.Scan(&s.SubHeaderID, &program, &s.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		s.Program = display.ProgramTitle(program)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
