package position

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

// seedFS holds the owner's PositionAlias sheet as four tab-separated files.
// WHY embedded: the tables must exist and be filled on a fresh install without a
// separate import step, and the data holds job titles only, nothing personal.
//
//go:embed seed/*.tsv
var seedFS embed.FS

// Store loads the lookup tables from the database.
type Store struct{ DB *sql.DB }

// readTSV returns the data rows of an embedded file, each split on tabs, with
// the header line dropped. Rows with the wrong number of columns are an error,
// so a damaged seed file fails loudly instead of loading half a table.
func readTSV(name string, cols int) ([][]string, error) {
	b, err := seedFS.ReadFile("seed/" + name)
	if err != nil {
		return nil, fmt.Errorf("read seed %s: %w", name, err)
	}
	var out [][]string
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != cols {
			return nil, fmt.Errorf("seed %s line %d has %d columns, want %d", name, i+1, len(f), cols)
		}
		out = append(out, f)
	}
	return out, nil
}

// Seed fills the four tables from the embedded sheet, one table at a time and
// only when that table is empty. WHY per table and only when empty: an admin's
// later edits (once the edit pages exist) must survive a restart.
func (s Store) Seed(ctx context.Context) error {
	tables := []struct {
		table, file string
		cols        int
		insert      string
	}{
		{"position_canonical", "canonical.tsv", 5, `INSERT INTO position_canonical (name, category, risk_tier, certificate, cert_required, seq) VALUES (?, ?, ?, ?, ?, ?)`},
		{"position_alias", "alias.tsv", 2, `INSERT INTO position_alias (alias, canonical, seq) VALUES (?, ?, ?)`},
		{"position_keyword", "keyword.tsv", 3, `INSERT INTO position_keyword (keywords, canonical, risk_tier, seq) VALUES (?, ?, ?, ?)`},
		{"position_class", "class.tsv", 3, `INSERT INTO position_class (keywords, classification, risk_tier, seq) VALUES (?, ?, ?, ?)`},
	}
	for _, t := range tables {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.table).Scan(&n); err != nil {
			return fmt.Errorf("count %s: %w", t.table, err)
		}
		if n > 0 {
			continue
		}
		rows, err := readTSV(t.file, t.cols)
		if err != nil {
			return err
		}
		if err := s.insertAll(ctx, t.table, t.insert, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) insertAll(ctx context.Context, table, insert string, rows [][]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed %s: %w", table, err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, r := range rows {
		args := make([]any, 0, len(r)+1)
		for _, v := range r {
			args = append(args, v)
		}
		args = append(args, i+1)
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			return fmt.Errorf("seed %s row %d: %w", table, i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed %s: %w", table, err)
	}
	return nil
}

// Resolver reads the four tables, in row order, and builds the lookup.
func (s Store) Resolver(ctx context.Context) (*Resolver, error) {
	var canon []Canonical
	if err := s.scan(ctx, `SELECT name, category, risk_tier, certificate, cert_required FROM position_canonical ORDER BY seq`, 5, func(v []string) {
		canon = append(canon, Canonical{Name: v[0], Category: v[1], RiskTier: v[2], Certificate: v[3], CertRequired: v[4]})
	}); err != nil {
		return nil, err
	}
	var aliases []Alias
	if err := s.scan(ctx, `SELECT alias, canonical FROM position_alias ORDER BY seq`, 2, func(v []string) {
		aliases = append(aliases, Alias{Alias: v[0], Canonical: v[1]})
	}); err != nil {
		return nil, err
	}
	var kws []KeywordRule
	if err := s.scan(ctx, `SELECT keywords, canonical, risk_tier FROM position_keyword ORDER BY seq`, 3, func(v []string) {
		kws = append(kws, KeywordRule{Keywords: v[0], Canonical: v[1], RiskTier: v[2]})
	}); err != nil {
		return nil, err
	}
	var classes []ClassRule
	if err := s.scan(ctx, `SELECT keywords, classification, risk_tier FROM position_class ORDER BY seq`, 3, func(v []string) {
		classes = append(classes, ClassRule{Keywords: v[0], Classification: v[1], RiskTier: v[2]})
	}); err != nil {
		return nil, err
	}
	return NewResolver(canon, aliases, kws, classes), nil
}

// scan runs a query whose columns are all text and hands each row to add.
func (s Store) scan(ctx context.Context, query string, cols int, add func([]string)) error {
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read positions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		v := make([]string, cols)
		ptrs := make([]any, cols)
		for i := range v {
			ptrs[i] = &v[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan positions: %w", err)
		}
		add(v)
	}
	return rows.Err()
}
