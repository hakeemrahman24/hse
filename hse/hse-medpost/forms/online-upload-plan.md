# Online upload for the Drug & Alcohol Consent form

## Context

`forms/da-consent-result-form-v280227-191026.html` is a standalone, offline-first
HTML form (fills out, signs, and saves a PDF with zero network calls). The
fatigue scoring form already has a client-side pattern for queuing records
and uploading them when online (`UPLOAD_URL`, a pending queue, an `Upload`
button) — but it was shipped with `UPLOAD_URL` left empty, so nothing has
ever actually received an upload: there is no server endpoint anywhere in
`hse-medpost` for this today.

The goal: when the Drug & Alcohol form is saved (PDF generated) and the
device later has a connection, the structured result data should sync to
the existing `hse-medpost` Go/SQL backend (already live at
`medical-activities.ayahaif.my.id`, deployed via the documented Pi/systemd
runbook in the root `README.md`), without changing how the form works
offline. Agreed scope (confirmed with the user):

- Add upload-sync to **this one form only** for now (the fatigue form's
  queue pattern is the template; this isn't a ground-up redesign, and not a
  server-rendered in-app form — the HTML file stays the source of truth and
  keeps working fully offline exactly as it does today).
- Auth: **the existing staff login** (username + 6-digit password, the same
  credentials used everywhere else in the app), not a static secret baked
  into the HTML file. See "Auth" below for why and how.
- Server home: a **new `internal/screening` package** (currently an empty
  placeholder in the repo — this is what it looks reserved for).
- Payload: **data only, no signatures or photos** — mirrors the convention
  already established in the fatigue form's `toRow()`.

A companion copy of this plan lives at `forms/online-upload-plan.md` in the
repo (per the user's request) — keep it in sync if this file changes
materially.

### Auth — reuse staff login, not a static token (revised after discussion)

The first draft of this plan used a shared bearer token baked into the
form, like `internal/directory`'s API token. The user pushed back: that
token would sit in plain text inside a file copied onto every field
device — a much weaker secret than `internal/directory`'s (which lives
server-side, used by other backends, never shipped to an end-user device).

Investigated the existing staff login system
(`internal/staff/auth.go`, `internal/auth/auth.go`,
`internal/middleware/middleware.go`) to see whether to reuse it instead.
Findings:

- Staff accounts already exist: `AppUserTable` (`Username` + bcrypt
  `PasswordHash`, the password being a 6-digit numeric string validated by
  `auth.Digits6`) joined 1:1 to `MedpostStaffTable` (`StaffID`,
  `StaffName`). This is exactly "username + PIN" in effect, just not
  labeled "PIN" in the code.
- Every write handler that needs "who did this" (`internal/activity-log`,
  `internal/entrymcu`) pulls `StaffID` from `middleware.Session(r)` and
  stores it as `FK_StaffID` on the new row — a universal attribution
  pattern across the codebase. A static device token has no `StaffID` to
  attach, so it can't produce that same audit trail.
