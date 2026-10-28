package sofi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hse-medpost/internal/middleware"
)

// maxNoteLetters keeps the note to a paragraph or two: it is printed on one page of the report.
const maxNoteLetters = 2000

var (
	ErrNoteTooLong      = errors.New("The note is too long (2000 letters at most).")
	ErrPeriodNotStarted = errors.New("That period has not opened yet, so there is nothing to write about.")
	errNoteForbidden    = "Only a doctor or the Chief Medical Officer can write the note."
)

// NoteView is the newest note of a period as the pages show it; the time is GMT+9 text.
type NoteView struct {
	Note      string `json:"note"`
	WrittenBy string `json:"writtenBy"`
	WrittenAt string `json:"writtenAt"`
}

// LatestNote is the newest note of the window, or an empty NoteView when none was written.
func (a *API) LatestNote(windowID int64) (NoteView, error) {
	var n NoteView
	var at string
	err := a.DB.QueryRow(`
		SELECT Note, WrittenBy, WrittenAt FROM SofiPeriodNote
		WHERE FK_WindowID = ? ORDER BY NoteID DESC LIMIT 1`, windowID).Scan(&n.Note, &n.WrittenBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return NoteView{}, nil
	}
	if err != nil {
		return NoteView{}, fmt.Errorf("read note of window %d: %w", windowID, err)
	}
	n.WrittenAt = siteFromDB(at)
	return n, nil
}

// SaveNote adds a new version of the period's note. An empty note is allowed: it clears the note
// shown, and the earlier versions stay on record. by is the writer's name for the audit line.
func (a *API) SaveNote(windowID int64, note, by string, now time.Time) error {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > maxNoteLetters {
		return ErrNoteTooLong
	}
	w, err := scanWindow(a.DB.QueryRow(`SELECT `+windowColumns+` FROM SofiWindow WHERE WindowID = ?`, windowID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWindowNotFound
	}
	if err != nil {
		return fmt.Errorf("read window %d: %w", windowID, err)
	}
	// A period that never opened has no surveys, so a note about it would describe nothing.
	if state := stateOf(w, now.UTC().Format(timeLayoutDB)); state == StateUpcoming || state == StateCancelled {
		return ErrPeriodNotStarted
	}
	_, err = a.DB.Exec(`INSERT INTO SofiPeriodNote (FK_WindowID, Note, WrittenBy, WrittenAt) VALUES (?, ?, ?, ?)`,
		windowID, note, by, now.UTC().Format(timeLayoutDB))
	if err != nil {
		return fmt.Errorf("insert SofiPeriodNote: %w", err)
	}
	return nil
}

// noteResponse is the note plus whether the signed-in person may change it, so the page can show
// a text box or just the text.
type noteResponse struct {
	NoteView
	CanWrite bool `json:"canWrite"`
}

func (a *API) canWriteNote(r *http.Request) bool {
	s := middleware.Session(r)
	return s != nil && middleware.MayOpenMedicalCheckup(a.DB, s.StaffID)
}

func (a *API) windowExists(id int64) (bool, error) {
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM SofiWindow WHERE WindowID = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check window %d: %w", id, err)
	}
	return n > 0, nil
}

func noteWindowID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		staffError(w, http.StatusBadRequest, "No such period.")
		return 0, false
	}
	return id, true
}

// GetNote returns the period's newest note to any signed-in staff.
func (a *API) GetNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteWindowID(w, r)
	if !ok {
		return
	}
	exists, err := a.windowExists(id)
	if err == nil && !exists {
		staffError(w, http.StatusNotFound, ErrWindowNotFound.Error())
		return
	}
	var n NoteView
	if err == nil {
		n, err = a.LatestNote(id)
	}
	if err != nil {
		log.Printf("sofi: get note: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not read the note.")
		return
	}
	writeJSON(w, http.StatusOK, noteResponse{NoteView: n, CanWrite: a.canWriteNote(r)})
}

// PutNote saves a new version of the note. Only a doctor, the Chief Medical Officer or the
// administrator may: the check is here, not only on the route, so no route change can open it up.
func (a *API) PutNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteWindowID(w, r)
	if !ok {
		return
	}
	if !a.canWriteNote(r) {
		staffError(w, http.StatusForbidden, errNoteForbidden)
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		staffError(w, http.StatusBadRequest, "Could not read the note.")
		return
	}
	err := a.SaveNote(id, in.Note, staffName(r), a.now())
	switch {
	case errors.Is(err, ErrWindowNotFound):
		staffError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNoteTooLong), errors.Is(err, ErrPeriodNotStarted):
		staffError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		log.Printf("sofi: save note: %v", err)
		staffError(w, http.StatusInternalServerError, "Could not save the note.")
	default:
		n, lerr := a.LatestNote(id)
		if lerr != nil {
			log.Printf("sofi: read back note: %v", lerr)
		}
		writeJSON(w, http.StatusOK, noteResponse{NoteView: n, CanWrite: true})
	}
}
