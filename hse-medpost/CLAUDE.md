# hse-medpost — AI assistant notes

## SQL must work on MariaDB, not just SQLite

Production runs MariaDB; SQLite is only the local/test default, so green tests do not prove a query works on the Pi (a `LIKE ? ESCAPE '\'` passed on SQLite and broke every `/sofi` name search on MariaDB). Write SQL valid on both, avoid backslashes in SQL string literals, add both a `migrations/sqlite/` and a `migrations/mysql/` file for schema changes, and log the real error server-side. Details: "Production runs MariaDB, not SQLite" in [README.md](README.md).

## Editing the forms in `forms/`

See [forms/form_readme.md](forms/form_readme.md) for the full form architecture. Its **Verification methodology** section is the canonical rule for how to verify an edit — summary:

- **Text/content edits** (wording, translations, labels): verify with `node --check` on the affected `<script>` block plus an exact-match occurrence-count check (`content.count(old) == 1`) before and after each replacement. Do **not** spin up a headless browser render for this tier — it's unnecessary cost for a wording change.
- **Layout/visual edits** (stamp geometry, CSS spacing/positioning, page-break behavior, anything about how something looks rather than what it says): these still need live verification — launch headless Chromium over CDP, call the form's real PDF-building function (`buildPdf()` / `buildInspectionPDF()` / `drawSignedStamp()` / `stampPage()`), render to PNG via `pdftoppm`, and for page-break-sensitive changes also check via CDP `Page.printToPDF` (the jsPDF "Save PDF" path and the browser's `@media print` path are independent — fixing one does not fix the other).

- **Rename on every revision.** Whenever you change a form, rename only the files you changed so the last `-<ddmmyy>` segment is today's date (`food-inspection-form-v280227-191026.html`: expiry `280227`, revised `191026`). Do not run `update-validity.py` for this (it renames every form and changes the expiry); use a plain `mv`, leave untouched forms alone, and update the filename in `forms/form_readme.md` and `forms/online-upload-plan.md`. A second edit on the same day keeps the same name.

This distinction was set explicitly by the project owner after a session that was doing full browser-render proofs on simple text/translation edits — keep applying it rather than defaulting back to rendering everything.
