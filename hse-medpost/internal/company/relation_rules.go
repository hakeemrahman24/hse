package company

import (
	"database/sql"
	"strings"
)

// relationReject is a save that must not be stored. status is the HTTP code.
// Called from CreateCompanyRelation and UpdateCompanyRelation before INSERT/UPDATE.
type relationReject struct {
	status int
	msg    string
}

func (e *relationReject) Error() string { return e.msg }

func normalizeParentID(id *int64) *int64 {
	if id == nil || *id == 0 {
		return nil
	}
	return id
}

// rejectRelation blocks a company parenting itself, a parent chain that
// loops back to that company, and a second relation for the same company
// on the same project. excludeID is the row being edited; 0 on create.
func rejectRelation(database *sql.DB, companyID int64, parentID *int64, project string, excludeID int64) error {
	if companyID == 0 {
		return &relationReject{status: 400, msg: "company required"}
	}
	parentID = normalizeParentID(parentID)
	project = strings.TrimSpace(project)
	if parentID != nil && *parentID == companyID {
		return &relationReject{status: 400, msg: "a company cannot be its own parent"}
	}
	var existing int64
	err := database.QueryRow(`
		SELECT CompanyRelationID FROM CompanyRelation
		WHERE FK_CompanyID = ? AND COALESCE(Project, '') = ? AND CompanyRelationID <> ?`,
		companyID, project, excludeID).Scan(&existing)
	if err == nil {
		return &relationReject{status: 409, msg: "this company already has a relation on that project"}
	}
	if err != sql.ErrNoRows {
		return err
	}
	loop, err := parentChainLoops(database, companyID, parentID, project, excludeID)
	if err != nil {
		return err
	}
	if loop {
		return &relationReject{status: 400, msg: "that parent is already under this company on the same project"}
	}
	return nil
}

func parentChainLoops(database *sql.DB, companyID int64, parentID *int64, project string, excludeID int64) (bool, error) {
	if parentID == nil {
		return false, nil
	}
	seen := map[int64]bool{companyID: true}
	current := *parentID
	for step := 0; step < 64; step++ {
		if seen[current] {
			return true, nil
		}
		seen[current] = true
		var next sql.NullInt64
		err := database.QueryRow(`
			SELECT FK_ParentID FROM CompanyRelation
			WHERE FK_CompanyID = ? AND COALESCE(Project, '') = ? AND CompanyRelationID <> ?
			ORDER BY CompanyRelationID LIMIT 1`,
			current, project, excludeID).Scan(&next)
		if err == sql.ErrNoRows || (err == nil && !next.Valid) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		current = next.Int64
	}
	return true, nil
}
