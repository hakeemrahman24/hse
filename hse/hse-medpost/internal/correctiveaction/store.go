package correctiveaction

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/display"
)

// inTx runs fn in one transaction: either every statement of a change lands (the row, its history line,
// the recomputed status) or none does. Inside fn use only tx, never s.DB: on SQLite a second connection
// would wait for the write lock this transaction already holds.
func (s *Store) inTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// logChange appends one history line. Field names are short snake_case words the page translates.
func logChange(tx *sql.Tx, actionID, staffID int64, field, oldValue, newValue string) error {
	_, err := tx.Exec(`
		INSERT INTO CorrectiveActionHistoryTable (FK_ActionID, FK_StaffID, ChangedAt, Field, OldValue, NewValue)
		VALUES (?, ?, ?, ?, ?, ?)`, actionID, staffID, dates.NowStamp(), field, oldValue, newValue)
	if err != nil {
		return fmt.Errorf("log corrective action change %q: %w", field, err)
	}
	return nil
}

// nullIfEmpty stores a blank optional date as SQL NULL, not as an empty string that would sort first.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// lookupPIC is how "only listed employees" is enforced: the page sends the picked employee's id, and the
// name and employee id that get stored are read from Patient here, so no typed or forged name can be saved.
func lookupPIC(q querier, patientID int64) (name, employeeID string, err error) {
	if patientID <= 0 {
		return "", "", ValidationError("PIC must be picked from the employee list")
	}
	err = q.QueryRow(`SELECT Name, COALESCE(EmployeeID, '') FROM Patient WHERE PatientID = ?`, patientID).Scan(&name, &employeeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ValidationError("PIC must be picked from the employee list")
	}
	if err != nil {
		return "", "", fmt.Errorf("look up employee %d: %w", patientID, err)
	}
	return name, employeeID, nil
}

// picLabel is how a PIC is written in the history: the name, and the employee id when there is one.
func picLabel(name, employeeID string) string {
	if employeeID == "" {
		return name
	}
	return name + " (" + employeeID + ")"
}

// programName returns the name of an Active program, or a ValidationError.
func programName(q querier, procID int64) (string, error) {
	var name string
	err := q.QueryRow(`SELECT HealthProcedure FROM HealthProcedureTable WHERE ProcID = ? AND Status = 'Active'`, procID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ValidationError("program not found")
	}
	if err != nil {
		return "", fmt.Errorf("look up program %d: %w", procID, err)
	}
	return display.ProgramTitle(name), nil
}

// checkActivity makes sure a linked inspection activity exists and belongs to the same program, so a
// finding cannot point at another program's activity.
func checkActivity(q querier, activityID, procID int64) error {
	if activityID == 0 {
		return nil
	}
	var activityProc int64
	err := q.QueryRow(`SELECT FK_ProcID FROM ActivityTable WHERE ActivityID = ?`, activityID).Scan(&activityProc)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && activityProc != procID) {
		return ValidationError("the linked activity must exist and belong to the same program")
	}
	if err != nil {
		return fmt.Errorf("look up activity %d: %w", activityID, err)
	}
	return nil
}

