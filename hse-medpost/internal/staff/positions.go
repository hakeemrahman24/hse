package staff

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (a *API) ListPositionRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT PositionID, PositionName, Role
		FROM PositionRoleTable
		WHERE LOWER(PositionName) NOT IN ('administrator', 'admin')
		ORDER BY Role, PositionName`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type row struct {
		PositionID   int64  `json:"positionId"`
		PositionName string `json:"positionName"`
		Role         string `json:"role"`
	}
	out := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.PositionID, &item.PositionName, &item.Role); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreatePositionRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PositionName string `json:"positionName"`
		Role         string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.PositionName = strings.Join(strings.Fields(strings.TrimSpace(body.PositionName)), " ")
	if body.PositionName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "position name required"})
		return
	}
	if isBuiltInAdminPosition(body.PositionName) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "position already exists"})
		return
	}
	if !isValidRole(body.Role) {
		body.Role = "staff"
	}
	res, err := a.DB.Exec(
		`INSERT INTO PositionRoleTable (PositionName, Role) VALUES (?, ?)`,
		body.PositionName, body.Role,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "position already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "positionId": id})
}

func (a *API) UpdatePositionRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		PositionName *string `json:"positionName"`
		Role         *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var currentName string
	if err := a.DB.QueryRow(`SELECT PositionName FROM PositionRoleTable WHERE PositionID = ?`, id).Scan(&currentName); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if isBuiltInAdminPosition(currentName) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	// The name and the role change together or not at all.
	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	if body.PositionName != nil {
		name := strings.Join(strings.Fields(strings.TrimSpace(*body.PositionName)), " ")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "position name required"})
			return
		}
		if isBuiltInAdminPosition(name) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "position already exists"})
			return
		}
		if _, err := tx.Exec(`UPDATE PositionRoleTable SET PositionName = ? WHERE PositionID = ?`, name, id); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "position already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Role != nil {
		role := strings.TrimSpace(*body.Role)
		if !isValidRole(role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be admin or staff"})
			return
		}
		if _, err := tx.Exec(`UPDATE PositionRoleTable SET Role = ? WHERE PositionID = ?`, role, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeletePositionRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var currentName string
	if err := a.DB.QueryRow(`SELECT PositionName FROM PositionRoleTable WHERE PositionID = ?`, id).Scan(&currentName); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if isBuiltInAdminPosition(currentName) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM PositionRoleTable WHERE PositionID = ?`, id)
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

func (a *API) roleForPosition(position string) (string, bool) {
	position = strings.Join(strings.Fields(strings.TrimSpace(position)), " ")
	if position == "" {
		return "", false
	}
	var role string
	err := a.DB.QueryRow(
		`SELECT Role FROM PositionRoleTable WHERE lower(PositionName) = lower(?)`,
		position,
	).Scan(&role)
	if err != nil {
		return "", false
	}
	return role, true
}
