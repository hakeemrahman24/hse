// Near-expiry email notifier — ports the legacy
// sendLngValidityWarningsIfEndOfMonth()/installLngMonthlyWarningTrigger()
// subsystem (Code.js ~4927+) as a fixed monthly trigger again: first Monday
// of the month, 09:00 WIB/GMT+7 (see firstMondayNineAM below), rather than
// the admin-configurable elapsed-period check this used to be.
// Reuses fetchExpiringSoon (report.go) with activeOnly=true so
// Close/Archived-Exit records — patients who already left or were archived
// — are never chased for a renewal.
package entrymcu

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

type EmailNotificationConfig struct {
	Enabled             bool   `json:"enabled"`
	SmtpHost            string `json:"smtpHost"`
	SmtpPort            int    `json:"smtpPort"`
	SmtpUsername        string `json:"smtpUsername"`
	SmtpPasswordSet     bool   `json:"smtpPasswordSet"`
	FromAddress         string `json:"fromAddress"`
	ToAddresses         string `json:"toAddresses"`
	CcAddresses         string `json:"ccAddresses"`
	ExpiryThresholdDays int    `json:"expiryThresholdDays"`
	// CheckPeriodMinutes is vestigial: RunEmailNotificationScheduler no
	// longer reads it (the send moment is now the fixed first-Monday-09:00-
	// WIB rule — see firstMondayNineAM). Kept in the struct/column only so
	// existing rows and the API shape don't need a migration; still
	// accepted/stored on write, just unused.
	CheckPeriodMinutes int    `json:"checkPeriodMinutes"`
	LastSentAt         string `json:"lastSentAt"`
	LastSendStatus     string `json:"lastSendStatus"`
}

// emailNotificationConfigRow is the internal, password-carrying shape used
// for sending; EmailNotificationConfig (above) is the API-facing shape that
// never echoes the stored password back to the browser.
type emailNotificationConfigRow struct {
	EmailNotificationConfig
	SmtpPassword string
}

func (a *API) loadEmailNotificationConfig() (emailNotificationConfigRow, error) {
	var row emailNotificationConfigRow
	var enabled int
	var smtpHost, smtpUsername, smtpPassword, fromAddress, toAddresses, ccAddresses sql.NullString
	var lastSentAt, lastSendStatus sql.NullString
	err := a.DB.QueryRow(`
		SELECT Enabled, SmtpHost, SmtpPort, SmtpUsername, SmtpPassword, FromAddress,
		       ToAddresses, CcAddresses, ExpiryThresholdDays, CheckPeriodMinutes,
		       LastSentAt, LastSendStatus
		FROM McuEmailNotificationConfig WHERE McuEmailNotificationConfigID = 1`,
	).Scan(&enabled, &smtpHost, &row.SmtpPort, &smtpUsername, &smtpPassword,
		&fromAddress, &toAddresses, &ccAddresses, &row.ExpiryThresholdDays,
		&row.CheckPeriodMinutes, &lastSentAt, &lastSendStatus)
	if err != nil {
		return row, err
	}
	row.Enabled = enabled != 0
	row.SmtpHost = smtpHost.String
	row.SmtpUsername = smtpUsername.String
	row.SmtpPassword = smtpPassword.String
	row.SmtpPasswordSet = smtpPassword.String != ""
	row.FromAddress = fromAddress.String
	row.ToAddresses = toAddresses.String
	row.CcAddresses = ccAddresses.String
	row.LastSentAt = lastSentAt.String
	row.LastSendStatus = lastSendStatus.String
	return row, nil
}

// GetEmailNotificationConfig — RequireAdmin (see main.go). Never returns the
// stored SMTP password; SmtpPasswordSet tells the settings UI whether one is
// already on file so it can show "leave blank to keep existing".
func (a *API) GetEmailNotificationConfig(w http.ResponseWriter, r *http.Request) {
	row, err := a.loadEmailNotificationConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, row.EmailNotificationConfig)
}

type emailNotificationConfigInput struct {
	Enabled             bool   `json:"enabled"`
	SmtpHost            string `json:"smtpHost"`
	SmtpPort            int    `json:"smtpPort"`
	SmtpUsername        string `json:"smtpUsername"`
	SmtpPassword        string `json:"smtpPassword"`
	FromAddress         string `json:"fromAddress"`
	ToAddresses         string `json:"toAddresses"`
	CcAddresses         string `json:"ccAddresses"`
	ExpiryThresholdDays int    `json:"expiryThresholdDays"`
	CheckPeriodMinutes  int    `json:"checkPeriodMinutes"`
}

