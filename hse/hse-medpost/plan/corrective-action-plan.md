# Corrective Action tracking (CAR) — plan

Status: decided, not built. Decisions below are the owner's (2026-11-09); do not reopen, add new questions to section 8.

## 1. Goal

One place to record, follow up and close findings from inspections and monitoring, across **every** health program. Easy to add, edit and close, and exportable to Excel with photos.

## 2. Where it appears

- **Per program:** a "Corrective Actions" tab on each program's activities page, showing only that program's findings.
- **Overall:** a "Corrective Actions" tab beside the "Activities" header, on the right side, listing every program's findings with filters (program, status, PIC, severity, date range, search).
- Every program gets the tab (decided). Programs are admin-defined data, so the code cannot assume which ones are inspection-only or socialization-only.

## 3. Columns

| Column | Notes |
|---|---|
| No. | display order, not stored |
| Program | `FK_ProcID`, required |
| Inspection date | required, `YYYY-MM-DD` text via `internal/dates` |
| Location | free text |
| Finding | required |
| Recommendation | required |
| PIC | custom searchable dropdown over the `Patient` table (employees), but the chosen name is **copied as text** into `PicName`, not stored as a foreign key (decided). **Only listed employees (decided):** typed text that is not picked from the list is rejected. The dropdown has keyboard navigation (see section 6a). |
| Due date | target close date, optional |
| Severity | Minor / Major / Critical |
| Status | Open / In Progress / Closed (stored). **Overdue** is computed: not Closed and due date before today |
| Closing date | set when closed |
| Action taken | what was done; required to close |
| Before photos | up to 3 |
| After photos | up to 3 |
| Source activity | optional `FK_ActivityID` link to the inspection that found it |
| Created by / closed by | staff ids and timestamps |
| Days open | computed, not stored |

## 3a. Follow-ups (decided 2026-11-09)

A finding can have several follow-up items, because one finding can need several actions and one of them may still be open.

| Follow-up field | Notes |
|---|---|
| Description | required, what must be done |
| PIC | same employee dropdown, copied as text; its own PIC, may differ from the finding's (decided) |
| Due date | optional, its own |
| Status | Open / Done |
| Done date, action taken | required to mark Done |

- **Finding status is computed when it has follow-ups:** none done = Open, some done = In Progress. A finding with no follow-ups keeps the manual Open / In Progress choice, so simple findings stay simple.
- **A finding cannot be closed while any follow-up is Open.** The error names the open ones. Closing still needs an after photo, action taken and a closing date.
- **Overdue** applies per follow-up; the finding shows overdue if it, or any open follow-up, is past its due date.
- A follow-up cannot be added to a Closed finding (reopen it first). A Done follow-up can be set back to Open while the finding is not closed.
- Lists and Excel show "2 of 3 follow-ups done" and the next open follow-up's due date; the export gets a Follow-ups sheet, one row per follow-up.
- Follow-up changes go into the finding's history log.
- Follow-up photos: not in this build (later).

## 4. Rules

- **Permissions:** any logged-in staff can add, edit, close and reopen.
- **Closing** needs a closing date (defaults to today), action taken and at least one after photo. Reopen clears closing date and closed-by but keeps the history.
- **Edit history (decided):** every change is appended to a history table (who, when, field, old value, new value). Shown in a "History" view on a finding. Not editable.
- **Delete (decided):** an admin (`AppUserTable.Role = 'admin'`) can delete a wrong entry; other staff cannot, the button is hidden and the API checks the role. Delete asks for confirmation, removes the finding, its photos (rows cascade, files removed from the `FileStore`) and its history. Because the history goes with it, write one line to the server log (who, finding id, program, time) so a deletion is still traceable.

## 5. Data (both engines, per CLAUDE.md)

Migrations: one file per engine in `migrations/sqlite/` and `migrations/mysql/` (check `ls` for the next free number first; numbers are separate per engine and other work is landing, so do not assume). MariaDB file is run by hand after a mysqldump. SQL must be valid on both engines.

