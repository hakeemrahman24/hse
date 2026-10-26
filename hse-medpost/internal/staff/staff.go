package staff

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"hse-medpost/internal/auth"
)

func (a *API) ListStaff(w http.ResponseWriter, r *http.Request) {
	q := `
		SELECT s.StaffID, s.StaffName, COALESCE(s.Position,''), s.Status
		FROM MedpostStaffTable s
		WHERE NOT EXISTS (
			SELECT 1 FROM AppUserTable u
			WHERE u.FK_StaffID = s.StaffID AND LOWER(u.Username) = 'admin'
		)`
	args := []any{}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "Active" || status == "NotActive" {
		q += ` AND s.Status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY Status, StaffName`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type staff struct {
		StaffID   int64  `json:"staffId"`
		StaffName string `json:"staffName"`
		Position  string `json:"position"`
		Status    string `json:"status"`
	}
	out := []staff{}
	for rows.Next() {
		var s staff
		if err := rows.Scan(&s.StaffID, &s.StaffName, &s.Position, &s.Status); err != nil {
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

func (a *API) UpdateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		StaffName *string `json:"staffName"`
		Position  *string `json:"position"`
		Status    *string `json:"status"`
		Username  *string `json:"username"`
		Password  *string `json:"password"`
		Role      *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	hidden, err := a.isBuiltInAdminStaff(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if hidden {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM MedpostStaffTable WHERE StaffID = ?`, id).Scan(&exists); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if body.StaffName != nil {
		name := strings.TrimSpace(*body.StaffName)
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff name required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE MedpostStaffTable SET StaffName = ? WHERE StaffID = ?`, name, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Position != nil {
		pos := strings.Join(strings.Fields(strings.TrimSpace(*body.Position)), " ")
		if _, err := a.DB.Exec(`UPDATE MedpostStaffTable SET Position = ? WHERE StaffID = ?`, pos, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	roleToSave := ""
	if body.Role != nil {
		roleToSave = strings.TrimSpace(*body.Role)
		if !isValidRole(roleToSave) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be admin or staff"})
			return
		}
	} else if body.Position != nil {
		if mapped, ok := a.roleForPosition(strings.Join(strings.Fields(strings.TrimSpace(*body.Position)), " ")); ok {
			roleToSave = mapped
		}
	}
	if roleToSave != "" {
		if _, err := a.DB.Exec(`UPDATE AppUserTable SET Role = ? WHERE FK_StaffID = ?`, roleToSave, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Username != nil {
		username := strings.TrimSpace(*body.Username)
		if username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required"})
			return
		}
		if isBuiltInAdminUsername(username) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE AppUserTable SET Username = ? WHERE FK_StaffID = ?`, username, id); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Password != nil && strings.TrimSpace(*body.Password) != "" {
		password := strings.TrimSpace(*body.Password)
		if !auth.Digits6.MatchString(password) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be 6 digits"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash error"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE AppUserTable SET PasswordHash = ? WHERE FK_StaffID = ?`, string(hash), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Status != nil {
		st := strings.TrimSpace(*body.Status)
		if st != "Active" && st != "NotActive" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be Active or NotActive"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE MedpostStaffTable SET Status = ? WHERE StaffID = ?`, st, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type staffCascadeCounts struct {
	activities   int
	supervisors  int
	loginDeleted bool
}

func (a *API) cascadeDeleteStaff(staffID int64) (staffCascadeCounts, error) {
	var c staffCascadeCounts
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM ActivityTable WHERE FK_StaffID = ?`, staffID).Scan(&c.activities)
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM SupervisorTable WHERE FK_StaffID = ?`, staffID).Scan(&c.supervisors)
	var nLogin int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM AppUserTable WHERE FK_StaffID = ?`, staffID).Scan(&nLogin)
	c.loginDeleted = nLogin > 0

	tx, err := a.DB.Begin()
	if err != nil {
		return c, err
	}
	defer tx.Rollback()

	// Evidence cascades from ActivityTable ON DELETE CASCADE.
	if _, err := tx.Exec(`DELETE FROM ActivityTable WHERE FK_StaffID = ?`, staffID); err != nil {
		return c, err
	}
	if _, err := tx.Exec(`DELETE FROM SupervisorTable WHERE FK_StaffID = ?`, staffID); err != nil {
		return c, err
	}
	if _, err := tx.Exec(`DELETE FROM AppUserTable WHERE FK_StaffID = ?`, staffID); err != nil {
		return c, err
	}
	res, err := tx.Exec(`DELETE FROM MedpostStaffTable WHERE StaffID = ?`, staffID)
	if err != nil {
		return c, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return c, err
	}
	return c, nil
}

func isBuiltInAdminUsername(username string) bool {
	return strings.EqualFold(strings.TrimSpace(username), "admin")
}

func isBuiltInAdminPosition(name string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " ")) {
	case "administrator", "admin":
		return true
	default:
		return false
	}
}

func (a *API) isBuiltInAdminUser(userID int64) (bool, error) {
	var username string
	err := a.DB.QueryRow(`SELECT Username FROM AppUserTable WHERE UserID = ?`, userID).Scan(&username)
	if err != nil {
		return false, err
	}
	return isBuiltInAdminUsername(username), nil
}

func (a *API) isBuiltInAdminStaff(staffID int64) (bool, error) {
	var n int
	err := a.DB.QueryRow(
		`SELECT COUNT(*) FROM AppUserTable WHERE FK_StaffID = ? AND LOWER(Username) = 'admin'`,
		staffID,
	).Scan(&n)
	return n > 0, err
}
