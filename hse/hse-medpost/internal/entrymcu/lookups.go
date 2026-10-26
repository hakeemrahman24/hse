package entrymcu

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Checkup purpose values shown in Settings and on the checkup form.
// Older installs used spaced names; EnsureCheckUpCriteriaSeed renames those.
var checkupPurposeNames = []string{
	"PreEmployment",
	"Annual",
	"Bi-Annual",
	"Exit",
	"ConsultationResult",
}

var checkupPurposeRenames = map[string]string{
	"Pre Employment": "PreEmployment",
	"Bi-annual":      "Bi-Annual",
	"Biannual":       "Bi-Annual",
}

var reviewResultNames = []string{
	"Fit",
	"FitWithNote",
	"IncompleteResult",
	"NeedFollowUp",
	"TemporaryUnfit",
	"Unfit",
}

var workLocationNames = []string{
	"RemoteSite",
	"NonRemote",
}

// workLocationRenames maps the names used before to the current ones. Lookup
// rows and the McuRecord.WorkLocation text are both moved.
var workLocationRenames = map[string]string{
	"RemoteSiteWorker":     "RemoteSite",
	"Non RemoteSiteWorker": "NonRemote",
}

// Task categories that moved onto the Work location dropdown.
var retiredTaskCategoryNames = []string{
	"Remote Site Worker",
	"Non Remote Site Worker/Jakarta Office",
}

type namedLookup struct {
	ID     int64
	Name   string
	Status string
}

type ReviewResultCriteria struct {
	ReviewResultCriteriaID int64  `json:"reviewResultCriteriaId"`
	Name                   string `json:"name"`
	Status                 string `json:"status"`
}

type WorkLocation struct {
	WorkLocationID int64  `json:"workLocationId"`
	Name           string `json:"name"`
	Status         string `json:"status"`
}

func EnsureLookupTables(database *sql.DB) error {
	if err := createNameTable(database, "ReviewResultCriteriaTable", "ReviewResultCriteriaID"); err != nil {
		return err
	}
	return createNameTable(database, "WorkLocationTable", "WorkLocationID")
}

func createNameTable(database *sql.DB, table, idCol string) error {
	var ddl string
	if lookupIsSQLite(database) {
		ddl = `CREATE TABLE IF NOT EXISTS ` + table + ` (
			` + idCol + ` INTEGER PRIMARY KEY AUTOINCREMENT,
			Name TEXT NOT NULL UNIQUE,
			Status TEXT NOT NULL DEFAULT 'Active'
		)`
	} else {
		ddl = `CREATE TABLE IF NOT EXISTS ` + table + ` (
			` + idCol + ` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			Name VARCHAR(255) NOT NULL,
			Status VARCHAR(32) NOT NULL DEFAULT 'Active',
			UNIQUE KEY uq_` + table + `_name (Name)
		)`
	}
	_, err := database.Exec(ddl)
	return err
}

func EnsureTaskCategoryTriggerWordsColumn(database *sql.DB) error {
	ok, err := lookupHasColumn(database, "TaskCategoryTable", "TriggerWords")
	if err != nil || ok {
		return err
	}
	_, err = database.Exec(`ALTER TABLE TaskCategoryTable ADD COLUMN TriggerWords TEXT NULL`)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return nil
	}
	return err
}

func EnsureWorkLocationColumn(database *sql.DB) error {
	ok, err := lookupHasColumn(database, "McuRecord", "WorkLocation")
	if err != nil || ok {
		return err
	}
	_, err = database.Exec(`ALTER TABLE McuRecord ADD COLUMN WorkLocation TEXT NULL`)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return nil
	}
	return err
}

var resultCriteriaCheckStart = regexp.MustCompile(`(?is)ResultCriteria\s+TEXT(?:\s+NULL)?\s+CHECK\s*\(`)

