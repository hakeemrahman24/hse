-- When the public SOFI questionnaire (/sofi) is open. See plan/sofi-window-plan.md.
--
-- One row per window an admin scheduled. Rows are never edited or deleted, so the old schedule
-- stays on record: "Cancel" (before it opened) and "Close now" (while open) only set EndedAt,
-- EndedReason and EndedBy. The window is open while OpensAt <= now < min(ClosesAt, EndedAt).
-- All times are UTC, YYYY-MM-DDTHH:MM:SS; the pages show them in GMT+9.
-- CreatedBy / EndedBy are the admin's name as it was at the time (kept as text so the audit line
-- still reads the same if the account is renamed or removed later).
--
-- SofiScreening.FK_WindowID (which window a questionnaire was submitted in) is added by
-- sofi.EnsureWindowColumn at startup: SQLite's ADD COLUMN cannot be repeated, so it cannot live
-- in this file, which runs on every start.
CREATE TABLE IF NOT EXISTS SofiWindow (
    WindowID    INTEGER PRIMARY KEY AUTOINCREMENT,
    Label       TEXT NOT NULL DEFAULT '',
    OpensAt     TEXT NOT NULL,
    ClosesAt    TEXT NOT NULL,
    EndedAt     TEXT NULL,
    EndedReason TEXT NULL CHECK (EndedReason IN ('closed_early', 'cancelled')),
    CreatedAt   TEXT NOT NULL,
    CreatedBy   TEXT NOT NULL DEFAULT '',
    EndedBy     TEXT NOT NULL DEFAULT '',
    CHECK (ClosesAt > OpensAt)
);
CREATE INDEX IF NOT EXISTS idx_sofiwindow_opens ON SofiWindow(OpensAt);
