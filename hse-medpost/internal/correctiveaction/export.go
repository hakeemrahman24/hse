package correctiveaction

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder; thumbnails are read only for their size
	"log"
	"math"
	"net/http"
	"time"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/dates"
)

// maxExportRows caps one workbook. Each row can carry up to six pictures and the whole file is built in
// memory on a Pi shared with the database, so a very wide export is cut and says so. A var, not a const,
// so a test can lower it.
var maxExportRows = 500

const (
	sheetFindings  = "Findings"
	sheetFollowUps = "Follow-ups"
	xlsxType       = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

	// Photo cell geometry, in pixels. Every photo column is the same width and every data row the same
	// height, whether a finding has no photos or six, so the sheet stays a regular grid (owner's decision).
	// A picture is scaled to fit inside the cell with a small margin and centred in it.
	photoCellW = 160
	photoCellH = 120
	photoPad   = 4
	// Excel measures row height in points (3 points to 4 pixels) and column width in characters
	// (about 7 pixels each, plus 5 pixels of padding).
	rowPoints    = float64(photoCellH) * 3 / 4
	headerPoints = 32.0
	photoColChar = float64(photoCellW-5) / 7

	numberFormatDMY = "dd/mm/yyyy"
	headerFill      = "1A7A6E" // the app's teal
	borderColor     = "BFBFBF"
)

type column struct {
	title string
	width float64 // Excel characters
}

// findingColumns are the text columns, in order. The six photo columns and "Recorded by" follow them.
var findingColumns = []column{
	{"No.", 6}, {"Program", 24}, {"Inspection date", 13}, {"Location", 20}, {"Finding", 38}, {"Recommendation", 38},
	{"PIC", 22}, {"Employee ID", 12}, {"Due date", 12}, {"Severity", 10}, {"Status", 12}, {"Closing date", 13},
	{"Action taken", 30}, {"Days open", 9}, {"Follow-ups", 14},
}

var photoColumnTitles = []string{"Before photo 1", "Before photo 2", "Before photo 3", "After photo 1", "After photo 2", "After photo 3"}

var followUpColumns = []column{
	{"Finding no.", 10}, {"Program", 24}, {"Finding", 36}, {"Follow-up", 38}, {"PIC", 22}, {"Employee ID", 12},
	{"Due date", 12}, {"Status", 10}, {"Done date", 12}, {"Action taken", 34}, {"Done by", 16},
}

