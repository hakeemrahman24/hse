package entrymcu

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// SyncReport is the result of aligning the database with the reference workbook.
type SyncReport struct {
	SheetRows   int
	Created     int
	CreatedIDs  []string
	Patched     int
	PatchedIDs  []string
	SkippedDate []string
	Errors      []ImportRowError
}

type referenceFacts struct {
	Result         string
	Status         string
	Notes          string
	Recommendation string
	Until          string
	Standing       string
	Terminal       string
	UntilInvalid   bool
}

// SyncReferenceWorkbook inserts checkup IDs that are in the legacy workbook
// and missing from the database, then applies result, status, note,
// recommendation, valid-until, and standing corrections on rows already
// stored. It does not rewrite certificate file names or validity-ladder text
// the application recalculates on read.
func SyncReferenceWorkbook(db *sql.DB, xlsxPath string) (SyncReport, error) {
	sheetRows, err := readLegacySheet(xlsxPath)
	if err != nil {
		return SyncReport{}, err
	}
	prepared, prepReport, err := prepareLegacySheet(sheetRows)
	if err != nil {
		return SyncReport{}, err
	}
	api := &API{DB: db}
	existing, err := loadStoredCheckUpIDs(db)
	if err != nil {
		return SyncReport{}, err
	}
	report := SyncReport{SheetRows: len(sheetRows) - 1}
	missing, err := missingPreparedRows(db, prepared, existing, prepReport.NoneID)
	if err != nil {
		return report, err
	}
	if missing != nil {
		imported, err := api.ImportWorkbook(missing)
		if err != nil {
			return report, err
		}
		report.Created = imported.Created
		report.Errors = append(report.Errors, imported.Errors...)
		ids := checkupIDs(missing)
		report.CreatedIDs = ids
		if err := clearImportedCertificateLabels(db, ids); err != nil {
			return report, err
		}
	}
	patches, skipped, err := referencePatches(db, sheetRows, existing)
	if err != nil {
		return report, err
	}
	report.SkippedDate = skipped
	if err := applyReferencePatches(db, patches); err != nil {
		return report, err
	}
	report.Patched = len(patches)
	for _, patch := range patches {
		report.PatchedIDs = append(report.PatchedIDs, patch.id)
	}
	return report, nil
}

func readLegacySheet(path string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("reference workbook has no data rows")
	}
	return rows, nil
}

func prepareLegacySheet(rows [][]string) (*excelize.File, OldSheetReport, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return nil, OldSheetReport{}, err
	}
	return PrepareOldMCU(strings.NewReader(buf.String()))
}

func loadStoredCheckUpIDs(db *sql.DB) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	rs, err := db.Query(`SELECT CheckUpID FROM McuRecord`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rs.Err()
}

func missingPreparedRows(db *sql.DB, prepared *excelize.File, existing map[string]struct{}, noneID string) (*excelize.File, error) {
	rows, err := prepared.GetRows(mcuSheetName)
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("prepared workbook has no checkup rows")
	}
	get := cellGetter(headerIndex(rows[0]))
	var keep [][]string
	for _, row := range rows[1:] {
		id := get(row, "CheckUpID")
		if id == "" {
			continue
		}
		if _, ok := existing[id]; ok {
			continue
		}
		if id == noneID {
			taken, err := checkupAlreadyStored(db, get(row, "PatientName"), get(row, "DateOfBirth"), get(row, "ReviewDate"))
			if err != nil {
				return nil, err
			}
			if taken {
				continue
			}
		}
		keep = append(keep, row)
	}
	if len(keep) == 0 {
		return nil, nil
	}
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", mcuSheetName)
	if err := writeImportSheet(f, mcuSheetName, rows[0], keep); err != nil {
		return nil, err
	}
	return f, nil
}

