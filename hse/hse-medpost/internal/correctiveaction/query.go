package correctiveaction

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/db"
	"hse-medpost/internal/display"
)

// Store reads and writes corrective actions. DB is the app's one connection pool (SQLite locally,
// MariaDB in production), so every statement here is plain SQL valid on both.
type Store struct {
	DB *sql.DB
}

// querier is what *sql.DB and *sql.Tx share, so a read can run inside a write's transaction and see
// the rows that transaction has just changed.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

const selectAction = `
	SELECT a.ActionID, a.FK_ProcID, p.HealthProcedure, COALESCE(a.FK_ActivityID, 0),
	       COALESCE(act.ActivityTitle, ''), COALESCE(act.ActivityDate, ''),
	       a.InspectionDate, a.Location, a.Finding, a.Recommendation, a.PicName, a.PicEmployeeID,
	       COALESCE(a.DueDate, ''), a.Severity, a.Status, COALESCE(a.ClosingDate, ''), a.ActionTaken,
	       COALESCE(cs.StaffName, ''), a.CreatedAt, COALESCE(xs.StaffName, ''), COALESCE(a.ClosedAt, '')
	FROM CorrectiveActionTable a
	JOIN HealthProcedureTable p ON p.ProcID = a.FK_ProcID
	LEFT JOIN ActivityTable act ON act.ActivityID = a.FK_ActivityID
	LEFT JOIN MedpostStaffTable cs ON cs.StaffID = a.FK_CreatedBy
	LEFT JOIN MedpostStaffTable xs ON xs.StaffID = a.FK_ClosedBy`

func scanAction(row interface{ Scan(...any) error }) (Action, error) {
	var a Action
	err := row.Scan(&a.ID, &a.ProcID, &a.ProgramName, &a.ActivityID, &a.ActivityTitle, &a.ActivityDate,
		&a.InspectionDate, &a.Location, &a.Finding, &a.Recommendation, &a.PicName, &a.PicEmployeeID,
		&a.DueDate, &a.Severity, &a.Status, &a.ClosingDate, &a.ActionTaken,
		&a.CreatedByName, &a.CreatedAt, &a.ClosedByName, &a.ClosedAt)
	a.ProgramKey = a.ProgramName
	a.ProgramName = display.ProgramTitle(a.ProgramKey)
	// Empty slices, not nil, so the JSON is [] and the page never has to null-check.
	a.BeforePhotos, a.AfterPhotos, a.FollowUps, a.Documents = []Photo{}, []Photo{}, []FollowUp{}, []Document{}
	return a, err
}

