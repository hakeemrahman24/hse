# Deploy and cutover runbook (slice 8)

Status: written 2026-11-07, **not run**. Nothing here touches the Pi until the owner says go. Steps marked **[Pi]** are outward-facing. Steps marked **[local]** can be rehearsed on the dev machine first.

## 0. Facts this runbook relies on

- One Orange Pi, one MariaDB server, **two databases**: medpost's own, and `entry_permit`. They must never share a database. Reason: separate users and grants, so a bug or an import in one cannot touch the other's patient and checkup records. (Not a name clash: medpost's table is `Company`, entry-permit's is `company`, which are different tables while MariaDB has `lower_case_table_names=0`, the Linux default. Check this on the Pi in section 3, because with `1` they would be the same table.)
- `deploy/setup-host.sh` already creates `entry_permit` plus `entry_permit_user`, with `GRANT ALL` on `entry_permit.*` only. The user has no rights on medpost's database.
- Ports: medpost 8113, entry-permit 8114 (check `ss -ltnp` before starting). Each has its own user systemd unit, folder, `.env` and upload tree. Entry-permit reads medpost only through the loopback HTTP API.
- Companies: entry-permit holds a read-only mirror, refreshed from medpost's `GET /api/directory/companies` (nightly, or admin "refresh now"). Medpost is the only place a company is edited.
- Migrations run by themselves when the service starts. `.env` is never copied by `deploy.sh`.

## 1. Before the Pi (all local)

1. Medpost: all tests green (`go test ./...`), changes committed. Needs the `includeExpired` option and the `/companies` route, both already committed in `80a582b`.
2. Entry-permit: `go vet ./...` and `go test -race ./...` green.
3. **[local] Rehearse everything in sections 3-5 against a throwaway MariaDB** (as was done for the importer), including a restart and the second import run, which must change nothing.
4. Owner decisions (see section 9) are answered or consciously deferred.

## 2. Medpost goes first **[Pi]**

Entry-permit sends `includeExpired=1` and calls `/companies`. A medpost without them ignores the flag, so workers with an expired checkup would still read "could not find", and the company sync would get a 404.

1. Deploy medpost with its own `deploy/deploy.sh full`, from the medpost project.
2. Check `GET /api/directory/companies` with the directory token (expect 200 and a JSON list).
3. Create a directory API token for entry-permit in medpost if none exists. It goes only into entry-permit's `.env` as `MEDPOST_API_TOKEN`.

## 3. First setup of entry-permit on the Pi **[Pi]**

0. On the Pi: `mariadb -e "SHOW VARIABLES LIKE 'lower_case_table_names'"`. Expect `0`. If it is `1`, tell the owner before going on.
1. Run `deploy/setup-host.sh` on the Pi once. It makes the NVMe folders, the database and user, and a first `.env` with a generated password and session secret (not printed). Safe to rerun, never overwrites `.env`.
2. Fill `.env` by hand. `setup-host.sh` leaves these out or blank:
   - `BASE_URL` (the real public address)
   - `MEDPOST_API_TOKEN`
   - `GATE_WORKER_URL`, `GATE_SHARED_SECRET`
   - `TURNSTILE_SITEKEY`, `TURNSTILE_SECRET` (leave both empty and the "My permit" lookup is switched off). `TURNSTILE_DEV` must stay empty.
   - `TURNSTILE_HOSTNAMES` (optional, comma separated, for example `orangepi3b.tailf2cf40.ts.net,z.ayahaif.my.id`): the page hostnames whose security-check answers this server accepts. The same names must also be in the Turnstile widget's own hostname list (Cloudflare dashboard, Turnstile, the widget, Hostname management); a bare machine name or a `100.x` address cannot be added there, so reach the site by its `ts.net` or real domain name. Leave it empty to accept any hostname the widget allows (the check is then the plain one). Once set, a hostname missing from it makes every check fail, so add the new address here before switching to it at cutover.
   - `TRUST_CF_IP=1` once the site is reached through Cloudflare, so rate limits use the real visitor address.
   - `SESSION_SECRET` must not be the dev default: in MariaDB mode the server refuses to start with it.
3. Copy `systemd/entry-permit.service` to `~/.config/systemd/user/`, then `systemctl --user daemon-reload && systemctl --user enable entry-permit`. Run `loginctl enable-linger` for the user if it is not set, so the service survives logout.
4. From the dev machine: `deploy/deploy.sh full`. Expect `active`, `healthz 200`, and a clean journal. It keeps `entry-permit.bak-<TS>` and `web.bak-<TS>` for rollback.

