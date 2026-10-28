package sofi

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SiteZone is the time zone the pages show and the admin types in. GMT+9 has no daylight saving,
// so a fixed offset is exact all year; the database keeps UTC like every other timestamp.
var SiteZone = time.FixedZone("GMT+9", 9*60*60)

const (
	// siteInputLayout is what <input type="datetime-local"> sends.
	siteInputLayout   = "2006-01-02T15:04"
	siteDisplayLayout = "2006-01-02 15:04"
	maxLabelLetters   = 100

	reasonCancelled   = "cancelled"
	reasonClosedEarly = "closed_early"
)

// The state of a window as the admin sees it. "cancelled" and "closed_early" are the two ways an
// admin ends a window; the others follow from the clock.
const (
	StateUpcoming    = "upcoming"
	StateOpen        = "open"
	StatePast        = "past"
	StateCancelled   = "cancelled"
	StateClosedEarly = "closed_early"
)

var (
	ErrWindowOrder    = errors.New("The window must close after it opens.")
	ErrWindowPast     = errors.New("The window has already finished.")
	ErrWindowOverlap  = errors.New("It overlaps another window. Cancel that one first or pick other times.")
	ErrWindowNotFound = errors.New("No such window.")
	ErrWindowFinished = errors.New("That window has already finished or been ended.")
	ErrWindowLabel    = errors.New("The name is too long.")
)

// ParseSiteTime reads what the admin typed (yyyy-MM-ddTHH:mm, GMT+9) as an instant.
func ParseSiteTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(siteInputLayout, strings.TrimSpace(s), SiteZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("time must look like 2026-11-03T08:00: %w", err)
	}
	return t, nil
}

// FormatSiteTime shows an instant as yyyy-MM-dd HH:mm in GMT+9.
func FormatSiteTime(t time.Time) string { return t.In(SiteZone).Format(siteDisplayLayout) }

