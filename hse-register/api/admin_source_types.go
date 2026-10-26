package main

import (
	"log"
	"net/http"
	"strings"
)

const maxSourceTypeName = 80

// typeRow is one line of Settings > Source types.
type typeRow struct {
	ID        int64
	Source    string
	Name      string
	Archived  bool
	SourceOff bool // its source is archived, so the type is hidden from the forms too
	Used      int  // observations that carry this source and type
}

func (h *hazardHandlers) typeRows() ([]typeRow, []string, error) {
	rs, err := h.db.Query(`SELECT d.id, s.name, d.name, d.is_archived = 1, s.is_archived = 1,
			(SELECT COUNT(*) FROM observations_observation o WHERE o.source = s.name COLLATE utf8mb4_unicode_ci AND o.source_detail = d.name COLLATE utf8mb4_unicode_ci)
		FROM ref_source_detail d JOIN ref_source s ON s.id = d.source_id
		ORDER BY s.sort_order, s.id, d.sort_order, d.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	var out []typeRow
	for rs.Next() {
		var t typeRow
		if err := rs.Scan(&t.ID, &t.Source, &t.Name, &t.Archived, &t.SourceOff, &t.Used); err != nil {
			return nil, nil, err
		}
		out = append(out, t)
	}
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	sources, err := h.sourceNames()
	return out, sources, err
}

// sourceNames lists every source name in order, for the Source dropdown of the add form.
func (h *hazardHandlers) sourceNames() ([]string, error) {
	rs, err := h.db.Query(`SELECT name FROM ref_source ORDER BY sort_order, id`)
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

func (h *hazardHandlers) typePage(w http.ResponseWriter, r *http.Request, status int, msg, kind string) {
	rows, sources, err := h.typeRows()
	if err != nil {
		log.Printf("source types: %v", err)
		http.Error(w, "Could not load the source types. Did you run migration 013?", http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "settings_source_types", PageData{
		Title: "Source types", Heading: "Source types", Active: "set-types",
		Subheading: "The Type choices under a source. When a source has types, staff must pick one (for example Site Audit, HO Audit or Corporate Audit under Internal Audit).",
		Session:    h.auth.session(r), TypeRows: rows, SourceOptions: sources, EditID: editID(r), Flash: msg, FlashKind: kind,
	})
}

func (h *hazardHandlers) typeList(w http.ResponseWriter, r *http.Request) {
	msg, kind := doneMessage(r, map[string]string{
		"added": "Type added.", "renamed": "Type saved.", "archived": "Type archived.",
		"restored": "Type restored.", "deleted": "Type deleted."})
	h.typePage(w, r, http.StatusOK, msg, kind)
}

func (h *hazardHandlers) typeAdd(w http.ResponseWriter, r *http.Request) {
	name, err := cleanListName(r.FormValue("name"), "type", maxSourceTypeName)
	if err != nil {
		h.typePage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	var sourceID int64
	if err := h.db.QueryRow(`SELECT id FROM ref_source WHERE name = ?`, strings.TrimSpace(r.FormValue("source"))).Scan(&sourceID); err != nil {
		h.typePage(w, r, http.StatusUnprocessableEntity, "Choose the source this type belongs to.", "error")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_source_detail (source_id, name, sort_order)
		SELECT ?, ?, COALESCE(MAX(sort_order), 0) + 1 FROM ref_source_detail WHERE source_id = ?`, sourceID, name, sourceID); err != nil {
		if isDuplicateKey(err) {
			h.typePage(w, r, http.StatusUnprocessableEntity, "That source already has this type.", "error")
			return
		}
		h.fail(w, "type add", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/source-types?done=added", http.StatusSeeOther)
}

// typeRename renames a type and the observations that carry it, in one transaction.
func (h *hazardHandlers) typeRename(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	name, err := cleanListName(r.FormValue("name"), "type", maxSourceTypeName)
	if err != nil {
		h.typePage(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.fail(w, "type rename", err)
		return
	}
	defer tx.Rollback()
	var old, source string
	if err := tx.QueryRow(`SELECT d.name, s.name FROM ref_source_detail d JOIN ref_source s ON s.id = d.source_id
		WHERE d.id = ? FOR UPDATE`, id).Scan(&old, &source); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := tx.Exec(`UPDATE ref_source_detail SET name = ? WHERE id = ?`, name, id); err != nil {
		if isDuplicateKey(err) {
			h.typePage(w, r, http.StatusUnprocessableEntity, "That source already has this type.", "error")
			return
		}
		h.fail(w, "type rename", err)
		return
	}
	if _, err := tx.Exec(`UPDATE observations_observation SET source_detail = ? WHERE source = ? AND source_detail = ?`, name, source, old); err != nil {
		h.fail(w, "type rename (observations)", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.fail(w, "type rename", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/source-types?done=renamed", http.StatusSeeOther)
}

func (h *hazardHandlers) typeArchive(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_source_detail", "/admin/settings/source-types", true)
}
func (h *hazardHandlers) typeRestore(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, "ref_source_detail", "/admin/settings/source-types", false)
}

// typeDelete removes a type no observation uses; one in use must be archived instead.
func (h *hazardHandlers) typeDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	res, err := h.db.Exec(`DELETE d FROM ref_source_detail d JOIN ref_source s ON s.id = d.source_id
		WHERE d.id = ? AND NOT EXISTS (
			SELECT 1 FROM observations_observation o WHERE o.source = s.name COLLATE utf8mb4_unicode_ci AND o.source_detail = d.name COLLATE utf8mb4_unicode_ci)`, id)
	if err != nil {
		h.fail(w, "type delete", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.typePage(w, r, http.StatusUnprocessableEntity, "Observations use this type, so it cannot be deleted. Archive it instead.", "error")
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/source-types?done=deleted", http.StatusSeeOther)
}
