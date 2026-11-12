package dashboard

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// uncompressed makes the PDF text readable to the tests, and restores the real
// setting afterwards: production PDFs are always compressed.
func uncompressed(t *testing.T) {
	t.Helper()
	old := compress
	compress = false
	t.Cleanup(func() { compress = old })
}

func render(t *testing.T, d Doc) string {
	t.Helper()
	var b bytes.Buffer
	if err := WritePDF(&b, d); err != nil {
		t.Fatalf("WritePDF: %v", err)
	}
	return b.String()
}

func pages(pdf string) int { return strings.Count(pdf, "/Type /Page\n") }

func TestWritePDFMakesAValidDocumentWithTheRowsTitleAndFilters(t *testing.T) {
	uncompressed(t)
	out := render(t, Doc{
		Tab: "registered", Title: "Dashboard: Registered", Query: "santoso", GeneratedAt: "2026-11-06 08:00:00", By: "Viewer",
		Sync: "Gate data last synced 2026-11-06 07:58:00.",
		Rows: []Row{{Kind: KindWork, ID: "LEP-AAAA1", Name: "Budi Santoso", Company: "PT Maju", Position: "Welder", Status: "Approved", ValidUntil: "2027-01-17"}},
	})
	if !strings.HasPrefix(out, "%PDF-") || !strings.HasSuffix(strings.TrimSpace(out), "%%EOF") {
		t.Fatalf("not a PDF: %q ... %q", out[:min(8, len(out))], out[max(0, len(out)-8):])
	}
	for _, want := range []string{"Dashboard: Registered", "Budi Santoso", "LEP-AAAA1", "PT Maju", "2027-01-17", "Search: santoso", "2026-11-06 08:00:00", "Viewer", "1 row", "last synced 2026-11-06 07:58:00", "Page 1 of 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("PDF lacks %q", want)
		}
	}
}

func TestAnEmptyListSaysSoInsteadOfPrintingAnEmptyTable(t *testing.T) {
	uncompressed(t)
	out := render(t, Doc{Tab: "pending", Title: "Dashboard: Pending", GeneratedAt: "2026-11-06 08:00:00"})
	if !strings.Contains(out, "Nothing on this list.") || !strings.Contains(out, "0 rows") {
		t.Error("an empty list should say so")
	}
}

func TestALongListContinuesOnNewPagesAndRepeatsTheColumnHeadings(t *testing.T) {
	uncompressed(t)
	var rows []Row
	for i := 0; i < 120; i++ {
		rows = append(rows, Row{Kind: KindWork, ID: fmt.Sprintf("LEP-%05d", i), Name: fmt.Sprintf("Worker %03d", i), Company: "PT Maju"})
	}
	out := render(t, Doc{Tab: "registered", Title: "Dashboard: Registered", Rows: rows})
	n := pages(out)
	if n < 2 {
		t.Fatalf("120 rows fit on %d page", n)
	}
	if got := strings.Count(out, "(Name)"); got != n {
		t.Errorf("heading printed %d times on %d pages", got, n)
	}
	if !strings.Contains(out, fmt.Sprintf("Page %d of %d", n, n)) {
		t.Error("the last page is not numbered Page n of n")
	}
}

func TestTooManyRowsAreCutOffWithANoteSayingSo(t *testing.T) {
	uncompressed(t)
	old := maxRows
	maxRows = 3
	t.Cleanup(func() { maxRows = old })
	var rows []Row
	for i := 0; i < 10; i++ {
		rows = append(rows, Row{ID: fmt.Sprintf("LEP-%05d", i), Name: fmt.Sprintf("Worker %03d", i)})
	}
	out := render(t, Doc{Tab: "registered", Title: "T", Rows: rows})
	if !strings.Contains(out, "Showing the first 3 of 10 rows") || strings.Contains(out, "Worker 003") || !strings.Contains(out, "Worker 002") {
		t.Error("the row cap or its note is wrong")
	}
}

func TestTextTheFontCannotDrawIsReplacedNotFatal(t *testing.T) {
	uncompressed(t)
	// the built-in fonts cover Western European letters; other scripts become '?'
	out := render(t, Doc{Tab: "registered", Title: "T", Rows: []Row{{ID: "LEP-AAAA1", Name: "Zoë 张伟 O'Connor (Jr)"}}})
	if !strings.Contains(out, "O'Connor") || !strings.Contains(out, `\(Jr\)`) {
		t.Error("apostrophes and brackets must survive, with brackets escaped")
	}
}

func TestVeryLongTextIsShortenedToFitItsColumn(t *testing.T) {
	uncompressed(t)
	long := strings.Repeat("Long Company Name ", 30)
	out := render(t, Doc{Tab: "registered", Title: "T", Rows: []Row{{ID: "LEP-AAAA1", Name: "A", Company: long}}})
	if strings.Contains(out, long) || !strings.Contains(out, "...") {
		t.Error("an over-long cell should be cut with ...")
	}
}

func TestTheGateTabPrintsWhoIsInsideAndTheLatestActivity(t *testing.T) {
	uncompressed(t)
	out := render(t, Doc{
		Tab: "gate", Title: "Dashboard: Gate",
		Inside:   []Row{{ID: "LEP-AAAA1", Name: "Inside Ian", Gate: "Gate 1", At: "2026-11-06 07:00:00", Officer: "Sari"}},
		Activity: []Row{{ID: "LEP-AAAA1", Name: "Inside Ian", Action: "checkin", At: "2026-11-06 07:00:00", Gate: "Gate 1", Officer: "Sari"}},
	})
	for _, want := range []string{`Inside now \(1\)`, "Latest gate activity", "Inside Ian", "checkin"} {
		if !strings.Contains(out, want) {
			t.Errorf("gate PDF lacks %q", want)
		}
	}
}

func TestEveryTabHasItsOwnColumns(t *testing.T) {
	uncompressed(t)
	for _, tab := range []string{"pending", "registered", "active", "scheduled"} {
		out := render(t, Doc{Tab: tab, Title: tab, Rows: []Row{{ID: "X-1", Name: "Someone", Arrival: "2026-11-10", Departure: "2026-11-12"}}})
		if !strings.Contains(out, "(Name)") || !strings.Contains(out, "Someone") {
			t.Errorf("%s tab: no table", tab)
		}
	}
	if out := render(t, Doc{Tab: "scheduled", Title: "s", Rows: []Row{{Name: "A", Arrival: "2026-11-10"}}}); !strings.Contains(out, "(Arrival)") {
		t.Error("the scheduled tab shows the arrival date")
	}
	if out := render(t, Doc{Tab: "pending", Title: "p", Rows: []Row{{Name: "A", Status: "Waiting for Final"}}}); !strings.Contains(out, "Waiting for Final") {
		t.Error("the pending tab shows the stage")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestAWriteErrorIsReturned(t *testing.T) {
	if err := WritePDF(failWriter{}, Doc{Tab: "registered", Title: "T"}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v", err)
	}
}
