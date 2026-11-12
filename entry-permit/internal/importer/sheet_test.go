package importer

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func writeBook(t *testing.T, sheetName string, cells map[string]any) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if _, err := f.NewSheet(sheetName); err != nil {
		t.Fatal(err)
	}
	for cell, v := range cells {
		if err := f.SetCellValue(sheetName, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSheetKeepsTextAsShownAndRecoversPhoneNumbersFromNumericCells(t *testing.T) {
	// Column index 73 is BV (phone), 78 is CA (emergency phone), 14 is O (MCU valid until).
	path := writeBook(t, "WorkEntryPermitLog", map[string]any{
		"A1": "Timestamp", "B1": "RequestID",
		"A5": "8/7/2026 11:11:51", "B5": "WEP-AAAAA", "O5": "04 Oct 2026",
		"BV5": 812345678901.0, // a phone number kept in a numeric cell
		"CA5": "081234567890", // and one kept as text
	})
	rows, err := ReadSheet(path, "WorkEntryPermitLog")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 5 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[4]
	if r[1] != "WEP-AAAAA" || r[14] != "04 Oct 2026" {
		t.Errorf("text cells %q %q", r[1], r[14])
	}
	if got, _ := Phone(r[colPhone]); got != "812345678901" {
		t.Errorf("phone %q -> %q", r[colPhone], got)
	}
	if got, _ := Phone(r[colEmergencyPhone]); got != "6281234567890" {
		t.Errorf("emergency phone %q -> %q", r[colEmergencyPhone], got)
	}
}

func TestReadSheetSaysWhenTheFileOrTheSheetIsMissing(t *testing.T) {
	if _, err := ReadSheet(filepath.Join(t.TempDir(), "nope.xlsx"), "WorkEntryPermitLog"); err == nil {
		t.Error("a missing file was read")
	}
	path := writeBook(t, "Other", map[string]any{"A1": "x"})
	_, err := ReadSheet(path, "WorkEntryPermitLog")
	if err == nil || !strings.Contains(err.Error(), "WorkEntryPermitLog") {
		t.Errorf("missing sheet error: %v", err)
	}
}

func TestReportPrintsCountsRejectsAndWarningsWithoutPersonalData(t *testing.T) {
	rep := Report{
		DryRun: true, Rows: 10, Inserted: 6, Replaced: 1, Unchanged: 1, Skipped: 1,
		ByStatus: map[string]int{"Approved": 5, "Renewed by ...": 2},
		Rejected: []Problem{{Row: 9, ID: "WEP-BAD01", Msg: "work location \"Mars\" is not Onshore or Offshore"}},
		Warnings: map[string]WarnGroup{"mcu_valid_until": {Count: 4, Examples: []string{"WEP-A", "WEP-B", "WEP-C"}, Msg: "mcu_valid_until cannot be read; left blank"}},
	}
	var b bytes.Buffer
	rep.Write(&b)
	out := b.String()
	for _, want := range []string{"DRY RUN", "Rows read: 10", "Inserted: 6", "Replaced: 1", "Unchanged: 1", "Skipped (blank): 1", "Rejected: 1",
		"Approved", "Renewed by ...", "row 9", "WEP-BAD01", "Mars", "mcu_valid_until", "4 rows", "WEP-A, WEP-B, WEP-C"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	var real bytes.Buffer
	Report{Rows: 1, Inserted: 1}.Write(&real)
	if strings.Contains(real.String(), "DRY RUN") || !strings.Contains(real.String(), "Imported") {
		t.Errorf("a real run's report:\n%s", real.String())
	}
}
