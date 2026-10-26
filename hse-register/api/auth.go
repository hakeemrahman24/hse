package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	UserID   int64  `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
	// MustChange is filled from the database on every request, never from the cookie: the
	// person signed in with a reset password and has to choose a new one first.
	MustChange bool `json:"-"`
}

type SessionStore struct {
	Secret []byte
}

// Roles. A cookie issued before roles existed has an empty role, which is treated as roleUser
// everywhere, so an old cookie can never open the admin pages.
const (
	roleUser  = "user"
	roleAdmin = "admin"
)

const sessionCookieName = "session"
const sessionTTL = 12 * time.Hour

func NewSessionStore(secret string) *SessionStore {
	return &SessionStore{Secret: []byte(secret)}
}

func (s *SessionStore) Set(w http.ResponseWriter, sess Session) error {
	sess.Exp = time.Now().Add(sessionTTL).Unix()
	payload, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	token := enc + "." + s.sign(enc)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *SessionStore) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *SessionStore) Get(r *http.Request) (*Session, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid session")
	}
	if !hmac.Equal([]byte(s.sign(parts[0])), []byte(parts[1])) {
		return nil, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	if time.Now().Unix() > sess.Exp {
		return nil, errors.New("expired")
	}
	return &sess, nil
}

func (s *SessionStore) sign(payload string) string {
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var ErrInvalidCredentials = errors.New("invalid username or password")

func VerifyAdminCredentials(db *sql.DB, username, password string) (Session, error) {
	var sess Session
	var hash string
	var archived bool
	err := db.QueryRow(`SELECT id, username, role, password_hash, archived_at IS NOT NULL FROM admin_users WHERE username = ?`, username).
		Scan(&sess.UserID, &sess.Username, &sess.Role, &hash, &archived)
	if err == sql.ErrNoRows {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || archived {
		return Session{}, ErrInvalidCredentials // an archived account gets the same answer as a wrong password
	}
	return sess, nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

type authContext struct {
	store *SessionStore
	db    *sql.DB // when set, every request is checked against the account (archived, deleted, role)
}

type sessionKey struct{}

// session returns the signed-in person for this request. Behind requireAuth it is the live
// account (name and role as the database has them now), not just what the cookie says.
func (a *authContext) session(r *http.Request) *Session {
	if s, ok := r.Context().Value(sessionKey{}).(*Session); ok {
		return s
	}
	sess, err := a.store.Get(r)
	if err != nil {
		return nil
	}
	return sess
}

// verified checks the cookie and, when there is a database, that the account still exists, is not
// archived, and what its role is today. So archiving, deleting or demoting someone takes effect on
// their very next click, not when their 12-hour cookie runs out. On failure it has already answered.
func (a *authContext) verified(w http.ResponseWriter, r *http.Request) (*http.Request, *Session, bool) {
	sess, err := a.store.Get(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return r, nil, false
	}
	if a.db != nil {
		var archived bool
		live := *sess
		err := a.db.QueryRowContext(r.Context(), `SELECT username, role, archived_at IS NOT NULL, must_change_password FROM admin_users WHERE id = ?`, sess.UserID).
			Scan(&live.Username, &live.Role, &archived, &live.MustChange)
		switch {
		case err == sql.ErrNoRows || (err == nil && archived):
			a.store.Clear(w)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return r, nil, false
		case err != nil:
			http.Error(w, "The account could not be checked. Try again.", http.StatusServiceUnavailable)
			return r, nil, false
		}
		sess = &live
	}
	if sess.MustChange && r.URL.Path != "/account" && r.URL.Path != "/logout" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return r, nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)), sess, true
}

func (a *authContext) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r, _, ok := a.verified(w, r)
		if !ok {
			return
		}
		next(w, r)
	}
}

// requireAdmin is requireAuth plus the admin role. A signed-in non-admin gets 403 (not a
// redirect) so it is clear the page exists but is not for them.
func (a *authContext) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r, sess, ok := a.verified(w, r)
		if !ok {
			return
		}
		if sess.Role != roleAdmin {
			http.Error(w, "Not allowed: this page is for administrators.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// tokenEnvelope wraps any payload with an expiry so a signed cookie can carry its own lifetime.
type tokenEnvelope struct {
	P   json.RawMessage `json:"p"`
	Exp int64           `json:"exp"`
}

// SetToken stores v in a signed cookie called name that lasts ttl. It is used for the SOC cookies
// (check and session), which have different lifetimes from the staff session. Cookies are
// signed (cannot be changed) but not encrypted, so nothing secret may go in v.
func (s *SessionStore) SetToken(w http.ResponseWriter, name string, ttl time.Duration, secure bool, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	env, err := json.Marshal(tokenEnvelope{P: payload, Exp: time.Now().Add(ttl).Unix()})
	if err != nil {
		return err
	}
	enc := base64.RawURLEncoding.EncodeToString(env)
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: enc + "." + s.sign(enc), Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
	return nil
}

func (s *SessionStore) GetToken(r *http.Request, name string, v any) error {
	c, err := r.Cookie(name)
	if err != nil {
		return err
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(s.sign(parts[0])), []byte(parts[1])) {
		return errors.New("bad token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var env tokenEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if time.Now().Unix() > env.Exp {
		return errors.New("expired")
	}
	return json.Unmarshal(env.P, v)
}

func (s *SessionStore) ClearToken(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure, MaxAge: -1})
}
