# hse-medpost

Consolidates `medical-activities`, `entry-mcu`, and a new EMR/inventory/screening system into one first-aid-post app. See `plan/hse-medpost-plan.md` for the full context and decisions — this file is just an orientation map for the folder layout.

## Running locally

```bash
PORT=8090 go run ./cmd/server
```

Defaults to the SQLite dev driver (`data/hse_medpost.db`, created and migrated automatically on first run). The app's own default port is `8080`, but on this machine that port is permanently occupied by an unrelated `nocodb` Docker container, so `PORT=8090` (or any free port) is required — open `http://127.0.0.1:8090` and log in with `admin` / `101112`. Override any other default via env vars (`DB_DRIVER=mysql` + `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` for MariaDB — see `internal/config/config.go`).

### Production runs MariaDB, not SQLite

The Pi uses **MariaDB** (`DB_DRIVER=mysql`). SQLite is only the local dev default, so passing tests and a working local run do **not** prove the SQL works in production. The two disagree in ways that have already broken a release: the `/sofi` name search used `LIKE ? ESCAPE '\'`, which SQLite accepts, but in MariaDB a backslash inside a string literal escapes the closing quote, so every search failed on the Pi (fixed by `ESCAPE '!'`).

When you write or change SQL:
- Write it so it is valid on both. Avoid backslashes in SQL string literals, SQLite-only functions and syntax (`INSERT OR REPLACE`, `strftime`, `AUTOINCREMENT`), and MariaDB-only syntax. Use `?` placeholders for every value.
- Schema changes need two files: the SQLite one (`migrations/sqlite/`, or a startup `Ensure…` function) and the MariaDB one (`migrations/mysql/`, run by hand).
- Before deploying SQL you have not seen run on MariaDB, run it against a MariaDB copy (`DB_DRIVER=mysql`, see above), not only SQLite.
- Log the real error server-side when a query fails. A generic "not available" message on the page hides which database it broke on.

## Deploying to the Pi

Production runs on the Orange Pi 3B (aarch64, MariaDB, systemd user service `hse-medpost`, port from `.env`, currently 8113). Public URL: `medical-activities.ayahaif.my.id` through the Cloudflare tunnel.

Pi layout (under `~/projects/hse-medpost` on the Pi):
- `hse-medpost` — the binary the service runs (`ExecStart`).
- `web/` — static files and templates, served from the working directory.
- `.env` — runtime config; never copy it, never print it.
- `hse-medpost.bak-<timestamp>` and `web.bak-<timestamp>` — rollback copies made by each update deploy (none exist yet on a fresh install).

Storage (moved 2026-10-26): the Pi's eMMC (`mmcblk0`) now holds only the OS. Everything that grows lives on the 256 GB NVMe, mounted at `/mnt/data` (UUID entry in `/etc/fstab`, `nofail`):
- MariaDB datadir is `/mnt/data/mysql` (set in `/etc/mysql/mariadb.conf.d/99-nvme-datadir.cnf`; a `mariadb.service.d/nvme.conf` drop-in makes MariaDB wait for the mount, so a missing SSD stops MariaDB instead of silently starting an empty database).
- `~/projects` is a symlink to `/mnt/data/projects`, so every path in this README, the systemd unit, and `deploy/deploy.sh` still works unchanged. `uploads/` and the `*.bak-*` rollback copies therefore live on the NVMe too. `deploy/deploy.sh` first checks that `/mnt/data` is mounted and `~/projects` resolves inside it, and aborts before sending anything if not (`DATA_MOUNT=` skips the check for a Pi without an NVMe). `deploy/prune-backups.sh` needs no change.
- Not moved: Docker data (nginx, pocketbase, portainer, nocodb), logs, and the `~/pre-move/` SQL dumps from the move.
- The move was done with `~/Projects/move-to-nvme.sh` (`check`, `apply`, `rollback`). Until the old copies `/var/lib/mysql.old` and `~/projects.old` are deleted, `sudo bash move-to-nvme.sh rollback` undoes it and syncs new writes back to the eMMC.

Connection notes (both flows below):
- SSH host alias is `orangepi3b` (key auth, host key already in `~/.ssh/known_hosts`). The Tailscale name `orangepi3b.tailf2cf40.ts.net` fails host-key checks from this machine, so use the short alias.
- If the shell is fish, wrap the commands in `bash -c '...'`: fish expands `*` and `--include=` globs differently.

