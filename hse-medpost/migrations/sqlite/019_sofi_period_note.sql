-- The doctor's note written for one SOFI test period (one /sofi window). See plan/sofi-window-plan.md.
--
-- Append-only, like SofiWindow: saving a note adds a row, and the newest row for the window is the
-- one shown and printed on the PDF report. Older versions stay on record (who wrote what and when);
-- clearing a note adds an empty row. Only a doctor or the Chief Medical Officer may write
-- (checked in internal/sofi/note.go), anyone signed in may read.
-- WrittenAt is UTC, YYYY-MM-DDTHH:MM:SS; the pages show it in GMT+9. WrittenBy is the writer's name
-- as it was at the time.
CREATE TABLE IF NOT EXISTS SofiPeriodNote (
    NoteID      INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_WindowID INTEGER NOT NULL REFERENCES SofiWindow(WindowID),
    Note        TEXT NOT NULL,
    WrittenBy   TEXT NOT NULL DEFAULT '',
    WrittenAt   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sofinote_window ON SofiPeriodNote(FK_WindowID, NoteID);
