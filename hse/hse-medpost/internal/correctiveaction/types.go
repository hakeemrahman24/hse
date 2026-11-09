// Package correctiveaction tracks findings from inspections across every health program: what was
// found, who must fix it, when it was closed, and before/after photos. See
// plan/corrective-action-plan.md for the decisions behind it.
package correctiveaction

import (
	"errors"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
)

// ErrNotFound is returned when a finding or photo id does not exist.
var ErrNotFound = errors.New("corrective action not found")

// ValidationError is a problem with what the user submitted. The API turns it into a 400 with this
// text; any other error is a server fault and becomes a 500 whose detail stays in the log.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

const (
	// MaxPhotosPerPhase is the plan's "up to 3" before photos and 3 after photos per finding.
	MaxPhotosPerPhase = 3

	StatusOpen       = "Open"
	StatusInProgress = "In Progress"
	StatusClosed     = "Closed"

	// StatusOverdue is a filter value only. Overdue is computed (not closed and past the due date), never
	// stored, so it cannot drift out of date when the calendar moves on.
	StatusOverdue = "Overdue"

	FollowUpOpen = "Open"
	FollowUpDone = "Done"

	PhaseBefore = "Before"
	PhaseAfter  = "After"

	maxShortText = 255
	maxLongText  = 4000
	// defaultListLimit and maxListLimit keep one request from reading the whole table on a Pi.
	defaultListLimit = 500
	maxListLimit     = 5000
)

var severities = map[string]bool{"Minor": true, "Major": true, "Critical": true}

// Input is what a user submits to create or edit a finding.
type Input struct {
	ProcID         int64
	ActivityID     int64 // 0 = not linked to an inspection activity
	InspectionDate string
	Location       string
	Finding        string
	Recommendation string
	// PicPatientID is only a lookup key for the employee picked in the dropdown. The store copies that
	// employee's name and employee id into the finding as text and never stores this id. 0 on an edit
	// means "keep the PIC as saved".
	PicPatientID int64
	DueDate      string
	Severity     string
	Status       string // Open or In Progress; blank keeps the current one (Open on create)
}

// Photo is one stored picture. FileLink is the path on disk and is never sent to the browser; the API
// builds a URL from the id.
type Photo struct {
	ID         int64  `json:"id"`
	Phase      string `json:"phase"`
	FileLink   string `json:"-"`
	SortOrder  int    `json:"sortOrder"`
	UploadedAt string `json:"uploadedAt"`
	URL        string `json:"url,omitempty"`
}

