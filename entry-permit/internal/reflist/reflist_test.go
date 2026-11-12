package reflist

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/migrations"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Store, *testDB) {
	t.Helper()
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return NewStore(d, dates.FixedClock("2026-11-06 08:00:00")), &testDB{d}
}

type testDB struct{ audit.Queryer }

func mustFind(t *testing.T, key string) Def {
	t.Helper()
	d, ok := Find(key)
	if !ok {
		t.Fatalf("no list %q", key)
	}
	return d
}

func TestDefsAreConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Defs {
		if seen[d.Key] || d.Key == "" || d.Label == "" {
			t.Errorf("bad or duplicate list %q", d.Key)
		}
		seen[d.Key] = true
		if n := len(d.Columns); n < 1 || n > maxColumns {
			t.Errorf("%s has %d columns", d.Key, n)
		}
		if len(d.KeyCols) == 0 {
			t.Errorf("%s has no key columns", d.Key)
		}
		for _, c := range append(append([]int{}, d.KeyCols...), d.SortBy...) {
			if c < 0 || c >= len(d.Columns) {
				t.Errorf("%s refers to missing column %d", d.Key, c)
			}
		}
	}
	if _, ok := Find("nope"); ok {
		t.Error("Find invented a list")
	}
}

func TestValidate(t *testing.T) {
	mal := mustFind(t, "malaria")
	if got, err := Validate(mal, []string{" tinggi ", "  Kota   Sorong ", "Papua Barat Daya"}); err != nil || got[0] != "Tinggi" || got[1] != "Kota Sorong" {
		t.Errorf("%v %v", got, err)
	}
	cases := []struct {
		name string
		def  Def
		vals []string
	}{
		{"wrong count", mal, []string{"Tinggi"}},
		{"required", mal, []string{"Tinggi", "", "Papua"}},
		{"option", mal, []string{"Parah", "Sorong", "Papua"}},
		{"too long", mustFind(t, "nationality"), []string{string(make([]rune, 161))}},
	}
	for _, c := range cases {
		if _, err := Validate(c.def, c.vals); err == nil {
			t.Errorf("%s accepted", c.name)
		} else if _, ok := err.(ValidationError); !ok {
			t.Errorf("%s: not a ValidationError: %T", c.name, err)
		}
	}
	re := mustFind(t, "role_email")
	if got, err := Validate(re, []string{"Doctor", " Clinic@Example.COM "}); err != nil || got[0] != "doctor" || got[1] != "clinic@example.com" {
		t.Errorf("%v %v", got, err)
	}
	if _, err := Validate(re, []string{"doctor", "not-an-email"}); err == nil {
		t.Error("bad email accepted")
	}
	// Optional columns may stay empty (Indonesian / Foreign rows of the ring list).
	if got, err := Validate(mustFind(t, "ring"), []string{"Indonesian", "Indonesian", "", ""}); err != nil || got[2] != "" {
		t.Errorf("%v %v", got, err)
	}
}

