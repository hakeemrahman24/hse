package activitylog

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

// Section links, listings and delete for the Reference tab (see
// reference_store.go for the file and version side).

// referenceSections are the keys of the record-page sections that can have a
// Reference tab: data-section / data-sub in web/templates/emr.html. The list
// lives here, not in a CHECK constraint, so a new section needs no schema change.
var referenceSections = map[string]bool{
	"outpatient": true, "inpatient": true, "wellness": true,
	"alcohol": true, "fatigue": true, "malaria": true,
}

var (
	ErrUnknownSection   = errors.New("unknown section")
	ErrAlreadyInSection = errors.New("this document is already in that section")
	ErrNotInSection     = errors.New("this document is not in that section")
)

// SharedDocumentError blocks deleting a document that other sections still
// show: deleting would silently empty their Reference tabs. Sections lists
// them so the dialog can say "Also shown in: fatigue".
type SharedDocumentError struct {
	Sections []string
}

func (e *SharedDocumentError) Error() string {
	return fmt.Sprintf("this document is also shown in: %s", strings.Join(e.Sections, ", "))
}

// ReferenceItem is one row of a section's Reference tab. FileLink is
// deliberately not exposed: files are fetched through an authenticated
// handler by document id, never by path.
type ReferenceItem struct {
	DocumentID    int64    `json:"documentId"`
	Title         string   `json:"title"`
	DocumentsType string   `json:"documentsType"`
	FormLink      string   `json:"formLink,omitempty"`
	Notes         string   `json:"notes,omitempty"`
	HasFile       bool     `json:"hasFile"`
	VersionNo     int      `json:"versionNo"`
	UpdatedDate   string   `json:"updatedDate,omitempty"`
	AlsoIn        []string `json:"alsoIn"`
}

// ReferenceLibraryItem is one row of the "Pick existing" list: every document
// in the library, flagged when it is already in the section being edited.
type ReferenceLibraryItem struct {
	DocumentID    int64  `json:"documentId"`
	Title         string `json:"title"`
	DocumentsType string `json:"documentsType"`
	Program       string `json:"program"`
	HasFile       bool   `json:"hasFile"`
	UpdatedDate   string `json:"updatedDate,omitempty"`
	InSection     bool   `json:"inSection"`
}

// SQL fragments shared by both listings: a document's newest version number
// and that version's upload date ("Updated <date>"). Documents that are only
// a link have no version, so they get 0 and an empty date.
const (
	latestVersionNoSQL   = `COALESCE((SELECT MAX(v.VersionNo) FROM DocumentVersion v WHERE v.FK_DocumentID = d.DocumentID), 0)`
	latestVersionDateSQL = `COALESCE((SELECT v2.UploadDate FROM DocumentVersion v2 WHERE v2.FK_DocumentID = d.DocumentID ORDER BY v2.VersionNo DESC LIMIT 1), '')`
	hasFileSQL           = `CASE WHEN COALESCE(d.FileLink, '') <> '' THEN 1 ELSE 0 END`
)

