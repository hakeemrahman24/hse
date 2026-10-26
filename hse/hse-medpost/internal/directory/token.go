package directory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

// EnsureToken creates the single admin-managed token row. An empty table
// starts from envToken so a server that already has DIRECTORY_API_TOKEN
// keeps working. Later updates from Settings replace that value.
func EnsureToken(database *sql.DB, envToken string) error {
	_, err := database.Exec(`CREATE TABLE IF NOT EXISTS DirectoryAPIToken (
		DirectoryAPITokenID INTEGER PRIMARY KEY,
		Token TEXT NOT NULL DEFAULT '',
		UpdatedAt TEXT NULL
	)`)
	if err != nil {
		return err
	}
	if err := ensureFullTokenColumn(database); err != nil {
		return err
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM DirectoryAPIToken`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = database.Exec(
		`INSERT INTO DirectoryAPIToken (DirectoryAPITokenID, Token, UpdatedAt) VALUES (1, ?, ?)`,
		strings.TrimSpace(envToken), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (a *API) currentToken() string {
	if a.DB != nil {
		token, err := a.storedToken()
		if err == nil && token != "" {
			return token
		}
	}
	return strings.TrimSpace(a.Token)
}

func (a *API) currentFullToken() string {
	if a.DB != nil {
		token, err := a.storedFullToken()
		if err == nil && token != "" {
			return token
		}
		if err == nil {
			return strings.TrimSpace(a.FullToken)
		}
	}
	return strings.TrimSpace(a.FullToken)
}

func (a *API) storedFullToken() (string, error) {
	var token string
	err := a.DB.QueryRow(`SELECT COALESCE(FullToken,'') FROM DirectoryAPIToken WHERE DirectoryAPITokenID = 1`).Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(token), err
}

func (a *API) saveFullToken(token string) error {
	if err := EnsureToken(a.DB, ""); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := a.DB.Exec(
		`UPDATE DirectoryAPIToken SET FullToken = ?, UpdatedAt = ? WHERE DirectoryAPITokenID = 1`,
		token, now,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = a.DB.Exec(
		`INSERT INTO DirectoryAPIToken (DirectoryAPITokenID, Token, FullToken, UpdatedAt) VALUES (1, '', ?, ?)`,
		token, now,
	)
	return err
}

func ensureFullTokenColumn(database *sql.DB) error {
	var probe string
	err := database.QueryRow(`SELECT FullToken FROM DirectoryAPIToken LIMIT 1`).Scan(&probe)
	if err == nil || err == sql.ErrNoRows {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "fulltoken") && !strings.Contains(msg, "no such column") && !strings.Contains(msg, "unknown column") {
		return err
	}
	_, err = database.Exec(`ALTER TABLE DirectoryAPIToken ADD COLUMN FullToken TEXT NOT NULL DEFAULT ''`)
	return err
}

func (a *API) storedToken() (string, error) {
	var token string
	err := a.DB.QueryRow(`SELECT COALESCE(Token,'') FROM DirectoryAPIToken WHERE DirectoryAPITokenID = 1`).Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return strings.TrimSpace(token), err
}

func (a *API) saveToken(token string) error {
	if err := EnsureToken(a.DB, ""); err != nil {
		return err
	}
	res, err := a.DB.Exec(
		`UPDATE DirectoryAPIToken SET Token = ?, UpdatedAt = ? WHERE DirectoryAPITokenID = 1`,
		token, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = a.DB.Exec(
		`INSERT INTO DirectoryAPIToken (DirectoryAPITokenID, Token, UpdatedAt) VALUES (1, ?, ?)`,
		token, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// GetToken is the admin settings read. The directory search itself uses the
// bearer token and does not call this.
func (a *API) GetToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"token":     a.currentToken(),
		"fullToken": a.currentFullToken(),
	})
}

// RotateToken replaces one token. POST /api/directory/full-token, or
// scope=full on the shared address, replaces the all-checkups token. Any
// other call replaces the eligible token. Other databases keep the previous
// value until an admin gives them the new one.
func (a *API) RotateToken(w http.ResponseWriter, r *http.Request) {
	full := strings.HasSuffix(r.URL.Path, "/full-token") || r.URL.Query().Get("scope") == "full"
	token, err := newToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate token"})
		return
	}
	if full {
		err = a.saveFullToken(token)
	} else {
		err = a.saveToken(token)
	}
	if err != nil {
		log.Printf("directory token rotate: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	body := map[string]string{
		"token":     a.currentToken(),
		"fullToken": a.currentFullToken(),
	}
	if full && body["fullToken"] == "" {
		body["fullToken"] = token
	}
	if !full && body["token"] == "" {
		body["token"] = token
	}
	writeJSON(w, http.StatusOK, body)
}
