// Bulk import: the inverse of export.go's DownloadAllEntries. A current
// workbook has two sheets — Patients, then MCU entries. An older one-sheet
// file (checkup rows only, with Gender/Nationality/EmployeeID on that sheet)
// still imports. This is a structural bulk load, not a simulation of the
// live /mcu clinical entry form: it deliberately skips validateCheckupDate's
// 180/365-day short-project gate (meaningless for historical data — most
// imported rows are, by definition, old) and does not recompute
// ResultValidUntil/HcValidityStatus/IsTerminal via calculateValidityDate/
// closeParentRecord — it trusts whatever the spreadsheet cell says, since
// re-deriving "final" historical state from current business rules could
// silently rewrite history that was correct under whatever rules applied
// when the record was actually created.
package entrymcu

import (
	"fmt"
	"hse-medpost/internal/dates"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/db"
	"hse-medpost/internal/patient"
)

// ImportRowError is one row's failure — the row number is 1-based and counts
// the header row as row 1, matching what a user sees when they open the
// file in Excel.
type ImportRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type ImportResult struct {
	Created int              `json:"created"`
	Updated int              `json:"updated"`
	Skipped int              `json:"skipped"`
	Errors  []ImportRowError `json:"errors"`
}

const maxImportErrors = 100

// ImportMcuRecords — RequireAdmin. multipart/form-data with a "file" field
// holding the .xlsx. Every row is handled independently (no single
// transaction spanning the whole file): one bad row shouldn't sink an
// otherwise-good 500-row import, so errors are collected per row and the
// good rows still commit.
func (a *API) ImportMcuRecords(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upload"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file is required"})
		return
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read xlsx file"})
		return
	}
	defer f.Close()

	result, err := a.ImportWorkbook(f)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ImportWorkbook loads a Patients sheet when the workbook has one, then
// inserts or updates each MCU entries row matched by CheckUpID.
func (a *API) ImportWorkbook(f *excelize.File) (ImportResult, error) {
	mcuSheet, patientErrs, sheetErr := a.prepareImportSheets(f)
	if sheetErr != nil {
		return ImportResult{}, sheetErr
	}
	rows, err := f.GetRows(mcuSheet)
	if err != nil {
		return ImportResult{}, fmt.Errorf("could not read sheet rows")
	}
	if len(rows) < 1 {
		return ImportResult{}, fmt.Errorf("sheet is empty")
	}

	colIndex := headerIndex(rows[0])
	get := cellGetter(colIndex)
	cols := importColumnSet{
		personalID:      hasColumn(colIndex, "personalid"),
		bloodType:       hasColumn(colIndex, "bloodtype"),
		timestamp:       hasColumn(colIndex, "timestamp"),
		certificateName: hasColumn(colIndex, "healthcertificatefilename"),
		reviewYear:      hasColumn(colIndex, "reviewyear"),
		submissionDate:  hasColumn(colIndex, "submissiondate"),
		daysToReview:    hasColumn(colIndex, "daystoreview"),
	}

	result := ImportResult{Errors: patientErrs}
	for rowNum := 1; rowNum < len(rows); rowNum++ {
		row := rows[rowNum]
		if isBlankRow(row) {
			result.Skipped++
			continue
		}

		var existedBefore bool
		if checkUpID := get(row, "CheckUpID"); checkUpID != "" {
			var tmp int64
			existedBefore = a.DB.QueryRow(`SELECT McuRecordID FROM McuRecord WHERE CheckUpID = ?`, checkUpID).Scan(&tmp) == nil
		}

		if err := a.importOneRow(row, get, cols); err != nil {
			if len(result.Errors) < maxImportErrors {
				result.Errors = append(result.Errors, ImportRowError{Row: rowNum + 1, Message: err.Error()})
			}
			continue
		}
		if existedBefore {
			result.Updated++
		} else {
			result.Created++
		}
	}
	return result, nil
}

func isBlankRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

type importColumnSet struct {
	personalID      bool
	bloodType       bool
	timestamp       bool
	certificateName bool
	reviewYear      bool
	submissionDate  bool
	daysToReview    bool
}

func hasColumn(colIndex map[string]int, name string) bool {
	_, ok := colIndex[name]
	return ok
}

