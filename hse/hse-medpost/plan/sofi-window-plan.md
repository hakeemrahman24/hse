# SOFI open windows and results summary (built 2026-10-28)

Status: decided by the owner on 2026-10-28 and built the same day (section 7 lists what differs from the plan below). Scope: the public `/sofi` fatigue questionnaire and the staff views of its results.

## 1. Decisions (owner)

- **The public link opens only inside an admin-set window.** Outside every window `/sofi` shows "Closed, opens on <date>" (EN/ID, same language switch as today).
- **Several future windows can be scheduled.** Old windows stay on record for audit; nothing is deleted.
- **All times are shown and entered in GMT+9.** Stored in UTC like every other timestamp (`YYYY-MM-DDTHH:MM:SS`). GMT+9 has no daylight saving, so a fixed offset (`time.FixedZone("GMT+9", 9*3600)`) is enough. Every label says "GMT+9" so nobody has to guess.
- **No new results page.** Medical team results stay under Medical Record > Screening > Fatigue (table tab and dashboard already exist). We add a summary card there.

## 2. Schema

`SofiWindow` (sqlite `018_sofi_window.sql`, mysql `028_sofi_window.sql`; mysql is run by hand after a mysqldump):

| Column | Meaning |
|---|---|
| WindowID | PK |
| Label | optional name, e.g. "Round 3, Nov crew" |
| OpensAt, ClosesAt | UTC, `ClosesAt > OpensAt` |
| EndedAt | NULL, or UTC time an admin pressed "Close now" / "Cancel" |
| EndedReason | NULL, `closed_early` or `cancelled` |
| CreatedAt, FK_CreatedByStaffID | who scheduled it |
| EndedByStaffID | who ended it |

Rules:

- **Windows are immutable.** To change one, cancel it and schedule a new one; the cancelled row stays. This is what keeps the audit trail honest.
- **The effective close time is `min(ClosesAt, EndedAt)`.** A cancelled future window never opens.
- **No overlap** between windows that are not cancelled; the server rejects it with a clear message.
- **Open now** means some window has `OpensAt <= now < effective close`.
- `SofiScreening` gets a nullable `FK_WindowID` (same migration), set at submit, so each window is a "round" and results can be filtered per round exactly. Old rows stay NULL ("before windows existed").

## 3. Server

