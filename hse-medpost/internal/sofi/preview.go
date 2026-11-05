package sofi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// Preview mode lets the administrator open /sofi to look at it, or to let the doctor try it, without
// scheduling a window. Nothing sent in preview is saved: the answers are scored and shown, then
// forgotten, so the medical team's table, dashboard and PDF never see them.
//
// The switch lives in memory on purpose. A restart then closes the preview, and a preview that
// somebody forgot cannot stay open: it also turns itself off after previewTTL. That matters because
// real employees could land on the page while it is on, and they would believe they had submitted.
const (
	// previewWindowID stands in for a window id. Real ids start at 1, so a session or submit that
	// carries -1 can never be mistaken for a saved period.
	previewWindowID int64 = -1

	previewTTL = 2 * time.Hour
)

// msgPreviewOn is shown on every /sofi page while preview is on (translated in i18n.go).
const msgPreviewOn = "Preview: your answers are not saved."

// previewState is the on/off switch and the time it turns itself off.
type previewState struct {
	mu    sync.Mutex
	until time.Time
}

// previewUntil is when preview ends, or the zero time when it is off.
func (a *API) previewUntil() time.Time {
	a.preview.mu.Lock()
	defer a.preview.mu.Unlock()
	if !a.now().Before(a.preview.until) {
		return time.Time{}
	}
	return a.preview.until
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
func (a *API) setPreview(on bool) error {
	if !on {
		a.preview.mu.Lock()
		a.preview.until = time.Time{}
		a.preview.mu.Unlock()
		return nil
	}
	win, err := a.openWindow(a.now())
	if err != nil {
		return err
	}
	if win != nil {
		return errPreviewWindowOpen
	}
	a.preview.mu.Lock()
	a.preview.until = a.now().Add(previewTTL)
	a.preview.mu.Unlock()
	return nil
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
	if err := a.setPreview(in.On); err != nil {
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
