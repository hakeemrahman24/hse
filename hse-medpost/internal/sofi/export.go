package sofi

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	exportLimit     = 50000
	exportSheet     = "SOFI"
	exportTimeShown = "2006-01-02 15:04"
)

// StaffExport: GET /api/sofi-log.xlsx?q=&window=. The SOFI table as an Excel file: one row per
// questionnaire with the same filters as the table, but every match rather than the newest
// listLimit, and with the five dimension scores as their own columns instead of the row's
// detail. Staff sign-in only (RequireAuth on the route). Submit times are shown in GMT+9 like
// the table.
func (a *API) StaffExport(w http.ResponseWriter, r *http.Request) {
	records, _, qerr := a.staffRecords(r, exportLimit)
	if qerr != nil {
		writeError(w, r, qerr.status, qerr.msg)
		return
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", exportSheet); err != nil {
		log.Printf("sofi: export sheet: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not build the Excel file.")
		return
	}
	for i, row := range exportRows(records, a.dimensions()) {
		cells := make([]any, len(row))
		for j, v := range row {
			cells[j] = v
		}
		if err := f.SetSheetRow(exportSheet, "A"+strconv.Itoa(i+1), &cells); err != nil {
			log.Printf("sofi: export row %d: %v", i+1, err)
			writeError(w, r, http.StatusInternalServerError, "Could not build the Excel file.")
			return
		}
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		log.Printf("sofi: export write: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not build the Excel file.")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sofi fatigue survey - %s.xlsx"`, time.Now().In(SiteZone).Format("02012006-150405")))
	_, _ = w.Write(buf.Bytes())
}

// exportRows is the header row followed by one row per record. Scores are numbers so Excel can
// sort and average them; the rest is text.
func exportRows(records []Record, dims []Dimension) [][]any {
	header := []any{"Submitted (GMT+9)", "Name", "Position", "Company", "Age", "Weeks on site", "MCU review"}
	for _, d := range dims {
		header = append(header, d.EN)
	}
	header = append(header, "Total", "Result")
	out := [][]any{header}
	for _, rec := range records {
		weeks := any(rec.WeeksOnSite)
		if rec.WeeksOverYear {
			weeks = "> 1 year" // the stored 54 is a marker, not a number of weeks
		}
		row := []any{submittedGMT9(rec.SubmittedAt), rec.Name, rec.WorkPosition, rec.Company, rec.Age, weeks, rec.MCUReviewStatus}
		for _, d := range rec.Dimensions {
			row = append(row, d.Score)
		}
		out = append(out, append(row, rec.Total, rec.Level))
	}
	return out
}

// submittedGMT9 turns a stored UTC timestamp into the site's time; anything unreadable is kept as stored.
func submittedGMT9(utc string) string {
	t, err := time.Parse(timeLayoutDB, utc)
	if err != nil {
		return utc
	}
	return t.In(SiteZone).Format(exportTimeShown)
}
