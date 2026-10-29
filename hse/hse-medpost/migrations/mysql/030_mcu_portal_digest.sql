-- Medical checkup portal: the reviewers' mail list and the digest interval (MariaDB). Same shape as
-- migrations/sqlite/020_mcu_portal_digest.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS / INSERT IGNORE).
--
-- Run it BEFORE deploying the build that sends the reviewer digest.

CREATE TABLE IF NOT EXISTS McuReviewerEmail (
    ReviewerEmailID INT AUTO_INCREMENT PRIMARY KEY,
    Email           VARCHAR(254) NOT NULL,
    Name            VARCHAR(150) NOT NULL DEFAULT '',
    Active          TINYINT(1) NOT NULL DEFAULT 1,
    EmailBouncedAt  VARCHAR(32) NULL,
    CreatedAt       VARCHAR(32) NOT NULL,
    UNIQUE KEY uq_mcureviewer_email (Email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS McuDigestSetting (
    McuDigestSettingID INT NOT NULL PRIMARY KEY,
    IntervalHours      INT NOT NULL DEFAULT 24,
    LastSentAt         VARCHAR(32) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO McuDigestSetting (McuDigestSettingID) VALUES (1);
