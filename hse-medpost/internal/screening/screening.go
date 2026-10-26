// Package screening receives results from the offline HSE forms (drug & alcohol
// test) over the internet.
//
// The forms open from file://, so every request is cross-origin and carries
// no cookies. Staff sign in with the normal username + 6-digit password, get
// a short-lived bearer token back in the JSON body, and send that token on
// each upload. The token holds a real StaffID, so every uploaded row is
// attributed to the person who sent it.
package screening

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/staff"
)

const (
	formDrugAlcohol      = "DrugAlcoholConsent"
	maxBodyBytes         = 1 << 20 // a batch of text-only records is far below 1 MB
	maxRecordsPerRequest = 200
	// Upload sign-in lasts 8 hours, shorter than the 12-hour browser session.
	tokenTTL = 8 * time.Hour
)

type API struct {
	DB        *sql.DB
	AuthStore *auth.Store
	Now       func() time.Time // nil means time.Now; tests override it
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// setCORS is applied to every response, including 401s, so the form can show
// a real message instead of an opaque CORS failure. Wildcard origin is safe
// because this flow never uses cookies or credentials.
func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
}

// Preflight answers the browser's OPTIONS check before a cross-origin POST
// with an Authorization header.
func Preflight(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Login: POST /api/screening/login. Same credentials as /api/login, but the
// token comes back in the body and no Set-Cookie header is sent.
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	username := strings.TrimSpace(body.Username)
	password := strings.TrimSpace(body.Password)
	if username == "" || !auth.Digits6.MatchString(password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be 6 digits"})
		return
	}

	sess, err := staff.VerifyCredentials(a.DB, username, password)
	switch {
	case err == staff.ErrInvalidCredentials, err == staff.ErrAccountInactive:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	token, err := a.AuthStore.IssueFor(sess, tokenTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "token": token, "name": sess.Name, "staffId": sess.StaffID,
		"role": sess.Role, "exp": a.now().Add(tokenTTL).Unix(),
	})
}

// authorize reads the bearer token and returns the signed-in session, or
// writes a 401 and returns false.
func (a *API) authorize(w http.ResponseWriter, r *http.Request) (*auth.Session, bool) {
	token := bearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to upload"})
		return nil, false
	}
	sess, err := a.AuthStore.Verify(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired, sign in again"})
		return nil, false
	}
	return sess, true
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// batch is the upload envelope every offline form sends: which form, which
// version of it, and the records themselves.
type batch struct {
	Form        string            `json:"form"`
	FormVersion string            `json:"formVersion"`
	Records     []json.RawMessage `json:"records"`
}

// decodeBatch reads and checks the envelope. On failure it writes the 400
// response and returns false.
func decodeBatch(w http.ResponseWriter, r *http.Request, wantForm string) (batch, bool) {
	var body batch
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return batch{}, false
	}
	if body.Form != wantForm || body.FormVersion == "" || len(body.FormVersion) > maxFieldLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown form"})
		return batch{}, false
	}
	if len(body.Records) == 0 || len(body.Records) > maxRecordsPerRequest {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send 1 to 200 records"})
		return batch{}, false
	}
	return body, true
}

// SubmitDrugAlcohol: POST /api/screening/drug-alcohol. Stores each record
// once, keyed by the client-generated id, so a retry after a dropped
// connection cannot create duplicates. The response lists every id now
// stored, which lets the form clear exactly those from its queue.
func (a *API) SubmitDrugAlcohol(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	sess, ok := a.authorize(w, r)
	if !ok {
		return
	}
	body, ok := decodeBatch(w, r, formDrugAlcohol)
	if !ok {
		return
	}

	accepted := []string{}
	for _, raw := range body.Records {
		values, err := parseRecord(raw)
		if err != nil {
			// Not listed as accepted, so the form keeps this one queued.
			continue
		}
		if err := a.insert(sess.StaffID, body.FormVersion, values, raw); err != nil {
			log.Printf("screening: drug & alcohol insert failed (client id %s, staff %d): %v", values["id"], sess.StaffID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		accepted = append(accepted, values["id"])
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted})
}