### First deployment (new Pi / fresh install)

One-time setup — the Pi has no `hse-medpost` systemd service, directory, or `.env` yet. (Production already went through this once; this is for a replacement box or a from-scratch rebuild.)

**Step 1 — build and upload the binary, web folder, and systemd unit:**

```bash
# All deps are pure Go (modernc.org/sqlite, no mattn/go-sqlite3), but
# CGO_ENABLED=0 is the confirmed field-tested flag from the 2026-10-03
# deploy session (plan/hse-medpost-plan.md) — keep it explicit.
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/claude-1000/deploy/hse-medpost ./cmd/server

ssh -o BatchMode=yes orangepi@orangepi3b 'mkdir -p ~/projects/hse-medpost ~/.config/systemd/user'
scp -o BatchMode=yes /tmp/claude-1000/deploy/hse-medpost orangepi@orangepi3b:projects/hse-medpost/hse-medpost
rsync -a -e "ssh -o BatchMode=yes" web/ orangepi@orangepi3b:projects/hse-medpost/web/
scp -o BatchMode=yes systemd/hse-medpost.service orangepi@orangepi3b:.config/systemd/user/hse-medpost.service
```

**Step 2 — on the Pi, create `~/projects/hse-medpost/.env` by hand** (never copy a `.env` between machines) with at minimum:

```
PORT=8113
DB_DRIVER=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=hse_medpost_user
DB_PASSWORD=<generate a real password>
DB_NAME=hse_medpost
BASE_URL=https://medical-activities.ayahaif.my.id
SESSION_SECRET=<generate — never leave the dev default>
MCU_CERT_SECRET=<generate — never leave the dev default>
DIRECTORY_API_TOKEN=<generate — leave blank to disable the directory API until needed>
```

`internal/config/config.go`'s defaults (`dev-session-secret-change-me`, `dev-cert-secret-change-me`) are fine for local dev only — production must override `SESSION_SECRET` and `MCU_CERT_SECRET`, since config.go falls back to those literal strings when unset.

**Step 3 — create the MariaDB database, user, and base schema.** The sibling apps on this Pi (`hse-workpermit`, `hse-observation`, `medical-activities`) are provisioned by `~/Projects/setup-host.sh` (run once with `sudo`), which does `CREATE DATABASE ... CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci` + `CREATE USER ... IDENTIFIED BY <random password>` + `GRANT ALL PRIVILEGES` for each app — matching `plan/hse-medpost-plan.md` §2's own schema convention (`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`). **`hse-medpost` is not yet in that script's `APPS=()`/`DBNAMES=()` arrays** (plan Progress Log item 10, still open) — either add it there and rerun the script, or run the equivalent SQL by hand for `hse_medpost` / `hse_medpost_user` (matching the `.env` above). Either way, **there is no ready-made base-schema file for MariaDB in this repo** — `migrations/mysql/` only holds incremental `ALTER TABLE` migrations (011 onward) layered on top of a base schema that, per `migrations/001_sqlite.sql`'s own header ("Production NAS uses `plan/sql schema` (MariaDB)"), exists only as prose and inline `CREATE TABLE` snippets scattered through `plan/hse-medpost-plan.md` §2 — never as one runnable file. On the existing production box this schema was created once, by hand, long before this README existed; standing up a genuinely new box means first assembling and running that base schema (adapt `migrations/00{1..10}_*.sql`'s `CREATE TABLE` statements to MySQL syntax — `AUTO_INCREMENT` instead of `INTEGER PRIMARY KEY AUTOINCREMENT`, `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, no SQLite `CHECK` constraints per the plan's own convention — cross-checking against §2's corrections, e.g. `migrations/006_entrymcu.sql`'s header for the authoritative `McuRecord` columns), *then* running every file in `migrations/mysql/` in order on top of it. Treat this step as the highest-risk, least-tested part of a from-scratch rebuild.

**Step 4 — enable and start the service:**

```bash
ssh -o BatchMode=yes orangepi@orangepi3b 'cd ~/projects/hse-medpost
  loginctl enable-linger $(whoami)   # service keeps running after SSH logout
  systemctl --user daemon-reload
  systemctl --user enable --now hse-medpost
  sleep 3; systemctl --user is-active hse-medpost
  P=$(grep -E "^PORT=" .env | cut -d= -f2)
  curl -s -o /dev/null -w "root %{http_code}\n" http://127.0.0.1:$P/'
