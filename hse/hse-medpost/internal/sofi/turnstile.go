package sofi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const siteVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Verifier checks a Cloudflare Turnstile token on the server.
type Verifier interface {
	Verify(ctx context.Context, token, ip string) (bool, error)
}

type cloudflareVerifier struct {
	secret string
	client *http.Client
}

// NewCloudflareVerifier verifies tokens against Cloudflare with the site's secret key.
func NewCloudflareVerifier(secret string) Verifier {
	return &cloudflareVerifier{secret: secret, client: &http.Client{Timeout: 5 * time.Second}}
}

func (c *cloudflareVerifier) Verify(ctx context.Context, token, ip string) (bool, error) {
	if strings.TrimSpace(token) == "" {
		return false, nil
	}
	form := url.Values{"secret": {c.secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("build siteverify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("siteverify: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("decode siteverify: %w", err)
	}
	return out.Success, nil
}

// DevVerifier accepts only the token "dev-pass". For local development, never production.
type DevVerifier struct{}

func (DevVerifier) Verify(_ context.Context, token, _ string) (bool, error) {
	return token == "dev-pass", nil
}