// loadAction reads one finding without its photos or follow-ups. ErrNotFound when the id does not exist.
func loadAction(q querier, id int64) (Action, error) {
	a, err := scanAction(q.QueryRow(selectAction+` WHERE a.ActionID = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, fmt.Errorf("load corrective action %d: %w", id, err)
	}
	return a, nil
}

// fill sets the computed fields. "Today" comes from dates.Today (the clinic's calendar day), never the
// raw server clock, which runs in UTC and is a day behind the clinic for the first nine hours. It runs
// after the follow-ups are attached, because the finding is overdue if any open follow-up is.
func (a *Action) fill(today string) {
	a.IsOverdue = a.Status != StatusClosed && a.DueDate != "" && a.DueDate < today
	end := today
	if a.Status == StatusClosed && a.ClosingDate != "" {
		end = a.ClosingDate
	}
	if days, err := dates.DaysBetween(a.InspectionDate, end); err == nil && days > 0 {
		a.DaysOpen = days
	}
	a.FollowUpsTotal = len(a.FollowUps)
	for i := range a.FollowUps {
		fu := &a.FollowUps[i]
		fu.IsOverdue = fu.Status != FollowUpDone && fu.DueDate != "" && fu.DueDate < today
		if fu.Status == FollowUpDone {
			a.FollowUpsDone++
			continue
		}
		if fu.IsOverdue && a.Status != StatusClosed {
			a.IsOverdue = true
		}
		if fu.DueDate != "" && (a.NextFollowUpDue == "" || fu.DueDate < a.NextFollowUpDue) {
			a.NextFollowUpDue = fu.DueDate
		}
	}
}

// attachPhotos loads the photos of all given findings in one query (not one per finding).
func attachPhotos(q querier, actions []Action) error {
	if len(actions) == 0 {
		return nil
	}
	index, args, marks := indexActions(actions)
	rows, err := q.Query(`
		SELECT PhotoID, FK_ActionID, Phase, FileLink, SortOrder, UploadedAt
		FROM CorrectiveActionPhotoTable
		WHERE FK_ActionID IN (`+marks+`)
		ORDER BY SortOrder, PhotoID`, args...)
	if err != nil {
		return fmt.Errorf("load corrective action photos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Photo
		var actionID int64
		if err := rows.Scan(&p.ID, &actionID, &p.Phase, &p.FileLink, &p.SortOrder, &p.UploadedAt); err != nil {
			return fmt.Errorf("scan corrective action photo: %w", err)
		}
		i := index[actionID]
		if p.Phase == PhaseAfter {
			actions[i].AfterPhotos = append(actions[i].AfterPhotos, p)
		} else {
			actions[i].BeforePhotos = append(actions[i].BeforePhotos, p)
		}
	}
	return rows.Err()
}

// attachFollowUps loads the follow-ups of all given findings in one query.
func attachFollowUps(q querier, actions []Action) error {
	if len(actions) == 0 {
		return nil
	}
	index, args, marks := indexActions(actions)
	rows, err := q.Query(`
		SELECT f.FollowUpID, f.FK_ActionID, f.Description, f.PicName, f.PicEmployeeID,
		       COALESCE(f.DueDate, ''), f.Status, COALESCE(f.DoneDate, ''), f.ActionTaken,
		       COALESCE(s.StaffName, ''), f.CreatedAt
		FROM CorrectiveActionFollowUpTable f
		LEFT JOIN MedpostStaffTable s ON s.StaffID = f.FK_DoneBy
		WHERE f.FK_ActionID IN (`+marks+`)
		ORDER BY f.FollowUpID`, args...)
	if err != nil {
		return fmt.Errorf("load corrective action follow-ups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var fu FollowUp
		if err := rows.Scan(&fu.ID, &fu.ActionID, &fu.Description, &fu.PicName, &fu.PicEmployeeID,
			&fu.DueDate, &fu.Status, &fu.DoneDate, &fu.ActionTaken, &fu.DoneByName, &fu.CreatedAt); err != nil {
			return fmt.Errorf("scan corrective action follow-up: %w", err)
		}
		i := index[fu.ActionID]
		actions[i].FollowUps = append(actions[i].FollowUps, fu)
	}
	return rows.Err()
}

// indexActions maps finding id to its slice position and builds the IN (...) placeholders.
func indexActions(actions []Action) (map[int64]int, []any, string) {
	index := make(map[int64]int, len(actions))
	args := make([]any, len(actions))
	marks := make([]string, len(actions))
	for i, a := range actions {
		index[a.ID] = i
		args[i] = a.ID
		marks[i] = "?"
	}
	return index, args, strings.Join(marks, ",")
}

// complete attaches photos and follow-ups, then fills the computed fields.
func complete(q querier, actions []Action) error {
	if err := attachPhotos(q, actions); err != nil {
		return err
	}
	if err := attachFollowUps(q, actions); err != nil {
		return err
	}
	if err := attachDocuments(q, actions); err != nil {
		return err
	}
	today := dates.Today()
	for i := range actions {
		actions[i].fill(today)
	}
	return nil
}

// Get returns one finding with its photos, follow-ups and computed fields.
func (s *Store) Get(id int64) (Action, error) {
	a, err := loadAction(s.DB, id)
	if err != nil {
		return Action{}, err
	}
	list := []Action{a}
	if err := complete(s.DB, list); err != nil {
		return Action{}, err
	}
	return list[0], nil
}

// List returns findings newest inspection first, with photos, follow-ups and computed fields.
func (s *Store) List(f Filter) ([]Action, error) {
	today := dates.Today()
	where, args := []string{"1 = 1"}, []any{}
	if f.ProcID != 0 {
		where = append(where, "a.FK_ProcID = ?")
		args = append(args, f.ProcID)
	}
	switch status := strings.TrimSpace(f.Status); status {
	case "":
	case StatusOverdue:
		// A finding is overdue when its own due date has passed, or when any open follow-up's has.
		where = append(where, `a.Status <> 'Closed' AND (
			(a.DueDate IS NOT NULL AND a.DueDate < ?)
			OR EXISTS (SELECT 1 FROM CorrectiveActionFollowUpTable f
			           WHERE f.FK_ActionID = a.ActionID AND f.Status = 'Open'
			             AND f.DueDate IS NOT NULL AND f.DueDate < ?))`)
		args = append(args, today, today)
	default:
		where = append(where, "a.Status = ?")
		args = append(args, status)
	}
	if f.Severity != "" {
		where = append(where, "a.Severity = ?")
		args = append(args, f.Severity)
	}
	if from, err := dates.Normalize(f.From); err != nil {
		return nil, ValidationError("from date: " + err.Error())
	} else if from != "" {
		where = append(where, "a.InspectionDate >= ?")
		args = append(args, from)
	}
	if to, err := dates.Normalize(f.To); err != nil {
		return nil, ValidationError("to date: " + err.Error())
	} else if to != "" {
		where = append(where, "a.InspectionDate <= ?")
		args = append(args, to)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		// A % or _ typed by the user only widens the match, which is harmless, so no ESCAPE clause is
		// used: backslash escapes behave differently on SQLite and MariaDB (see CLAUDE.md).
		like := "%" + q + "%"
		where = append(where, "(a.Finding LIKE ? OR a.Recommendation LIKE ? OR a.Location LIKE ? OR a.PicName LIKE ?)")
		args = append(args, like, like, like, like)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	args = append(args, limit)

	rows, err := s.DB.Query(selectAction+` WHERE `+strings.Join(where, " AND ")+
		` ORDER BY a.InspectionDate DESC, a.ActionID DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list corrective actions: %w", err)
	}
	defer rows.Close()
	out := []Action{}
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan corrective action: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list corrective actions: %w", err)
	}
	// Close before the next queries: a SQLite connection serves one open result set at a time here.
	rows.Close()
	if err := complete(s.DB, out); err != nil {
		return nil, err
	}
	return out, nil
}

