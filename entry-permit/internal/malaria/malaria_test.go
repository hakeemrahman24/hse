package malaria

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
	"entry-permit/migrations"
)

var ctx = context.Background()

type fakeSource struct {
	list []region.Regency
	err  error
}

func (f fakeSource) Regencies(context.Context) ([]region.Regency, error) { return f.list, f.err }

var regions = []region.Regency{
	{Code: "11.01", Name: "Kabupaten Aceh Selatan", Province: "Aceh"},
	{Code: "96.01", Name: "Kabupaten Sorong", Province: "Papua Barat Daya"},
	{Code: "96.71", Name: "Kota Sorong", Province: "Papua Barat Daya"},
	{Code: "63.02", Name: "Kabupaten Kotabaru", Province: "Kalimantan Selatan"},
}

func newService(t *testing.T, src region.Source) (*Service, audit.Queryer) {
	t.Helper()
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return &Service{Lists: reflist.NewStore(d, dates.FixedClock("2026-11-06 08:00:00")), Regions: src}, d
}

func TestMalariaListIsDefinedAndManagedByThisPage(t *testing.T) {
	d, ok := reflist.Find("malaria")
	if !ok || d.CustomPath == "" || d.Path() != d.CustomPath {
		t.Fatalf("malaria def: %+v", d)
	}
	if g, _ := reflist.Find("nationality"); g.Path() != "/staff/admin/lists/nationality" {
		t.Errorf("generic path: %s", g.Path())
	}
}

func TestRowsListAllRegenciesFromTheAPI(t *testing.T) {
	s, _ := newService(t, fakeSource{list: regions})
	if err := s.Set(ctx, "arif", "96.01", "Tinggi"); err != nil {
		t.Fatal(err)
	}
	pg, err := s.Page(ctx, Filter{})
	all, provinces := pg.Rows, pg.Provinces
	if err != nil || len(all) != 4 || len(provinces) != 3 {
		t.Fatalf("%v %v %v", all, provinces, err)
	}
	if provinces[0] != "Aceh" || provinces[2] != "Papua Barat Daya" {
		t.Errorf("provinces not sorted: %v", provinces)
	}
	listed := rowsOf(t, s, Filter{OnlyListed: true})
	if len(listed) != 1 || listed[0].Regency != "Kabupaten Sorong" || listed[0].Endemicity != "Tinggi" {
		t.Errorf("only listed: %+v", listed)
	}
	byProv := rowsOf(t, s, Filter{Province: "Papua Barat Daya"})
	if len(byProv) != 2 {
		t.Errorf("province filter: %+v", byProv)
	}
	byText := rowsOf(t, s, Filter{Q: " sorong "})
	if len(byText) != 2 {
		t.Errorf("text filter: %+v", byText)
	}
	none := rowsOf(t, s, Filter{Q: "zzz"})
	if len(none) != 0 {
		t.Errorf("no match: %+v", none)
	}
}

func TestSetAddsChangesRemovesAndRestores(t *testing.T) {
	s, q := newService(t, fakeSource{list: regions})
	steps := []struct{ level, want string }{
		{"rendah", "Rendah"}, // add (case-insensitive input)
		{"Sedang", "Sedang"}, // change
		{"Sedang", "Sedang"}, // same value: no change
		{"", ""},             // take off the list: archived
		{"", ""},             // already off: no-op
		{"Tinggi", "Tinggi"}, // back on: restored with a new value
	}
	for i, st := range steps {
		if err := s.Set(ctx, "arif", "11.01", st.level); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		got, err := s.Lookup(ctx, "", "Kabupaten Aceh Selatan", "Aceh")
		if err != nil || got != st.want {
			t.Errorf("step %d: %q %v want %q", i, got, err, st.want)
		}
	}
	// add, update, archive, restore, update: five audit rows (no-ops write none).
	log, _ := audit.Recent(ctx, q, 20)
	if len(log) != 5 {
		t.Errorf("audit rows: %d %+v", len(log), log)
	}
	// Removing a regency that was never listed does nothing.
	if err := s.Set(ctx, "arif", "63.02", ""); err != nil {
		t.Error(err)
	}
	if got, _ := s.Lookup(ctx, "", "Kabupaten Kotabaru", "Kalimantan Selatan"); got != "" {
		t.Errorf("unlisted regency has level %q", got)
	}
}

