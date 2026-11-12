// Package region checks province and regency names against the Indonesian
// administrative-area data that hse-medpost serves on /api/regions
// (indonesia-region/data/region.tsv, one "code<TAB>name" line per area: a
// 2-digit code is a province, "11.01" a regency or city).
//
// WHY: the permit forms take the regency and province from that API, so any
// list that is later compared with a permit's address (the malaria list) must
// use the API's exact spelling and province. The owner decided the API is the
// correct source. Names in a sheet are spelled by people, so matching ignores
// case, spaces and punctuation, expands "Kep.", and keeps Kabupaten and Kota
// apart (Kabupaten Sorong and Kota Sorong are different places).
package region

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var regencyCode = regexp.MustCompile(`^\d{2}\.\d{2}$`)

// Regency is one kabupaten or kota with its province.
type Regency struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Province string `json:"province"`
}

// Index looks regencies up by a normalised name.
type Index struct {
	byKey map[string][]Regency
}

// aliases maps a spelling used in the sheet to the official name in the
// dataset, for places that were renamed or are spelled differently. Both sides
// are normal names, normalised by Key.
var aliases = map[string]string{
	Key("Kabupaten Kepulauan Sitaro"):       Key("Kabupaten Kep. Siau Tagulandang Biaro"), // Sitaro is the short name
	Key("Kabupaten Siau Tagulandang Biaro"): Key("Kabupaten Kep. Siau Tagulandang Biaro"), // sheet leaves out "Kepulauan"
	Key("Kabupaten Maluku Tenggara Barat"):  Key("Kabupaten Kepulauan Tanimbar"),          // renamed in 2020
}

// Key normalises a regency name for matching.
func Key(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "kep.", "kepulauan")
	kind := "kab"
	switch {
	case strings.HasPrefix(s, "kota "):
		kind, s = "kota", s[len("kota "):]
	case strings.HasPrefix(s, "kabupaten "):
		s = s[len("kabupaten "):]
	case strings.HasPrefix(s, "kab. "):
		s = s[len("kab. "):]
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return kind + ":" + b.String()
}

// ReadTSV reads a region.tsv into regencies with their province. Districts
// and villages are ignored.
func ReadTSV(r io.Reader) ([]Regency, error) {
	provinces := map[string]string{}
	var regencies []Regency
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		code, name, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("region line %d: expected code<TAB>name", n)
		}
		switch {
		case len(code) == 2:
			provinces[code] = name
		case regencyCode.MatchString(code):
			regencies = append(regencies, Regency{Code: code, Name: name})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read regions: %w", err)
	}
	if len(provinces) == 0 || len(regencies) == 0 {
		return nil, fmt.Errorf("region file has %d provinces and %d regencies", len(provinces), len(regencies))
	}
	for i := range regencies {
		regencies[i].Province = provinces[regencies[i].Code[:2]]
	}
	return regencies, nil
}

// NewIndex builds a lookup from regencies.
func NewIndex(regencies []Regency) *Index {
	ix := &Index{byKey: map[string][]Regency{}}
	for _, rg := range regencies {
		k := Key(rg.Name)
		ix.byKey[k] = append(ix.byKey[k], rg)
	}
	return ix
}

// LoadIndex reads a region.tsv into an Index.
func LoadIndex(r io.Reader) (*Index, error) {
	regencies, err := ReadTSV(r)
	if err != nil {
		return nil, err
	}
	return NewIndex(regencies), nil
}

// Source supplies the regency list. In production it is hse-medpost's
// /api/directory/regions; the file source is for development and the command
// line tools.
type Source interface {
	Regencies(ctx context.Context) ([]Regency, error)
}

const (
	httpTimeout  = 10 * time.Second
	maxBodyBytes = 8 << 20 // the full list is about 60 KB; this only stops a runaway reply
)

// HTTPSource reads hse-medpost's token-guarded region route.
type HTTPSource struct {
	BaseURL, Token string
	Client         *http.Client // nil uses a client with a 10 second timeout
}