```

`data/uploads/` and `data/signing/` (the latter `0700`, holds doctor/CMO `.p12` signing keys) are created automatically on startup (`cmd/server/main.go`) — no manual `mkdir` needed.

**Step 5 — point the Cloudflare tunnel config** at `127.0.0.1:<PORT>` for `medical-activities.ayahaif.my.id` (already done for the current production box; only needed when standing up a replacement).

### Deploy an update (existing deployment)

Routine deploy once the service above already exists — from anywhere in the repo on the dev machine, pick one of two commands:

```bash
deploy/deploy.sh web     # only web/ changed (templates, JS, CSS): about 1 MB, no Go build
deploy/deploy.sh full    # Go code changed (anything outside web/): builds the arm64 binary and sends it too
DRY_RUN=1 deploy/deploy.sh full   # print every step without running anything
```

| | `web` | `full` |
|---|---|---|
| Builds the binary | no | yes (`GOOS=linux GOARCH=arm64 CGO_ENABLED=0`) |
| Sends | `web/` changes only | the binary (about 27 MB) plus `web/` changes |
| Rollback copies on the Pi | `web.bak-<TS>` | `web.bak-<TS>` and `hse-medpost.bak-<TS>` |
| Restarts the service | yes (templates are parsed at startup) | yes |

How it keeps the upload small: it first copies the running binary and `web/` to their staging names on the Pi (`hse-medpost.new`, `web.new`), then `rsync -z` sends only what differs, so a small Go change moves a few MB instead of 27, and an interrupted upload resumes. It finishes by swapping the new files in, restarting, and printing the service state, three HTTP status checks (`/` 200, `/emr` 303 and `/api/malaria-screenings` 401 when signed out) and the last two log lines. If unsure which to use, use `full`.

Both do exactly what the manual steps below do. Manual equivalent, for a machine without the script (the plain `scp` sends the whole binary every time):

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/hse-medpost-deploy/hse-medpost ./cmd/server     # skip if only web/ changed
scp -o BatchMode=yes /tmp/hse-medpost-deploy/hse-medpost orangepi@orangepi3b:projects/hse-medpost/hse-medpost.new
rsync -a --delete -e "ssh -o BatchMode=yes" web/ orangepi@orangepi3b:projects/hse-medpost/web.new/
ssh -o BatchMode=yes orangepi@orangepi3b 'set -e; cd ~/projects/hse-medpost
  TS=$(date +%Y%m%d%H%M)
  cp -p hse-medpost hse-medpost.bak-$TS
  cp -a web web.bak-$TS
  chmod 755 hse-medpost.new && mv hse-medpost.new hse-medpost
  rm -rf web && mv web.new web
  systemctl --user restart hse-medpost
  sleep 3; systemctl --user is-active hse-medpost
  P=$(grep -E "^PORT=" .env | cut -d= -f2)
  curl -s -o /dev/null -w "root %{http_code}\n" http://127.0.0.1:$P/'
```

Neither command runs database migrations. If the update adds a new `migrations/mysql/0NN_*.sql` file, take a `mysqldump` backup of the production database first, then run the migration by hand before (or immediately after) restarting — every file in `migrations/mysql/` says so in its own header, and the server does not apply MariaDB migrations automatically. See each migration's note under **Current status** below for which ones are still pending.

Rollback: on the Pi, `mv hse-medpost.bak-<TS> hse-medpost`, then replace `web` with `web.bak-<TS>`, then `systemctl --user restart hse-medpost`.

The Pi needs Ghostscript for upload compression: `sudo apt install ghostscript` (installed 2026-10-19). Check with `gs --version`. Without it the service still works and logs `pdf compress: ghostscript not installed` for each large PDF.

### Recompress PDFs already uploaded

`cmd/recompress-pdfs` shrinks PDFs that were uploaded before compression existed, the same way new uploads are shrunk. Build it with `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o recompress-pdfs ./cmd/recompress-pdfs`, copy it to `~/projects/hse-medpost/` on the Pi, then run:

```bash
cd ~/projects/hse-medpost && set -a && . ./.env && set +a
./recompress-pdfs            # dry run: lists what would shrink, changes nothing
./recompress-pdfs -apply     # copies each original to pdf-backup-<time>/, then replaces it
```