// Export: GET /api/corrective-actions.xlsx with the same filters as the table, so the file holds what the
// page shows. Photos are thumbnails of one fixed cell size (see photoCellW).
func (a *API) Export(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	filter.Limit = maxExportRows + 1 // one extra row tells us the list was cut
	actions, err := a.Store.List(filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	truncated := len(actions) > maxExportRows
	if truncated {
		actions = actions[:maxExportRows]
	}
	book, err := a.buildWorkbook(actions, truncated)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer book.Close()
	var buf bytes.Buffer
	if _, err := book.WriteTo(&buf); err != nil {
		writeError(w, r, fmt.Errorf("write corrective action workbook: %w", err))
		return
	}
	w.Header().Set("Content-Type", xlsxType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="corrective-actions-%s.xlsx"`, dates.Today()))
	_, _ = w.Write(buf.Bytes())
}

type sheetStyles struct{ header, text, date, number, photo int }

func newStyles(book *excelize.File) (sheetStyles, error) {
	border := []excelize.Border{
		{Type: "left", Color: borderColor, Style: 1}, {Type: "right", Color: borderColor, Style: 1},
		{Type: "top", Color: borderColor, Style: 1}, {Type: "bottom", Color: borderColor, Style: 1},
	}
	format := numberFormatDMY
	var s sheetStyles
	var err error
	if s.header, err = book.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{headerFill}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    border,
	}); err != nil {
		return s, err
	}
	if s.text, err = book.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}, Border: border}); err != nil {
		return s, err
	}
	if s.date, err = book.NewStyle(&excelize.Style{CustomNumFmt: &format, Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "top"}, Border: border}); err != nil {
		return s, err
	}
	if s.number, err = book.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "top"}, Border: border}); err != nil {
		return s, err
	}
	s.photo, err = book.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}, Border: border})
	return s, err
}

// dateValue is a real Excel date (so the column sorts and filters as dates) or nil for a blank one.
func dateValue(iso string) any {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return nil
	}
	return t
}

func exportStatus(a Action) string {
	switch {
	case a.Status == StatusClosed:
		return "Closed"
	case a.IsOverdue:
		return "Overdue"
	case a.Status == StatusInProgress:
		return "In progress"
	}
	return "Open"
}

func followUpStatus(f FollowUp) string {
	switch {
	case f.Status == FollowUpDone:
		return "Done"
	case f.IsOverdue:
		return "Overdue"
	}
	return "Open"
}

func (a *API) buildWorkbook(actions []Action, truncated bool) (*excelize.File, error) {
	book := excelize.NewFile()
	if err := book.SetSheetName("Sheet1", sheetFindings); err != nil {
		return nil, fmt.Errorf("name findings sheet: %w", err)
	}
	styles, err := newStyles(book)
	if err != nil {
		return nil, fmt.Errorf("create sheet styles: %w", err)
	}
	if err := a.writeFindings(book, styles, actions, truncated); err != nil {
		book.Close()
		return nil, err
	}
	if err := writeFollowUps(book, styles, actions); err != nil {
		book.Close()
		return nil, err
	}
	return book, nil
}

func (a *API) writeFindings(book *excelize.File, st sheetStyles, actions []Action, truncated bool) error {
	photoFirst := len(findingColumns) + 1
	recordedBy := photoFirst + len(photoColumnTitles)

	titles := make([]column, 0, recordedBy)
	titles = append(titles, findingColumns...)
	for _, title := range photoColumnTitles {
		titles = append(titles, column{title, photoColChar})
	}
	titles = append(titles, column{"Recorded by", 16})

	if err := writeHeader(book, sheetFindings, st, titles, headerPoints); err != nil {
		return err
	}
	for i, action := range actions {
		row := i + 2
		values := []any{
			action.ID, action.ProgramName, dateValue(action.InspectionDate), action.Location, action.Finding,
			action.Recommendation, action.PicName, action.PicEmployeeID, dateValue(action.DueDate), action.Severity,
			exportStatus(action), dateValue(action.ClosingDate), action.ActionTaken, action.DaysOpen, followUpSummaryText(action),
		}
		start, err := excelize.CoordinatesToCellName(1, row)
		if err != nil {
			return err
		}
		if err := book.SetSheetRow(sheetFindings, start, &values); err != nil {
			return fmt.Errorf("write finding row: %w", err)
		}
		cell, _ := excelize.CoordinatesToCellName(recordedBy, row)
		if err := book.SetCellValue(sheetFindings, cell, action.CreatedByName); err != nil {
			return err
		}
		// Every data row has the same height, with or without photos.
		if err := book.SetRowHeight(sheetFindings, row, rowPoints); err != nil {
			return err
		}
		for col := 1; col <= recordedBy; col++ {
			style := st.text
			switch {
			case col == 1 || col == 14: // No., Days open
				style = st.number
			case col == 3 || col == 9 || col == 12: // the three date columns
				style = st.date
			case col >= photoFirst && col < recordedBy:
				style = st.photo
			}
			c, _ := excelize.CoordinatesToCellName(col, row)
			if err := book.SetCellStyle(sheetFindings, c, c, style); err != nil {
				return err
			}
		}
		photos := append(append([]Photo{}, firstN(action.BeforePhotos, 3)...), make([]Photo, 3-min(3, len(action.BeforePhotos)))...)
		photos = append(photos, firstN(action.AfterPhotos, 3)...)
		for slot, photo := range photos {
			if photo.ID == 0 {
				continue // an empty slot
			}
			a.placePhoto(book, photoFirst+slot, row, photo)
		}
	}
	if truncated {
		note, _ := excelize.CoordinatesToCellName(1, len(actions)+3)
		_ = book.SetCellValue(sheetFindings, note, fmt.Sprintf("Only the newest %d findings are included. Narrow the filters to export the rest.", maxExportRows))
	}
	last, _ := excelize.CoordinatesToCellName(recordedBy, 1)
	if err := book.AutoFilter(sheetFindings, "A1:"+last, nil); err != nil {
		return fmt.Errorf("add filter: %w", err)
	}
	return book.SetPanes(sheetFindings, &excelize.Panes{
		Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
		Selection: []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}},
	})
}

func firstN(photos []Photo, n int) []Photo {
	if len(photos) > n {
		return photos[:n]
	}
	return photos
}

func followUpSummaryText(a Action) string {
	if a.FollowUpsTotal == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d done", a.FollowUpsDone, a.FollowUpsTotal)
}

// placePhoto puts one thumbnail in a photo cell, scaled to fit the cell with a margin and centred. A photo
// that cannot be read leaves a short note in the cell instead of failing the whole export.
func (a *API) placePhoto(book *excelize.File, col, row int, photo Photo) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	unavailable := func(err error) {
		log.Printf("corrective action export: photo %d: %v", photo.ID, err)
		_ = book.SetCellValue(sheetFindings, cell, "(photo unavailable)")
	}
	if !a.insideUploadDir(photo.FileLink) {
		unavailable(fmt.Errorf("path %q is outside the upload folder", photo.FileLink))
		return
	}
	thumb, err := a.thumbnailFor(photo.FileLink)
	if err != nil {
		unavailable(err)
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		unavailable(fmt.Errorf("thumbnail is not readable: %v", err))
		return
	}
	boxW, boxH := float64(photoCellW-2*photoPad), float64(photoCellH-2*photoPad)
	scale := math.Min(1, math.Min(boxW/float64(cfg.Width), boxH/float64(cfg.Height)))
	offsetX := int((float64(photoCellW) - float64(cfg.Width)*scale) / 2)
	offsetY := int((float64(photoCellH) - float64(cfg.Height)*scale) / 2)
	if err := book.AddPictureFromBytes(sheetFindings, cell, &excelize.Picture{
		Extension: ".jpg",
		File:      thumb,
		Format: &excelize.GraphicOptions{
			ScaleX: scale, ScaleY: scale, OffsetX: offsetX, OffsetY: offsetY, Positioning: "oneCell",
			AltText: fmt.Sprintf("%s photo", photo.Phase),
		},
	}); err != nil {
		unavailable(err)
	}
}

func writeFollowUps(book *excelize.File, st sheetStyles, actions []Action) error {
	if _, err := book.NewSheet(sheetFollowUps); err != nil {
		return fmt.Errorf("create follow-ups sheet: %w", err)
	}
	if err := writeHeader(book, sheetFollowUps, st, followUpColumns, headerPoints); err != nil {
		return err
	}
	row := 2
	for _, action := range actions {
		for _, f := range action.FollowUps {
			values := []any{
				action.ID, action.ProgramName, action.Finding, f.Description, f.PicName, f.PicEmployeeID,
				dateValue(f.DueDate), followUpStatus(f), dateValue(f.DoneDate), f.ActionTaken, f.DoneByName,
			}
			start, _ := excelize.CoordinatesToCellName(1, row)
			if err := book.SetSheetRow(sheetFollowUps, start, &values); err != nil {
				return fmt.Errorf("write follow-up row: %w", err)
			}
			for col := range values {
				style := st.text
				switch col + 1 {
				case 1:
					style = st.number
				case 7, 9:
					style = st.date
				}
				c, _ := excelize.CoordinatesToCellName(col+1, row)
				if err := book.SetCellStyle(sheetFollowUps, c, c, style); err != nil {
					return err
				}
			}
			row++
		}
	}
	return book.SetPanes(sheetFollowUps, &excelize.Panes{
		Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
		Selection: []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}},
	})
}

// writeHeader writes the header row, styles it and sets every column width.
func writeHeader(book *excelize.File, sheet string, st sheetStyles, columns []column, height float64) error {
	for i, col := range columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := book.SetCellValue(sheet, cell, col.title); err != nil {
			return err
		}
		if err := book.SetCellStyle(sheet, cell, cell, st.header); err != nil {
			return err
		}
		letters, _ := excelize.ColumnNumberToName(i + 1)
		if err := book.SetColWidth(sheet, letters, letters, col.width); err != nil {
			return err
		}
	}
	return book.SetRowHeight(sheet, 1, height)
}