func TestSetRefusesAnythingNotFromTheAPI(t *testing.T) {
	s, _ := newService(t, fakeSource{list: regions})
	if err := s.Set(ctx, "arif", "99.99", "Tinggi"); !errors.Is(err, ErrUnknownRegion) {
		t.Errorf("unknown code: %v", err)
	}
	if err := s.Set(ctx, "arif", "11.01", "Parah"); !errors.Is(err, ErrBadEndemicity) {
		t.Errorf("bad level: %v", err)
	}
}

func TestSourceProblems(t *testing.T) {
	for name, src := range map[string]region.Source{
		"none":  nil,
		"error": fakeSource{err: errors.New("down")},
	} {
		s, _ := newService(t, src)
		if _, err := s.Page(ctx, Filter{}); !errors.Is(err, ErrNoSource) {
			t.Errorf("%s rows: %v", name, err)
		}
		if err := s.Set(ctx, "a", "11.01", "Tinggi"); !errors.Is(err, ErrNoSource) {
			t.Errorf("%s set: %v", name, err)
		}
	}
}

func rowsOf(t *testing.T, s *Service, f Filter) []Row {
	t.Helper()
	pg, err := s.Page(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	return pg.Rows
}

// storeLegacy puts an item in the list the way the old seed did: no code.
func storeLegacy(t *testing.T, s *Service, level, regency, province string) {
	t.Helper()
	d, _ := reflist.Find("malaria")
	if _, err := s.Lists.Add(ctx, "seed", d, []string{level, regency, province, ""}); err != nil {
		t.Fatal(err)
	}
}

func TestOldItemsWithoutACodeGetItBackfilled(t *testing.T) {
	s, q := newService(t, fakeSource{list: regions})
	storeLegacy(t, s, "Tinggi", "Kabupaten Sorong", "Papua Barat Daya")
	pg, err := s.Page(ctx, Filter{OnlyListed: true})
	if err != nil || len(pg.Rows) != 1 || pg.Rows[0].Endemicity != "Tinggi" || len(pg.Orphans) != 0 {
		t.Fatalf("%+v %v", pg, err)
	}
	// Second view changes nothing more: one audit row for the backfill only.
	_, _ = s.Page(ctx, Filter{})
	log, _ := audit.Recent(ctx, q, 20)
	if len(log) != 2 || log[0].Actor != "system" {
		t.Errorf("audit: %+v", log)
	}
	if got, _ := s.Lookup(ctx, "96.01", "x", "y"); got != "Tinggi" {
		t.Errorf("lookup by code: %q", got)
	}
}

func TestRenamedRegencyIsFoundByCodeAndHealed(t *testing.T) {
	old := []region.Regency{{Code: "63.02", Name: "Kabupaten Kota Baru", Province: "Kalimantan Selatan"}}
	s, q := newService(t, fakeSource{list: old})
	if err := s.Set(ctx, "arif", "63.02", "Sedang"); err != nil {
		t.Fatal(err)
	}
	// medpost renames it and moves it to another province.
	s.Regions = fakeSource{list: []region.Regency{{Code: "63.02", Name: "Kabupaten Kotabaru", Province: "Kalsel Baru"}}}
	if got, _ := s.Lookup(ctx, "63.02", "Kabupaten Kotabaru", "Kalsel Baru"); got != "Sedang" {
		t.Errorf("lookup before heal: %q", got)
	}
	if n, err := s.OrphanCount(ctx); err != nil || n != 0 {
		t.Errorf("a renamed regency is not an orphan: %d %v", n, err)
	}
	pg, err := s.Page(ctx, Filter{OnlyListed: true})
	if err != nil || len(pg.Rows) != 1 || pg.Rows[0].Regency != "Kabupaten Kotabaru" || pg.Rows[0].Endemicity != "Sedang" {
		t.Fatalf("%+v %v", pg, err)
	}
	if got, _ := s.Lookup(ctx, "", "Kabupaten Kotabaru", "Kalsel Baru"); got != "Sedang" {
		t.Errorf("lookup by new name after heal: %q", got)
	}
	log, _ := audit.Recent(ctx, q, 20)
	if len(log) != 2 || log[0].Actor != "system" {
		t.Errorf("heal not audited: %+v", log)
	}
}

func TestOrphansAreShownAndCanBeRelinkedOrArchived(t *testing.T) {
	s, _ := newService(t, fakeSource{list: regions})
	storeLegacy(t, s, "Tinggi", "Kabupaten Hilang", "Aceh")
	storeLegacy(t, s, "Rendah", "Kabupaten Lama", "Aceh")
	if n, _ := s.OrphanCount(ctx); n != 2 {
		t.Fatalf("orphan count %d", n)
	}
	pg, _ := s.Page(ctx, Filter{})
	if len(pg.Orphans) != 2 || len(pg.Regencies) != 4 {
		t.Fatalf("%+v", pg)
	}
	hilang, lama := pg.Orphans[0], pg.Orphans[1]
	if err := s.Relink(ctx, "arif", hilang.ItemID, "11.01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Lookup(ctx, "11.01", "", ""); got != "Tinggi" {
		t.Errorf("relink should carry the level: %q", got)
	}
	if err := s.ArchiveOrphan(ctx, "arif", lama.ItemID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.OrphanCount(ctx); n != 0 {
		t.Errorf("orphans left: %d", n)
	}
	// Things that are not orphans, or not in the API, are refused.
	if err := s.ArchiveOrphan(ctx, "arif", hilang.ItemID); !errors.Is(err, ErrNotOrphan) {
		t.Errorf("archive non-orphan: %v", err)
	}
	if err := s.Relink(ctx, "arif", hilang.ItemID, "11.01"); !errors.Is(err, ErrNotOrphan) {
		t.Errorf("relink non-orphan: %v", err)
	}
}

func TestRelinkRules(t *testing.T) {
	s, _ := newService(t, fakeSource{list: regions})
	if err := s.Set(ctx, "arif", "96.01", "Sedang"); err != nil {
		t.Fatal(err)
	}
	storeLegacy(t, s, "Tinggi", "Kabupaten Hilang", "Aceh")
	pg, _ := s.Page(ctx, Filter{})
	id := pg.Orphans[0].ItemID
	if err := s.Relink(ctx, "arif", id, "99.99"); !errors.Is(err, ErrUnknownRegion) {
		t.Errorf("unknown target: %v", err)
	}
	if err := s.Relink(ctx, "arif", id, "96.01"); !errors.Is(err, ErrAlreadyListed) {
		t.Errorf("target already listed: %v", err)
	}
	// A target that was listed and archived is restored with the orphan's level.
	if err := s.Set(ctx, "arif", "96.01", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Relink(ctx, "arif", id, "96.01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Lookup(ctx, "96.01", "", ""); got != "Tinggi" {
		t.Errorf("restored level: %q", got)
	}
	if n, _ := s.OrphanCount(ctx); n != 0 {
		t.Errorf("orphans left: %d", n)
	}
}

func TestRenameOntoAnExistingNameBecomesAnOrphanNotAnError(t *testing.T) {
	// Two stored items would end up with the same API name: the second stays for the admin.
	s, _ := newService(t, fakeSource{list: regions})
	storeLegacy(t, s, "Tinggi", "Kabupaten Sorong", "Papua Barat Daya")
	d, _ := reflist.Find("malaria")
	if _, err := s.Lists.Add(ctx, "seed", d, []string{"Rendah", "Kabupaten Sorong Lama", "Papua Barat Daya", "96.01"}); err != nil {
		t.Fatal(err)
	}
	pg, err := s.Page(ctx, Filter{})
	if err != nil || len(pg.Orphans) != 1 {
		t.Fatalf("%+v %v", pg, err)
	}
}