## 4. Load the data **[Pi]**

Order matters: companies first, because permits and the pickers refer to them.

1. Entry-permit pulls the company list from medpost by itself when it starts, so after the first start `/staff/admin/companies` already shows a count and a "Last refreshed" time. Check it, and press **Refresh now** once (expect "Already up to date"). If the count is 0 or it fails, fix section 2 first.
2. Reference lists: `refseed load <list_key> <file.tsv>` for each list (safe to repeat). Staff accounts and PINs: `staffctl add ...`. The PIN source stays under `data/seed-private/` and is never read into a chat.
3. Copy the owner's spreadsheet (today `reference/LayarEntryPermit_Revamp.xlsx`; `data/sheet-snapshot/` is empty) and `data/drive-files/` (about 1 GB) to the Pi, outside git. Both hold personal data.
4. `import-sheets --file <xlsx> --dry-run`, read the counts against the acceptance numbers (permits 1,009 of 1,010, waivers 19 of 19, tickets 896 of 897 on the last rehearsal). Rows that cannot import are listed with a reason.
5. Real run: `import-sheets --file <xlsx> --fill-valid-until`. Run it again: the second run must report no changes. Exit status 1 is expected while the one permit with a typo date is still rejected.
6. `import-sheets --file <xlsx> --files <drive-files> --dry-run`, then without `--dry-run`. About 60 ID and photo files are ambiguous and stay unattached by design, listed by request id.
7. Sample check: 20 permits per type render the same card and the same dashboard bucket as the sheet.

## 5. Gate sync **[Pi]**

The scan log and gate state are not imported. The gate Worker holds them and the Pi pulls them at the first sync.

1. Set `GATE_WORKER_URL` and `GATE_SHARED_SECRET`, restart, and watch the first pull in the journal.
2. Compare gate counts (scans, people currently inside) against the Worker's own numbers.
3. Run the gate end-to-end test: approve a permit, scan at the gate, check in and out. This is the item the old notes list as never fully proven.

### 5a. Building the gate Worker in Cloudflare (new account, or a second copy beside the live one)

Not run yet. The Worker needs: one Worker (today `ayahaif-revampedgate`), one D1 database bound as `gate_db` (today `entrypermitrevamp`), the `public/` officer page, and one secret `SYNC_SECRET`. All commands run in `gate-worker/`.

1. `npm install`, then `npx wrangler login` with the **target** account (`npx wrangler whoami` shows which one is active).
2. `npx wrangler d1 create entrypermitrevamp`, then copy the printed `database_id` into `wrangler.jsonc` (`d1_databases[0].database_id`). A copy in the **same** account needs a different database name and a different Worker `name`, and the `d1:schema` script must use the same database name.
3. `npm run d1:schema` (idempotent, builds the tables from `schema.sql`). Skip it if a snapshot is imported in the optional step below, because the export already carries the tables.
4. `npx wrangler secret put SYNC_SECRET` with a **new** random value. The same value goes into the Pi `.env` as `GATE_SHARED_SECRET`. A Worker without it refuses every sync call.
5. `npm run deploy`. Wrangler prints the `*.workers.dev` address: that is the officer page address and the Pi's `GATE_WORKER_URL`.
6. Check: `GET <address>/api/references` returns 200 with names only, and a sync call with a wrong secret returns 401.
7. Set `GATE_WORKER_URL` and `GATE_SHARED_SECRET` on the Pi, restart, watch the first push in the journal, then run step 3 above.

Optional snapshot of the live data (the old Worker keeps working, the export only reads): on the old account `npx wrangler d1 export entrypermitrevamp --remote --output=data/gate-d1/dump.sql`, then on the new account `npx wrangler d1 execute <new name> --remote --file=data/gate-d1/dump.sql`. `data/` is gitignored because the file shows who entered and left the site. It is a snapshot: scans after the export are not in it, so the cutover (section 6) needs a fresh export and import after the freeze.

