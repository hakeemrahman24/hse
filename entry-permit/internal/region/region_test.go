package region

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `11	Aceh
11.01	Kabupaten Aceh Selatan
11.01.01	Bakongan
63	Kalimantan Selatan
63.02	Kabupaten Kotabaru
71	Sulawesi Utara
71.09	Kabupaten Kep. Siau Tagulandang Biaro
81	Maluku
81.03	Kabupaten Kepulauan Tanimbar
92	Papua Barat
96	Papua Barat Daya
96.01	Kabupaten Sorong
96.71	Kota Sorong
92.77	Kabupaten Dupe
96.77	Kabupaten Dupe
`

func index(t *testing.T) *Index {
	t.Helper()
	ix, err := LoadIndex(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestKey(t *testing.T) {
	same := [][2]string{
		{"Kabupaten Kota Baru", "Kabupaten Kotabaru"},
		{"Kabupaten Fak-Fak", "kabupaten  Fak Fak"},
		{"Kabupaten Kep. Aru", "Kabupaten Kepulauan Aru"},
		{"Kab. Sorong", "Kabupaten Sorong"},
		{"  Kota Bau-Bau ", "Kota Bau Bau"},
	}
	for _, p := range same {
		if Key(p[0]) != Key(p[1]) {
			t.Errorf("%q and %q should match (%s vs %s)", p[0], p[1], Key(p[0]), Key(p[1]))
		}
	}
	if Key("Kabupaten Sorong") == Key("Kota Sorong") {
		t.Error("Kabupaten and Kota must stay apart")
	}
}

func TestLoadIndexErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no tab":         "11 Aceh\n",
		"no regencies":   "11\tAceh\n",
		"empty":          "",
		"only regencies": "11.01\tKabupaten X\n",
	} {
		if _, err := LoadIndex(strings.NewReader(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestFind(t *testing.T) {
	ix := index(t)
	if m := ix.Find("Kabupaten Kota Baru"); len(m) != 1 || m[0].Province != "Kalimantan Selatan" {
		t.Errorf("%v", m)
	}
	if m := ix.Find("Kabupaten Kepulauan Sitaro"); len(m) != 1 || m[0].Code != "71.09" {
		t.Errorf("sitaro alias: %v", m)
	}
	if m := ix.Find("Kabupaten Maluku Tenggara Barat"); len(m) != 1 || m[0].Province != "Maluku" {
		t.Errorf("tanimbar alias: %v", m)
	}
	if m := ix.Find("Kabupaten Dupe"); len(m) != 2 {
		t.Errorf("same name in two provinces: %v", m)
	}
	if m := ix.Find("Bakongan"); len(m) != 0 {
		t.Errorf("a district matched: %v", m)
	}
}

func TestAlignUsesTheAPINamesAndProvinces(t *testing.T) {
	ix := index(t)
	rows := []Row{
		{2, "Rendah", "Kabupaten Aceh Selatan", "Aceh"},
		{3, "Rendah", "Kabupaten Kota Baru", "Kalimantan Selatan"},          // spelling follows the API
		{4, "Tinggi", "Kabupaten Sorong", "Papua Barat"},                    // old province -> API province
		{5, "Tinggi", "Kabupaten Sorong", "Papua Barat Daya"},               // now a duplicate of line 4
		{6, "Rendah", "Kabupaten Kepulauan Sitaro", "Sulawesi Utara"},       // short name -> official
		{7, "Rendah", "Kabupaten Siau Tagulandang Biaro", "Sulawesi Utara"}, // same regency again
		{8, "Sedang", "Kabupaten Dupe", "Papua Barat"},                      // two provinces: the row's wins
	}
	got := ix.Align(rows)
	if len(got.Problems) != 0 {
		t.Fatalf("problems: %v", got.Problems)
	}
	want := []Row{
		{2, "Rendah", "Kabupaten Aceh Selatan", "Aceh"},
		{3, "Rendah", "Kabupaten Kotabaru", "Kalimantan Selatan"},
		{4, "Tinggi", "Kabupaten Sorong", "Papua Barat Daya"},
		{6, "Rendah", "Kabupaten Kep. Siau Tagulandang Biaro", "Sulawesi Utara"},
		{8, "Sedang", "Kabupaten Dupe", "Papua Barat"},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("rows: %+v", got.Rows)
	}
	for i := range want {
		if got.Rows[i] != want[i] {
			t.Errorf("row %d: got %+v want %+v", i, got.Rows[i], want[i])
		}
	}
	text := strings.Join(got.Changes, "\n")
	for _, w := range []string{"name Kabupaten Kota Baru -> Kabupaten Kotabaru", "province Papua Barat -> Papua Barat Daya", "line 5: dropped, same regency as line 4", "line 7: dropped"} {
		if !strings.Contains(text, w) {
			t.Errorf("changes lack %q:\n%s", w, text)
		}
	}
}

func TestAlignProblems(t *testing.T) {
	ix := index(t)
	got := ix.Align([]Row{
		{2, "Rendah", "Kabupaten Nowhere", "Aceh"},
		{3, "Rendah", "Kabupaten Dupe", "Aceh"}, // in two provinces, neither is Aceh
		{4, "Tinggi", "Kabupaten Sorong", "Papua Barat Daya"},
		{5, "Rendah", "Kabupaten Sorong", "Papua Barat"}, // same regency after aligning, endemicity differs
	})
	if len(got.Problems) != 3 || len(got.Rows) != 1 {
		t.Fatalf("%+v", got)
	}
	for i, w := range []string{"not in the region data", "exists in several provinces", "endemicity differs"} {
		if !strings.Contains(got.Problems[i], w) {
			t.Errorf("problem %d: %s", i, got.Problems[i])
		}
	}
}

// ---- sources ----

func TestHTTPSource(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`[{"code":"11.01","name":"Kabupaten Aceh Selatan","province":"Aceh"}]`))
	}))
	defer srv.Close()
	got, err := HTTPSource{BaseURL: srv.URL + "/", Token: "tok"}.Regencies(context.Background())
	if err != nil || len(got) != 1 || got[0].Province != "Aceh" {
		t.Fatalf("%v %v", got, err)
	}
	if gotAuth != "Bearer tok" || gotPath != "/api/directory/regions" {
		t.Errorf("request: %q %q", gotAuth, gotPath)
	}
}

