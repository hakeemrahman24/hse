package entrymcu

import (
	"database/sql"
	"strings"
)

// McuRecord.WorkLocation holds one or more lookup names joined with "; "
// (see CreateMcuRecord/UpdateMcuRecord). Exact-match SQL only sees the whole
// joined text, so rename and in-use checks must work on the individual names.

func splitWorkLocationNames(text string) []string {
	var names []string
	for _, part := range strings.Split(text, ";") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// replaceWorkLocationName swaps oldName for newName in a joined value. It
// reports false when oldName is not one of the names, so untouched rows are
// not rewritten. A duplicate created by the swap is dropped.
func replaceWorkLocationName(text, oldName, newName string) (string, bool) {
	found := false
	var out []string
	for _, name := range splitWorkLocationNames(text) {
		if name == oldName {
			found = true
			name = newName
		}
		if !containsString(out, name) {
			out = append(out, name)
		}
	}
	if !found {
		return text, false
	}
	return strings.Join(out, "; "), true
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// sqlRunner is satisfied by *sql.DB and *sql.Tx, so the rename can run inside
// the lookup update's transaction.
type sqlRunner interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// workLocationCandidates returns the checkup IDs and stored values whose text
// contains name. LIKE only narrows the rows; the exact token match is done in
// Go so "Site A" never matches "Site AB".
func workLocationCandidates(database sqlRunner, name string) (map[int64]string, error) {
	rows, err := database.Query(
		`SELECT CheckUpID, WorkLocation FROM McuRecord WHERE WorkLocation LIKE ?`, "%"+name+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var text sql.NullString
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		if containsString(splitWorkLocationNames(text.String), name) {
			out[id] = text.String
		}
	}
	return out, rows.Err()
}

// workLocationInUse counts checkup records that use name, alone or in a list.
func workLocationInUse(database sqlRunner, name string) (int, error) {
	matches, err := workLocationCandidates(database, name)
	return len(matches), err
}

// renameWorkLocationInRecords rewrites oldName to newName inside every
// checkup's WorkLocation value, whether it is the only name or one of several.
func renameWorkLocationInRecords(database sqlRunner, oldName, newName string) error {
	matches, err := workLocationCandidates(database, oldName)
	if err != nil {
		return err
	}
	for id, text := range matches {
		updated, changed := replaceWorkLocationName(text, oldName, newName)
		if !changed {
			continue
		}
		if _, err := database.Exec(`UPDATE McuRecord SET WorkLocation = ? WHERE CheckUpID = ?`, updated, id); err != nil {
			return err
		}
	}
	return nil
}
