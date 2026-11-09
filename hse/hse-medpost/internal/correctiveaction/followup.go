package correctiveaction

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
)

// followUpRow is a follow-up as read inside a write transaction, with the facts about its finding that
// every follow-up rule needs.
type followUpRow struct {
	FollowUp
	findingStatus  string
	inspectionDate string
}

// loadFollowUp reads one follow-up and its finding's status and inspection date. ErrNotFound if missing.
func loadFollowUp(tx *sql.Tx, id int64) (followUpRow, error) {
	var r followUpRow
	err := tx.QueryRow(`
		SELECT f.FollowUpID, f.FK_ActionID, f.Description, f.PicName, f.PicEmployeeID, COALESCE(f.DueDate, ''),
		       f.Status, COALESCE(f.DoneDate, ''), a.Status, a.InspectionDate
		FROM CorrectiveActionFollowUpTable f
		JOIN CorrectiveActionTable a ON a.ActionID = f.FK_ActionID
		WHERE f.FollowUpID = ?`, id).Scan(&r.ID, &r.ActionID, &r.Description, &r.PicName, &r.PicEmployeeID, &r.DueDate,
		&r.FollowUp.Status, &r.DoneDate, &r.findingStatus, &r.inspectionDate)
	if errors.Is(err, sql.ErrNoRows) {
		return followUpRow{}, ErrNotFound
	}
	if err != nil {
		return followUpRow{}, fmt.Errorf("load follow-up %d: %w", id, err)
	}
	return r, nil
}

// cleanFollowUp trims and validates what needs no database lookup.
func cleanFollowUp(in FollowUpInput) (FollowUpInput, error) {
	in.Description = strings.TrimSpace(in.Description)
	if in.Description == "" {
		return in, ValidationError("follow-up description is required")
	}
	if utf8.RuneCountInString(in.Description) > maxLongText {
		return in, ValidationError("follow-up description must be 4000 characters or fewer")
	}
	var err error
	if in.DueDate, err = dates.Normalize(in.DueDate); err != nil {
		return in, ValidationError("due date: " + err.Error())
	}
	return in, nil
}

// statusFromFollowUps is the status a not-closed finding should have given its follow-ups: Open when none
// is done, In Progress when some are. A finding with no follow-ups is Open (its status is then typed by
// hand, and this is only used where a status must be chosen anyway, such as after a reopen).
func statusFromFollowUps(tx *sql.Tx, actionID int64) (string, error) {
	var total, done int
	err := tx.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN Status = 'Done' THEN 1 ELSE 0 END), 0)
		FROM CorrectiveActionFollowUpTable WHERE FK_ActionID = ?`, actionID).Scan(&total, &done)
	if err != nil {
		return "", fmt.Errorf("count follow-ups: %w", err)
	}
	if done > 0 {
		return StatusInProgress, nil
	}
	return StatusOpen, nil
}

// syncStatus recomputes a not-closed finding's status after a follow-up changed and logs the change. A
// closed finding is left alone, and so is a finding with no follow-ups left, whose status is typed by hand.
func syncStatus(tx *sql.Tx, actionID, staffID int64) error {
	var current string
	var total int
	if err := tx.QueryRow(`SELECT Status FROM CorrectiveActionTable WHERE ActionID = ?`, actionID).Scan(&current); err != nil {
		return fmt.Errorf("read finding status: %w", err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionFollowUpTable WHERE FK_ActionID = ?`, actionID).Scan(&total); err != nil {
		return fmt.Errorf("count follow-ups: %w", err)
	}
	if current == StatusClosed || total == 0 {
		return nil
	}
	want, err := statusFromFollowUps(tx, actionID)
	if err != nil || want == current {
		return err
	}
	if _, err := tx.Exec(`UPDATE CorrectiveActionTable SET Status = ? WHERE ActionID = ?`, want, actionID); err != nil {
		return fmt.Errorf("update finding status: %w", err)
	}
	return logChange(tx, actionID, staffID, "status", current, want)
}

// requireOpenFinding is the rule that a closed finding's follow-ups are locked until it is reopened.
func requireOpenFinding(status string) error {
	if status == StatusClosed {
		return ValidationError("this finding is closed; reopen it to change its follow-ups")
	}
	return nil
}

// AddFollowUp adds an action item under a finding, with its own PIC (picked from the employee list) and
// due date, and returns its id.
func (s *Store) AddFollowUp(actionID int64, in FollowUpInput, staffID int64) (int64, error) {
	in, err := cleanFollowUp(in)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.inTx(func(tx *sql.Tx) error {
		finding, err := loadAction(tx, actionID)
		if err != nil {
			return err
		}
		if err := requireOpenFinding(finding.Status); err != nil {
			return err
		}
		if in.DueDate != "" && in.DueDate < finding.InspectionDate {
			return ValidationError("due date cannot be before the inspection date")
		}
		picName, picCode, err := lookupPIC(tx, in.PicPatientID)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`
			INSERT INTO CorrectiveActionFollowUpTable
			  (FK_ActionID, Description, PicName, PicEmployeeID, DueDate, Status, ActionTaken, FK_CreatedBy, CreatedAt)
			VALUES (?, ?, ?, ?, ?, 'Open', '', ?, ?)`,
			actionID, in.Description, picName, picCode, nullIfEmpty(in.DueDate), staffID, dates.NowStamp())
		if err != nil {
			return fmt.Errorf("insert follow-up: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("read new follow-up id: %w", err)
		}
		if err := logChange(tx, actionID, staffID, "follow_up_added", "", in.Description); err != nil {
			return err
		}
		return syncStatus(tx, actionID, staffID)
	})
	return id, err
}