`-min` sets the size floor in bytes (default 2000000). Backups go in a folder beside `uploads/`, so the download-all zips never include them. A replaced file keeps its name, so no links change; its `EvidenceTable.FileHash` is updated to match. Run `-apply` when nobody is uploading, after a database backup. To undo one file, copy its backup back over it.

`deploy/prune-backups.sh` (run from cron on the Pi, weekly) keeps only the newest 5 of each `hse-medpost.bak-*`, `web.bak-*`, and manual pre-migration SQL dump, deleting the rest. Set `DRY_RUN=1` to preview without deleting.

### Verification (both flows)

The service is `active`, `/` returns 200, and a changed static file (for example `curl .../static/mcu.js | grep <new string>`) contains the new code. Unauthenticated `/api/...` calls should return 401, not 500.

Open items on the Pi (a deploy does not fix these):
- The nightly backup reads `DB_ADMIN_DATABASES` from `~/projects/admin-db.env`. It must not list `medical_activities`; the remaining list is `hse_workpermit hse_observation`.
- The service prints the admin username and password at startup (`journalctl --user -u hse-medpost`). Remove that log line in the code, and change the password.

## How this folder is organized

This top level *is* the target structure the finished app has — not a flat staging dump: `cmd/`, `internal/`, `web/`, `migrations/`, `plan/`, `systemd/`, `deploy/`, `data/`, matching `plan/hse-medpost-plan.md` §1. The legacy `entry-mcu`/`activity-log`/`medical-activities` projects that were ported *from* are no longer kept in this repo (the old `_reference/` zone is gone); `old-data/` holds only a stray legacy spreadsheet kept for a one-off historical lookup, not a reference tree.

## Current status

_This section is a snapshot, not a maintained log — it drifted stale for several sessions in a row. `plan/hse-medpost-plan.md`'s **Progress Log** (bottom of that file) is the actual source of truth for what's built and why; `internal/README.md` is the per-package status index. Treat anything below as "as of its last update," not current fact._