// Regencies fetches the list. The token is sent only to BaseURL and never logged.
func (s HTTPSource) Regencies(ctx context.Context) ([]Regency, error) {
	if s.BaseURL == "" || s.Token == "" {
		return nil, errors.New("medpost region API is not configured (MEDPOST_API_URL / MEDPOST_API_TOKEN)")
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/api/directory/regions", nil)
	if err != nil {
		return nil, fmt.Errorf("build region request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost region API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("medpost region API answered %d", resp.StatusCode)
	}
	var rows []Regency
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("medpost region API sent bad data: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("medpost region API sent an empty list")
	}
	return rows, nil
}

// FileSource reads a region.tsv (development and offline use only).
type FileSource struct{ Path string }

// Regencies reads the file.
func (s FileSource) Regencies(context.Context) ([]Regency, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, fmt.Errorf("open region file: %w", err)
	}
	defer f.Close()
	return ReadTSV(f)
}

// Cached keeps the last good answer for TTL. WHY: the admin page asks for the
// list on every view, and the data changes only when medpost reloads it. If
// medpost is briefly down, the last good copy is still served.
type Cached struct {
	Src Source
	TTL time.Duration

	mu   sync.Mutex
	at   time.Time
	data []Regency
}

// Regencies returns the cached list, refreshing it when older than TTL.
func (c *Cached) Regencies(ctx context.Context) ([]Regency, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && time.Since(c.at) < c.TTL {
		return c.data, nil
	}
	fresh, err := c.Src.Regencies(ctx)
	if err != nil {
		if c.data != nil {
			return c.data, nil // stale beats nothing
		}
		return nil, err
	}
	c.data, c.at = fresh, time.Now()
	return c.data, nil
}

// Find returns the regencies matching name (more than one when the same name
// exists in several provinces).
func (ix *Index) Find(name string) []Regency {
	k := Key(name)
	if official, ok := aliases[k]; ok {
		k = official
	}
	return ix.byKey[k]
}

// Row is one line of a list: endemicity, regency, province.
type Row struct {
	Line                          int
	Endemicity, Regency, Province string
}

// Aligned is the outcome of Align. Changes explains every edit; Problems are
// lines a person must decide (nothing should be loaded while there are any).
type Aligned struct {
	Rows     []Row
	Changes  []string
	Problems []string
}

// Align replaces each row's regency and province with the API's official
// ones, then drops rows that became exact duplicates (an old and a new name of
// the same regency, or a regency listed under an old and a new province).
// It never guesses: an unknown regency, a name found in several provinces
// where none matches the row, or duplicates with different endemicity are
// problems.
func (ix *Index) Align(rows []Row) Aligned {
	var out Aligned
	firstLine := map[string]Row{}
	for _, r := range rows {
		found := ix.Find(r.Regency)
		var pick *Regency
		switch len(found) {
		case 0:
			out.Problems = append(out.Problems, fmt.Sprintf("line %d: %s is not in the region data", r.Line, r.Regency))
			continue
		case 1:
			pick = &found[0]
		default:
			for i := range found {
				if strings.EqualFold(found[i].Province, strings.TrimSpace(r.Province)) {
					pick = &found[i]
				}
			}
			if pick == nil {
				out.Problems = append(out.Problems, fmt.Sprintf("line %d: %s exists in several provinces and none is %s", r.Line, r.Regency, r.Province))
				continue
			}
		}
		if pick.Name != r.Regency {
			out.Changes = append(out.Changes, fmt.Sprintf("line %d: name %s -> %s", r.Line, r.Regency, pick.Name))
		}
		if pick.Province != r.Province {
			out.Changes = append(out.Changes, fmt.Sprintf("line %d: %s: province %s -> %s", r.Line, pick.Name, r.Province, pick.Province))
		}
		aligned := Row{Line: r.Line, Endemicity: r.Endemicity, Regency: pick.Name, Province: pick.Province}
		key := strings.ToLower(aligned.Regency + "|" + aligned.Province)
		if first, dup := firstLine[key]; dup {
			if first.Endemicity != aligned.Endemicity {
				out.Problems = append(out.Problems, fmt.Sprintf("line %d: %s is the same regency as line %d but the endemicity differs (%s / %s)",
					r.Line, aligned.Regency, first.Line, aligned.Endemicity, first.Endemicity))
				continue
			}
			out.Changes = append(out.Changes, fmt.Sprintf("line %d: dropped, same regency as line %d", r.Line, first.Line))
			continue
		}
		firstLine[key] = aligned
		out.Rows = append(out.Rows, aligned)
	}
	return out
}
