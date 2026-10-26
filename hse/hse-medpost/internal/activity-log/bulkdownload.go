package activitylog

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

var zipPathUnsafe = regexp.MustCompile(`[/\\:*?"<>|]`)

// zipFolderName turns free text into a filesystem-safe zip entry path
// segment, keeping spaces and casing for readability.
func zipFolderName(s string) string {
	s = zipPathUnsafe.ReplaceAllString(strings.TrimSpace(s), "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// DownloadProgramActivities bundles every activity for a program (honoring
// the same ?from=&to= filter as the activities feed) into a zip, one folder
// per activity containing that activity's evidence files plus a small
// info.txt. Admin only (RequireProgramManager) — this is a bulk
// export of potentially sensitive medpost records, not a staff-facing feature.
func (a *API) DownloadProgramActivities(w http.ResponseWriter, r *http.Request) {
	procID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}

	from, err := dates.Normalize(r.URL.Query().Get("from"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from: " + err.Error()})
		return
	}
	to, err := dates.Normalize(r.URL.Query().Get("to"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to: " + err.Error()})
		return
	}
	if from != "" && to != "" && from > to {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be <= to"})
		return
	}

	var healthProcedure string
	if err := a.DB.QueryRow(`SELECT HealthProcedure FROM HealthProcedureTable WHERE ProcID = ?`, procID).Scan(&healthProcedure); err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	programTitle := display.ProgramTitle(healthProcedure)

	q := `
		SELECT ActivityID, ActivityType, ActivityTitle, COALESCE(ActivitySummary,''),
		       COALESCE(ActivityDate,''), COALESCE(Location,''), COALESCE(Notes,'')
		FROM ActivityTable
		WHERE FK_ProcID = ? AND FK_SubHeaderID IS NULL`
	args := []any{procID}
	if from != "" {
		q += ` AND ActivityDate >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND ActivityDate <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY ActivityDate, ActivityID`

	rows, err := a.DB.Query(q, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	type activityRow struct {
		id                                         int64
		typ, title, summary, date, location, notes string
	}
	activities := []activityRow{}
	for rows.Next() {
		var ar activityRow
		if err := rows.Scan(&ar.id, &ar.typ, &ar.title, &ar.summary, &ar.date, &ar.location, &ar.notes); err != nil {
			rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		activities = append(activities, ar)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	rows.Close()

	if len(activities) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no activities in this range"})
		return
	}

	filename := fmt.Sprintf("%s-activities-%s.zip", zipFolderName(programTitle), rangeLabel(from, to))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	zw := zip.NewWriter(w)
	defer zw.Close()

	usedFolders := map[string]int{}
	for _, act := range activities {
		folder := fmt.Sprintf("%s - %s", act.date, zipFolderName(act.title))
		usedFolders[folder]++
		if n := usedFolders[folder]; n > 1 {
			folder = fmt.Sprintf("%s (%d)", folder, n)
		}

		if info, err := zw.Create(folder + "/info.txt"); err == nil {
			fmt.Fprintf(info, "Type: %s\nTitle: %s\nDate: %s\nLocation: %s\nSummary: %s\nNotes: %s\n",
				act.typ, act.title, act.date, act.location, act.summary, act.notes)
		}

		erows, err := a.DB.Query(`SELECT COALESCE(FileLink,'') FROM EvidenceTable WHERE FK_IDActivity = ?`, act.id)
		if err != nil {
			continue
		}
		for erows.Next() {
			var link string
			if err := erows.Scan(&link); err != nil || link == "" {
				continue
			}
			addFileToZip(zw, folder, link)
		}
		if err := erows.Err(); err != nil {
			erows.Close()
			continue
		}
		erows.Close()
	}
}

// rangeLabel makes the applied date filter visible in the filename, so two
// exports of the same program with different filters don't look identical.
func rangeLabel(from, to string) string {
	switch {
	case from != "" && to != "":
		return from + "_to_" + to
	case from != "":
		return "from_" + from
	case to != "":
		return "until_" + to
	default:
		return "all"
	}
}

func addFileToZip(zw *zip.Writer, folder, filePath string) {
	src, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := zw.Create(folder + "/" + filepath.Base(filePath))
	if err != nil {
		return
	}
	_, _ = io.Copy(dst, src)
}

// allEntryRow is the unified shape top-level and sub-header-scoped
// ActivityTable rows are flattened into so they can share one sheet.
type allEntryRow struct {
	program, category, title, date, details, location, link, loggedBy string
}

// writeSheet fills a sheet with a header row plus one row per record.
func writeSheet(f *excelize.File, sheet string, headers []string, records [][]any) {
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for r, values := range records {
		for i, v := range values {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
}

// DownloadAllEntries exports every logged activity across every program —
// both top-level activities and activities logged under a sub-header, both
// just ActivityTable rows distinguished by whether FK_SubHeaderID is set —
// into a single flat .xlsx sheet, sorted by program then date. Admin-only,
// same bulk-export sensitivity class as DownloadProgramActivities.
func (a *API) DownloadAllEntries(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT hp.HealthProcedure, act.ActivityType, act.ActivityTitle, COALESCE(act.ActivitySummary,''),
		       COALESCE(act.ActivityDate,''), COALESCE(act.Location,''), COALESCE(act.Notes,''),
		       COALESCE(act.FormLink,''), COALESCE(sh.SubHeaderName,''), cs.StaffName
		FROM ActivityTable act
		JOIN HealthProcedureTable hp ON hp.ProcID = act.FK_ProcID
		JOIN MedpostStaffTable cs ON cs.StaffID = act.FK_StaffID
		LEFT JOIN SubHeaderTable sh ON sh.SubHeaderID = act.FK_SubHeaderID`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	var allRows []allEntryRow
	for rows.Next() {
		var program, atype, title, summary, date, location, notes, formLink, subHeader, staffName string
		if err := rows.Scan(&program, &atype, &title, &summary, &date, &location, &notes, &formLink, &subHeader, &staffName); err != nil {
			rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		category := atype
		details := summary
		if subHeader != "" {
			category = "Sub-header: " + subHeader
		} else if notes != "" {
			details = strings.TrimSpace(details + "\nNotes: " + notes)
		}
		allRows = append(allRows, allEntryRow{
			program: display.ProgramTitle(program), category: category, title: title,
			date: date, details: details, location: location, link: formLink, loggedBy: staffName,
		})
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if len(allRows) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no entries to export"})
		return
	}

	sort.Slice(allRows, func(i, j int) bool {
		if allRows[i].program != allRows[j].program {
			return allRows[i].program < allRows[j].program
		}
		if allRows[i].date != allRows[j].date {
			return allRows[i].date < allRows[j].date
		}
		return allRows[i].title < allRows[j].title
	})

	records := make([][]any, len(allRows))
	for i, row := range allRows {
		records[i] = []any{row.program, row.category, row.title, row.date, row.details, row.location, row.link, row.loggedBy}
	}

	f := excelize.NewFile()
	defer f.Close()

	const sheet = "All entries"
	f.SetSheetName("Sheet1", sheet)
	writeSheet(f, sheet,
		[]string{"Program", "Category", "Title", "Date", "Details", "Location", "Link", "Logged by"},
		records)

	filename := fmt.Sprintf("all-entries-%s.xlsx", dates.Today())
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = f.WriteTo(w)
}
