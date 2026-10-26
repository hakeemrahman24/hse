package activitylog

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

func (a *API) ListSupervisors(w http.ResponseWriter, r *http.Request) {
	procQ := strings.TrimSpace(r.URL.Query().Get("procId"))
	q := `
		SELECT sv.SupervisorID, sv.FK_ProcID, sv.FK_StaffID,
		       COALESCE(sv.StartDate,''), COALESCE(sv.EndDate,''),
		       p.HealthProcedure, s.StaffName, COALESCE(s.Position,''), s.Status
		FROM SupervisorTable sv
		JOIN HealthProcedureTable p ON p.ProcID = sv.FK_ProcID
		JOIN MedpostStaffTable s ON s.StaffID = sv.FK_StaffID
		WHERE NOT EXISTS (
			SELECT 1 FROM AppUserTable u
			WHERE u.FK_StaffID = s.StaffID AND LOWER(u.Username) = 'admin'
		)`
	args := []any{}
	if procQ != "" {
		procID, err := strconv.ParseInt(procQ, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad procId"})
			return
		}
		q += ` AND sv.FK_ProcID = ?`
		args = append(args, procID)
	}
	q += ` ORDER BY p.HealthProcedure, s.StaffName`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type row struct {
		SupervisorID    int64  `json:"supervisorId"`
		ProcID          int64  `json:"procId"`
		StaffID         int64  `json:"staffId"`
		StartDate       string `json:"startDate"`
		EndDate         string `json:"endDate"`
		ProgramTitle    string `json:"programTitle"`
		HealthProcedure string `json:"healthProcedure"`
		StaffName       string `json:"staffName"`
		Position        string `json:"position"`
		StaffStatus     string `json:"staffStatus"`
	}
	out := []row{}
	for rows.Next() {
		var item row
		var procName string
		if err := rows.Scan(&item.SupervisorID, &item.ProcID, &item.StaffID,
			&item.StartDate, &item.EndDate, &procName, &item.StaffName, &item.Position, &item.StaffStatus); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		item.HealthProcedure = procName
		item.ProgramTitle = display.ProgramTitle(procName)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateSupervisor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProcID    int64  `json:"procId"`
		StaffID   int64  `json:"staffId"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.ProcID <= 0 || body.StaffID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "procId and staffId required"})
		return
	}
	var err error
	body.StartDate, err = dates.Normalize(body.StartDate)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "startDate: " + err.Error()})
		return
	}
	body.EndDate, err = dates.Normalize(body.EndDate)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endDate: " + err.Error()})
		return
	}
	if body.StartDate != "" && body.EndDate != "" && body.StartDate > body.EndDate {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "startDate must be <= endDate"})
		return
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM HealthProcedureTable WHERE ProcID = ?`, body.ProcID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "program not found"})
		return
	}
	if err := a.DB.QueryRow(`SELECT 1 FROM MedpostStaffTable WHERE StaffID = ?`, body.StaffID).Scan(&exists); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff not found"})
		return
	}
	if hidden, err := staffIsBuiltInAdmin(a.DB, body.StaffID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	} else if hidden {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff not found"})
		return
	}

	res, err := a.DB.Exec(`
		INSERT INTO SupervisorTable (FK_ProcID, FK_StaffID, StartDate, EndDate)
		VALUES (?, ?, ?, ?)`,
		body.ProcID, body.StaffID, nullIfEmpty(body.StartDate), nullIfEmpty(body.EndDate),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "supervisorId": id})
}

func (a *API) UpdateSupervisor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		StaffID   *int64  `json:"staffId"`
		StartDate *string `json:"startDate"`
		EndDate   *string `json:"endDate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM SupervisorTable WHERE SupervisorID = ?`, id).Scan(&exists); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if body.StaffID != nil {
		if err := a.DB.QueryRow(`SELECT 1 FROM MedpostStaffTable WHERE StaffID = ?`, *body.StaffID).Scan(&exists); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff not found"})
			return
		}
		if hidden, err := staffIsBuiltInAdmin(a.DB, *body.StaffID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		} else if hidden {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff not found"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE SupervisorTable SET FK_StaffID = ? WHERE SupervisorID = ?`, *body.StaffID, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.StartDate != nil {
		start, err := dates.Normalize(*body.StartDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "startDate: " + err.Error()})
			return
		}
		if _, err := a.DB.Exec(`UPDATE SupervisorTable SET StartDate = ? WHERE SupervisorID = ?`, nullIfEmpty(start), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.EndDate != nil {
		end, err := dates.Normalize(*body.EndDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endDate: " + err.Error()})
			return
		}
		if _, err := a.DB.Exec(`UPDATE SupervisorTable SET EndDate = ? WHERE SupervisorID = ?`, nullIfEmpty(end), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeleteSupervisor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM SupervisorTable WHERE SupervisorID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func staffIsBuiltInAdmin(database *sql.DB, staffID int64) (bool, error) {
	var n int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM AppUserTable WHERE FK_StaffID = ? AND LOWER(Username) = 'admin'`,
		staffID,
	).Scan(&n)
	return n > 0, err
}
