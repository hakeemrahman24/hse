package importer

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ReadSheet reads one sheet of an Excel download as rows of text.
//
// Cells are read as they are shown ("04 Oct 2026"), which is what the date parsers expect.
// The two phone columns are the exception: the sheet kept many of them in numeric cells,
// and the shown form of a long number is rounded ("8.1234567890E+11"), so those two columns
// are taken from the raw cell value, which keeps every digit that was stored.
func ReadSheet(path, sheet string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
		return nil, fmt.Errorf("sheet %q not found in %s (sheets: %s)", sheet, path, strings.Join(f.GetSheetList(), ", "))
	}
	shown, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet %s: %w", sheet, err)
	}
	raw, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("read sheet %s: %w", sheet, err)
	}
	for i := range shown {
		if i >= len(raw) {
			break
		}
		for _, col := range []int{colPhone, colEmergencyPhone} {
			if col >= len(raw[i]) {
				continue
			}
			for len(shown[i]) <= col {
				shown[i] = append(shown[i], "")
			}
			shown[i][col] = raw[i][col]
		}
	}
	return shown, nil
}

// Write prints the report. It lists request ids and field names, never a name, a birth date
// or an address, so it can be pasted into a chat or a ticket.
func (r Report) Write(w io.Writer) {
	if r.DryRun {
		fmt.Fprintln(w, "DRY RUN: nothing was written.")
	}
	verb := "Imported"
	if r.DryRun {
		verb = "Would import"
	}
	fmt.Fprintf(w, "Rows read: %d\n", r.Rows)
	fmt.Fprintf(w, "%s: %d new, %d replaced (sheet row changed), %d already up to date\n", verb, r.Inserted, r.Replaced, r.Unchanged)
	fmt.Fprintf(w, "Inserted: %d\nReplaced: %d\nUnchanged: %d\nSkipped (blank): %d\nRejected: %d\n", r.Inserted, r.Replaced, r.Unchanged, r.Skipped, len(r.Rejected))
	if len(r.ByStatus) > 0 {
		fmt.Fprintln(w, "\nBy status:")
		statuses := make([]string, 0, len(r.ByStatus))
		for s := range r.ByStatus {
			statuses = append(statuses, s)
		}
		sort.Strings(statuses)
		for _, s := range statuses {
			fmt.Fprintf(w, "  %-28s %d\n", s, r.ByStatus[s])
		}
	}
	if len(r.Rejected) > 0 {
		fmt.Fprintln(w, "\nRejected rows (fix the cell in the sheet and run again):")
		for _, p := range r.Rejected {
			fmt.Fprintf(w, "  row %d  %s  %s\n", p.Row, p.ID, p.Msg)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintln(w, "\nWarnings (the permit was imported without that value):")
		for _, field := range r.SortedWarnings() {
			g := r.Warnings[field]
			fmt.Fprintf(w, "  %-18s %d rows  %s  e.g. %s\n", field, g.Count, g.Msg, strings.Join(g.Examples, ", "))
		}
	}
}

// WriteAll prints each sheet's report under its name.
func WriteAll(w io.Writer, reps []SheetReport) {
	for i, r := range reps {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "== %s ==\n", r.Name)
		r.Report.Write(w)
	}
}
