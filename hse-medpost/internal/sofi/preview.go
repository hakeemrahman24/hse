package sofi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

// Preview mode lets the administrator open /sofi to look at it, or to let the doctor try it, without
// scheduling a window. Nothing sent in preview is saved: the answers are scored and shown, then
// forgotten, so the medical team's table, dashboard and PDF never see them.
//
// The switch is a row in SofiPreview, not a variable in memory: a server restart (a deploy, or a
// crash) must not close a preview the administrator opened a few minutes ago. A preview that
// somebody forgot still cannot stay open, because the row carries its own end time (previewTTL).
// That matters because real employees could land on the page while it is on, and they would
// believe they had submitted.
const (
	// previewWindowID stands in for a window id. Real ids start at 1, so a session or submit that
	// carries -1 can never be mistaken for a saved period.
	previewWindowID int64 = -1

	previewTTL = 2 * time.Hour
)

// msgPreviewOn is shown on every /sofi page while preview is on (translated in i18n.go).
const msgPreviewOn = "Preview: your answers are not saved."

// previewUntil is when preview ends, or the zero time when it is off. If the table is missing
// (the MariaDB migration was not run) preview counts as off, and the log says so once.
func (a *API) previewUntil() time.Time {
	var s string
	err := a.DB.QueryRow(`SELECT UntilAt FROM SofiPreview WHERE PreviewID = 1`).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}
	}
	if err != nil {
		if !a.previewWarned.Swap(true) {
			log.Printf("sofi: preview switch unreadable, treating preview as off (run migrations/mysql/037_sofi_preview.sql?): %v", err)
		}
		return time.Time{}
	}
	until, err := time.Parse(timeLayoutDB, s)
	if err != nil || !a.now().Before(until) {
		return time.Time{}
	}
	return until
}

// previewActive reports whether /sofi is open for preview. A real window always wins, so a real
// submission is never lost to a preview that is still switched on.
func (a *API) previewActive() bool {
	if a.previewUntil().IsZero() {
		return false
	}
	win, err := a.openWindow(a.now())
	return err == nil && win == nil
}

var errPreviewWindowOpen = errors.New("A window is open right now, so /sofi is already open and recording. Preview is not needed.")

// setPreview switches preview on or off. Turning it on while a real window is open is refused:
// that window is already open, and the admin should not think it is being previewed.
func (a *API) setPreview(on bool, by string) error {
	if !on {
		_, err := a.DB.Exec(`DELETE FROM SofiPreview`)
		return err
	}
	win, err := a.openWindow(a.now())
	if err != nil {
		return err
	}
	if win != nil {
		return errPreviewWindowOpen
	}
	now := a.now().UTC()
	// Delete then insert, in one transaction: "INSERT ... ON CONFLICT" and "ON DUPLICATE KEY" are
	// spelled differently on SQLite and MariaDB.
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM SofiPreview`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO SofiPreview (PreviewID, UntilAt, SetBy, SetAt) VALUES (1, ?, ?, ?)`,
		now.Add(previewTTL).Format(timeLayoutDB), by, now.Format(timeLayoutDB)); err != nil {
		return err
	}
	return tx.Commit()
}

// previewUntilText is the end of preview in GMT+9 text, "" when off.
func (a *API) previewUntilText() string {
	if !a.previewActive() {
		return ""
	}
	return siteFromDB(a.previewUntil().UTC().Format(timeLayoutDB))
}

// AdminPreview is POST /api/sofi-preview {"on": true|false}, administrator only.
func (a *API) AdminPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		staffError(w, http.StatusBadRequest, "Send {\"on\": true} or {\"on\": false}.")
		return
	}
	if err := a.setPreview(in.On, staffName(r)); err != nil {
		if errors.Is(err, errPreviewWindowOpen) {
			staffError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("sofi: preview: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not change preview.")
		return
	}
	log.Printf("sofi: preview on=%v by %s", in.On, staffName(r))
	writeJSON(w, http.StatusOK, map[string]any{"preview": a.previewActive(), "previewUntil": a.previewUntilText()})
}