// AddDocumentToSection shows an existing library document in a section's tab.
// It only adds a link row: no upload, no copy of the file.
func (a *API) AddDocumentToSection(docID int64, section string, staffID int64) error {
	if !referenceSections[section] {
		return ErrUnknownSection
	}
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM DocumentTable WHERE DocumentID = ?`, docID).Scan(&exists); err == sql.ErrNoRows {
		return ErrDocumentNotFound
	} else if err != nil {
		return err
	}
	// New items go to the end, so the tab lists them in the order the admin added them.
	_, err := a.DB.Exec(`
		INSERT INTO DocumentSection (FK_DocumentID, Section, SortOrder, FK_StaffID, AddedDate)
		VALUES (?, ?, (SELECT COALESCE(MAX(s.SortOrder), 0) + 1 FROM DocumentSection s WHERE s.Section = ?), ?, ?)`,
		docID, section, section, nullStaff(staffID), dates.Today())
	if isUniqueViolation(err) {
		return ErrAlreadyInSection
	}
	return err
}

// RemoveDocumentFromSection takes a document out of one section's tab. The
// document, its files and its other sections are untouched.
func (a *API) RemoveDocumentFromSection(docID int64, section string) error {
	res, err := a.DB.Exec(`DELETE FROM DocumentSection WHERE FK_DocumentID = ? AND Section = ?`, docID, section)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotInSection
	}
	return nil
}

// ListSectionDocuments is what a section's Reference tab shows, in the order
// the documents were added, each with the other sections that show it.
func (a *API) ListSectionDocuments(section string) ([]ReferenceItem, error) {
	if !referenceSections[section] {
		return nil, ErrUnknownSection
	}
	rows, err := a.DB.Query(`
		SELECT d.DocumentID, d.DocumentTitle, d.DocumentsType, COALESCE(d.FormLink, ''), COALESCE(d.Notes, ''),
		       `+hasFileSQL+`, `+latestVersionNoSQL+`, `+latestVersionDateSQL+`
		FROM DocumentSection s JOIN DocumentTable d ON d.DocumentID = s.FK_DocumentID
		WHERE s.Section = ?
		ORDER BY s.SortOrder, d.DocumentTitle`, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReferenceItem{}
	for rows.Next() {
		var it ReferenceItem
		var hasFile int
		if err := rows.Scan(&it.DocumentID, &it.Title, &it.DocumentsType, &it.FormLink, &it.Notes,
			&hasFile, &it.VersionNo, &it.UpdatedDate); err != nil {
			return nil, err
		}
		it.HasFile = hasFile == 1
		it.AlsoIn = []string{}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, a.fillAlsoIn(items, section)
}

// fillAlsoIn adds, to each item, the other sections that show the same
// document, in one query for the whole tab.
func (a *API) fillAlsoIn(items []ReferenceItem, section string) error {
	if len(items) == 0 {
		return nil
	}
	rows, err := a.DB.Query(`
		SELECT FK_DocumentID, Section FROM DocumentSection
		WHERE Section <> ? AND FK_DocumentID IN (SELECT FK_DocumentID FROM DocumentSection WHERE Section = ?)
		ORDER BY Section`, section, section)
	if err != nil {
		return err
	}
	defer rows.Close()
	other := map[int64][]string{}
	for rows.Next() {
		var id int64
		var sec string
		if err := rows.Scan(&id, &sec); err != nil {
			return err
		}
		other[id] = append(other[id], sec)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range items {
		if got := other[items[i].DocumentID]; got != nil {
			items[i].AlsoIn = got
		}
	}
	return nil
}

// ListLibraryDocuments is the "Pick existing" list: every document, files and
// links, with a flag for the ones this section already shows.
func (a *API) ListLibraryDocuments(section string) ([]ReferenceLibraryItem, error) {
	if !referenceSections[section] {
		return nil, ErrUnknownSection
	}
	rows, err := a.DB.Query(`
		SELECT d.DocumentID, d.DocumentTitle, d.DocumentsType, COALESCE(p.HealthProcedure, ''),
		       `+hasFileSQL+`, `+latestVersionDateSQL+`,
		       CASE WHEN EXISTS (SELECT 1 FROM DocumentSection s WHERE s.FK_DocumentID = d.DocumentID AND s.Section = ?) THEN 1 ELSE 0 END
		FROM DocumentTable d LEFT JOIN HealthProcedureTable p ON p.ProcID = d.FK_ProcID
		ORDER BY d.DocumentTitle`, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReferenceLibraryItem{}
	for rows.Next() {
		var it ReferenceLibraryItem
		var hasFile, inSection int
		if err := rows.Scan(&it.DocumentID, &it.Title, &it.DocumentsType, &it.Program,
			&hasFile, &it.UpdatedDate, &inSection); err != nil {
			return nil, err
		}
		it.HasFile, it.InSection = hasFile == 1, inSection == 1
		it.Program = display.ProgramTitle(it.Program) // "FitnessForWork" -> "Fitness For Work", as in /api/procedures
		items = append(items, it)
	}
	return items, rows.Err()
}

// DeleteReferenceDocument deletes a document from the whole library, but only
// when no section OTHER than fromSection still shows it. fromSection is the tab
// the admin is working in ("" when there is none, which makes every section
// count as "another"). All version files are removed from disk with the rows.
func (a *API) DeleteReferenceDocument(docID int64, fromSection string) error {
	rows, err := a.DB.Query(`SELECT Section FROM DocumentSection WHERE FK_DocumentID = ? AND Section <> ? ORDER BY Section`, docID, fromSection)
	if err != nil {
		return err
	}
	var others []string
	for rows.Next() {
		var sec string
		if err := rows.Scan(&sec); err != nil {
			rows.Close()
			return err
		}
		others = append(others, sec)
	}
	rows.Close()
	if len(others) > 0 {
		return &SharedDocumentError{Sections: others}
	}
	return a.removeDocument(docID)
}

// removeDocument deletes a document row with its versions and section links,
// then its files. Rows go first and in one transaction, files after commit: a
// failed delete then never loses files, and a file that cannot be removed only
// leaves an orphan on disk instead of a row pointing at nothing. The child rows
// are deleted explicitly so it works whether or not the database enforces
// ON DELETE CASCADE (production does; the SQLite test database does not).
// The activity log's own document delete uses this too, so it no longer leaves
// old versions on disk.
func (a *API) removeDocument(docID int64) error {
	var current sql.NullString
	err := a.DB.QueryRow(`SELECT FileLink FROM DocumentTable WHERE DocumentID = ?`, docID).Scan(&current)
	if err == sql.ErrNoRows {
		return ErrDocumentNotFound
	}
	if err != nil {
		return err
	}
	links, err := a.documentFileLinks(docID, current)
	if err != nil {
		return err
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM DocumentSection WHERE FK_DocumentID = ?`,
		`DELETE FROM DocumentVersion WHERE FK_DocumentID = ?`,
		`DELETE FROM DocumentTable WHERE DocumentID = ?`,
	} {
		if _, err := tx.Exec(stmt, docID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, link := range links {
		_ = os.Remove(link)
	}
	return nil
}

// documentFileLinks is every file a document owns: the current one plus each
// stored version, without repeats and without demo placeholders.
func (a *API) documentFileLinks(docID int64, current sql.NullString) ([]string, error) {
	seen := map[string]bool{}
	var links []string
	add := func(link string) {
		if link == "" || strings.HasPrefix(link, "placeholder:") || seen[link] {
			return
		}
		seen[link] = true
		links = append(links, link)
	}
	if current.Valid {
		add(current.String)
	}
	rows, err := a.DB.Query(`SELECT FileLink FROM DocumentVersion WHERE FK_DocumentID = ?`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			return nil, err
		}
		add(link)
	}
	return links, rows.Err()
}
