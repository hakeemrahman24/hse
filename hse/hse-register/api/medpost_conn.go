package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	settingMedpostURL   = "medpost_url"
	settingMedpostToken = "medpost_token"

	sealPrefix         = "enc1:"
	maxMedpostURLLen   = 200
	maxMedpostTokenLen = 256

	medpostSourceSettings = "settings"
	medpostSourceEnv      = "environment"
)

var errMedpostOff = errors.New("hse-medpost is not configured")

// formError is a validation message meant to be shown on the Settings form as it is.
type formError string

func (e formError) Error() string { return string(e) }

// medpostConn is the connection to hse-medpost that administrators manage in Settings > Medpost
// API. The URL and token saved there win; the MEDPOST_URL and MEDPOST_DIRECTORY_TOKEN settings
// are the fallback, so a server that already runs on them keeps working. Changes apply at once,
// without a restart.
//
// The token is saved encrypted (AES-GCM, key derived from the session secret) and is never shown
// again. If the session secret changes, the saved token cannot be read any more; the page says so
// and the administrator enters it again.
type medpostConn struct {
	db  *sql.DB
	key [32]byte

	mu      sync.RWMutex
	dir     *medpostDirectory
	url     string
	source  string // medpostSourceSettings, medpostSourceEnv or "" when not configured
	problem string // why the saved settings could not be used, if they could not
}

func newMedpostConn(db *sql.DB, secret string) *medpostConn {
	c := &medpostConn{db: db, key: sha256.Sum256([]byte("hse-register/medpost-token|" + secret))}
	c.reload()
	return c
}

// Get returns the current client, or nil when hse-medpost is not configured.
func (c *medpostConn) Get() *medpostDirectory {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dir
}

// medpostStatus is what the Settings page shows. The token itself is never part of it.
type medpostStatus struct {
	Configured bool
	Source     string // medpostSourceSettings, medpostSourceEnv or ""
	URL        string
	Problem    string
	EnvSet     bool // MEDPOST_URL and MEDPOST_DIRECTORY_TOKEN are both set
}

func (c *medpostConn) Status() medpostStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return medpostStatus{
		Configured: c.dir != nil, Source: c.source, URL: c.url, Problem: c.problem,
		EnvSet: os.Getenv("MEDPOST_URL") != "" && os.Getenv("MEDPOST_DIRECTORY_TOKEN") != "",
	}
}

// reload picks the connection: saved settings first, then the environment.
func (c *medpostConn) reload() {
	u, token, saved, problem := c.readSaved()
	source := medpostSourceSettings
	if !saved {
		u, token, source = os.Getenv("MEDPOST_URL"), os.Getenv("MEDPOST_DIRECTORY_TOKEN"), medpostSourceEnv
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.problem = problem
	if u == "" || token == "" {
		c.dir, c.url, c.source = nil, "", ""
		return
	}
	c.dir, c.url, c.source = newMedpostDirectory(u, token), strings.TrimRight(u, "/"), source
}

// readSaved reads the saved URL and token. saved is true only when both are present and the
// token could be decrypted; problem explains a failure for the Settings page. A missing table
// (migration 014 not run yet) is not an error: the environment settings then apply.
func (c *medpostConn) readSaved() (u, token string, saved bool, problem string) {
	if c.db == nil {
		return "", "", false, ""
	}
	rows, err := c.db.Query(`SELECT name, value FROM app_setting WHERE name IN (?, ?)`, settingMedpostURL, settingMedpostToken)
	if err != nil {
		log.Printf("medpost settings: %v (did you run migration 014?)", err)
		return "", "", false, ""
	}
	defer rows.Close()
	var sealed string
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			log.Printf("medpost settings: %v", err)
			return "", "", false, ""
		}
		switch name {
		case settingMedpostURL:
			u = value
		case settingMedpostToken:
			sealed = value
		}
	}
	if rows.Err() != nil || u == "" || sealed == "" {
		return "", "", false, ""
	}
	token, err = c.open(sealed)
	if err != nil {
		log.Print("medpost settings: the saved token cannot be read (the session secret changed?)")
		return "", "", false, "The saved token cannot be read any more (the server's session secret changed). Enter the token again."
	}
	return u, token, true, ""
}

// savedToken returns the token to use when the form leaves the token empty: the saved one, else
// the environment one.
func (c *medpostConn) savedToken() string {
	if _, token, saved, _ := c.readSaved(); saved {
		return token
	}
	return os.Getenv("MEDPOST_DIRECTORY_TOKEN")
}