- **Built and verified**: `internal/{config,db,auth,middleware}` (infra), `internal/staff` (login/session, roster, position-role CRUD), `internal/activity-log` (programs, activities, evidence, documents, supervisors, sub-headers, bulk downloads incl. Excel export), `internal/company` (EntityType/Company/CompanyRelation), `internal/patient` (Patient/NationalityTable, shared by entrymcu/emr/screening), `internal/entrymcu` (McuRecord + lookups, validity-date calc, renewal cascade, PDF health certificates with disk persistence + audit trail, admin reporting/analytics + bulk Excel export as a two-sheet workbook — Patients, then MCU entries linked by PatientID — plus certificate zip, live `/mcu` clinical UI with name typeahead and suggested-next-criteria, and an admin Dashboard tab), `internal/screening` (offline Drug & Alcohol and Fatigue forms upload over bearer-token auth; the Medical Record page lists both with a date filter and an `.xlsx` export), `internal/presence` (generic "who's on this page" tracker, currently used by `/mcu`), `internal/storage` (upload path/filename conventions, shared by activity-log and entrymcu).
- **Company list** (Settings › Company details): add, edit, archive, and restore companies. Archiving hides a company from the pickers in other forms without deleting it, and existing checkups and relations keep their links. Delete still removes a company that nothing references. Names with a capital letter are kept as typed, so acronyms like PT, KSO, and SEI survive. An all-capital word longer than 6 letters is proper-cased (`STARWAYS` becomes `Starways`), and all-lowercase input is capitalized word by word.
- **Company relations** are keyed by `RelationCode` (for example `SEI-LNG`, `INDOCATER-LNG`), which is unique. The legacy sheet's R01–R48 codes are translated to these codes on import (`legacyRelationCodes` in `internal/entrymcu/oldsheet.go`). Client employees (entity type Client) have no relation, so their checkups are intentionally unlinked.
- **Database migration for the company archive**: on MySQL, `migrations/mysql/012_company_archived.sql` must be run by hand once before deploying this build. SQLite adds the column on startup (`EnsureCompanyArchivedColumn`).
- **Screening results typed on the web** (2026-10-21): Medical Record > Screening > Drug and Alcohol and Fatigue each have a Table and a Dashboard tab, and a button (**New drug & alcohol screening**, **New fatigue screening**) that opens a web version of the offline form (same sections, patient-name pickers, click-to-approve cursive signatures, Save and a PDF under 1 MB). Save posts to `POST /api/screening-log/alcohol` or `/fatigue` (signed-in session, once per record id). No migration: they use the tables above. Files: `web/static/da-form*.js`, `fatigue-form*.js`, `internal/screening/web_entry.go`, `web_fatigue.go`; jsPDF (MIT) is `web/static/vendor/jspdf.min.js` and the signature font Dancing Script (OFL) is `web/static/fonts/`.
- **Database migration for drug & alcohol uploads**: on MySQL, `migrations/mysql/013_screening.sql` and `migrations/mysql/015_alcohol_fields.sql` must be run by hand once before deploying this build (the table is renamed to `DrugAlcoholScreening` by `019`, see the next bullets). SQLite creates/updates the table on startup from `migrations/011_screening.sql` and renames an older database's `DrugAlcoholSubmission` first (`EnsureScreeningTableNames`). The offline Drug & Alcohol form posts to `POST /api/screening/login` and `POST /api/screening/drug-alcohol` (see `internal/screening`).
- **Database migration for the malaria screening form**: on MySQL, `migrations/mysql/022_visit_malaria.sql` (creates `Visit` and `MalariaScreening`) must be run by hand once, after a `mysqldump`, before the form is used; on the Pi `bash ~/projects/hse-medpost/migrate-022/run.sh` does the backup, a column-type check and the migration. The region data for its dropdowns is loaded separately by `indonesia-region/load.sh` (done 2026-10-20). SQLite creates both tables on startup.
- **Screening purpose column**: on MySQL also run `migrations/mysql/023_malaria_purpose.sql` by hand (after a `mysqldump`, before deploying the build that sends `purposeId`); it adds `MalariaPurposeTable` (1 On arrival, 2 Symptomatic) and `MalariaScreening.FK_PurposeID` (default 1, so saved rows become On arrival). SQLite upgrades itself on startup.
- **Own key for malaria tests**: on MySQL, after 023, run `migrations/mysql/024_malaria_id.sql` by hand once (after a `mysqldump`, before deploying): `MalariaScreening` gets `MalariaID` as its primary key and `FK_VisitID` becomes a normal indexed foreign key, so one visit (for example an outpatient fever visit) can hold more than one test, an RDT and then a smear. Saved tests keep their numbers, so Test IDs do not change. Test IDs are now made from `MalariaID`, and `PUT /api/malaria-screenings/{id}/result` takes it. SQLite rebuilds the table itself on startup.
- **SOFI fatigue questionnaire (`/sofi`)** (2026-10-21): a public page for every employee on file. Sign-in is the same as hse-register's SOC page: Cloudflare Turnstile tick, search the name, enter the date of birth. The form shows the identity block (name, date of birth, work position, company, age, MCU review status, and the typed weeks on site) and 25 words in five dimensions of five, rated 0 to 6; the server scores them (`internal/sofi/scoring.go`: dimension 0 to 30, total 0 to 150) and saves the row. The page shows one language, Indonesian (default) or English, with an EN/ID switch. Staff see the saved rows in Medical Record > Screening > Fatigue > SOFI Fatigue Survey (`GET /api/sofi-log`), and a SOFI section on the Fatigue Dashboard under the sleep-score charts (`GET /api/sofi-log/summary`: tiles for surveys, people and Low / Moderate / High, a stacked bar per dimension with its average, and a bar per company or position; it follows the dashboard's From / To dates and "Chart by"). **The Low / Moderate / High bands are provisional (equal thirds) until the doctor approves them; edit only the constants at the top of `scoring.go`.** The Indonesian explanations of the words are the clinic's own. On MariaDB run `migrations/mysql/025_sofi.sql` by hand once, after a `mysqldump`, before deploying (SQLite does it on startup, `015_sofi.sql`). `/sofi` needs `TURNSTILE_SITEKEY` and `TURNSTILE_SECRET` in `.env` (and `SOFI_TRUST_CF_HEADER=1` behind the tunnel); without them the page stays off and the log says why. Keep the SOFI templates in `web/templates/sofi/`: the staff server parses every `web/templates/*.html` and would fail to start on them.
- **Database migration for fatigue scoring uploads**: on MySQL, `migrations/mysql/014_fatigue.sql` must be run by hand once before deploying this build (the table is renamed to `FatigueScreening` by `019`). SQLite creates it on startup from `migrations/012_fatigue.sql`. The offline Fatigue Scoring form posts to `POST /api/screening/login` and `POST /api/screening/fatigue`. Both screening types are listed on the Medical Record page (Screening > Drug and Alcohol / Fatigue). The old per-program `Test log` tab, `web/static/test-log.js` and `GET /api/test-log*` were removed on 2026-10-21; the `TestLogKind` / `TestLogTitle` columns of `HealthProcedureTable` stay in the database, unused (dropping them is an optional manual migration).
- **Database migration for the form-result renames and sleep hours** (2026-10-20): the two result tables are now `DrugAlcoholScreening` and `FatigueScreening`, because they hold every test whether done in the clinic or at a worksite. Production already has `013`–`018`; this build also needs `migrations/mysql/019_rename_screening_tables.sql` (the rename; try it on a restored copy first, it ends with a check query) and then `migrations/mysql/020_fatigue_sleep_hours.sql` (adds `Sleep24h`, `Sleep48h`, `AwakeHours`, the raw hours the fatigue form has always sent), run by hand, once, in that order, after a `mysqldump` and before deploying. SQLite does both on startup. Timestamps from the forms are stored as UTC `YYYY-MM-DDTHH:MM:SS` (`dates.NormalizeTimestamp`); an unparsable `created_at` makes that record invalid, so the form keeps it queued.
- **Activity summaries** (`/programs`, Activities home and each program feed): the Summary field is a small document. Type a paragraph, then use the • / 1. toolbar to turn the current line (or a selection) into a bulleted or numbered list, and press Enter on a blank item to go back to a paragraph. The saved text stays plain (`- item`, `1. item`). The feed renders those lines as real lists (`web/static/summary-format.js`).
- **PDF uploads** (activity attachments): a PDF over 1 MB is rewritten when it is uploaded: images downsampled to 150, then 120, then 100 dpi (never lower, so scans stay readable) until it is under 1 MB, transparency flattened, forms and annotations printed into the page. The first pass that gets under 1 MB wins. It needs Ghostscript (`gs`) on the server; without it, or if the run fails, times out, or changes the page count, the upload is kept as sent. Photos and PDFs already under 1 MB are not touched. `EvidenceTable.FileHash` is the hash of the stored (compressed) bytes (`storage.CompressPDF`, `internal/storage/compress.go`). **Download all uploads** splits the zip into parts that stay under 10 MB counting zip headers; a single file larger than that still gets a part of its own (`internal/activity-log/downloadall.go`).
- **Employee** (`/mcu`, the page opens on this tab): one row per person, their latest checkup. Columns are Name, Company, Work position, Latest medical ID, Latest med status, and Medical validity status. Clicking the name opens that person's details under the row and hides the other rows; clicking it again closes them. Today's review, the certificate zip, Excel export, and Clear cache stay on MCU data. When the window is too narrow for every column, the Employee list and the MCU data list scroll sideways on a slim scrollbar.
- **Checkup form**: New patient, New checkup, Edit checkup, and Edit employee replace the list. The title becomes `Medical Checkup | …`, and the words after the bar are brick. Save and Cancel sit in a bar just above the disclaimer. New patient Save creates the person, then that checkup. Date of birth, review date, and date of checkup show as `dd/mm/yyyy` and are stored as `YYYY-MM-DD`. Review date starts as today. Date of checkup starts blank. Company and work position at this visit start ticked as Same as previous; typing in either box unticks it. The patient form has no company or work position field: `Patient.FK_CompanyID` and `Patient.WorkPosition` are derived from the latest visit that has each (by review date) and are recomputed whenever a visit is saved, edited, deleted or imported, and once at startup (`db.SyncPatientFromLatestVisit`). A visit's own company and position are never changed by this, so certificates keep what was recorded at that visit. A new patient has neither yet, so those lines are hidden in their details, Same as previous is hidden for both, and what is entered on their first visit becomes theirs. Save requires the employee identity fields (name, gender, date of birth, nationality) plus checkup purpose, work location, company at this visit, work position at this visit, date of checkup, and result.
- **MCU data** (`/mcu`, MCU data tab): every visit, not only the latest. Click a checkup ID to open the full checkup under that row (worker, exam, and result; every exam section starts open). That ID turns red and bold. Click the patient name to close the checkup and open only the worker details, with the same highlight on the name. Click the ID or name that is already open to close the row. The Certificate column says **View**. Unfit and Temporarily Unfit are brick red; Fit and Fit with note keep the normal link color. Both tabs filter by name and company; there is no Parent filter (the Parent / Project / Depth columns stay in the Excel export). **Export to Excel** is `GET /api/mcu-records/mcu-data.xlsx` (any logged-in user): one flat sheet with the checkup columns plus gender, nationality, blood type, employee ID, and Parent / Project / Depth from Company relations. Several relations for one company stay on that checkup row, joined with ` / `. The admin two-sheet workbook (`GET /api/mcu-records/export.xlsx`) is unchanged.
- **Exam choices**: Smoking/vape is Yes or No. ECG and Treadmill are Normal, Acceptable - monitor, or Unacceptable. An older stored ECG or treadmill phrase still shows when that visit is opened. Fit locks Result notes and Recommendation / follow-up, and both stay empty. Fit with note, Temporarily unfit, and Unfit require Result notes and leave follow-up locked and empty. Need follow up and Incomplete lock Result notes and require Recommendation / follow-up. The server stores a locked field as empty.
- **Checkup status and certificates**: Fit, Fit with note, and Unfit close the checkup. Temporarily Unfit, Need follow up, and Incomplete stay open. Status is computed on save; existing rows are not rewritten. Unfit is Archived / Exit, and choosing it shows a red note on the form. The PDF stays black. For Temporarily Unfit and Unfit, only the Fitness Status value is red, and the page says it is not a clearance to work. The QR verification page flashes red for those two results. Fit and Fit with note keep the normal certificate.
- **MCU dashboard** (`/mcu`, Dashboard tab, admin): opens on this month. Reset clears the dates and the company and loads every review. This month looks like a button only while that range is filled; Reset is highlighted whenever a date is filled. Bar/Pie sit with Overall/Per company (pie only). Per company changes the Purpose and Review result tables and charts. The Open/Close table stays the company pivot and ends with a Share of total row. The on-screen count is just "N records". Export PDF (`GET /api/mcu-reports/summary.pdf`) always prints that overall layout, even if the screen is on Per company: table on the left, chart on the right, percentages on the chart. A section that would cross the bottom margin starts on the next page. Charts are vectors in `internal/entrymcu/dashboard_pdf.go` (`github.com/go-pdf/fpdf`); the screen uses inline SVG. Each person counts once, by their newest checkup in the chosen dates (later review date wins, ties go to the higher record ID), so a person with an earlier open record and a later closed one is not counted twice. The newer record must also be inside the range, so a past month does not change when the person is seen again. Review result counts every checkup that has a result, open or closed, so Need follow up and Temporarily unfit sit next to the closed outcomes (a closed Need follow up is still left out of every chart). Purpose shows Fit for Task, Follow Up, and ConsultationResult together as **Consultation result**; stored names are not changed. Bars line up with the rows of the table beside them. The PDF uses the same counts (`dashSupersededSQL` and `dashPurposeLabel` in `internal/entrymcu/dashboard.go`).
- **Occupational health risks** (checkup form, optional): a trigger word in Work position ticks the matching box. Words are stored on `TaskCategoryTable.TriggerWords` and edited in Settings. The active list adds Seafarer, Confined space, and Respirator or SCBA, and renames Health Professionals to Health care worker. Other Professional (Pilot, Seafarer, Diver), Remote Site Visitor, and the old combined confined-space row stay archived so old checkups still show those names.
- **Staff accounts** (Settings, admin): Edit changes name, position, role, username, and password. Role is a dropdown; changing the position suggests that position’s role and the chosen role is what is saved. The built-in username `admin` is omitted from the staff list, the supervisor list, and the login username picker. The Administrator position is omitted from Positions & roles. Each person still changes their own password with Change password.
- **Phone layout** (2026-10-28): below 900px pages with a sidebar (`/emr`, `/programs`, settings) stack the sidebar above the panel. `.app-shell:has(> .app-sidebar)` uses `align-content: start` in `web/static/clearwave/app.css` so the rows sit together instead of splitting the shell's minimum height into a gap. `mcu.html` has no sidebar and is left out on purpose. Not yet deployed.
- `web/templates/{login,start,settings,programs,mcu}.html` + matching `web/static/*.js` are live pages, not placeholders. `web/templates/{emr,inventory}.html` are still the "under development" placeholder pages (behind `RequireAuth`).
- **Still empty**: `internal/{emr,inventory}`. See `internal/README.md` for what each will own.
- Run locally and confirm: `go build ./...` + `go vet ./...` clean; `admin`/`101112` logs in; `/programs`, `/mcu`, `/admin/settings` all load and their APIs respond.