// stripResultCriteriaCheck removes the CHECK (...) on ResultCriteria, including
// the nested IN (...) list, and leaves the column nullable.
func stripResultCriteriaCheck(ddl string) (string, bool) {
	loc := resultCriteriaCheckStart.FindStringIndex(ddl)
	if loc == nil {
		return ddl, false
	}
	depth := 1
	i := loc[1]
	for i < len(ddl) && depth > 0 {
		switch ddl[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		i++
	}
	if depth != 0 {
		return ddl, false
	}
	return ddl[:loc[0]] + "ResultCriteria TEXT NULL" + ddl[i:], true
}

// EnsureResultCriteriaEditable drops the fixed ResultCriteria check so the
// settings list can add IncompleteResult and so a renamed option can be saved.
func EnsureResultCriteriaEditable(database *sql.DB) error {
	if !lookupIsSQLite(database) {
		return dropMySQLResultCriteriaCheck(database)
	}
	var ddl string
	err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'McuRecord'`).Scan(&ddl)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	relaxed, ok := stripResultCriteriaCheck(ddl)
	if !ok {
		return nil
	}
	relaxed = strings.Replace(relaxed, "CREATE TABLE McuRecord", "CREATE TABLE McuRecord__rc", 1)
	relaxed = strings.Replace(relaxed, `CREATE TABLE "McuRecord"`, `CREATE TABLE "McuRecord__rc"`, 1)

	indexRows, err := database.Query(`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'McuRecord' AND sql IS NOT NULL`)
	if err != nil {
		return err
	}
	defer indexRows.Close()
	var indexes []string
	for indexRows.Next() {
		var stmt string
		if err := indexRows.Scan(&stmt); err != nil {
			return err
		}
		indexes = append(indexes, stmt)
	}
	if err := indexRows.Err(); err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(relaxed); err != nil {
		return fmt.Errorf("rebuild McuRecord without result check: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO McuRecord__rc SELECT * FROM McuRecord`); err != nil {
		return fmt.Errorf("copy McuRecord: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE McuRecord`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE McuRecord__rc RENAME TO McuRecord`); err != nil {
		return err
	}
	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate index: %w", err)
		}
	}
	return tx.Commit()
}

func dropMySQLResultCriteriaCheck(database *sql.DB) error {
	rows, err := database.Query(`
		SELECT CONSTRAINT_NAME, CHECK_CLAUSE
		FROM information_schema.CHECK_CONSTRAINTS
		WHERE CONSTRAINT_SCHEMA = DATABASE()`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, clause string
		if err := rows.Scan(&name, &clause); err != nil {
			return err
		}
		if strings.Contains(clause, "ResultCriteria") {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		if _, err := database.Exec(`ALTER TABLE McuRecord DROP CHECK ` + quoteMySQLIdent(name)); err != nil {
			if _, err2 := database.Exec(`ALTER TABLE McuRecord DROP CONSTRAINT ` + quoteMySQLIdent(name)); err2 != nil {
				return err2
			}
		}
	}
	return nil
}

func quoteMySQLIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "") + "`"
}

func EnsureReviewResultSeed(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		return ensureNames(database, "ReviewResultCriteriaTable", reviewResultNames)
	})
}

func EnsureWorkLocationSeed(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		for from, to := range workLocationRenames {
			if err := renameLookup(database, "WorkLocationTable", "WorkLocationID", from, to); err != nil {
				return err
			}
			hasColumn, err := lookupHasColumn(database, "McuRecord", "WorkLocation")
			if err != nil {
				return err
			}
			if !hasColumn {
				continue
			}
			if err := renameWorkLocationInRecords(database, from, to); err != nil {
				return err
			}
		}
		return ensureNames(database, "WorkLocationTable", workLocationNames)
	})
}

