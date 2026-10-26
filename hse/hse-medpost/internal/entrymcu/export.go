// Bulk export for admin/CMO: one workbook, two sheets — Patients (one row
// per person) and MCU entries (one row per checkup, linked by PatientID).
// writeSheet is local rather than shared, matching this project's
// per-package duplication. Certificate PDFs stay a separate zip.
package entrymcu

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"hse-medpost/internal/dates"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/db"

	"hse-medpost/internal/storage"
)

var exportPathUnsafe = regexp.MustCompile(`[/\\:*?"<>|]`)

func exportSafeName(s string) string {
	s = exportPathUnsafe.ReplaceAllString(strings.TrimSpace(s), "-")
	if s == "" {
		return "untitled"
	}
	return s
}

func scanExportRows(rows *sql.Rows, n int) ([][]any, error) {
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals := make([]any, n)
		dest := make([]any, n)
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

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

const (
	patientSheetName = "Patients"
	mcuSheetName     = "MCU entries"
	mcuDataSheetName = "MCU data"
)

// patientExportColumns is the Patients sheet: identity that belongs to the
// person, including fields that are not copied onto each checkup.
var patientExportColumns = []string{
	"PatientID", "Name", "DateOfBirth", "Gender", "Nationality",
	"BloodType", "EmployeeID", "PersonalID", "Company", "WorkPosition",
}

// exportColumns is the MCU entries sheet: one checkup per row. PatientID
// joins to the Patients sheet. PersonalID, Company, and WorkPosition here
// are the values saved on that visit. Name and date of birth are repeated
// so a checkup row can be read without looking up the other sheet.
var exportColumns = []string{
	"PatientID", "CheckUpID", "PatientName", "DateOfBirth", "PersonalID",
	"Company", "WorkPosition", "CheckUpCriteria", "TaskCategory",
	"ReviewDate", "DateCheckUp", "Reviewer", "Age", "HeightCm", "WeightKg",
	"IsShortProject", "ValidityPeriodMonths",
	"BmiValue", "BmiCriteria", "BmiNotes", "Smoking", "OtherPhysicalExamNotes",
	"BpCriteria", "BpNotes", "VisionCriteria", "VisionNotes",
	"CbcCriteria", "CbcNotes", "LipidCriteria", "LipidNotes",
	"GlucoseCriteria", "GlucoseNotes", "LiverCriteria", "LiverNotes",
	"KidneyCriteria", "KidneyNotes", "UrinalysisCriteria", "UrinalysisNotes",
	"RadiologyResult", "RadiologyNotes",
	"EchocardiographyCriteria", "EchocardiographyNotes", "CtScanCriteria", "CtScanNotes",
	"UsgAbdomenCriteria", "UsgAbdomenNotes",
	"EcgResult", "TreadmillResult", "EcgTreadmillNotes",
	"HerniaHemorrhoidCriteria", "HerniaHemorrhoidNotes", "SpirometryCriteria", "SpirometryNotes",
	"AudiometryCriteria", "AudiometryNotes", "HepBCriteria", "HepBNotes", "HepACriteria", "HepANotes",
	"RectalSwabCriteria", "RectalSwabNotes", "HivCriteria",
	"ResultCriteria", "ResultValidUntil", "ResultNotes", "Recommendation", "AdditionalNotes", "CheckUpStatus",
	"ParentCheckUpID", "HasCertificate", "HcValidityStatus", "IsTerminal", "CompanyRelationCode",
}

// flatExportColumns is the MCU data sheet: one checkup per row, with the
// employee identity (gender, nationality, blood type, badge id) on that
// same row so the file does not need a second sheet.
var flatExportColumns = []string{
	"PatientID", "CheckUpID", "PatientName", "DateOfBirth", "Gender", "Nationality", "BloodType", "EmployeeID",
	"PersonalID", "Company", "WorkPosition", "CheckUpCriteria", "TaskCategory",
	"ReviewDate", "DateCheckUp", "Reviewer", "Age", "HeightCm", "WeightKg",
	"IsShortProject", "ValidityPeriodMonths",
	"BmiValue", "BmiCriteria", "BmiNotes", "Smoking", "OtherPhysicalExamNotes",
	"BpCriteria", "BpNotes", "VisionCriteria", "VisionNotes",
	"CbcCriteria", "CbcNotes", "LipidCriteria", "LipidNotes",
	"GlucoseCriteria", "GlucoseNotes", "LiverCriteria", "LiverNotes",
	"KidneyCriteria", "KidneyNotes", "UrinalysisCriteria", "UrinalysisNotes",
	"RadiologyResult", "RadiologyNotes",
	"EchocardiographyCriteria", "EchocardiographyNotes", "CtScanCriteria", "CtScanNotes",
	"UsgAbdomenCriteria", "UsgAbdomenNotes",
	"EcgResult", "TreadmillResult", "EcgTreadmillNotes",
	"HerniaHemorrhoidCriteria", "HerniaHemorrhoidNotes", "SpirometryCriteria", "SpirometryNotes",
	"AudiometryCriteria", "AudiometryNotes", "HepBCriteria", "HepBNotes", "HepACriteria", "HepANotes",
	"RectalSwabCriteria", "RectalSwabNotes", "HivCriteria",
	"ResultCriteria", "ResultValidUntil", "ResultNotes", "Recommendation", "AdditionalNotes", "CheckUpStatus",
	"ParentCheckUpID", "HasCertificate", "HcValidityStatus", "IsTerminal", "Parent", "Project", "Depth",
}

// DownloadAllEntries exports every patient and every checkup — no date
// filter, unlike the admin reports grid — as the full MCU dump.
func (a *API) DownloadAllEntries(w http.ResponseWriter, r *http.Request) {
	patientRows, err := a.DB.Query(`
		SELECT p.PatientID, p.Name, COALESCE(p.DateOfBirth,''), COALESCE(p.Gender,''),
		       COALESCE(n.Name,''), COALESCE(` + db.LatestBloodTypeSQL("p") + `,''), COALESCE(p.EmployeeID,''),
		       COALESCE(p.PersonalID,''), COALESCE(c.Name,''), COALESCE(p.WorkPosition,'')
		FROM Patient p
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		ORDER BY p.Name, p.PatientID`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	patients, err := scanExportRows(patientRows, len(patientExportColumns))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	rows, err := a.DB.Query(`
		SELECT m.FK_PatientID, m.CheckUpID, p.Name, COALESCE(p.DateOfBirth,''), COALESCE(m.PersonalID,''),
		       COALESCE(co.Name,''), COALESCE(m.WorkPosition, p.WorkPosition, ''),
		       COALESCE(c.Name,''), COALESCE(m.TaskCategoryNames,''),
		       m.ReviewDate, COALESCE(m.DateCheckUp,''), COALESCE(m.Reviewer,''),
		       m.Age, m.HeightCm, m.WeightKg, m.IsShortProject, m.ValidityPeriodMonths,
		       m.BmiValue, COALESCE(m.BmiCriteria,''), COALESCE(m.BmiNotes,''), COALESCE(m.Smoking,''), COALESCE(m.OtherPhysicalExamNotes,''),
		       COALESCE(m.BpCriteria,''), COALESCE(m.BpNotes,''), COALESCE(m.VisionCriteria,''), COALESCE(m.VisionNotes,''),
		       COALESCE(m.CbcCriteria,''), COALESCE(m.CbcNotes,''), COALESCE(m.LipidCriteria,''), COALESCE(m.LipidNotes,''),
		       COALESCE(m.GlucoseCriteria,''), COALESCE(m.GlucoseNotes,''), COALESCE(m.LiverCriteria,''), COALESCE(m.LiverNotes,''),
		       COALESCE(m.KidneyCriteria,''), COALESCE(m.KidneyNotes,''), COALESCE(m.UrinalysisCriteria,''), COALESCE(m.UrinalysisNotes,''),
		       COALESCE(m.RadiologyResult,''), COALESCE(m.RadiologyNotes,''),
		       COALESCE(m.EchocardiographyCriteria,''), COALESCE(m.EchocardiographyNotes,''), COALESCE(m.CtScanCriteria,''), COALESCE(m.CtScanNotes,''),
		       COALESCE(m.UsgAbdomenCriteria,''), COALESCE(m.UsgAbdomenNotes,''),
		       COALESCE(m.EcgResult,''), COALESCE(m.TreadmillResult,''), COALESCE(m.EcgTreadmillNotes,''),
		       COALESCE(m.HerniaHemorrhoidCriteria,''), COALESCE(m.HerniaHemorrhoidNotes,''), COALESCE(m.SpirometryCriteria,''), COALESCE(m.SpirometryNotes,''),
		       COALESCE(m.AudiometryCriteria,''), COALESCE(m.AudiometryNotes,''), COALESCE(m.HepBCriteria,''), COALESCE(m.HepBNotes,''), COALESCE(m.HepACriteria,''), COALESCE(m.HepANotes,''),
		       COALESCE(m.RectalSwabCriteria,''), COALESCE(m.RectalSwabNotes,''), COALESCE(m.HivCriteria,''),
		       COALESCE(m.ResultCriteria,''), COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,''), COALESCE(m.Recommendation,''), COALESCE(m.AdditionalNotes,''), COALESCE(m.CheckUpStatus,''),
		       COALESCE(parent.CheckUpID,''), CASE WHEN m.HealthCertificateFileName IS NOT NULL THEN 'Yes' ELSE 'No' END,
		       COALESCE(m.HcValidityStatus,''), m.IsTerminal, COALESCE(cr.RelationCode,'')
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		LEFT JOIN Company co ON co.CompanyID = COALESCE(m.FK_CompanyID, p.FK_CompanyID)
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		LEFT JOIN McuRecord parent ON parent.McuRecordID = m.FK_ParentRecordID
		LEFT JOIN CompanyRelation cr ON cr.CompanyRelationID = m.FK_CompanyRelationID
		ORDER BY m.ReviewDate, m.CheckUpID`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	records, err := scanExportRows(rows, len(exportColumns))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	if len(patients) == 0 && len(records) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no entries to export"})
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	f.SetSheetName("Sheet1", patientSheetName)
	writeSheet(f, patientSheetName, patientExportColumns, patients)
	if _, err := f.NewSheet(mcuSheetName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build workbook"})
		return
	}
	writeSheet(f, mcuSheetName, exportColumns, records)

	filename := fmt.Sprintf("mcu-entries-%s.xlsx", dates.Today())
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = f.WriteTo(w)
}

// relationCells is one company's place in the company-relation table:
// parent, project, and depth. A checkup exports exactly one parent, so a
// company on several projects contributes only its first relation here (by
// project) and only when the checkup itself names none.
type relationCells struct {
	parent  string
	project string
	depth   string
}

type relationFact struct {
	parent  string
	project string
	depth   sql.NullInt64
}

func (a *API) relationsByCompany() (map[string]relationCells, error) {
	rows, err := a.DB.Query(`
		SELECT c.Name, COALESCE(parent.Name,''), COALESCE(cr.Project,''), cr.Depth
		FROM CompanyRelation cr
		JOIN Company c ON c.CompanyID = cr.FK_CompanyID
		LEFT JOIN Company parent ON parent.CompanyID = cr.FK_ParentID
		ORDER BY c.Name, COALESCE(cr.Project,'')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := map[string][]relationFact{}
	var names []string
	for rows.Next() {
		var name, parent, project string
		var depth sql.NullInt64
		if err := rows.Scan(&name, &parent, &project, &depth); err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if _, seen := grouped[name]; !seen {
			names = append(names, name)
		}
		grouped[name] = append(grouped[name], relationFact{parent: parent, project: project, depth: depth})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]relationCells, len(names))
	for _, name := range names {
		out[name] = relationCellsFor(grouped[name])
	}
	return out, nil
}

// relationCellsFor keeps only the first relation: a visit has one parent.
func relationCellsFor(facts []relationFact) relationCells {
	if len(facts) == 0 {
		return relationCells{}
	}
	first := facts[0]
	cells := relationCells{parent: first.parent, project: first.project}
	if first.depth.Valid {
		cells.depth = strconv.FormatInt(first.depth.Int64, 10)
	}
	return cells
}

// fillMissingRelations completes the Parent/Project/Depth cells (the last
// three of each row) for checkups that carry no company relation of their
// own, using the visit company's first relation.
func fillMissingRelations(records [][]any, relations map[string]relationCells) {
	companyCol := slices.Index(flatExportColumns, "Company")
	parentCol := slices.Index(flatExportColumns, "Parent")
	for _, row := range records {
		if exportText(row[parentCol]) != "" || exportText(row[parentCol+1]) != "" {
			continue
		}
		cells := relations[strings.TrimSpace(exportText(row[companyCol]))]
		row[parentCol] = cells.parent
		row[parentCol+1] = cells.project
		row[parentCol+2] = cells.depth
	}
}

func exportText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// DownloadMcuDataSheet exports every checkup as one flat sheet. Employee
// identity sits on the checkup row. Company relation (parent, project,
// depth) is the one the checkup is tied to; checkups with none fall back to
// the visit company's first relation, so every row has a single parent.
//
// An optional ?limit=N keeps only the N latest checkups (by review date, then
// checkup id); the sheet still lists them oldest first like the full export.
// An optional ?ids=1,2,3 keeps only those checkups, in that order (the rows the
// MCU data tab shows, top to bottom); it wins over limit.
func (a *API) DownloadMcuDataSheet(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a whole number of 1 or more"})
			return
		}
		limit = n
	}
	ids, ok := parseExportIDs(r.URL.Query().Get("ids"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids must be comma-separated whole numbers"})
		return
	}
	orderBy := "ORDER BY m.ReviewDate, m.CheckUpID"
	args := []any{}
	where := ""
	if len(ids) > 0 {
		limit = 0
		where = "WHERE m.McuRecordID IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")\n\t\t"
		for _, id := range ids {
			args = append(args, id)
		}
		// Keep the caller's order so the sheet matches the table on screen.
		orderBy = "ORDER BY CASE m.McuRecordID"
		for range ids {
			orderBy += " WHEN ? THEN ?"
		}
		orderBy += " END"
		for i, id := range ids {
			args = append(args, id, i)
		}
	} else if limit > 0 {
		orderBy = "ORDER BY m.ReviewDate DESC, m.CheckUpID DESC LIMIT ?"
		args = append(args, limit)
	}
	rows, err := a.DB.Query(`
		SELECT m.FK_PatientID, m.CheckUpID, p.Name, COALESCE(p.DateOfBirth,''),
		       COALESCE(p.Gender,''), COALESCE(n.Name,''), COALESCE(m.BloodType,''), COALESCE(p.EmployeeID,''),
		       COALESCE(m.PersonalID,''),
		       COALESCE(co.Name,''), COALESCE(m.WorkPosition, p.WorkPosition, ''),
		       COALESCE(c.Name,''), COALESCE(m.TaskCategoryNames,''),
		       m.ReviewDate, COALESCE(m.DateCheckUp,''), COALESCE(m.Reviewer,''),
		       m.Age, m.HeightCm, m.WeightKg, m.IsShortProject, m.ValidityPeriodMonths,
		       m.BmiValue, COALESCE(m.BmiCriteria,''), COALESCE(m.BmiNotes,''), COALESCE(m.Smoking,''), COALESCE(m.OtherPhysicalExamNotes,''),
		       COALESCE(m.BpCriteria,''), COALESCE(m.BpNotes,''), COALESCE(m.VisionCriteria,''), COALESCE(m.VisionNotes,''),
		       COALESCE(m.CbcCriteria,''), COALESCE(m.CbcNotes,''), COALESCE(m.LipidCriteria,''), COALESCE(m.LipidNotes,''),
		       COALESCE(m.GlucoseCriteria,''), COALESCE(m.GlucoseNotes,''), COALESCE(m.LiverCriteria,''), COALESCE(m.LiverNotes,''),
		       COALESCE(m.KidneyCriteria,''), COALESCE(m.KidneyNotes,''), COALESCE(m.UrinalysisCriteria,''), COALESCE(m.UrinalysisNotes,''),
		       COALESCE(m.RadiologyResult,''), COALESCE(m.RadiologyNotes,''),
		       COALESCE(m.EchocardiographyCriteria,''), COALESCE(m.EchocardiographyNotes,''), COALESCE(m.CtScanCriteria,''), COALESCE(m.CtScanNotes,''),
		       COALESCE(m.UsgAbdomenCriteria,''), COALESCE(m.UsgAbdomenNotes,''),
		       COALESCE(m.EcgResult,''), COALESCE(m.TreadmillResult,''), COALESCE(m.EcgTreadmillNotes,''),
		       COALESCE(m.HerniaHemorrhoidCriteria,''), COALESCE(m.HerniaHemorrhoidNotes,''), COALESCE(m.SpirometryCriteria,''), COALESCE(m.SpirometryNotes,''),
		       COALESCE(m.AudiometryCriteria,''), COALESCE(m.AudiometryNotes,''), COALESCE(m.HepBCriteria,''), COALESCE(m.HepBNotes,''), COALESCE(m.HepACriteria,''), COALESCE(m.HepANotes,''),
		       COALESCE(m.RectalSwabCriteria,''), COALESCE(m.RectalSwabNotes,''), COALESCE(m.HivCriteria,''),
		       COALESCE(m.ResultCriteria,''), COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,''), COALESCE(m.Recommendation,''), COALESCE(m.AdditionalNotes,''), COALESCE(m.CheckUpStatus,''),
		       COALESCE(parent.CheckUpID,''), CASE WHEN m.HealthCertificateFileName IS NOT NULL THEN 'Yes' ELSE 'No' END,
		       COALESCE(m.HcValidityStatus,''), m.IsTerminal,
		       COALESCE(rp.Name,''), COALESCE(cr.Project,''), COALESCE(CAST(cr.Depth AS CHAR),'')
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company co ON co.CompanyID = COALESCE(m.FK_CompanyID, p.FK_CompanyID)
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		LEFT JOIN McuRecord parent ON parent.McuRecordID = m.FK_ParentRecordID
		LEFT JOIN CompanyRelation cr ON cr.CompanyRelationID = m.FK_CompanyRelationID
		LEFT JOIN Company rp ON rp.CompanyID = cr.FK_ParentID
		`+where+orderBy, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	records, err := scanExportRows(rows, len(flatExportColumns))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if limit > 0 {
		// The query picked the latest rows newest first; flip back to oldest first.
		slices.Reverse(records)
	}
	relations, err := a.relationsByCompany()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	fillMissingRelations(records, relations)
	if len(records) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no entries to export"})
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	f.SetSheetName("Sheet1", mcuDataSheetName)
	writeSheet(f, mcuDataSheetName, flatExportColumns, records)

	filename := fmt.Sprintf("mcu-data-%s.xlsx", dates.Today())
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = f.WriteTo(w)
}

// parseExportIDs reads a comma-separated list of record ids. An empty string is
// no filter; any entry that is not a positive whole number rejects the list.
func parseExportIDs(raw string) ([]int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n < 1 {
			return nil, false
		}
		ids = append(ids, n)
	}
	return ids, true
}

// DownloadAllCertificates zips every closed record's health certificate,
// generating (and persisting, same as a single DownloadCertificate call)
// any that don't exist on disk yet — so this always produces a complete
// set rather than only whatever staff happened to have downloaded before.
func (a *API) DownloadAllCertificates(cfg CertConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := a.DB.Query(`SELECT McuRecordID FROM McuRecord WHERE CheckUpStatus = 'Close' ORDER BY ReviewDate`)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		rows.Close()

		if len(ids) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no closed records with certificates"})
			return
		}

		a.writeCertificateZip(w, ids, cfg, fmt.Sprintf("mcu-certificates-%s.zip", dates.Today()))
	}
}

// DownloadLatestEligibleCertificates zips the n newest certificate-eligible
// records, one per patient (the same "current checkup" the MCU data tab
// shows). n is the count the user picked, 1 through 20. Eligible matches
// DownloadCertificate: closed, and not Archived/Exit or ReEvaluate/Overdue.
func (a *API) DownloadLatestEligibleCertificates(cfg CertConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, ok := parseCertCount(r.URL.Query().Get("n"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pick a number from 1 to 20"})
			return
		}
		ids, err := a.latestEligibleCertificateIDs(n, dates.ClinicNow())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if len(ids) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no eligible certificates"})
			return
		}
		a.writeCertificateZip(w, ids, cfg, fmt.Sprintf("mcu-latest-eligible-certificates-%d-%s.zip", n, dates.Today()))
	}
}

func parseCertCount(s string) (int, bool) {
	n, ok := parsePositiveInt(strings.TrimSpace(s))
	if !ok || n > 20 {
		return 0, false
	}
	return n, true
}

func (a *API) latestEligibleCertificateIDs(n int, today time.Time) ([]int64, error) {
	rows, err := a.DB.Query(`
		SELECT m.McuRecordID, COALESCE(m.CheckUpStatus,''), m.ResultValidUntil, m.ResultCriteria, m.HcValidityStatus
		FROM McuRecord m
		WHERE NOT EXISTS (
			SELECT 1 FROM McuRecord newer
			WHERE newer.FK_PatientID = m.FK_PatientID
			  AND (newer.ReviewDate > m.ReviewDate
			       OR (newer.ReviewDate = m.ReviewDate AND newer.McuRecordID > m.McuRecordID))
		)
		ORDER BY m.ReviewDate DESC, m.McuRecordID DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var status string
		var validUntil, result, stored sql.NullString
		if err := rows.Scan(&id, &status, &validUntil, &result, &stored); err != nil {
			return nil, err
		}
		if !certificateDownloadEligible(status, validUntil, result, stored, today) {
			continue
		}
		ids = append(ids, id)
		if len(ids) == n {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func certificateDownloadEligible(status string, validUntil, result, stored sql.NullString, today time.Time) bool {
	if !result.Valid || !certifiableResult(result.String) || status != "Close" {
		return false
	}
	st := status
	var vu, rc, hc *string
	if validUntil.Valid {
		vu = &validUntil.String
	}
	rc = &result.String
	if stored.Valid {
		hc = &stored.String
	}
	live := displayHcValidityStatus(hc, &st, vu, rc, today)
	return live != "Archived / Exit" && live != "ReEvaluate / Overdue" && !strings.HasPrefix(live, "Renewed by ")
}

func (a *API) writeCertificateZip(w http.ResponseWriter, ids []int64, cfg CertConfig, filename string) {
	w.Header().Set("Content-Type", "application/zip")
	// The URL ends in .zip, which Cloudflare caches by default. no-store keeps
	// the edge from serving an old certificate bundle.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	zw := zip.NewWriter(w)
	defer zw.Close()

	usedNames := map[string]int{}
	for _, id := range ids {
		d, path, ok := a.ensureCertificateFile(id, cfg)
		if !ok {
			continue
		}
		name := strings.TrimSuffix(certificateOpenFilename(d.CheckUpID, d.CompanyAbbrev, d.Name, d.ValidUntil), ".pdf")
		usedNames[name]++
		if n := usedNames[name]; n > 1 {
			name = fmt.Sprintf("%s (%d)", name, n)
		}
		addFileToZipAt(zw, name+".pdf", path)
	}
}

func (a *API) ensureCertificateFile(id int64, cfg CertConfig) (certData, string, bool) {
	d, ok := a.fetchCertData(id)
	if !ok {
		return d, "", false
	}
	if d.CertFilePath != "" && !fileMissing(d.CertFilePath) {
		return d, d.CertFilePath, true
	}
	pdfBytes, err := GenerateCertificatePDF(d, cfg)
	if err != nil {
		return d, "", false
	}
	pdfBytes, err = a.signCertificatePDF(cfg, d.Reviewer, pdfBytes)
	if err != nil {
		return d, "", false
	}
	dir := storage.McuCertificateDir(cfg.UploadDir)
	filename := storage.McuCertificateFilename(dates.Today(), d.CheckUpID, d.Name, d.ValidUntil)
	saved, err := storage.Save(dir, filename, bytes.NewReader(pdfBytes))
	if err != nil {
		return d, "", false
	}
	_, _ = a.DB.Exec(`UPDATE McuRecord SET HealthCertificateFileName = ? WHERE McuRecordID = ?`, saved, id)
	return d, saved, true
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}

func addFileToZipAt(zw *zip.Writer, entryName, filePath string) {
	src, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := zw.Create(entryName)
	if err != nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
