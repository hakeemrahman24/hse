package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"hse-medpost/internal/dates"
)

// Expiring-checkup reminders (plan section 8). Each active submitter is mailed one list of the
// workers THEY submitted whose current checkup lapses soon. It is separate from the clinic's own
// monthly reminder in internal/entrymcu/emailnotify.go, which has its own settings and recipients.

// ErrBadReminderSetting means a number outside what the settings page allows.
var ErrBadReminderSetting = errors.New("mcusubmit: reminder days must be between 1 and 365")

const maxReminderDays = 365

// ReminderConfig is the single McuPortalSetting row.
type ReminderConfig struct {
	Enabled    bool // master switch; off until the admin turns it on
	DaysBefore int  // remind when a checkup lapses within this many days
	RepeatDays int  // do not mail the same submitter again within this many days
}

// ReminderSettings reads the settings, with the plan's defaults if the row is missing.
func (s *Store) ReminderSettings(ctx context.Context) (ReminderConfig, error) {
	var c ReminderConfig
	var on int
	err := s.DB.QueryRowContext(ctx,
		`SELECT RemindersEnabled, ReminderDaysBefore, ReminderRepeatDays FROM McuPortalSetting WHERE McuPortalSettingID = 1`).
		Scan(&on, &c.DaysBefore, &c.RepeatDays)
	if errors.Is(err, sql.ErrNoRows) {
		return ReminderConfig{DaysBefore: 60, RepeatDays: 30}, nil
	}
	c.Enabled = on == 1
	return c, err
}

// SaveReminderSettings stores the settings. adminID is who changed them (0 = nobody, for tests).
func (s *Store) SaveReminderSettings(ctx context.Context, c ReminderConfig, adminID int64) error {
	if c.DaysBefore < 1 || c.DaysBefore > maxReminderDays || c.RepeatDays < 1 || c.RepeatDays > maxReminderDays {
		return ErrBadReminderSetting
	}
	on := 0
	if c.Enabled {
		on = 1
	}
	var by any
	if adminID != 0 {
		by = adminID
	}
	return s.oneRow(ctx,
		`UPDATE McuPortalSetting SET RemindersEnabled = ?, ReminderDaysBefore = ?, ReminderRepeatDays = ?,
		   UpdatedAt = ?, FK_UpdatedBySubmitterID = ? WHERE McuPortalSettingID = 1`,
		on, c.DaysBefore, c.RepeatDays, s.stamp(), by)
}

// Expiring is one worker whose checkup is about to lapse. It carries no ID number or birth date: it
// goes into a mail.
type Expiring struct {
	Name, Position, Company, ValidUntil string
}

// clinicToday is today's date in the clinic's zone, the same day ResultValidUntil is written in.
func (s *Store) clinicToday() time.Time { return s.now().In(dates.ClinicLocation) }

// lapsedGraceDays is how long after its end date a checkup is still in the reminder mail: expired
// less than 32 days ago (owner, 2026-11-13). Beyond that the worker is skipped.
const lapsedGraceDays = 31

