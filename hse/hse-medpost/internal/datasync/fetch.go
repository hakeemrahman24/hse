package datasync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	fetchTimeout     = 2 * time.Minute
	maxDownloadBytes = 40 << 20 // the region file is about 8 MB; this only stops a runaway reply
	userAgent        = "hse-medpost-datasync"
)

// Fetcher downloads a URL as bytes. Production uses HTTPFetcher; tests pass a fake.
type Fetcher interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher fetches over HTTPS, and only from the listed host prefixes, so a value read out of a
// downloaded file can never send the server somewhere else.
type HTTPFetcher struct {
	Client       *http.Client // nil uses a client with a 2 minute timeout
	AllowedHosts []string     // URL prefixes, e.g. "https://raw.githubusercontent.com/"
}

// Get downloads url.
func (f HTTPFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	allowed := false
	for _, prefix := range f.AllowedHosts {
		if strings.HasPrefix(url, prefix) {
			allowed = true
		}
	}
	if !allowed {
		return nil, fmt.Errorf("refusing to fetch %s", url)
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: answered %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxDownloadBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, maxDownloadBytes)
	}
	return body, nil
}
