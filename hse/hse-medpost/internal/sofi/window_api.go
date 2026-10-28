package sofi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

// msgClosed is shown (and translated) whenever /sofi is used outside an open window.
const msgClosed = "The questionnaire is closed right now."

// msgAlreadyDone is the answer to a second questionnaire from the same person in one period.
const msgAlreadyDone = "You have already completed the questionnaire for this period."

// requireOpen lets a /sofi request go on only while a window is open. It answers 403 itself and
// returns false otherwise. Every public route calls it, not just the landing page, because the
// JSON routes can be called directly without ever seeing the page.
func (a *API) requireOpen(w http.ResponseWriter, r *http.Request) (*windowRow, bool) {
	win, err := a.openWindow(a.now())
	if err != nil {
		log.Printf("sofi: open window: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return nil, false
	}
	if win == nil {
		writeError(w, r, http.StatusForbidden, msgClosed)
		return nil, false
	}
	return win, true
}

// sessionWindowID is the window a signed-in person may submit under, or 0 when none.
//
// A window that is open right now always wins, even when the person signed in under an earlier
// one: a result must land in the period it was really sent in, not in one that has closed (and may
// already be reported). Only when no window is open does the window recorded at sign-in count, until
// an admin ends it, so someone halfway through the form is not cut off by the closing time.
// A cookie from before windows existed has no window and so needs an open one.
func (a *API) sessionWindowID(s *auth.Session) (int64, error) {
	win, err := a.openWindow(a.now())
	if err != nil {
		return 0, err
	}
	if win != nil {
		return win.ID, nil
	}
	if s.WindowID == 0 {
		return 0, nil
	}
	ok, err := a.windowUsable(s.WindowID)
	if err != nil || !ok {
		return 0, err
	}
	return s.WindowID, nil
}

// closedData is what sofi_closed.html needs.
type closedData struct {
	pageData
	Next string // when the next window opens, GMT+9; "" when none is scheduled
}

// closedPage shows "closed, opens on ..." in the visitor's language. It is a normal 200 page:
// a 5xx could be swapped for Cloudflare's own error page on the way out.
func (a *API) closedPage(w http.ResponseWriter, r *http.Request, path string) {
	noStore(w)
	d := closedData{pageData: a.pageData(r, path)}
	next, err := a.nextWindow(a.now())
	if err != nil {
		log.Printf("sofi: next window: %v", err)
	} else if next != nil {
		d.Next = siteFromDB(next.OpensAt)
	}
	_ = a.pages.ExecuteTemplate(w, "sofi_closed.html", d)
}

// pageOpen is requireOpen for the HTML pages: it shows the closed page instead of JSON.
func (a *API) pageOpen(w http.ResponseWriter, r *http.Request, path string) bool {
	win, err := a.openWindow(a.now())
	if err != nil {
		log.Printf("sofi: open window: %v", err)
		http.Error(w, tr(langOf(r), "Something went wrong."), http.StatusInternalServerError)
		return false
	}
	if win == nil {
		a.closedPage(w, r, path)
		return false
	}
	return true
}

// startAllowed is pageOpen for the questionnaire page, which also admits a person who signed in
// under a window that has since run out (see sessionWindowID).
func (a *API) startAllowed(w http.ResponseWriter, r *http.Request, s *auth.Session) bool {
	id, err := a.sessionWindowID(s)
	if err != nil {
		log.Printf("sofi: start window: %v", err)
		http.Error(w, tr(langOf(r), "Something went wrong."), http.StatusInternalServerError)
		return false
	}
	if id == 0 {
		a.closedPage(w, r, "/sofi/start")
		return false
	}
	return true
}

// windowFilter turns the ?window= value of the staff list and summary into a WHERE piece:
// "" is no filter, "none" the questionnaires from before windows existed, a number one window.
func windowFilter(v string) (string, []any, error) {
	switch v {
	case "":
		return "", nil, nil
	case "none":
		return "s.FK_WindowID IS NULL", nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return "", nil, errors.New("window must be a window id or none")
	}
	return "s.FK_WindowID = ?", []any{id}, nil
}

// staffName is who to write on the audit line.
func staffName(r *http.Request) string {
	if s := middleware.Session(r); s != nil && s.Name != "" {
		return s.Name
	}
	return "admin"
}

// staffError answers the Settings page in plain English (staff pages are not translated).
func staffError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// AdminWindows lists every window ever scheduled, with who made and who ended each (admin only).
func (a *API) AdminWindows(w http.ResponseWriter, r *http.Request) {
	list, err := a.ListWindows(a.now())
	if err != nil {
		log.Printf("sofi: admin list windows: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not read the SOFI schedule.")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// AdminCreateWindow schedules a window. Opens and closes are GMT+9 (admin only).
func (a *API) AdminCreateWindow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label  string `json:"label"`
		Opens  string `json:"opens"`
		Closes string `json:"closes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		staffError(w, http.StatusBadRequest, "Could not read the form.")
		return
	}
	opens, err := ParseSiteTime(in.Opens)
	if err != nil {
		staffError(w, http.StatusBadRequest, "Enter when it opens.")
		return
	}
	closes, err := ParseSiteTime(in.Closes)
	if err != nil {
		staffError(w, http.StatusBadRequest, "Enter when it closes.")
		return
	}
	id, err := a.CreateWindow(in.Label, opens, closes, staffName(r), a.now())
	switch {
	case errors.Is(err, ErrWindowOverlap):
		staffError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrWindowOrder), errors.Is(err, ErrWindowPast), errors.Is(err, ErrWindowLabel):
		staffError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		log.Printf("sofi: create window: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not save the window.")
	default:
		writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
	}
}

// AdminEndWindow cancels an upcoming window or closes the open one right now (admin only).
func (a *API) AdminEndWindow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		staffError(w, http.StatusBadRequest, "No such window.")
		return
	}
	err = a.EndWindow(id, staffName(r), a.now())
	switch {
	case errors.Is(err, ErrWindowNotFound):
		staffError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrWindowFinished):
		staffError(w, http.StatusConflict, err.Error())
	case err != nil:
		log.Printf("sofi: end window %d: %v", id, err)
		staffError(w, http.StatusInternalServerError, "Could not end the window.")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// Round is one window that has opened, for the results' Round filter.
type Round struct {
	ID     int64  `json:"id"`
	Label  string `json:"label"`
	Opens  string `json:"opens"`
	Closes string `json:"closes"`
}

// windowStatus is the one-line status shown above the results. Times are GMT+9.
type windowStatus struct {
	State  string  `json:"state"` // open | upcoming | closed
	Until  string  `json:"until,omitempty"`
	Opens  string  `json:"opens,omitempty"`
	Zone   string  `json:"zone"`
	Rounds []Round `json:"rounds"`
}

// WindowStatus tells any signed-in staff whether /sofi is open, when it next opens, and which
// windows (rounds) have results to filter by.
func (a *API) WindowStatus(w http.ResponseWriter, r *http.Request) {
	now := a.now()
	out := windowStatus{State: "closed", Zone: SiteZone.String(), Rounds: []Round{}}
	open, err := a.openWindow(now)
	if err == nil && open == nil {
		var next *windowRow
		if next, err = a.nextWindow(now); err == nil && next != nil {
			out.State, out.Opens = "upcoming", siteFromDB(next.OpensAt)
		}
	} else if err == nil {
		out.State, out.Until = "open", siteFromDB(open.ClosesAt)
	}
	var list []WindowView
	if err == nil {
		list, err = a.ListWindows(now)
	}
	if err != nil {
		log.Printf("sofi: window status: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not read the SOFI schedule.")
		return
	}
	// Newest first, and only windows that actually opened: a cancelled or upcoming one has no results.
	for _, v := range list {
		if v.State == StateUpcoming || v.State == StateCancelled {
			continue
		}
		out.Rounds = append(out.Rounds, Round{ID: v.ID, Label: v.Label, Opens: v.Opens, Closes: v.Closes})
	}
	writeJSON(w, http.StatusOK, out)
}
