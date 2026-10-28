# Medical checkup submission portal — plan

Draft, 2026-10-23; revised 2026-10-25 and again 2026-10-26 after the project owner's answers (batch notify with a manual Send button only, one status list, username and password after admin approval, `clamd` allowed, submitters see their own status, company is pick-only, 2-year retention, a dedicated reviewer email list); revised 2026-10-28 after a plan check (Brevo for mail and bounce handling, infected files removed, unverified requests purged, submitters get a table of their own documents they can open, a separate event log for the portal); revised again 2026-10-28 after the owner chose to keep the portal inside the one binary but give it its own face (own home page, register and sign-in, settings page, black and pink palette), a built-in hidden portal admin, and an admin setting for expiring-checkup reminders. Plan step 1 (shared `turnstile` and `ratelimit` packages) is built; nothing else is. Extends `hse-medpost-plan.md` (the `entrymcu` and `patient` sections). Section 9 lists what is decided and what is still open.

## 1. What this is

Outside parties (company HR, contractors' clinics) submit a worker's medical checkup result as a PDF through a public page. Medpost staff review it, link it to a `Patient`, and later turn it into an `McuRecord`. Today staff receive these by other channels and key them in by hand.

Four things are new: a **submitter account** (approved by the portal admin), a **submission** (one PDF about one patient), a **batch** (the set of submissions a submitter sends together), and a **review queue** for staff. Patients and `McuRecord` already exist and are reused.

**Integrated, with its own face (owner, 2026-10-28).** The portal runs inside the same Go binary and database as the staff app (like `/sofi`), but it is a separate world to the user: its own pages, its own look (section 4), its own accounts (`Submitter`, never `staff` or `AppUserTable`) and its own built-in admin. A company's HR contact who submits checkups is therefore not part of the medical team and sees nothing of the staff app. In code, `internal/mcusubmit` owns its router, cookie and templates and never imports staff handlers, so it could be moved to its own binary later without a rewrite. Its database queries use a dedicated restricted database login where the engine allows it (MariaDB: grants on the portal tables plus read on `Patient` for matching).

## 2. What already exists and is reused

| Need | Existing piece | Change |
|---|---|---|
| Cloudflare Turnstile | `internal/sofi/turnstile.go` (`Verifier`, `NewCloudflareVerifier`), wired in `cmd/server/main.go` | Move to a shared `internal/turnstile` package so `sofi` and the new package both import it. Behaviour unchanged. |
| Per-IP rate limit | `limiter` in `internal/sofi/sofi.go` | Same move; it is 15 lines. |
| PDF squeeze | `internal/storage/compress.go` (Ghostscript passes, gentle to strong; 1 MB for activity uploads) | The portal reuses its passes through `GhostscriptCommand` with its own 2 MB limit (`PortalTargetBytes`) and a flatten step before it (section 5). |
| PDF magic check | `storage` (`pdfMagic`) | Reuse. |
| SMTP and recipients | `internal/entrymcu/emailnotify.go` (SMTP config, `ToAddresses`) | Reuse the config and sender, pointed at Brevo's SMTP relay (host, port, login and SMTP key are already settings, so no new sender code); the batch notification job runs beside the monthly expiry one. Bounce events come back through a Brevo webhook (section 4). |
| Searchable dropdown | typeahead in `web/static/malaria.js` (patients, regions) | Reuse the pattern for company, position and patient. There is no shared `combo.js` in this repo. |
| Patient / Company lookup | `internal/patient`, `internal/company` | Reuse. |
| Staff login, roles | `internal/auth`, `internal/staff` | Staff side of the review queue only. The portal does not use them. |
| Page shell and look | `web/templates/sofi/` (own templates beside the staff ones), `web/static/clearwave/` and `sofi.css` (theme tokens such as `--accent`, `--surface`), `combo.js`-style typeahead in `malaria.js` | Same component set and CSS variables, with a new `web/templates/mcusubmit/` layout and a `mcu-portal.css` that overrides the palette (section 4). |
| Expiring-checkup query | `fetchExpiringSoon` in `internal/entrymcu/report.go` (used by the monthly staff reminder, `activeOnly=true`) | Reused for the submitter reminders (section 8), so a Close/Archived worker is never chased. |
| Hidden built-in admin | `AppUserTable` username `admin`, hidden from every list and protected from edit, reset and delete (Progress Log 2026-10-01); seeded by `internal/staff/seed.go` | Same idea for the portal, but the password is never fixed in code (section 4). |

Not present: any malware scanner (no ClamAV reference anywhere in the repo), any submitter concept, any email verification.

## 3. Data model

Migrations: `migrations/sqlite/016_mcu_submission.sql` and `migrations/mysql/026_mcu_submission.sql` (the next free numbers after `015_sofi` and `025_sofi`). Per `CLAUDE.md`: valid on both engines, no backslashes in SQL literals, MariaDB file run by hand after a mysqldump. Timestamps are UTC strings like the other tables. Sketch, not final DDL:

```
Submitter
  SubmitterID        PK
  Email              VARCHAR(254) NOT NULL UNIQUE     lower-cased
  Username           VARCHAR(40) NULL UNIQUE          chosen by the submitter after approval
  PasswordHash       VARCHAR(255) NULL                argon2id or bcrypt (golang.org/x/crypto is already in go.mod)
  Name               VARCHAR(150) NOT NULL            sanitized, section 4
  FK_CompanyID       NOT NULL                         picked from the Company list; no free text
  PositionText       VARCHAR(128) NOT NULL
  EmailVerifiedAt    NULL
  Role               ENUM('Submitter','Admin') NOT NULL DEFAULT 'Submitter'
  IsBuiltIn          BOOL NOT NULL DEFAULT 0          1 only for the hidden portal admin (section 4)
  SessionVersion     INT NOT NULL DEFAULT 1           in the signed cookie; bumped on password change, Disable or Reset, so every older session stops working on its next request
  RemindersEnabled   BOOL NOT NULL DEFAULT 1          the submitter can turn expiry reminder mails off in Settings
  LastReminderAt     NULL
  EmailBouncedAt     NULL                             set by the Brevo webhook on a hard bounce, block, invalid address or spam report
  EmailBounceReason  VARCHAR(100) NULL
  ErasedAt           NULL                             personal data blanked after retention (section 5)
  Status             ENUM('Requested','Approved','Active','Rejected','Disabled')
                     Requested = waiting for an admin; Approved = admin said yes, no password yet;
                     Active = username and password set
  ApprovedAt NULL, FK_ApprovedByStaffID NULL
  FailedLogins INT, LockedUntil NULL
  CreatedAt, LastLoginAt

SubmitterToken                                         one-time link, stored hashed
  TokenHash CHAR(64) PK (sha256), FK_SubmitterID, Purpose ENUM('Verify','Setup','Reset','EmailChange'), NewEmail NULL, ExpiresAt, UsedAt

McuPortalSetting                                       one row (id 1), edited by the portal admin
  RemindersEnabled   BOOL                              master switch for the expiring-checkup reminders (section 8)
  ReminderDaysBefore INT                               remind when a checkup expires within this many days (default 60)
  ReminderRepeatDays INT                               do not mail the same submitter again within this many days (default 30)
  UpdatedAt, FK_UpdatedBySubmitterID

McuBatch                                               the documents a submitter sends together
  BatchID            PK
  FK_SubmitterID     NOT NULL
  CreatedAt
  SentAt             NULL                              NULL = still a draft; set only by the Send button
  ReviewersNotifiedAt NULL, SubmitterNotifiedAt NULL

McuSubmission                                          one PDF about one worker
  SubmissionID       PK
  FK_SubmitterID     NOT NULL
  FK_BatchID         NOT NULL
  FK_PatientID       NULL                              set on match or on staff approval
  PatName, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality,
  PatNik, PatPassportNo                                snapshot of what the submitter typed
  FK_CompanyID       NULL
  IsNewEmployee      BOOL
  FilePath           VARCHAR(255)                      outside the web root
  FileSha256         CHAR(64)
  FileBytes          INT
  FileExpiresAt      NULL                              upload date plus 2 years; the daily job clears the file
  ScanStatus         ENUM('Scanning','Clean','Infected','ScanError')
  Status             ENUM('Draft','Pending','Reviewed','Closed','Rejected','Withdrawn')
                     Draft = uploaded but batch not sent (staff cannot see it);
                     Pending = batch sent, waiting for staff; Reviewed = staff checked the file;
                     Closed = a checkup record was created from it; Rejected = refused, with a reason;
                     Withdrawn = the submitter took it back while it was still Pending
  RejectReason       VARCHAR(255) NULL
  WithdrawnAt        NULL
  SubmittedAt        NOT NULL
  ReviewedAt NULL, FK_ReviewerStaffID NULL
  FK_McuRecordID     NULL                              filled when staff create the checkup record

McuSubmissionEvent                                     append-only audit trail for this portal; never updated or deleted
  EventID            PK
  At                 CHAR(19)                          UTC
  ActorType          ENUM('Submitter','Staff','System')
  ActorID            INT NULL                          SubmitterID or StaffID; NULL for System (scan, expiry job, webhook)
  EntityType         ENUM('Submitter','Batch','Submission')
  EntityID           INT                               no foreign key, so the event survives a deleted row
  Action             VARCHAR(30)                       Registered, Verified, Approved, Rejected, Disabled, LoggedIn, LoginFailed,
                                                       Uploaded, Removed, Sent, Withdrawn, Infected, Reviewed, PatientLinked,
                                                       Closed, FileViewed, FileExpired, Erased, EmailChanged, PasswordChanged, ReminderSent
  Detail             VARCHAR(255) NULL                 short reason or signature name; never a name, NIK or date of birth
  IpHash             CHAR(64) NULL                     sha256 of IP plus a server secret, for abuse tracing without storing the IP
```

Why a separate table instead of `AuditLog` (decided in the 2026-10-28 check): `AuditLog` in `emr-plan.md` is not built yet and its `FK_StaffID` is NOT NULL, so it cannot record a submitter or the system. This table has an actor type, holds no patient data, and the portal can be built without waiting for the EMR one. If the EMR `AuditLog` later becomes generic, this one can merge into it.

`Patient` is **not** written by submitters. A worker who matches an existing `Patient` is linked at submit time; a new employee stays as the typed snapshot until a staff member approves, at which point the app creates the `Patient` row. Reason: `Patient` already has unique indexes on `PersonalID` and `(Name, DateOfBirth)` and staff are cleaning homonyms (`emr-plan.md` section 3); anonymous writers must not add rows to it.

## 4. Submitter account (public pages under `/mcu-submit`)

Three steps, decided 2026-10-25: **request**, **admin approval**, **create username and password**.

1. **Request** (`/mcu-submit/register`): name, email, company, position, Turnstile. Status `Requested`. A verification link is emailed to confirm the address works; the request does not reach the admin queue until it is used. The admins get one email "new submitter request" (name, company), not one per attempt. Two rules added 2026-10-28:
   - **Same answer every time.** The page always replies "If this address can register, we sent a link", whether the email is new, already registered or already verified. An existing account gets a different email ("you already have an account, sign in or reset your password"). This stops the form from revealing who has an account.
   - **Unverified requests expire.** A `Requested` row whose email was never verified is deleted after 48 hours, with its token. This stops someone parking another person's address on the `UNIQUE` email and locking the real owner out.
2. **Approval**: the portal admin (below) sees the list of requests on the portal's Admin page, can approve, or reject with a reason; Status becomes `Approved` or `Rejected`; the submitter is emailed either way. Staff-app admins do not approve submitters (changed 2026-10-28 with the built-in portal admin: one place, and the staff app stays out of the public portal's accounts).
3. **Create the login**: the approval email carries a one-time setup link (valid 3 days, single use). The submitter picks a username (3 to 40 letters, digits, dot, underscore; unique) and a password. Status becomes `Active`.