- **But** the existing login (`POST /api/login`, `internal/staff/auth.go`)
  issues a `SameSite=Lax` **cookie**, meant for same-origin browser
  navigation inside the app. This form is opened via `file://` and talks
  to a different HTTPS origin — a cross-site `fetch()` call. Cookie-based
  sessions are the wrong tool here: `SameSite=Lax` cookies generally aren't
  sent on cross-site `fetch()`/XHR, `file://` sends `Origin: null` (which
  browsers won't reliably honor for credentialed cross-site requests), and
  relying on it would resurrect exactly the kind of flaky-auth problem this
  revision is trying to avoid.

**Resolution:** reuse the *credential check* (username + 6-digit password
against `AppUserTable`, bcrypt), but have the login step return a
short-lived **bearer token in the JSON response body** instead of setting
a cookie. The upload request then sends that token as
`Authorization: Bearer <token>`, exactly like the CORS/preflight plan
already called for — no cookies, no `SameSite`/credentialed-CORS
complications, and the token carries a real `StaffID` for attribution.
Concretely:

- Extract the existing bcrypt/`AppUserTable` check out of
  `internal/staff/auth.go`'s `Login` handler into a small shared function
  (e.g. `staff.VerifyCredentials(db, username, password) (*auth.Session, error)`),
  called by both the existing cookie-setting `Login` handler (unchanged
  behavior for the main app) and the new token-issuing handler below — so
  the credential-check logic isn't duplicated.
- New `internal/screening` handler, `POST /api/screening/login`: same
  request shape as `/api/login` (`{username, password}`), calls
  `staff.VerifyCredentials`, and on success returns
  `{ "ok": true, "token": "<signed>", "name": ..., "staffId": ..., "role": ..., "exp": <unix> }`
  — **no `Set-Cookie`**. The token itself reuses `internal/auth`'s existing
  HMAC-sign/verify code (`auth.Store.sign`, the same `Session{UserID,
  StaffID, Role, Name, Exp}` payload, same 12h expiry) — just read from and
  written to the `Authorization` header/JSON body instead of a cookie, so
  no new signing scheme is introduced.
- `screening.API.authorize(w, r)` (used by the actual upload endpoint)
  parses the bearer token the same way `auth.Store.Get` parses the cookie
  (HMAC verify + expiry check), just pulled from the `Authorization`
  header via the existing `bearerToken(r)` helper style already in
  `internal/directory/directory.go`.
- No `ScreeningUploadToken`/`.env` secret, no `EnsureToken`/DB-token-row
  machinery at all — this whole chunk of the original design is dropped.

### Client UX for the login step

Reuse the existing signature-pad modal's visual pattern
(`.sigpad-overlay`/`.sigpad-box` in the drug-alcohol form) for a small
"Sign in to upload" dialog: username + 6-digit password fields, same
bilingual labelling convention as the rest of the form. On success, store
the returned token + its `exp` in `sessionStorage` (cleared when the
browser/tab closes — deliberately **not** `localStorage`, since these can
be shared/field devices and the token shouldn't quietly outlive the
browser session) and reuse it for subsequent uploads in the same tab until
it expires, at which point `uploadPending()` re-prompts. This means most
uploads after the first one in a session need no extra interaction.

## Client side: `forms/da-consent-result-form-v280227-191026.html`

This form is single-shot (one donor per save — no "Next patient" queue like
the fatigue form), has no `FORM_NAME`/`FORM_VERSION`/IndexedDB today, and
`REQUIRED_IDS`/`collectDraft()` already enumerate every field id
(`donorName`, `donorCompany`, `donorPosition`, `testType`, `b1Time`/`b1Read`/
`b1Mouth`/`b1Res`, `b2Time`/…, `pneg-*`/`pnonneg-*` panel checkboxes,
`finalRes`, `remarks`, etc. — see lines ~1119–1385).

Because the upload payload excludes signatures/photos (the only large
binary data this form holds), a full IndexedDB store isn't needed — a small
`localStorage`-backed pending queue (array of flattened records) is enough,
simpler than copying the fatigue form's IndexedDB machinery wholesale.

Add, following the fatigue form as the reference pattern:

1. `var UPLOAD_URL = "https://medical-activities.ayahaif.my.id/api/screening/drug-alcohol";`
   and `var LOGIN_URL = "https://medical-activities.ayahaif.my.id/api/screening/login";`
   near the top with the other form constants. No token constant — nothing
   secret is baked into the file.
2. `toRow(rec)` — flattens the current form state into the upload JSON
   shape (one function, covering donor info, test type, consent
   date/time, both breath-test rows, the urine panel checks, final result,
   remarks, collector/witness/donor **names** — never signature images).
   Give every record a client-generated id (`crypto.randomUUID()`, with a
   timestamp+random fallback for older WebViews) so retried uploads are
   idempotent server-side (see migration below).
3. A pending queue in `localStorage` (new key, e.g.
   `f02ConsentPendingUploads_v1`): `doSave()`'s success path pushes the
   `toRow()` snapshot after the PDF is generated, independent of the
   visible form (so the user can still hit "Reset Form" right after
   printing without losing the queued upload).
4. An `Upload (n)` button in the footer next to `Save PDF`/`Reset Form`
   (same placement style as the fatigue form's `uploadBtn`), with a pending
   count badge, disabled at 0.
5. A small "Sign in to upload" modal (styled like the existing
   `.sigpad-overlay`): username + 6-digit password. Submits to `LOGIN_URL`;
   on success stores `{token, exp, name, staffId}` in `sessionStorage`
   (never `localStorage` — see Auth section above) and proceeds straight to
   upload; on failure (401) shows the error inline and lets them retry.
6. `uploadPending()`: bail with a clear offline message if
   `!navigator.onLine` (never attempt the request offline — data stays
   queued, nothing is lost); if there's no stored token or it's expired,
   open the sign-in modal first; otherwise POST
   `{ form, formVersion, records }` as JSON to `UPLOAD_URL` with
   `Authorization: Bearer <token>`; on a `401` (token rejected/expired
   server-side too), clear the stored token and re-open the sign-in modal;
   on success, remove the accepted ids from the queue (server returns
   `{ "accepted": ["<id>", ...] }`, same shape the fatigue form already
   expects); on any other failure, leave everything queued and show a
   bilingual error — same never-lose-data behavior as the fatigue form.
7. A small pending-count indicator so staff get a clear "records waiting to
   upload" signal (the fatigue form already solved this with a pill/badge —
   reuse that look).

## Server side: `hse-medpost` Go backend

New package **`internal/screening`**. Its auth plumbing borrows from
`internal/auth`'s existing HMAC sign/verify code and `internal/staff`'s
credential check (see "Auth" above) rather than `internal/directory`'s
static-token pattern — the token here is per-login and carries a
`StaffID`, not a fixed shared secret.

- `API struct { DB *sql.DB; AuthStore *auth.Store; Now func() time.Time }`.
- `Login(w, r)` — `POST /api/screening/login`: decode `{username,
  password}`, call the extracted `staff.VerifyCredentials(db, username,
  password)`, on success build an `auth.Session{StaffID, Role, Name, Exp:
  now+12h}`, sign it with `AuthStore` (same signing code `internal/auth`
  already uses for cookies, just not set as a cookie here), and respond
  `{ "ok": true, "token": "<signed>", "name", "staffId", "role", "exp" }`.
  401 on bad credentials, same error shape as the existing `/api/login`.
- `authorize(w, r) *auth.Session` — reads `Authorization: Bearer <token>`
  (same `bearerToken(r)` header-parsing helper style as
  `internal/directory/directory.go`), verifies the HMAC signature and
  expiry the same way `auth.Store.Get` does for the cookie, and returns
  the decoded session (or writes 401 and returns nil). This is what gives
  the upload handler a real `StaffID` to attribute the submission to.
- `SubmitDrugAlcohol(w, r)` — `POST /api/screening/drug-alcohol`:
  1. `sess := authorize(w, r)`; bail if nil.
  2. Decode `{ form, formVersion, records: [...] }`.
  3. For each record: `INSERT ... ON CONFLICT (ClientID) DO NOTHING`
     (SQLite) / `INSERT IGNORE` (MySQL) into a new table, keyed on the
     client-generated id (idempotency), **plus `FK_StaffID = sess.StaffID`**
     — so every row is attributable to the staff member who uploaded it,
     consistent with the `activity-log`/`entrymcu` pattern.
  4. Respond `{ "accepted": [ids that are now stored, including ones that
     were already there from a prior attempt] }` — same contract the
     fatigue form's client already expects.
- `writeJSON` helper — copy the same small per-package helper every other
  `internal/*` package already has (`internal/directory/directory.go:306`
  etc.) rather than introducing a shared one against the grain of the
  existing code.

### CORS — new requirement, nothing in the repo does this today

Verified by grep: **no existing route sets `Access-Control-*` headers or
handles `OPTIONS`.** Every existing API is called from this same origin's
pages or server-to-server. This form is opened via `file://`, so its
`fetch()` POST is cross-origin (`file://` origins send `Origin: null`), and
the `Authorization` + `application/json` headers trigger a CORS preflight.
Without explicit handling, every upload attempt will fail with an opaque
CORS error, not a clean 401/500 the form can show a message for. Needed:

- `Access-Control-Allow-Origin: *` on both handlers' responses (deliberately
  no cookies/credentials mode anywhere in this flow — see Auth above — so a
  wildcard origin is fine and keeps things simple).
