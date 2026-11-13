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

// Expiring-checkup reminders (plan section 8). Each company's active accounts are mailed one list of
// the company's workers whose checkup is in a reminder stage, once per worker and stage. It is separate from the clinic's own
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
	PatientID                           int64
	Name, Position, Company, ValidUntil string
	DaysLeft                            int    // negative once lapsed
	Stage                               string // one of the Stage* labels; "" when the worker is not in a reminder stage
}

// The reminder stages (owner, 2026-11-13): a worker is in the mail only at these points, nothing
// else. The window is the earliest stage, the grace period the last one.
const (
	reminderWindowDays = 60 // first stage; the mail ignores the Settings "within (days)" value
	StageExpired       = "Expired"
	StageTwoWeeks      = "Within 2 weeks"
	StageThirtyDays    = "Within 30 days"
	StageSixtyDays     = "Within 60 days"
)

// reminderStage names where a checkup stands: expired (less than 32 days ago, the caller filters
// the rest), within 14 days, within 30 days, within 60 days. Beyond 60 days there is no stage.
func reminderStage(daysLeft int) string {
	switch {
	case daysLeft < 0:
		return StageExpired
	case daysLeft <= 14:
		return StageTwoWeeks
	case daysLeft <= 30:
		return StageThirtyDays
	case daysLeft <= reminderWindowDays:
		return StageSixtyDays
	}
	return ""
}

// clinicToday is today's date in the clinic's zone, the same day ResultValidUntil is written in.
func (s *Store) clinicToday() time.Time { return s.now().In(dates.ClinicLocation) }

// lapsedGraceDays is how long after its end date a checkup is still in the reminder mail: expired
// less than 32 days ago (owner, 2026-11-13). Beyond that the worker is skipped.
const lapsedGraceDays = 31

