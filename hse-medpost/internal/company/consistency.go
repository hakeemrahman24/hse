package company

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
)

// projectClientCompanies names the client company for each production project.
// Clients have no CompanyRelation row (they are depth 0); the project points
// at them so "companies on LNG" still includes Layar Nusantara Gas.
var projectClientCompanies = map[string]string{
	"LNG":   "Layar Nusantara Gas",
	"GOKPL": "Genting Oil Kasuri Pte Ltd",
}

var entityTypeCodes = []struct {
	name string
	code string
}{
	{"Client", "E01"},
	{"Contractor", "E02"},
	{"Consultant", "E03"},
	{"ManPowerSupply", "E04"},
	{"Supplier", "E05"},
	{"Government", "E06"},
}

// EnsureEntityTypeCodes writes the stable E01–E06 codes onto the six entity
// types, inserting any of those names that are missing. Safe to re-run.
func EnsureEntityTypeCodes(database dbRunner) error {
	for _, c := range entityTypeCodes {
		if err := ensureEntityTypeCode(database, c.name, c.code); err != nil {
			return err
		}
	}
	return nil
}

func ensureEntityTypeCode(database dbRunner, name, code string) error {
	var id int64
	err := database.QueryRow(`SELECT EntityTypeID FROM EntityType WHERE LOWER(Name) = LOWER(?)`, name).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = database.Exec(`INSERT INTO EntityType (Name, LegacyCode) VALUES (?, ?)`, name, code)
		return err
	}
	if err != nil {
		return err
	}
	_, err = database.Exec(`UPDATE EntityType SET LegacyCode = ? WHERE EntityTypeID = ?`, code, id)
	return err
}

// EnsureProjectClientColumn adds ProjectTable.FK_ClientCompanyID on databases
// created before that column existed. CREATE TABLE IF NOT EXISTS will not
// alter an existing table, and this migration set re-runs on every startup.
func EnsureProjectClientColumn(database dbRunner) error {
	return addColumn(database, "ProjectTable", "FK_ClientCompanyID",
		`ALTER TABLE ProjectTable ADD COLUMN FK_ClientCompanyID INTEGER NULL`)
}

// EnsureProjectClients points LNG and GOKPL at their client companies.
func EnsureProjectClients(database dbRunner) error {
	ok, err := tableHasColumn(database, "ProjectTable", "FK_ClientCompanyID")
	if err != nil || !ok {
		return err
	}
	for project, companyName := range projectClientCompanies {
		if err := setProjectClient(database, project, companyName); err != nil {
			return err
		}
	}
	return nil
}

func setProjectClient(database dbRunner, project, companyName string) error {
	var companyID int64
	err := database.QueryRow(`SELECT CompanyID FROM Company WHERE Name = ?`, companyName).Scan(&companyID)
	if err == sql.ErrNoRows {
		log.Printf("company seed: project %s client %q not found", project, companyName)
		return nil
	}
	if err != nil {
		return err
	}
	_, err = database.Exec(
		`UPDATE ProjectTable SET FK_ClientCompanyID = ? WHERE Name = ? AND (FK_ClientCompanyID IS NULL OR FK_ClientCompanyID != ?)`,
		companyID, project, companyID,
	)
	return err
}

// EnsureCompanyRelationProjectColumn adds CompanyRelation.FK_ProjectID.
// Project text stays filled with the project name so existing queries keep working.
func EnsureCompanyRelationProjectColumn(database dbRunner) error {
	return addColumn(database, "CompanyRelation", "FK_ProjectID",
		`ALTER TABLE CompanyRelation ADD COLUMN FK_ProjectID INTEGER NULL`)
}

