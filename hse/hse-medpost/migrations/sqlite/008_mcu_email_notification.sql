-- Admin-configurable SMTP account + recipients + schedule for the
-- near-expiry MCU certificate warning email (ports the spirit of the legacy
-- sendLngValidityWarnings*/installLngMonthlyWarningTrigger subsystem, but
-- as a settings-driven periodic check rather than a fixed monthly trigger).
-- Always exactly one row (McuEmailNotificationConfigID = 1), same
-- single-row-config convention already used elsewhere in this project family.
CREATE TABLE IF NOT EXISTS McuEmailNotificationConfig (
    McuEmailNotificationConfigID INTEGER PRIMARY KEY AUTOINCREMENT,
    Enabled              INTEGER NOT NULL DEFAULT 0,
    SmtpHost              TEXT NULL,
    SmtpPort              INTEGER NOT NULL DEFAULT 587,
    SmtpUsername          TEXT NULL,
    SmtpPassword          TEXT NULL,
    FromAddress           TEXT NULL,
    ToAddresses           TEXT NULL,   -- comma-separated
    CcAddresses           TEXT NULL,   -- comma-separated
    ExpiryThresholdDays   INTEGER NOT NULL DEFAULT 60,   -- "2 months or less"
    CheckPeriodMinutes    INTEGER NOT NULL DEFAULT 1440, -- how often the scheduler may resend
    LastSentAt            TEXT NULL,
    LastSendStatus        TEXT NULL
);

-- The default row is inserted by Go (EnsureEmailNotificationConfigSeed in
-- internal/entrymcu/seed.go), same convention as this project's other
-- EnsureXSeed functions, since a plain migration-file INSERT isn't safe to
-- rerun (Migrate() re-executes every migration file on every startup, with
-- no per-migration "already applied" tracking).
