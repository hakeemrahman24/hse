// Package turnstile verifies Cloudflare Turnstile tokens on the server. It is shared by the public
// pages (/sofi and the medical checkup submission portal) so both check the same way.
package turnstile

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
	secret    string
	client    *http.Client
	url       string              // siteVerifyURL; a field only so tests can point it at a fake server
	hostnames map[string]struct{} // lower-case; empty means any hostname is accepted
}

// NewCloudflareVerifier verifies tokens against Cloudflare with the site's secret key. With
// hostnames, a token also has to have been solved on one of them: Cloudflare reports the page
// hostname in its reply. WHY optional: one widget can list several hostnames (a test address and
// the real one), and the list says which this server accepts. Leaving it out keeps the plain check.
func NewCloudflareVerifier(secret string, hostnames ...string) Verifier {
	allowed := map[string]struct{}{}
	for _, h := range hostnames {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			allowed[h] = struct{}{}
		}
	}
	return &cloudflareVerifier{secret: secret, client: &http.Client{Timeout: 5 * time.Second}, url: siteVerifyURL, hostnames: allowed}
}

func (c *cloudflareVerifier) Verify(ctx context.Context, token, ip string) (bool, error) {
	if strings.TrimSpace(token) == "" {
		return false, nil
	}
	form := url.Values{"secret": {c.secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(form.Encode()))
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
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("decode siteverify: %w", err)
	}
	if !out.Success {
		return false, nil
	}
	if len(c.hostnames) > 0 {
		// A missing hostname fails too: with a list set, "unknown" is not "allowed".
		if _, ok := c.hostnames[strings.ToLower(strings.TrimSpace(out.Hostname))]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// DevVerifier accepts only the token "dev-pass". For local development, never production.
type DevVerifier struct{}

func (DevVerifier) Verify(_ context.Context, token, _ string) (bool, error) {
	return token == "dev-pass", nil
}