Keep the two Workers apart: only the Pi talks to the new one, and the old Apps Script keeps the old Worker and its old secret. Turnstile keys belong to the account that made them, so make new ones in the target account. **Not verified:** whether a fresh, larger snapshot at cutover duplicates rows the Pi already pulled during a rehearsal (the pull is idempotent per the Worker README, but the Pi's sync code was not checked).

## 6. Cutover **[Pi]** (needs the owner's explicit go)

1. Put the app on a new tunnel hostname and let internal users try it against the copied data.
2. Freeze window: set the old sheet to read-only, run the importer once more, repeat the gate test.
3. Repoint `z.ayahaif.my.id` to the new app. Leave the old Apps Script URL working for one release, showing a "moved" page, so printed and emailed LEP QR codes keep working (compatibility routes `?page=ticket&lep=...` and `?page=card&id=...` are already in the app).
4. **Rollback:** repoint the short URL back and unfreeze the sheet. Anything written to MariaDB during the window must be exported back by a one-off script written **before** cutover, not after.
5. Per-binary rollback: restore `entry-permit.bak-<TS>` and `web.bak-<TS>`, then restart the unit. Not yet checked: whether every migration is safe for an older binary to run against (a column added by a newer migration). Read the migrations since the last good deploy before relying on this, and take a `mysqldump` of `entry_permit` before each deploy that adds one.

## 7. Backups before every deploy that adds a migration **[Pi]**

`deploy.sh` keeps the old binary and `web/`, but it does **not** back up the database or the uploads. Before the import and before each later deploy: `mariadb-dump --single-transaction entry_permit > entry_permit-<TS>.sql` (rehearsed: 1.4 MB, restores with identical row counts), and copy or `rsync` the uploads folder (about 525 MB after the file import), because the dump holds only the paths.

## 8. Rehearsal results (2026-11-07, local, nothing on the Pi)

Run on a fresh MariaDB 13.0 (own data folder, port 3307, own socket; the machine's own MariaDB on 3306 untouched), with the real medpost code on SQLite as the company source, and the real spreadsheet and Drive files.

- **Databases:** `entry_permit_user` is denied on medpost's database and the reverse. A first start on an empty database created 25 tables and left the medpost database empty.
- **Companies:** 54 pulled from medpost's `/companies` at first start; "Refresh now" says "Already up to date"; a wrong CSRF token gets 403. The route returns 401 without the token.
- **Import:** a default run imports 991 permits, 19 waivers, 884 tickets and rejects 19 permits and 13 tickets. 18 of the permits have a blank valid-until date and 1 has a typo (`07-08-027`). A second run with `--fill-valid-until` brings it to **1,009 / 19 / 896** (1 permit and 1 ticket still rejected), and a third run changes nothing. The command exits with status 1 while any row is rejected, so a script will not read it as success.
- **Files:** dry run writes nothing. The real run copies 3,038 files (525 MB): ID 949 of 1,009, photo 932, visa 124 of 126; ambiguous ones are listed and left unattached (59 ID, 76 photo, 23 certificate, 4 BPJS Kes, 4 BPJS TK, 2 visa, 2 RPTKA, 2 police). A rerun copies nothing new.
- **Warnings seen:** 7 waivers have a visitor company that is not in medpost's list, so they import with company 0 and a rerun after the next sync links them (the real list on the Pi may differ from the test one). 1 waiver birth date cannot be read and 18 phone numbers may have lost trailing digits.
- **Restart and pages:** restart on loaded data healthy; `/` and `/permit` return 200; `/staff` redirects to `/login`. Staff accounts are made with `staffctl add <user> "<Name>" <roles>` and it asks for the PIN on standard input.
- **Deploy script:** `DRY_RUN=1 deploy/deploy.sh full` prints the expected steps (SSD check, arm64 build, rsync, swap with `.bak-<TS>`, restart, health check).

**Not rehearsed** (cannot be, locally): gate sync and the gate end-to-end test (needs the Cloudflare Worker), the "My permit" lookup with Turnstile (off without keys; covered by unit tests), the systemd user unit and linger, the tunnel, medpost's real company list and checkups, and a real medpost on MariaDB (its MariaDB migrations start at 011, so a fresh MariaDB cannot build its schema; the Pi's medpost already has its own).

## 9. Open for the owner

- 18 permits with a blank valid-until date: **decided 2026-11-08 (owner): import with `--fill-valid-until`**. Every real run in section 4 uses the flag.
- About 60 ambiguous ID and photo files: add a matching rule, or attach by hand after import.
- Whether the cap e-mail also copies the Stage 1 mailboxes.
- Imported permits have no medpost patient id. **Decided 2026-11-08 (owner): link by name and date of birth for now, NIK or passport later.** The first time a worker is confirmed by name and birth date (My permit page or the permit form), their unlinked permits are linked to their medpost patient id (`permitlog.Store.Adopt`). Check after the import: a worker who has an imported permit opens My permit and sees it. Permits whose sheet name is spelled differently from medpost's, or has no birth date, stay unlinked until the NIK or passport number becomes the key (medpost must then return it on `employees`).
- Locking tickets of a renewed LEP.
- Delete the scratch upload folders (about 500 MB of ID documents) left in the session scratchpad.