- `CorrectiveActionTable`: ActionID, FK_ProcID, FK_ActivityID (nullable), InspectionDate, Location, Finding, Recommendation, PicName (text copy of the picked employee's name, no FK), PicEmployeeID (optional text copy of `Patient.EmployeeID` to tell same-name people apart), DueDate, Severity, Status, ClosingDate, ActionTaken, FK_CreatedBy, CreatedAt, FK_ClosedBy, ClosedAt.
- `CorrectiveActionPhotoTable`: PhotoID, FK_ActionID (cascade), Phase ('Before'/'After'), FileLink, SortOrder, UploadedBy, UploadedAt. The limit of 3 per action and phase is enforced in the store, inside a transaction.
- `CorrectiveActionFollowUpTable`: FollowUpID, FK_ActionID (cascade), Description, PicName, PicEmployeeID (text copies, no FK), DueDate, Status (Open/Done), DoneDate, ActionTaken, FK_CreatedBy, CreatedAt, FK_DoneBy, DoneAt.
- `CorrectiveActionHistoryTable`: HistoryID, FK_ActionID (cascade), FK_StaffID, ChangedAt, Field, OldValue, NewValue.

Files go through the existing `FileStore` (`internal/storage`) under a `corrective-action/` prefix, never new disk code.

## 6. Photos under 600 KB

- **Browser:** extend `web/static/image-compress.js` (or a sibling) to downscale to a 1600 px long edge, honour EXIF orientation, then step JPEG quality down until under 600 KB. The current helper targets 1 MB and never resizes.
- **Server:** the real limit. Reject non-JPEG/PNG; if over 600 KB try one server-side re-encode (stdlib `image/jpeg`), and reject with a clear error if still over, so a bypassed client cannot store a large file.
- Show a "Compressing photos…" status like the existing upload fields.

## 6a. PIC dropdown behaviour

- Type to search by name or employee id; results appear after a short pause and each row shows name, employee id, company and position. The most recent search wins (a slow earlier response never overwrites a newer one).
- **Keyboard navigation:** Down/Up arrows move through results (wrapping at the ends), Enter picks the highlighted row, Escape closes the list, Tab leaves the field. Home/End jump to first/last. The highlighted row scrolls into view.
- **Mouse and touch:** click or tap a row to pick; works at phone width.
- **Accessibility:** the WAI-ARIA combobox pattern (`role="combobox"`, `aria-expanded`, `aria-controls`, `aria-activedescendant`, results as `role="listbox"` / `option`), with a "no matching employee" message announced politely.
- After a pick the field shows the chosen person with a clear button; editing the text again clears the pick, so the form cannot be saved with text that was not picked. Required-field error if nothing is picked.
- Reuse the pattern in `web/static/mcu-portal-combobox.js` and `mcu-worker-picker.js` rather than writing a third one; names are written with `textContent` only.

## 7. Excel export

- Follows the current filters. Uses `excelize` (already a dependency), pattern from `internal/activity-log/bulkdownload.go`.
- **Thumbnails only (decided):** each photo is scaled to one fixed thumbnail size (about 160 x 120 px). **Every row gets the same row height and every photo column the same width**, so the sheet is uniform whether a finding has 0 or 3 photos. Photos sit in Before 1-3 and After 1-3 columns, one thumbnail per cell.
- Thumbnail resize likely needs `golang.org/x/image/draw` (new dependency, to be approved when building) or a stdlib-only box downscale.
- Full-size photos stay available in the app.
- Written row by row to keep memory low on the Pi (2 GB shared with the database).

## 8. Open questions

1. Reuse the existing patient search behind `web/static/mcu-worker-picker.js`, or add a small dedicated endpoint? Decide at build step 2. Either way: return only id-free display fields (name, employee id, company, position), no medical data; write names with `textContent` only.

Consequence of "text, not FK": a later rename or removal of the employee does not change old findings (a snapshot, which suits a record).

How "only listed" is enforced: the browser sends the picked employee's id with the form, but only as a lookup key. The server looks that id up in `Patient`, rejects the save if it does not exist, and copies the name and employee id from the database into `PicName` and `PicEmployeeID`. The id itself is not stored. Any name typed or posted by hand is ignored, so the rule holds even if the page is bypassed. When editing a finding whose PIC was picked earlier, an unchanged PIC is kept as saved (the id is not resent), and picking again replaces it.

## 9. Build order (TDD, tests first, 80% coverage)

1. Migrations (both engines) and `internal/correctiveaction` store with tests, including the 3-photo limit and history rows.
2. API: list (with filters), create, edit, close, reopen, photo upload/delete, history. Server-side 600 KB check.
3. Browser photo resize/compress to 600 KB.
4. UI: follow-up list inside a finding (add, mark done, reopen, remove), searchable PIC dropdown over employees (saved as text), admin-only delete with confirmation, per-program tab, overall tab beside the Activities header, add/edit/close modals, history view.
5. Excel export with uniform thumbnail cells.
6. Review (code-reviewer, security-reviewer for the upload path), update docs/CODEMAPS, Progress Log entry.

## 10. Later (not in this build)

- Dashboard card: open and overdue findings per program.
- Pre-fill a finding from the camp and food inspection forms.
- Overdue reminders to the PIC.
