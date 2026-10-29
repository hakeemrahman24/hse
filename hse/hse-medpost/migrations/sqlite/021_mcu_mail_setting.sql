-- Medical checkup portal: the outgoing mail (Brevo SMTP) login, edited by the portal admin on
-- /mcu-submit/admin/mail instead of in environment variables.
--
-- One row (id 1). Host = '' means "not set on the page": the server's environment variables
-- (MCU_SMTP_*) are used, if any. PasswordEnc is the SMTP key sealed with AES-256-GCM under a key
-- derived from SESSION_SECRET, so a copy of the database or a backup does not hold a working
-- mail login. Changing SESSION_SECRET makes it unreadable: enter the key again on the page.
CREATE TABLE IF NOT EXISTS McuMailSetting (
    McuMailSettingID INTEGER PRIMARY KEY,
    Host             TEXT NOT NULL DEFAULT '',
    Port             INTEGER NOT NULL DEFAULT 587,
    Username         TEXT NOT NULL DEFAULT '',
    PasswordEnc      TEXT NOT NULL DEFAULT '',
    FromAddress      TEXT NOT NULL DEFAULT '',
    UpdatedAt        TEXT NULL
);
INSERT OR IGNORE INTO McuMailSetting (McuMailSettingID) VALUES (1);
