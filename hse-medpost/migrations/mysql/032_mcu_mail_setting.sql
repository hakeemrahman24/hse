-- Medical checkup portal: the outgoing mail (Brevo SMTP) login (MariaDB). Same shape as
-- migrations/sqlite/021_mcu_mail_setting.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS / INSERT IGNORE).
--
-- Run it BEFORE deploying the build that has the portal's Mail settings page.

CREATE TABLE IF NOT EXISTS McuMailSetting (
    McuMailSettingID INT NOT NULL PRIMARY KEY,
    Host             VARCHAR(253) NOT NULL DEFAULT '',
    Port             INT NOT NULL DEFAULT 587,
    Username         VARCHAR(254) NOT NULL DEFAULT '',
    PasswordEnc      VARCHAR(600) NOT NULL DEFAULT '',
    FromAddress      VARCHAR(254) NOT NULL DEFAULT '',
    UpdatedAt        VARCHAR(32) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO McuMailSetting (McuMailSettingID) VALUES (1);
