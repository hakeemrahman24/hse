-- SofiWindow and SofiScreening.FK_WindowID (MariaDB). Same shape as migrations/sqlite/018_sofi_window.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS on the table, the column and the index).
--
-- Run it BEFORE deploying the build that has the SOFI open windows: from that build on /sofi is
-- closed until an admin schedules a window in Settings > SOFI schedule.
--
-- FK_WindowID has no FOREIGN KEY constraint on purpose: old questionnaires keep NULL ("before
-- windows existed"), and the app only ever writes ids it just read from SofiWindow.

CREATE TABLE IF NOT EXISTS SofiWindow (
    WindowID    INT AUTO_INCREMENT PRIMARY KEY,
    Label       VARCHAR(100) NOT NULL DEFAULT '',
    OpensAt     VARCHAR(32) NOT NULL,
    ClosesAt    VARCHAR(32) NOT NULL,
    EndedAt     VARCHAR(32) NULL,
    EndedReason VARCHAR(16) NULL,
    CreatedAt   VARCHAR(32) NOT NULL,
    CreatedBy   VARCHAR(150) NOT NULL DEFAULT '',
    EndedBy     VARCHAR(150) NOT NULL DEFAULT '',
    KEY idx_sofiwindow_opens (OpensAt)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE SofiScreening ADD COLUMN IF NOT EXISTS FK_WindowID INT NULL;
ALTER TABLE SofiScreening ADD INDEX IF NOT EXISTS idx_sofi_window (FK_WindowID);
