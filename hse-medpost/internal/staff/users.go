package staff

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

func (a *API) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT u.UserID, u.Username, u.Role, s.StaffID, s.StaffName, s.Position, s.Status
		FROM AppUserTable u
		JOIN MedpostStaffTable s ON s.StaffID = u.FK_StaffID
		WHERE LOWER(u.Username) <> 'admin'
		ORDER BY u.Role DESC, s.StaffName`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type user struct {
		UserID             int64  `json:"userId"`
		Username           string `json:"username"`
		Role               string `json:"role"`
		StaffID            int64  `json:"staffId"`
		StaffName          string `json:"staffName"`
		Position           string `json:"position"`
		Status             string `json:"status"`
		SigningCertificate bool   `json:"signingCertificate"`
	}
	out := []user{}
	for rows.Next() {
		var u user
		var pos sql.NullString
		if err := rows.Scan(&u.UserID, &u.Username, &u.Role, &u.StaffID, &u.StaffName, &pos, &u.Status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		u.Position = pos.String
		u.SigningCertificate = a.hasSigningFile(u.StaffID)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StaffName   string `json:"staffName"`
		TitleBefore string `json:"titleBefore"`
		TitleAfter  string `json:"titleAfter"`
		Position    string `json:"position"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	staffName, err := FormatStaffName(body.TitleBefore, body.StaffName, body.TitleAfter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff name and username required"})
		return
	}
	body.StaffName = staffName
	body.Username = strings.TrimSpace(body.Username)
	body.Password = strings.TrimSpace(body.Password)
	body.Position = strings.Join(strings.Fields(strings.TrimSpace(body.Position)), " ")
	if body.StaffName == "" || body.Username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "staff name and username required"})
		return
	}
	if body.Position == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "position required"})
		return
	}
	if mapped, ok := a.roleForPosition(body.Position); ok {
		body.Role = mapped
	} else if !isValidRole(body.Role) {
		body.Role = "staff"
	}
	if !auth.Digits6.MatchString(body.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be 6 digits"})
		return
	}
	if isBuiltInAdminUsername(body.Username) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash error"})
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES (?, ?, 'Active')`,
		body.StaffName, body.Position,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	staffID, _ := res.LastInsertId()
	_, err = tx.Exec(
		`INSERT INTO AppUserTable (FK_StaffID, Username, PasswordHash, Role) VALUES (?, ?, ?, ?)`,
		staffID, body.Username, string(hash), body.Role,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "staffId": staffID})
}

func (a *API) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Password = strings.TrimSpace(body.Password)
	if !auth.Digits6.MatchString(body.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be 6 digits"})
		return
	}
	hidden, err := a.isBuiltInAdminUser(id)
	if err != nil && err != sql.ErrNoRows {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if hidden || err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash error"})
		return
	}
	res, err := a.DB.Exec(`UPDATE AppUserTable SET PasswordHash = ? WHERE UserID = ?`, string(hash), id)
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

func (a *API) DeactivateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	sess := middleware.Session(r)
	hidden, hiddenErr := a.isBuiltInAdminUser(id)
	if hiddenErr != nil && hiddenErr != sql.ErrNoRows {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if hidden {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var staffID int64
	var role string
	err = a.DB.QueryRow(`SELECT FK_StaffID, Role FROM AppUserTable WHERE UserID = ?`, id).Scan(&staffID, &role)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if sess.UserID == id {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete your own account"})
		return
	}
	if role == "admin" {
		var admins int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM AppUserTable WHERE Role = 'admin'`).Scan(&admins)
		if admins <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "keep at least one admin"})
			return
		}
	}

	var name string
	_ = a.DB.QueryRow(`SELECT StaffName FROM MedpostStaffTable WHERE StaffID = ?`, staffID).Scan(&name)

	counts, err := a.cascadeDeleteStaff(staffID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cascade delete failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"staffName":    name,
		"activities":   counts.activities,
		"supervisors":  counts.supervisors,
		"loginDeleted": counts.loginDeleted,
	})
}
