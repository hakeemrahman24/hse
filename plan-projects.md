# Projects Plan — hse-workpermit / hse-observation / medical-activities

Self-hosted stack on this OrangePi (`orangepi3b`), decided and built in this chat.

## Architecture

- **MariaDB** (native systemd service, already running) — one database per app.
- **Django** — owns the schema (migrations) and gives a free admin CRUD UI per app.
- **Go** — a separate lightweight service per app exposing a public JSON REST API, reading/writing the *same* MariaDB tables directly via `database/sql` (no ORM, no shared code with Django — the table names are the contract between them).
- **Deployment**: native `systemctl --user` services, not Docker. (Docker Hub is blocked/interfered with on this network — TLS handshake fails to `registry-1.docker.io`, and the configured mirror `docker.mirrors.ustc.edu.cn` is dead. Existing containers — nginx, pocketbase, portainer — were pulled before that stopped working.)
- Each Go/Django pair reads config from `.env` (non-secret: Django secret key, debug flag, ports) and `db.env` (DB credentials, written by the setup script, chmod 600).

## The three apps

| App | Django port | Go port | DB name | DB user | Model / table |
|---|---|---|---|---|---|
| `hse-workpermit` | 8101 | 8111 | `hse_workpermit` | `hse_workpermit_user` | `permits.WorkPermit` → `permits_workpermit` |
| `hse-observation` | 8102 | 8112 | `hse_observation` | `hse_observation_user` | `observations.Observation` → `observations_observation` |
| `medical-activities` | 8103 | 8113 | `medical_activities` | `medical_activities_user` | 8 tables, see below |

Go API routes (same shape for all three, swap the resource name): `GET /health`, `GET|POST /api/<resource>`, `GET|PUT|DELETE /api/<resource>/{id}`.

**`medical-activities`'s schema is bigger (8 tables) but the same shape applies to each.** It was migrated in this chat from a separate, more complete existing project (originally built for an ASUSTOR NAS; see `~/projects/medical-activities/README.md` for full detail — including the discarded full-app detour this went through before landing here). Current state:
- Go (`api/`) is a thin REST API, structurally identical to the other two apps' `main.go`/`db.go`/`handlers.go` — just one handler file per resource instead of one: `procedures.go`, `staff.go`, `supervisors.go`, `activities.go`, `evidence.go`, `documents.go`, `positionroles.go`, `users.go`. No auth, no session, no file-upload handling — plain JSON CRUD only, same as the other two apps.
- Django's models are **unmanaged** (`managed = False`) — they mirror a hand-applied SQL schema ([`plan/sql schema`](medical-activities/plan/sql%20schema)) rather than owning it via migrations. Django is admin/ops CRUD only here, not the schema authority (deviates from the other two apps, where Django owns the schema via normal migrations).
- Schema is 8 tables (`HealthProcedureTable`, `ClinicStaffTable`, `SupervisorTable`, `ActivityTable`, `EvidenceTable`, `DocumentTable`, `PositionRoleTable`, `AppUserTable`), applied by hand with `mysql ... < "plan/sql schema"`, not via `manage.py migrate`.
- Seeded rows carried over from the original project's seed logic: `admin`/`101112` and demo staff `ahmad.yani`/`101112` in `AppUserTable` (bcrypt hashes, from before the full app was discarded) — no login/auth actually enforces these anymore since the API has no auth layer; they're just seed data now.
- **Known gap vs. the other two apps' single-table model**: no file upload handling for `EvidenceTable.FileLink`/`DocumentTable.FileLink` — the API just stores whatever string is PUT/POSTed, it doesn't move files anywhere. The original project's storage convention is documented in `medical-activities/README.md` for reference if upload handling gets added later.

## Layout per app (`~/projects/<app>/`)

```
django_admin/     Django project + app + model + admin.py + migrations
api/               Go module (go.mod, main.go, db.go, handlers.go)
systemd/           two .service files (…-django, …-go), installed into ~/.config/systemd/user/
venv/              Python venv (not in git)
.env               Django secret key, debug flag, ports (git-ignored)
db.env             DB_NAME/DB_USER/DB_PASSWORD/DB_HOST/DB_PORT — written by setup-host.sh (git-ignored)
README.md          setup + run instructions for that app
```

