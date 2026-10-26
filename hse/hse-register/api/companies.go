package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// companySource lists the company names for the "responsible company" dropdown. The real one
// is hse-medpost (below); development uses a fixed list.
type companySource interface {
	Companies(ctx context.Context) ([]string, error)
}

const companyCacheTTL = time.Hour

// companyCache keeps the last list for an hour so the dropdown does not call Medpost on every page.
// If Medpost cannot be reached, the older list is still served; with no list at all the form
// still works, because the field also accepts a typed name.
type companyCache struct {
	src companySource
	ttl time.Duration

	mu   sync.Mutex
	list []string
	at   time.Time
}

func newCompanyCache(src companySource) *companyCache {
	return &companyCache{src: src, ttl: companyCacheTTL}
}

// Reset drops the cached list, so the next List asks the (possibly changed) source again.
func (c *companyCache) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.list, c.at = nil, time.Time{}
	c.mu.Unlock()
}

// List never fails: it returns the cached, a fresh or an empty list.
func (c *companyCache) List(ctx context.Context) []string {
	if c == nil || c.src == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && time.Since(c.at) < c.ttl {
		return c.list
	}
	list, err := c.src.Companies(ctx)
	if err != nil {
		return c.list // stale or nil; nothing from Medpost's answer is logged
	}
	c.list, c.at = list, time.Now()
	return list
}

// Companies reads the distinct company names from hse-medpost's /api/directory/fitness, the
// existing endpoint that lists every employee with a current Fit result. So a company appears in
// the list while it has at least one Fit employee.
//
// Only the company field is decoded; everything else in each row (date of birth, medical notes)
// is dropped as soon as the row is parsed and is never stored or logged.
func (m *medpostDirectory) Companies(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/api/directory/fitness", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second} // the full list is larger than a name search
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("medpost answered %d", resp.StatusCode)
	}

	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<20))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, fmt.Errorf("medpost answer could not be read")
	}
	seen := map[string]string{} // lower-case -> first spelling
	for dec.More() {
		var row struct {
			Company string `json:"company"`
		}
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("medpost answer could not be read: %w", err)
		}
		name := strings.Join(strings.Fields(row.Company), " ")
		if name == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(name)]; !ok {
			seen[strings.ToLower(name)] = name
		}
	}
	out := make([]string, 0, len(seen))
	for _, n := range seen {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, nil
}

// devCompanySource is the list used when hse-medpost is not configured: the fixed development
// list under SOC_DEV_STUBS=1, otherwise none (the field then accepts typed names).
func devCompanySource() companySource {
	if os.Getenv("SOC_DEV_STUBS") == "1" {
		return devCompanies{}
	}
	return nil
}

// devCompanies is the development list (SOC_DEV_STUBS=1), the same invented companies as the
// fake employees.
type devCompanies struct{}

func (devCompanies) Companies(context.Context) ([]string, error) {
	return []string{"LAYAR NUSANTARA GAS", "SALAWATI MOTORINDO", "STARWAY ENGINEERING INDONESIA"}, nil
}
