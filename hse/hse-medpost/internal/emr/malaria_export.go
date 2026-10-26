package emr

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/dates"
)

const (
	exportSheet   = "Malaria screening"
	maxExportRows = 50000
)

var exportHeader = []string{
	"Test ID", "Test date", "Week", "Slide initial", "Name", "Age", "Weight (kg)", "Head of household", "Head is the patient",
	"Regency", "District", "Village", "Regency code", "District code", "Village code",
	"Work position", "Company", "Examiner", "Screening purpose", "Exam type", "Result", "Recorded at (UTC)",
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// Export is GET /api/malaria-screenings.xlsx?from=&to=&q= : the same rows as the
// table on the page (same date range and name search), but with every column the
// table leaves out. Newest test date first. A result still waiting for the smear
// is written as the word Pending so it can be filtered in Excel.
func (a *API) Export(w http.ResponseWriter, r *http.Request) {
	where, args, err := filters(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := a.DB.Query(malariaSelect+` WHERE 1=1`+where+
		fmt.Sprintf(` ORDER BY m.TestDate DESC, m.MalariaID DESC LIMIT %d`, maxExportRows), args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	book := excelize.NewFile()
	defer book.Close()
	if err := book.SetSheetName("Sheet1", exportSheet); err != nil {
		writeError(w, err)
		return
	}
	header := make([]any, len(exportHeader))
	for i, h := range exportHeader {
		header[i] = h
	}
	if err := book.SetSheetRow(exportSheet, "A1", &header); err != nil {
		writeError(w, err)
		return
	}
	line := 2
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			writeError(w, err)
			return
		}
		result := "Pending"
		if row.Result != nil {
			result = *row.Result
		}
		var age any // an empty cell, not 0, when the date of birth was unknown
		if row.AgeYears != nil {
			age = *row.AgeYears
		}
		values := []any{
			row.TestID, row.TestDate, row.Week, row.SlideInitial, row.PatientName, age, row.WeightKg, row.HeadOfHousehold, yesNo(row.HeadIsPatient),
			row.Regency, row.District, row.Village, row.RegencyCode, row.DistrictCode, row.VillageCode,
			row.WorkPosition, row.Company, row.Examiner, row.Purpose, row.ExamType, result, row.RecordedAt,
		}
		cell, err := excelize.CoordinatesToCellName(1, line)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := book.SetSheetRow(exportSheet, cell, &values); err != nil {
			writeError(w, err)
			return
		}
		line++
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}

	var buf bytes.Buffer
	if _, err := book.WriteTo(&buf); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="malaria screening - %s.xlsx"`,
		a.now().In(dates.ClinicLocation).Format("02012006-150405")))
	_, _ = w.Write(buf.Bytes())
}
