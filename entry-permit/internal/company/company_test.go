package company

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/migrations"
)

var ctx = context.Background()

type fakeSource struct {
	list []Company
	err  error
}

func (f *fakeSource) Companies(context.Context) ([]Company, error) { return f.list, f.err }

func newStore(t *testing.T) (*Store, *sql.DB) {
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
	return &Store{DB: d, Clock: dates.FixedClock("2026-11-06 10:00:00")}, d
}

func sample() []Company {
	return []Company{
		{ID: 2, Name: "PT Karya", Entity: "Contractor", Parents: []Parent{{Name: "PT LNG", Depth: 2}, {Name: "PT Induk", Depth: 1}}},
		{ID: 1, Name: "PT LNG", Abbreviation: "LNG", Entity: "Client"},
		{ID: 3, Name: "Dinas Ketenagakerjaan", Abbreviation: "GOV", Entity: "Government", Gov: true},
		{ID: 4, Name: "PT Lama", Archived: true},
	}
}

func TestFirstSyncStoresEverythingOrderedByName(t *testing.T) {
	s, d := newStore(t)
	res, err := s.Sync(ctx, &fakeSource{list: sample()}, "arif")
	if err != nil || res.Added != 4 || res.Total != 4 {
		t.Fatalf("sync = %+v %v", res, err)
	}
	all, _ := s.List(ctx, true)
	if len(all) != 4 || all[0].Name != "Dinas Ketenagakerjaan" || !all[0].Gov {
		t.Fatalf("list = %+v", all)
	}
	for _, c := range all {
		if c.Name == "PT Karya" && (len(c.Parents) != 2 || c.Parents[0].Name != "PT Induk" || c.Parents[1].Depth != 2) {
			t.Errorf("parents nearest first, got %+v", c.Parents)
		}
	}
	if active, _ := s.List(ctx, false); len(active) != 3 {
		t.Errorf("pickers must not see archived: %d", len(active))
	}
	if c, ok, _ := s.Find(ctx, 4); !ok || !c.Archived {
		t.Errorf("an archived company must still be findable: %+v %v", c, ok)
	}
	if _, ok, _ := s.Find(ctx, 99); ok {
		t.Error("found a company that does not exist")
	}
	at, total, _ := s.LastSync(ctx)
	if at != "2026-11-06 10:00:00" || total != 4 {
		t.Errorf("last sync = %q %d", at, total)
	}
	rows, _ := audit.Recent(ctx, d, 3)
	if len(rows) != 1 || rows[0].Action != "company.sync" || rows[0].Actor != "arif" || !strings.Contains(rows[0].New, "4 added") {
		t.Fatalf("audit = %+v", rows)
	}
}

func TestSecondSyncOfSameDataChangesNothingAndIsNotAudited(t *testing.T) {
	s, d := newStore(t)
	src := &fakeSource{list: sample()}
	_, _ = s.Sync(ctx, src, "arif")
	res, err := s.Sync(ctx, src, "system")
	if err != nil || res.Any() {
		t.Fatalf("second sync = %+v %v", res, err)
	}
	if rows, _ := audit.Recent(ctx, d, 5); len(rows) != 1 {
		t.Fatalf("an unchanged sync wrote %d audit rows", len(rows)-1)
	}
}

func TestSyncCountsAddedChangedRemoved(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Sync(ctx, &fakeSource{list: sample()}, "arif")
	next := sample()[:3]                                 // PT Lama removed
	next[1].Entity = "Subcontractor"                     // PT LNG changed
	next = append(next, Company{ID: 9, Name: "PT Baru"}) // added
	res, err := s.Sync(ctx, &fakeSource{list: next}, "system")
	if err != nil || res.Added != 1 || res.Changed != 1 || res.Removed != 1 {
		t.Fatalf("sync = %+v %v", res, err)
	}
	if _, ok, _ := s.Find(ctx, 4); ok {
		t.Error("removed company is still in the mirror")
	}
}

func TestBadAnswersChangeNothing(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Sync(ctx, &fakeSource{list: sample()}, "arif")
	cases := map[string]*fakeSource{
		"medpost down":   {err: errors.New("connection refused")},
		"empty list":     {list: []Company{}},
		"blank name":     {list: []Company{{ID: 1, Name: "  "}}},
		"no id":          {list: []Company{{Name: "PT X"}}},
		"duplicate id":   {list: []Company{{ID: 1, Name: "A"}, {ID: 1, Name: "B"}}},
		"duplicate name": {list: []Company{{ID: 1, Name: "PT Same"}, {ID: 2, Name: "pt  same"}}},
	}
	for name, src := range cases {
		if _, err := s.Sync(ctx, src, "system"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if all, _ := s.List(ctx, true); len(all) != 4 {
		t.Fatalf("mirror changed after bad answers: %d companies", len(all))
	}
	if _, err := s.Sync(ctx, nil, "system"); !errors.Is(err, ErrNoSource) {
		t.Errorf("no source: %v", err)
	}
}

func TestSyncRefusesToDropMostOfTheMirror(t *testing.T) {
	s, _ := newStore(t)
	var big []Company
	for i := 1; i <= 30; i++ {
		big = append(big, Company{ID: int64(i), Name: fmt.Sprintf("PT %02d", i)})
	}
	_, _ = s.Sync(ctx, &fakeSource{list: big}, "arif")
	if _, err := s.Sync(ctx, &fakeSource{list: big[:5]}, "system"); !errors.Is(err, ErrBadData) {
		t.Fatalf("dropping 25 of 30: %v", err)
	}
	if res, err := s.Sync(ctx, &fakeSource{list: big[:25]}, "system"); err != nil || res.Removed != 5 {
		t.Fatalf("dropping 5 of 30 is normal: %+v %v", res, err)
	}
}

func TestHTTPSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/directory/companies" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[{"companyId":7,"name":"PT Karya","abbreviation":"","entity":"Contractor","gov":false,"archived":false,"parents":[{"name":"PT LNG","depth":1}]}]`)
	}))
	defer srv.Close()
	got, err := HTTPSource{BaseURL: srv.URL + "/", Token: "tok"}.Companies(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 7 || got[0].Parents[0].Name != "PT LNG" {
		t.Fatalf("companies = %+v %v", got, err)
	}
	if _, err := (HTTPSource{BaseURL: srv.URL, Token: "wrong"}).Companies(ctx); err == nil {
		t.Error("a wrong token was accepted")
	}
	if _, err := (HTTPSource{}).Companies(ctx); !errors.Is(err, ErrNoSource) {
		t.Errorf("unconfigured: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not json") }))
	defer bad.Close()
	if _, err := (HTTPSource{BaseURL: bad.URL, Token: "tok"}).Companies(ctx); err == nil {
		t.Error("bad json was accepted")
	}
}

func TestLastSyncBeforeAnySync(t *testing.T) {
	s, _ := newStore(t)
	if at, total, err := s.LastSync(ctx); at != "" || total != 0 || err != nil {
		t.Fatalf("empty = %q %d %v", at, total, err)
	}
}