// Action is a finding with its photos and the computed fields.
type Action struct {
	ID          int64  `json:"id"`
	ProcID      int64  `json:"procId"`
	ProgramName string `json:"programName"` // the friendly title people see ("Drug And Alcohol Testing")
	// ProgramKey is the program's stored name ("DrugAndAlcoholTesting"). It names the photo folder, so a
	// finding's photos sit beside the program's other evidence, and is never sent to the browser.
	ProgramKey string `json:"-"`
	ActivityID int64  `json:"activityId,omitempty"`
	// ActivityTitle and ActivityDate describe the linked activity (read only, blank when not linked).
	ActivityTitle  string `json:"activityTitle,omitempty"`
	ActivityDate   string `json:"activityDate,omitempty"`
	InspectionDate string `json:"inspectionDate"`
	Location       string `json:"location"`
	Finding        string `json:"finding"`
	Recommendation string `json:"recommendation"`
	PicName        string `json:"picName"`
	PicEmployeeID  string `json:"picEmployeeId"`
	DueDate        string `json:"dueDate"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
	ClosingDate    string `json:"closingDate"`
	ActionTaken    string `json:"actionTaken"`
	CreatedByName  string `json:"createdByName"`
	CreatedAt      string `json:"createdAt"`
	ClosedByName   string `json:"closedByName"`
	ClosedAt       string `json:"closedAt"`
	DaysOpen       int    `json:"daysOpen"`
	// IsOverdue is true when the finding, or any of its open follow-ups, is past its due date.
	IsOverdue    bool       `json:"isOverdue"`
	BeforePhotos []Photo    `json:"beforePhotos"`
	AfterPhotos  []Photo    `json:"afterPhotos"`
	FollowUps    []FollowUp `json:"followUps"`
	// FollowUpsDone / FollowUpsTotal drive the "2 of 3 follow-ups done" label in lists and Excel.
	FollowUpsDone  int `json:"followUpsDone"`
	FollowUpsTotal int `json:"followUpsTotal"`
	// NextFollowUpDue is the earliest due date among open follow-ups ("" when none has one).
	NextFollowUpDue string `json:"nextFollowUpDue"`
}

// FollowUp is one action item under a finding, with its own PIC and due date.
type FollowUp struct {
	ID            int64  `json:"id"`
	ActionID      int64  `json:"actionId"`
	Description   string `json:"description"`
	PicName       string `json:"picName"`
	PicEmployeeID string `json:"picEmployeeId"`
	DueDate       string `json:"dueDate"`
	Status        string `json:"status"`
	DoneDate      string `json:"doneDate"`
	ActionTaken   string `json:"actionTaken"`
	DoneByName    string `json:"doneByName"`
	CreatedAt     string `json:"createdAt"`
	IsOverdue     bool   `json:"isOverdue"`
}

// FollowUpInput is what a user submits to add or edit a follow-up. PicPatientID has the same meaning as
// on Input: a lookup key only, 0 on an edit keeps the PIC as saved.
type FollowUpInput struct {
	Description  string
	PicPatientID int64
	DueDate      string
}

// HistoryEntry is one change to a finding: who, when, which field, old and new value.
type HistoryEntry struct {
	ID        int64  `json:"id"`
	Field     string `json:"field"`
	OldValue  string `json:"oldValue"`
	NewValue  string `json:"newValue"`
	ChangedAt string `json:"changedAt"`
	StaffName string `json:"staffName"`
}

// Employee is one row of the PIC dropdown. Only fields needed to tell people apart: no date of birth,
// blood type or personal id, which the Patient table also holds.
type Employee struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	EmployeeID string `json:"employeeId"`
	Company    string `json:"company"`
	Position   string `json:"position"`
}

// Filter narrows List. Zero values mean "no filter".
type Filter struct {
	ProcID   int64
	Status   string // Open, In Progress, Closed or Overdue
	Severity string
	Q        string // substring of finding, recommendation, location or PIC
	From, To string // inspection date range, inclusive
	Limit    int
}

// clean trims and validates the parts of Input that need no database, and fills the defaults. Rules
// that need a lookup (program, activity, employee) are checked by the caller.
func clean(in Input) (Input, error) {
	in.Location = strings.TrimSpace(in.Location)
	in.Finding = strings.TrimSpace(in.Finding)
	in.Recommendation = strings.TrimSpace(in.Recommendation)
	in.Severity = strings.TrimSpace(in.Severity)
	in.Status = strings.TrimSpace(in.Status)

	if in.Finding == "" {
		return in, ValidationError("finding is required")
	}
	if in.Recommendation == "" {
		return in, ValidationError("recommendation is required")
	}
	if utf8.RuneCountInString(in.Finding) > maxLongText || utf8.RuneCountInString(in.Recommendation) > maxLongText {
		return in, ValidationError("finding and recommendation must be 4000 characters or fewer")
	}
	if utf8.RuneCountInString(in.Location) > maxShortText {
		return in, ValidationError("location must be 255 characters or fewer")
	}
	var err error
	if in.InspectionDate, err = dates.Require(in.InspectionDate); err != nil {
		return in, ValidationError("inspection date: " + err.Error())
	}
	if in.DueDate, err = dates.Normalize(in.DueDate); err != nil {
		return in, ValidationError("due date: " + err.Error())
	}
	// ISO days sort correctly as text, so a string compare is a date compare.
	if in.DueDate != "" && in.DueDate < in.InspectionDate {
		return in, ValidationError("due date cannot be before the inspection date")
	}
	if in.Severity == "" {
		in.Severity = "Minor"
	}
	if !severities[in.Severity] {
		return in, ValidationError("severity must be Minor, Major or Critical")
	}
	if in.Status != "" && in.Status != StatusOpen && in.Status != StatusInProgress {
		return in, ValidationError("status can be Open or In Progress; use Close to close a finding")
	}
	return in, nil
}
