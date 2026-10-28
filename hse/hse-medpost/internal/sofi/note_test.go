package sofi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
	"hse-medpost/internal/middleware"
)

// finishedPeriod schedules a window on the 2nd to 5th and returns its id; the notes are written
// on the 6th, when it is over.
func finishedPeriod(t *testing.T, api *API) int64 {
	t.Helper()
	id, err := api.CreateWindow("Round 1", at(2, 0, 0), at(5, 0, 0), "Admin", at(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func countNotes(t *testing.T, api *API) int {
	t.Helper()
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiPeriodNote`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLatestNoteIsEmptyBeforeAnyIsWritten(t *testing.T) {
	api := newBareTestAPI(t)
	id := finishedPeriod(t, api)
	got, err := api.LatestNote(id)
	if err != nil || got != (NoteView{}) {
		t.Fatalf("got %+v, %v; want an empty note", got, err)
	}
}

func TestSaveNoteKeepsEveryVersionAndShowsTheLatest(t *testing.T) {
	api := newBareTestAPI(t)
	id := finishedPeriod(t, api)
	now := at(6, 0, 0)
	if err := api.SaveNote(id, "First draft", "Dr Sari", now); err != nil {
		t.Fatal(err)
	}
	if err := api.SaveNote(id, "  Final note  ", "Dr Budi", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := api.LatestNote(id)
	if err != nil {
		t.Fatal(err)
	}
	// The second save is at 01:00 UTC on the 6th, which is 10:00 in GMT+9.
	if got.Note != "Final note" || got.WrittenBy != "Dr Budi" || got.WrittenAt != "2026-11-06 10:00" {
		t.Fatalf("latest = %+v", got)
	}
	if n := countNotes(t, api); n != 2 {
		t.Fatalf("%d rows; the first version must stay on record", n)
	}
}

func TestAnEmptyNoteClearsItButKeepsTheHistory(t *testing.T) {
	api := newBareTestAPI(t)
	id := finishedPeriod(t, api)
	_ = api.SaveNote(id, "Something", "Dr Sari", at(6, 0, 0))
	if err := api.SaveNote(id, "   ", "Dr Sari", at(6, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := api.LatestNote(id); got.Note != "" {
		t.Fatalf("note after clearing = %q", got.Note)
	}
	if n := countNotes(t, api); n != 2 {
		t.Fatalf("%d rows", n)
	}
}

func TestNoteLengthLimitCountsLettersNotBytes(t *testing.T) {
	api := newBareTestAPI(t)
	id := finishedPeriod(t, api)
	if err := api.SaveNote(id, strings.Repeat("é", maxNoteLetters), "Dr Sari", at(6, 0, 0)); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	if err := api.SaveNote(id, strings.Repeat("é", maxNoteLetters+1), "Dr Sari", at(6, 0, 0)); !errors.Is(err, ErrNoteTooLong) {
		t.Fatalf("one over the limit: %v", err)
	}
}

func TestNoteNeedsAPeriodThatHasOpened(t *testing.T) {
	api := newBareTestAPI(t)
	now := at(6, 0, 0)
	if err := api.SaveNote(999, "x", "Dr", now); !errors.Is(err, ErrWindowNotFound) {
		t.Errorf("unknown window: %v", err)
	}
	upcoming, _ := api.CreateWindow("", at(10, 0, 0), at(11, 0, 0), "A", at(1, 0, 0))
	if err := api.SaveNote(upcoming, "x", "Dr", now); !errors.Is(err, ErrPeriodNotStarted) {
		t.Errorf("upcoming: %v", err)
	}
	cancelled, _ := api.CreateWindow("", at(20, 0, 0), at(21, 0, 0), "A", at(1, 0, 0))
	_ = api.EndWindow(cancelled, "A", at(2, 0, 0))
	if err := api.SaveNote(cancelled, "x", "Dr", now); !errors.Is(err, ErrPeriodNotStarted) {
		t.Errorf("cancelled: %v", err)
	}
	open, _ := api.CreateWindow("", at(5, 0, 0), at(8, 0, 0), "A", at(1, 0, 0))
	if err := api.SaveNote(open, "a note while it is still open", "Dr", now); err != nil {
		t.Errorf("open period: %v", err)
	}
}

// addStaff creates an active staff member with this job title and returns the staff id.
func addStaff(t *testing.T, api *API, position string) int64 {
	t.Helper()
	sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", "001_sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(api.DB, sqlText); err != nil {
		t.Fatal(err)
	}
	res, err := api.DB.Exec(`INSERT INTO MedpostStaffTable (StaffName, Position) VALUES (?, ?)`, "Staff "+position, position)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func noteReq(method, windowID, body string, staffID int64, name string) *http.Request {
	req := httptest.NewRequest(method, "/api/sofi-windows/"+windowID+"/note", strings.NewReader(body))
	req.SetPathValue("id", windowID)
	sess := &auth.Session{StaffID: staffID, Name: name, Role: "staff"}
	return req.WithContext(middleware.ContextWithSession(context.Background(), sess))
}

func TestNoteAPIReadByAnyStaffWrittenOnlyByADoctorOrCMO(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(6, 0, 0))
	id := finishedPeriod(t, api)
	idStr := "1"
	if id != 1 {
		t.Fatalf("test assumes the first window has id 1, got %d", id)
	}
	doctor := addStaff(t, api, "Doctor")
	nurse := addStaff(t, api, "Nurse")

	put := func(staff int64, name, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.PutNote(rec, noteReq(http.MethodPut, idStr, body, staff, name))
		return rec
	}
	get := func(staff int64) (int, map[string]any) {
		rec := httptest.NewRecorder()
		api.GetNote(rec, noteReq(http.MethodGet, idStr, "", staff, "x"))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if rec := put(nurse, "Nurse Ani", `{"note":"I am not a doctor"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("nurse write: %d", rec.Code)
	}
	if n := countNotes(t, api); n != 0 {
		t.Fatalf("a forbidden write saved %d rows", n)
	}
	if rec := put(doctor, "Dr Sari", `{"note":"High fatigue clustered in PT Alpha."}`); rec.Code != http.StatusOK {
		t.Fatalf("doctor write: %d %s", rec.Code, rec.Body.String())
	}

	code, asNurse := get(nurse)
	if code != http.StatusOK || asNurse["note"] != "High fatigue clustered in PT Alpha." || asNurse["writtenBy"] != "Dr Sari" || asNurse["canWrite"] != false {
		t.Fatalf("nurse read: %d %v", code, asNurse)
	}
	if _, asDoctor := get(doctor); asDoctor["canWrite"] != true {
		t.Fatalf("doctor read: %v", asDoctor)
	}
}

func TestNoteAPIRejectsBadRequests(t *testing.T) {
	api := newBareTestAPI(t)
	clockAt(api, at(6, 0, 0))
	finishedPeriod(t, api)
	doctor := addStaff(t, api, "Chief Medical Officer")
	status := func(method, id, body string) int {
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			api.GetNote(rec, noteReq(method, id, body, doctor, "Dr"))
		} else {
			api.PutNote(rec, noteReq(method, id, body, doctor, "Dr"))
		}
		return rec.Code
	}
	tooLong, _ := json.Marshal(map[string]string{"note": strings.Repeat("a", maxNoteLetters+1)})
	for name, c := range map[string]struct {
		got, want int
	}{
		"get bad id":         {status(http.MethodGet, "abc", ""), http.StatusBadRequest},
		"get unknown":        {status(http.MethodGet, "99", ""), http.StatusNotFound},
		"put bad id":         {status(http.MethodPut, "abc", `{"note":"x"}`), http.StatusBadRequest},
		"put unknown":        {status(http.MethodPut, "99", `{"note":"x"}`), http.StatusNotFound},
		"put bad json":       {status(http.MethodPut, "1", `not json`), http.StatusBadRequest},
		"put too long":       {status(http.MethodPut, "1", string(tooLong)), http.StatusBadRequest},
		"put CMO is allowed": {status(http.MethodPut, "1", `{"note":"ok"}`), http.StatusOK},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, want %d", name, c.got, c.want)
		}
	}
}