func addColumn(database dbRunner, table, column, ddl string) error {
	ok, err := tableHasColumn(database, table, column)
	if err != nil || ok {
		return err
	}
	if _, err = database.Exec(ddl); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

func tableHasColumn(database dbRunner, table, column string) (bool, error) {
	if err := safeIdent(table); err != nil {
		return false, err
	}
	if err := safeIdent(column); err != nil {
		return false, err
	}
	if isSQLite(database) {
		return sqliteHasColumn(database, table, column)
	}
	var n int
	err := database.QueryRow(`
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		table, column).Scan(&n)
	return n > 0, err
}

func sqliteHasColumn(database dbRunner, table, column string) (bool, error) {
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

func isSQLite(database dbRunner) bool {
	var v string
	return database.QueryRow(`SELECT sqlite_version()`).Scan(&v) == nil
}

func safeIdent(name string) error {
	if name == "" {
		return fmt.Errorf("empty sql identifier")
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return fmt.Errorf("invalid sql identifier %q", name)
		}
	}
	return nil
}

func projectIDByName(database dbRunner, name string) (sql.NullInt64, error) {
	var id int64
	err := database.QueryRow(`SELECT ProjectID FROM ProjectTable WHERE Name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return sql.NullInt64{}, nil
	}
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func legacyRelationCode(code, abbreviation, project string) bool {
	abbreviation = strings.TrimSpace(abbreviation)
	if abbreviation == "" || project == "" {
		return false
	}
	return code == abbreviation+"-"+project
}

type storedRelation struct {
	id        int64
	companyID int64
	parentID  sql.NullInt64
	project   string
	projectFK sql.NullInt64
	depth     sql.NullInt64
}

func recomputeRelationDepths(database dbRunner) error {
	hasProjectFK, err := tableHasColumn(database, "CompanyRelation", "FK_ProjectID")
	if err != nil {
		return err
	}
	clients, err := loadProjectClients(database)
	if err != nil {
		return err
	}
	rows, err := loadStoredRelations(database, hasProjectFK)
	if err != nil {
		return err
	}
	computed := deriveDepths(rows, clients)
	return writeDerivedDepths(database, rows, computed)
}

func deriveDepths(rows []storedRelation, clients projectClients) map[int64]int64 {
	computed := map[int64]int64{}
	limit := len(rows) + 1
	if limit < 2 {
		limit = 2
	}
	for pass := 0; pass < limit; pass++ {
		if !deriveDepthPass(rows, clients, computed) {
			break
		}
	}
	return computed
}

// EnsureRelationDepths fills CompanyRelation.Depth from each row's parent
// chain. Saving a relation calls the same calculator; this covers rows that
// were stored before that, and any chain a later edit changed.
func EnsureRelationDepths(database dbRunner) error {
	return recomputeRelationDepths(database)
}

func deriveDepthPass(rows []storedRelation, clients projectClients, computed map[int64]int64) bool {
	progress := false
	for i := range rows {
		row := rows[i]
		if _, done := computed[row.id]; done {
			continue
		}
		if !row.parentID.Valid {
			computed[row.id] = 0
			progress = true
			continue
		}
		depth, ok := depthFromParent(rows, row, clients, computed)
		if !ok {
			continue
		}
		computed[row.id] = depth
		progress = true
	}
	return progress
}

func depthFromParent(rows []storedRelation, row storedRelation, clients projectClients, computed map[int64]int64) (int64, bool) {
	if clientID, ok := clients.of(row); ok && row.parentID.Int64 == clientID {
		return 1, true
	}
	parent := findRelation(rows, row.parentID.Int64, row)
	if parent == nil {
		// Parent is the project client, or another company with no relation
		// row on this project. Either way it sits at the top, so this row is 1.
		return 1, true
	}
	parentDepth, ok := computed[parent.id]
	if !ok {
		return 0, false
	}
	return parentDepth + 1, true
}

func writeDerivedDepths(database dbRunner, rows []storedRelation, computed map[int64]int64) error {
	for i := range rows {
		row := rows[i]
		depth, ok := computed[row.id]
		if !ok {
			if row.parentID.Valid {
				log.Printf("company seed: relation %d depth left unchanged; parent chain is not under the project client", row.id)
			}
			continue
		}
		if row.depth.Valid && row.depth.Int64 == depth {
			continue
		}
		if _, err := database.Exec(`UPDATE CompanyRelation SET Depth = ? WHERE CompanyRelationID = ?`, depth, row.id); err != nil {
			return err
		}
	}
	return nil
}

func loadStoredRelations(database dbRunner, hasProjectFK bool) ([]storedRelation, error) {
	q := `SELECT CompanyRelationID, FK_CompanyID, FK_ParentID, COALESCE(Project,''), Depth FROM CompanyRelation`
	if hasProjectFK {
		q = `SELECT CompanyRelationID, FK_CompanyID, FK_ParentID, COALESCE(Project,''), Depth, FK_ProjectID FROM CompanyRelation`
	}
	rows, err := database.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedRelation
	for rows.Next() {
		item, err := scanStoredRelation(rows, hasProjectFK)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanStoredRelation(rows *sql.Rows, hasProjectFK bool) (storedRelation, error) {
	var item storedRelation
	if hasProjectFK {
		err := rows.Scan(&item.id, &item.companyID, &item.parentID, &item.project, &item.depth, &item.projectFK)
		return item, err
	}
	err := rows.Scan(&item.id, &item.companyID, &item.parentID, &item.project, &item.depth)
	return item, err
}

func findRelation(rows []storedRelation, companyID int64, same storedRelation) *storedRelation {
	for i := range rows {
		if rows[i].companyID != companyID || !sameProject(rows[i], same) {
			continue
		}
		return &rows[i]
	}
	return nil
}

func sameProject(a, b storedRelation) bool {
	if a.projectFK.Valid && b.projectFK.Valid {
		return a.projectFK.Int64 == b.projectFK.Int64
	}
	return a.project != "" && a.project == b.project
}

type projectClients struct {
	byName map[string]int64
	byID   map[int64]int64
}

func (c projectClients) of(row storedRelation) (int64, bool) {
	if row.projectFK.Valid {
		if id, ok := c.byID[row.projectFK.Int64]; ok {
			return id, true
		}
	}
	id, ok := c.byName[row.project]
	return id, ok
}

func loadProjectClients(database dbRunner) (projectClients, error) {
	clients := projectClients{byName: map[string]int64{}, byID: map[int64]int64{}}
	if err := readProjectClientColumn(database, &clients); err != nil {
		return projectClients{}, err
	}
	if err := fillKnownProjectClients(database, &clients); err != nil {
		return projectClients{}, err
	}
	return clients, nil
}

func readProjectClientColumn(database dbRunner, clients *projectClients) error {
	ok, err := tableHasColumn(database, "ProjectTable", "FK_ClientCompanyID")
	if err != nil || !ok {
		return err
	}
	rows, err := database.Query(`SELECT ProjectID, Name, FK_ClientCompanyID FROM ProjectTable`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID int64
		var name string
		var client sql.NullInt64
		if err := rows.Scan(&projectID, &name, &client); err != nil {
			return err
		}
		if !client.Valid {
			continue
		}
		clients.byName[name] = client.Int64
		clients.byID[projectID] = client.Int64
	}
	return rows.Err()
}

func fillKnownProjectClients(database dbRunner, clients *projectClients) error {
	for project, companyName := range projectClientCompanies {
		if _, ok := clients.byName[project]; ok {
			continue
		}
		var companyID int64
		err := database.QueryRow(`SELECT CompanyID FROM Company WHERE Name = ?`, companyName).Scan(&companyID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		clients.byName[project] = companyID
		var projectID int64
		err = database.QueryRow(`SELECT ProjectID FROM ProjectTable WHERE Name = ?`, project).Scan(&projectID)
		if err == nil {
			clients.byID[projectID] = companyID
		}
	}
	return nil
}

func ensureCompanyProjectUniqueIndex(database dbRunner) error {
	var dup int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT FK_CompanyID, Project
			FROM CompanyRelation
			WHERE Project IS NOT NULL AND Project <> ''
			GROUP BY FK_CompanyID, Project
			HAVING COUNT(*) > 1
		)`).Scan(&dup); err != nil {
		return err
	}
	if dup > 0 {
		return fmt.Errorf("company relation: %d company/project pairs are duplicated", dup)
	}
	_, err := database.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_company_relation_company_project
		ON CompanyRelation(FK_CompanyID, Project)`)
	if err != nil && !indexAlreadyExists(err) {
		return err
	}
	return nil
}

func indexAlreadyExists(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "duplicate key name")
}
