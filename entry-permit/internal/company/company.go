// Package company keeps a read-only mirror of hse-medpost's company list.
//
// WHY a mirror and not live calls (plan 4.3): the company is edited in one
// place, medpost. The permit and waiver forms must keep working while medpost
// restarts, and a permit snapshots the company text when it is submitted anyway,
// so a copy refreshed nightly (and by an admin's "Refresh now") is enough.
//
// Nothing in this app edits a company. Sync replaces the whole copy in one
// transaction, so a reader never sees half of an old list and half of a new one.
package company

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
)

const (
	httpTimeout  = 15 * time.Second
	maxBodyBytes = 8 << 20
	// A sync that would drop more than this share of the mirror (and at least
	// minShrink companies) looks like a bad answer, not real removals.
	suspiciousShrink = 0.5
	minShrink        = 10
)

// ErrNoSource: medpost is not configured.
var ErrNoSource = errors.New("The company list from hse-medpost is not available. Check that hse-medpost is running and the connection settings are filled in.")

// ErrBadData: the answer from medpost cannot be trusted, so nothing was changed.
var ErrBadData = errors.New("hse-medpost sent a company list that looks wrong, so the saved list was not changed")

// Parent is a company above another, with its distance (1 = direct parent).
type Parent struct {
	Name  string `json:"name"`
	Depth int    `json:"depth"`
}

// Company is one entry of the list.
type Company struct {
	ID           int64    `json:"companyId"`
	Name         string   `json:"name"`
	Abbreviation string   `json:"abbreviation"`
	Entity       string   `json:"entity"`
	Gov          bool     `json:"gov"`
	Archived     bool     `json:"archived"`
	Parents      []Parent `json:"parents"`
}

// Source returns the current list from medpost.
type Source interface {
	Companies(ctx context.Context) ([]Company, error)
}

// HTTPSource reads hse-medpost's token-guarded companies route.
type HTTPSource struct {
	BaseURL, Token string
	Client         *http.Client // nil uses a client with a timeout
}

// Companies fetches the list. The token goes only to BaseURL and is never logged.
func (s HTTPSource) Companies(ctx context.Context) ([]Company, error) {
	if s.BaseURL == "" || s.Token == "" {
		return nil, ErrNoSource
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/api/directory/companies", nil)
	if err != nil {
		return nil, fmt.Errorf("build company request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost company API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("medpost company API answered %d", resp.StatusCode)
	}
	var rows []Company
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("medpost company API sent bad data: %w", err)
	}
	return rows, nil
}

// Store is the mirror in the database.
type Store struct {
	DB    *sql.DB
	Clock dates.Clock
}

// Result says what a sync changed.
type Result struct {
	Total, Added, Changed, Removed int
}

// Any reports whether the mirror differs from before.
func (r Result) Any() bool { return r.Added+r.Changed+r.Removed > 0 }

// clean validates and normalises what medpost sent: every company needs an id
// and a name, ids and (case-insensitive) names must be unique, and parent lists
// are put in a fixed order so two syncs of the same data compare equal.
func clean(in []Company) ([]Company, error) {
	ids := map[int64]bool{}
	names := map[string]bool{}
	out := make([]Company, 0, len(in))
	for _, c := range in {
		c.Name = strings.Join(strings.Fields(c.Name), " ")
		key := strings.ToLower(c.Name)
		if c.ID <= 0 || c.Name == "" || ids[c.ID] || names[key] {
			return nil, ErrBadData
		}
		ids[c.ID], names[key] = true, true
		c.Abbreviation = strings.TrimSpace(c.Abbreviation)
		c.Entity = strings.TrimSpace(c.Entity)
		ps := make([]Parent, 0, len(c.Parents))
		for _, p := range c.Parents {
			if n := strings.Join(strings.Fields(p.Name), " "); n != "" && p.Depth > 0 {
				ps = append(ps, Parent{Name: n, Depth: p.Depth})
			}
		}
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Depth != ps[j].Depth {
				return ps[i].Depth < ps[j].Depth
			}
			return ps[i].Name < ps[j].Name
		})
		c.Parents = ps
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// fingerprint is a comparable summary of one company, to count what changed.
func fingerprint(c Company) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%t|%t", c.Name, c.Abbreviation, c.Entity, c.Gov, c.Archived)
	for _, p := range c.Parents {
		fmt.Fprintf(&b, "|%d:%s", p.Depth, p.Name)
	}
	return b.String()
}