// UpdateFollowUp edits a follow-up's description, PIC and due date. A blank PicPatientID keeps the saved PIC.
func (s *Store) UpdateFollowUp(id int64, in FollowUpInput, staffID int64) error {
	in, err := cleanFollowUp(in)
	if err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		fu, err := loadFollowUp(tx, id)
		if err != nil {
			return err
		}
		if err := requireOpenFinding(fu.findingStatus); err != nil {
			return err
		}
		if in.DueDate != "" && in.DueDate < fu.inspectionDate {
			return ValidationError("due date cannot be before the inspection date")
		}
		picName, picCode := fu.PicName, fu.PicEmployeeID
		if in.PicPatientID != 0 {
			if picName, picCode, err = lookupPIC(tx, in.PicPatientID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			UPDATE CorrectiveActionFollowUpTable SET Description = ?, PicName = ?, PicEmployeeID = ?, DueDate = ?
			WHERE FollowUpID = ?`, in.Description, picName, picCode, nullIfEmpty(in.DueDate), id); err != nil {
			return fmt.Errorf("update follow-up %d: %w", id, err)
		}
		for _, c := range []change{
			{"follow_up_description", fu.Description, in.Description},
			{"follow_up_pic", picLabel(fu.PicName, fu.PicEmployeeID), picLabel(picName, picCode)},
			{"follow_up_due_date", fu.DueDate, in.DueDate},
		} {
			if c.oldValue == c.newValue {
				continue
			}
			if err := logChange(tx, fu.ActionID, staffID, c.field, c.oldValue, c.newValue); err != nil {
				return err
			}
		}
		return nil
	})
}

// CompleteFollowUp marks a follow-up Done. It needs action taken and a done date (blank means today, not
// before the inspection).
func (s *Store) CompleteFollowUp(id int64, doneDate, actionTaken string, staffID int64) error {
	actionTaken = strings.TrimSpace(actionTaken)
	if actionTaken == "" {
		return ValidationError("action taken is required to mark a follow-up done")
	}
	if utf8.RuneCountInString(actionTaken) > maxLongText {
		return ValidationError("action taken must be 4000 characters or fewer")
	}
	doneDate, err := dates.Normalize(doneDate)
	if err != nil {
		return ValidationError("done date: " + err.Error())
	}
	if doneDate == "" {
		doneDate = dates.Today()
	}
	return s.inTx(func(tx *sql.Tx) error {
		fu, err := loadFollowUp(tx, id)
		if err != nil {
			return err
		}
		if err := requireOpenFinding(fu.findingStatus); err != nil {
			return err
		}
		if fu.FollowUp.Status == FollowUpDone {
			return ValidationError("this follow-up is already done")
		}
		if doneDate < fu.inspectionDate {
			return ValidationError("done date cannot be before the inspection date")
		}
		if _, err := tx.Exec(`
			UPDATE CorrectiveActionFollowUpTable
			SET Status = 'Done', DoneDate = ?, ActionTaken = ?, FK_DoneBy = ?, DoneAt = ?
			WHERE FollowUpID = ?`, doneDate, actionTaken, staffID, dates.NowStamp(), id); err != nil {
			return fmt.Errorf("complete follow-up %d: %w", id, err)
		}
		if err := logChange(tx, fu.ActionID, staffID, "follow_up_done", fu.Description, doneDate); err != nil {
			return err
		}
		return syncStatus(tx, fu.ActionID, staffID)
	})
}

// ReopenFollowUp sets a Done follow-up back to Open while its finding is not closed. The done date and who
// finished it are cleared; the history keeps them.
func (s *Store) ReopenFollowUp(id, staffID int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		fu, err := loadFollowUp(tx, id)
		if err != nil {
			return err
		}
		if err := requireOpenFinding(fu.findingStatus); err != nil {
			return err
		}
		if fu.FollowUp.Status != FollowUpDone {
			return ValidationError("this follow-up is already open")
		}
		if _, err := tx.Exec(`
			UPDATE CorrectiveActionFollowUpTable
			SET Status = 'Open', DoneDate = NULL, FK_DoneBy = NULL, DoneAt = NULL
			WHERE FollowUpID = ?`, id); err != nil {
			return fmt.Errorf("reopen follow-up %d: %w", id, err)
		}
		if err := logChange(tx, fu.ActionID, staffID, "follow_up_reopened", fu.DoneDate, fu.Description); err != nil {
			return err
		}
		return syncStatus(tx, fu.ActionID, staffID)
	})
}

// DeleteFollowUp removes a follow-up (for example one added by mistake) and recomputes the finding's status.
func (s *Store) DeleteFollowUp(id, staffID int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		fu, err := loadFollowUp(tx, id)
		if err != nil {
			return err
		}
		if err := requireOpenFinding(fu.findingStatus); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM CorrectiveActionFollowUpTable WHERE FollowUpID = ?`, id); err != nil {
			return fmt.Errorf("delete follow-up %d: %w", id, err)
		}
		if err := logChange(tx, fu.ActionID, staffID, "follow_up_removed", fu.Description, ""); err != nil {
			return err
		}
		return syncStatus(tx, fu.ActionID, staffID)
	})
}