- `Access-Control-Allow-Headers: Authorization, Content-Type`.
- `Access-Control-Allow-Methods: POST, OPTIONS`.
- Explicit `mux.HandleFunc("OPTIONS /api/screening/login", ...)` and
  `mux.HandleFunc("OPTIONS /api/screening/drug-alcohol", ...)`
  registrations in `cmd/server/main.go` next to their `POST` routes, since
  Go's `net/http` `ServeMux` does not synthesize `OPTIONS` handling for a
  method-prefixed pattern — it has to be routed and answered (204,
  no body) explicitly.

### Route wiring: `cmd/server/main.go`

Follow the existing style for constructing/wiring an API struct (see
`directoryAPI` at line ~325 and its route registrations) — construct
`screeningAPI := &screening.API{DB: database, AuthStore: sessionStore}`
(reusing the same `*auth.Store` already built for the cookie-based staff
login, so both share one HMAC secret/signing implementation), and register
`POST /api/screening/login` and `POST /api/screening/drug-alcohol` with
**no** `middleware.RequireAuth` wrapper (that middleware expects the
cookie-based session; this flow authenticates itself via the bearer token
inside each handler instead, same shape as `directory.*`'s routes just
with a different, per-login token rather than a fixed one).

### Config: `internal/config/config.go`