// Sync replaces the mirror with what src returns. It changes nothing when the
// answer looks wrong: an error, an empty list, bad rows, or a list that would
// drop more than half of a real mirror at once. The audit row is written in the
// same transaction, and only when something changed.
func (s *Store) Sync(ctx context.Context, src Source, actor string) (Result, error) {
	if src == nil {
		return Result{}, ErrNoSource
	}
	got, err := src.Companies(ctx)
	if err != nil {
		return Result{}, err
	}
	list, err := clean(got)
	if err != nil {
		return Result{}, err
	}
	if len(list) == 0 {
		return Result{}, ErrBadData
	}
	old, err := s.List(ctx, true)
	if err != nil {
		return Result{}, err
	}
	res := diff(old, list)
	if res.Removed >= minShrink && float64(res.Removed) > suspiciousShrink*float64(len(old)) {
		return Result{}, ErrBadData
	}
	now := s.Clock.Now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin company sync: %w", err)
	}
	defer tx.Rollback()
	if err := replace(ctx, tx, list, now); err != nil {
		return Result{}, err
	}
	if res.Any() {
		if err := audit.Write(ctx, tx, now, actor, "company.sync", "company", fmt.Sprintf("%d companies", len(old)),
			fmt.Sprintf("%d companies: %d added, %d changed, %d removed", len(list), res.Added, res.Changed, res.Removed)); err != nil {
			return Result{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit company sync: %w", err)
	}
	return res, nil
}

func diff(old, now []Company) Result {
	before := map[int64]string{}
	for _, c := range old {
		before[c.ID] = fingerprint(c)
	}
	res := Result{Total: len(now)}
	seen := map[int64]bool{}
	for _, c := range now {
		seen[c.ID] = true
		switch fp, ok := before[c.ID]; {
		case !ok:
			res.Added++
		case fp != fingerprint(c):
			res.Changed++
		}
	}
	for id := range before {
		if !seen[id] {
			res.Removed++
		}
	}
	return res
}

func replace(ctx context.Context, tx *sql.Tx, list []Company, now dates.Moment) error {
	for _, q := range []string{`DELETE FROM company_parent`, `DELETE FROM company`, `DELETE FROM company_sync`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("clear company mirror: %w", err)
		}
	}
	for _, c := range list {
		if _, err := tx.ExecContext(ctx, `INSERT INTO company (company_id, name, abbreviation, entity, gov, archived, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, c.Name, c.Abbreviation, c.Entity, b2i(c.Gov), b2i(c.Archived), string(now)); err != nil {
			return fmt.Errorf("save company %d: %w", c.ID, err)
		}
		for _, p := range c.Parents {
			if _, err := tx.ExecContext(ctx, `INSERT INTO company_parent (company_id, depth, parent_name) VALUES (?, ?, ?)`,
				c.ID, p.Depth, p.Name); err != nil {
				return fmt.Errorf("save parent of company %d: %w", c.ID, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO company_sync (id, at, total) VALUES (1, ?, ?)`, string(now), len(list)); err != nil {
		return fmt.Errorf("save sync time: %w", err)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// List returns the mirror ordered by name. Archived companies are left out
// unless includeArchived (the form pickers leave them out; old permits keep the name).
func (s *Store) List(ctx context.Context, includeArchived bool) ([]Company, error) {
	q := `SELECT company_id, name, abbreviation, entity, gov, archived FROM company`
	if !includeArchived {
		q += ` WHERE archived = 0`
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	defer rows.Close()
	var out []Company
	index := map[int64]int{}
	for rows.Next() {
		var c Company
		var gov, arch int
		if err := rows.Scan(&c.ID, &c.Name, &c.Abbreviation, &c.Entity, &gov, &arch); err != nil {
			return nil, fmt.Errorf("scan company: %w", err)
		}
		c.Gov, c.Archived, c.Parents = gov == 1, arch == 1, []Parent{}
		index[c.ID] = len(out)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	prows, err := s.DB.QueryContext(ctx, `SELECT company_id, depth, parent_name FROM company_parent ORDER BY company_id, depth, parent_name`)
	if err != nil {
		return nil, fmt.Errorf("list company parents: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var id int64
		var p Parent
		if err := prows.Scan(&id, &p.Depth, &p.Name); err != nil {
			return nil, fmt.Errorf("scan company parent: %w", err)
		}
		if i, ok := index[id]; ok {
			out[i].Parents = append(out[i].Parents, p)
		}
	}
	return out, prows.Err()
}

// Find returns the company with exactly this id, archived or not.
func (s *Store) Find(ctx context.Context, id int64) (Company, bool, error) {
	all, err := s.List(ctx, true)
	if err != nil {
		return Company{}, false, err
	}
	for _, c := range all {
		if c.ID == id {
			return c, true, nil
		}
	}
	return Company{}, false, nil
}

// LastSync returns when the mirror was last refreshed ("" if never) and its size.
func (s *Store) LastSync(ctx context.Context) (string, int, error) {
	var at string
	var total int
	err := s.DB.QueryRowContext(ctx, `SELECT at, total FROM company_sync WHERE id = 1`).Scan(&at, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("read company sync time: %w", err)
	}
	return at, total, nil
}