## Employee search API

Other databases read an employee plus the latest MCU review from this app. The caller is that database’s server. Opening the address in a browser does not send the token.

https://medical-activities.ayahaif.my.id/api/directory/employees

https://medical-activities.ayahaif.my.id/api/directory/employees?q=andi

### GET /api/directory/employees

Live address: `https://medical-activities.ayahaif.my.id/api/directory/employees`. Send `Authorization: Bearer <token>`. Search with `q` (at least two letters), or load one person with `checkUpId`, `patientId`, or `q` plus `dob` (`yyyy-MM-dd`). The list is at most 20 people whose latest closed checkup is still valid and is Fit or Fit with note.

### GET /api/directory/fitness

Same token. Returns every Fit and Fit with note checkup whose standing is not `ReEvaluate / Overdue` and not `Archived / Exit`. A lapsed date is excluded even when the stored label is still Valid. Each row matches the employee search fields and adds `hcValidityStatus`.

### GET /api/directory/activities

Same (eligible) token. Returns program activities, newest first, for the register's read-only **Medical Activities** page. Each row has only `activityId`, `program`, `type`, `title`, `summary`, `date` (YYYY-MM-DD) and `location`; notes, the staff member's name, photos and documents are never included, and entries inside a program sub-header are left out (as in Medpost's own activity feed). Optional `from` and `to` (YYYY-MM-DD) bound the date, and `limit` caps the rows (default 200, at most 500). A missing or wrong token returns 401, and 503 if no token is set.

### Activity photos, proof and program documents

Same (eligible) token, read-only, for the register's Medical Activities pages.

- `GET /api/directory/subheaders` lists the active program sub-headers (`subHeaderId`, `program`, `name`); `GET /api/directory/activities?subheaders=1` also returns the entries of active sub-headers, each with its `subHeaderId`.
- `GET /api/directory/activities?files=1` adds `photos` and `proof` (name, type, date, evidence id) to each activity in one call.
- `GET /api/directory/activities/{id}/files` returns `{photos: [...], proof: [...]}`; each file has `evidenceId`, `type`, `name` (the stored file name only, never a server path) and `date`. Who uploaded it is not shared. Demo placeholder photos are left out. Unknown activity: 404.
- `GET /api/directory/evidence/{id}` streams one of those files.
- `GET /api/directory/documents` lists the program documents (`documentId`, `program`, `type`, `title`, `notes`, `formLink`, `fileName`); `GET /api/directory/documents/{id}` streams the file.
- Files are sent with `nosniff` and `Content-Security-Policy: sandbox`; types a browser cannot show (docx, xlsx, svg) are sent as downloads. 401 without the token, 503 if none is set.

### GET /api/directory/checkups

Requires the all-checkups token. Returns every checkup, including exam sections, result, notes, and company parent / project / depth. `hcValidityStatus` is the standing a reader sees.

The eligible token and the all-checkups token are separate. A database that has only the eligible token can call the employee search and `/api/directory/fitness`. It cannot call `/api/directory/checkups`. Settings → Employee API shows both. Update each one on its own button. The token already stored for other databases stays the eligible token.

### Employee API response

Each row is identity (name, date of birth, gender, nationality, blood type, employee id, company, company entity, parents, work position) plus `checkUpId`, `dateCheckUp`, `resultCriteria` (`Fit` or `Fit with note`), `resultValidUntil`, and `resultNotes`. Exam sections are not included.

### Employee API token

Settings → **Employee API** (admin only) shows the token. **Update token** replaces it immediately. Every caller still using the old value, including the entry-permit script property `MCU_DIRECTORY_TOKEN`, is rejected until they receive the new one. `DIRECTORY_API_TOKEN` in the server environment is copied in only when no token is stored yet. `GET` and `POST /api/directory/token` are the admin session routes behind that settings button.

## Next (not started)

The health-certificate layout, the flame logo, the Chief Medical Officer signature block, and QR verification are in place. See the 2026-10-03 entry in `plan/hse-medpost-plan.md`.

- Audit the code and the database structure and write a recommendation.
