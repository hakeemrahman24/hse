package datasync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/db"
)

// fakeFetcher answers from a map; a missing URL is a fetch error.
type fakeFetcher map[string]string

func (f fakeFetcher) Get(_ context.Context, url string) ([]byte, error) {
	body, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("fetch %s: answered 404", url)
	}
	return []byte(body), nil
}

func newStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, f := range []string{"013_region.sql", "031_data_sync.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	return &Store{DB: database, Now: func() time.Time { return now }}
}

func seedRegions(t *testing.T, database *sql.DB, rows ...[4]any) {
	t.Helper()
	for _, r := range rows {
		if _, err := database.Exec(`INSERT INTO Region (Code, Name, Level, ParentCode) VALUES (?,?,?,?)`, r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
}

// --- schedule ---

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		st   Setting
		want bool
	}{
		{"never run", Setting{Enabled: true, IntervalMonths: 6}, true},
		{"switched off", Setting{Enabled: false, IntervalMonths: 6}, false},
		{"five months ago on a 6 month interval", Setting{Enabled: true, IntervalMonths: 6, LastRunAt: "2026-06-13T10:00:01"}, false},
		{"six months ago exactly", Setting{Enabled: true, IntervalMonths: 6, LastRunAt: "2026-05-13T10:00:00"}, true},
		{"failed an hour ago, interval long passed", Setting{Enabled: true, IntervalMonths: 6, LastRunAt: "2025-01-01T00:00:00", LastAttemptAt: "2026-11-13T09:00:00", LastStatus: "error"}, false},
		{"failed two days ago", Setting{Enabled: true, IntervalMonths: 6, LastRunAt: "2025-01-01T00:00:00", LastAttemptAt: "2026-11-11T09:00:00", LastStatus: "error"}, true},
		{"unreadable last run counts as never", Setting{Enabled: true, IntervalMonths: 6, LastRunAt: "garbage"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.Due(now); got != tc.want {
				t.Errorf("Due = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSaveSchedule(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.SaveSchedule(ctx, SourceRegion, false, 12); err != nil {
		t.Fatal(err)
	}
	st, err := s.Get(ctx, SourceRegion)
	if err != nil || st.Enabled || st.IntervalMonths != 12 {
		t.Fatalf("got %+v, %v", st, err)
	}
	for _, bad := range []struct {
		source string
		months int
	}{{SourceRegion, 5}, {SourceRegion, 0}, {"nope", 6}} {
		if err := s.SaveSchedule(ctx, bad.source, true, bad.months); err != ErrBadSetting {
			t.Errorf("SaveSchedule(%v) = %v, want ErrBadSetting", bad, err)
		}
	}
}

func TestSeededRegionRowStartsFromTheHandLoadedSnapshot(t *testing.T) {
	s := newStore(t)
	st, err := s.Get(context.Background(), SourceRegion)
	if err != nil {
		t.Fatal(err)
	}
	if st.IntervalMonths != 6 || !st.Enabled || st.NextRun().Format("2006-01-02") != "2027-04-20" {
		t.Errorf("seed = %+v, next %v", st, st.NextRun())
	}
}

// --- region refresh ---

const sha = "d68e8d5516f969d1905d0b2940f20034becb0db7"

func wilayah(rows ...string) string {
	return "-- Kepmendagri No 300.2.2-2138 Tahun 2025\nINSERT INTO wilayah VALUES\n" + strings.Join(rows, ",\n") + ";\n"
}

func regionFetcher(body string) fakeFetcher {
	return fakeFetcher{
		regionCommitsURL:               `[{"sha":"` + sha + `"}]`,
		fmt.Sprintf(regionRawURL, sha): body,
	}
}

func TestParseWilayah(t *testing.T) {
	rows, err := ParseWilayah(wilayah("('11','Aceh')", "('11.01','Kabupaten Pasi Kuala Ba''u')"))
	if err != nil || len(rows) != 2 || rows[1].Name != "Kabupaten Pasi Kuala Ba'u" {
		t.Fatalf("got %v, %v", rows, err)
	}
	if _, err := ParseWilayah("('11','Aceh' oops\n"); err == nil {
		t.Error("a row that cannot be read must be an error, not skipped")
	}
	if _, err := ParseWilayah("nothing here"); err == nil {
		t.Error("a file with no rows must be an error")
	}
}

func TestValidateRegion(t *testing.T) {
	ok := []RegionRow{{"11", "Aceh"}, {"11.01", "Kabupaten Simeulue"}}
	if err := ValidateRegion(ok, 0, 2); err != nil {
		t.Fatalf("valid rows refused: %v", err)
	}
	bad := map[string][]RegionRow{
		"too few":      ok[:1],
		"bad code":     {{"11", "Aceh"}, {"1x", "X"}},
		"duplicate":    {{"11", "Aceh"}, {"11", "Aceh"}},
		"empty name":   {{"11", "Aceh"}, {"11.01", " "}},
		"backslash":    {{"11", "Aceh"}, {"11.01", `A\B`}},
		"control char": {{"11", "Aceh"}, {"11.01", "A\x00B"}},
		"orphan":       {{"11", "Aceh"}, {"12.01", "X"}},
		"long name":    {{"11", "Aceh"}, {"11.01", strings.Repeat("x", 101)}},
	}
	for name, rows := range bad {
		if err := ValidateRegion(rows, 0, 2); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := ValidateRegion(ok, 100, 2); err == nil {
		t.Error("a drop from 100 to 2 rows must be refused")
	}
}

func TestRegionRunReplacesTableAndRecords(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRegions(t, s.DB, [4]any{"11", "Aceh", 1, nil}, [4]any{"11.01", "Kab. Simeulue", 2, "11"}, [4]any{"11.99", "Gone", 2, "11"})
	sync := &RegionSync{Store: s, MinRows: 2, Fetcher: regionFetcher(wilayah("('11','Aceh')", "('11.01','Kabupaten Simeulue')", "('11.02','Kabupaten Aceh Singkil')"))}
	msg, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "1 added, 1 removed, 1 renamed, 3 areas") || !strings.Contains(msg, "Tahun 2025") {
		t.Errorf("message = %q", msg)
	}
	var name, parent string
	var level int
	if err := s.DB.QueryRow(`SELECT Name, Level, ParentCode FROM Region WHERE Code = '11.02'`).Scan(&name, &level, &parent); err != nil || level != 2 || parent != "11" {
		t.Errorf("new row: %q %d %q %v", name, level, parent, err)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM Region`).Scan(&n)
	if n != 3 {
		t.Errorf("Region has %d rows, want 3", n)
	}
	st, _ := s.Get(ctx, SourceRegion)
	if st.LastStatus != "ok" || st.LastRunAt != "2026-11-13T10:00:00" {
		t.Errorf("setting = %+v", st)
	}
	// Second run: same data, nothing to change.
	msg, err = sync.Run(ctx)
	if err != nil || !strings.Contains(msg, "no change") {
		t.Errorf("second run: %q %v", msg, err)
	}
}

func TestRegionRunRefusedLeavesTableAndRetriesLater(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRegions(t, s.DB, [4]any{"11", "Aceh", 1, nil}, [4]any{"11.01", "Kab. Simeulue", 2, "11"})
	sync := &RegionSync{Store: s, MinRows: 2, Fetcher: regionFetcher(wilayah("('11','Aceh')", "('11.01',''"))}
	if _, err := sync.Run(ctx); err == nil {
		t.Fatal("expected an error")
	}
	var name string
	_ = s.DB.QueryRow(`SELECT Name FROM Region WHERE Code = '11.01'`).Scan(&name)
	if name != "Kab. Simeulue" {
		t.Errorf("table changed after a refused download: %q", name)
	}
	st, _ := s.Get(ctx, SourceRegion)
	if st.LastStatus != "error" || st.LastRunAt != "2026-10-20T07:58:45" {
		t.Errorf("a failure must keep LastRunAt and set the status: %+v", st)
	}
	if st.Due(s.Now()) {
		t.Error("a just-failed source must wait a day before the next try")
	}
}

func TestRegionRunBadCommitList(t *testing.T) {
	s := newStore(t)
	sync := &RegionSync{Store: s, MinRows: 2, Fetcher: fakeFetcher{regionCommitsURL: `[{"sha":"../etc/passwd"}]`}}
	if _, err := sync.Run(context.Background()); err == nil {
		t.Fatal("a commit id that is not 40 hex characters must be refused")
	}
}

func TestHTTPFetcherRefusesOtherHosts(t *testing.T) {
	f := HTTPFetcher{AllowedHosts: RegionHosts}
	if _, err := f.Get(context.Background(), "https://example.com/x"); err == nil {
		t.Error("a host outside the allow list must be refused")
	}
}

func TestHTTPFetcherStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("hello"))
	}))
	defer ts.Close()
	f := HTTPFetcher{AllowedHosts: []string{ts.URL}}
	if b, err := f.Get(context.Background(), ts.URL+"/ok"); err != nil || string(b) != "hello" {
		t.Errorf("got %q, %v", b, err)
	}
	if _, err := f.Get(context.Background(), ts.URL+"/missing"); err == nil {
		t.Error("a 404 must be an error")
	}
}

// --- endemicity ---

func kemkes(code, regency, province, label, cases, pop string) string {
	return fmt.Sprintf(`{"last_update":"2026-11-13 11:31:55","id_kabupaten":%q,"nama_kabupaten":%q,"namaprop1":%q,"jum_positif_malaria":%q,"jum_pddk":%q,"endemisitas":%q}`,
		code, regency, province, cases, pop, label)
}

func year(rows ...string) string { return "[" + strings.Join(rows, ",") + "]" }

func seedEndemicityRegions(t *testing.T, s *Store) {
	seedRegions(t, s.DB,
		[4]any{"62", "Kalimantan Tengah", 1, nil}, [4]any{"62.13", "Kabupaten Murung Raya", 2, "62"},
		[4]any{"14", "Riau", 1, nil}, [4]any{"14.72", "Kota Dumai", 2, "14"},
		[4]any{"91", "Papua", 1, nil}, [4]any{"91.06", "Kabupaten Biak Numfor", 2, "91"},
		[4]any{"96", "Papua Barat Daya", 1, nil}, [4]any{"96.01", "Kabupaten Sorong", 2, "96"}, [4]any{"96.71", "Kota Sorong", 2, "96"},
		[4]any{"92", "Papua Tengah", 1, nil}, [4]any{"92.01", "Kabupaten Sorong", 2, "92"}, // same name, other province
	)
}

func endemicitySync(s *Store, f fakeFetcher) *EndemicitySync {
	return &EndemicitySync{Store: s, Fetcher: f, MinPrimary: 2, MinResolved: 4}
}

func TestEndemicityRunStagesWithFallbackAndNameMatching(t *testing.T) {
	s := newStore(t)
	seedEndemicityRegions(t, s)
	ctx := context.Background()
	f := fakeFetcher{
		"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": `[]`, // not published yet
		"https://malaria.kemkes.go.id/api/sismal/chartMap/2025": year(
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", "Endemis Rendah", "287", "120222"),
			kemkes("1473", "KOTA DUMAI", "RIAU", "Eliminasi", "0", "300000"), // Kemenkes code 1473, ours 14.72
			kemkes("9107", "SORONG", "PAPUA BARAT DAYA", "Endemis Tinggi I", "10", "1000"),
			kemkes("9171", "KOTA SORONG", "PAPUA BARAT DAYA", "Endemis Rendah", "5", "1000"),
			kemkes("9999", "NOWHERE", "PAPUA", "Eliminasi", "0", "1"),
		),
		"https://malaria.kemkes.go.id/api/sismal/chartMap/2024": year(
			kemkes("9409", "BIAK NUMFOR", "PAPUA", "Endemis Tinggi I", "20", "2000"), // old Papua code, missing from 2025
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", "Endemis Sedang", "1", "1"),
		),
	}
	msg, err := endemicitySync(s, f).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Read map year 2025") || !strings.Contains(msg, "NOWHERE (PAPUA)") {
		t.Errorf("message = %q", msg)
	}
	staged, err := s.liveRows(ctx, "MalariaEndemicityStaged")
	if err != nil || len(staged) != 5 {
		t.Fatalf("staged %d rows, %v", len(staged), err)
	}
	if r := staged["62.13"]; r.Label != "Endemis Rendah" || r.DataYear != 2025 || r.Name != "Kabupaten Murung Raya" || r.Province != "Kalimantan Tengah" {
		t.Errorf("Murung Raya must come from 2025, not 2024: %+v", r)
	}
	if r := staged["14.72"]; r.Label != "Eliminasi" {
		t.Errorf("Kota Dumai matched by name despite the other code: %+v", r)
	}
	if r := staged["96.01"]; r.Label != "Endemis Tinggi I" {
		t.Errorf("Kabupaten Sorong must pick the Papua Barat Daya one by province: %+v", r)
	}
	if _, ok := staged["92.01"]; ok {
		t.Error("the Papua Tengah Sorong must not be filled from the Papua Barat Daya row")
	}
	if r := staged["96.71"]; r.Label != "Endemis Rendah" {
		t.Errorf("Kota Sorong kept apart from Kabupaten Sorong: %+v", r)
	}
	if r := staged["91.06"]; r.DataYear != 2024 || r.Label != "Endemis Tinggi I" {
		t.Errorf("Biak Numfor must fall back to 2024: %+v", r)
	}
	if live, _ := s.Live(ctx); len(live) != 0 {
		t.Error("a scrape must not touch the live table")
	}
}

func TestEndemicityApplyAndDiscard(t *testing.T) {
	s := newStore(t)
	seedEndemicityRegions(t, s)
	ctx := context.Background()
	scrape := func(label string) {
		f := fakeFetcher{"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": year(
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", label, "287", "120222"),
			kemkes("1472", "KOTA DUMAI", "RIAU", "Eliminasi", "0", "1"),
			kemkes("9106", "BIAK NUMFOR", "PAPUA", "Eliminasi", "0", "1"),
			kemkes("9171", "KOTA SORONG", "PAPUA BARAT DAYA", "Eliminasi", "0", "1"),
		)}
		if _, err := endemicitySync(s, f).Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	scrape("Endemis Rendah")
	p, err := s.Pending(ctx)
	if err != nil || p.Rows != 4 || len(p.Changes) != 4 || p.Changes[0].Old != "" {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	if err := s.ApplyStaged(ctx); err != nil {
		t.Fatal(err)
	}
	if live, _ := s.Live(ctx); len(live) != 4 {
		t.Fatalf("live has %d rows", len(live))
	}
	if st, _ := s.Get(ctx, SourceEndemicity); st.AppliedAt != "2026-11-13T10:00:00" {
		t.Errorf("AppliedAt = %q", st.AppliedAt)
	}
	if err := s.ApplyStaged(ctx); err != ErrNothingStaged {
		t.Errorf("second apply = %v, want ErrNothingStaged", err)
	}

	scrape("Endemis Sedang")
	p, _ = s.Pending(ctx)
	if len(p.Changes) != 1 || p.Changes[0].Old != "Endemis Rendah" || p.Changes[0].New != "Endemis Sedang" || p.Changes[0].Code != "62.13" {
		t.Fatalf("changes = %+v", p.Changes)
	}
	if err := s.DiscardStaged(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Pending(ctx); p.Rows != 0 {
		t.Error("discard must empty the staging table")
	}
	live, _ := s.Live(ctx)
	for _, r := range live {
		if r.Code == "62.13" && r.Label != "Endemis Rendah" {
			t.Errorf("discard must leave the live list alone: %+v", r)
		}
	}
}

func TestEndemicityRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown label", func(t *testing.T) {
		s := newStore(t)
		seedEndemicityRegions(t, s)
		f := fakeFetcher{"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": year(
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", "Endemis Baru", "1", "1"),
			kemkes("1472", "KOTA DUMAI", "RIAU", "Eliminasi", "0", "1"))}
		if _, err := endemicitySync(s, f).Run(ctx); err == nil || !strings.Contains(err.Error(), "Endemis Baru") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too few matched", func(t *testing.T) {
		s := newStore(t)
		seedEndemicityRegions(t, s)
		f := fakeFetcher{"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": year(
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", "Endemis Rendah", "1", "1"),
			kemkes("1472", "KOTA DUMAI", "RIAU", "Eliminasi", "0", "1"))}
		if _, err := endemicitySync(s, f).Run(ctx); err == nil {
			t.Error("2 matched of a required 4 must be refused")
		}
		if p, _ := s.Pending(ctx); p.Rows != 0 {
			t.Error("a refused scrape must stage nothing")
		}
		if st, _ := s.Get(ctx, SourceEndemicity); st.LastStatus != "error" {
			t.Errorf("status = %q", st.LastStatus)
		}
	})
	t.Run("not json", func(t *testing.T) {
		s := newStore(t)
		seedEndemicityRegions(t, s)
		f := fakeFetcher{"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": "<html>maintenance</html>"}
		if _, err := endemicitySync(s, f).Run(ctx); err == nil {
			t.Error("an HTML page must be refused")
		}
	})
	t.Run("site down", func(t *testing.T) {
		s := newStore(t)
		seedEndemicityRegions(t, s)
		if _, err := endemicitySync(s, fakeFetcher{}).Run(ctx); err == nil {
			t.Error("no year reachable must be an error")
		}
	})
}

func TestStatus(t *testing.T) {
	for label, want := range map[string]string{
		"Eliminasi": "free", "Endemis Rendah": "low", "Endemis Sedang": "medium",
		"Endemis Tinggi I": "high", "Endemis Tinggi II": "high", "Endemis Tinggi III": "high",
		"": "unknown", "Endemis Baru": "unknown",
	} {
		if got := Status(label); got != want {
			t.Errorf("Status(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestPer1000(t *testing.T) {
	if got := (EndemicityRow{Cases: 287, Population: 120222}).Per1000(); got < 2.38 || got > 2.39 {
		t.Errorf("Per1000 = %v", got)
	}
	if (EndemicityRow{Cases: 5}).Per1000() != 0 {
		t.Error("no population must not divide by zero")
	}
}

func TestRegencyKey(t *testing.T) {
	same := [][2]string{
		{"Kabupaten Kep. Siau Tagulandang Biaro", "KEPULAUAN SIAU TAGULANDANG BIARO"},
		{"Kabupaten Fak Fak", "FAKFAK"},
		{"Kota Sorong", "KOTA SORONG"},
		{"Kota Administrasi Jakarta Barat", "KOTA JAKARTA BARAT"},
		{"Kabupaten Administrasi Kepulauan Seribu", "KEPULAUAN SERIBU"},
		{"Kota Administrasi Jakarta Utara ", "KOTA JAKARTA UTARA"}, // the region list has a trailing space
	}
	for kemenkes, region := range map[string]string{
		"KOTA BARU": "Kabupaten Kotabaru", "MAHAKAM HULU": "Kabupaten Mahakam Ulu", "MAMUJU UTARA": "Kabupaten Pasangkayu",
	} {
		if aliases[regencyKey(kemenkes)] != regencyKey(region) {
			t.Errorf("alias %q -> %q missing", kemenkes, region)
		}
	}
	for _, p := range same {
		if regencyKey(p[0]) != regencyKey(p[1]) {
			t.Errorf("%q and %q should match", p[0], p[1])
		}
	}
	if regencyKey("Kota Sorong") == regencyKey("Kabupaten Sorong") {
		t.Error("kota and kabupaten must stay apart")
	}
}

// --- HTTP ---

func newService(t *testing.T) (*Service, *http.ServeMux) {
	t.Helper()
	s := newStore(t)
	seedEndemicityRegions(t, s)
	svc := &Service{Store: s,
		Region: &RegionSync{Store: s, MinRows: 2, Fetcher: fakeFetcher{}},
		Endemicity: endemicitySync(s, fakeFetcher{"https://malaria.kemkes.go.id/api/sismal/chartMap/2026": year(
			kemkes("6213", "MURUNG RAYA", "KALIMANTAN TENGAH", "Endemis Rendah", "287", "120222"),
			kemkes("1472", "KOTA DUMAI", "RIAU", "Eliminasi", "0", "1"),
			kemkes("9106", "BIAK NUMFOR", "PAPUA", "Eliminasi", "0", "1"),
			kemkes("9171", "KOTA SORONG", "PAPUA BARAT DAYA", "Eliminasi", "0", "1"))}),
	}
	mux := http.NewServeMux()
	svc.Register(mux, func(h http.Handler) http.Handler { return h })
	return svc, mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return rec
}

func TestHandlersFlow(t *testing.T) {
	_, mux := newService(t)

	if rec := do(mux, http.MethodPut, "/api/data-sync/region", `{"enabled":true,"intervalMonths":4}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad interval: %d", rec.Code)
	}
	if rec := do(mux, http.MethodPut, "/api/data-sync/region", `{"enabled":false,"intervalMonths":12}`); rec.Code != http.StatusOK {
		t.Errorf("save: %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPost, "/api/data-sync/endemicity/apply", ""); rec.Code != http.StatusConflict {
		t.Errorf("apply with nothing staged: %d", rec.Code)
	}
	if rec := do(mux, http.MethodPost, "/api/data-sync/bogus/run", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d", rec.Code)
	}
	if rec := do(mux, http.MethodPost, "/api/data-sync/region/run", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("failing region run: %d", rec.Code)
	}
	if rec := do(mux, http.MethodPost, "/api/data-sync/endemicity/run", ""); rec.Code != http.StatusOK {
		t.Fatalf("endemicity run: %d %s", rec.Code, rec.Body)
	}

	var ov overview
	rec := do(mux, http.MethodGet, "/api/data-sync", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Region.Enabled || ov.Region.IntervalMonths != 12 || ov.Region.LastStatus != "error" ||
		ov.Pending != 4 || len(ov.Changes) != 4 || ov.Endemicity.LastStatus != "ok" {
		t.Errorf("overview = %+v", ov)
	}

	if rec := do(mux, http.MethodPost, "/api/data-sync/endemicity/apply", ""); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d", rec.Code)
	}
	var applied []appliedRow
	if err := json.Unmarshal(do(mux, http.MethodGet, "/api/data-sync/endemicity", "").Body.Bytes(), &applied); err != nil || len(applied) != 4 {
		t.Fatalf("applied rows: %d, %v", len(applied), err)
	}
	for _, r := range applied {
		if r.Code == "62.13" && (r.Status != "low" || r.Per1000 != 2.39 || r.Name != "Kabupaten Murung Raya") {
			t.Errorf("applied row: %+v", r)
		}
	}
	rec = do(mux, http.MethodGet, "/api/data-sync", "")
	ov = overview{}
	_ = json.Unmarshal(rec.Body.Bytes(), &ov)
	if ov.Live != 4 || ov.Pending != 0 || ov.Counts["Eliminasi"] != 3 || ov.Endemicity.AppliedAt == "" {
		t.Errorf("after apply: %+v", ov)
	}
}

func TestRegencyEndemicityForTheScreeningForm(t *testing.T) {
	svc, mux := newService(t)
	svc.RegisterRead(mux, func(h http.Handler) http.Handler { return h })
	ctx := context.Background()

	get := func(code string) (int, endemicityView) {
		rec := do(mux, http.MethodGet, "/api/endemicity/"+code, "")
		var v endemicityView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return rec.Code, v
	}
	// Nothing applied yet: known=false, not an error.
	if code, v := get("62.13"); code != http.StatusOK || v.Known {
		t.Errorf("before apply: %d %+v", code, v)
	}
	if _, _, err := svc.runSource(ctx, SourceEndemicity); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.ApplyStaged(ctx); err != nil {
		t.Fatal(err)
	}
	// Murung Raya (our fake region code 62.13): endemic, not free.
	if code, v := get("62.13"); code != http.StatusOK || !v.Known || v.Free || v.Label != "Endemis Rendah" || v.Name != "Kabupaten Murung Raya" || v.DataYear != 2026 {
		t.Errorf("endemic regency: %d %+v", code, v)
	}
	// Kota Dumai is Eliminasi: free of malaria.
	if _, v := get("14.72"); !v.Known || !v.Free || v.Label != "Eliminasi" {
		t.Errorf("eliminated regency: %+v", v)
	}
	// A regency that is not on the list.
	if _, v := get("11.01"); v.Known {
		t.Errorf("unlisted regency: %+v", v)
	}
	for _, bad := range []string{"6213", "62.1", "62.13.01", "abc", "62.13'"} {
		if code, _ := get(bad); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Errorf("code %q: status %d, want 400 or 404", bad, code)
		}
	}
}

func TestRunDueRunsOnlyWhatIsDue(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	// region was last run 2026-10-20 (6 month interval): not due. endemicity never ran: due.
	svc.RunDue(ctx)
	if st, _ := svc.Store.Get(ctx, SourceRegion); st.LastAttemptAt != "" {
		t.Errorf("region ran although not due: %+v", st)
	}
	if st, _ := svc.Store.Get(ctx, SourceEndemicity); st.LastStatus != "ok" {
		t.Errorf("endemicity should have run: %+v", st)
	}
	if p, _ := svc.Store.Pending(ctx); p.Rows != 4 {
		t.Errorf("staged %d rows", p.Rows)
	}
}

func TestRunSourceRefusesOverlap(t *testing.T) {
	svc, _ := newService(t)
	svc.running.Lock()
	defer svc.running.Unlock()
	if _, ran, _ := svc.runSource(context.Background(), SourceRegion); ran {
		t.Error("a second refresh must not start while one is running")
	}
}