// UpdateEmailNotificationConfig — RequireAdmin. An empty SmtpPassword in the
// request keeps whatever password is already stored, so the settings form
// never has to round-trip the real secret back through the browser.
func (a *API) UpdateEmailNotificationConfig(w http.ResponseWriter, r *http.Request) {
	var in emailNotificationConfigInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if in.SmtpPort <= 0 {
		in.SmtpPort = 587
	}
	if in.ExpiryThresholdDays <= 0 {
		in.ExpiryThresholdDays = 60
	}
	if in.CheckPeriodMinutes <= 0 {
		in.CheckPeriodMinutes = 1440
	}

	if in.SmtpPassword == "" {
		_, err := a.DB.Exec(`
			UPDATE McuEmailNotificationConfig SET
				Enabled = ?, SmtpHost = ?, SmtpPort = ?, SmtpUsername = ?, FromAddress = ?,
				ToAddresses = ?, CcAddresses = ?, ExpiryThresholdDays = ?, CheckPeriodMinutes = ?
			WHERE McuEmailNotificationConfigID = 1`,
			boolToInt(in.Enabled), nullIfEmptyMcu(in.SmtpHost), in.SmtpPort, nullIfEmptyMcu(in.SmtpUsername),
			nullIfEmptyMcu(in.FromAddress), nullIfEmptyMcu(in.ToAddresses), nullIfEmptyMcu(in.CcAddresses),
			in.ExpiryThresholdDays, in.CheckPeriodMinutes,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	} else {
		_, err := a.DB.Exec(`
			UPDATE McuEmailNotificationConfig SET
				Enabled = ?, SmtpHost = ?, SmtpPort = ?, SmtpUsername = ?, SmtpPassword = ?, FromAddress = ?,
				ToAddresses = ?, CcAddresses = ?, ExpiryThresholdDays = ?, CheckPeriodMinutes = ?
			WHERE McuEmailNotificationConfigID = 1`,
			boolToInt(in.Enabled), nullIfEmptyMcu(in.SmtpHost), in.SmtpPort, nullIfEmptyMcu(in.SmtpUsername),
			in.SmtpPassword, nullIfEmptyMcu(in.FromAddress), nullIfEmptyMcu(in.ToAddresses), nullIfEmptyMcu(in.CcAddresses),
			in.ExpiryThresholdDays, in.CheckPeriodMinutes,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}

	row, err := a.loadEmailNotificationConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, row.EmailNotificationConfig)
}

// SendNowEmailNotification — RequireAdmin: a manual "send now" button in
// Settings so an admin can verify the SMTP account works without waiting for
// the scheduler.
func (a *API) SendNowEmailNotification(w http.ResponseWriter, r *http.Request) {
	status, err := a.runEmailNotificationOnce(true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmptyMcu(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func splitAddresses(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// runEmailNotificationOnce loads the config, finds near-expiry records
// (excluding Close/Archived-Exit), and sends the warning email. force=true
// (the manual "send now" button) sends even with zero matching records, so
// an admin can confirm the SMTP account works; the scheduled tick only
// sends when there's something to report.
func (a *API) runEmailNotificationOnce(force bool) (string, error) {
	cfg, err := a.loadEmailNotificationConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if !cfg.Enabled {
		return "disabled", nil
	}
	if cfg.SmtpHost == "" || cfg.FromAddress == "" || cfg.ToAddresses == "" {
		return "", fmt.Errorf("SMTP host, from address, and at least one To address are required")
	}

	expiring, err := a.fetchExpiringSoon(cfg.ExpiryThresholdDays, true)
	if err != nil {
		return "", fmt.Errorf("query expiring records: %w", err)
	}
	if len(expiring) == 0 && !force {
		a.recordEmailSendResult("skipped: no records within threshold")
		return "skipped: no records within threshold", nil
	}

	to := splitAddresses(cfg.ToAddresses)
	cc := splitAddresses(cfg.CcAddresses)
	if len(to) == 0 {
		return "", fmt.Errorf("no valid To address configured")
	}

	subject := fmt.Sprintf("MCU fitness certificates expiring within %d days (%d patient(s))", cfg.ExpiryThresholdDays, len(expiring))
	var body strings.Builder
	fmt.Fprintf(&body, "The following active MCU records expire within %d days. Archived/closed records are excluded.\n\n", cfg.ExpiryThresholdDays)
	fmt.Fprintf(&body, "%-14s %-28s %-24s %-12s %s\n", "CheckUp ID", "Patient", "Company", "Valid until", "Days left")
	for _, e := range expiring {
		fmt.Fprintf(&body, "%-14s %-28s %-24s %-12s %d\n", e.CheckUpID, e.PatientName, e.CompanyName, e.ResultValidUntil, e.DaysLeft)
	}
	if len(expiring) == 0 {
		body.WriteString("(no records currently within the threshold — this is a manual test send)\n")
	}

	if err := sendSMTPMail(cfg.SmtpHost, cfg.SmtpPort, cfg.SmtpUsername, cfg.SmtpPassword, cfg.FromAddress, to, cc, subject, body.String()); err != nil {
		a.recordEmailSendResult("error: " + err.Error())
		return "", err
	}

	status := fmt.Sprintf("ok: sent for %d record(s)", len(expiring))
	a.recordEmailSendResult(status)
	return status, nil
}

func (a *API) recordEmailSendResult(status string) {
	// Stored in UTC (unlike the old server-local-but-unlabeled format) so
	// RunEmailNotificationScheduler's monthly-trigger comparison below is
	// exact regardless of what timezone the host OS is set to.
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, _ = a.DB.Exec(`UPDATE McuEmailNotificationConfig SET LastSentAt = ?, LastSendStatus = ? WHERE McuEmailNotificationConfigID = 1`, now, status)
}

func sendSMTPMail(host string, port int, username, password, from string, to, cc []string, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	headers := map[string]string{
		"From":    from,
		"To":      strings.Join(to, ", "),
		"Subject": subject,
	}
	if len(cc) > 0 {
		headers["Cc"] = strings.Join(cc, ", ")
	}
	var msg strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)

	rcpts := append(append([]string{}, to...), cc...)
	return smtp.SendMail(addr, auth, from, rcpts, []byte(msg.String()))
}

// jakartaLocation is GMT+7 (WIB) — the email is a monthly reminder for a
// site team on Indonesia time, not whatever timezone the server host
// happens to be set to. Falls back to a fixed +7 offset if the platform has
// no IANA tzdata installed (e.g. a minimal container image).
var jakartaLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}()

// firstMondayNineAM returns 09:00 WIB on the first Monday of the month that
// contains t (t is first converted to WIB, since "first Monday of the
// month" only means one thing at a time in a specific timezone).
func firstMondayNineAM(t time.Time) time.Time {
	t = t.In(jakartaLocation)
	firstOfMonth := time.Date(t.Year(), t.Month(), 1, 9, 0, 0, 0, jakartaLocation)
	daysUntilMonday := (int(time.Monday) - int(firstOfMonth.Weekday()) + 7) % 7
	return firstOfMonth.AddDate(0, 0, daysUntilMonday)
}

// RunEmailNotificationScheduler is started as a background goroutine from
// main.go. It wakes up once a minute — cheap — and sends only once the
// current month's trigger moment (first Monday, 09:00 WIB/GMT+7) has
// arrived and hasn't already been sent for. Comparing against LastSentAt
// (rather than firing exactly at the minute) means a send that was missed
// because the process was down at 09:00 still goes out on the next tick
// after restart, instead of silently skipping the whole month.
func (a *API) RunEmailNotificationScheduler(tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for range ticker.C {
		cfg, err := a.loadEmailNotificationConfig()
		if err != nil || !cfg.Enabled {
			continue
		}
		now := time.Now().UTC()
		trigger := firstMondayNineAM(now).UTC()
		if now.Before(trigger) {
			continue // this month's trigger hasn't arrived yet
		}
		if cfg.LastSentAt != "" {
			if last, err := time.Parse("2006-01-02T15:04:05", cfg.LastSentAt); err == nil && !last.Before(trigger) {
				continue // already sent for this month's trigger
			}
		}
		_, _ = a.runEmailNotificationOnce(false)
	}
}