// ExpiringForCompany lists the workers of one company (Patient.FK_CompanyID, the same people as the
// Employee checkups page) whose current checkup was fit and ends within the window or lapsed up to
// graceDays ago, soonest first. The rules match entrymcu's fetchExpiringSoon with activeOnly: a closed,
// archived, unfit or superseded record is not chased. A worker with a document already waiting for
// review (Pending) is skipped too: the renewal is on its way, so a reminder would be noise (owner,
// 2026-11-13). graceDays 0 is Home's list, which shows only what is still valid.
func (s *Store) ExpiringForCompany(ctx context.Context, companyID int64, windowDays, graceDays int) ([]Expiring, error) {
	if companyID == 0 {
		return nil, nil
	}
	today := s.clinicToday()
	from := today.AddDate(0, 0, -graceDays).Format("2006-01-02")
	until := today.AddDate(0, 0, windowDays).Format("2006-01-02")
	rows, err := s.DB.QueryContext(ctx,
		`SELECT p.PatientID, p.Name, COALESCE(MAX(m.WorkPosition), ''), COALESCE(MAX(co.Name), ''), MIN(m.ResultValidUntil)
		 FROM McuRecord m
		 JOIN Patient p ON p.PatientID = m.FK_PatientID
		 LEFT JOIN Company co ON co.CompanyID = p.FK_CompanyID
		 WHERE m.IsTerminal = 1 AND m.ResultCriteria IN ('Fit', 'FitWithNote') AND m.ResultValidUntil IS NOT NULL
		   AND m.ResultValidUntil >= ? AND m.ResultValidUntil <= ?
		   AND (m.CheckUpStatus IS NULL OR m.CheckUpStatus NOT IN ('Close', 'Archived / Exit'))
		   AND p.FK_CompanyID = ?
		   AND p.PatientID NOT IN (SELECT FK_PatientID FROM McuSubmission WHERE FK_PatientID IS NOT NULL AND Status = ?)
		 GROUP BY p.PatientID, p.Name
		 ORDER BY MIN(m.ResultValidUntil), p.Name`,
		from, until, companyID, SubPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expiring
	for rows.Next() {
		var e Expiring
		if err := rows.Scan(&e.PatientID, &e.Name, &e.Position, &e.Company, &e.ValidUntil); err != nil {
			return nil, err
		}
		if end, perr := time.Parse(dateOnlyLayout, e.ValidUntil); perr == nil {
			day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
			e.DaysLeft = int(end.Sub(day).Hours() / 24)
			e.Stage = reminderStage(e.DaysLeft)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// alreadyMailed drops the workers whose (checkup, stage) was mailed before: each worker is mailed once
// per stage, so a worker gets at most four mails per checkup. This replaces the old "do not mail the
// same person again within N days" rule, which could skip a stage.
func (s *Store) alreadyMailed(ctx context.Context, lines []Expiring) ([]Expiring, error) {
	var fresh []Expiring
	for _, l := range lines {
		var n int
		err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM McuReminderSent WHERE FK_PatientID = ? AND ValidUntil = ? AND Stage = ?`,
			l.PatientID, l.ValidUntil, l.Stage).Scan(&n)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			fresh = append(fresh, l)
		}
	}
	return fresh, nil
}

func (s *Store) markMailed(ctx context.Context, lines []Expiring) error {
	for _, l := range lines {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO McuReminderSent (FK_PatientID, ValidUntil, Stage, SentAt) VALUES (?, ?, ?, ?)`,
			l.PatientID, l.ValidUntil, l.Stage, s.stamp())
		if err != nil && !isUniqueViolation(err) {
			return err
		}
	}
	return nil
}

// reminderRecipient is one account that gets the company's mail.
type reminderRecipient struct {
	id    int64
	email string
}

// reminderTarget is one company due for a reminder: who is mailed and the workers listed.
type reminderTarget struct {
	company    int64
	recipients []reminderRecipient
	lines      []Expiring
}

// dueReminders finds what would be mailed now, for the daily job, Send now and the preview. The mail is
// per COMPANY (owner, 2026-11-13): every active account of the company that has reminders on, an
// address that is not bouncing and is not erased gets the same list, so the second account is not
// left out and a worker is not listed twice. Only workers not yet mailed at their current stage are
// in it.
func (s *Store) dueReminders(ctx context.Context) ([]reminderTarget, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT SubmitterID, Email, FK_CompanyID FROM Submitter
		 WHERE Status = ? AND IsBuiltIn = 0 AND RemindersEnabled = 1 AND EmailBouncedAt IS NULL AND ErasedAt IS NULL
		   AND FK_CompanyID IS NOT NULL
		 ORDER BY FK_CompanyID, SubmitterID`, StatusActive)
	if err != nil {
		return nil, err
	}
	byCompany := map[int64][]reminderRecipient{}
	var order []int64
	for rows.Next() {
		var r reminderRecipient
		var company int64
		if err := rows.Scan(&r.id, &r.email, &company); err != nil {
			rows.Close()
			return nil, err
		}
		if _, seen := byCompany[company]; !seen {
			order = append(order, company)
		}
		byCompany[company] = append(byCompany[company], r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var due []reminderTarget
	for _, company := range order {
		lines, err := s.ExpiringForCompany(ctx, company, reminderWindowDays, lapsedGraceDays)
		if err != nil {
			return nil, err
		}
		if lines, err = s.alreadyMailed(ctx, lines); err != nil {
			return nil, err
		}
		if len(lines) == 0 {
			continue
		}
		due = append(due, reminderTarget{company: company, recipients: byCompany[company], lines: lines})
	}
	return due, nil
}

// ReminderPreview says how many accounts and workers a send would reach right now, whether or not the
// master switch is on, so the admin can see the effect before turning it on.
func (s *Store) ReminderPreview(ctx context.Context) (submitters, workers int, err error) {
	due, err := s.dueReminders(ctx)
	for _, t := range due {
		submitters += len(t.recipients)
		workers += len(t.lines)
	}
	return submitters, workers, err
}

// RunReminders mails every company that is due and returns how many accounts and workers it reached.
// With the master switch off it does nothing. A company's workers are marked as mailed only after at
// least one of its accounts accepted the mail, so a failed send is tried again on the next run. The
// daily job and the admin's Send now button both call this, so they follow the same rules.
func (s *Store) RunReminders(ctx context.Context, m Mailer, portalURL string) (submitters, workers int, err error) {
	c, err := s.ReminderSettings(ctx)
	if err != nil || !c.Enabled {
		return 0, 0, err
	}
	due, err := s.dueReminders(ctx)
	if err != nil {
		return 0, 0, err
	}
	var failures []error
	for _, t := range due {
		accepted := 0
		for _, r := range t.recipients {
			if err := sendRich(ctx, m, r.email, "Medical checkups that expire soon", reminderBody(portalURL, t.lines), reminderHTML(portalURL, t.lines)); err != nil {
				log.Printf("mcu reminders: mail to submitter %d failed: %v", r.id, err)
				failures = append(failures, err)
				continue
			}
			accepted++
			if _, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET LastReminderAt = ? WHERE SubmitterID = ?`, s.stamp(), r.id); err != nil {
				failures = append(failures, err)
			}
			// Count only, never a name (plan section 3).
			_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmitter, EntityID: r.id,
				Action: "ReminderSent", Detail: fmt.Sprintf("%d workers", len(t.lines))})
		}
		if accepted == 0 {
			continue
		}
		if err := s.markMailed(ctx, t.lines); err != nil {
			failures = append(failures, err)
			continue
		}
		submitters += accepted
		workers += len(t.lines)
	}
	return submitters, workers, errors.Join(failures...)
}

// reminderHTML is reminderBody as one table, soonest expiry first (the order the lines arrive in).
func reminderHTML(portalURL string, lines []Expiring) string {
	rows := make([][]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, []string{l.Name, l.Position, l.Company, l.ValidUntil, l.Stage})
	}
	return htmlPage(
		htmlPara("These medical checkups of the workers of your company expire within 60 days, or expired less than a month ago:"),
		htmlTable([]string{"Employee", "Position", "Company", "Valid until", "Status"}, rows),
		htmlLink("Submit the renewed checkup here:", portalURL+basePath+"/batch"),
		htmlLink("To stop these mails, switch them off in Settings:", portalURL+basePath+"/settings"),
		htmlPara("This mail holds no ID numbers or birth dates on purpose."),
		htmlSignature(),
	)
}

func reminderBody(portalURL string, lines []Expiring) string {
	var b strings.Builder
	b.WriteString("These medical checkups of the workers of your company expire within 60 days, or expired less than a month ago:\n\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "- %s | %s | %s | valid until %s | %s\n", oneLine(l.Name), oneLine(l.Position), oneLine(l.Company), l.ValidUntil, l.Stage)
	}
	fmt.Fprintf(&b, "\nSubmit the renewed checkup here: %s/batch\n", portalURL+basePath)
	fmt.Fprintf(&b, "To stop these mails, switch them off in Settings: %s/settings\n", portalURL+basePath)
	b.WriteString("\nThis mail holds no ID numbers or birth dates on purpose.\n")
	b.WriteString(noticeSignature)
	return b.String()
}
