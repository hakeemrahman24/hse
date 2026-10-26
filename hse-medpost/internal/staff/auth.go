package staff

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

type API struct {
	DB         *sql.DB
	Store      *auth.Store
	SigningDir string // private folder on this server for doctor and CMO .p12 files
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Password = strings.TrimSpace(body.Password)
	if body.Username == "" || !auth.Digits6.MatchString(body.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be 6 digits"})
		return
	}

	sess, err := VerifyCredentials(a.DB, body.Username, body.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	case errors.Is(err, ErrAccountInactive):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "account inactive"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if err := a.Store.Set(w, sess); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "role": sess.Role, "name": sess.Name, "staffId": sess.StaffID,
	})
}

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountInactive    = errors.New("account inactive")
)

// VerifyCredentials checks a username and password against AppUserTable
// (bcrypt) and returns the matching session fields. It is shared by the
// cookie login above and the bearer-token login in internal/screening, so
// the credential check lives in one place.
func VerifyCredentials(db *sql.DB, username, password string) (auth.Session, error) {
	var sess auth.Session
	var hash, status string
	err := db.QueryRow(`
		SELECT u.UserID, u.FK_StaffID, u.PasswordHash, u.Role, s.StaffName, s.Status
		FROM AppUserTable u
		JOIN MedpostStaffTable s ON s.StaffID = u.FK_StaffID
		WHERE u.Username = ?`, username,
	).Scan(&sess.UserID, &sess.StaffID, &hash, &sess.Role, &sess.Name, &status)
	if err == sql.ErrNoRows {
		return auth.Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return auth.Session{}, err
	}
	if status != "Active" {
		return auth.Session{}, ErrAccountInactive
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return auth.Session{}, ErrInvalidCredentials
	}
	return sess, nil
}

// StaffUsernames is public: active logins for the pick-from-list dropdown.
// The built-in "admin" username is omitted (type it manually) but other
// accounts with Role=admin still appear in the list.
func (a *API) StaffUsernames(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT u.Username, s.StaffName, u.Role
		FROM AppUserTable u
		JOIN MedpostStaffTable s ON s.StaffID = u.FK_StaffID
		WHERE s.Status = 'Active' AND LOWER(u.Username) <> 'admin'
		ORDER BY s.StaffName`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	type row struct {
		Username  string `json:"username"`
		StaffName string `json:"staffName"`
		Role      string `json:"role"`
	}
	out := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.Username, &item.StaffName, &item.Role); err != nil {
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

func (a *API) Logout(w http.ResponseWriter, r *http.Request) {
	a.Store.Clear(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"userId": sess.UserID, "staffId": sess.StaffID, "role": sess.Role, "name": sess.Name,
	})
}

// ChangePassword lets the logged-in user set their own password, given their current one.
func (a *API) ChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.CurrentPassword = strings.TrimSpace(body.CurrentPassword)
	body.NewPassword = strings.TrimSpace(body.NewPassword)
	if !auth.Digits6.MatchString(body.NewPassword) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be 6 digits"})
		return
	}

	var hash string
	if err := a.DB.QueryRow(`SELECT PasswordHash FROM AppUserTable WHERE UserID = ?`, sess.UserID).Scan(&hash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.CurrentPassword)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash error"})
		return
	}
	if _, err := a.DB.Exec(`UPDATE AppUserTable SET PasswordHash = ? WHERE UserID = ?`, string(newHash), sess.UserID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