// Save stores the URL and token. An empty token keeps the token already saved.
func (c *medpostConn) Save(rawURL, token string) error {
	u, err := validateMedpostURL(rawURL)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		_, saved, ok, _ := c.readSaved()
		if !ok {
			return formError("Enter the token.")
		}
		token = saved
	}
	if err := validateMedpostToken(token); err != nil {
		return err
	}
	sealed, err := c.seal(token)
	if err != nil {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, value := range map[string]string{settingMedpostURL: u, settingMedpostToken: sealed} {
		if _, err := tx.Exec(`INSERT INTO app_setting (name, value) VALUES (?, ?)
			ON DUPLICATE KEY UPDATE value = VALUES(value)`, name, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.reload()
	return nil
}

// Clear removes the saved settings, so the environment settings (if any) apply again.
func (c *medpostConn) Clear() error {
	if _, err := c.db.Exec(`DELETE FROM app_setting WHERE name IN (?, ?)`, settingMedpostURL, settingMedpostToken); err != nil {
		return err
	}
	c.reload()
	return nil
}

// ---- encryption of the saved token ----

func (c *medpostConn) seal(plain string) (string, error) {
	gcm, err := c.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return sealPrefix + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (c *medpostConn) open(sealed string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, sealPrefix)
	if !ok {
		return "", errors.New("unknown format")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	gcm, err := c.gcm()
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	return string(plain), err
}

func (c *medpostConn) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ---- validation ----

// validateMedpostURL checks and normalises the address. Plain http is allowed only for this
// machine, because the bearer token travels in every request and must not cross a network in
// clear text.
func validateMedpostURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", formError("Enter the address of hse-medpost.")
	}
	if len(raw) > maxMedpostURLLen {
		return "", formError("The address is too long.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", formError("Enter a full address such as https://medpost.example.")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", formError("The address must not contain a login, a query or a fragment.")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLocalHost(u.Hostname()) {
			return "", formError("Use https, or http only for this machine (localhost or 127.0.0.1).")
		}
	default:
		return "", formError("The address must start with https://.")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateMedpostToken(token string) error {
	switch {
	case token == "":
		return formError("Enter the token.")
	case len(token) > maxMedpostTokenLen:
		return formError("The token is too long.")
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return formError("The token cannot contain spaces or control characters.")
		}
	}
	return nil
}

// ---- test connection ----

// medpostTest is the outcome of "Test connection".
type medpostTest struct {
	OK      bool
	Message string
}

var medpostTestClient = &http.Client{
	Timeout: 8 * time.Second,
	// Never follow a redirect: the token must only go to the address that was entered.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// testMedpost checks the employee directory (used by the SOC sign-in and the company list) and
// the activities list (used by the Medical Activities page). The directory result decides OK; a
// missing activities route is reported but is not a connection failure. No answer body is read
// into a message.
func testMedpost(ctx context.Context, baseURL, token string) medpostTest {
	dirStatus, err := medpostProbe(ctx, baseURL+"/api/directory/employees?q=te", token)
	if err != nil {
		return medpostTest{Message: "Could not reach hse-medpost at that address."}
	}
	switch dirStatus {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return medpostTest{Message: "hse-medpost answered, but did not accept the token."}
	case http.StatusServiceUnavailable:
		return medpostTest{Message: "hse-medpost answered, but its directory API has no token set."}
	default:
		return medpostTest{Message: fmt.Sprintf("hse-medpost answered with an unexpected status (%d). Check the address.", dirStatus)}
	}
	actStatus, err := medpostProbe(ctx, baseURL+"/api/directory/activities?limit=1", token)
	switch {
	case err != nil:
		return medpostTest{OK: true, Message: "Connected. The activities list could not be checked."}
	case actStatus == http.StatusOK:
		return medpostTest{OK: true, Message: "Connected. Employee directory and activities list both work."}
	case actStatus == http.StatusNotFound:
		return medpostTest{OK: true, Message: "Connected, but this hse-medpost has no activities list yet. Update hse-medpost to show Medical Activities."}
	default:
		return medpostTest{OK: true, Message: fmt.Sprintf("Connected. The activities list answered with status %d.", actStatus)}
	}
}

func medpostProbe(ctx context.Context, target, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := medpostTestClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, nil
}

// ---- adapters that always use the current connection ----

// liveDirectory is the employeeDirectory the SOC form uses: every call goes to whatever
// connection is configured right now.
type liveDirectory struct{ conn *medpostConn }

func (l liveDirectory) Search(ctx context.Context, q string, max int) ([]nameMatch, error) {
	d := l.conn.Get()
	if d == nil {
		return nil, errMedpostOff
	}
	return d.Search(ctx, q, max)
}

func (l liveDirectory) Verify(ctx context.Context, key string, dob time.Time) (*employee, error) {
	d := l.conn.Get()
	if d == nil {
		return nil, errMedpostOff
	}
	return d.Verify(ctx, key, dob)
}

// liveCompanies is the companySource of the responsible-company dropdown. Without a Medpost
// connection it uses the fallback (the development list, or none).
type liveCompanies struct {
	conn     *medpostConn
	fallback companySource
}

func (l liveCompanies) Companies(ctx context.Context) ([]string, error) {
	if d := l.conn.Get(); d != nil {
		return d.Companies(ctx)
	}
	if l.fallback != nil {
		return l.fallback.Companies(ctx)
	}
	return nil, errMedpostOff
}