func checkupAlreadyStored(db *sql.DB, name, dob, review string) (bool, error) {
	name = strings.Join(strings.Fields(name), " ")
	dob = parseImportDate(dob)
	review = parseImportDate(review)
	var id string
	err := db.QueryRow(`
		SELECT m.CheckUpID
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		WHERE LOWER(p.Name) = LOWER(?) AND p.DateOfBirth = ? AND m.ReviewDate = ?
		LIMIT 1`, name, dob, review).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func checkupIDs(f *excelize.File) []string {
	rows, err := f.GetRows(mcuSheetName)
	if err != nil || len(rows) < 2 {
		return nil
	}
	get := cellGetter(headerIndex(rows[0]))
	ids := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if id := get(row, "CheckUpID"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func clearImportedCertificateLabels(db *sql.DB, ids []string) error {
	for _, id := range ids {
		if _, err := db.Exec(`
			UPDATE McuRecord
			SET HealthCertificateFileName = NULL
			WHERE CheckUpID = ?
			  AND HealthCertificateFileName IS NOT NULL
			  AND HealthCertificateFileName NOT LIKE '%/%'`, id); err != nil {
			return err
		}
		if _, err := db.Exec(`
			UPDATE McuRecord
			SET HcValidityStatus = NULL
			WHERE CheckUpID = ?
			  AND HcValidityStatus IS NOT NULL
			  AND HcValidityStatus NOT LIKE 'Renewed by %'
			  AND HcValidityStatus <> 'Archived / Exit'`, id); err != nil {
			return err
		}
	}
	return nil
}

type referencePatch struct {
	id             string
	result         *string
	status         *string
	notes          *string
	recommendation *string
	until          *string
	clearUntil     bool
	standing       *string
	clearStanding  bool
	terminal       *bool
}

func referencePatches(db *sql.DB, sheet [][]string, existing map[string]struct{}) ([]referencePatch, []string, error) {
	stored, err := loadStoredFacts(db)
	if err != nil {
		return nil, nil, err
	}
	get := cellGetter(headerIndex(sheet[0]))
	var patches []referencePatch
	var skipped []string
	for _, row := range sheet[1:] {
		id := get(row, "CheckUpID")
		if id == "" || id == "None" {
			continue
		}
		if _, ok := existing[id]; !ok {
			continue
		}
		have, ok := stored[id]
		if !ok {
			continue
		}
		want := sheetFacts(get, row)
		if want.UntilInvalid {
			skipped = append(skipped, id)
		}
		patch, changed := decideReferencePatch(id, want, have)
		if changed {
			patches = append(patches, patch)
		}
	}
	return patches, skipped, nil
}

func sheetFacts(get func([]string, string) string, row []string) referenceFacts {
	untilRaw := cleanLegacyCell(get(row, "ResultValidUntil"))
	until := parseImportDate(untilRaw)
	invalid := untilRaw != "" && !saneImportDate(until)
	return referenceFacts{
		Result:         cleanLegacyCell(get(row, "ResultCriteria")),
		Status:         cleanLegacyCell(get(row, "CheckUpStatus")),
		Notes:          strings.Join(strings.Fields(cleanLegacyCell(get(row, "ResultNotes"))), " "),
		Recommendation: strings.Join(strings.Fields(cleanLegacyCell(get(row, "RecommendationForFollowUp"))), " "),
		Until:          until,
		Standing:       normalizeStanding(cleanLegacyCell(get(row, "HCValidityStatus"))),
		Terminal:       strings.ToUpper(cleanLegacyCell(get(row, "Terminal"))),
		UntilInvalid:   invalid,
	}
}

func saneImportDate(s string) bool {
	if len(s) != len("2006-01-02") || s[4] != '-' || s[7] != '-' {
		return false
	}
	year := 0
	for _, c := range s[:4] {
		if c < '0' || c > '9' {
			return false
		}
		year = year*10 + int(c-'0')
	}
	return year >= 1900 && year <= 2100
}

func normalizeStanding(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const prefix = "renewed by "
	if len(s) >= len(prefix) && strings.HasPrefix(strings.ToLower(s), prefix) {
		return "Renewed by " + strings.TrimSpace(s[len(prefix):])
	}
	return s
}

func ladderStanding(s string) bool {
	switch strings.ToLower(s) {
	case "", "valid", "1 week urgent", "2 weeks urgent", "1 month warning", "2 months warning", "reevaluate / overdue", "temporary unfit":
		return true
	default:
		return false
	}
}

func decideReferencePatch(id string, want, have referenceFacts) (referencePatch, bool) {
	patch := referencePatch{id: id}
	changed := false
	sheetResult := want.Result
	if sheetResult == "FitWithRestriction" {
		sheetResult = "FitWithNote"
	}
	if sheetResult != "" && sheetResult != "Waiver" && sheetResult != have.Result && allowedResult(sheetResult) {
		patch.result = &sheetResult
		changed = true
	}
	if (want.Status == "Open" || want.Status == "Close") && !strings.EqualFold(want.Status, have.Status) {
		patch.status = &want.Status
		changed = true
	}
	if want.Notes != "" && want.Notes != strings.Join(strings.Fields(have.Notes), " ") {
		patch.notes = &want.Notes
		changed = true
	}
	if want.Recommendation != "" && want.Recommendation != strings.Join(strings.Fields(have.Recommendation), " ") {
		patch.recommendation = &want.Recommendation
		changed = true
	}
	if !want.UntilInvalid && want.Until != "" && want.Until != have.Until {
		patch.until = &want.Until
		changed = true
	}
	renewed := strings.HasPrefix(want.Standing, "Renewed by ")
	switch {
	case renewed && want.Standing != have.Standing:
		patch.standing = &want.Standing
		close := "Close"
		patch.status = &close
		terminal := false
		patch.terminal = &terminal
		changed = true
	case want.Standing == "Archived / Exit" && have.Standing != "Archived / Exit":
		patch.standing = &want.Standing
		changed = true
	case ladderStanding(want.Standing) && have.Standing == "Archived / Exit" && patch.result != nil && *patch.result != "Unfit":
		patch.clearStanding = true
		changed = true
	}
	if patch.terminal == nil && (patch.result != nil || patch.status != nil) && !renewed {
		terminal := want.Terminal != "N"
		if patch.status != nil && *patch.status == "Open" {
			terminal = false
		}
		patch.terminal = &terminal
	}
	return patch, changed
}

func allowedResult(s string) bool {
	switch s {
	case "Fit", "FitWithNote", "Unfit", "TemporaryUnfit", "NeedFollowUp":
		return true
	default:
		return false
	}
}

func loadStoredFacts(db *sql.DB) (map[string]referenceFacts, error) {
	rs, err := db.Query(`
		SELECT CheckUpID,
			IFNULL(ResultCriteria, ''),
			IFNULL(CheckUpStatus, ''),
			IFNULL(ResultNotes, ''),
			IFNULL(Recommendation, ''),
			IFNULL(DATE_FORMAT(ResultValidUntil, '%Y-%m-%d'), ''),
			IFNULL(HcValidityStatus, ''),
			IF(IsTerminal = 1, 'T', 'N')
		FROM McuRecord`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]referenceFacts{}
	for rs.Next() {
		var id string
		var facts referenceFacts
		if err := rs.Scan(&id, &facts.Result, &facts.Status, &facts.Notes, &facts.Recommendation, &facts.Until, &facts.Standing, &facts.Terminal); err != nil {
			return nil, err
		}
		out[id] = facts
	}
	return out, rs.Err()
}

func applyReferencePatches(db *sql.DB, patches []referencePatch) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, patch := range patches {
		sets := make([]string, 0, 8)
		args := make([]any, 0, 8)
		add := func(column string, value any) {
			sets = append(sets, column+"=?")
			args = append(args, value)
		}
		if patch.result != nil {
			add("ResultCriteria", *patch.result)
		}
		if patch.status != nil {
			add("CheckUpStatus", *patch.status)
		}
		if patch.notes != nil {
			add("ResultNotes", *patch.notes)
		}
		if patch.recommendation != nil {
			add("Recommendation", *patch.recommendation)
		}
		if patch.until != nil {
			add("ResultValidUntil", *patch.until)
		}
		if patch.clearUntil {
			sets = append(sets, "ResultValidUntil=NULL")
		}
		if patch.standing != nil {
			add("HcValidityStatus", *patch.standing)
		}
		if patch.clearStanding {
			sets = append(sets, "HcValidityStatus=NULL")
		}
		if patch.terminal != nil {
			v := 0
			if *patch.terminal {
				v = 1
			}
			add("IsTerminal", v)
		}
		if len(sets) == 0 {
			continue
		}
		args = append(args, patch.id)
		if _, err := tx.Exec(`UPDATE McuRecord SET `+strings.Join(sets, ", ")+` WHERE CheckUpID = ?`, args...); err != nil {
			return fmt.Errorf("update %s: %w", patch.id, err)
		}
	}
	return tx.Commit()
}

// CorrectShiftedYears rewrites date text whose year is 20026 or 20027 to
// 2026 or 2027. Those years are typed with an extra zero. It updates stored
// checkup and patient dates, then applies the same correction from the
// reference workbook where that cell is the bad year.
func CorrectShiftedYears(db *sql.DB, xlsxPath string) (int, []string, error) {
	n, ids, err := correctStoredYears(db)
	if err != nil {
		return n, ids, err
	}
	if xlsxPath == "" {
		return n, ids, nil
	}
	extra, extraIDs, err := correctSheetYears(db, xlsxPath)
	if err != nil {
		return n, ids, err
	}
	return n + extra, append(ids, extraIDs...), nil
}

func correctShiftedYear(s string) (string, bool) {
	if !strings.Contains(s, "20026") && !strings.Contains(s, "20027") {
		return s, false
	}
	replaced := strings.ReplaceAll(strings.ReplaceAll(s, "20026", "2026"), "20027", "2027")
	parsed := parseImportDate(replaced)
	if saneImportDate(parsed) {
		if i := strings.Index(replaced, "T"); i >= 0 {
			return parsed + replaced[i:], true
		}
		return parsed, true
	}
	if replaced != s {
		return replaced, true
	}
	return s, false
}

func correctStoredYears(db *sql.DB) (int, []string, error) {
	type spec struct {
		table  string
		idCol  string
		label  string
		column string
	}
	specs := []spec{
		{"McuRecord", "McuRecordID", "CheckUpID", "ReviewDate"},
		{"McuRecord", "McuRecordID", "CheckUpID", "DateCheckUp"},
		{"McuRecord", "McuRecordID", "CheckUpID", "ResultValidUntil"},
		{"McuRecord", "McuRecordID", "CheckUpID", "SubmissionDate"},
		{"McuRecord", "McuRecordID", "CheckUpID", "Timestamp"},
		{"Patient", "PatientID", "PatientID", "DateOfBirth"},
	}
	var n int
	var ids []string
	for _, spec := range specs {
		q := fmt.Sprintf(`SELECT %s, CAST(%s AS CHAR), COALESCE(%s,'') FROM %s WHERE %s LIKE '%%20026%%' OR %s LIKE '%%20027%%'`,
			spec.idCol, spec.label, spec.column, spec.table, spec.column, spec.column)
		rows, err := db.Query(q)
		if err != nil {
			return n, ids, err
		}
		for rows.Next() {
			var id int64
			var label, raw string
			if err := rows.Scan(&id, &label, &raw); err != nil {
				rows.Close()
				return n, ids, err
			}
			fixed, ok := correctShiftedYear(raw)
			if !ok || fixed == raw {
				continue
			}
			upd := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, spec.table, spec.column, spec.idCol)
			if _, err := db.Exec(upd, fixed, id); err != nil {
				rows.Close()
				return n, ids, err
			}
			n++
			ids = append(ids, label+" "+spec.column)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return n, ids, err
		}
		rows.Close()
	}
	rows, err := db.Query(`SELECT CheckUpID, ReviewYear FROM McuRecord WHERE ReviewYear IN (20026, 20027)`)
	if err != nil {
		return n, ids, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var year int
		if err := rows.Scan(&id, &year); err != nil {
			return n, ids, err
		}
		fixed := year - 18000
		if _, err := db.Exec(`UPDATE McuRecord SET ReviewYear = ? WHERE CheckUpID = ?`, fixed, id); err != nil {
			return n, ids, err
		}
		n++
		ids = append(ids, id+" ReviewYear")
	}
	return n, ids, rows.Err()
}

func correctSheetYears(db *sql.DB, path string) (int, []string, error) {
	rows, err := readLegacySheet(path)
	if err != nil {
		return 0, nil, err
	}
	get := cellGetter(headerIndex(rows[0]))
	columns := []struct {
		sheet string
		db    string
	}{
		{"ReviewDate", "ReviewDate"},
		{"DateCheckUp", "DateCheckUp"},
		{"ResultValidUntil", "ResultValidUntil"},
		{"SubmissionDate", "SubmissionDate"},
		{"Timestamp", "Timestamp"},
	}
	var n int
	var ids []string
	for _, row := range rows[1:] {
		id := get(row, "CheckUpID")
		if id == "" || id == "None" {
			continue
		}
		for _, col := range columns {
			fixed, ok := correctShiftedYear(get(row, col.sheet))
			if !ok {
				continue
			}
			res, err := db.Exec(`UPDATE McuRecord SET `+col.db+` = ? WHERE CheckUpID = ? AND COALESCE(`+col.db+`, '') <> ?`, fixed, id, fixed)
			if err != nil {
				return n, ids, err
			}
			changed, _ := res.RowsAffected()
			if changed > 0 {
				n++
				ids = append(ids, id+" "+col.db)
			}
		}
		if fixed, ok := correctShiftedYear(get(row, "DateOfBirth")); ok {
			res, err := db.Exec(`
				UPDATE Patient SET DateOfBirth = ?
				WHERE PatientID = (SELECT FK_PatientID FROM McuRecord WHERE CheckUpID = ?)
				  AND (DateOfBirth IS NULL OR DateOfBirth = '' OR DateOfBirth LIKE '%20026%' OR DateOfBirth LIKE '%20027%')`,
				fixed, id)
			if err != nil {
				return n, ids, err
			}
			changed, _ := res.RowsAffected()
			if changed > 0 {
				n++
				ids = append(ids, id+" DateOfBirth")
			}
		}
	}
	return n, ids, nil
}

// LoadProjectEnv reads KEY=VALUE lines into the process environment when the
// key is not already set. Blank lines and comments are ignored.
func LoadProjectEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}
