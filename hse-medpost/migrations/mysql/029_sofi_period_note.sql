-- SofiPeriodNote (MariaDB). Same shape as migrations/sqlite/019_sofi_period_note.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build that has the SOFI doctor's note and PDF report. It needs
-- 028_sofi_window.sql to have been run first (the note belongs to a window).

CREATE TABLE IF NOT EXISTS SofiPeriodNote (
    NoteID      INT AUTO_INCREMENT PRIMARY KEY,
    FK_WindowID INT NOT NULL,
    Note        TEXT NOT NULL,
    WrittenBy   VARCHAR(150) NOT NULL DEFAULT '',
    WrittenAt   VARCHAR(32) NOT NULL,
    KEY idx_sofinote_window (FK_WindowID, NoteID),
    CONSTRAINT fk_sofinote_window FOREIGN KEY (FK_WindowID) REFERENCES SofiWindow(WindowID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
