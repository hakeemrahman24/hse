package reflist

import (
	"strings"
	"testing"
)

func TestLoadTSVAddsSkipsAndReports(t *testing.T) {
	s, _ := newStore(t)
	mal := mustFind(t, "malaria")
	data := "Endemicity\tRegency\tProvince\n" +
		"Rendah\tKabupaten Simeulue\tAceh\n" +
		"\n" +
		"Tinggi\tKabupaten Sorong\tPapua Barat\r\n" +
		"Tinggi\tKabupaten Sorong\tPapua Barat Daya\n" + // same regency, other province: allowed
		"Rendah\tkabupaten simeulue\taceh\n" + // duplicate of line 2
		"Parah\tKabupaten X\tAceh\n" + // not an allowed endemicity
		"Rendah\tToo few fields\n"
	res, err := LoadTSV(ctx, s, "seed", mal, strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 3 || res.Skipped != 1 || len(res.Problems) != 2 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Problems[0], "line 7") || !strings.Contains(res.Problems[1], "line 8") {
		t.Errorf("problems lack line numbers: %v", res.Problems)
	}
	// Loading the same data again adds nothing.
	again, _ := LoadTSV(ctx, s, "seed", mal, strings.NewReader(data))
	if again.Added != 0 || again.Skipped != 4 {
		t.Errorf("not repeatable: %+v", again)
	}
	items, _ := s.Items(ctx, mal, false)
	if len(items) != 3 {
		t.Errorf("%d items stored", len(items))
	}
}

func TestLoadTSVReadError(t *testing.T) {
	s, _ := newStore(t)
	nat := mustFind(t, "nationality")
	if _, err := LoadTSV(ctx, s, "seed", nat, errReader{}); err == nil {
		t.Error("reader error ignored")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBoom }

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }
