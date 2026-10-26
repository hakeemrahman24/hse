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

	"github.com/go-sql-driver/mysql"
)

const maxAreaName = 100

// areaRow is one line of the Settings > Areas page.
type areaRow struct {
	ID   int64
	Name string
	Used int // observations that carry this area
	Subs []subAreaRow
}

// subAreaRow is one sub-area (the second level) of an area.
type subAreaRow struct {
	ID, AreaID int64
	Name       string
	Used       int // observations that carry this area and this sub-area
}

// cleanAreaName trims the name and checks it. Spaces inside are collapsed to one.
func cleanAreaName(s string) (string, error) {
	name := strings.Join(strings.Fields(s), " ")
	switch {
	case name == "":
		return "", errors.New("Enter an area name.")
	case utf8.RuneCountInString(name) > maxAreaName:
		return "", errors.New("The area name can have at most 100 characters.")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("The area name has characters that are not allowed.")
		}
	}
	return name, nil
}

func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

type areaHandlers struct {
	db   *sql.DB
	auth *authContext
	ref  *refData
}

func (h *areaHandlers) rows() ([]areaRow, error) {
	rs, err := h.db.Query(`SELECT a.id, a.name, COUNT(o.id)
		FROM ref_area a LEFT JOIN observations_observation o ON o.location = a.name COLLATE utf8mb4_unicode_ci
		GROUP BY a.id, a.name ORDER BY a.name`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []areaRow
	for rs.Next() {
		var a areaRow
		if err := rs.Scan(&a.ID, &a.Name, &a.Used); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	subs, err := h.subRows()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Subs = subs[out[i].ID]
	}
	return out, nil
}

// page shows the list; message and kind ("success" or "error") are shown at the top. The sub-areas of
// the area named by ?sub= are open for editing.
func (h *areaHandlers) page(w http.ResponseWriter, r *http.Request, status int, message, kind string) {
	subOf, _ := strconv.ParseInt(r.URL.Query().Get("sub"), 10, 64)
	h.pageFor(w, r, status, message, kind, subOf)
}

func (h *areaHandlers) pageFor(w http.ResponseWriter, r *http.Request, status int, message, kind string, subOf int64) {
	rows, err := h.rows()
	if err != nil {
		log.Printf("areas: %v", err)
		http.Error(w, "Could not load the areas. Did you run migration 007?", http.StatusInternalServerError)
		return
	}
	renderStatus(w, status, "settings_areas", PageData{
		Title: "Areas", Heading: "Areas", Active: "set-areas",
		Subheading: "The Area dropdown on the observation forms. Renaming an area also renames it on the observations that already use it.",
		Session:    h.auth.session(r), AreaRows: rows, EditID: editID(r), SubOf: subOf, Flash: message, FlashKind: kind,
	})
}

func (h *areaHandlers) list(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("done") {
	case "added":
		h.page(w, r, http.StatusOK, "Area added.", "success")
	case "renamed":
		h.page(w, r, http.StatusOK, "Area renamed.", "success")
	case "deleted":
		h.page(w, r, http.StatusOK, "Area deleted.", "success")
	case "sub-added":
		h.page(w, r, http.StatusOK, "Sub-area added.", "success")
	case "sub-renamed":
		h.page(w, r, http.StatusOK, "Sub-area renamed.", "success")
	case "sub-deleted":
		h.page(w, r, http.StatusOK, "Sub-area deleted.", "success")
	default:
		h.page(w, r, http.StatusOK, "", "")
	}
}

func (h *areaHandlers) add(w http.ResponseWriter, r *http.Request) {
	name, err := cleanAreaName(r.FormValue("name"))
	if err != nil {
		h.page(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO ref_area (name) VALUES (?)`, name); err != nil {
		if isDuplicateKey(err) {
			h.page(w, r, http.StatusUnprocessableEntity, "That area already exists.", "error")
			return
		}
		log.Printf("area add: %v", err)
		http.Error(w, "Could not save the area.", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/areas?done=added", http.StatusSeeOther)
}

// rename changes the area in the list and on every observation that carries it, in one
// transaction, so the list and the saved observations never disagree.
func (h *areaHandlers) rename(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	name, err := cleanAreaName(r.FormValue("name"))
	if err != nil {
		h.page(w, r, http.StatusUnprocessableEntity, err.Error(), "error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var old string
	if err := tx.QueryRow(`SELECT name FROM ref_area WHERE id = ? FOR UPDATE`, id).Scan(&old); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := tx.Exec(`UPDATE ref_area SET name = ? WHERE id = ?`, name, id); err != nil {
		if isDuplicateKey(err) {
			h.page(w, r, http.StatusUnprocessableEntity, "That area already exists.", "error")
			return
		}
		log.Printf("area rename: %v", err)
		http.Error(w, "Could not save the area.", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`UPDATE observations_observation SET location = ? WHERE location = ?`, name, old); err != nil {
		log.Printf("area rename (observations): %v", err)
		http.Error(w, "Could not save the area.", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Could not save the area.", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/areas?done=renamed", http.StatusSeeOther)
}

// remove deletes an area from the list. Observations that already carry it keep their text, so
// nothing is lost; the area just can no longer be chosen for new ones.
func (h *areaHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := h.db.Exec(`DELETE FROM ref_area WHERE id = ?`, id); err != nil {
		log.Printf("area delete: %v", err)
		http.Error(w, "Could not delete the area.", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, "/admin/settings/areas?done=deleted", http.StatusSeeOther)
}
