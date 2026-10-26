package activitylog

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"hse-medpost/internal/display"
)

func (a *API) AdminListPrograms(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT ProcID, HealthProcedure, Status
		FROM HealthProcedureTable
		ORDER BY Status, HealthProcedure`)
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

func (a *API) CreateProgram(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	title := strings.Join(strings.Fields(strings.TrimSpace(body.Title)), " ")
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}
	res, err := a.DB.Exec(
		`INSERT INTO HealthProcedureTable (HealthProcedure, Status) VALUES (?, 'Active')`,
		title,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "procId": id, "title": title, "healthProcedure": title,
	})
}

func (a *API) UpdateProgram(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Title  *string `json:"title"`
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.Title != nil {
		title := strings.Join(strings.Fields(strings.TrimSpace(*body.Title)), " ")
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE HealthProcedureTable SET HealthProcedure = ? WHERE ProcID = ?`, title, id); err != nil {
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
		if _, err := a.DB.Exec(`UPDATE HealthProcedureTable SET Status = ? WHERE ProcID = ?`, st, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeleteProgram(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}

	var title string
	err = a.DB.QueryRow(`SELECT HealthProcedure FROM HealthProcedureTable WHERE ProcID = ?`, id).Scan(&title)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	var activities, documents, supervisors, subHeaders int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM ActivityTable WHERE FK_ProcID = ?`, id).Scan(&activities)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable WHERE FK_ProcID = ?`, id).Scan(&documents)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM SupervisorTable WHERE FK_ProcID = ?`, id).Scan(&supervisors)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM SubHeaderTable WHERE FK_ProcID = ?`, id).Scan(&subHeaders)

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	// Evidence cascades when activities are deleted (FK ON DELETE CASCADE).
	if _, err := tx.Exec(`DELETE FROM ActivityTable WHERE FK_ProcID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete activities"})
		return
	}
	if _, err := tx.Exec(`DELETE FROM DocumentTable WHERE FK_ProcID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete documents"})
		return
	}
	if _, err := tx.Exec(`DELETE FROM SupervisorTable WHERE FK_ProcID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete supervisors"})
		return
	}
	// Sub-header-scoped activities share FK_ProcID with the program, so the
	// ActivityTable delete above already removed them; this just clears the
	// now-empty SubHeaderTable rows themselves.
	if _, err := tx.Exec(`DELETE FROM SubHeaderTable WHERE FK_ProcID = ?`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete sub-headers"})
		return
	}
	res, err := tx.Exec(`DELETE FROM HealthProcedureTable WHERE ProcID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete program"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"title":       title,
		"activities":  activities,
		"documents":   documents,
		"supervisors": supervisors,
		"subHeaders":  subHeaders,
	})
}