func TestHTTPSourceFailures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"unauthorized": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) },
		"bad json":     func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) },
		"empty":        func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) },
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		_, err := HTTPSource{BaseURL: srv.URL, Token: "tok"}.Regencies(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := (HTTPSource{}).Regencies(context.Background()); err == nil {
		t.Error("unconfigured source accepted")
	}
	if _, err := (HTTPSource{BaseURL: "http://127.0.0.1:1", Token: "t"}).Regencies(context.Background()); err == nil {
		t.Error("unreachable server accepted")
	}
	if _, err := (HTTPSource{BaseURL: "http://bad host", Token: "t"}).Regencies(context.Background()); err == nil {
		t.Error("bad url accepted")
	}
}

func TestFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "region.tsv")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FileSource{Path: path}.Regencies(context.Background())
	if err != nil || len(got) != 8 {
		t.Fatalf("%d %v", len(got), err)
	}
	if _, err := (FileSource{Path: path + ".missing"}).Regencies(context.Background()); err == nil {
		t.Error("missing file accepted")
	}
}

type countingSource struct {
	calls int
	fail  bool
}

func (c *countingSource) Regencies(context.Context) ([]Regency, error) {
	c.calls++
	if c.fail {
		return nil, errors.New("down")
	}
	return []Regency{{Code: "11.01", Name: "Kabupaten X", Province: "Aceh"}}, nil
}

func TestCachedKeepsLastGoodCopy(t *testing.T) {
	src := &countingSource{}
	c := &Cached{Src: src, TTL: time.Hour}
	for i := 0; i < 3; i++ {
		if _, err := c.Regencies(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if src.calls != 1 {
		t.Errorf("source called %d times within the TTL", src.calls)
	}
	// After the TTL a failing source still serves the last good copy.
	c.TTL = 0
	src.fail = true
	got, err := c.Regencies(context.Background())
	if err != nil || len(got) != 1 {
		t.Errorf("stale copy not served: %v %v", got, err)
	}
	// With nothing cached, the error comes through.
	empty := &Cached{Src: &countingSource{fail: true}, TTL: time.Hour}
	if _, err := empty.Regencies(context.Background()); err == nil {
		t.Error("error swallowed")
	}
}
