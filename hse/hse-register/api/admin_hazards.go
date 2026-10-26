package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxClassName    = 40
	maxCategoryName = 100
	maxSourceName   = 80
)

// cleanListName trims a lookup name, collapses inner spaces and checks its length and characters.
// what is the word used in messages ("class", "category", "source").
func cleanListName(s, what string, max int) (string, error) {
	name := strings.Join(strings.Fields(s), " ")
	switch {
	case name == "":
		return "", errors.New("Enter a " + what + " name.")
	case utf8.RuneCountInString(name) > max:
		return "", errors.New("The " + what + " name can have at most " + strconv.Itoa(max) + " characters.")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("The " + what + " name has characters that are not allowed.")
		}
	}
	return name, nil
}

// classRow is one line of Settings > Hazard classes.
type classRow struct {
	ID         int64
	Name       string
	Archived   bool
	Categories int // categories under this class
	Used       int // observations whose hazard is in this class
}

// categoryRow is one line of Settings > Hazard categories.
type categoryRow struct {
	ID       int64
	Class    string
	Name     string
	Archived bool // the category itself is archived
	ClassOff bool // its class is archived, so it is hidden from the forms too
	Used     int  // observations that carry this category
}

// sourceRow is one line of Settings > Sources.
type sourceRow struct {
	ID       int64
	Name     string
	Archived bool
	Builtin  bool // cannot be renamed or deleted, only archived
	Used     int
}

type hazardHandlers struct {
	db   *sql.DB
	auth *authContext
	ref  *refData
}