## One-time host setup scripts (both need `sudo`, both already run)

- `~/projects/setup-host.sh` — installed `python3.11-venv`; created the 3 databases + scoped DB users; wrote each app's `db.env`.
- `~/projects/create-admin-user.sh` — created `projects_admin`@`localhost` with `ALL PRIVILEGES` on all three databases; wrote `~/projects/admin-db.env`. Used only for logging into Adminer to browse/manage all three DBs at once — not used by any app code.

## Running as services

All 6 services (`hse-workpermit-django`, `hse-workpermit-go`, `hse-observation-django`, `hse-observation-go`, `medical-activities-django`, `medical-activities-go`) are installed under `~/.config/systemd/user/` and enabled via `systemctl --user enable --now <name>`. To survive reboot/logout without an active session: `sudo loginctl enable-linger orangepi` (one-time, covers all of them — it's a per-user setting, not per-app).

Gotcha hit and fixed during setup: two Go services were stuck silently crash-looping because leftover manual `nohup` test processes were already squatting on their ports. `ss -ltnp | grep :<port>` is the way to check who's actually holding a port before assuming the systemd unit is serving it.

## Database management — Adminer

Docker was ruled out for this too (same registry block), so Adminer runs as a plain PHP script instead of a container:

- `~/adminer/index.php` (and `adminer.php`) — the single-file Adminer app, downloaded from `adminer.org`.
- Requires `php-cli` + `php-mysql` (installed via `apt`, one-time sudo step).
- Served by `~/.config/systemd/user/adminer.service` running `php -S 127.0.0.1:8091 -t ~/adminer` — bound to `127.0.0.1` only (not `0.0.0.0`) since it's a raw-SQL-capable tool; reached via the same VSCode port-forwarding used for everything else.
- Login at `http://orangepi3b:8091/`: server `127.0.0.1`, username `projects_admin`, password from `~/projects/admin-db.env`. Leave the database field blank to see all three databases and switch between them.

## Rotating a password

Editing an env file alone changes nothing — the real credential lives in MariaDB's own user table. Both steps are required, in order:

```bash
NEWPASS='...'
sudo mysql -e "ALTER USER '<db_user>'@'localhost' IDENTIFIED BY '${NEWPASS}';"
sed -i "s/^DB_PASSWORD=.*/DB_PASSWORD=${NEWPASS}/" ~/projects/<app>/db.env   # or DB_ADMIN_PASSWORD in admin-db.env
```

After rotating an app's DB user password, restart that app's services so they pick it up:
```bash
systemctl --user restart <app>-django <app>-go
```

## Known bug already fixed

All three Go `main.go` files originally called `godotenv.Load(".env", "db.env", "../.env", "../db.env")` — `godotenv.Load` aborts on the *first* file in the list that doesn't exist, so running from `api/` (where `.env`/`db.env` don't exist) silently skipped loading `../.env`/`../db.env` too, leaving `DB_USER`/`DB_PASSWORD` empty (`Access denied for user ''@'localhost'`). Fixed by loading each candidate path in its own `godotenv.Load(f)` call so one missing file doesn't block the rest.

## Status as of this chat

All three apps: Django migrated, superuser created (`ayahaif`, confirmed present on both `hse-workpermit` and `medical-activities`), all 6 systemd services active and verified end-to-end (Django `/admin/` → 302, Go `/health` → `{"status":"ok"}` for hse-workpermit/hse-observation; full CRUD cycle tested against `hse-observation`'s API). Adminer running and reachable. `hse-workpermit`/`hse-observation` models are a first-draft starting point — expect to adjust once real usage starts.

`medical-activities` was later replaced with a migrated, already-built project (real schema + real Go app) — see the deviation notes above and its own `README.md`. `ctags` and `Graphviz` are installed system-wide (used by `django-extensions`/`graph_models`, installed in each app's venv, for model ER diagrams if needed later).