// ExpiringFor lists the submitter's own workers (patients on one of their sent, not withdrawn or
// rejected documents) whose current checkup was fit and lapses within the window, soonest first.
// The rules match entrymcu's fetchExpiringSoon with activeOnly: a closed, archived, unfit or
// superseded record is not chased. Only the submitter's own workers are listed, so one company's
// HR never sees another company's staff.
//
// graceDays also keeps a checkup that lapsed up to that many days ago (owner, 2026-11-13): the mail
// still chases a worker who has just expired, but stops once they are well past it, so it does not
// nag about people who have left or are long overdue. Home passes 0 and lists only what is still valid.
func (s *Store) ExpiringFor(ctx context.Context, submitterID int64, windowDays, graceDays int) ([]Expiring, error) {
	today := s.clinicToday()
	from := today.AddDate(0, 0, -graceDays).Format("2006-01-02")
	until := today.AddDate(0, 0, windowDays).Format("2006-01-02")
	rows, err := s.DB.QueryContext(ctx,
		`SELECT p.Name, COALESCE(MAX(m.WorkPosition), ''), COALESCE(MAX(co.Name), ''), MIN(m.ResultValidUntil)
		 FROM McuRecord m
		 JOIN Patient p ON p.PatientID = m.FK_PatientID
		 LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
		 WHERE m.IsTerminal = 1 AND m.ResultCriteria IN ('Fit', 'FitWithNote') AND m.ResultValidUntil IS NOT NULL
		   AND m.ResultValidUntil >= ? AND m.ResultValidUntil <= ?
		   AND (m.CheckUpStatus IS NULL OR m.CheckUpStatus NOT IN ('Close', 'Archived / Exit'))
		   AND m.FK_PatientID IN (SELECT FK_PatientID FROM McuSubmission
		        WHERE FK_SubmitterID = ? AND FK_PatientID IS NOT NULL AND Status IN (?, ?, ?))
		 GROUP BY p.PatientID, p.Name
		 ORDER BY MIN(m.ResultValidUntil), p.Name`,
		from, until, submitterID, SubPending, SubReviewed, SubClosed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expiring
	for rows.Next() {
		var e Expiring
		if err := rows.Scan(&e.Name, &e.Position, &e.Company, &e.ValidUntil); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// reminderTarget is one submitter due for a reminder.
type reminderTarget struct {
	id    int64
	email string
	lines []Expiring
}

// dueReminders finds who would be mailed now under the same rules for the daily job, Send now and
// the preview: active, not the built-in admin, reminders on, address not bounced, not erased, and
// not mailed within the repeat period.
func (s *Store) dueReminders(ctx context.Context, c ReminderConfig) ([]reminderTarget, error) {
	notAfter := stampOf(s.now().AddDate(0, 0, -c.RepeatDays))
	rows, err := s.DB.QueryContext(ctx,
		`SELECT SubmitterID, Email FROM Submitter
		 WHERE Status = ? AND IsBuiltIn = 0 AND RemindersEnabled = 1 AND EmailBouncedAt IS NULL AND ErasedAt IS NULL
		   AND (LastReminderAt IS NULL OR LastReminderAt <= ?)
		 ORDER BY SubmitterID`, StatusActive, notAfter)
	if err != nil {
		return nil, err
	}
	var all []reminderTarget
	for rows.Next() {
		var t reminderTarget
		if err := rows.Scan(&t.id, &t.email); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var due []reminderTarget
	for _, t := range all {
		lines, err := s.ExpiringFor(ctx, t.id, c.DaysBefore, lapsedGraceDays)
		if err != nil {
			return nil, err
		}
		if len(lines) == 0 {
			continue
		}
		t.lines = lines
		due = append(due, t)
	}
	return due, nil
}

// ReminderPreview says how many submitters and workers a send would reach right now, whether or
// not the master switch is on, so the admin can see the effect before turning it on.
func (s *Store) ReminderPreview(ctx context.Context) (submitters, workers int, err error) {
	c, err := s.ReminderSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	due, err := s.dueReminders(ctx, c)
	for _, t := range due {
		workers += len(t.lines)
	}
	return len(due), workers, err
}

// RunReminders mails everyone who is due and returns how many submitters and workers it reached.
// With the master switch off it does nothing. LastReminderAt moves only after a mail was accepted,
// so a failed send is tried again on the next run. The daily job and the admin's Send now button
// both call this, so they follow the same rules.
func (s *Store) RunReminders(ctx context.Context, m Mailer, portalURL string) (submitters, workers int, err error) {
	c, err := s.ReminderSettings(ctx)
	if err != nil || !c.Enabled {
		return 0, 0, err
	}
	due, err := s.dueReminders(ctx, c)
	if err != nil {
		return 0, 0, err
	}
	var failures []error
	for _, t := range due {
		if err := sendRich(ctx, m, t.email, "Medical checkups that expire soon", reminderBody(portalURL, t.lines), reminderHTML(portalURL, t.lines)); err != nil {
			log.Printf("mcu reminders: mail to submitter %d failed: %v", t.id, err)
			failures = append(failures, err)
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET LastReminderAt = ? WHERE SubmitterID = ?`, s.stamp(), t.id); err != nil {
			failures = append(failures, err)
		}
		// Count only, never a name (plan section 3).
		_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmitter, EntityID: t.id,
			Action: "ReminderSent", Detail: fmt.Sprintf("%d workers", len(t.lines))})
		submitters++
		workers += len(t.lines)
	}
	return submitters, workers, errors.Join(failures...)
}

// reminderHTML is reminderBody as one table, soonest expiry first (the order the lines arrive in).
func reminderHTML(portalURL string, lines []Expiring) string {
	rows := make([][]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, []string{l.Name, l.Position, l.Company, l.ValidUntil})
	}
	return htmlPage(
		htmlPara("These medical checkups of the workers you submitted expire soon, or expired less than a month ago:"),
		htmlTable([]string{"Employee", "Position", "Company", "Valid until"}, rows),
		htmlLink("Submit the renewed checkup here:", portalURL+basePath+"/batch"),
		htmlLink("To stop these mails, switch them off in Settings:", portalURL+basePath+"/settings"),
		htmlPara("This mail holds no ID numbers or birth dates on purpose."),
		htmlSignature(),
	)
}

func reminderBody(portalURL string, lines []Expiring) string {
	var b strings.Builder
	b.WriteString("These medical checkups of the workers you submitted expire soon, or expired less than a month ago:\n\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "- %s | %s | %s | valid until %s\n", oneLine(l.Name), oneLine(l.Position), oneLine(l.Company), l.ValidUntil)
	}
	fmt.Fprintf(&b, "\nSubmit the renewed checkup here: %s/batch\n", portalURL+basePath)
	fmt.Fprintf(&b, "To stop these mails, switch them off in Settings: %s/settings\n", portalURL+basePath)
	b.WriteString("\nThis mail holds no ID numbers or birth dates on purpose.\n")
	b.WriteString(noticeSignature)
	return b.String()
}