func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// editID is the list row whose edit form should be open: ?edit=ID on a page view, or the {id} of
// the rename/save request when it is shown again after a validation error. 0 means none.
func editID(r *http.Request) int64 {
	raw := r.URL.Query().Get("edit")
	if raw == "" {
		raw = r.PathValue("id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

func doneMessage(r *http.Request, messages map[string]string) (string, string) {
	if m, ok := messages[r.URL.Query().Get("done")]; ok {
		return m, "success"
	}
	return "", ""
}

func (h *hazardHandlers) fail(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	http.Error(w, "Could not save the change.", http.StatusInternalServerError)
}

// ---- classes ----

func (h *hazardHandlers) classRows() ([]classRow, error) {
	rs, err := h.db.Query(`SELECT c.id, c.name, c.is_archived = 1,
			(SELECT COUNT(*) FROM ref_hazard_category h WHERE h.hazard_class = c.name),
			(SELECT COUNT(*) FROM observations_observation o JOIN ref_hazard_category h ON h.id = o.hazard_id WHERE h.hazard_class = c.name)
		FROM ref_hazard_class c ORDER BY c.sort_order, c.id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []classRow
	for rs.Next() {
		var c classRow
		if err := rs.Scan(&c.ID, &c.Name, &c.Archived, &c.Categories, &c.Used); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rs.Err()
}

func (h *hazardHandlers) classPage(w http.ResponseWriter, r *http.Request, status int, msg, kind string) {
	rows, err := h.classRows()
	if err != nil {
		log.Printf("hazard classes: %v", err)
		http.Error(w, "Could not load the hazard classes. Did you run migration 012?", http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "settings_classes", PageData{
		Title: "Hazard classes", Heading: "Hazard classes", Active: "set-classes",
		Subheading: "The groups the hazard categories belong to. Archiving a class hides all its categories from the forms.",
		Session:    h.auth.session(r), ClassRows: rows, EditID: editID(r), Flash: msg, FlashKind: kind,
	})
}

func (h *hazardHandlers) classList(w http.ResponseWriter, r *http.Request) {
	msg, kind := doneMessage(r, map[string]string{
		"added": "Class added.", "renamed": "Class saved.", "archived": "Class archived.",
		"restored": "Class restored.", "deleted": "Class deleted."})
	h.classPage(w, r, http.StatusOK, msg, kind)
}

func (h *hazardHandlers) classAdd(w http.ResponseWriter, r *http.Request) {
	name, err := cleanListName(r.FormValue("name"), "class", maxClassName)
	if err != nil {
		h.classPage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_hazard_class (name, sort_order)
		SELECT ?, COALESCE(MAX(sort_order), 0) + 1 FROM ref_hazard_class`, name); err != nil {
		if isDuplicateKey(err) {
			h.classPage(w, r, http.StatusUnprocessableEntity, "That class already exists.", "error")
			return
		}
		h.fail(w, "class add", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-classes?done=added", http.StatusSeeOther)
}

// classRename changes the class name and the class text on its categories in one transaction.
func (h *hazardHandlers) classRename(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	name, err := cleanListName(r.FormValue("name"), "class", maxClassName)
	if err != nil {
		h.classPage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.fail(w, "class rename", err)
		return
	}
	defer tx.Rollback()
	var old string
	if err := tx.QueryRow(`SELECT name FROM ref_hazard_class WHERE id = ? FOR UPDATE`, id).Scan(&old); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := tx.Exec(`UPDATE ref_hazard_class SET name = ? WHERE id = ?`, name, id); err != nil {
		if isDuplicateKey(err) {
			h.classPage(w, r, http.StatusUnprocessableEntity, "That class already exists.", "error")
			return
		}
		h.fail(w, "class rename", err)
		return
	}
	if _, err := tx.Exec(`UPDATE ref_hazard_category SET hazard_class = ? WHERE hazard_class = ?`, name, old); err != nil {
		h.fail(w, "class rename (categories)", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.fail(w, "class rename", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-classes?done=renamed", http.StatusSeeOther)
}

// setArchived flips the archive flag of one row. table is always one of this file's own
// literals, never request input.
func (h *hazardHandlers) setArchived(w http.ResponseWriter, r *http.Request, table, redirect string, archived bool) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	flag, done := 0, "restored"
	if archived {
		flag, done = 1, "archived"
	}
	if _, err := h.db.Exec(`UPDATE `+table+` SET is_archived = ? WHERE id = ?`, flag, id); err != nil {
		h.fail(w, table+" archive", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, redirect+"?done="+done, http.StatusSeeOther)
}

func (h *hazardHandlers) classArchive(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_hazard_class", "/admin/settings/hazard-classes", true)
}
func (h *hazardHandlers) classRestore(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_hazard_class", "/admin/settings/hazard-classes", false)
}

// classDelete removes an empty class. A class that still has categories must be emptied or
// archived first, so no category is left without a class.
func (h *hazardHandlers) classDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	res, err := h.db.Exec(`DELETE FROM ref_hazard_class WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM ref_hazard_category h WHERE h.hazard_class = ref_hazard_class.name)`, id)
	if err != nil {
		h.fail(w, "class delete", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.classPage(w, r, http.StatusUnprocessableEntity, "This class still has categories. Move or delete them first, or archive the class instead.", "error")
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-classes?done=deleted", http.StatusSeeOther)
}

// ---- categories ----

func (h *hazardHandlers) categoryRows() ([]categoryRow, []string, error) {
	rs, err := h.db.Query(`SELECT h.id, h.hazard_class, h.name, h.is_archived = 1, COALESCE(c.is_archived, 0) = 1,
			(SELECT COUNT(*) FROM observations_observation o WHERE o.hazard_id = h.id)
		FROM ref_hazard_category h LEFT JOIN ref_hazard_class c ON c.name = h.hazard_class
		ORDER BY COALESCE(c.sort_order, 65535), h.name`)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	var out []categoryRow
	for rs.Next() {
		var c categoryRow
		if err := rs.Scan(&c.ID, &c.Class, &c.Name, &c.Archived, &c.ClassOff, &c.Used); err != nil {
			return nil, nil, err
		}
		out = append(out, c)
	}
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	classes, err := h.classNames()
	return out, classes, err
}

// classNames lists every class name in order, for the class dropdowns.
func (h *hazardHandlers) classNames() ([]string, error) {
	rs, err := h.db.Query(`SELECT name FROM ref_hazard_class ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var n string
		if err := rs.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rs.Err()
}

func (h *hazardHandlers) categoryPage(w http.ResponseWriter, r *http.Request, status int, msg, kind string) {
	rows, classes, err := h.categoryRows()
	if err != nil {
		log.Printf("hazard categories: %v", err)
		http.Error(w, "Could not load the hazard categories. Did you run migration 012?", http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "settings_categories", PageData{
		Title: "Hazard categories", Heading: "Hazard categories", Active: "set-categories",
		Subheading: "The Hazard dropdown on the observation forms, grouped by class.",
		Session:    h.auth.session(r), CategoryRows: rows, ClassOptions: classes, EditID: editID(r), Flash: msg, FlashKind: kind,
	})
}

func (h *hazardHandlers) categoryList(w http.ResponseWriter, r *http.Request) {
	msg, kind := doneMessage(r, map[string]string{
		"added": "Category added.", "saved": "Category saved.", "archived": "Category archived.",
		"restored": "Category restored.", "deleted": "Category deleted."})
	h.categoryPage(w, r, http.StatusOK, msg, kind)
}

func (h *hazardHandlers) classExists(name string) (bool, error) {
	var n int
	err := h.db.QueryRow(`SELECT COUNT(*) FROM ref_hazard_class WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

func (h *hazardHandlers) categoryAdd(w http.ResponseWriter, r *http.Request) {
	name, err := cleanListName(r.FormValue("name"), "category", maxCategoryName)
	if err != nil {
		h.categoryPage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	class := strings.TrimSpace(r.FormValue("class"))
	if ok, err := h.classExists(class); err != nil {
		h.fail(w, "category add", err)
		return
	} else if !ok {
		h.categoryPage(w, r, http.StatusUnprocessableEntity, "Choose the class this category belongs to.", "error")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_hazard_category (hazard_class, name) VALUES (?, ?)`, class, name); err != nil {
		if isDuplicateKey(err) {
			h.categoryPage(w, r, http.StatusUnprocessableEntity, "That category already exists.", "error")
			return
		}
		h.fail(w, "category add", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-categories?done=added", http.StatusSeeOther)
}

// categorySave renames a category and/or moves it to another class.
func (h *hazardHandlers) categorySave(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	name, err := cleanListName(r.FormValue("name"), "category", maxCategoryName)
	if err != nil {
		h.categoryPage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	class := strings.TrimSpace(r.FormValue("class"))
	if ok, err := h.classExists(class); err != nil {
		h.fail(w, "category save", err)
		return
	} else if !ok {
		h.categoryPage(w, r, http.StatusUnprocessableEntity, "Choose the class this category belongs to.", "error")
		return
	}
	res, err := h.db.Exec(`UPDATE ref_hazard_category SET name = ?, hazard_class = ? WHERE id = ?`, name, class, id)
	if err != nil {
		if isDuplicateKey(err) {
			h.categoryPage(w, r, http.StatusUnprocessableEntity, "That category already exists.", "error")
			return
		}
		h.fail(w, "category save", err)
		return
	}
	// MySQL reports 0 rows when nothing changed, which is fine; only a missing id is an error.
	if n, _ := res.RowsAffected(); n == 0 {
		var one int
		if h.db.QueryRow(`SELECT 1 FROM ref_hazard_category WHERE id = ?`, id).Scan(&one) != nil {
			http.NotFound(w, r)
			return
		}
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-categories?done=saved", http.StatusSeeOther)
}

func (h *hazardHandlers) categoryArchive(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_hazard_category", "/admin/settings/hazard-categories", true)
}
func (h *hazardHandlers) categoryRestore(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_hazard_category", "/admin/settings/hazard-categories", false)
}

// categoryDelete removes a category no observation uses. One that is in use must be archived, so
// no observation loses its classification.
func (h *hazardHandlers) categoryDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	res, err := h.db.Exec(`DELETE FROM ref_hazard_category WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM observations_observation o WHERE o.hazard_id = ref_hazard_category.id)`, id)
	if err != nil {
		h.fail(w, "category delete", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.categoryPage(w, r, http.StatusUnprocessableEntity, "Observations use this category, so it cannot be deleted. Archive it instead.", "error")
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/hazard-categories?done=deleted", http.StatusSeeOther)
}

// ---- sources ----

// The reference tables are created with the server's default utf8mb4 collation, which differs
// between installations (and from the observations table on the production database). Comparing
// an observation's text column with a reference name therefore names one collation explicitly
// (COLLATE utf8mb4_unicode_ci, the collation of the observations table); without it MariaDB
// refuses the comparison with "Illegal mix of collations".

func isBuiltinSource(name string) bool { return contains(staffSources, name) }

func (h *hazardHandlers) sourceRows() ([]sourceRow, error) {
	rs, err := h.db.Query(`SELECT s.id, s.name, s.is_archived = 1,
			(SELECT COUNT(*) FROM observations_observation o WHERE o.source = s.name COLLATE utf8mb4_unicode_ci)
		FROM ref_source s ORDER BY s.sort_order, s.id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []sourceRow
	for rs.Next() {
		var s sourceRow
		if err := rs.Scan(&s.ID, &s.Name, &s.Archived, &s.Used); err != nil {
			return nil, err
		}
		s.Builtin = isBuiltinSource(s.Name)
		out = append(out, s)
	}
	return out, rs.Err()
}

func (h *hazardHandlers) sourcePage(w http.ResponseWriter, r *http.Request, status int, msg, kind string) {
	rows, err := h.sourceRows()
	if err != nil {
		log.Printf("sources: %v", err)
		http.Error(w, "Could not load the sources. Did you run migration 012?", http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "settings_sources", PageData{
		Title: "Sources", Heading: "Sources", Active: "set-sources",
		Subheading: "The Source dropdown for staff observations (Internal Audit, External Audit, ...). SOC is set only by the public form.",
		Session:    h.auth.session(r), SourceRows: rows, EditID: editID(r), Flash: msg, FlashKind: kind,
	})
}

func (h *hazardHandlers) sourceList(w http.ResponseWriter, r *http.Request) {
	msg, kind := doneMessage(r, map[string]string{
		"added": "Source added.", "renamed": "Source saved.", "archived": "Source archived.",
		"restored": "Source restored.", "deleted": "Source deleted."})
	h.sourcePage(w, r, http.StatusOK, msg, kind)
}

func (h *hazardHandlers) sourceAdd(w http.ResponseWriter, r *http.Request) {
	name, err := cleanListName(r.FormValue("name"), "source", maxSourceName)
	if err != nil {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	if strings.EqualFold(name, sourceSOC) {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, "SOC is reserved for the public form.", "error")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_source (name, sort_order)
		SELECT ?, COALESCE(MAX(sort_order), 0) + 1 FROM ref_source`, name); err != nil {
		if isDuplicateKey(err) {
			h.sourcePage(w, r, http.StatusUnprocessableEntity, "That source already exists.", "error")
			return
		}
		h.fail(w, "source add", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/sources?done=added", http.StatusSeeOther)
}

// sourceRename renames a custom source and the observations that carry it, in one transaction.
func (h *hazardHandlers) sourceRename(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	name, err := cleanListName(r.FormValue("name"), "source", maxSourceName)
	if err != nil {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	if strings.EqualFold(name, sourceSOC) {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, "SOC is reserved for the public form.", "error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.fail(w, "source rename", err)
		return
	}
	defer tx.Rollback()
	var old string
	if err := tx.QueryRow(`SELECT name FROM ref_source WHERE id = ? FOR UPDATE`, id).Scan(&old); err != nil {
		http.NotFound(w, r)
		return
	}
	if isBuiltinSource(old) {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, old+" is built in and cannot be renamed. You can archive it.", "error")
		return
	}
	if _, err := tx.Exec(`UPDATE ref_source SET name = ? WHERE id = ?`, name, id); err != nil {
		if isDuplicateKey(err) {
			h.sourcePage(w, r, http.StatusUnprocessableEntity, "That source already exists.", "error")
			return
		}
		h.fail(w, "source rename", err)
		return
	}
	if _, err := tx.Exec(`UPDATE observations_observation SET source = ? WHERE source = ?`, name, old); err != nil {
		h.fail(w, "source rename (observations)", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.fail(w, "source rename", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/sources?done=renamed", http.StatusSeeOther)
}

func (h *hazardHandlers) sourceArchive(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_source", "/admin/settings/sources", true)
}
func (h *hazardHandlers) sourceRestore(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_source", "/admin/settings/sources", false)
}

// sourceDelete removes a custom source no observation uses.
func (h *hazardHandlers) sourceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var name string
	if err := h.db.QueryRow(`SELECT name FROM ref_source WHERE id = ?`, id).Scan(&name); err != nil {
		http.NotFound(w, r)
		return
	}
	if isBuiltinSource(name) {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, name+" is built in and cannot be deleted. You can archive it.", "error")
		return
	}
	res, err := h.db.Exec(`DELETE FROM ref_source WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM observations_observation o WHERE o.source = ref_source.name COLLATE utf8mb4_unicode_ci)`, id)
	if err != nil {
		h.fail(w, "source delete", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.sourcePage(w, r, http.StatusUnprocessableEntity, "Observations use this source, so it cannot be deleted. Archive it instead.", "error")
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/sources?done=deleted", http.StatusSeeOther)
}
