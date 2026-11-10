-- Medical checkup portal: how often submitters are told about reviewed and rejected documents, and
-- whether the reviewers are copied (owner, 2026-11-10) (MariaDB). SQLite gets the same table from
-- migrations/sqlite/029.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS / INSERT IGNORE). Run it BEFORE deploying the build that reads the table;
-- until it exists the server falls back to 12 hours with copies on.

CREATE TABLE IF NOT EXISTS McuNoticeSetting (
    McuNoticeSettingID INT NOT NULL PRIMARY KEY,
    IntervalHours      INT NOT NULL DEFAULT 12,
    StartHour          INT NOT NULL DEFAULT 8,
    Timezone           VARCHAR(8) NOT NULL DEFAULT 'WIB',
    CopyReviewers      TINYINT NOT NULL DEFAULT 1,
    LastRunAt          VARCHAR(32) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO McuNoticeSetting (McuNoticeSettingID) VALUES (1);
