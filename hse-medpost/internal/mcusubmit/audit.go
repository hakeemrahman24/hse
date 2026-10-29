package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
)

// Who did something, and what it was done to.
const (
	ActorSubmitter = "Submitter"
	ActorStaff     = "Staff"
	ActorSystem    = "System"

	EntitySubmitter  = "Submitter"
	EntityBatch      = "Batch"
	EntitySubmission = "Submission"
	EntitySystem     = "System" // jobs that belong to no one row (EntityID is 0), such as the reviewer digest

	maxActionLen = 30
	maxDetailLen = 255
)

// Event is one row of the append-only audit trail. Detail is a short reason or signature name and
// must never hold a worker's name, NIK or birth date (plan section 3).
type Event struct {
	At         string
	ActorType  string
	ActorID    int64 // 0 for System
	EntityType string
	EntityID   int64
	Action     string
	Detail     string
	IpHash     string
}

// LogEvent appends one event. There is deliberately no update or delete: the trail is the one
// place that must survive a withdrawn, expired or erased row.
func (s *Store) LogEvent(ctx context.Context, e Event) error {
	switch e.ActorType {
	case ActorSubmitter, ActorStaff, ActorSystem:
	default:
		return errors.New("mcusubmit: unknown actor type")
	}
	switch e.EntityType {
	case EntitySubmitter, EntityBatch, EntitySubmission, EntitySystem:
	default:
		return errors.New("mcusubmit: unknown entity type")
	}
	if e.Action == "" || len(e.Action) > maxActionLen {
		return errors.New("mcusubmit: action must be 1 to 30 characters")
	}
	if len(e.Detail) > maxDetailLen {
		return errors.New("mcusubmit: detail is too long")
	}
	var actor sql.NullInt64
	if e.ActorID != 0 {
		actor = sql.NullInt64{Int64: e.ActorID, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO McuSubmissionEvent (At, ActorType, ActorID, EntityType, EntityID, Action, Detail, IpHash)
		 VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		s.stamp(), e.ActorType, actor, e.EntityType, e.EntityID, e.Action, e.Detail, e.IpHash)
	return err
}

// Events returns the trail of one entity, oldest first.
func (s *Store) Events(ctx context.Context, entityType string, entityID int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT At, ActorType, COALESCE(ActorID, 0), EntityType, EntityID, Action, COALESCE(Detail, ''), COALESCE(IpHash, '')
		 FROM McuSubmissionEvent WHERE EntityType = ? AND EntityID = ? ORDER BY EventID`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.At, &e.ActorType, &e.ActorID, &e.EntityType, &e.EntityID, &e.Action, &e.Detail, &e.IpHash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Settings are the admin's expiring-checkup reminder switches (plan section 8).
type Settings struct {
	RemindersEnabled bool
	DaysBefore       int // remind when a checkup expires within this many days
	RepeatDays       int // do not mail the same submitter again within this many days
}

const maxSettingDays = 365

// Settings reads the single settings row.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	var enabled int
	err := s.DB.QueryRowContext(ctx,
		`SELECT RemindersEnabled, ReminderDaysBefore, ReminderRepeatDays FROM McuPortalSetting WHERE McuPortalSettingID = 1`).
		Scan(&enabled, &out.DaysBefore, &out.RepeatDays)
	out.RemindersEnabled = enabled == 1
	return out, err
}

// SaveSettings validates and stores the settings. Day counts must be 1 to 365: 0 would remind
// about nothing, and a huge number would mail everyone about everything.
func (s *Store) SaveSettings(ctx context.Context, in Settings, byAdminID int64) error {
	if in.DaysBefore < 1 || in.DaysBefore > maxSettingDays {
		return errors.New("days before expiry must be between 1 and 365")
	}
	if in.RepeatDays < 1 || in.RepeatDays > maxSettingDays {
		return errors.New("days between reminders must be between 1 and 365")
	}
	enabled := 0
	if in.RemindersEnabled {
		enabled = 1
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE McuPortalSetting SET RemindersEnabled = ?, ReminderDaysBefore = ?, ReminderRepeatDays = ?,
		 UpdatedAt = ?, FK_UpdatedBySubmitterID = ? WHERE McuPortalSettingID = 1`,
		enabled, in.DaysBefore, in.RepeatDays, s.stamp(), byAdminID)
	return err
}
