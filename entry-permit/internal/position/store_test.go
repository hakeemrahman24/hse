package position

import (
	"context"
	"database/sql"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/internal/policy"
	"entry-permit/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return d
}

func count(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSeedLoadsTheSheetOnceAndOnlyWhenEmpty(t *testing.T) {
	d := newDB(t)
	s := Store{DB: d}
	ctx := context.Background()
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	// The counts of the owner's PositionAlias sheet (plan 3b): 50 positions, 38 keyword rules, 19 classes.
	want := map[string]int{"position_canonical": 50, "position_alias": 496, "position_keyword": 38, "position_class": 19}
	for table, n := range want {
		if got := count(t, d, table); got != n {
			t.Errorf("%s has %d rows, want %d", table, got, n)
		}
	}
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(t, d, "position_alias"); got != 496 {
		t.Errorf("a second seed changed the data: %d aliases", got)
	}
	// An admin's edit must survive a restart.
	if _, err := d.Exec(`DELETE FROM position_class WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(t, d, "position_class"); got != 18 {
		t.Errorf("seed refilled a table that was already in use: %d", got)
	}
}

func TestResolverFromTheSeedMatchesTheSheetRules(t *testing.T) {
	d := newDB(t)
	s := Store{DB: d}
	ctx := context.Background()
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := policy.Default()
	cases := []struct {
		raw, canonical, cert string
	}{
		{"Op Crane", "Crane Operator", "Y"},                    // layer 1, exact alias
		{"tower crane operator senior", "Crane Operator", "Y"}, // layer 2, keyword
		{"Director", "Executive", "N"},                         // layer 3, classification
		{"zzz unknown job", "zzz unknown job", "N#A"},          // nothing matches
	}
	for _, c := range cases {
		got := r.Resolve(c.raw, p)
		if got.Canonical != c.canonical || got.CertRequired != c.cert {
			t.Errorf("%q: got %q cert %q, want %q cert %q", c.raw, got.Canonical, got.CertRequired, c.canonical, c.cert)
		}
	}
	if got := r.Resolve("Op Crane", p); got.Certificate == "" {
		t.Error("a position that needs a certificate must name it")
	}
}

func TestResolverFromAnEmptyDatabaseKeepsWhatWasTyped(t *testing.T) {
	r, err := Store{DB: newDB(t)}.Resolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve("Welder", policy.Default()); got.Canonical != "Welder" || got.CertRequired != "N#A" {
		t.Errorf("got %+v", got)
	}
}