Sign-in: username and password plus Turnstile. Password rules: at least 10 characters, not equal to the username or email, not on a short list of common passwords; hashed with argon2id (or bcrypt cost 12). Five wrong passwords lock the account for 15 minutes (`FailedLogins`, `LockedUntil`), and the error message never says whether the username exists. "Forgot password" emails a reset link (`Reset` token, 1 hour). The staff 6-digit password pattern is deliberately not reused: this login faces the internet. Separate signed cookie `mcu_submitter`, Path `/mcu-submit`, HttpOnly, Secure, SameSite=Lax, 12 h; never accepted by staff routes and the reverse. Admins can Disable an account at any time, which also ends open sessions on the next request.

Fields on the request form:

- **Turnstile** on register, sign-in and once per batch (not per file, section 5 step 1); server-side verify with the shared `Verifier`.
- **Name**: Unicode NFC, trim, collapse spaces, strip control characters and anything outside letters, apostrophe, hyphen, dot and space, max 150. Output is always HTML-escaped by the templates; sanitizing is for data quality, escaping is for safety.
- **Company**: searchable dropdown over `Company` (non-archived), pick only; there is no "Other" and no free text (owner, 2026-10-26). A submitter whose company is missing cannot register until an admin adds it, so the page shows "Company not listed? Ask the medpost admin" under the box. The same rule applies to the company on each patient entry (section 6).
- **Position**: searchable dropdown of positions already used in `Patient.WorkPosition`, plus "Other" free text, same sanitizing.
- **Email**: lower-case, syntax check, then `net.LookupMX` (fall back to A record) to reject domains that cannot receive mail. A disposable-domain list is optional.
- **"Inbox not full" cannot be checked up front.** The only way is an SMTP `RCPT TO` probe, which most providers block or treat as abuse, and it would get the Pi's IP listed. Instead the verification email must be opened before the request counts. Mail goes out through Brevo (decided 2026-10-28), which reports delivery results to a webhook:
  - `POST /hooks/brevo`, outside every session and cookie, answers 200 quickly and does nothing but update the row. It must not be trusted by its payload alone: protect it with a shared secret (a header if Brevo's webhook settings allow a custom one, otherwise a long random token in the URL), compare in constant time, rate-limit it, and match events to a `Submitter` by the lower-cased email. Confirm the exact event names and the auth option in phase 0 (expected: hard bounce, soft bounce, blocked, invalid address, spam report).
  - Hard bounce, blocked, invalid address and spam report set `EmailBouncedAt` and `EmailBounceReason`. A soft bounce (this is where mailbox-full lands) is only recorded as an event, with the admin shown the latest one. The app stops sending to a bounced address and the admin list shows a badge so the submitter can be contacted another way or the email corrected.
  - The sender domain needs SPF, DKIM and DMARC set up in Brevo, or verification mail will land in spam and the whole sign-up flow looks broken. Also check Brevo's daily send limit on the current plan against one reviewers' mail plus one submitter mail per batch.
- Rate limits: register requests per IP and per email, login attempts per IP and per username, reset requests per email.

### The portal's own face (decided 2026-10-28)

Pages, all under `/mcu-submit`, in a layout of their own (own header with the nav Home, New batch, My submissions, Settings, and Admin for the admin; no link to the staff app, no staff login form):

| Page | Who | What it shows |
|---|---|---|
| Home | signed in | Greeting, a **New batch** button, count cards by status (Draft, Pending, Reviewed, Closed, Rejected), and an **Expiring soon** list of the submitter's own workers whose checkup is within the reminder window (section 8). Before sign-in, `/mcu-submit` is a short public landing page: what the portal is for, Sign in and Register buttons. |
| Register / Sign in / Forgot password / Set password | public | Section 4 flows, each with Turnstile where listed. |
| New batch, My submissions | submitter | Section 7. |
| **Settings** | submitter | Change email, change password, reminder mails on or off (below). |
| Admin | admin | Requests waiting, all submitters (Approve, Reject, Disable, Enable), bounce badges, and **Reminder settings** (below). |

**Look.** Same component set and CSS variables as the rest of the app (cards, tables, buttons, the typeahead), so there is one design system to maintain, but its own palette from a new `web/static/mcu-portal.css` that overrides the theme tokens: near-black surfaces and pink as the accent. Rules for the palette: text and background pairs meet WCAG AA contrast (4.5:1 for normal text), so buttons on a pink fill use dark text, not white; status badges never rely on colour alone (each carries its word); pink is the accent for actions and focus rings, not for error messages (errors stay a distinct red). Exact hex values are chosen at build time with a contrast check, then written back here. The pages carry a short "Medical checkup submission" brand line, not the staff app's name or logo, so a submitter never thinks they are inside the clinic system.

**Settings page (submitter).**
- *Change email*: enter the new address and the current password. The same checks as registration apply (syntax, `net.LookupMX`, not already used). A one-time `EmailChange` link goes to the **new** address; the login email changes only when it is opened. A notice goes to the **old** address ("your email was changed or a change was requested; if this was not you, reset your password"), so a stolen session cannot silently move the account. Brevo bounces apply to the new address once it is active.
- *Change password*: current password, new password twice, same password rules as section 4. On success `SessionVersion` is bumped, which signs out every other session, and the current one is re-issued.
- *Reminder mails*: a checkbox to stop or resume the expiring-checkup reminders (section 8).
- Name, company and position are not editable here: company is admin-controlled (pick-only), and a change request would need approval. A "request a change" link mailing the admin is enough for now.

**Built-in portal admin.** One account with `Role = 'Admin'` and `IsBuiltIn = 1`, username `admin`, treated like the staff app's hidden `admin` (Progress Log 2026-10-01):
- It is omitted from the submitters list, the approval list and the username picker; edit, Disable, delete and password reset by any API return not found. It signs in on the same sign-in page as everyone, with Turnstile.
- **No password in the source.** The staff seed uses a fixed password, which is not acceptable on an internet-facing page. Instead, on the first start with no built-in admin, the app reads `MCU_PORTAL_ADMIN_PASSWORD` from the environment (the same way the Turnstile keys are read); if it is unset, the portal admin is not created and the log says so. The password must pass the section 4 rules, so a weak one is refused at start. The admin changes it from Settings like anyone else. The seed runs once and never overwrites an existing password.
- The normal lockout applies (five wrong passwords lock it for 15 minutes), so anyone can lock the admin out by guessing; Turnstile and the per-IP limit reduce that. The hidden account has no email for "forgot password", so recovery is an environment switch: starting once with `MCU_PORTAL_ADMIN_RESET=1` and a new `MCU_PORTAL_ADMIN_PASSWORD` replaces the password and clears the lock.
- Every action writes `McuSubmissionEvent` with `ActorID` of the admin. Because several people may share this one login, the audit trail says "admin" and not who; if that matters, add named portal admins later (open question below).

## 5. Upload pipeline

Per submission, in this order. Each step fails closed with a clear message.

**Status (2026-10-29): built, tested and wired to the upload page and the `McuSubmission` rows (`internal/mcusubmit/web_upload.go`). Decision (owner, 2026-10-29): no ClamAV and no virus scanner. Step 4 below is dropped; the Ghostscript rebuild, the structure check and the sandbox are the protection, and the rows are marked `Clean` once a file has come out of the pipeline. `Pipeline.Scanner` stays as an unused optional hook. The `ScanError` status and the "retry scan" job are therefore not needed, and the `clamd` RAM and `freshclam` notes below no longer apply. The page does not ask for Turnstile on each batch (the account is already signed in through Turnstile); the caps are the daily limit of 50 uploads, checked before any Ghostscript work, and the one-at-a-time rebuild.**

1. Session valid (account `Active`), Turnstile passes (once per batch, not once per file, so uploading 10 files is not 10 challenges), per-submitter limit (for example 50 files per day).
2. `http.MaxBytesReader` at 15 MB raw. Must start with `%PDF-`; extension and client MIME ignored. Reject encrypted PDFs.
3. Write to a **quarantine directory** (outside the web root, mode 0600, random name) with `ScanStatus = Scanning`.
4. **Malware scan** with ClamAV `clamd` over its unix socket (`INSTREAM`), behind a small `Scanner` interface so tests use a fake and one integration test uses the EICAR string. Infected (owner, 2026-10-28: removed): delete the file and the `McuSubmission` row at once, so it never appears in a batch or in My submissions; the page tells the submitter once that the file was rejected as unsafe, and a `McuSubmissionEvent` row (`Infected`, signature name, no worker data) is the only trace. Scanner unreachable: `ScanError`, file stays quarantined and is not downloadable; a retry job rescans, and the submitter may remove the draft instead. Do not treat "scan failed" as clean.
5. **Flatten and shrink**: run Ghostscript over the file to rewrite it (interprets and re-emits the page content, so form fields, annotations, JavaScript, embedded files and launch actions should not survive), then the existing compress passes until `<= PortalTargetBytes` (2 MB for the portal, decided by the owner 2026-10-29; the activity uploads keep 1 MB). If the strongest pass is still over 2 MB, reject with "file too large, split or rescan at lower resolution". Run `gs` with `-dSAFER`, a timeout, and limited memory (`ulimit -v`); a hostile PDF is the main attack surface here. **Spike first (phase 0)**: confirm that this gs invocation really flattens AcroForm fields and annotations (`-dShowAnnots` and `-dPrinted` behave differently between versions) on 3 real submitted PDFs; if not, add a `pdftocairo` step.
6. Move to final storage, record `FileSha256`, `FileBytes`, set `Clean`. The submission stays `Draft` inside the submitter's open batch. A duplicate hash for the same patient is flagged, not blocked.

Disk and memory: the project owner confirmed `clamd` can run on the Pi (2026-10-25). It holds the signature database in RAM (roughly 1 GB), so phase 0 still checks `free -m` with the app, MariaDB and `clamd` running together, and sets up `freshclam` for daily signature updates plus a systemd unit ordered before the app.

Storage: decided 2026-10-26 to keep the PDFs on the Pi's own disk, not in Nextcloud. Nextcloud would add a PHP stack and about 1 GB of RAM beside MariaDB and `clamd`, still leave the portal logic (approval, batches, status, retention, patient matching) to build, and put patient files under a second access-control system. The pipeline talks to a small `FileStore` interface (`Put`, `Open`, `Delete`, plus `Stat` for size and hash) with one implementation, `LocalDiskStore`, writing outside the web root with mode 0600 and a random name. Nothing else in the code handles file paths, so a WebDAV or S3 store can be added later without touching the pipeline; tests use an in-memory store. Backup: a nightly encrypted copy (restic or rsync) of the PDF directory and a database dump to an external disk, with a restore tried once before go-live.

Retention: 2 years from upload, `Draft`, `Clean`, `Rejected` alike; a daily job deletes the PDF from storage and clears `FilePath`, keeping the row (status, dates, hash) as an audit stub. Infected files are deleted at once. The date is stored per row (`FileExpiresAt`), so a later change of the period does not rewrite history. Drafts follow the same 2 years (owner, 2026-10-28). The daily job also writes a `FileExpired` event.

Submitter accounts (decided 2026-10-28, a proposal accepted by the owner): a never-verified request is purged after 48 hours (section 4). Every other account row is kept, because submissions and events point at it. Once an account has been inactive for 2 years and none of its files remain, the daily job blanks `Name`, `PasswordHash`, `PositionText` and `Username`, replaces `Email` with a unique placeholder (`erased-<id>@invalid`, which also frees the real address), sets `Status = Disabled` and `ErasedAt`, and writes an `Erased` event. The audit trail then shows that an account existed and what it did, without personal data.

Files are served only by `GET /mcu-submissions/{id}/file` (staff session required), with `Content-Type: application/pdf`, `X-Content-Type-Options: nosniff`, `Content-Disposition: inline`, `Cache-Control: no-store` and `Content-Security-Policy: sandbox`. No direct path to the storage directory, and Cloudflare must not cache that route. These are health records: apply the `healthcare-phi-compliance` checklist (file access logged in `AuditLog`, retention period decided, no names in file names or URLs).

## 6. Patient entry (one worker per submission, no bulk upload)

Form fields: name, date of birth, work position, company, nationality, NIK or passport number.

- Name box is a searchable patient list. Picking an existing patient fills date of birth, company and position read-only (same behaviour as `malaria.js`). Choosing "Not in the list / new employee" opens plain fields.
- Matching on submit, in this order: NIK or passport number (`Patient.PersonalID`), then name plus date of birth. Match found: `FK_PatientID` set. No match: `IsNewEmployee = 1`, snapshot only. Live data has almost no NIKs (1,621 of 1,622 patients had none on 2026-10-12), so most matches will be name plus date of birth; a near-match (same date of birth, similar name) is shown to staff as "possible duplicate" instead of being merged silently.
- Validation: date of birth a real date, not in the future, age 15 to 80; NIK exactly 16 digits (Indonesian nationals) or passport 6 to 12 letters or digits (foreign nationals), chosen by nationality; nationality from `NationalityTable`.
- One form, one PDF, one worker. No CSV or multi-row upload and no endpoint that accepts several files.

## 7. Batches, "Send for review", and the two views

**Submitter side** (`/mcu-submit`, after login):

- **New batch**: add documents one at a time (the form in section 6, one worker and one PDF each). Each saved document shows in a table below as a `Draft` with a remove button (a removed draft is deleted outright, file and row). Nothing is visible to staff and no email is sent while the batch is a draft.
- **Send for review** button, enabled when the batch has at least one document and none is `Scanning` or `ScanError`. It asks "Send N documents for review?" and then: sets `SentAt`, moves every document to `Pending`, and locks the batch (no more additions; a new batch starts for later documents). This is the only moment mail goes out (section 8).
- **Withdraw** (owner, 2026-10-26): while a document is `Pending`, its submitter can withdraw it with a confirmation ("Withdraw this document? Staff will no longer see it."). Status becomes `Withdrawn`, `WithdrawnAt` is set, the PDF is deleted from storage at once and the row stays as an audit stub, and an `AuditLog` row records who and when. Not allowed once it is `Reviewed`, `Closed` or `Rejected`; after that only staff act. A withdrawal sends no email; it drops out of the staff queue, which hides `Withdrawn` rows by default (a filter shows them). If a reviewer has the PDF open in a tab at that moment, the file route returns 404 on the next request. The submitter may upload a corrected document as a new one in a new batch.
- **My submissions** (owner, 2026-10-28: a table of their own data, with the document openable): one table of every document of the signed-in submitter only, newest first, paged. Columns: Sent (date, or "Draft"), Worker name, Date of birth, Position, Company, Nationality, Status badge (`Draft`, `Pending`, `Reviewed`, `Closed`, `Rejected` with the reason, `Withdrawn`), **File** (link, `target="_blank" rel="noopener"`, opens the PDF in a new tab) and Withdraw (only while `Pending`) or Remove (only while `Draft`). Filter by status and date. The File link is disabled with a reason when the file is not `Clean`, was withdrawn, or passed its 2-year expiry ("File deleted after retention period"). The submitter sees their own status but not staff notes.
- **Submitter file route**: `GET /mcu-submit/documents/{id}/file`, needs the `mcu_submitter` cookie and checks that the document belongs to the signed-in submitter. Another submitter's id, a missing id and an expired file all return the same 404 (never 403, which would confirm the id exists). Same response headers as the staff route (section 5). Each open writes a `FileViewed` event.

**Staff side** (inside the existing app, role staff or admin): new list under Medical Record, next to the checkup table. Columns: Sent, Name, Position, Company, Submitter, Status, **File** (link, `target="_blank" rel="noopener"`, opens the PDF in a new tab; disabled with a reason unless `ScanStatus = Clean`). `Draft` rows are never listed. Filters by status, company, submitter and date, in the style of `screening-log.js`.

Actions: mark Reviewed (sets `ReviewedAt`, reviewer); match or create the patient (new employee: shows the typed data, creates the `Patient`, resolves "possible duplicate"); create the `McuRecord` from it (opens the existing checkup form pre-filled, stores `FK_McuRecordID`) which sets `Closed`; reject with a reason. Every state change writes `AuditLog`.

**One status instead of two pairs (2026-10-25).** The outline had "open/close" and "pending, reviewed". Both described the same life of a document, so there is a single `Status`: `Draft` then `Pending` then `Reviewed` then `Closed`, `Rejected` by staff at any point after `Pending`, or `Withdrawn` by the submitter while `Pending`. `Open` in the outline means `Pending` or `Reviewed`; `Close` means `Closed`.

## 8. Notifications (batch, not per document)

Decided 2026-10-25: uploading never sends mail. Mail is sent once per batch, when the batch is sent.

- **Trigger**: only the submitter pressing **Send for review** (section 7). There is no timer and no auto-send (owner, 2026-10-26): an unsent batch stays a `Draft`, invisible to staff, until the button is pressed. Drafts count toward the retention clock in section 5 from the day of upload.
- **Reviewers' email**: goes to a dedicated list of reviewer addresses for this feature, managed by an admin in Settings (table `McuReviewerEmail`: Email, Name, Active; one plain list, every active reviewer gets every batch and every new-account request); it does not reuse the expiry-reminder recipients. One message per batch, subject like "10 medical checkups submitted by PT Example", body a list of name, work position and company, the submitter and company, and a link to the review queue. No NIK, date of birth or attachment.
- **Submitter's email**: one confirmation to the submitter with the same list and "received, status will show in My submissions". The submitter can also see it at any time on the page.
- **New account requests**: separate email to every active address in the same reviewer list (owner: "all reviewer"), only for new account requests (section 4). Approving still needs the `admin` role.
- Send rules: set `SentAt` and move documents to `Pending` in one database transaction, then send the mails, then set `ReviewersNotifiedAt` and `SubmitterNotifiedAt`. If SMTP fails the batch is still sent and visible to staff; a retry job (every 15 minutes, 5 attempts) resends only the mail that is not marked. A crash between sending and marking resends once, which is acceptable; the reverse would lose a notification.
- Infected documents are already deleted (section 5) and never reach a batch. **Send is blocked while any document is `Scanning` or `ScanError`** (assumption of 2026-10-28, change it if you prefer otherwise): the submitter waits for the retry or removes that draft, so no batch is ever locked with a document stuck in `Draft`. Staff get a separate alert only for infections.
- Mail goes through Brevo (section 4); skip any address with `EmailBouncedAt` set and show the submitter a notice on the page instead.

**Expiring-checkup reminders (admin setting, owner 2026-10-28).** The portal admin controls these on the Admin page under Reminder settings (`McuPortalSetting`): a master on/off switch, "remind when a checkup expires within N days" (default 60) and "do not mail the same submitter again within N days" (default 30). This is separate from the monthly staff reminder in `entrymcu/emailnotify.go`, which keeps its own settings and recipients.
- **Who is reminded about whom** (my reading, to confirm): each submitter gets a digest about **the workers they submitted themselves**, meaning patients linked to one of their non-withdrawn, non-rejected submissions, whose latest checkup expires within the window, using `fetchExpiringSoon` with `activeOnly=true`. Reason: it keeps one company's HR from seeing another's workers, and it is what the submitter can act on (send a renewed checkup). The alternative, every worker of the submitter's company, is more useful but shows the validity of workers the submitter never submitted; see the open list.
- **One mail per submitter**, not one per worker: a table of name, position, company, expiry date, and a link to the portal. No NIK or date of birth in the mail. Skipped when the submitter is not `Active`, has turned reminders off, has a bounced address, or was mailed within the repeat period (`LastReminderAt`).
- **Schedule**: a daily job at a fixed hour WIB (so a reminder is never sent in the small hours), sending only to those due. The same list shows on the submitter's Home page as **Expiring soon**, whether or not mail is on.
- A preview in Reminder settings ("N submitters, M workers would be mailed now") and a **Send now** button that follows the same rules, so the admin can see the effect before turning the switch on. Each send writes one `McuSubmissionEvent` (`ReminderSent`, count only).

## 9. Decisions and open questions

Decided by the project owner on 2026-10-25 and 2026-10-26:

- Mail per batch, not per document, triggered only by the Send button; the sender gets a confirmation as well as the reviewers. No auto-send.
- Reviewer mail goes to a dedicated reviewer email list (all reviewers on it also get new-account-request mail).
- Submitters sign in with a username and password they create after an admin approves the request.
- Company is pick-only from the `Company` list, in registration and in patient entry. The patient name box is a searchable list of existing patients with a "new employee" option.
- Submitted PDFs are kept 2 years (section 5), then deleted by a scheduled job; the database row stays as an audit stub without the file.
- `clamd` may run on the Pi.
- Files stay on the Pi's disk behind a `FileStore` interface; no Nextcloud (section 5).
- Submitters can see the status of their own submissions, and can withdraw a `Pending` document.
- Reviewers are one plain list, no categories.
- One status list replaces the two pairs (section 7).

Decided on 2026-10-28:

- Mail through Brevo; bounces come back by webhook and set `EmailBouncedAt` (section 4).
- Infected files are deleted with their row at once (section 5).
- Unverified requests are purged after 48 hours; register answers the same whether or not the email exists (section 4).
- Drafts keep the full 2-year retention. Submitter accounts keep a row for the audit trail and are blanked after 2 years of inactivity (section 5).
- Submitters get a table of their own documents and can open each one (section 7).
- Portal audit trail is a separate append-only `McuSubmissionEvent` table, not the EMR `AuditLog` (section 3).
- **CSRF (owner, 2026-10-28, option A):** Go's built-in `http.CrossOriginProtection` (rejects state-changing requests whose `Sec-Fetch-Site` or `Origin` says another site sent them) wrapped around every `/mcu-submit` route, plus the `SameSite=Lax` cookie, and state changes (Send, Withdraw, Remove, upload, logout) accepted as POST only, never GET. No tokens in forms. Tests: a POST with a foreign `Origin` is rejected with 403, a same-origin POST passes, a GET never changes state.

Decided on 2026-10-28 (second round):

- The portal stays inside the one binary and database, with its own face: own pages, black and pink palette on the shared component set, own accounts, own built-in admin (sections 1 and 4).
- Pages: public landing, register, sign in, forgot and set password, Home, New batch, My submissions, Settings (change email, change password, reminders on or off), Admin.
- A built-in hidden portal admin, seeded from `MCU_PORTAL_ADMIN_PASSWORD`, never a password fixed in code. It approves submitters and sets the reminders; staff-app admins no longer approve.
- Admin setting for expiring-checkup reminders: on/off, days before expiry, minimum days between mails (section 8).

Open:

- **Reminder audience.** Assumed: each submitter is reminded only about workers they submitted. Alternative: every worker of their company. Needs the owner's pick before step 8.
- **Named portal admins.** One shared built-in `admin` means the audit trail cannot say who acted. Option: let the built-in admin promote named submitter accounts to `Admin` later. Not planned now.
- **Language.** `/sofi` has English and Indonesian (`tr`). Assumed the portal needs both, since the submitters are Indonesian companies; confirm.
- Whether Brevo's webhook can send a custom auth header, or the secret must go in the URL (phase 0).
- Whether a `ScanError` document should block Send (the current assumption) or be dropped from the batch automatically.

Remaining technical checks sit in phase 0: Ghostscript flatten on real PDFs, `clamd` memory next to the app and MariaDB, and the Brevo webhook and domain authentication.

## 10. Build order

Tests first (RED, GREEN, refactor), as in the other modules; 80% coverage target on the new package.

0. **Spikes (no schema)**: Ghostscript flatten check on real PDFs; `clamd` and `freshclam` memory and startup on the Pi alongside the app and MariaDB; Brevo SMTP relay send from the Pi, SPF/DKIM/DMARC on the sender domain, webhook event names and auth option, daily send limit.
1. Move Turnstile and limiter to shared packages; `sofi` tests stay green. **Done 2026-10-28** (`internal/turnstile`, `internal/ratelimit`; the full suite and `-race` pass).
2. Migrations (sqlite and mysql) and `internal/mcusubmit` store layer, with tests that run on both engines. **Partly done 2026-10-28:** `migrations/sqlite/016_mcu_submission.sql` (loaded at start) and `migrations/mysql/026_mcu_submission.sql` (by hand) cover `Submitter`, `SubmitterToken`, `McuSubmissionEvent` and `McuPortalSetting`; `internal/mcusubmit` has the account store (register, verify, approve, reject, disable, setup, password, lockout, 48-hour purge), one-time tokens, the built-in admin seed and reset, settings and the event log, 83.5% covered on SQLite. The owner ran 026 on the production MariaDB through Adminer (2026-10-28). The batch tables are in their own migration pair, `migrations/sqlite/017_mcu_submission_batches.sql` (loaded at start) and `migrations/mysql/027_mcu_submission_batches.sql` (run on the production MariaDB through Adminer by the owner, 2026-10-28, after 026). The store (`internal/mcusubmit/batches.go`) covers open batch, add document (24-hour cap of 50 counted from events), scan result, remove infected, remove draft, send, withdraw, the submitter's own list, the staff queue, and the two file lookups (own and staff). **Not done:** the upload pipeline itself (file store, scanner, flatten and shrink), patient matching, and a MariaDB run of every new query (no MariaDB server here).
3. **Done 2026-10-28 except reset:** submitter request, email verification, admin approval, username and password setup, sign-in, lockout, sign-out, the pages, the admin page and the built-in admin (`internal/mcusubmit/web*.go`, `cmd/server/mcuportal.go`). **Not done:** forgot password and reset. Original description: submitter request, email verification, admin approval, username and password setup, sign-in, lockout, reset (fake mailer and fake Turnstile in tests; tests: wrong password five times locks, unknown username gives the same message, expired and reused tokens fail, a `Requested` or `Disabled` account cannot sign in; `web/` node tests for the sanitizer and the dropdown wiring; register gives the same reply for a new and an existing email, an unverified request is purged after 48 hours, the Brevo webhook rejects a wrong secret and sets `EmailBouncedAt` on a hard bounce, the 2-year erase job blanks personal data and keeps the events; a POST with a foreign `Origin` gets 403 and a same-origin POST passes).
4. Upload pipeline behind `FileStore`, `Scanner` and `PDFProcessor` interfaces (tests: not a PDF, encrypted, too large after compression, EICAR, scanner down, flatten failure leaves no file behind).
5. Patient entry and matching (tests: NIK match, name plus date of birth match, near-duplicate, new employee, invalid NIK, future date of birth).
6. Batches and "My submissions" (tests: a submitter sees only their own rows, `Draft` rows are invisible to staff, Send is blocked while a file is `Scanning`, a sent batch is locked, withdraw works only while `Pending`, withdraw deletes the file and keeps the row, a withdrawn document disappears from the staff queue and its file route returns 404, a submitter cannot withdraw another submitter's document, a submitter opens their own PDF but gets the same 404 for another's id, an infected upload leaves no row and no file, Send is blocked on `ScanError`, every state change and file open writes a `McuSubmissionEvent`). Then the staff review list, file route (tests: staff-only, submitter cookie rejected, a submitter cannot open another submitter's PDF, headers, unscanned file refused), actions and `AuditLog`.
7. Batch notifications (tests: nothing is sent on upload, nothing is sent without the button however long the batch waits, Send sends exactly one reviewers' mail and one submitter mail listing only `Clean` documents, SMTP failure leaves the batch sent and retries only the unmarked mail, no resend after success, inactive reviewer addresses are skipped).
8. **Portal face, settings and reminders** (the shell can start earlier, beside step 3, since step 3's pages need it): `web/templates/mcusubmit/` layout and `mcu-portal.css` palette with a contrast check on every text and background pair, landing and Home pages, Settings (tests: email change only takes effect after the link is opened and mails the old address, a wrong current password changes nothing, a password change signs out other sessions, an expired or reused `EmailChange` token fails, a new email already in use is refused); built-in admin seed (tests: created only when `MCU_PORTAL_ADMIN_PASSWORD` is set and strong, never overwritten on restart, hidden from every list, edit and delete return not found, a reset flag replaces the password and clears the lock); Admin page (tests: a submitter cannot reach Admin routes, a staff cookie cannot either); reminders (tests: nothing is sent when switched off, only workers within N days, a submitter is not mailed again inside the repeat period, a bounced or opted-out submitter is skipped, one mail per submitter, a submitter never sees a worker they did not submit, Send now follows the same rules).
9. Hardening review: `security-reviewer` on the upload and auth code, `healthcare-reviewer` on PHI handling, a MariaDB run of every new query (the `LIKE ... ESCAPE` incident in `CLAUDE.md`), and a deploy runbook for migration 026 and the `clamd` service added to `README.md`.

Not in scope: a separate binary or service for the portal (revisit only if the Pi's memory or an incident calls for it), bulk upload (several workers in one file or a spreadsheet; a batch is still one worker per document), submitter-side editing after sending, automatic extraction of results from the PDF, SMS or WhatsApp notices.
