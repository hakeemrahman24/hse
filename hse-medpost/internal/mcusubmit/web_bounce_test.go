package mcusubmit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const bounceSecret = "brevo-secret-value-0123456789"

func (e *env) webhook(body string, auth string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcu-submit/webhooks/brevo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestBounceWebhookIsOffWithoutASecret(t *testing.T) {
	e := newEnv(t)
	if rec := e.webhook(`{"event":"hard_bounce","email":"x@example.com"}`, "Bearer "+bounceSecret); rec.Code != http.StatusNotFound {
		t.Fatalf("no secret configured = %d, want 404", rec.Code)
	}
}

func TestBounceWebhookNeedsTheRightSecret(t *testing.T) {
	e := newEnv(t)
	e.api.SetBounceSecret(bounceSecret)
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	body := `{"event":"hard_bounce","email":"hr@example.com"}`
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + bounceSecret + "x", bounceSecret, "Basic " + bounceSecret} {
		if rec := e.webhook(body, auth); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q = %d, want 401", auth, rec.Code)
		}
	}
	if u, _ := e.store.ByID(context.Background(), id); u.EmailBouncedAt != "" {
		t.Fatal("an unauthenticated call flagged an account")
	}
}

func TestBounceWebhookFlagsSubmittersAndReviewers(t *testing.T) {
	e := newEnv(t)
	e.api.SetBounceSecret(bounceSecret)
	ctx := context.Background()
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, _ = e.store.AddReviewer(ctx, "doc@example.com", "")

	if rec := e.webhook(`{"event":"hard_bounce","email":"HR@example.com"}`, "Bearer "+bounceSecret); rec.Code != http.StatusOK {
		t.Fatalf("hard bounce = %d", rec.Code)
	}
	if u, _ := e.store.ByID(ctx, id); u.EmailBouncedAt == "" {
		t.Error("the submitter was not flagged")
	}
	if rec := e.webhook(`{"event":"invalid_email","email":"doc@example.com"}`, "Bearer "+bounceSecret); rec.Code != http.StatusOK {
		t.Fatalf("invalid email = %d", rec.Code)
	}
	if list, _ := e.store.Reviewers(ctx); list[0].BouncedAt == "" {
		t.Error("the reviewer was not flagged")
	}
	events, _ := e.store.Events(ctx, EntitySubmitter, id)
	found := false
	for _, ev := range events {
		found = found || ev.Action == "EmailBounced"
	}
	if !found {
		t.Error("no EmailBounced event")
	}
	var reason string
	if err := e.store.DB.QueryRow(`SELECT COALESCE(EmailBounceReason, '') FROM Submitter WHERE SubmitterID = ?`, id).Scan(&reason); err != nil || reason != "hard_bounce" {
		t.Errorf("EmailBounceReason = %q (%v), want hard_bounce", reason, err)
	}
}

func TestBounceWebhookIgnoresSoftEventsUnknownAddressesAndBadBodies(t *testing.T) {
	e := newEnv(t)
	e.api.SetBounceSecret(bounceSecret)
	ctx := context.Background()
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	auth := "Bearer " + bounceSecret

	for _, body := range []string{
		`{"event":"soft_bounce","email":"hr@example.com"}`,
		`{"event":"delivered","email":"hr@example.com"}`,
		`{"event":"deferred","email":"hr@example.com"}`,
	} {
		if rec := e.webhook(body, auth); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200 (accepted, nothing done)", body, rec.Code)
		}
	}
	if u, _ := e.store.ByID(ctx, id); u.EmailBouncedAt != "" {
		t.Fatal("a soft event flagged the account")
	}
	// An address nobody here owns is normal (Brevo also reports other senders' mail): no error, or
	// Brevo would retry and eventually disable the hook.
	if rec := e.webhook(`{"event":"hard_bounce","email":"stranger@example.com"}`, auth); rec.Code != http.StatusOK {
		t.Errorf("unknown address = %d, want 200", rec.Code)
	}
	if rec := e.webhook(`not json`, auth); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", rec.Code)
	}
	big := `{"event":"hard_bounce","email":"` + strings.Repeat("a", 2<<20) + `"}`
	if rec := e.webhook(big, auth); rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Errorf("2 MB body = %d, want 413 or 400", rec.Code)
	}
}
