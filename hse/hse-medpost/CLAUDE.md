# hse-medpost — AI assistant notes

## SQL must work on MariaDB, not just SQLite

Production runs MariaDB; SQLite is only the local/test default, so green tests do not prove a query works on the Pi (a `LIKE ? ESCAPE '\'` passed on SQLite and broke every `/sofi` name search on MariaDB). Write SQL valid on both, avoid backslashes in SQL string literals, add both a `migrations/sqlite/` and a `migrations/mysql/` file for schema changes, and log the real error server-side. Details: "Production runs MariaDB, not SQLite" in [README.md](README.md).

## Every commit and every deploy is pushed to GitHub (owner rule, 2026-11-09)

- After each commit, run `git push` on the current branch, so GitHub always has what is on the Pi. After a deploy (`deploy/deploy.sh`), make sure the commit that was deployed is pushed too (and say which commit went out).
- Deploy from a clean checkout of the commit (a `git worktree`), not the working tree, when the tree holds unrelated uncommitted work: `deploy.sh` sends whatever is on disk.
- If the push fails or there is no remote (`git remote -v` empty, the case until 2026-11-09), say so plainly. Do not skip the push silently, and do not invent a remote URL: ask the owner for it.
- Pushing is part of the owner's standing instruction for this project only; it does not cover force-pushes, other branches, or other repositories.

## Editing the forms in `forms/`

See [forms/form_readme.md](forms/form_readme.md) for the full form architecture. Its **Verification methodology** section is the canonical rule for how to verify an edit — summary:

- **Text/content edits** (wording, translations, labels): verify with `node --check` on the affected `<script>` block plus an exact-match occurrence-count check (`content.count(old) == 1`) before and after each replacement. Do **not** spin up a headless browser render for this tier — it's unnecessary cost for a wording change.
- **Layout/visual edits** (stamp geometry, CSS spacing/positioning, page-break behavior, anything about how something looks rather than what it says): these still need live verification — launch headless Chromium over CDP, call the form's real PDF-building function (`buildPdf()` / `buildInspectionPDF()` / `drawSignedStamp()` / `stampPage()`), render to PNG via `pdftoppm`, and for page-break-sensitive changes also check via CDP `Page.printToPDF` (the jsPDF "Save PDF" path and the browser's `@media print` path are independent — fixing one does not fix the other).

- **Rename on every revision.** Whenever you change a form, rename only the files you changed so the last `-<ddmmyy>` segment is today's date (`food-inspection-form-v280227-191026.html`: expiry `280227`, revised `191026`). Do not run `update-validity.py` for this (it renames every form and changes the expiry); use a plain `mv`, leave untouched forms alone, and update the filename in `forms/form_readme.md` and `forms/online-upload-plan.md`. A second edit on the same day keeps the same name.

This distinction was set explicitly by the project owner after a session that was doing full browser-render proofs on simple text/translation edits — keep applying it rather than defaulting back to rendering everything.

## Where things are written down (read before changing anything)

- **Plans live in `plan/`.** `hse-medpost-plan.md` is the master plan and its **Progress Log** (newest entry on top) is the current state of the project. `emr-plan.md` covers clinical visits and `mcu-submission-plan.md` covers the public medical checkup submission portal (decided, not built; its section 9 lists the decisions, section 10 the build order). Read the relevant plan and the latest Progress Log entries before designing or editing, instead of re-deriving from code.
- **End every session that changed code or made a design decision** with one dated Progress Log entry: what changed, why, what is still open.
- **Decisions in a plan are the owner's.** Do not reopen a decided item (for example: no Nextcloud, files on the Pi's disk behind a `FileStore` interface; company is pick-only; mail is sent per batch only when the submitter presses Send). Add new questions to the plan's open list instead.
- **Add WHY-comments** in new Go code (the owner is learning from this codebase); `internal/activity-log/migrate.go` shows the density wanted.
- **Schema changes need both** `migrations/sqlite/NNN_*.sql` and `migrations/mysql/NNN_*.sql` (numbers are separate per engine; check `ls` for the next free one). MariaDB files are run by hand after a mysqldump.

## The knowledge graph (`graphify-out/`)

Use it for "where is X / what touches Y" questions: `graphify query "<question>"`, `graphify path "A" "B"`, `graphify explain "X"`, or open `graphify-out/GRAPH_REPORT.md`. It can be stale; confirm anything important in the code.

- **Refresh** after a batch of changes with `/graphify . --update` (re-reads only changed files; docs cost subagent tokens, code-only changes cost none).
- **When files are renamed or deleted** (forms get a new date suffix on every revision), the update can leave nodes pointing at the old filename. After an update, find nodes whose `source_file` no longer exists on disk, remove them from `graphify-out/graph.json` (and their edges), then run `graphify cluster-only .` so the report and HTML match the graph. Last cleaned 2026-10-27 (383 stale nodes from the pre-rename form files).
- If `GRAPH_REPORT.md` counts differ from `graph.json`, the report is from a different build: run `graphify cluster-only .`.
- **What is excluded:** `.graphifyignore` skips `_reference/`, `.cursor/`, `data/` and `old-data/` (the legacy Flask clinic app, kept on disk only). To exclude something else, add it there, remove its nodes from `graph.json` (nodes whose `source_file` starts with the folder), run `graphify cluster-only .`, then drop its entries from `graphify-out/manifest.json` by hand. `save_manifest` never removes entries, so without that last step every `--update` keeps reporting the ignored files as "deleted".
- If an `--update` reports many "deleted" files that still exist on disk, the scan stopped including them: check `.graphifyignore` before pruning anything.
- Communities are unlabeled ("Community N"); that is expected.