// PhotoPath returns the stored file path of one photo, for the handler that serves it.
func (s *Store) PhotoPath(photoID int64) (string, error) {
	var link string
	err := s.DB.QueryRow(`SELECT FileLink FROM CorrectiveActionPhotoTable WHERE PhotoID = ?`, photoID).Scan(&link)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up photo %d: %w", photoID, err)
	}
	return link, nil
}

// History returns every change to a finding, oldest first.
func (s *Store) History(actionID int64) ([]HistoryEntry, error) {
	rows, err := s.DB.Query(`
		SELECT h.HistoryID, h.Field, h.OldValue, h.NewValue, h.ChangedAt, COALESCE(st.StaffName, '')
		FROM CorrectiveActionHistoryTable h
		LEFT JOIN MedpostStaffTable st ON st.StaffID = h.FK_StaffID
		WHERE h.FK_ActionID = ?
		ORDER BY h.HistoryID`, actionID)
	if err != nil {
		return nil, fmt.Errorf("load corrective action history: %w", err)
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		if err := rows.Scan(&h.ID, &h.Field, &h.OldValue, &h.NewValue, &h.ChangedAt, &h.StaffName); err != nil {
			return nil, fmt.Errorf("scan corrective action history: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

const (
	minEmployeeQuery = 2
	maxEmployeeQuery = 100
	maxEmployeeHits  = 25
)

// SearchEmployees feeds the PIC dropdown: employees whose name or employee id contains q. A query
// shorter than two characters returns nothing, so the box never dumps the whole employee list.
func (s *Store) SearchEmployees(q string, limit int) ([]Employee, error) {
	q = strings.TrimSpace(q)
	out := []Employee{}
	if utf8.RuneCountInString(q) < minEmployeeQuery {
		return out, nil
	}
	if r := []rune(q); len(r) > maxEmployeeQuery {
		q = string(r[:maxEmployeeQuery])
	}
	if limit <= 0 || limit > maxEmployeeHits {
		limit = maxEmployeeHits
	}
	rows, err := s.DB.Query(`
		SELECT p.PatientID, p.Name, COALESCE(p.EmployeeID, ''), COALESCE(c.Name, ''), COALESCE(p.WorkPosition, '')
		FROM Patient p
		LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		WHERE `+db.NameLikeSQL("p.Name")+` OR p.EmployeeID LIKE ?
		ORDER BY p.Name
		LIMIT ?`, db.NameLikeArg(q), "%"+q+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("search employees: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Employee
		if err := rows.Scan(&e.ID, &e.Name, &e.EmployeeID, &e.Company, &e.Position); err != nil {
			return nil, fmt.Errorf("scan employee: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
