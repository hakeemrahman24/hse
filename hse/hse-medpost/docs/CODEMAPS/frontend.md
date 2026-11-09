<!-- Generated: 2026-10-20 | Files scanned: web/templates (7), web/static/*.js (18) | Token estimate: ~750 -->
# Frontend (plain JS, templates in web/templates, assets in web/static)

Templates parse at startup → **restart after template edits**. Static JS/CSS read from disk. Main CSS: `static/clearwave/app.css` (+ `styles.css`).
JS tests: `node --test web/*.test.js` (pure helpers loaded via `vm`).

## Pages → scripts
| page | template | scripts |
|---|---|---|
| / login | login.html | login.js |
| /start | start.html | page-shell.js, presence.js |
| /programs, /programs/{id} | programs.html | programs.js, program.js, app.js (1500, activity log), dashboard.js, summary-format.js |
| /mcu (clinician) | mcu.html (800) | mcu.js (2900), date-dmy.js, certificate-name.js, risk-triggers.js, case-guard.js, image-compress.js |
| /emr | emr.html | emr.js (routing, sidebar tree, titles), date-dmy.js, malaria.js (1100), screening-log.js (400), da-form-core.js / da-form-pdf.js / da-form.js (Drug & Alcohol entry form) — load order malaria.js, screening-log.js, da-form-core.js, da-form-pdf.js, da-form.js, emr.js |
| /admin/settings | settings.html | settings.js (1860) |
| /inventory | inventory.html | — |

## Medical Record (/emr#screening/malaria)
emr.js: hash route → panel; calls `window.loadMalaria()`.
malaria.js: pure helpers (cleanHeadOfHousehold, parseWeightKg, matchPatients, filterRegions, barPercent, speciesSegments, nextIndex, malariaDetailFields…) then `initMalariaForm`:
- tabs Table | Dashboard; **two modes** in Table tab: toolbar+table (search, Export, New) ⇄ form (`is-form-mode` class; Cancel top-left and bottom; save clears form, stays open)
- `createSearchPicker` (regency/district/village; arrows, Enter/Tab pick, free text cleared on blur; remote mode for districts) + patient picker with same keys
- table: 8 columns, Test ID click expands detail row (bold red when open); result select for pending rows → PUT result
- dashboard: filter (from/to, chart by, Positives all|by type) → /summary → tiles + stacked SVG bars
screening-log.js: alcohol + fatigue share `#mr-panel-screening` (Table | Dashboard tabs, `window.loadScreening(kind)` from emr.js); config in `SCREENING_KINDS`; reuses barPercent/addClearButton from malaria.js and the malaria CSS via `#scr-` ids.
Reused CSS: .add-activity, .settings-grid, .date-dmy, .ro-value, .data-table.compact, .app-tabs, .company-typeahead, .dash-bars.

da-form*.js: "New drug & alcohol screening" (`#scr-new`, alcohol only) switches `#scr-view-table` to form mode (`is-form-mode`) and shows `#da-form`. da-form-core.js = pure helpers (answers, `daRecord`, `daMissing`, `daFirstName`, stamps); da-form-pdf.js = `daBuildPdf` / `daPdfBlob` (<1 MB, jsPDF lazy-loaded from /static/vendor); da-form.js = page wiring (patient pickers via `createSearchPicker`, click-to-approve cursive signatures, photos, Save → POST /api/screening-log/alcohol, Save PDF). Font: /static/fonts/DancingScript-wght.ttf (OFL).

fatigue-form*.js: "New fatigue screening" (`#scr-new-fatigue`, fatigue only) shows `#fa-form` in form mode, same pattern as da-form*.js and reusing its helpers (load order da-form.js, fatigue-form-core.js, fatigue-form-pdf.js, fatigue-form.js, emr.js). Core = scoring (`faScore`, `faBandIndex`, `faBpLevel`), `faProblems`, `faRecord`; pdf = `faBuildPdf` / `faPdfBlob`; page = pickers, live scores, signatures, Save → POST /api/screening-log/fatigue.

corrective-*.js: "Corrective actions" tab in programs.html (every program + home beside Activities; `window.CA`). corrective-core.js = pure helpers (tested by web/corrective.test.js); corrective-photo.js (canvas reduce <600 KB), corrective-pic.js (ARIA combobox over employees), corrective.js (compact one-line table, row click opens details), corrective-form.js, corrective-detail.js (thumbnails, follow-ups, close/reopen, history); corrective.css (`--ca-control-h` 2.25rem).
Finding link: GET /api/corrective-actions/{id} also returns activityId/activityTitle/activityDate (LEFT JOIN ActivityTable); app.js "Report finding" button opens CA.openForm(null, procId, source).