// siteDayFilter turns the From / To days typed on the results page (yyyy-MM-dd) into WHERE pieces
// on s.SubmittedAt. They are GMT+9 days, the zone the table shows, so "1 October" starts at
// 15:00 UTC on 30 September and "to 31 October" stops before 15:00 UTC on 31 October. Comparing
// the full stored timestamps (fixed-width UTC text) works the same on SQLite and MariaDB.
func siteDayFilter(from, to string) ([]string, []any, error) {
	var where []string
	var args []any
	for _, d := range []struct {
		name, value, op string
		plusDays        int
	}{{"from", from, ">=", 0}, {"to", to, "<", 1}} {
		if d.value == "" {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", d.value, SiteZone)
		if err != nil {
			return nil, nil, fmt.Errorf("%s must be yyyy-mm-dd", d.name)
		}
		where = append(where, "s.SubmittedAt "+d.op+" ?")
		args = append(args, day.AddDate(0, 0, d.plusDays).UTC().Format(timeLayoutDB))
	}
	return where, args, nil
}

// siteFromDB turns a stored UTC string into the GMT+9 text; "" stays "".
func siteFromDB(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(timeLayoutDB, s)
	if err != nil {
		return s
	}
	return FormatSiteTime(t)
}

// windowRow is a SofiWindow row as stored (UTC strings; "" for a NULL EndedAt).
type windowRow struct {
	ID          int64
	Label       string
	OpensAt     string
	ClosesAt    string
	EndedAt     string
	EndedReason string
	CreatedAt   string
	CreatedBy   string
	EndedBy     string
}

const windowColumns = `WindowID, Label, OpensAt, ClosesAt, COALESCE(EndedAt, ''), COALESCE(EndedReason, ''), CreatedAt, CreatedBy, EndedBy`

func scanWindow(sc interface{ Scan(...any) error }) (windowRow, error) {
	var w windowRow
	err := sc.Scan(&w.ID, &w.Label, &w.OpensAt, &w.ClosesAt, &w.EndedAt, &w.EndedReason, &w.CreatedAt, &w.CreatedBy, &w.EndedBy)
	return w, err
}

// EnsureWindowColumn adds SofiScreening.FK_WindowID to a SQLite database that was created before
// it. MariaDB gets it from migrations/mysql/028_sofi_window.sql, run by hand.
func EnsureWindowColumn(database *sql.DB) error {
	rows, err := database.Query(`PRAGMA table_info(SofiScreening)`)
	if err != nil {
		return fmt.Errorf("read SofiScreening columns: %w", err)
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		if name == "FK_WindowID" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if has {
		return nil
	}
	for _, q := range []string{
		`ALTER TABLE SofiScreening ADD COLUMN FK_WindowID INTEGER NULL REFERENCES SofiWindow(WindowID)`,
		`CREATE INDEX IF NOT EXISTS idx_sofi_window ON SofiScreening(FK_WindowID)`,
	} {
		if _, err := database.Exec(q); err != nil {
			return fmt.Errorf("add FK_WindowID: %w", err)
		}
	}
	return nil
}

// openWindow is the window that is open at now, or nil when /sofi is closed.
//
// Times are fixed-width UTC text (YYYY-MM-DDTHH:MM:SS), so comparing them as strings in SQL is
// the same as comparing the instants, on SQLite and MariaDB alike. A window an admin ended is
// open only until EndedAt; a cancelled one has EndedAt before OpensAt, so it never matches.
func (a *API) openWindow(now time.Time) (*windowRow, error) {
	n := now.UTC().Format(timeLayoutDB)
	w, err := scanWindow(a.DB.QueryRow(`
		SELECT `+windowColumns+` FROM SofiWindow
		WHERE OpensAt <= ? AND ClosesAt > ? AND (EndedAt IS NULL OR EndedAt > ?)
		ORDER BY OpensAt DESC, WindowID DESC LIMIT 1`, n, n, n))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find open window: %w", err)
	}
	return &w, nil
}

// nextWindow is the earliest window that has not opened yet and was not cancelled, or nil.
func (a *API) nextWindow(now time.Time) (*windowRow, error) {
	w, err := scanWindow(a.DB.QueryRow(`
		SELECT `+windowColumns+` FROM SofiWindow
		WHERE OpensAt > ? AND EndedAt IS NULL
		ORDER BY OpensAt ASC, WindowID ASC LIMIT 1`, now.UTC().Format(timeLayoutDB)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find next window: %w", err)
	}
	return &w, nil
}

// windowUsable reports whether someone who signed in under this window may still submit. It is
// true after the window has closed by itself (a person halfway through the form is not cut off;
// the sign-in cookie lasts two hours at most) and false once an admin ended it.
func (a *API) windowUsable(id int64) (bool, error) {
	var n int
	err := a.DB.QueryRow(`SELECT COUNT(*) FROM SofiWindow WHERE WindowID = ? AND EndedAt IS NULL`, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check window %d: %w", id, err)
	}
	return n > 0, nil
}

// CreateWindow schedules a window. by is the admin's name, kept for the audit line.
func (a *API) CreateWindow(label string, opens, closes time.Time, by string, now time.Time) (int64, error) {
	label = strings.TrimSpace(label)
	if len([]rune(label)) > maxLabelLetters {
		return 0, ErrWindowLabel
	}
	if !closes.After(opens) {
		return 0, ErrWindowOrder
	}
	if !closes.After(now) {
		return 0, ErrWindowPast
	}
	o, c := opens.UTC().Format(timeLayoutDB), closes.UTC().Format(timeLayoutDB)
	// Overlap = another live window starts before this one ends and ends after this one starts.
	// A cancelled window never counts (its slot is free again); a window closed early counts
	// only up to the moment it was closed.
	var clash int
	err := a.DB.QueryRow(`
		SELECT COUNT(*) FROM SofiWindow
		WHERE (EndedReason IS NULL OR EndedReason <> ?)
		  AND OpensAt < ? AND ClosesAt > ? AND (EndedAt IS NULL OR EndedAt > ?)`,
		reasonCancelled, c, o, o).Scan(&clash)
	if err != nil {
		return 0, fmt.Errorf("check overlap: %w", err)
	}
	if clash > 0 {
		return 0, ErrWindowOverlap
	}
	res, err := a.DB.Exec(`
		INSERT INTO SofiWindow (Label, OpensAt, ClosesAt, CreatedAt, CreatedBy) VALUES (?, ?, ?, ?, ?)`,
		label, o, c, now.UTC().Format(timeLayoutDB), by)
	if err != nil {
		return 0, fmt.Errorf("insert SofiWindow: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("window id: %w", err)
	}
	return id, nil
}

// EndWindow cancels a window that has not opened, or closes one that is open, right now. The row
// stays; only EndedAt, EndedReason and EndedBy are filled.
func (a *API) EndWindow(id int64, by string, now time.Time) error {
	w, err := scanWindow(a.DB.QueryRow(`SELECT `+windowColumns+` FROM SofiWindow WHERE WindowID = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWindowNotFound
	}
	if err != nil {
		return fmt.Errorf("read window %d: %w", id, err)
	}
	n := now.UTC().Format(timeLayoutDB)
	if w.EndedAt != "" || w.ClosesAt <= n {
		return ErrWindowFinished
	}
	reason := reasonClosedEarly
	if n < w.OpensAt {
		reason = reasonCancelled
	}
	// "AND EndedAt IS NULL" keeps two admins pressing the button together from both succeeding.
	res, err := a.DB.Exec(`UPDATE SofiWindow SET EndedAt = ?, EndedReason = ?, EndedBy = ? WHERE WindowID = ? AND EndedAt IS NULL`,
		n, reason, by, id)
	if err != nil {
		return fmt.Errorf("end window %d: %w", id, err)
	}
	if changed, err := res.RowsAffected(); err == nil && changed == 0 {
		return ErrWindowFinished
	}
	return nil
}

// WindowView is one window as the Settings page shows it. Times are GMT+9 text.
type WindowView struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	State     string `json:"state"`
	Opens     string `json:"opens"`
	Closes    string `json:"closes"`
	Ended     string `json:"ended"` // when an admin ended it, "" otherwise
	CreatedAt string `json:"createdAt"`
	CreatedBy string `json:"createdBy"`
	EndedBy   string `json:"endedBy"`
}

func stateOf(w windowRow, now string) string {
	switch {
	case w.EndedReason == reasonCancelled:
		return StateCancelled
	case w.EndedReason == reasonClosedEarly:
		return StateClosedEarly
	case now >= w.ClosesAt:
		return StatePast
	case now >= w.OpensAt:
		return StateOpen
	default:
		return StateUpcoming
	}
}

// ListWindows lists every window ever scheduled, the one opening latest first.
func (a *API) ListWindows(now time.Time) ([]WindowView, error) {
	rows, err := a.DB.Query(`SELECT ` + windowColumns + ` FROM SofiWindow ORDER BY OpensAt DESC, WindowID DESC`)
	if err != nil {
		return nil, fmt.Errorf("list windows: %w", err)
	}
	defer rows.Close()
	n := now.UTC().Format(timeLayoutDB)
	out := []WindowView{}
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan window: %w", err)
		}
		out = append(out, WindowView{
			ID: w.ID, Label: w.Label, State: stateOf(w, n),
			Opens: siteFromDB(w.OpensAt), Closes: siteFromDB(w.ClosesAt), Ended: siteFromDB(w.EndedAt),
			CreatedAt: siteFromDB(w.CreatedAt), CreatedBy: w.CreatedBy, EndedBy: w.EndedBy,
		})
	}
	return out, rows.Err()
}
