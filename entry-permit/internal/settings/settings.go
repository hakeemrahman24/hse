// Package settings stores the adjustable business values in the setting table,
// validates edits, audits them and builds a policy.Policy for the rules.
//
// WHY: the owner wants every rule number editable without a deploy. Rules keep
// receiving a plain Policy value; this package is the only place that knows
// where the numbers live. A bad or missing stored value falls back to the
// default, so a hand edit in the database can never break the rules.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// cacheMinutes: how long a loaded Policy is reused. A save clears it at once
// on this instance; there is one instance, so the edit applies to the next request.
const cacheMinutes = 1

// ValidationError carries a message safe to show the admin.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return ValidationError{fmt.Sprintf(format, a...)} }

// Item is one setting with its stored value, for the page.
type Item struct {
	Def
	Value     string
	IsDefault bool
}

// Group is one card on the page.
type Group struct {
	Name  string
	Items []Item
}

// Service reads and writes settings.
type Service struct {
	db    *sql.DB
	clock dates.Clock

	mu       sync.Mutex
	cached   policy.Policy
	loadedAt dates.Moment
	have     bool
}

// New builds a Service.
func New(db *sql.DB, clock dates.Clock) *Service { return &Service{db: db, clock: clock} }

// Seed inserts a row for every Def that has none and refreshes the texts and
// default of existing rows, but never the value an admin chose.
func (s *Service) Seed(ctx context.Context) error {
	for _, d := range Defs {
		lo, hi := "", ""
		if d.Type == TypeInt {
			lo, hi = strconv.Itoa(d.Min), strconv.Itoa(d.Max)
		}
		res, err := s.db.ExecContext(ctx, `UPDATE setting SET grp=?, label=?, value_type=?, unit=?, default_value=?, min_value=?, max_value=?, description=?
			WHERE setting_key=?`, d.Group, d.Label, d.Type, d.Unit, d.Default, lo, hi, d.Description, d.Key)
		if err != nil {
			return fmt.Errorf("seed setting %s: %w", d.Key, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO setting (setting_key, grp, label, value_type, unit, value, default_value, min_value, max_value, description)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.Key, d.Group, d.Label, d.Type, d.Unit, d.Default, d.Default, lo, hi, d.Description); err != nil {
			return fmt.Errorf("seed setting %s: %w", d.Key, err)
		}
	}
	s.Invalidate()
	return nil
}

// Invalidate drops the cached Policy.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.have = false
	s.mu.Unlock()
}

// Policy returns the current Policy. On a database error it keeps the last
// good copy (or the defaults) and logs, so a hiccup never stops the rules.
func (s *Service) Policy() policy.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if s.have && now < s.loadedAt.AddMinutes(cacheMinutes) {
		return s.cached
	}
	vals, err := s.stored(context.Background())
	if err != nil {
		log.Printf("settings: load failed, keeping last good policy: %v", err)
		if !s.have {
			s.cached, s.have = policy.Default(), true
		}
		s.loadedAt = now
		return s.cached
	}
	p, bad := Build(vals)
	for _, b := range bad {
		log.Printf("settings: %s, using the default", b)
	}
	s.cached, s.loadedAt, s.have = p, now, true
	return p
}

func (s *Service) stored(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT setting_key, value FROM setting`)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()
	vals := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		vals[k] = v
	}
	return vals, rows.Err()
}

// Build makes a Policy from stored values. A missing or invalid value keeps the
// default and is reported in bad.
func Build(vals map[string]string) (policy.Policy, []string) {
	p := policy.Default()
	var bad []string
	fm := fields(&p)
	bm := boolFields(&p)
	for _, d := range Defs {
		v, ok := vals[d.Key]
		if !ok {
			continue
		}
		norm, err := normalise(d, v)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s=%q (%v)", d.Key, v, err))
			continue
		}
		switch {
		case fm[d.Key] != nil:
			n, _ := strconv.Atoi(norm)
			*fm[d.Key] = n
		case d.Key == "mcu_portal_url":
			p.McuPortalURL = norm
		case d.Key == "mcu_lep_grace_unit":
			p.McuLepGraceUnit = norm
		case bm[d.Key] != nil:
			*bm[d.Key] = norm == "on"
		case d.Key == "perpetual_cert_positions":
			p.PerpetualCertPositions = splitList(norm)
		case d.Key == "ticket_completing_checkout_reasons":
			p.CompletingReasons = splitList(norm)
		}
	}
	return p, bad
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalise validates v against d and returns the form that is stored.
func normalise(d Def, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch d.Type {
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", invalid("%s must be a whole number.", d.Label)
		}
		if n < d.Min || n > d.Max {
			return "", invalid("%s must be between %d and %d.", d.Label, d.Min, d.Max)
		}
		return strconv.Itoa(n), nil
	case TypeBool:
		if v != "on" && v != "off" {
			return "", invalid("%s must be on or off.", d.Label)
		}
		return v, nil
	case TypeChoice:
		for _, o := range d.Options {
			if v == o {
				return v, nil
			}
		}
		return "", invalid("%s must be one of: %s.", d.Label, strings.Join(d.Options, ", "))
	case TypeURL:
		if v == "" && d.AllowEmpty {
			return "", nil
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(v, " \t\r\n") {
			return "", invalid("%s must be a full web address starting with https:// (or http://).", d.Label)
		}
		return v, nil
	case TypeList:
		items := splitList(v)
		if len(items) == 0 {
			return "", invalid("%s needs at least one entry.", d.Label)
		}
		return strings.Join(items, ", "), nil
	}
	return "", invalid("Unknown setting type.")
}

// Set validates and saves one value, and audits the change in the same transaction.
func (s *Service) Set(ctx context.Context, actor, key, value string) error {
	d, ok := byKey(key)
	if !ok {
		return invalid("That setting does not exist.")
	}
	norm, err := normalise(d, value)
	if err != nil {
		return err
	}
	return s.write(ctx, actor, d, norm)
}

// Reset puts the default back.
func (s *Service) Reset(ctx context.Context, actor, key string) error {
	d, ok := byKey(key)
	if !ok {
		return invalid("That setting does not exist.")
	}
	return s.write(ctx, actor, d, d.Default)
}

func (s *Service) write(ctx context.Context, actor string, d Def, norm string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var old string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM setting WHERE setting_key=?`, d.Key).Scan(&old); err != nil {
		return fmt.Errorf("read setting %s: %w", d.Key, err)
	}
	if old == norm {
		return tx.Commit()
	}
	now := s.clock.Now()
	if _, err = tx.ExecContext(ctx, `UPDATE setting SET value=?, updated_at=?, updated_by=? WHERE setting_key=?`, norm, string(now), actor, d.Key); err != nil {
		return fmt.Errorf("save setting %s: %w", d.Key, err)
	}
	if err = audit.Write(ctx, tx, now, actor, "setting.update", "setting:"+d.Key, old, norm); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.Invalidate()
	return nil
}

// Groups returns every setting with its stored value, grouped for the page.
func (s *Service) Groups(ctx context.Context) ([]Group, error) {
	vals, err := s.stored(ctx)
	if err != nil {
		return nil, err
	}
	var out []Group
	for _, d := range Defs {
		v, ok := vals[d.Key]
		if !ok {
			v = d.Default
		}
		it := Item{Def: d, Value: v, IsDefault: v == d.Default}
		if n := len(out); n > 0 && out[n-1].Name == d.Group {
			out[n-1].Items = append(out[n-1].Items, it)
		} else {
			out = append(out, Group{Name: d.Group, Items: []Item{it}})
		}
	}
	return out, nil
}