func TestAddListUpdateArchiveRestore(t *testing.T) {
	s, q := newStore(t)
	nat := mustFind(t, "nationality")
	a, err := s.Add(ctx, "arif", nat, []string{"Indonesia"})
	if err != nil || a.ID == 0 {
		t.Fatal(err)
	}
	b, _ := s.Add(ctx, "arif", nat, []string{"Malaysia"})
	items, _ := s.Items(ctx, nat, false)
	if len(items) != 2 || items[0].Values[0] != "Indonesia" || items[1].Values[0] != "Malaysia" {
		t.Fatalf("added order lost: %+v", items)
	}
	if err := s.Update(ctx, "arif", nat, b.ID, []string{"Philippines"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetArchived(ctx, "arif", nat, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.Items(ctx, nat, false); len(items) != 1 || items[0].Values[0] != "Philippines" {
		t.Errorf("archived item still listed: %+v", items)
	}
	all, _ := s.Items(ctx, nat, true)
	if len(all) != 2 || !all[0].Archived {
		t.Errorf("includeArchived: %+v", all)
	}
	if err := s.SetArchived(ctx, "arif", nat, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.Items(ctx, nat, false); len(items) != 2 {
		t.Errorf("restore failed: %+v", items)
	}
	// Audit: add, add, update, archive, restore, newest first, with old and new values.
	log, err := audit.Recent(ctx, q, 20)
	if err != nil || len(log) != 5 {
		t.Fatalf("%v %v", log, err)
	}
	if log[0].Action != "list.restore" || log[3].Action != "list.add" && log[2].Action != "list.update" {
		t.Errorf("audit order: %+v", log)
	}
	var sawUpdate bool
	for _, e := range log {
		if e.Action == "list.update" && e.Old == "Malaysia" && e.New == "Philippines" && e.Actor == "arif" && e.Target == "nationality#2" {
			sawUpdate = true
		}
	}
	if !sawUpdate {
		t.Errorf("update audit row missing: %+v", log)
	}
}

func TestDuplicatesAreCaseInsensitiveAndIncludeArchived(t *testing.T) {
	s, _ := newStore(t)
	nat := mustFind(t, "nationality")
	a, _ := s.Add(ctx, "x", nat, []string{"Indonesia"})
	if _, err := s.Add(ctx, "x", nat, []string{" indonesia "}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("case duplicate: %v", err)
	}
	_ = s.SetArchived(ctx, "x", nat, a.ID, true)
	if _, err := s.Add(ctx, "x", nat, []string{"INDONESIA"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("archived duplicate: %v", err)
	}
	b, _ := s.Add(ctx, "x", nat, []string{"Malaysia"})
	if err := s.Update(ctx, "x", nat, b.ID, []string{"indonesia"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("update into duplicate: %v", err)
	}
	// Saving an item unchanged is not a duplicate of itself.
	if err := s.Update(ctx, "x", nat, b.ID, []string{"Malaysia"}); err != nil {
		t.Errorf("self update: %v", err)
	}
}

func TestMalariaKeyIsRegencyPlusProvince(t *testing.T) {
	s, _ := newStore(t)
	mal := mustFind(t, "malaria")
	for _, province := range []string{"Papua Barat", "Papua Barat Daya"} {
		if _, err := s.Add(ctx, "x", mal, []string{"Tinggi", "Sorong", province}); err != nil {
			t.Fatalf("same regency in %s: %v", province, err)
		}
	}
	if _, err := s.Add(ctx, "x", mal, []string{"Sedang", "sorong", "papua barat"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("same regency and province: %v", err)
	}
	_, _ = s.Add(ctx, "x", mal, []string{"Rendah", "Kota Jayapura", "Papua"})
	items, _ := s.Items(ctx, mal, false)
	// SortBy province then regency: Papua, Papua Barat, Papua Barat Daya.
	if len(items) != 3 || items[0].Values[2] != "Papua" || items[2].Values[2] != "Papua Barat Daya" {
		t.Errorf("sort: %+v", items)
	}
}

func TestNotFoundAndWrongList(t *testing.T) {
	s, _ := newStore(t)
	nat := mustFind(t, "nationality")
	gate := mustFind(t, "gate_name")
	it, _ := s.Add(ctx, "x", nat, []string{"Indonesia"})
	if err := s.Update(ctx, "x", nat, 999, []string{"A"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: %v", err)
	}
	// An item id from another list must not be editable through this list.
	if err := s.Update(ctx, "x", gate, it.ID, []string{"Gate 1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-list update: %v", err)
	}
	if err := s.SetArchived(ctx, "x", gate, it.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-list archive: %v", err)
	}
	if _, err := s.Add(ctx, "x", nat, []string{""}); err == nil {
		t.Error("blank accepted")
	}
	if err := s.Update(ctx, "x", nat, it.ID, []string{""}); err == nil {
		t.Error("blank update accepted")
	}
}
