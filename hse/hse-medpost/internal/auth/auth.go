package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var Digits6 = regexp.MustCompile(`^[0-9]{6}$`)
var UsernameOK = regexp.MustCompile(`^[a-zA-Z0-9._-]{2,64}$`)

func ValidAdminUsername(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && len(s) <= 64 && !strings.ContainsAny(s, " \t\n")
}

func ValidStaffUsername(s string) bool {
	return ValidAdminUsername(s)
}

type Session struct {
	UserID  int64  `json:"uid"`
	StaffID int64  `json:"sid"`
	Role    string `json:"role"`
	Name    string `json:"name"`
	Exp     int64  `json:"exp"`
}

type Store struct {
	Secret []byte
}

func NewStore(secret string) *Store {
	return &Store{Secret: []byte(secret)}
}

// SessionTTL is how long a signed session stays valid, for both the browser
// cookie and the bearer tokens issued to the offline screening forms.
const SessionTTL = 12 * time.Hour

// Issue returns a signed, expiring token for sess. Callers decide how to
// deliver it: Set puts it in a cookie, the screening login returns it in JSON.
func (s *Store) Issue(sess Session) (string, error) {
	return s.IssueFor(sess, SessionTTL)
}

// IssueFor is Issue with a custom lifetime, for tokens that should expire sooner.
func (s *Store) IssueFor(sess Session, ttl time.Duration) (string, error) {
	sess.Exp = time.Now().Add(ttl).Unix()
	payload, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return enc + "." + s.sign(enc), nil
}

func (s *Store) Set(w http.ResponseWriter, sess Session) error {
	token, err := s.Issue(sess)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
	return nil
}

func (s *Store) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *Store) Get(r *http.Request) (*Session, error) {
	c, err := r.Cookie("session")
	if err != nil {
		return nil, err
	}
	return s.Verify(c.Value)
}

// Verify checks a token produced by Issue: signature first, then expiry.
func (s *Store) Verify(token string) (*Session, error) {
	parts := strings.Split(token, ".")
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

func (s *Store) sign(payload string) string {
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func ParseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