No new config needed — no static token to load. The HMAC secret for
signing these bearer tokens is the same `SessionSecret` already loaded for
the cookie-based login (`cfg.SessionSecret`, see existing `Config` struct),
reused via the shared `*auth.Store`.

### Migration

New table (name: `DrugAlcoholSubmission` or similar — final column list
should mirror whatever `toRow()` ends up emitting, so write the client
payload shape first, then the table). Key points, not a full column-by-
column spec:

- `ClientID TEXT NOT NULL UNIQUE` — the idempotency key described above.
- `FK_StaffID INTEGER NOT NULL REFERENCES MedpostStaffTable(StaffID)` — who
  uploaded it (from the verified bearer token's session), same attribution
  pattern as `ActivityTable`/`EvidenceTable`'s `FK_StaffID`.
- `SubmittedAt`, `FormVersion` — bookkeeping columns, same spirit as the
  fatigue form's `created_at`/`form_version` in `toRow()`.
- One column per flattened field from `toRow()` (donor info, test type,
  both breath-test rows, panel results, final result, remarks, signer
  names) — flat and denormalized, same shape as the JSON payload; this is
  a results log, not something that needs to join against `patient`/
  `company`, so it does **not** need foreign keys into those tables.
- Store the full raw JSON payload too, in an extra `RawJSON TEXT` column,
  as a forward-compatible safety net if a client adds a field before the
  schema catches up (cheap insurance, already a common pattern for this
  kind of ingest endpoint).

Add it two places, following the existing split exactly:
- `migrations/011_screening.sql` (SQLite dialect) — append its filename to
  the hardcoded auto-migrate list in `cmd/server/main.go` (~line 35,
  alongside `"010_project.sql"`), so a fresh dev DB picks it up
  automatically.
- `migrations/mysql/013_screening.sql` (MySQL dialect, next number after
  the existing `011_mcu_bloodtype.sql`/`012_company_archived.sql`) — **not**
  auto-run (production is MySQL and those are applied by hand per the
  README's existing "Current Status" note) — document it in the same
  "run by hand before deploying" spot the other two mysql-only migrations
  are documented.

## Verification

1. `go build ./...` — confirms the new package compiles and wires in.
2. `PORT=8090 go run ./cmd/server` locally (SQLite auto-migrates, picks up
   the new table) — confirm server starts clean, no migration errors.
3. `curl -X OPTIONS` and `curl -X POST` against
   `http://127.0.0.1:8090/api/screening/login` with a known dev staff
   username/6-digit password — confirm CORS headers are present, a bad
   password returns 401, and a correct one returns a token (no
   `Set-Cookie` header — this must stay a pure JSON-body token).
4. `curl -X POST` against `http://127.0.0.1:8090/api/screening/drug-alcohol`
   with that token as `Authorization: Bearer <token>` — confirm a
   well-formed payload inserts and returns `{"accepted":[...]}`, and the
   inserted row's `FK_StaffID` matches the logged-in staff account. Retry
   with a missing/garbage/expired token — confirm 401.
   - Re-POST the exact same payload (same `ClientID`) — confirm it does
     **not** create a second row (idempotency check) and still returns the
     id in `accepted`.
5. Open the form in a headless browser (the project already has a
   puppeteer-core-based pattern used for the other forms in this
   conversation's scratchpad), fill the required fields, save the PDF,
   confirm the record lands in the pending queue and the `Upload (n)`
   button reflects it; click Upload, confirm the sign-in modal appears,
   log in with a dev staff account, and confirm the upload completes and
   empties the queue; click Upload again in the same tab and confirm it
   does **not** re-prompt (token reused from `sessionStorage`).
6. Confirm the form still works with **no network at all** (airplane
   mode/dev server stopped): PDF save, draft autosave, and the queue still
   accumulating pending records with a clear "offline" message instead of
   a failed request — and that the sign-in modal is never shown while
   offline (upload is skipped with the offline message before it would
   otherwise prompt).

## Status (2026-10-07)

Built and checked:

- Server: `internal/screening` (`POST /api/screening/login`, `POST /api/screening/drug-alcohol`, CORS and OPTIONS preflight), `staff.VerifyCredentials` shared by the cookie login and the token login, `auth.Store.Issue`/`Verify` for the token. Migrations `migrations/011_screening.sql` (auto-run on SQLite) and `migrations/mysql/013_screening.sql` (run by hand on MySQL).
- Client: `da-consent-result-form-v280227-191026.html` now has **Next patient** (keeps the finished test in a device queue and clears the form), **Upload (n)**, and a sign-in box. Save PDF also queues the result. Reset clears the form and keeps the queue.
- Checks: `go test -race ./internal/screening ./internal/staff ./internal/auth` passes. A live server smoke test covered preflight, bad and good login, the no-token 401, and a repeated upload (accepted, not duplicated). The browser flow was not run: no headless browser is installed here.

Deviation from the plan above: the table keeps the key result columns (donor, consent time, test type, breath readings and results, final result, remarks) plus `RawJSON` with the full payload, not one column per field. A field the form adds later is preserved in `RawJSON` without a schema change.

## Status (2026-10-17)

- Fatigue Scoring form uploads (`POST /api/screening/fatigue`): production failed with 500 because `FatigueSubmission.CreatedAt/SubmittedAt` were `CHAR(19)` and the form sends a 24-char ISO timestamp. Columns are now `VARCHAR(32)` in `migrations/mysql/013_screening.sql` and `014_fatigue.sql`; production needed a manual `ALTER TABLE`. Insert failures for fatigue and drug & alcohol are now logged server-side with the client record id and staff id.
- Production runs MariaDB, so only `migrations/mysql/` is applied (by hand, after a backup). The SQLite files in `migrations/` must not be run there. The Fatigue log tab needs `HealthProcedureTable.TestLogKind='fatigue'` (`016_test_log_kind.sql`).
- Test log: record-count label removed; Excel export is named like `fatigue test log - 17102026-143005.xlsx`.
- Fatigue form: X (0–20), Y (0–30) and Z (2–24) hour fields are numbers-only with range checks.
- Phone footer: compact layout on all forms. Fatigue and consent use a 2+3 button grid; the seven two-button forms share the row width equally.
- File names: forms are now `<name>-form-v<ddmmyy>-<ddmmyy>.html` (expiry date, then the date `update-validity.py` was run). PDF names already used `-yyyymmdd-hhmmss`. The consent form is now `da-consent-result-form-v280227-191026.html`.
- Not yet done: deploying the edited forms; confirming `DrugAlcoholSubmission` in production has no `CHAR(19)` columns and all expected columns.

## Status (2026-10-20)

- The two result tables are now `DrugAlcoholScreening` and `FatigueScreening` (SQLite renames on start; MariaDB: `migrations/mysql/019_rename_screening_tables.sql`, by hand). The older notes above use the old names.
- Uploads store `CreatedAt` and `SubmittedAt` as UTC `YYYY-MM-DDTHH:MM:SS` (19 characters). The form still sends its 24-character `toISOString()` value; the server converts it, so a `CHAR(19)` column no longer rejects it. An unparsable `created_at` is not accepted, so the form keeps the record queued.
- The fatigue form's raw hours (`sleep_24h`, `sleep_48h`, `awake_hours`) now have columns `Sleep24h`, `Sleep48h`, `AwakeHours` (`migrations/mysql/020_fatigue_sleep_hours.sql`). No form change.
- 2026-10-20: `019` and `020` ran in production and the build is deployed. The live `CreatedAt` and `SubmittedAt` columns of both tables are `varchar(32)`, so there are no `CHAR(19)` columns left. Both tables are still empty.
