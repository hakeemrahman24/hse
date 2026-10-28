package turnstile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeCloudflare answers siteverify and records what the verifier sent.
func fakeCloudflare(t *testing.T, reply string) (*cloudflareVerifier, *http.Request) {
	t.Helper()
	var seen http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = *r
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	v := NewCloudflareVerifier("secret-key").(*cloudflareVerifier)
	v.url = srv.URL
	return v, &seen
}

func TestVerifyAcceptsATokenCloudflareApproves(t *testing.T) {
	v, seen := fakeCloudflare(t, `{"success":true}`)

	ok, err := v.Verify(context.Background(), "good-token", "203.0.113.9")
	if err != nil || !ok {
		t.Fatalf("Verify = %v, %v; want true, nil", ok, err)
	}
	if seen.PostForm.Get("secret") != "secret-key" || seen.PostForm.Get("response") != "good-token" ||
		seen.PostForm.Get("remoteip") != "203.0.113.9" {
		t.Fatalf("unexpected siteverify form: %v", seen.PostForm)
	}
}

func TestVerifyRejectsATokenCloudflareDeclines(t *testing.T) {
	v, _ := fakeCloudflare(t, `{"success":false}`)
	if ok, err := v.Verify(context.Background(), "bad", ""); err != nil || ok {
		t.Fatalf("Verify = %v, %v; want false, nil", ok, err)
	}
}

func TestVerifyRejectsAnEmptyTokenWithoutCallingCloudflare(t *testing.T) {
	v := NewCloudflareVerifier("secret-key")
	// No server is set up: a network call here would fail the test with an error.
	if ok, err := v.Verify(context.Background(), "  ", ""); err != nil || ok {
		t.Fatalf("Verify(empty) = %v, %v; want false, nil", ok, err)
	}
}

func TestVerifyReportsAnUnreadableReply(t *testing.T) {
	v, _ := fakeCloudflare(t, `not json`)
	if _, err := v.Verify(context.Background(), "t", ""); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestDevVerifierPassesOnlyTheDevToken(t *testing.T) {
	if ok, _ := (DevVerifier{}).Verify(context.Background(), "dev-pass", ""); !ok {
		t.Fatal("dev-pass should pass")
	}
	if ok, _ := (DevVerifier{}).Verify(context.Background(), "anything", ""); ok {
		t.Fatal("other tokens must not pass")
	}
}
