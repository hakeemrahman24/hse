# hse-register

Merger of `hse-observation` and `hse-workpermitregister` into one project: one MariaDB
database, one Go binary that serves both the JSON API and a hand-built HTML admin
(no Django, no ORM). Built to run comfortably on an OrangePi 3B with 2GB RAM.

- **api/** — single Go service, two jobs in one binary:
  - JSON API: `handlers_workpermitregister.go`/`handlers_observations.go` serve
    `/api/workpermitregister*` and `/api/observations*` (unauthenticated, unchanged
    since the original merge).
  - HTML admin: `admin_observations.go`, `admin_users.go`,
    `admin_auth_handlers.go` render the login page and CRUD screens via `html/template`
    (`web/templates/`), gated by `auth.go`'s HMAC-signed cookie session
    (`requireAuth` middleware) — minimal CRUD only, no search/filter/pagination.
  - `-createadmin username:password` CLI flag bootstraps the first login (bcrypt-hashes
    the password and inserts into `admin_users`); run once per fresh database.
- **web/templates/** — `base.html` (shared shell) + one list/form pair per resource
  (work permit register, observations, users) + a standalone `login.html`.
- **web/static/register/** — the "clearwave" CSS carried over from the old Django
  static files; class names (`#result_list`, `.module.aligned`, `.object-tools`, etc.)
  are reused as-is so the templates needed no new styling.
- **migrations/** — plain `.sql` files, run by hand with `mysql <db> < file.sql`. Schema
  is no longer owned by an ORM:
  - `000_core_tables.sql` — `workpermitregister` / `observations_observation` (idempotent;
    these already exist on the Pi from the original Django deploy, only needed for a
    fresh database).
  - `001_admin_users.sql` — login accounts for the Go admin.
  - `002_drop_django_tables.sql` — drops Django's old bookkeeping tables
    (`auth_*`, `django_*`, the retired `usersettings_*` stub tables). Run manually,
    only after the Go admin is verified working.
  - `003_admin_users_full_name.sql` — adds `full_name` to `admin_users`.
  - `006_admin_users_role.sql` — adds `role` ('user' / 'admin') to `admin_users`; existing
    accounts become admin. Run it before starting the new build: login reads the column.
  - `007_reference_tables.sql` — lookup tables `ref_hazard_category`, `ref_topic`, `ref_keyword`,
    `ref_area`. They are filled on the first start from `migrations/seed/*.csv` (folder set by
    `REF_SEED_DIR`, default `migrations/seed`), only while empty. Areas are edited in Settings.
  - `008_observations_soc.sql` — new observation fields: `source` / `source_detail`, submitter
    (`employee_id`, `designation`, `company`), `topic_id`, `hazard_id`, `responsible_company`,
    `verification`, `created_by` (the staff username that entered the post); `category` becomes Unsafe Action / Unsafe Condition / Positive Finding (old values
    are mapped, `near_miss` and `suggestion` become Unsafe Condition); `severity` is dropped.
    Old rows keep source and verification empty. `location` now holds the Area.
  - `010_admin_users_archive.sql` — `archived_at` (archived accounts cannot sign in) and `must_change_password` (set by "Reset password"). Run by hand after 009.
  - `011_evidence_paths.sql` — `observation_files.stored_name` widened to 255 characters, because it now holds a relative path. Run by hand after 010.
  - `012_hazard_admin.sql` — lets administrators manage hazard classes, hazard categories and sources in Settings. Adds `ref_hazard_class` (filled from the classes already in use), `ref_source` (the four staff sources) and `ref_hazard_category.is_archived`, and turns `observations_observation.source` from an ENUM into `VARCHAR(80)` so new sources can be saved. Run by hand after 011, **before** deploying this build (the program reads these tables on every form).
  - `013_source_details.sql` — the type under a source (Internal Audit → Site Audit / HO Audit / Corporate Audit, External Audit → 3rd Party Company / Government) becomes a list managed in Settings > Source types: adds `ref_source_detail` and turns `observations_observation.source_detail` from an ENUM into `VARCHAR(80)`. The old "KL Audit" type is kept but archived, so older observations stay valid. Run by hand after 012, before deploying.
  - `014_app_setting.sql` — `app_setting` (name, value, updated_at), which holds the HSE Medpost address and token saved in Settings > Medpost API (the token is encrypted). Run by hand after 013. Until it exists the program uses `MEDPOST_URL` and `MEDPOST_DIRECTORY_TOKEN`, so the SOC form keeps working, but saving from Settings needs the table.
  - `015_soc_v8.sql` — SOC keyword list v8 (`SOC_Keyword_Improvement_Plan_v8.md`, `SOC_Form_Spec_v1.1.md`). Adds `ref_topic.label_id` / `high_potential` / `life_saving_rule`, the `ref_picklist` table and `observations_observation.topic_pick` / `suggestions_shown`. Run by hand after 014, **before** deploying this build (the program reads these columns on every form). The labels and picklists are filled on the next start from `migrations/seed` (labels only where still empty, picklists only while the table is empty).
  - `016_soc_v8_followup.sql` — `ref_keyword.suggested_count` / `picked_count` (how often a keyword helped suggest a topic shown to a reporter, and how often that topic was then picked; counted when a SOC post is first sent, not on edits) and `ppe_topic_id` (the hazard a PPE keyword implies, offered only when no hazard topic matched; filled on the next start from the seed). Run by hand after 015, before deploying.
  - `017_classic_five_classes.sql` — hazard classes return to the classic five (Physical, Chemical, Biological, Ergonomic, Psychosocial). The Safety and Environmental classes are removed and their 30 categories move to Physical; move them again in Settings > Hazard categories if wanted. Run by hand after 016, before deploying. The seed file has the same change.
  - `018_soc_wizard.sql` — the columns the SOC wizard fills: `path` (A to D), `unsafe_reason`, `ppe_item`, `stop_work`, `stopped_by`, `immediate_danger`, `barricaded`, `good_practice`, `control_topic_id` (the control tag set by a picklist answer, kept apart from the hazard `topic_id`) and `review_flags`. Run by hand after 017, before deploying.
  - `019_sub_areas.sql` — the second level of the area list: table `ref_subarea` (sub-areas under an area) and the column `observations_observation.sub_area`. Administrators edit them in Settings > Areas > Sub-areas (add, rename, delete; a rename also renames the sub-area on the reports that carry it, a delete leaves old reports as they are). The SOC wizard asks for the sub-area only when the chosen area has some; a QR code can skip it with `/soc/new?area=Camp&sub=Mess%20hall`. The report keeps the sub-area as text. Run by hand after 018, before deploying.
  - `009_observation_files.sql` — `observation_files` table: "before" and "after" files, each kind
    with up to 4 photos (JPEG, PNG, WebP or GIF, stored as JPEG of at most 500 KB) and up to 2 PDFs
    (5 MB each, staff only, scripts refused, always downloaded). Files live in `EVIDENCE_DIR`
    (default `data/evidence`). A visitor sees thumbnails of their own photos only.
  - `004_rename_permits_table.sql` / `005_rename_table_drop_prefix.sql` — the
    `WorkPermit` struct/table was renamed twice: first `permits_workpermit` →
    `permits_workpermitregister` (matching the `WorkPermitRegister` Go struct
    rename), then → plain `workpermitregister` (dropping the old Django app-label
    prefix once routes, handlers, and templates were renamed to match too). Both
    use `RENAME TABLE`, so existing rows survive.
- **systemd/** — one user-level unit (`systemctl --user`), no root required to run it.
- **deploy/setup-host.sh** — one-time native MariaDB database/user provisioning (needs sudo).

Port (see `.env`): `8111`, bound to `127.0.0.1` only — not meant to be reachable
directly; front it with Tailscale and/or a `cloudflared` tunnel.

## Why no Django

The project originally paired this Go API with a Django admin reusing the
"clearwave" template theme. Once asked directly why the stack was slower than
`hse-medpost` (which is pure Go), the answer was Django/Python itself — gunicorn
workers, ORM overhead, a Python venv to maintain on a 2GB board. The Django admin
was scrapped and rebuilt as plain Go + `html/template`, matching medpost's
architecture exactly (same HMAC cookie session pattern, same bcrypt login, same
`WorkingDirectory`-at-project-root convention for resolving `web/` paths).

## Deployment target

OrangePi 3B, 2GB RAM, eMMC storage. No Docker — native MariaDB, one static Go
binary, nothing else to run:

- Go binary: single static binary, `MemoryMax=80M` in the systemd unit — Go's
  `net/http` + `database/sql` + `html/template` footprint is a few MB at idle.
- MariaDB: runs natively on the host (not containerized) to avoid the memory
  overhead of a container runtime on a 2GB board.
- Networking: Tailscale for private access between your devices and this host;
  `cloudflared` tunnel for any public-facing endpoint. The binary never binds
  `0.0.0.0`, so nothing is exposed until one of those is wired up.

## Local dev setup

```
cd api
go mod tidy
GO_API_PORT=8211 go run .          # pick a free port if 8111 is in use locally
```

First time against a fresh database, apply the migrations and create a login:
```
mysql hse_register < migrations/000_core_tables.sql
mysql hse_register < migrations/001_admin_users.sql
cd api
go build -o hse-register-api .
./hse-register-api -createadmin admin:somepassword
```

Then browse to `http://127.0.0.1:8111/` (or whatever `GO_API_PORT` you picked) and log in.

Endpoints:
- `GET /health`
- `GET|POST /api/workpermitregister`, `GET|PUT|DELETE /api/workpermitregister/{id}`
- `GET|POST /api/observations`, `GET|PUT|DELETE /api/observations/{id}`
- `GET /`, `GET|POST /login`, `POST /logout` — admin session
- `GET|POST /workpermitregister`, `/workpermitregister/new`, `/workpermitregister/{id}/edit`, `/workpermitregister/{id}/delete`
- `GET|POST /observations`, `/observations/new`, `/observations/{id}/edit`, `/observations/{id}/delete`
- `GET|POST /users`, `/users/new`, `/users/{id}/delete`

All of the above except `/health` and `/api/*` require a logged-in session
(`requireAuth` middleware redirects to `/login`).

## Deploying to the OrangePi (actual runbook)

The board is a 2GB RK3566 (`aarch64`) — the Go binary is built **on the laptop**,
not on the board, so `go build`'s memory/CPU spike never touches the Pi.

1. **Cross-compile the Go binary locally** (laptop, not the Pi):
   ```
   cd api
   GOOS=linux GOARCH=arm64 go build -o hse-register-api .
   file hse-register-api   # sanity check: "ELF 64-bit ... ARM aarch64"
   ```
   Confirm the board's arch first if unsure: `ssh orangepi@orangepi3b uname -m`
   (should print `aarch64`).

2. **Copy the project over** (dry-run first to review the file list):
   ```
   rsync -avn ./ orangepi@orangepi3b:~/projects/hse-register/
   # drop -n once the file list looks right
   ```

3. **Fix permissions after rsync**:
   ```
   ssh orangepi@orangepi3b '
     chmod 600 ~/projects/hse-register/.env ~/projects/hse-register/db.env ~/projects/hse-register/admin-db.env
     chmod +x ~/projects/hse-register/api/hse-register-api ~/projects/hse-register/deploy/setup-host.sh
   '
   ```

4. **First deploy only — stop the old Django service and update the systemd unit**:
   ```
   ssh orangepi@orangepi3b '
     systemctl --user stop hse-register-django || true
     systemctl --user disable hse-register-django || true
     rm -f ~/.config/systemd/user/hse-register-django.service
     cp ~/projects/hse-register/systemd/hse-register-go.service ~/.config/systemd/user/
     systemctl --user daemon-reload
   '
   ```
   The updated unit's `WorkingDirectory` now points at the project root (not
   `api/`), matching medpost's convention — the binary resolves `web/templates`
   and `web/static` as relative paths from there.

5. **Drop Django's bookkeeping tables** (only after confirming the data in
   `workpermitregister`/`observations_observation` still looks right):
   ```
   ssh orangepi@orangepi3b 'mysql hse_register < ~/projects/hse-register/migrations/002_drop_django_tables.sql'
   ```

6. **Apply the new admin_users table and create the first login**:
   ```
   ssh orangepi@orangepi3b '
     mysql hse_register < ~/projects/hse-register/migrations/001_admin_users.sql
     ~/projects/hse-register/api/hse-register-api -createadmin admin:CHANGE_ME
   '
   ```

7. **Start the service and verify**:
   ```
   ssh orangepi@orangepi3b 'systemctl --user enable --now hse-register-go'
   ```
   Hit `/health`, then log into `/` over the same SSH/VSCode port-forward pattern
   already used for other services on this host (binds `127.0.0.1` only, by
   design — see "Deployment target" above).

8. **Only after step 7 passes**, retire the two older apps' services if they're
   still installed (left over from the original `hse-observation`/
   `hse-workpermitregister` merge, see git history for that cutover):
   ```
   systemctl --user disable --now hse-workpermit-django hse-workpermit-go
   systemctl --user disable --now hse-observation-django hse-observation-go
   ```

## Deploying an update (after the first deploy)

Shorter than the first deploy — no migrations, no systemd changes, just: rebuild,
copy, restart.

```
cd api
GOOS=linux GOARCH=arm64 go build -o hse-register-api .
rsync -av hse-register-api orangepi@orangepi3b:~/projects/hse-register/api/hse-register-api
rsync -av ../web/ orangepi@orangepi3b:~/projects/hse-register/web/   # if templates/static changed
ssh orangepi@orangepi3b '
  chmod +x ~/projects/hse-register/api/hse-register-api
  systemctl --user restart hse-register-go'
```
Overwriting the binary file while the old process is still running is safe on
Linux (the running process keeps its own inode until it exits) — the `restart`
is what actually swaps in the new code.

## Adding another admin user later

No UI-less bootstrapping needed after the first login exists — use the **Add user** button on the **User accounts** page in Settings (`/users/new`), or run the CLI flag again against
the live database if locked out:
```
ssh orangepi@orangepi3b '~/projects/hse-register/api/hse-register-api -createadmin anotheruser:theirpassword'
```

## Resetting the admin password

`-createadmin` is an upsert (`runCreateAdmin` in `main.go` uses
`ON DUPLICATE KEY UPDATE password_hash = ...`): if the username already
exists, it just updates the password hash instead of failing on the unique
constraint. So the same flag doubles as a password reset — run it from the
`api/` directory (it reads `.env`/`db.env` as relative paths, so a bare
one-liner from `$HOME` will fail with a database access error):
```
ssh orangepi@100.122.91.65 'cd ~/projects/hse-register/api && ./hse-register-api -createadmin USERNAME:NEWPASSWORD'
```
It prints `updated password for existing admin user "USERNAME"` when it
recognized an existing account, or `created admin user "USERNAME"` for a new one.

## Planned: user roles and self-service password change

Not implemented yet — current `admin_users` CRUD is create/list/delete only
(`admin_users.go`). Captured here as the requirement for the next pass:

- **Admin** can add, edit, archive, and delete users. New users get a default
  password of `123456` (changed at first login — see below).
- **Admin** can only *reset* another user's password (back to the `123456`
  default, or to a new one they set) — admin cannot see or set a user's
  password to something only that user knows.
- **User** can change their own password (requires knowing the current one,
  unlike an admin reset).
- This implies splitting the current single "admin" role into at least two
  tiers (admin vs regular user) on `admin_users`, likely a `role` column plus
  an `archived_at`/`is_archived` flag instead of hard delete, so "archived"
  users are distinct from actually deleted ones.

## Planned: simplified SOC (Safety Observation Card) submission form

Not implemented yet. The current `/observations/new` form (`admin_observations.go`,
`observations_form.html`) is the full admin CRUD form, with the same fields admins
use to edit any observation. The requirement is a **separate, simpler form** meant
for all employees (not just admins) to submit a Safety Observation Card — fewer
fields, less jargon than the admin form — but submissions still land in the same
`observations_observation` table as any other observation. This means:

- A second, **fully public, no-login route** (e.g. `/soc/new`) and template,
  distinct from `/observations/new` and *not* behind `requireAuth` — any
  employee gets a plain link to open and submit, no account needed. Both this
  route and the authenticated admin form ultimately call the same
  `observationFromForm`-style insert into `observations_observation`.
- No new table or schema needed — this is a form/UX split, not a data split.
- Because this route is public and unauthenticated (and will be reachable
  through the Cloudflare tunnel, not just Tailscale), it needs its own abuse
  protection: add **Cloudflare Turnstile** (their CAPTCHA) to the `/soc/new`
  form specifically. The authenticated admin routes don't need this — session
  auth already gates them — so Turnstile should be scoped to just the SOC
  form's server-side handler (verify the Turnstile token on submit before
  inserting), not applied site-wide.

## Knowledge graph (graphify)

This project has a graphify knowledge graph (`graphify-out/`) covering the Go
source and templates. Regenerate after structural changes with `/graphify --update`.

## SOC (public safety observation form)

Routes `/soc/...` are served by a **separate public listener** (`SOC_PUBLIC_ADDR`, default
`127.0.0.1:8112`), so the staff pages do not exist at the address the Cloudflare tunnel points to.
Flow: Cloudflare tick-box -> name search -> date of birth -> form -> thank-you page. Submissions
go into `observations_observation` with `source = 'SOC'` and `review_status = 'Pending'`.

Employees are found through hse-medpost's existing `GET /api/directory/employees` (bearer token).
Only people with a current Fit result are returned by that endpoint, so only they can sign in.
Settings: `MEDPOST_URL`, `MEDPOST_DIRECTORY_TOKEN` (real Medpost), `TURNSTILE_SITEKEY`,
`TURNSTILE_SECRET` (real Cloudflare check). `SOC_DEV_STUBS=1` fakes whichever of the two is missing
(development only; saving still writes to the real table, so run migrations 007 to 009 first).
Photos: at most 4 per post, compressed to 500 KB in the browser and again on the server (EXIF/GPS
removed); the same rules apply to the "after" photos on the staff edit screen. Optional env:
`SOC_TRUST_CF_HEADER=1` (use `Cf-Connecting-Ip`, public listener behind the tunnel only),
`SOC_COOKIE_SECURE=1` (set when served over HTTPS).

**Live setup (2026-10-17).** The public address is `https://hse-observasi.ayahaif.my.id/soc`: a
Cloudflare tunnel published application with path `/soc` and service URL `http://localhost:8112`.
On the Pi, `.env` holds `TURNSTILE_SITEKEY`, `TURNSTILE_SECRET` (a Turnstile widget for that
hostname), `SOC_COOKIE_SECURE=1` and `SOC_TRUST_CF_HEADER=1`; the Medpost connection is set in
Settings. If the page shows **502 Bad Gateway**, the listener is off: run
`journalctl --user -u hse-register-go -n 20 | grep SOC`. "listener is off" names the missing setting;
after fixing it, restart with `systemctl --user restart hse-register-go`. Tip: fish does not support
`<<EOF` heredocs, so open a shell on the Pi first (`ssh orangepi@orangepi3b`) before appending to `.env`.


## HSE Observation module (staff)

Home > HSE Observation opens a page with the same shell as the Medpost activity pages: a sidebar
with **Dashboard**, **Observations** (all, with the overall count) and the **sources**
(the sources with counts: SOC, Internal Audit, External Audit, Management Walkthrough, Project Monitoring Action Report, and "Not recorded" for old posts; a source with types, such as Internal Audit, expands to them), and the content on the right.

- `/observations` Dashboard: three blocks, each with Total findings / Open / In progress / Closed (click a number to open the matching list):
  **HSE recommendations status** (all findings), **SOC status** (source SOC; adds the positive-finding count and the top 10 hazard categories, areas and subcontractors) and **PMR status** (source Project Monitoring Action Report; same breakdowns). Subcontractor = the responsible company.
- Below the blocks, **Findings by company**: the 12 companies with the most findings against them (responsible company, all sources, same date filter), each with a pie of closed / in progress / open and the percentages. A company name opens its list (`?company=NAME`).
- `/observations/list` all posts; `?source=SOC` (or `none` for posts without a source) with `&type=Site+Audit`, `?class=Physical` (or `unclassified`), `?hazard=ID`, `?topic=ID` (a control topic), `?status=open`, `?verify=1` filter it. The hazard class, category and control topic are chosen in a filter row above the table, not in the sidebar.
- Date filter (From / To, on the date of the finding, both days included) on the dashboard and the list: `?from=YYYY-MM-DD&to=YYYY-MM-DD`. Either end may be left empty. It also drives the sidebar counts and is kept when you click a sidebar link, a chip or a dashboard number. Invalid dates are ignored; swapped dates are put in order.
- `/observations/{id}` the details of one post, its Evidence (before) and After files, and the actions.

Who can do what (`api/obs_permissions.go`, one place for pages and handlers):

| | Creator (staff who entered it) | Other staff | Administrator |
|---|---|---|---|
| Edit the fields and the Evidence (before) | until the post is closed | no | only old rows without a creator |
| Add After photos / PDFs, Mark In Progress, Close | yes | yes | yes |
| Verify a SOC post | n/a | yes | yes |
| Delete | no | no | yes |

A **Closed** post is locked for everybody: no edit, no files, no status change. Closing needs at
least one After photo or PDF (a Positive Finding is exempt). A post sent through the public SOC
form belongs to the employee (the submitter):

- They edit it from "My submissions" (`/soc/edit/{id}`) until HSE verifies it or it is closed.
- They can add After photos and **close it themselves** (`/soc/after/{id}`), even after it was verified.
  The same closing rule applies (one After photo, except Positive Findings). After PDFs stay staff-only.
- **Verification waits for HSE officers.** Any staff user can verify a SOC post, also after the
  submitter closed it. Verifying changes nothing else. A closed post that is not verified shows as
  "Closed, waiting for HSE verification" to the employee, and is counted in the dashboard's
  "waiting for verification" and the list chip of the same name.
- Staff add After evidence and close any post that is not closed (their own or someone else's).
- The add form takes the submitter's name from the login (read-only), has a "Remarks: action taken" field and an optional After evidence section. Its dropdowns are the custom searchable list in `web/static/combo.js`.

## Look and feel

The working pages (home, observations, Medical Activities) use the register's grey and indigo; Settings uses the warm brown palette of HSE Medpost's Settings so the admin area is easy to tell apart. The login page matches HSE Medpost's login card in position and size, in the grey and indigo, with the developer credit in the footer.

Templates are read when the program starts, so after changing anything in `web/templates/` restart `hse-register-go`; CSS and images under `web/static/` show up without a restart (bump the `?v=` number in the templates so browsers fetch the new file).

## Settings (administrators)

Settings has its own sidebar and the same warm brown palette as the HSE Medpost Settings pages, so the admin area is easy to tell apart from the grey and blue working pages. Sections: **User accounts** (under Staff & access), **Areas**, **Sources**, **Source types**, **Keywords**, **Hazard classes**, **Hazard categories**, **Medpost API** (under Integrations), and **My account** (everyone). Lists work the same way:

- **Add** a new entry. Rows are plain text; **Edit** opens a small form at the top of the page (as in HSE Medpost's Settings) to rename it. Renaming a class also renames it on its categories, and renaming an area or a custom source also renames it on the observations that carry it. In the Hazard categories list, Edit can also move a category to another class.
- **Archive** hides an entry from the observation forms but keeps it on existing observations, the sidebar and the filters. **Restore** brings it back. Archiving a class hides all its categories from the forms.
- **Delete** is offered only when nothing depends on the entry: a class with no categories, a category or source no observation uses. Otherwise archive it.
- The four built-in sources (Internal Audit, External Audit, Management Walkthrough, Project Monitoring Action Report) can be archived but not renamed or deleted, because the dashboard and the audit "Type" choices rely on their names. "SOC" is reserved for the public form.
- **Source types** are the choices under a source. When a source has at least one active type, staff must pick one on the observation form (for example Site Audit, HO Audit or Corporate Audit under Internal Audit; 3rd Party Company or Government under External Audit). A source with no active types asks for none.
- An observation keeps an archived source, source type or hazard when it is edited; it is refused on new posts.

### SOC wizard (`/soc/new`)

After sign-in the employee lands in a one-question-per-screen wizard (`SOC_Form_Spec_v1.1.md`). First screen: **Apa yang Anda lihat?** with four buttons.

| Path | Screens | Saved as |
|---|---|---|
| A Unsafe action | area, company, work + topic suggestions, what was unsafe (picklist), PPE item (only for the PPE answer), stop work, who stopped it (if yes) / "should it have been stopped?" (if no and the topic is high-potential), what was done on the spot, recommendation, photo | Unsafe Action |
| B Unsafe condition | area, area owner, condition + topic, immediate danger, barricaded or stopped, action on the spot, recommendation, photo | Unsafe Condition |
| C Safe observation | area, company, what was done well (picklist), work (optional) + topic, photo | Positive Finding |
| D Suggestion | area, what needs improving + topic, proposal, photo | Unsafe Condition (as before the wizard) |

The browser shows one screen at a time but posts one ordinary form; `validateWizard` (`api/soc_wizard.go`) checks every answer per path. A report is dated the day it is sent. Picklist answers (`ref_picklist`, from `SOC_Form_Picklists_v8.csv`) are posted as their position in the list and stored as their wording; the answer's control tag is stored in `control_topic_id`. `?area=<name>` (a QR code on site) fills the area and skips that question. The reporter's name, position and company always come from the sign-in.

`review_flags` tells HSE why a report needs a look: `topic not picked`, `company not in list` (a company typed outside the Medpost list), `urgent` (immediate danger and not barricaded; also an **Urgent** badge on the list and the post), `stop work not used on high-potential work`. The post page shows what the wizard captured. The list's control-topic filter looks at both `topic_id` (older posts) and `control_topic_id`.

On the phone: each answer is saved as a draft in the browser (`localStorage`, 24 hours, no photos) and restored on the next visit, with a "Mulai dari awal" link to drop it; the last area and company are remembered. The old single-page form is still at `/soc/form` and is what "My submissions" edits; new reports start at `/soc/new`. **Not built:** the offline queue (sending later when there is no signal), and a second-level area list.

### SOC topic suggestion (keyword list v8)

On the public form the system **suggests** and the reporter **picks**: while they type what they saw, up to three hazard topics appear as buttons (Indonesian `label_id`, never a category name), below them the full list and "Lainnya / tidak ada yang cocok". Nothing is selected for the reporter; a report sent with no topic is stored with `topic_pick = 'none'` and waits for HSE review. The staff form is unchanged and still fills the topic itself.

Matching (`api/classify.go`, plan v8 section 2): keywords of 4 letters or fewer match whole words, longer ones from the start of a word (the `match_type` of each keyword); a keyword that is its own root (bocor, buang) also catches affixed text (kebocoran, dibuang) through the Indonesian stemmer, a keyword that is already affixed (kedalaman) does not; a word that is not a real Indonesian word may differ by one letter from a keyword of 6+ letters, two for 10+, with the same first letter (words in `typoGuardWords` are never corrected); keywords of one topic with the same root count once, at the highest weight.

Every SOC post stores `topic_pick` (`suggested` = one of the three, `list` = another topic from the full list, `none`) and `suggestions_shown` (topic id and score), worked out on the server from the saved text. Top-1 / top-3 / "list" / "none" rates for the pilot are counted from these two columns. The picklists of the form spec (`ref_picklist`) and the topic's high-potential flag and Life-Saving Rule are loaded and available to the page code, but the 4-path wizard that uses them (unsafe-reason, PPE item, stop work, ...) is not built yet.

Settings > **Typo guard words** (administrators) is the list of real words that typo tolerance must never correct, one per line, saved in `app_setting` (needs migration 014); it adds to the built-in list in `typoGuardWords` and applies within the 5-minute list cache or at once after saving. When no hazard topic matches a SOC description, the PPE keywords that matched stand in for the hazard they imply (helm → Line of Fire) and are offered as a suggestion, never selected.

### Keywords (Settings > Keywords)

Administrators manage the words that suggest a topic and hazard while staff type an observation description. The left side has **Add keyword** (topic, keyword, *Starts with* or *Whole word*, weight 1–10), **Edit keyword** and **Try a description**, which shows what a sentence would suggest. The right side is one table row per topic with its **Class**, **Category** and keywords as a list separated by "; " (turned-off ones are dimmed); **Manage** opens that topic's keywords with Edit, Turn off/on and Delete. Changes apply at once. A topic cannot have the same keyword twice, in any letter case. No migration is needed; it uses the existing `ref_keyword` table.

### Medpost API (Settings > Medpost API) and Medical Activities

The register talks to HSE Medpost for three things: the employee check on the public SOC form, the responsible-company list, and the view-only **Medical Activities** page that every signed-in user can open (programs and their activities, with date, program and text filters; nothing can be changed from here).

- Administrators enter the Medpost **address** and **directory token** (the *eligible* token from HSE Medpost's Settings > Employee API). **Save and test** stores them and checks the connection; **Test only** checks what is typed without saving. An empty token field keeps the saved token.
- The token is saved **encrypted** (AES-GCM, key derived from `SESSION_SECRET`) and is never shown again. If `SESSION_SECRET` changes, the page says the saved token cannot be read and asks for it again.
- The address must be `https`, except `http://127.0.0.1` / `localhost` when both programs run on the same machine, because the token travels in every request. Redirects are never followed.
- Saved settings win over the server's `MEDPOST_URL` and `MEDPOST_DIRECTORY_TOKEN`; **Remove the saved settings** goes back to them. Changes apply at once to the company list, the SOC sign-in and the Medical Activities page. If the public SOC form was switched off at the last start because no address was set, restart the register once after saving.
- **Medical Activities** is laid out like Medpost's program page, in the register's own grey and indigo and view only. The sidebar lists the health programs, indented, each with a small triangle on the left that opens its **sub-headers** (for example Monthly Resume); click a sub-header to see its entries. What you opened is remembered for the browser tab. the **Activities** tab shows each activity as a card (date, title, summary) with its **photos** (click one to enlarge), its **proof files** (attendance list, report attachment: View or Download) and the place; the **Reference** tab lists the program documents with View and Download. Files are fetched by this application with the saved token and passed to the browser, so the browser never sees the Medpost address or the token; only signed-in users can open them. It needs the new routes in HSE Medpost, so **deploy hse-medpost first**.
- Medical Activities needs the new `GET /api/directory/activities` route in HSE Medpost (see its README), so **deploy hse-medpost first**. Against an older Medpost the page says it is not offered yet, and the test shows the same.

## User management (Settings > Users, administrators)

- **Add / Edit**: username, full name, role, and (edit) an optional new password. Renaming a user moves the posts they entered to the new name, so they keep the right to edit them.
- **Reset password**: sets the default password (`DEFAULT_USER_PASSWORD` in `.env`, at least 8 characters; built-in fallback `Welcome@HSE1`) and makes the person choose a new one at their next sign-in (they are held on My account until they do).
- **Archive / Restore**: blocks sign-in without deleting anything. Takes effect on the person's next click.
- **Delete**: shows a warning page first. The account is removed for good; the posts they entered stay but are detached from any account (only an administrator can edit them). A new account with the same name does not inherit them. Archive is offered as the safer choice.
- Guards: you cannot archive, delete, demote or reset yourself, and the last administrator who can sign in cannot be archived, deleted or demoted.
- Every request is checked against the account in the database, so archive, delete, demotion and role changes apply immediately, not when the 12-hour cookie expires.
- Everyone, admin or not, can change their own username and password at **My account** (`/account`). Administrators are not listed in the sign-in dropdown; they use "Type username".

## Navigation

- The top-left "hse register" logo is the way back to the home page. There are no tabs in the top bar and no Home entry in the sidebars; modules are chosen from the home page cards.
- **Work Permit Register** is still under development: its home card opens one blank "under development" page (`/workpermitregister`). Its web screens (list, add/edit form, handlers, templates) have been removed from the code; the permit JSON API `/api/workpermitregister*` and the `workpermitregister` table are unchanged.

## Evidence files: folders and names

Photos and PDFs are files under `EVIDENCE_DIR` (default `data/evidence`); the database keeps only a row per file (`observation_files`). They are laid out so a person (or a backup) can read them:

```
2026/10/OBS-000123/OBS-000123_before_photo_01.jpg
                   OBS-000123_before_pdf_01_scaffold-inspection.pdf
                   OBS-000123_after_photo_01.jpg
```

- `year/month` the observation was recorded, then one folder per observation, so everything sorts by date and by number.
- `OBS-<6-digit id>` is the observation id (the same number as the employee's `SOC-<id>` reference).
- `before` = evidence at the time of the finding, `after` = proof of the fix; then `photo` or `pdf`, a 2-digit running number, and for PDFs a short form of the uploaded name.
- A file is saved first with a random name, then moved to its readable place as soon as the database row exists (`api/evidence_names.go`); a file that cannot be moved keeps its old name and still works. Deleting a file or an observation also removes the folders it leaves empty.
- Files written before this change keep their old random names until you run `hse-register-api -organize-evidence` once (needs the same `.env` as the service); it is safe to repeat.
- Back up this folder together with the database: the images are not in the database.