- New `internal/sofi/window.go`: `IsOpen(now)`, `Current(now)`, `Next(now)`, `Create`, `End`, `List`. Take `now` as a parameter so tests need no clock.
- Check the window in `Check`, `Start`, `Names`, `SignIn` and `Submit`, not only the landing page, because the API can be called directly.
- **Mid-form at closing time:** the sign-in records the window it was opened under; `Submit` accepts that window until the session expires. A cancel or "Close now" by an admin is the one case that stops submits at once (the session's window then has `EndedAt` set).
- Admin API (`RequireAdmin`): `GET /api/sofi-windows`, `POST /api/sofi-windows`, `POST /api/sofi-windows/{id}/end`. Staff API: `GET /api/sofi-windows/status` for the status line.
- Every create and end writes who and when.

## 4. Screens

- **Settings > "SOFI schedule"** (admin only, new `data-panel` like the existing ones): a table of windows (upcoming, open, past, cancelled, each with creator and end reason), an Add form (label, opens, closes, in GMT+9), a "Close now" button on the open window and "Cancel" on upcoming ones, and a quick preset "Open now for 7 days".
- **SOFI tab and dashboard** (Medical Record > Screening > Fatigue): a one-line status ("Open until 31 Oct 17:00 GMT+9", "Opens 3 Nov 08:00 GMT+9", or "Closed") and a **Round** filter (All / each window / before windows).
- **Summary card** at the top of the dashboard section, from the existing `/api/sofi-log/summary` (add `window` filter): surveys and people, share per overall level, highest-scoring dimension, company or position with the most High results. Same tile styling as the existing stat tiles.

## 5. Build order (tests first)

1. Migration pair + `window.go` with tests: overlap, open/closed edges, cancel, close early, grace on submit.
2. Enforce in the public routes; closed page in EN/ID.
3. Admin API + Settings panel.
4. `FK_WindowID` on submit; `window` filter on `StaffList` and `StaffSummary`.
5. Status line, Round filter, summary card.
6. Progress Log entry in `hse-medpost-plan.md`.

## 6. Open questions: answered and built (2026-10-28)

The owner accepted all four recommendations ("go"). Built the same day; the table keeps the reasoning.

| Question | Recommendation | Why |
|---|---|---|
| Who may see the results (names and fatigue scores)? | **Decided, no change needed: any signed-in Medpost staff.** Read-only, staff sign-in only, never public. Revisit and switch the two read routes to `clinician(...)` only if accounts for non-medical people (for example HSE officers) are ever added. | The owner asked that the whole medical team sees them. Fatigue scores with names are health data, so the rule is least privilege: the audience is exactly the people who act on a High result, and nobody else gets an account that can read them. Narrowing to doctor and CMO now would contradict that request. |
| Warn when a window is short? | **Built (Settings > SOFI schedule, `windowWarnings` in `sofi-schedule.js`): warn, do not block.** A confirm box when a window is shorter than **24 hours**, saying how long it is and that crews on another shift may miss it. Same box when it opens in the past by more than a few minutes. Close now and Cancel already ask for confirmation. | Crews work shifts and rotations, so a window shorter than a day can silently exclude a whole shift. A hard minimum would block legitimate use (a one-day catch-up for one crew), so the admin decides. |
| Contact line on the closed page? | **Built: one fixed line in both languages: "Questions? Ask the medical post."**, shown whether or not a next opening date exists. No phone number or name on the page. | People who arrive at a closed page need a next step. A fixed line cannot go stale and exposes no personal contact on a public page; a number can be added later from a setting if the medical post wants one. |
| From / To dates use the UTC day | **Built (`siteDayFilter` in `window.go`, used by the list and the summary): turn the typed day into GMT+9 day bounds on the server** (day starts at 15:00 UTC the day before), for the SOFI list and summary. The sleep-score charts keep their own dates. | Times are shown in GMT+9, so a day filter that uses another day boundary gives counts that disagree with the table next to it. |

"Results per round" is already built: each survey stores its window (`FK_WindowID`), and the Round filter and the From / To dates combine (both must match). Surveys saved before windows existed show under "Before rounds existed".

## 7. As built (differences from the plan above)

- **Migration numbers:** sqlite `018_sofi_window.sql` and mysql `028_sofi_window.sql` (017 / 027 were taken by `mcu_submission_batches` while this was being built). The MariaDB file also adds `SofiScreening.FK_WindowID` (no FOREIGN KEY constraint; `ADD COLUMN IF NOT EXISTS`). On SQLite the column is added at startup by `sofi.EnsureWindowColumn`, because `ADD COLUMN` cannot be repeated in a file that runs on every start.
- **Audit names:** `CreatedBy` / `EndedBy` are stored as the admin's name text (not staff ids), so the audit line still reads the same if an account is renamed or removed.
- **Server:** `internal/sofi/window.go` (rules, takes `now` as a parameter) and `window_api.go` (checks and endpoints). `auth.Session` got an optional `WindowID` so the sign-in remembers its window.
- **Routes:** `GET /api/sofi-windows/status` (any signed-in staff), `GET|POST /api/sofi-windows` and `POST /api/sofi-windows/{id}/end` (admin). `?window=<id>|none` filters `/api/sofi-log` and `/api/sofi-log/summary`.
- **Mid-form rule:** the sign-in window counts until an admin ends it; running out of time does not cut anyone off. A cookie from before windows has no window and works only while some window is open.
- **Screens:** Settings > Fatigue survey > SOFI schedule (`sofi-schedule.js`); status line, Round filter and submit times in GMT+9 on the SOFI tab and the dashboard (`sofi-rounds.js`, `sofi-log.js`); written summary card on the dashboard (`sofi-dashboard.js`, worked out in the browser from the summary counts).
- **Tests:** `window_test.go`, `window_api_test.go`; older tests use `newTestAPI` (one always-open window) and the window tests use `newBareTestAPI`.
- **Deploy order:** run `migrations/mysql/028_sofi_window.sql` BEFORE the new build. From that build on `/sofi` is closed until an admin schedules a window, so schedule one right after deploying.

## 8. Still open

- Nothing from this plan. A single comparison of two rounds side by side (instead of switching the Round filter) was not asked for and is not built.
- `migrations/mysql/028_sofi_window.sql` has been run on production (owner, 2026-10-28), so the build can be deployed; schedule a window right after, because `/sofi` stays closed until one exists.

## 9. Dashboard by test period, not by date (owner, 2026-10-28)

The SOFI dashboard does not need a date filter. It reports **one test period** at a time, the period's dates are its label, and the user picks the period in which the survey was conducted.

- **Built:** the dashboard no longer reads the From / To dates (they stay for the sleep-score charts above it; a note on the page says so). A **Period** dropdown lists every window that has opened, newest first, labelled by its dates in GMT+9 (with the name in front when it has one), plus "All periods" and "Before periods were scheduled". It opens on the **latest period**. The summary card starts with the period ("Period 2026-10-19 00:00 – 2026-10-26 00:00 (GMT+9). …").
- **Table tab:** keeps its own Period dropdown, which opens on "All periods". The two dropdowns are independent (a choice in one does not move the other).
- **Server:** unchanged. `from` / `to` (GMT+9 days) are still accepted by the API, but no SOFI screen sends them now.
- **Verified** in headless Chromium against a throwaway database with two finished periods and one survey from before periods existed: the dashboard opened on the latest period; picking the older period, "All periods" and the table's own choices gave the right counts (3, 2, 6, and 3 / 1 rows in the table); pressing Filter with new From / To dates sent no SOFI request; no script errors.

## 10. PDF report and the doctor's note, saved per period (owner, 2026-10-29)

The owner asked for a PDF report based on the dashboard (the rest of that sentence was cut off, so the default contents below were built; add anything else as a new open item). The report's written conclusion is a **doctor's note saved per period**, not typed fresh each time.

- **What the PDF holds:** header (period dates in GMT+9 or "All periods", generated time and who generated it), the auto-generated summary, the Low / Moderate / High counts with percentages, the result by dimension and by company or position as the same coloured bars as the screen, the doctor's note when one exists, and on every page the provisional-bands note and "Page n of N". One report is one period or all periods, and it prints exactly the numbers the dashboard has drawn.
- **Where:** Dashboard tab, under the Period picker: the note box and a **Download PDF report** button. File name `sofi-report-<opens>-to-<closes>.pdf`, or `sofi-report-all-periods.pdf`.
- **The note:** table `SofiPeriodNote` (sqlite `019_sofi_period_note.sql`, mysql `029_sofi_period_note.sql`). Append-only like `SofiWindow`: every save adds a row (who, when), the newest row is the one shown and printed, an empty save clears it and the older versions stay on record. Max 2000 letters. Only for a period that has opened (an upcoming or cancelled one has no surveys). No note for "All periods" or "Before periods were scheduled".
- **Who:** anyone signed in can read the note; only a doctor, the Chief Medical Officer or the administrator can write it (`middleware.MayOpenMedicalCheckup`, checked inside `PutNote`, not only on the route). Others see the text without a box, or nothing when there is no note.
- **Routes:** `GET|PUT /api/sofi-windows/{id}/note`.
- **PDF in the browser:** `sofi-report-pdf.js` (model, layout; pure functions, tested by `web/sofi-report.test.js`) and `sofi-report.js` (note box, button, loads jsPDF on first use). The dashboard hands its drawn numbers over as `window.SofiDashboard` together with the period they are for; the button refuses them if the dropdown has moved on, and warns when the note has unsaved changes (the PDF prints the saved note).
- **Deploy order:** run `migrations/mysql/029_sofi_period_note.sql` BEFORE the new build (after 028, which is already run).
- **Not built / open:** the table of individual results (names and scores) is not in the PDF, because names with fatigue scores are health data and the owner did not ask for it; a doctor's signature or approval line (the bands are provisional until the doctor approves them); comparing two periods side by side. A UI check as a non-doctor (read-only note) was not done in a browser; the rule is covered by Go tests.

## 11. East Asian and Arabic text in the PDF report (owner, 2026-10-29)

Company names and the doctor's note can be Chinese, Japanese, Korean or Arabic, and jsPDF's Helvetica only has Latin.

- **Fonts** (`web/static/fonts/`, licence in `OFL-Noto-report-fonts.txt`, rebuilt by `build-report-fonts.py`): `NotoSansArabic-Regular.ttf` (235 KB) and `NotoSansCJK-Report.ttf` (3.7 MB, 2.2 MB gzip), a subset of Noto Sans CJK JP converted from CFF to TrueType because jsPDF cannot embed CFF. The subset holds kana, CJK punctuation, full-width forms, ASCII and every character of GB 2312, JIS X 0208 and KS X 1001. `--big5` adds Traditional Chinese (+2.2 MB). Han characters use the Japanese glyph forms.
- **Loaded on demand** by `sofi-report.js`, only when the report text has that script (`sofiReportScripts`), then cached. If a font fails to load the PDF is still made, that script prints as `?`, and the doctor is told.
- **Drawing** (`sofi-report-pdf.js`): text is cut into runs by script; Latin in Helvetica, CJK one character per call, Arabic as a whole run with `{lang: "ar"}` (shaped and reversed by jsPDF). Own line wrapper, because `splitTextToSize` cannot break CJK. Anything else (Thai, emoji, other scripts) is drawn by the browser on a canvas (`createRasteriser` in `sofi-report.js`, 4 px per point) and placed as a picture, so the browser shapes it correctly; it is not selectable text and uses the viewer's system fonts (Noto Sans Thai / Leelawadee / Tahoma, colour emoji). Without a rasteriser it prints `?`.
- **Limits:** the page layout stays left to right, so a mostly Arabic note is not right-aligned. Rare characters outside the national sets show as an empty box. Fonts add about 4 MB to the repo.
- **Ghostscript** is not involved: it is only used on the server to compress uploads; the report is drawn in the browser.
- Verified in headless Chromium with a mixed Latin / Chinese / Japanese / Arabic note and company names; `node --test web/*.test.js` green.

## 12. A person sees their own earlier results (owner, 2026-10-29)

When someone signs in at `/sofi` (a window must be open to sign in) the start page lists their own earlier questionnaires above the form: date and time in GMT+9, total score out of 150 and the overall band, newest first, at most 20 (`past.go`, `sofi_home.html`). The person comes from the sign-in session only. Scores are worked out again from the stored answers, like the staff table. A result is still shown straight after submitting, as before. While no window is open nobody can sign in, so earlier results are not visible then; a personal PDF was not asked for.

## 13. One questionnaire per person per period (owner, 2026-10-29)

A person can submit once in each test period (window). `Submit` checks `SofiScreening` for the same person and window and answers 409 "You have already completed the questionnaire for this period." The check and the insert run under one in-memory lock (`submitMu`), so two taps at once cannot both pass; one server process serves the site. On `/sofi/start`, someone who has already submitted in their window sees that message and their earlier results, not the form. The old limit of 5 submissions an hour was dropped as it can no longer be reached. There is no unique index in the database: results saved before this rule may hold two rows for one person in one period, and they are left as they are. Add a unique index on (FK_PatientID, FK_WindowID) only after checking the data for such pairs.