func headerIndex(header []string) map[string]int {
	colIndex := map[string]int{}
	for i, h := range header {
		colIndex[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return colIndex
}

func cellGetter(colIndex map[string]int) func([]string, string) string {
	return func(row []string, col string) string {
		i, ok := colIndex[strings.ToLower(col)]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
}

// prepareImportSheets loads the Patients sheet when the workbook has one,
// then returns the checkup sheet. A legacy file with only "MCU entries"
// (or any single sheet) skips the patient pass.
func (a *API) prepareImportSheets(f *excelize.File) (string, []ImportRowError, error) {
	errs := []ImportRowError{}
	mcuSheet := ""
	for _, name := range f.GetSheetList() {
		if strings.EqualFold(name, patientSheetName) {
			rows, err := f.GetRows(name)
			if err != nil {
				return "", nil, fmt.Errorf("could not read Patients sheet")
			}
			errs = append(errs, a.importPatientSheet(rows)...)
		}
		if strings.EqualFold(name, mcuSheetName) {
			mcuSheet = name
		}
	}
	if mcuSheet == "" {
		mcuSheet = f.GetSheetName(0)
	}
	if mcuSheet == "" || strings.EqualFold(mcuSheet, patientSheetName) {
		for _, name := range f.GetSheetList() {
			if !strings.EqualFold(name, patientSheetName) {
				mcuSheet = name
				break
			}
		}
	}
	if mcuSheet == "" || strings.EqualFold(mcuSheet, patientSheetName) {
		return "", nil, fmt.Errorf("workbook has no MCU entries sheet")
	}
	return mcuSheet, errs, nil
}

func (a *API) importPatientSheet(rows [][]string) []ImportRowError {
	if len(rows) < 1 {
		return []ImportRowError{{Row: 1, Message: "Patients sheet is empty"}}
	}
	colIndex := headerIndex(rows[0])
	if _, ok := colIndex["name"]; !ok {
		return []ImportRowError{{Row: 1, Message: "Patients sheet needs a Name column"}}
	}
	get := cellGetter(colIndex)
	var errs []ImportRowError
	for rowNum := 1; rowNum < len(rows); rowNum++ {
		row := rows[rowNum]
		if isBlankRow(row) {
			continue
		}
		if err := a.upsertImportedPatient(get, row); err != nil && len(errs) < maxImportErrors {
			errs = append(errs, ImportRowError{Row: rowNum + 1, Message: "Patients: " + err.Error()})
		}
	}
	return errs
}

func (a *API) upsertImportedPatient(get func([]string, string) string, row []string) error {
	name := sheetText(get(row, "Name"))
	if name == "" {
		return fmt.Errorf("Name is required")
	}
	nationalityID, err := a.findOrCreateByName("NationalityTable", "NationalityID", get(row, "Nationality"))
	if err != nil {
		return err
	}
	companyID, err := a.findOrCreateByName("Company", "CompanyID", get(row, "Company"))
	if err != nil {
		return err
	}
	personalID, err := patient.NormalizePersonalID(get(row, "PersonalID"), get(row, "Nationality"))
	if err != nil {
		return err
	}
	dob := parseImportDate(get(row, "DateOfBirth"))
	gender := nullIfEmptyMcu(get(row, "Gender"))
	employeeID := nullIfEmptyMcu(get(row, "EmployeeID"))
	position := nullIfEmptyMcu(sheetText(get(row, "WorkPosition")))
	personal := nullIfEmptyMcu(personalID)

	var id int64
	if raw := get(row, "PatientID"); raw != "" {
		if parsed := parseImportInt(raw); parsed != nil {
			if a.DB.QueryRow(`SELECT PatientID FROM Patient WHERE PatientID = ?`, *parsed).Scan(&id) == nil {
				_, err = a.DB.Exec(`UPDATE Patient SET Name=?, Gender=?, DateOfBirth=?, FK_NationalityID=?, EmployeeID=?, FK_CompanyID=?, WorkPosition=?, PersonalID=? WHERE PatientID=?`,
					name, gender, nullIfEmptyMcu(dob), nationalityID, employeeID, companyID, position, personal, id)
				return err
			}
		}
	}
	if dob != "" && a.DB.QueryRow(`SELECT PatientID FROM Patient WHERE LOWER(Name) = LOWER(?) AND DateOfBirth = ?`, name, dob).Scan(&id) == nil {
		_, err = a.DB.Exec(`UPDATE Patient SET Name=?, Gender=?, DateOfBirth=?, FK_NationalityID=?, EmployeeID=?, FK_CompanyID=?, WorkPosition=?, PersonalID=? WHERE PatientID=?`,
			name, gender, dob, nationalityID, employeeID, companyID, position, personal, id)
		return err
	}
	_, err = a.DB.Exec(`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_NationalityID, EmployeeID, FK_CompanyID, WorkPosition, PersonalID) VALUES (?,?,?,?,?,?,?,?)`,
		name, gender, nullIfEmptyMcu(dob), nationalityID, employeeID, companyID, position, personal)
	if err != nil {
		return fmt.Errorf("could not create patient %q: %w", name, err)
	}
	return nil
}

// findOrCreateByName resolves a lookup row by case-insensitive Name match,
// creating it if missing — same "seen in the import isn't seeded yet, add
// it" behavior the plan's original deferred one-time-import design called
// for (plan.md §"Explicitly deferred", step 2), just implemented here
// instead since this importer replaces that deferred design.
func (a *API) findOrCreateByName(table, idColumn, name string) (*int64, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, nil
	}
	var id int64
	err := a.DB.QueryRow(`SELECT `+idColumn+` FROM `+table+` WHERE LOWER(Name) = LOWER(?)`, name).Scan(&id)
	if err == nil {
		return &id, nil
	}
	res, err := a.DB.Exec(`INSERT INTO `+table+` (Name) VALUES (?)`, name)
	if err != nil {
		return nil, fmt.Errorf("could not create %s %q: %w", table, name, err)
	}
	newID, _ := res.LastInsertId()
	return &newID, nil
}

// findOrCreateTaskCategoryNames handles the bulk importer's "TaskCategory"
// column now that task category is multi-select (see McuRecord.
// TaskCategoryNames) — the Excel cell may hold several names ("Food Handler;
// Work at Height", legacy exports might instead use commas), so this splits
// on either delimiter, find-or-creates each one, and rejoins the *canonical*
// TaskCategoryTable spellings with "; " — same normalization purpose as
// findOrCreateByName's own whitespace-collapsing, just across a list.
func (a *API) findOrCreateTaskCategoryNames(raw string) (*string, error) {
	parts := splitTaskCategories(raw)
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		id, err := a.findOrCreateByName("TaskCategoryTable", "TaskCategoryID", part)
		if err != nil {
			return nil, err
		}
		if id == nil {
			continue
		}
		if name := a.resolveLookupName("TaskCategoryTable", "TaskCategoryID", id); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	joined := strings.Join(names, "; ")
	return &joined, nil
}

// findOrCreatePatientID matches on Name+DateOfBirth (case-insensitive name,
// exact DOB string) — the same best-effort dedupe key the plan's original
// migration design used (plan.md §4), since the legacy data has no stable
// badge/NIK identifier to key off instead.
func (a *API) findOrCreatePatientID(name, dob, gender string, nationalityID *int64, employeeID string, companyID *int64, workPosition string) (int64, error) {
	name = sheetText(name)
	workPosition = sheetText(workPosition)
	if name == "" {
		return 0, fmt.Errorf("patient name is required")
	}

	var id int64
	if dob != "" {
		err := a.DB.QueryRow(`SELECT PatientID FROM Patient WHERE LOWER(Name) = LOWER(?) AND DateOfBirth = ?`, name, dob).Scan(&id)
		if err == nil {
			return id, nil
		}
	} else {
		err := a.DB.QueryRow(`SELECT PatientID FROM Patient WHERE LOWER(Name) = LOWER(?)`, name).Scan(&id)
		if err == nil {
			return id, nil
		}
	}

	res, err := a.DB.Exec(
		`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_NationalityID, EmployeeID, FK_CompanyID, WorkPosition)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, nullIfEmptyMcu(gender), nullIfEmptyMcu(dob), nationalityID, nullIfEmptyMcu(employeeID), companyID, nullIfEmptyMcu(workPosition),
	)
	if err != nil {
		return 0, fmt.Errorf("could not create patient %q: %w", name, err)
	}
	newID, _ := res.LastInsertId()
	return newID, nil
}

func parseImportFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseImportInt(s string) *int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64) // tolerate "12.0"-style cells from Excel numeric formatting
	if err != nil {
		return nil
	}
	i := int64(v)
	return &i
}

func parseImportBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "true", "1":
		return true
	default:
		return false
	}
}

// parseImportDate normalizes common spreadsheet date shapes to YYYY-MM-DD;
// Excel auto-converts a typed/pasted date to its own display format (or, if
// the cell has no date number-format applied, to a raw serial-number
// string) rather than keeping the ISO text this app stores, so both need
// handling — anything else is passed through as-is (it's a TEXT column, so
// an unrecognized value doesn't hard-fail the row, but downstream date
// parsing elsewhere in the app will just ignore it).
func parseImportDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		if t, err := excelize.ExcelDateToTime(v, false); err == nil {
			return t.Format("2006-01-02")
		}
	}
	for _, layout := range []string{"2006-01-02", "1/2/2006", "01/02/2006", "2-Jan-2006", "2 January 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// importOneRow resolves lookups/patient, then inserts a new McuRecord or
// updates an existing one matched by CheckUpID (round-tripping an exported-
// then-edited row) — CheckUpID is generated when blank, same MedC-prefixed
// shape the clinical entry form uses, made unique with the row's Excel
// position since many rows can be imported within the same second.
func (a *API) importOneRow(row []string, get func([]string, string) string, cols importColumnSet) error {
	patientName := get(row, "PatientName")
	if patientName == "" {
		return fmt.Errorf("PatientName is required")
	}

	nationalityID, err := a.findOrCreateByName("NationalityTable", "NationalityID", get(row, "Nationality"))
	if err != nil {
		return err
	}
	companyID, err := a.findOrCreateByName("Company", "CompanyID", get(row, "Company"))
	if err != nil {
		return err
	}
	criteriaID, err := a.findOrCreateByName("CheckUpCriteriaTable", "CheckUpCriteriaID", get(row, "CheckUpCriteria"))
	if err != nil {
		return err
	}
	taskCategoryNames, err := a.findOrCreateTaskCategoryNames(get(row, "TaskCategory"))
	if err != nil {
		return err
	}

	dob := parseImportDate(get(row, "DateOfBirth"))
	var patientID int64
	if raw := get(row, "PatientID"); raw != "" {
		if parsed := parseImportInt(raw); parsed != nil {
			var tmp int64
			var storedName, storedDOB string
			err := a.DB.QueryRow(`SELECT PatientID, Name, COALESCE(DateOfBirth,'') FROM Patient WHERE PatientID = ?`, *parsed).Scan(&tmp, &storedName, &storedDOB)
			if err == nil {
				if !strings.EqualFold(sheetText(storedName), sheetText(patientName)) || (dob != "" && storedDOB != "" && storedDOB != dob) {
					return fmt.Errorf("PatientID %d is %q (DOB %s), not %q (DOB %s)", *parsed, storedName, storedDOB, patientName, dob)
				}
				patientID = tmp
			}
		}
	}
	if patientID == 0 {
		var err error
		patientID, err = a.findOrCreatePatientID(
			patientName, dob, get(row, "Gender"), nationalityID, get(row, "EmployeeID"), companyID, get(row, "WorkPosition"),
		)
		if err != nil {
			return err
		}
	}

	var parentRecordID *int64
	if parentCheckUpID := get(row, "ParentCheckUpID"); parentCheckUpID != "" {
		var id int64
		if err := a.DB.QueryRow(`SELECT McuRecordID FROM McuRecord WHERE CheckUpID = ?`, parentCheckUpID).Scan(&id); err == nil {
			parentRecordID = &id
		}
		// Not found: left nil rather than failing the row — the parent may
		// simply not have been imported yet if rows aren't in dependency
		// order; re-running the import after all rows land resolves it.
	}

	var companyRelationID *int64
	if code := get(row, "CompanyRelationCode"); code != "" {
		var id int64
		if err := a.DB.QueryRow(`SELECT CompanyRelationID FROM CompanyRelation WHERE RelationCode = ?`, relationCodeFor(code)).Scan(&id); err == nil {
			companyRelationID = &id
		}
	}

	reviewDate := parseImportDate(get(row, "ReviewDate"))
	dateCheckUp := parseImportDate(get(row, "DateCheckUp"))
	if reviewDate == "" {
		reviewDate = dateCheckUp
	}
	if reviewDate == "" {
		reviewDate = dates.Today()
	}

	checkUpID := get(row, "CheckUpID")
	if checkUpID == "" {
		checkUpID = fmt.Sprintf("MedC%d", time.Now().UnixNano()%1_000_000_000)
	}

	timestamp := time.Now().Format("2006-01-02T15:04:05")
	if cols.timestamp {
		if ts := parseImportTimestamp(get(row, "Timestamp")); ts != "" {
			timestamp = ts
		}
	}

	m := McuRecord{
		PatientID:                patientID,
		Timestamp:                timestamp,
		ReviewDate:               reviewDate,
		CheckUpID:                checkUpID,
		Reviewer:                 nullableString(sheetText(get(row, "Reviewer"))),
		CheckUpCriteriaID:        criteriaID,
		TaskCategoryNames:        taskCategoryNames,
		Age:                      parseImportInt(get(row, "Age")),
		HeightCm:                 parseImportFloat(get(row, "HeightCm")),
		WeightKg:                 parseImportFloat(get(row, "WeightKg")),
		CompanyID:                companyID,
		WorkPosition:             nullableString(sheetText(get(row, "WorkPosition"))),
		PersonalID:               personalIDForImport(get(row, "PersonalID"), cols.personalID),
		DateCheckUp:              nullableString(dateCheckUp),
		IsShortProject:           parseImportBool(get(row, "IsShortProject")),
		ValidityPeriodMonths:     parseImportInt(get(row, "ValidityPeriodMonths")),
		BmiValue:                 parseImportFloat(get(row, "BmiValue")),
		BmiCriteria:              nullableString(get(row, "BmiCriteria")),
		BmiNotes:                 nullableString(get(row, "BmiNotes")),
		Smoking:                  nullableString(get(row, "Smoking")),
		OtherPhysicalExamNotes:   nullableString(get(row, "OtherPhysicalExamNotes")),
		BloodType:                nullableString(get(row, "BloodType")),
		BpCriteria:               nullableString(get(row, "BpCriteria")),
		BpNotes:                  nullableString(get(row, "BpNotes")),
		VisionCriteria:           nullableString(get(row, "VisionCriteria")),
		VisionNotes:              nullableString(get(row, "VisionNotes")),
		CbcCriteria:              nullableString(get(row, "CbcCriteria")),
		CbcNotes:                 nullableString(get(row, "CbcNotes")),
		LipidCriteria:            nullableString(get(row, "LipidCriteria")),
		LipidNotes:               nullableString(get(row, "LipidNotes")),
		GlucoseCriteria:          nullableString(get(row, "GlucoseCriteria")),
		GlucoseNotes:             nullableString(get(row, "GlucoseNotes")),
		LiverCriteria:            nullableString(get(row, "LiverCriteria")),
		LiverNotes:               nullableString(get(row, "LiverNotes")),
		KidneyCriteria:           nullableString(get(row, "KidneyCriteria")),
		KidneyNotes:              nullableString(get(row, "KidneyNotes")),
		UrinalysisCriteria:       nullableString(get(row, "UrinalysisCriteria")),
		UrinalysisNotes:          nullableString(get(row, "UrinalysisNotes")),
		RadiologyResult:          nullableString(get(row, "RadiologyResult")),
		RadiologyNotes:           nullableString(get(row, "RadiologyNotes")),
		EchocardiographyCriteria: nullableString(get(row, "EchocardiographyCriteria")),
		EchocardiographyNotes:    nullableString(get(row, "EchocardiographyNotes")),
		CtScanCriteria:           nullableString(get(row, "CtScanCriteria")),
		CtScanNotes:              nullableString(get(row, "CtScanNotes")),
		UsgAbdomenCriteria:       nullableString(get(row, "UsgAbdomenCriteria")),
		UsgAbdomenNotes:          nullableString(get(row, "UsgAbdomenNotes")),
		EcgResult:                nullableString(get(row, "EcgResult")),
		TreadmillResult:          nullableString(get(row, "TreadmillResult")),
		EcgTreadmillNotes:        nullableString(get(row, "EcgTreadmillNotes")),

		HerniaHemorrhoidCriteria: nullableString(get(row, "HerniaHemorrhoidCriteria")),
		HerniaHemorrhoidNotes:    nullableString(get(row, "HerniaHemorrhoidNotes")),
		SpirometryCriteria:       nullableString(get(row, "SpirometryCriteria")),
		SpirometryNotes:          nullableString(get(row, "SpirometryNotes")),
		AudiometryCriteria:       nullableString(get(row, "AudiometryCriteria")),
		AudiometryNotes:          nullableString(get(row, "AudiometryNotes")),
		HepBCriteria:             nullableString(get(row, "HepBCriteria")),
		HepBNotes:                nullableString(get(row, "HepBNotes")),
		HepACriteria:             nullableString(get(row, "HepACriteria")),
		HepANotes:                nullableString(get(row, "HepANotes")),
		RectalSwabCriteria:       nullableString(get(row, "RectalSwabCriteria")),
		RectalSwabNotes:          nullableString(get(row, "RectalSwabNotes")),
		HivCriteria:              nullableString(get(row, "HivCriteria")),

		ResultCriteria:   nullableString(get(row, "ResultCriteria")),
		ResultValidUntil: nullableString(parseImportDate(get(row, "ResultValidUntil"))),
		ResultNotes:      nullableString(get(row, "ResultNotes")),
		Recommendation:   nullableString(get(row, "Recommendation")),
		AdditionalNotes:  nullableString(get(row, "AdditionalNotes")),
		CheckUpStatus:    nullableString(get(row, "CheckUpStatus")),

		ParentRecordID:            parentRecordID,
		HealthCertificateFileName: nullableString(get(row, "HealthCertificateFileName")),
		HcValidityStatus:          nullableString(get(row, "HcValidityStatus")),
		ReviewYear:                parseImportInt(get(row, "ReviewYear")),
		IsTerminal:                importIsTerminal(get(row, "IsTerminal")),
		SubmissionDate:            nullableString(parseImportDate(get(row, "SubmissionDate"))),
		DaysToReview:              parseImportInt(get(row, "DaysToReview")),
		CompanyRelationID:         companyRelationID,
	}

	var existingID, previousPatientID int64
	err = a.DB.QueryRow(`SELECT McuRecordID, FK_PatientID FROM McuRecord WHERE CheckUpID = ?`, checkUpID).Scan(&existingID, &previousPatientID)
	if err == nil {
		if err := a.updateImportedMcuRecord(existingID, m, cols); err != nil {
			return err
		}
		// The row may have moved to another patient, so both lose or gain
		// a "latest visit".
		if previousPatientID != m.PatientID {
			if err := db.SyncPatientFromLatestVisit(a.DB, previousPatientID); err != nil {
				return err
			}
		}
		return db.SyncPatientFromLatestVisit(a.DB, m.PatientID)
	}
	if err := a.insertImportedMcuRecord(m, cols.personalID); err != nil {
		return err
	}
	return db.SyncPatientFromLatestVisit(a.DB, m.PatientID)
}

// sheetText keeps punctuation such as QA/QC and "MKK". It only trims and
// collapses spaces. The clinical entry form still title-cases as the user
// types; a historical spreadsheet should be stored as written.
func sheetText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// splitTaskCategories splits a multi-select task cell on commas and
// semicolons, but keeps commas inside parentheses. "Other Professional
// (Pilot, Seafarer, Diver)" is one category, not three.
func splitTaskCategories(raw string) []string {
	var b strings.Builder
	depth := 0
	for _, r := range raw {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth > 0 {
				b.WriteRune('\u0001')
				continue
			}
		}
		b.WriteRune(r)
	}
	parts := strings.FieldsFunc(b.String(), func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(part, "\u0001", ",")
		part = strings.Join(strings.Fields(part), " ")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseImportTimestamp(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04:05")
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		if t, err := excelize.ExcelDateToTime(v, false); err == nil {
			return t.Format("2006-01-02T15:04:05")
		}
	}
	return s
}

func personalIDForImport(raw string, hasColumn bool) *string {
	if !hasColumn {
		return nil
	}
	return nullableString(raw)
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// importIsTerminal defaults to true (the common case: an imported row is
// current unless the sheet says otherwise) rather than false-on-blank like
// parseImportBool, since IsTerminal drives whether the "expiring soon"
// report and renewal-linking even see the row at all.
func importIsTerminal(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	return parseImportBool(s)
}

func (a *API) insertImportedMcuRecord(m McuRecord, hasPersonalID bool) error {
	personalID := m.PersonalID
	if !hasPersonalID {
		personalID = nil
	}
	_, err := a.DB.Exec(`INSERT INTO McuRecord (
		FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer,
		FK_CheckUpCriteriaID, TaskCategoryNames,
		Age, HeightCm, WeightKg, FK_CompanyID, WorkPosition, PersonalID,
		DateCheckUp, IsShortProject, ValidityPeriodMonths,
		BmiValue, BmiCriteria, BmiNotes, Smoking, OtherPhysicalExamNotes, BloodType,
		BpCriteria, BpNotes, VisionCriteria, VisionNotes,
		CbcCriteria, CbcNotes, LipidCriteria, LipidNotes,
		GlucoseCriteria, GlucoseNotes, LiverCriteria, LiverNotes,
		KidneyCriteria, KidneyNotes, UrinalysisCriteria, UrinalysisNotes,
		RadiologyResult, RadiologyNotes,
		EchocardiographyCriteria, EchocardiographyNotes, CtScanCriteria, CtScanNotes,
		UsgAbdomenCriteria, UsgAbdomenNotes,
		EcgResult, TreadmillResult, EcgTreadmillNotes,
		HerniaHemorrhoidCriteria, HerniaHemorrhoidNotes, SpirometryCriteria, SpirometryNotes,
		AudiometryCriteria, AudiometryNotes, HepBCriteria, HepBNotes, HepACriteria, HepANotes,
		RectalSwabCriteria, RectalSwabNotes, HivCriteria,
		ResultCriteria, ResultValidUntil, ResultNotes, Recommendation, AdditionalNotes, CheckUpStatus,
		FK_ParentRecordID, HealthCertificateFileName, HcValidityStatus, ReviewYear,
		IsTerminal, SubmissionDate, DaysToReview, FK_CompanyRelationID
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.PatientID, m.Timestamp, m.ReviewDate, m.CheckUpID, m.Reviewer,
		m.CheckUpCriteriaID, m.TaskCategoryNames,
		m.Age, m.HeightCm, m.WeightKg, m.CompanyID, m.WorkPosition, personalID,
		m.DateCheckUp, m.IsShortProject, m.ValidityPeriodMonths,
		m.BmiValue, m.BmiCriteria, m.BmiNotes, m.Smoking, m.OtherPhysicalExamNotes, m.BloodType,
		m.BpCriteria, m.BpNotes, m.VisionCriteria, m.VisionNotes,
		m.CbcCriteria, m.CbcNotes, m.LipidCriteria, m.LipidNotes,
		m.GlucoseCriteria, m.GlucoseNotes, m.LiverCriteria, m.LiverNotes,
		m.KidneyCriteria, m.KidneyNotes, m.UrinalysisCriteria, m.UrinalysisNotes,
		m.RadiologyResult, m.RadiologyNotes,
		m.EchocardiographyCriteria, m.EchocardiographyNotes, m.CtScanCriteria, m.CtScanNotes,
		m.UsgAbdomenCriteria, m.UsgAbdomenNotes,
		m.EcgResult, m.TreadmillResult, m.EcgTreadmillNotes,
		m.HerniaHemorrhoidCriteria, m.HerniaHemorrhoidNotes, m.SpirometryCriteria, m.SpirometryNotes,
		m.AudiometryCriteria, m.AudiometryNotes, m.HepBCriteria, m.HepBNotes, m.HepACriteria, m.HepANotes,
		m.RectalSwabCriteria, m.RectalSwabNotes, m.HivCriteria,
		m.ResultCriteria, m.ResultValidUntil, m.ResultNotes, m.Recommendation, m.AdditionalNotes, m.CheckUpStatus,
		m.ParentRecordID, m.HealthCertificateFileName, m.HcValidityStatus, m.ReviewYear,
		m.IsTerminal, m.SubmissionDate, m.DaysToReview, m.CompanyRelationID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("CheckUpID %q already exists", m.CheckUpID)
		}
		return err
	}
	return nil
}

func (a *API) updateImportedMcuRecord(id int64, m McuRecord, cols importColumnSet) error {
	personalSQL := ""
	if cols.personalID {
		personalSQL = ", PersonalID=?"
	}
	q := `UPDATE McuRecord SET
		FK_PatientID=?, ReviewDate=?, Reviewer=?, FK_CheckUpCriteriaID=?, TaskCategoryNames=?,
		Age=?, HeightCm=?, WeightKg=?, FK_CompanyID=?, WorkPosition=?` + personalSQL + `,
		DateCheckUp=?, IsShortProject=?, ValidityPeriodMonths=?,
		BmiValue=?, BmiCriteria=?, BmiNotes=?, Smoking=?, OtherPhysicalExamNotes=?,
		BpCriteria=?, BpNotes=?, VisionCriteria=?, VisionNotes=?,
		CbcCriteria=?, CbcNotes=?, LipidCriteria=?, LipidNotes=?,
		GlucoseCriteria=?, GlucoseNotes=?, LiverCriteria=?, LiverNotes=?,
		KidneyCriteria=?, KidneyNotes=?, UrinalysisCriteria=?, UrinalysisNotes=?,
		RadiologyResult=?, RadiologyNotes=?,
		EchocardiographyCriteria=?, EchocardiographyNotes=?, CtScanCriteria=?, CtScanNotes=?,
		UsgAbdomenCriteria=?, UsgAbdomenNotes=?,
		EcgResult=?, TreadmillResult=?, EcgTreadmillNotes=?,
		HerniaHemorrhoidCriteria=?, HerniaHemorrhoidNotes=?, SpirometryCriteria=?, SpirometryNotes=?,
		AudiometryCriteria=?, AudiometryNotes=?, HepBCriteria=?, HepBNotes=?, HepACriteria=?, HepANotes=?,
		RectalSwabCriteria=?, RectalSwabNotes=?, HivCriteria=?,
		ResultCriteria=?, ResultValidUntil=?, ResultNotes=?, Recommendation=?, AdditionalNotes=?, CheckUpStatus=?,
		FK_ParentRecordID=?, HcValidityStatus=?, IsTerminal=?, FK_CompanyRelationID=?`
	args := []any{
		m.PatientID, m.ReviewDate, m.Reviewer, m.CheckUpCriteriaID, m.TaskCategoryNames,
		m.Age, m.HeightCm, m.WeightKg, m.CompanyID, m.WorkPosition,
	}
	if cols.personalID {
		args = append(args, m.PersonalID)
	}
	args = append(args,
		m.DateCheckUp, m.IsShortProject, m.ValidityPeriodMonths,
		m.BmiValue, m.BmiCriteria, m.BmiNotes, m.Smoking, m.OtherPhysicalExamNotes,
		m.BpCriteria, m.BpNotes, m.VisionCriteria, m.VisionNotes,
		m.CbcCriteria, m.CbcNotes, m.LipidCriteria, m.LipidNotes,
		m.GlucoseCriteria, m.GlucoseNotes, m.LiverCriteria, m.LiverNotes,
		m.KidneyCriteria, m.KidneyNotes, m.UrinalysisCriteria, m.UrinalysisNotes,
		m.RadiologyResult, m.RadiologyNotes,
		m.EchocardiographyCriteria, m.EchocardiographyNotes, m.CtScanCriteria, m.CtScanNotes,
		m.UsgAbdomenCriteria, m.UsgAbdomenNotes,
		m.EcgResult, m.TreadmillResult, m.EcgTreadmillNotes,
		m.HerniaHemorrhoidCriteria, m.HerniaHemorrhoidNotes, m.SpirometryCriteria, m.SpirometryNotes,
		m.AudiometryCriteria, m.AudiometryNotes, m.HepBCriteria, m.HepBNotes, m.HepACriteria, m.HepANotes,
		m.RectalSwabCriteria, m.RectalSwabNotes, m.HivCriteria,
		m.ResultCriteria, m.ResultValidUntil, m.ResultNotes, m.Recommendation, m.AdditionalNotes, m.CheckUpStatus,
		m.ParentRecordID, m.HcValidityStatus, m.IsTerminal, m.CompanyRelationID,
	)
	if cols.bloodType {
		q += `, BloodType=?`
		args = append(args, m.BloodType)
	}
	if cols.timestamp {
		q += `, Timestamp=?`
		args = append(args, m.Timestamp)
	}
	if cols.certificateName {
		q += `, HealthCertificateFileName=?`
		args = append(args, m.HealthCertificateFileName)
	}
	if cols.reviewYear {
		q += `, ReviewYear=?`
		args = append(args, m.ReviewYear)
	}
	if cols.submissionDate {
		q += `, SubmissionDate=?`
		args = append(args, m.SubmissionDate)
	}
	if cols.daysToReview {
		q += `, DaysToReview=?`
		args = append(args, m.DaysToReview)
	}
	q += ` WHERE McuRecordID=?`
	args = append(args, id)
	_, err := a.DB.Exec(q, args...)
	return err
}
