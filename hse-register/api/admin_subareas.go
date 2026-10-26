package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The second level of the area list. Administrators add, rename and delete the sub-areas of an area
// in Settings > Areas (Manage next to the area). The SOC wizard asks for the sub-area only when the
// chosen area has some. A report keeps the sub-area as text, like the area itself.

const maxSubAreaName = 100

// cleanSubAreaName trims the name and checks it. Spaces inside are collapsed to one.
func cleanSubAreaName(s string) (string, error) {
	name := strings.Join(strings.Fields(s), " ")
	switch {
	case name == "":
		return "", errors.New("Enter a sub-area name.")
	case utf8.RuneCountInString(name) > maxSubAreaName:
		return "", errors.New("The sub-area name can have at most 100 characters.")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("The sub-area name has characters that are not allowed.")
		}
	}
	return name, nil
}

// subRows lists every sub-area with how many observations carry it, grouped by area id.
func (h *areaHandlers) subRows() (map[int64][]subAreaRow, error) {
	rs, err := h.db.Query(`SELECT s.id, s.area_id, s.name, COUNT(o.id)
		FROM ref_subarea s JOIN ref_area a ON a.id = s.area_id
		LEFT JOIN observations_observation o
		  ON o.location = a.name COLLATE utf8mb4_unicode_ci AND o.sub_area = s.name COLLATE utf8mb4_unicode_ci
		GROUP BY s.id, s.area_id, s.name ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[int64][]subAreaRow{}
	for rs.Next() {
		var r subAreaRow
		if err := rs.Scan(&r.ID, &r.AreaID, &r.Name, &r.Used); err != nil {
			return nil, err
		}
		out[r.AreaID] = append(out[r.AreaID], r)
	}
	return out, rs.Err()
}

func subAreasURL(areaID int64, done string) string {
	u := "/admin/settings/areas?sub=" + strconv.FormatInt(areaID, 10)
	if done != "" {
		u += "&done=" + done
	}
	return u
}

func (h *areaHandlers) subAdd(w http.ResponseWriter, r *http.Request) {
	areaID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	name, err := cleanSubAreaName(r.FormValue("name"))
	if err != nil {
		h.pageFor(w, r, http.StatusUnprocessableEntity, err.Error(), "error", areaID)
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_subarea (area_id, name) VALUES (?, ?)`, areaID, name); err != nil {
		if isDuplicateKey(err) {
			h.pageFor(w, r, http.StatusUnprocessableEntity, "That sub-area already exists in this area.", "error", areaID)
			return
		}
		log.Printf("sub-area add: %v", err)
		http.Error(w, "Could not save the sub-area. Did you run migration 019?", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, subAreasURL(areaID, "sub-added"), http.StatusSeeOther)
}

// subRename changes the sub-area in the list and on every observation of that area that carries it,
// in one transaction, so the list and the saved observations never disagree.
func (h *areaHandlers) subRename(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	name, nameErr := cleanSubAreaName(r.FormValue("name"))
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var areaID int64
	var areaName, old string
	if err := tx.QueryRow(`SELECT s.area_id, a.name, s.name FROM ref_subarea s JOIN ref_area a ON a.id = s.area_id WHERE s.id = ? FOR UPDATE`, id).Scan(&areaID, &areaName, &old); err != nil {
		http.NotFound(w, r)
		return
	}
	if nameErr != nil {
		h.pageFor(w, r, http.StatusUnprocessableEntity, nameErr.Error(), "error", areaID)
		return
	}
	if _, err := tx.Exec(`UPDATE ref_subarea SET name = ? WHERE id = ?`, name, id); err != nil {
		if isDuplicateKey(err) {
			h.pageFor(w, r, http.StatusUnprocessableEntity, "That sub-area already exists in this area.", "error", areaID)
			return
		}
		log.Printf("sub-area rename: %v", err)
		http.Error(w, "Could not save the sub-area.", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`UPDATE observations_observation SET sub_area = ? WHERE location = ? AND sub_area = ?`, name, areaName, old); err != nil {
		log.Printf("sub-area rename (observations): %v", err)
		http.Error(w, "Could not save the sub-area.", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Could not save the sub-area.", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, subAreasURL(areaID, "sub-renamed"), http.StatusSeeOther)
}

// subRemove deletes a sub-area from the list. Observations that already carry it keep their text, so
// nothing is lost; it just can no longer be chosen for new ones.
func (h *areaHandlers) subRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var areaID int64
	if err := h.db.QueryRow(`SELECT area_id FROM ref_subarea WHERE id = ?`, id).Scan(&areaID); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM ref_subarea WHERE id = ?`, id); err != nil {
		log.Printf("sub-area delete: %v", err)
		http.Error(w, "Could not delete the sub-area.", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, subAreasURL(areaID, "sub-deleted"), http.StatusSeeOther)
}
