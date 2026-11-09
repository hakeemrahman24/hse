package correctiveaction

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func (f apiFixture) exportBook(t *testing.T, query string) *excelize.File {
	t.Helper()
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/corrective-actions.xlsx"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != xlsxType {
		t.Fatalf("content type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="corrective-actions-`) || !strings.HasSuffix(cd, `.xlsx"`) {
		t.Fatalf("content disposition = %q", cd)
	}
	book, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("the response is not a workbook: %v", err)
	}
	t.Cleanup(func() { book.Close() })
	return book
}

func cellName(t *testing.T, col, row int) string {
	t.Helper()
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// seedExport makes two findings: A (older, closed, two Before photos and one After) and B (newer, open,
// two follow-ups, one done, with a spreadsheet-formula-looking finding text).
func seedExport(t *testing.T, f apiFixture) (a, b Action, bigPhotoBytes int) {
	t.Helper()
	big := jpegBytes(t, noisyImage(900, 600), 80)
	if len(big) > MaxPhotoBytes {
		t.Fatalf("test setup: %d bytes would be re-encoded", len(big))
	}
	a = f.createViaAPI(t)
	if rec := f.upload(t, a.ID, "Before", big, tinyJPEG(t)); rec.Code != http.StatusCreated {
		t.Fatalf("upload before = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.upload(t, a.ID, "After", tinyJPEG(t)); rec.Code != http.StatusCreated {
		t.Fatalf("upload after = %d", rec.Code)
	}
	if rec := f.do(t, "POST", "/api/corrective-actions/"+itoa(a.ID)+"/close", map[string]any{"closingDate": "2026-11-10", "actionTaken": "Shelves installed"}); rec.Code != 200 {
		t.Fatalf("close = %d %s", rec.Code, rec.Body.String())
	}

	body := f.createBody()
	body["inspectionDate"] = "2026-11-05"
	body["finding"] = "=1+1"
	body["dueDate"] = "2026-11-30"
	rec := f.do(t, "POST", "/api/corrective-actions", body)
	b = decode[Action](t, rec)
	f.do(t, "POST", "/api/corrective-actions/"+itoa(b.ID)+"/follow-ups", map[string]any{"description": "Buy shelves", "picId": f.patient2ID, "dueDate": "2026-11-12"})
	rec = f.do(t, "POST", "/api/corrective-actions/"+itoa(b.ID)+"/follow-ups", map[string]any{"description": "Install shelves", "picId": f.patientID, "dueDate": "2026-11-14"})
	b = decode[Action](t, rec)
	if rec := f.do(t, "POST", "/api/corrective-action-follow-ups/"+itoa(b.FollowUps[0].ID)+"/done", map[string]any{"doneDate": "2026-11-08", "actionTaken": "Bought"}); rec.Code != 200 {
		t.Fatalf("follow-up done = %d", rec.Code)
	}
	return a, b, len(big)
}

func TestExportHasTheRequestedColumnsAndFormats(t *testing.T) {
	f := newAPIFixture(t)
	a, b, _ := seedExport(t, f)
	book := f.exportBook(t, "")

	wantHeader := []string{"No.", "Program", "Inspection date", "Location", "Finding", "Recommendation", "PIC", "Employee ID", "Due date", "Severity", "Status", "Closing date", "Action taken", "Days open", "Follow-ups",
		"Before photo 1", "Before photo 2", "Before photo 3", "After photo 1", "After photo 2", "After photo 3", "Recorded by"}
	for i, want := range wantHeader {
		if got, _ := book.GetCellValue(sheetFindings, cellName(t, i+1, 1)); got != want {
			t.Fatalf("header %d = %q, want %q", i+1, got, want)
		}
	}
	// Newest inspection first: B in row 2, A in row 3.
	if got, _ := book.GetCellValue(sheetFindings, "A2"); got != itoa(b.ID) {
		t.Fatalf("row 2 is finding %q, want the newer one (%d)", got, b.ID)
	}
	if got, _ := book.GetCellValue(sheetFindings, "A3"); got != itoa(a.ID) {
		t.Fatalf("row 3 is finding %q, want %d", got, a.ID)
	}
	checks := map[string]string{
		"B3": "Malaria", "C3": "01/11/2026", "D3": "Mess hall", "G3": "Budi Santoso", "H3": "E-001", "I3": "20/11/2026",
		"J3": "Major", "K3": "Closed", "L3": "10/11/2026", "M3": "Shelves installed", "N3": "9", "V3": "Nurse A",
		"K2": "In progress", "O2": "1 of 2 done", "O3": "",
	}
	for cell, want := range checks {
		if got, _ := book.GetCellValue(sheetFindings, cell); got != want {
			t.Errorf("%s = %q, want %q", cell, got, want)
		}
	}
	if got, _ := book.GetCellValue(sheetFindings, "L2"); got != "" {
		t.Fatalf("an open finding has no closing date, got %q", got)
	}
}

func TestExportDoesNotTurnTextIntoFormulas(t *testing.T) {
	f := newAPIFixture(t)
	seedExport(t, f)
	book := f.exportBook(t, "")
	if formula, _ := book.GetCellFormula(sheetFindings, "E2"); formula != "" {
		t.Fatalf("finding text became the formula %q", formula)
	}
	if got, _ := book.GetCellValue(sheetFindings, "E2"); got != "=1+1" {
		t.Fatalf("finding cell = %q, want the literal text", got)
	}
}

func TestExportPhotosAreThumbnailsInUniformCells(t *testing.T) {
	f := newAPIFixture(t)
	a, _, bigBytes := seedExport(t, f)
	book := f.exportBook(t, "")
	_ = a

	photoFirst := len(findingColumns) + 1
	cell := func(slot, row int) string { return cellName(t, photoFirst+slot, row) }
	// A is row 3: Before 1 and 2, no Before 3, After 1.
	wantPictures := map[int]int{0: 1, 1: 1, 2: 0, 3: 1, 4: 0, 5: 0}
	for slot, want := range wantPictures {
		pics, err := book.GetPictures(sheetFindings, cell(slot, 3))
		if err != nil {
			t.Fatal(err)
		}
		if len(pics) != want {
			t.Fatalf("row 3 photo slot %d has %d pictures, want %d", slot, len(pics), want)
		}
		if want == 1 && slot == 0 && len(pics[0].File) >= bigBytes {
			t.Fatalf("embedded picture is %d bytes, not smaller than the %d byte original: it must be a thumbnail", len(pics[0].File), bigBytes)
		}
	}
	for slot := 0; slot < 6; slot++ { // B (row 2) has no photos
		if pics, _ := book.GetPictures(sheetFindings, cell(slot, 2)); len(pics) != 0 {
			t.Fatalf("row 2 slot %d should be empty", slot)
		}
	}

	// Same row height for every data row, with or without photos; same width for every photo column.
	for row := 2; row <= 3; row++ {
		if h, _ := book.GetRowHeight(sheetFindings, row); h != rowPoints {
			t.Fatalf("row %d height = %v, want %v", row, h, rowPoints)
		}
	}
	var widths []float64
	for slot := 0; slot < 6; slot++ {
		letters, _ := excelize.ColumnNumberToName(photoFirst + slot)
		w, err := book.GetColWidth(sheetFindings, letters)
		if err != nil {
			t.Fatal(err)
		}
		widths = append(widths, w)
	}
	for _, w := range widths {
		if w != widths[0] {
			t.Fatalf("photo column widths differ: %v", widths)
		}
	}
}

func TestExportFollowUpsSheet(t *testing.T) {
	f := newAPIFixture(t)
	_, b, _ := seedExport(t, f)
	book := f.exportBook(t, "")
	rows, err := book.GetRows(sheetFollowUps)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 { // header + B's two follow-ups; A has none
		t.Fatalf("follow-ups sheet has %d rows, want 3: %v", len(rows), rows)
	}
	first := rows[1]
	if first[0] != itoa(b.ID) || first[3] != "Buy shelves" || first[4] != "Budiman Hakim" || first[6] != "12/11/2026" || first[7] != "Done" || first[8] != "08/11/2026" || first[9] != "Bought" {
		t.Fatalf("first follow-up row = %v", first)
	}
	if rows[2][3] != "Install shelves" || rows[2][7] == "Done" {
		t.Fatalf("second follow-up row = %v", rows[2])
	}
}

func TestExportFollowsTheFilters(t *testing.T) {
	f := newAPIFixture(t)
	a, _, _ := seedExport(t, f)
	book := f.exportBook(t, "?status=Closed")
	if got, _ := book.GetCellValue(sheetFindings, "A2"); got != itoa(a.ID) {
		t.Fatalf("closed filter row 2 = %q, want finding %d", got, a.ID)
	}
	if got, _ := book.GetCellValue(sheetFindings, "A3"); got != "" {
		t.Fatalf("closed filter must leave only one finding, row 3 = %q", got)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/corrective-actions.xlsx?procId=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad filter = %d, want 400", rec.Code)
	}
}

func TestExportSaysWhenTheListWasCut(t *testing.T) {
	f := newAPIFixture(t)
	seedExport(t, f)
	old := maxExportRows
	maxExportRows = 1
	t.Cleanup(func() { maxExportRows = old })

	book := f.exportBook(t, "")
	if got, _ := book.GetCellValue(sheetFindings, "A3"); got != "" {
		t.Fatalf("only one finding may be exported, row 3 = %q", got)
	}
	if got, _ := book.GetCellValue(sheetFindings, "A4"); !strings.Contains(got, "Only the newest 1 findings") {
		t.Fatalf("note = %q, want it to say the list was cut", got)
	}
}

func TestExportSurvivesAMissingPhotoFile(t *testing.T) {
	f := newAPIFixture(t)
	a, _, _ := seedExport(t, f)
	for _, p := range decode[Action](t, f.do(t, "GET", "/api/corrective-actions/"+itoa(a.ID), nil)).BeforePhotos[:1] {
		path, err := f.store.PhotoPath(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(path)
		os.Remove(thumbPath(path))
	}
	book := f.exportBook(t, "")
	first := cellName(t, len(findingColumns)+1, 3)
	if got, _ := book.GetCellValue(sheetFindings, first); got != "(photo unavailable)" {
		t.Fatalf("cell for a missing photo = %q", got)
	}
}

func TestThumbnailIsKeptNextToThePhotoAndRemovedWithIt(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	withPhoto := decode[Action](t, f.upload(t, created.ID, "Before", tinyJPEG(t)))
	path, err := f.store.PhotoPath(withPhoto.BeforePhotos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(thumbPath(path)); err != nil {
		t.Fatalf("the small copy must be written at upload time: %v", err)
	}
	// A photo stored without a small copy gets one the first time it is asked for.
	os.Remove(thumbPath(path))
	if rec := f.do(t, "GET", withPhoto.BeforePhotos[0].URL+"?thumb=1", nil); rec.Code != 200 {
		t.Fatalf("thumb = %d", rec.Code)
	}
	if _, err := os.Stat(thumbPath(path)); err != nil {
		t.Fatalf("the small copy must be made on first use: %v", err)
	}
	if rec := f.do(t, "DELETE", withPhoto.BeforePhotos[0].URL, nil); rec.Code != 200 {
		t.Fatalf("delete photo = %d", rec.Code)
	}
	for _, p := range []string{path, thumbPath(path)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone after the photo is removed", p)
		}
	}
}
