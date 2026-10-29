package mcusubmit

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

// maxBounceBody caps what the webhook reads. A Brevo event is a few hundred bytes.
const maxBounceBody = 1 << 20

// bounceEvents are the Brevo events after which mail to that address should stop. soft_bounce and
// deferred are temporary (full mailbox, busy server) and Brevo retries them, so they change nothing.
var bounceEvents = map[string]bool{
	"hard_bounce":   true,
	"invalid_email": true,
	"blocked":       true,
	"spam":          true, // the person marked a mail as spam; more mail would only hurt the sender's reputation
	"unsubscribed":  true,
}

// SetBounceSecret turns the Brevo webhook on. An empty secret leaves it off (404), so nothing that
// changes accounts is reachable without a shared secret.
func (a *API) SetBounceSecret(secret string) { a.bounceSecret = secret }

// BrevoWebhook receives Brevo's transactional events and flags addresses that cannot receive mail.
// It sits outside the sign-in: Brevo is not a browser. The shared secret arrives as
// "Authorization: Bearer <secret>" (set in Brevo's webhook settings), and it is compared in
// constant time so the answer time does not reveal how much of a guess was right.
func (a *API) BrevoWebhook(w http.ResponseWriter, r *http.Request) {
	if a.bounceSecret == "" {
		http.NotFound(w, r)
		return
	}
	if !secretMatches(r.Header.Get("Authorization"), a.bounceSecret) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBounceBody)
	var ev struct {
		Event string `json:"event"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if bounceEvents[strings.ToLower(ev.Event)] {
		// An unknown address is normal, so a miss is 200; only a database failure is a 500, which
		// makes Brevo retry later.
		if _, err := a.Store.MarkEmailBounced(r.Context(), ev.Email, strings.ToLower(ev.Event)); err != nil {
			log.Printf("mcusubmit: bounce webhook: %v", err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// secretMatches checks an "Authorization: Bearer ..." header against the secret. Both sides are
// hashed first so ConstantTimeCompare always sees equal-length inputs.
func secretMatches(header, secret string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := sha256.Sum256([]byte(header[len(prefix):]))
	want := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