func EnsureRemoteTaskCategoriesRemoved(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		for _, name := range retiredTaskCategoryNames {
			if _, err := database.Exec(`DELETE FROM TaskCategoryTable WHERE Name = ?`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

func ensureNames(database sqlRunner, table string, names []string) error {
	for _, name := range names {
		if _, err := database.Exec(`INSERT INTO `+table+` (Name) VALUES (?)`, name); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func renameLookup(database sqlRunner, table, idCol, from, to string) error {
	if from == to {
		return nil
	}
	var fromID int64
	err := database.QueryRow(
		`SELECT `+idCol+` FROM `+table+` WHERE Name = ?`, from,
	).Scan(&fromID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var toID int64
	err = database.QueryRow(
		`SELECT `+idCol+` FROM `+table+` WHERE Name = ?`, to,
	).Scan(&toID)
	if err == sql.ErrNoRows {
		_, err = database.Exec(`UPDATE `+table+` SET Name = ? WHERE `+idCol+` = ?`, to, fromID)
		return err
	}
	if err != nil {
		return err
	}
	// MariaDB's unicode collation treats Bi-annual and Bi-Annual as the same
	// row. A second real row, such as an archived FitForTask beside Fit for
	// Task, stays in the list so Settings can show it and restore it.
	if fromID == toID {
		_, err = database.Exec(`UPDATE `+table+` SET Name = ? WHERE `+idCol+` = ?`, to, fromID)
		return err
	}
	return nil
}

func (a *API) ListReviewResultCriteria(w http.ResponseWriter, r *http.Request) {
	items, err := listNamed(a.DB, "ReviewResultCriteriaTable", "ReviewResultCriteriaID")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	out := make([]ReviewResultCriteria, 0, len(items))
	for _, item := range items {
		out = append(out, ReviewResultCriteria{ReviewResultCriteriaID: item.ID, Name: item.Name, Status: item.Status})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateReviewResultCriteria(w http.ResponseWriter, r *http.Request) {
	createNamed(w, r, a.DB, "ReviewResultCriteriaTable", "reviewResultCriteriaId", "review result already exists")
}

func (a *API) UpdateReviewResultCriteria(w http.ResponseWriter, r *http.Request) {
	updateNamed(w, r, a.DB, "ReviewResultCriteriaTable", "ReviewResultCriteriaID", "review result already exists", func(tx *sql.Tx, oldName, newName string) error {
		_, err := tx.Exec(`UPDATE McuRecord SET ResultCriteria = ? WHERE ResultCriteria = ?`, newName, oldName)
		return err
	})
}

func (a *API) DeleteReviewResultCriteria(w http.ResponseWriter, r *http.Request) {
	deleteNamedIfUnused(w, r, a.DB, "ReviewResultCriteriaTable", "ReviewResultCriteriaID",
		func(id int64) (int, error) {
			var n int
			err := a.DB.QueryRow(`SELECT COUNT(*) FROM McuRecord WHERE ResultCriteria = (SELECT Name FROM ReviewResultCriteriaTable WHERE ReviewResultCriteriaID = ?)`, id).Scan(&n)
			return n, err
		},
		"this result is used by checkup records; edit the name instead")
}

func (a *API) ListWorkLocations(w http.ResponseWriter, r *http.Request) {
	items, err := listNamed(a.DB, "WorkLocationTable", "WorkLocationID")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	out := make([]WorkLocation, 0, len(items))
	for _, item := range items {
		out = append(out, WorkLocation{WorkLocationID: item.ID, Name: item.Name, Status: item.Status})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateWorkLocation(w http.ResponseWriter, r *http.Request) {
	createNamed(w, r, a.DB, "WorkLocationTable", "workLocationId", "work location already exists")
}

func (a *API) UpdateWorkLocation(w http.ResponseWriter, r *http.Request) {
	updateNamed(w, r, a.DB, "WorkLocationTable", "WorkLocationID", "work location already exists", func(tx *sql.Tx, oldName, newName string) error {
		return renameWorkLocationInRecords(tx, oldName, newName)
	})
}

func (a *API) DeleteWorkLocation(w http.ResponseWriter, r *http.Request) {
	deleteNamedIfUnused(w, r, a.DB, "WorkLocationTable", "WorkLocationID",
		func(id int64) (int, error) {
			var name string
			if err := a.DB.QueryRow(`SELECT Name FROM WorkLocationTable WHERE WorkLocationID = ?`, id).Scan(&name); err != nil {
				return 0, err
			}
			return workLocationInUse(a.DB, name)
		},
		"this work location is used by checkup records; edit the name instead")
}

func (a *API) UpdateCheckUpCriteria(w http.ResponseWriter, r *http.Request) {
	updateNamed(w, r, a.DB, "CheckUpCriteriaTable", "CheckUpCriteriaID", "checkup criteria already exists", nil)
}

func (a *API) SetCheckUpCriteriaStatus(w http.ResponseWriter, r *http.Request) {
	setNamedStatus(w, r, a.DB, "CheckUpCriteriaTable", "CheckUpCriteriaID")
}

func (a *API) SetReviewResultCriteriaStatus(w http.ResponseWriter, r *http.Request) {
	setNamedStatus(w, r, a.DB, "ReviewResultCriteriaTable", "ReviewResultCriteriaID")
}

func (a *API) SetWorkLocationStatus(w http.ResponseWriter, r *http.Request) {
	setNamedStatus(w, r, a.DB, "WorkLocationTable", "WorkLocationID")
}

func (a *API) SetTaskCategoryStatus(w http.ResponseWriter, r *http.Request) {
	setNamedStatus(w, r, a.DB, "TaskCategoryTable", "TaskCategoryID")
}

func (a *API) UpdateTaskCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Name         string `json:"name"`
		TriggerWords string `json:"triggerWords"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	name := strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	triggers := normalizeTriggerWords(body.TriggerWords)
	var oldName string
	err = a.DB.QueryRow(`SELECT Name FROM TaskCategoryTable WHERE TaskCategoryID = ?`, id).Scan(&oldName)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE TaskCategoryTable SET Name = ?, TriggerWords = ? WHERE TaskCategoryID = ?`, name, triggers, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task category already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 && oldName == name {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if oldName != name {
		if err := replaceTaskCategoryName(tx, oldName, name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func normalizeTriggerWords(raw string) string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(part)), " "))
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return strings.Join(out, ", ")
}

func rewriteTaskCategoryName(database sqlRunner, oldName, newName string) error {
	ok, err := lookupHasColumn(database, "McuRecord", "TaskCategoryNames")
	if err != nil || !ok {
		return err
	}
	return replaceTaskCategoryName(database, oldName, newName)
}

func replaceTaskCategoryName(database sqlRunner, oldName, newName string) error {
	rows, err := database.Query(`SELECT McuRecordID, TaskCategoryNames FROM McuRecord WHERE TaskCategoryNames LIKE ?`, "%"+oldName+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	type change struct {
		id    int64
		names string
	}
	var changes []change
	for rows.Next() {
		var id int64
		var names sql.NullString
		if err := rows.Scan(&id, &names); err != nil {
			return err
		}
		if !names.Valid {
			continue
		}
		parts := strings.Split(names.String, ";")
		changed := false
		for i, part := range parts {
			if strings.TrimSpace(part) == oldName {
				parts[i] = newName
				changed = true
			}
		}
		if !changed {
			continue
		}
		cleaned := make([]string, 0, len(parts))
		for _, part := range parts {
			cleaned = append(cleaned, strings.TrimSpace(part))
		}
		changes = append(changes, change{id: id, names: strings.Join(cleaned, "; ")})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := database.Exec(`UPDATE McuRecord SET TaskCategoryNames = ? WHERE McuRecordID = ?`, c.names, c.id); err != nil {
			return err
		}
	}
	return nil
}

func listNamed(database *sql.DB, table, idCol string) ([]namedLookup, error) {
	rows, err := database.Query(`SELECT ` + idCol + `, Name, Status FROM ` + table + ` ORDER BY Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []namedLookup{}
	for rows.Next() {
		var item namedLookup
		if err := rows.Scan(&item.ID, &item.Name, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func createNamed(w http.ResponseWriter, r *http.Request, database *sql.DB, table, idJSON, conflict string) {
	name, ok := decodeLookupName(w, r)
	if !ok {
		return
	}
	res, err := database.Exec(`INSERT INTO `+table+` (Name) VALUES (?)`, name)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": conflict})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, idJSON: id})
}

// updateNamed renames a lookup row. The rename and the after callback (which
// moves the name inside McuRecord) run in one transaction, so a failed record
// update leaves the lookup name unchanged too.
func updateNamed(w http.ResponseWriter, r *http.Request, database *sql.DB, table, idCol, conflict string, after func(tx *sql.Tx, oldName, newName string) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	name, ok := decodeLookupName(w, r)
	if !ok {
		return
	}
	tx, err := database.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	var oldName string
	err = tx.QueryRow(`SELECT Name FROM `+table+` WHERE `+idCol+` = ?`, id).Scan(&oldName)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if oldName == name {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	res, err := tx.Exec(`UPDATE `+table+` SET Name = ? WHERE `+idCol+` = ?`, name, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": conflict})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if after != nil {
		if err := after(tx, oldName, name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func deleteNamedIfUnused(w http.ResponseWriter, r *http.Request, database *sql.DB, table, idCol string, inUseCount func(id int64) (int, error), inUse string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	n, err := inUseCount(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if n > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": inUse})
		return
	}
	res, err := database.Exec(`DELETE FROM `+table+` WHERE `+idCol+` = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func setNamedStatus(w http.ResponseWriter, r *http.Request, database *sql.DB, table, idCol string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	status := strings.TrimSpace(body.Status)
	if status != "Active" && status != "Archived" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be Active or Archived"})
		return
	}
	res, err := database.Exec(`UPDATE `+table+` SET Status = ? WHERE `+idCol+` = ?`, status, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decodeLookupName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return "", false
	}
	body.Name = strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return "", false
	}
	return body.Name, true
}

func lookupIsSQLite(database sqlRunner) bool {
	var v string
	err := database.QueryRow(`SELECT sqlite_version()`).Scan(&v)
	return err == nil
}

func lookupHasColumn(database sqlRunner, table, column string) (bool, error) {
	if lookupIsSQLite(database) {
		rows, err := database.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				return false, err
			}
			if strings.EqualFold(name, column) {
				return true, nil
			}
		}
		return false, rows.Err()
	}
	var n int
	err := database.QueryRow(`
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		table, column).Scan(&n)
	return n > 0, err
}
