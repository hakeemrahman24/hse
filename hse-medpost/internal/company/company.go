// Package company owns the whole-app reference data shared by every module
// that ties a person or record to an employer: EntityType (client/
// contractor/... classification), Company itself, and CompanyRelation (a
// company's contract/project link to a client, with an optional parent for
// multi-tier subcontracting). Patient, McuRecord, and future emr/screening
// records all resolve FK_CompanyID against this package's Company table —
// see plan §2 "Common".
package company

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type API struct {
	DB *sql.DB
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var (
	companyRepeatedSpace = regexp.MustCompile(`\s{2,}`)
	companyASCIIWord     = regexp.MustCompile(`[A-Za-z]+`)
)

// maxAcronymLetters is the longest all-capital word kept as typed. Longer
// all-capital words are almost always a name typed in capitals, not an
// acronym, so they are proper-cased.
const maxAcronymLetters = 6

// NormalizeCompanyName collapses extra spaces and sets the case of each word:
//   - all lowercase ("china") becomes "China"
//   - all capitals up to maxAcronymLetters ("KSO", "PT") is kept
//   - all capitals longer than that ("STARWAY") becomes "Starway"
//   - mixed case ("McDonald", "Sikars") is kept as typed
//
// Punctuation, digits, and spacing stay as they are.
func NormalizeCompanyName(s string) string {
	collapsed := strings.TrimSpace(companyRepeatedSpace.ReplaceAllString(s, " "))
	return companyASCIIWord.ReplaceAllStringFunc(collapsed, companyWordCase)
}

func companyWordCase(word string) string {
	switch {
	case word == strings.ToLower(word):
		return titleCaseWord(word)
	case word == strings.ToUpper(word) && len(word) > maxAcronymLetters:
		return titleCaseWord(word)
	default:
		return word
	}
}

func titleCaseWord(word string) string {
	return strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
}

// isUniqueViolation reports whether err is a unique/duplicate-key constraint
// failure, across both supported drivers: SQLite says "UNIQUE constraint
// failed", MariaDB/MySQL says "Duplicate entry '...' for key '...'".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "Duplicate entry")
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- EntityType ---------------------------------------------------------

type EntityType struct {
	EntityTypeID int64  `json:"entityTypeId"`
	Name         string `json:"name"`
}

func (a *API) ListEntityTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT EntityTypeID, Name FROM EntityType ORDER BY Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []EntityType{}
	for rows.Next() {
		var e EntityType
		if err := rows.Scan(&e.EntityTypeID, &e.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Company -------------------------------------------------------------

type Company struct {
	CompanyID      int64  `json:"companyId"`
	Name           string `json:"name"`
	Abbreviation   string `json:"abbreviation"`
	EntityTypeID   *int64 `json:"entityTypeId"`
	EntityTypeName string `json:"entityTypeName"`
	Archived       bool   `json:"archived"`
}

// ListCompanies hides archived companies unless ?includeArchived=1, so the
// pickers in other forms only offer active companies. The settings page asks
// for everything so archived companies can still be edited or restored.
func (a *API) ListCompanies(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT c.CompanyID, c.Name, COALESCE(c.Abbreviation,''), c.FK_EntityTypeID, COALESCE(e.Name,''), COALESCE(c.Archived,0)
		FROM Company c
		LEFT JOIN EntityType e ON e.EntityTypeID = c.FK_EntityTypeID`
	if r.URL.Query().Get("includeArchived") != "1" {
		query += ` WHERE COALESCE(c.Archived,0) = 0`
	}
	rows, err := a.DB.Query(query + ` ORDER BY c.Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []Company{}
	for rows.Next() {
		var c Company
		var archived int
		if err := rows.Scan(&c.CompanyID, &c.Name, &c.Abbreviation, &c.EntityTypeID, &c.EntityTypeName, &archived); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		c.Archived = archived == 1
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateCompany(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Abbreviation string `json:"abbreviation"`
		EntityTypeID *int64 `json:"entityTypeId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Name = NormalizeCompanyName(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company name required"})
		return
	}

	res, err := a.DB.Exec(
		`INSERT INTO Company (Name, Abbreviation, FK_EntityTypeID) VALUES (?, ?, ?)`,
		body.Name, nullIfEmpty(strings.TrimSpace(body.Abbreviation)), body.EntityTypeID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "company already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "companyId": id})
}

func (a *API) UpdateCompany(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// EntityTypeID has no separate "clear the FK" path here — same
	// simplicity level as the rest of this codebase's optional-FK update
	// handlers (e.g. staff's PositionRoleTable mapping): a *int64 that
	// decodes to nil either means "omitted" or "sent as null", and both are
	// treated as "leave it alone". If a real need to detach a company's
	// EntityType shows up, add a companion `clearEntityType bool` field
	// then, rather than guessing at the shape now.
	var body struct {
		Name         *string `json:"name"`
		Abbreviation *string `json:"abbreviation"`
		EntityTypeID *int64  `json:"entityTypeId"`
		Archived     *bool   `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM Company WHERE CompanyID = ?`, id).Scan(&exists); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// All field changes share one transaction, so a failed later field does
	// not leave the earlier ones saved.
	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	if body.Name != nil {
		name := NormalizeCompanyName(*body.Name)
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "company name required"})
			return
		}
		if _, err := tx.Exec(`UPDATE Company SET Name = ? WHERE CompanyID = ?`, name, id); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "company already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Abbreviation != nil {
		if _, err := tx.Exec(`UPDATE Company SET Abbreviation = ? WHERE CompanyID = ?`, nullIfEmpty(strings.TrimSpace(*body.Abbreviation)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.EntityTypeID != nil {
		if _, err := tx.Exec(`UPDATE Company SET FK_EntityTypeID = ? WHERE CompanyID = ?`, *body.EntityTypeID, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.Archived != nil {
		archived := 0
		if *body.Archived {
			archived = 1
		}
		if _, err := tx.Exec(`UPDATE Company SET Archived = ? WHERE CompanyID = ?`, archived, id); err != nil {
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

func (a *API) DeleteCompany(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM Company WHERE CompanyID = ?`, id)
	if err != nil {
		// Company is referenced by CompanyRelation (and, once built,
		// Patient/McuRecord) — a company still in use surfaces as a plain
		// db error here rather than a friendlier "still referenced by N
		// records" message. Good enough until that's actually hit (YAGNI).
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

// --- CompanyRelation -------------------------------------------------------

type CompanyRelation struct {
	CompanyRelationID int64  `json:"companyRelationId"`
	RelationCode      string `json:"relationCode"`
	CompanyID         int64  `json:"companyId"`
	CompanyName       string `json:"companyName"`
	ParentID          *int64 `json:"parentId"`
	ParentName        string `json:"parentName"`
	Project           string `json:"project"`
	Depth             *int64 `json:"depth"`
}

func (a *API) ListCompanyRelations(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT cr.CompanyRelationID, cr.RelationCode, cr.FK_CompanyID, c.Name,
		       cr.FK_ParentID, COALESCE(p.Name,''), COALESCE(cr.Project,''), cr.Depth
		FROM CompanyRelation cr
		JOIN Company c ON c.CompanyID = cr.FK_CompanyID
		LEFT JOIN Company p ON p.CompanyID = cr.FK_ParentID
		ORDER BY c.Name, cr.RelationCode`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []CompanyRelation{}
	for rows.Next() {
		var cr CompanyRelation
		if err := rows.Scan(&cr.CompanyRelationID, &cr.RelationCode, &cr.CompanyID, &cr.CompanyName,
			&cr.ParentID, &cr.ParentName, &cr.Project, &cr.Depth); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, cr)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateCompanyRelation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RelationCode string `json:"relationCode"`
		CompanyID    int64  `json:"companyId"`
		ParentID     *int64 `json:"parentId"`
		Project      string `json:"project"`
		Depth        *int64 `json:"depth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.RelationCode = strings.TrimSpace(body.RelationCode)
	body.Project = strings.TrimSpace(body.Project)
	body.ParentID = normalizeParentID(body.ParentID)
	if body.RelationCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "relation code required"})
		return
	}
	if err := rejectRelation(a.DB, body.CompanyID, body.ParentID, body.Project, 0); err != nil {
		writeRelationError(w, err)
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID, Project, Depth)
		 VALUES (?, ?, ?, ?, NULL)`,
		body.RelationCode, body.CompanyID, body.ParentID, nullIfEmpty(body.Project),
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "relation code already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	depth, err := finishRelationDepth(tx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "companyRelationId": id, "depth": depth})
}

func (a *API) UpdateCompanyRelation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		RelationCode *string `json:"relationCode"`
		CompanyID    *int64  `json:"companyId"`
		ParentID     *int64  `json:"parentId"`
		Project      *string `json:"project"`
		Depth        *int64  `json:"depth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var code string
	var companyID int64
	var project string
	err = a.DB.QueryRow(`
		SELECT RelationCode, FK_CompanyID, COALESCE(Project, '')
		FROM CompanyRelation WHERE CompanyRelationID = ?`, id).Scan(&code, &companyID, &project)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if body.RelationCode != nil {
		code = strings.TrimSpace(*body.RelationCode)
		if code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "relation code required"})
			return
		}
	}
	if body.CompanyID != nil {
		companyID = *body.CompanyID
	}
	// The settings form always resends parent. A missing parent clears it,
	// which is how a relation moves back to the top of the chain.
	parentID := normalizeParentID(body.ParentID)
	if body.Project != nil {
		project = strings.TrimSpace(*body.Project)
	}
	if err := rejectRelation(a.DB, companyID, parentID, project, id); err != nil {
		writeRelationError(w, err)
		return
	}
	// The row update and the depth recompute share one transaction, so the
	// depths never disagree with the saved parent chain.
	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		UPDATE CompanyRelation
		SET RelationCode = ?, FK_CompanyID = ?, FK_ParentID = ?, Project = ?
		WHERE CompanyRelationID = ?`,
		code, companyID, parentID, nullIfEmpty(project), id); err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "relation code already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	depth, err := finishRelationDepth(tx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "depth": depth})
}

func (a *API) DeleteCompanyRelation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM CompanyRelation WHERE CompanyRelationID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := recomputeRelationDepths(a.DB); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func finishRelationDepth(database dbRunner, id int64) (*int64, error) {
	if err := recomputeRelationDepths(database); err != nil {
		return nil, err
	}
	var depth sql.NullInt64
	if err := database.QueryRow(`SELECT Depth FROM CompanyRelation WHERE CompanyRelationID = ?`, id).Scan(&depth); err != nil {
		return nil, err
	}
	if !depth.Valid {
		return nil, nil
	}
	value := depth.Int64
	return &value, nil
}

func writeRelationError(w http.ResponseWriter, err error) {
	var rejected *relationReject
	if errors.As(err, &rejected) {
		writeJSON(w, rejected.status, map[string]string{"error": rejected.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
}

// --- ProjectTable ----------------------------------------------------------
//
// Soft lookup feeding the Project dropdown in the company-relation forms —
// CompanyRelation.Project stays free TEXT (see migrations/010_project.sql),
// same non-FK relationship PositionRole.PositionName has to Staff.Position.

type Project struct {
	ProjectID int64  `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

func (a *API) ListProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT ProjectID, Name, Status FROM ProjectTable ORDER BY Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ProjectID, &p.Name, &p.Status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Name = strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	res, err := a.DB.Exec(`INSERT INTO ProjectTable (Name) VALUES (?)`, body.Name)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "project already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "projectId": id})
}

func (a *API) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Name = strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	var oldName string
	err = a.DB.QueryRow(`SELECT Name FROM ProjectTable WHERE ProjectID = ?`, id).Scan(&oldName)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if oldName != body.Name {
		// The project row and the relations that name it move together.
		tx, err := a.DB.Begin()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE ProjectTable SET Name = ? WHERE ProjectID = ?`, body.Name, id); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "project already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if _, err := tx.Exec(`UPDATE CompanyRelation SET Project = ? WHERE Project = ?`, body.Name, oldName); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM ProjectTable WHERE ProjectID = ?`, id)
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