// Create adds a finding and returns its id.
func (s *Store) Create(in Input, staffID int64) (int64, error) {
	in, err := clean(in)
	if err != nil {
		return 0, err
	}
	if in.Status == "" {
		in.Status = StatusOpen
	}
	var id int64
	err = s.inTx(func(tx *sql.Tx) error {
		if _, err := programName(tx, in.ProcID); err != nil {
			return err
		}
		if err := checkActivity(tx, in.ActivityID, in.ProcID); err != nil {
			return err
		}
		picName, picCode, err := lookupPIC(tx, in.PicPatientID)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`
			INSERT INTO CorrectiveActionTable
			  (FK_ProcID, FK_ActivityID, InspectionDate, Location, Finding, Recommendation, PicName, PicEmployeeID,
			   DueDate, Severity, Status, ActionTaken, FK_CreatedBy, CreatedAt)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			in.ProcID, nullableID(in.ActivityID), in.InspectionDate, in.Location, in.Finding, in.Recommendation,
			picName, picCode, nullIfEmpty(in.DueDate), in.Severity, in.Status, staffID, dates.NowStamp())
		if err != nil {
			return fmt.Errorf("insert corrective action: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("read new corrective action id: %w", err)
		}
		return logChange(tx, id, staffID, "created", "", in.Finding)
	})
	return id, err
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// change is one field that differs between the saved finding and the submitted edit.
type change struct{ field, oldValue, newValue string }

// Update edits a finding. A blank PicPatientID keeps the saved PIC; a picked one replaces it. The status
// can be set to Open or In Progress only while the finding has no follow-ups; with follow-ups it is
// computed from them, and a closed finding must be reopened first.
func (s *Store) Update(id int64, in Input, staffID int64) error {
	in, err := clean(in)
	if err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		old, err := loadAction(tx, id)
		if err != nil {
			return err
		}
		var followUps int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionFollowUpTable WHERE FK_ActionID = ?`, id).Scan(&followUps); err != nil {
			return fmt.Errorf("count follow-ups: %w", err)
		}
		newStatus := old.Status
		switch {
		case old.Status == StatusClosed && in.Status != "" && in.Status != StatusClosed:
			return ValidationError("reopen the finding before changing its status")
		case old.Status != StatusClosed && followUps == 0 && in.Status != "":
			newStatus = in.Status
		}
		if old.Status == StatusClosed && old.ClosingDate != "" && in.InspectionDate > old.ClosingDate {
			return ValidationError("inspection date cannot be after the closing date")
		}

		newProgram := old.ProgramName
		if in.ProcID != old.ProcID {
			if newProgram, err = programName(tx, in.ProcID); err != nil {
				return err
			}
		}
		if in.ActivityID != old.ActivityID || in.ProcID != old.ProcID {
			if err := checkActivity(tx, in.ActivityID, in.ProcID); err != nil {
				return err
			}
		}
		picName, picCode := old.PicName, old.PicEmployeeID
		if in.PicPatientID != 0 {
			if picName, picCode, err = lookupPIC(tx, in.PicPatientID); err != nil {
				return err
			}
		}

		var changes []change
		diff := func(field, was, now string) {
			if was != now {
				changes = append(changes, change{field, was, now})
			}
		}
		diff("program", old.ProgramName, newProgram)
		diff("inspection_date", old.InspectionDate, in.InspectionDate)
		diff("location", old.Location, in.Location)
		diff("finding", old.Finding, in.Finding)
		diff("recommendation", old.Recommendation, in.Recommendation)
		diff("pic", picLabel(old.PicName, old.PicEmployeeID), picLabel(picName, picCode))
		diff("due_date", old.DueDate, in.DueDate)
		diff("severity", old.Severity, in.Severity)
		diff("status", old.Status, newStatus)
		diff("source_activity", idText(old.ActivityID), idText(in.ActivityID))

		if _, err := tx.Exec(`
			UPDATE CorrectiveActionTable
			SET FK_ProcID = ?, FK_ActivityID = ?, InspectionDate = ?, Location = ?, Finding = ?, Recommendation = ?,
			    PicName = ?, PicEmployeeID = ?, DueDate = ?, Severity = ?, Status = ?
			WHERE ActionID = ?`,
			in.ProcID, nullableID(in.ActivityID), in.InspectionDate, in.Location, in.Finding, in.Recommendation,
			picName, picCode, nullIfEmpty(in.DueDate), in.Severity, newStatus, id); err != nil {
			return fmt.Errorf("update corrective action %d: %w", id, err)
		}
		for _, c := range changes {
			if err := logChange(tx, id, staffID, c.field, c.oldValue, c.newValue); err != nil {
				return err
			}
		}
		return nil
	})
}

func idText(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// Close marks a finding closed. It needs action taken, a closing date (blank means today, not before the
// inspection), at least one After photo, and no open follow-up.
func (s *Store) Close(id int64, closingDate, actionTaken string, staffID int64) error {
	actionTaken = strings.TrimSpace(actionTaken)
	if actionTaken == "" {
		return ValidationError("action taken is required to close a finding")
	}
	if utf8.RuneCountInString(actionTaken) > maxLongText {
		return ValidationError("action taken must be 4000 characters or fewer")
	}
	closingDate, err := dates.Normalize(closingDate)
	if err != nil {
		return ValidationError("closing date: " + err.Error())
	}
	if closingDate == "" {
		closingDate = dates.Today()
	}
	return s.inTx(func(tx *sql.Tx) error {
		old, err := loadAction(tx, id)
		if err != nil {
			return err
		}
		if old.Status == StatusClosed {
			return ValidationError("this finding is already closed")
		}
		if closingDate < old.InspectionDate {
			return ValidationError("closing date cannot be before the inspection date")
		}
		var afterPhotos int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionPhotoTable WHERE FK_ActionID = ? AND Phase = 'After'`, id).Scan(&afterPhotos); err != nil {
			return fmt.Errorf("count after photos: %w", err)
		}
		if afterPhotos == 0 {
			return ValidationError("add at least one After photo before closing")
		}
		open, err := openFollowUps(tx, id)
		if err != nil {
			return err
		}
		if len(open) > 0 {
			return ValidationError("finish the open follow-ups first: " + strings.Join(open, "; "))
		}
		if _, err := tx.Exec(`
			UPDATE CorrectiveActionTable
			SET Status = 'Closed', ClosingDate = ?, ActionTaken = ?, FK_ClosedBy = ?, ClosedAt = ?
			WHERE ActionID = ?`, closingDate, actionTaken, staffID, dates.NowStamp(), id); err != nil {
			return fmt.Errorf("close corrective action %d: %w", id, err)
		}
		return logChange(tx, id, staffID, "closed", "", closingDate)
	})
}

// openFollowUps returns the descriptions of the follow-ups not yet done, for the "cannot close" message.
func openFollowUps(tx *sql.Tx, actionID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT Description FROM CorrectiveActionFollowUpTable WHERE FK_ActionID = ? AND Status = 'Open' ORDER BY FollowUpID`, actionID)
	if err != nil {
		return nil, fmt.Errorf("list open follow-ups: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan open follow-up: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Reopen undoes a close. The closing date and who closed it are cleared (the history keeps them); the
// status goes back to what the follow-ups say, or Open when there are none.
func (s *Store) Reopen(id int64, staffID int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		old, err := loadAction(tx, id)
		if err != nil {
			return err
		}
		if old.Status != StatusClosed {
			return ValidationError("only a closed finding can be reopened")
		}
		status, err := statusFromFollowUps(tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE CorrectiveActionTable
			SET Status = ?, ClosingDate = NULL, FK_ClosedBy = NULL, ClosedAt = NULL
			WHERE ActionID = ?`, status, id); err != nil {
			return fmt.Errorf("reopen corrective action %d: %w", id, err)
		}
		return logChange(tx, id, staffID, "reopened", old.ClosingDate, "")
	})
}

// Delete removes a finding with its photos, follow-ups and history, and returns the photo file paths so
// the caller can remove the files. Who may delete is decided by the API (admin only), not here.
func (s *Store) Delete(id int64) ([]string, error) {
	var paths []string
	err := s.inTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM CorrectiveActionTable WHERE ActionID = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("find corrective action %d: %w", id, err)
		}
		for _, table := range []string{"CorrectiveActionPhotoTable", "CorrectiveActionDocumentTable"} {
			found, err := fileLinks(tx, table, id)
			if err != nil {
				return err
			}
			paths = append(paths, found...)
		}
		// Children are deleted explicitly, in this order, rather than relying on ON DELETE CASCADE: SQLite
		// only enforces foreign keys when the connection has that pragma on, and a delete must not leave
		// orphans on either engine.
		for _, table := range []string{"CorrectiveActionHistoryTable", "CorrectiveActionFollowUpTable", "CorrectiveActionPhotoTable", "CorrectiveActionDocumentTable"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE FK_ActionID = ?`, id); err != nil {
				return fmt.Errorf("delete from %s: %w", table, err)
			}
		}
		if _, err := tx.Exec(`DELETE FROM CorrectiveActionTable WHERE ActionID = ?`, id); err != nil {
			return fmt.Errorf("delete corrective action %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// AddPhoto records a stored file as one of a finding's Before or After photos (at most three of each).
// The caller has already validated and compressed the file; this only records it. The count and the
// insert share a transaction, which stops two quick uploads from both seeing room for the third photo
// on SQLite; MariaDB would need a row lock to be fully race-free, which is not worth it at clinic scale.
func (s *Store) AddPhoto(actionID int64, phase, fileLink string, staffID int64) (int64, error) {
	if phase != PhaseBefore && phase != PhaseAfter {
		return 0, ValidationError("photo phase must be Before or After")
	}
	if strings.TrimSpace(fileLink) == "" {
		return 0, ValidationError("photo file is missing")
	}
	var id int64
	err := s.inTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM CorrectiveActionTable WHERE ActionID = ?`, actionID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("find corrective action %d: %w", actionID, err)
		}
		var count, maxOrder int
		if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(MAX(SortOrder), 0) FROM CorrectiveActionPhotoTable WHERE FK_ActionID = ? AND Phase = ?`, actionID, phase).Scan(&count, &maxOrder); err != nil {
			return fmt.Errorf("count photos: %w", err)
		}
		if count >= MaxPhotosPerPhase {
			return ValidationError(fmt.Sprintf("a finding can have at most %d %s photos", MaxPhotosPerPhase, phase))
		}
		res, err := tx.Exec(`
			INSERT INTO CorrectiveActionPhotoTable (FK_ActionID, Phase, FileLink, SortOrder, UploadedBy, UploadedAt)
			VALUES (?, ?, ?, ?, ?, ?)`, actionID, phase, fileLink, maxOrder+1, staffID, dates.NowStamp())
		if err != nil {
			return fmt.Errorf("insert photo: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("read new photo id: %w", err)
		}
		return logChange(tx, actionID, staffID, "photo_added", "", phase)
	})
	return id, err
}

// DeletePhoto removes one photo record and returns its file path for the caller to delete. The last After
// photo of a closed finding is protected, because closing required it.
func (s *Store) DeletePhoto(photoID, staffID int64) (string, error) {
	var link string
	err := s.inTx(func(tx *sql.Tx) error {
		var actionID int64
		var phase string
		err := tx.QueryRow(`SELECT FK_ActionID, Phase, FileLink FROM CorrectiveActionPhotoTable WHERE PhotoID = ?`, photoID).Scan(&actionID, &phase, &link)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find photo %d: %w", photoID, err)
		}
		if phase == PhaseAfter {
			var status string
			var afterCount int
			if err := tx.QueryRow(`SELECT Status FROM CorrectiveActionTable WHERE ActionID = ?`, actionID).Scan(&status); err != nil {
				return fmt.Errorf("read finding status: %w", err)
			}
			if err := tx.QueryRow(`SELECT COUNT(*) FROM CorrectiveActionPhotoTable WHERE FK_ActionID = ? AND Phase = 'After'`, actionID).Scan(&afterCount); err != nil {
				return fmt.Errorf("count after photos: %w", err)
			}
			if status == StatusClosed && afterCount <= 1 {
				return ValidationError("a closed finding needs its After photo; reopen it first")
			}
		}
		if _, err := tx.Exec(`DELETE FROM CorrectiveActionPhotoTable WHERE PhotoID = ?`, photoID); err != nil {
			return fmt.Errorf("delete photo %d: %w", photoID, err)
		}
		return logChange(tx, actionID, staffID, "photo_removed", phase, "")
	})
	if err != nil {
		return "", err
	}
	return link, nil
}

// fileLinks lists the stored file paths a finding has in one child table (photos or documents), so a
// delete can remove the files once the rows are gone. table is always one of our own constants.
func fileLinks(tx *sql.Tx, table string, actionID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT FileLink FROM `+table+` WHERE FK_ActionID = ?`, actionID)
	if err != nil {
		return nil, fmt.Errorf("list files in %s: %w", table, err)
	}
	defer rows.Close()
	var links []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan file path from %s: %w", table, err)
		}
		links = append(links, p)
	}
	return links, rows.Err()
}
