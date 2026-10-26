# Medical checkup submission portal — plan

Draft, 2026-10-23; revised 2026-10-25 and again 2026-10-26 after the project owner's answers (batch notify with a manual Send button only, one status list, username and password after admin approval, `clamd` allowed, submitters see their own status, company is pick-only, 2-year retention, a dedicated reviewer email list). Nothing here is built. Extends `hse-medpost-plan.md` (the `entrymcu` and `patient` sections). Section 9 lists what is decided and what is still open.

## 1. What this is

Outside parties (company HR, contractors' clinics) submit a worker's medical checkup result as a PDF through a public page. Medpost staff review it, link it to a `Patient`, and later turn it into an `McuRecord`. Today staff receive these by other channels and key them in by hand.

Four things are new: a **submitter account** (approved by an admin), a **submission** (one PDF about one patient), a **batch** (the set of submissions a submitter sends together), and a **review queue** for staff. Patients and `McuRecord` already exist and are reused.

## 2. What already exists and is reused

| Need | Existing piece | Change |
|---|---|---|
| Cloudflare Turnstile | `internal/sofi/turnstile.go` (`Verifier`, `NewCloudflareVerifier`), wired in `cmd/server/main.go` | Move to a shared `internal/turnstile` package so `sofi` and the new package both import it. Behaviour unchanged. |
| Per-IP rate limit | `limiter` in `internal/sofi/sofi.go` | Same move; it is 15 lines. |
| PDF squeeze to under 1 MB | `internal/storage/compress.go` (`PDFTargetBytes = 1_000_000`, Ghostscript passes, gentle to strong) | Reuse as is; add a flatten step before it (section 5). |
| PDF magic check | `storage` (`pdfMagic`) | Reuse. |
| SMTP and recipients | `internal/entrymcu/emailnotify.go` (SMTP config, `ToAddresses`) | Reuse the config and sender; the batch notification job runs beside the monthly expiry one. |
| Searchable dropdown | typeahead in `web/static/malaria.js` (patients, regions) | Reuse the pattern for company, position and patient. There is no shared `combo.js` in this repo. |
| Patient / Company lookup | `internal/patient`, `internal/company` | Reuse. |
| Staff login, roles | `internal/auth`, `internal/staff` | Staff side of the review queue only. |

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
  Status             ENUM('Requested','Approved','Active','Rejected','Disabled')
                     Requested = waiting for an admin; Approved = admin said yes, no password yet;
                     Active = username and password set
  ApprovedAt NULL, FK_ApprovedByStaffID NULL
  FailedLogins INT, LockedUntil NULL
  CreatedAt, LastLoginAt

SubmitterToken                                         one-time link, stored hashed
  TokenHash CHAR(64) PK (sha256), FK_SubmitterID, Purpose ENUM('Verify','Setup','Reset'), ExpiresAt, UsedAt

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
```

`Patient` is **not** written by submitters. A worker who matches an existing `Patient` is linked at submit time; a new employee stays as the typed snapshot until a staff member approves, at which point the app creates the `Patient` row. Reason: `Patient` already has unique indexes on `PersonalID` and `(Name, DateOfBirth)` and staff are cleaning homonyms (`emr-plan.md` section 3); anonymous writers must not add rows to it.

## 4. Submitter account (public pages under `/mcu-submit`)

Three steps, decided 2026-10-25: **request**, **admin approval**, **create username and password**.

1. **Request** (`/mcu-submit/register`): name, email, company, position, Turnstile. Status `Requested`. A verification link is emailed to confirm the address works; the request does not reach the admin queue until it is used. The admins get one email "new submitter request" (name, company), not one per attempt.
2. **Approval**: an admin (role `admin` only, not every staff member) sees the list in Settings, can approve, reject with a reason, Status becomes `Approved` or `Rejected`; the submitter is emailed either way.
3. **Create the login**: the approval email carries a one-time setup link (valid 3 days, single use). The submitter picks a username (3 to 40 letters, digits, dot, underscore; unique) and a password. Status becomes `Active`.

Sign-in: username and password plus Turnstile. Password rules: at least 10 characters, not equal to the username or email, not on a short list of common passwords; hashed with argon2id (or bcrypt cost 12). Five wrong passwords lock the account for 15 minutes (`FailedLogins`, `LockedUntil`), and the error message never says whether the username exists. "Forgot password" emails a reset link (`Reset` token, 1 hour). The staff 6-digit password pattern is deliberately not reused: this login faces the internet. Separate signed cookie `mcu_submitter`, Path `/mcu-submit`, HttpOnly, Secure, SameSite=Lax, 12 h; never accepted by staff routes and the reverse. Admins can Disable an account at any time, which also ends open sessions on the next request.

Fields on the request form:

- **Turnstile** on register, sign-in request and each submission; server-side verify with the shared `Verifier`.
- **Name**: Unicode NFC, trim, collapse spaces, strip control characters and anything outside letters, apostrophe, hyphen, dot and space, max 150. Output is always HTML-escaped by the templates; sanitizing is for data quality, escaping is for safety.
- **Company**: searchable dropdown over `Company` (non-archived), pick only; there is no "Other" and no free text (owner, 2026-10-26). A submitter whose company is missing cannot register until an admin adds it, so the page shows "Company not listed? Ask the medpost admin" under the box. The same rule applies to the company on each patient entry (section 6).
- **Position**: searchable dropdown of positions already used in `Patient.WorkPosition`, plus "Other" free text, same sanitizing.
- **Email**: lower-case, syntax check, then `net.LookupMX` (fall back to A record) to reject domains that cannot receive mail. A disposable-domain list is optional.
- **"Inbox not full" cannot be checked up front.** The only way is an SMTP `RCPT TO` probe, which most providers block or treat as abuse, and it would get the Pi's IP listed. Instead the verification email must be opened before the request counts. If any later mail bounces (including mailbox-full, SMTP 552 / 4.2.2), the address is marked `Bounced` and the admin sees it.
- Rate limits: register requests per IP and per email, login attempts per IP and per username, reset requests per email.

## 5. Upload pipeline

Per submission, in this order. Each step fails closed with a clear message.

1. Session valid (account `Active`), Turnstile passes (once per batch, not once per file, so uploading 10 files is not 10 challenges), per-submitter limit (for example 50 files per day).
2. `http.MaxBytesReader` at 15 MB raw. Must start with `%PDF-`; extension and client MIME ignored. Reject encrypted PDFs.
3. Write to a **quarantine directory** (outside the web root, mode 0600, random name) with `ScanStatus = Scanning`.
4. **Malware scan** with ClamAV `clamd` over its unix socket (`INSTREAM`), behind a small `Scanner` interface so tests use a fake and one integration test uses the EICAR string. Infected: delete the file, keep the row with `Infected` and the signature name for the audit trail, tell the submitter. Scanner unreachable: `ScanError`, file stays quarantined and is not downloadable; a retry job rescans. Do not treat "scan failed" as clean.
5. **Flatten and shrink**: run Ghostscript over the file to rewrite it (interprets and re-emits the page content, so form fields, annotations, JavaScript, embedded files and launch actions should not survive), then the existing compress passes until `<= PDFTargetBytes`. If the strongest pass is still over 1 MB, reject with "file too large, split or rescan at lower resolution". Run `gs` with `-dSAFER`, a timeout, and limited memory (`ulimit -v`); a hostile PDF is the main attack surface here. **Spike first (phase 0)**: confirm that this gs invocation really flattens AcroForm fields and annotations (`-dShowAnnots` and `-dPrinted` behave differently between versions) on 3 real submitted PDFs; if not, add a `pdftocairo` step.
6. Move to final storage, record `FileSha256`, `FileBytes`, set `Clean`. The submission stays `Draft` inside the submitter's open batch. A duplicate hash for the same patient is flagged, not blocked.

Disk and memory: the project owner confirmed `clamd` can run on the Pi (2026-10-25). It holds the signature database in RAM (roughly 1 GB), so phase 0 still checks `free -m` with the app, MariaDB and `clamd` running together, and sets up `freshclam` for daily signature updates plus a systemd unit ordered before the app.

Storage: decided 2026-10-26 to keep the PDFs on the Pi's own disk, not in Nextcloud. Nextcloud would add a PHP stack and about 1 GB of RAM beside MariaDB and `clamd`, still leave the portal logic (approval, batches, status, retention, patient matching) to build, and put patient files under a second access-control system. The pipeline talks to a small `FileStore` interface (`Put`, `Open`, `Delete`, plus `Stat` for size and hash) with one implementation, `LocalDiskStore`, writing outside the web root with mode 0600 and a random name. Nothing else in the code handles file paths, so a WebDAV or S3 store can be added later without touching the pipeline; tests use an in-memory store. Backup: a nightly encrypted copy (restic or rsync) of the PDF directory and a database dump to an external disk, with a restore tried once before go-live.

Retention: 2 years from upload, `Draft`, `Clean`, `Rejected` alike; a daily job deletes the PDF from storage and clears `FilePath`, keeping the row (status, dates, hash) as an audit stub. Infected files are deleted at once. The date is stored per row (`FileExpiresAt`), so a later change of the period does not rewrite history.

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
- **Send for review** button, enabled when the batch has at least one document and none is still `Scanning`. It asks "Send N documents for review?" and then: sets `SentAt`, moves every document to `Pending`, and locks the batch (no more additions; a new batch starts for later documents). This is the only moment mail goes out (section 8).
- **Withdraw** (owner, 2026-10-26): while a document is `Pending`, its submitter can withdraw it with a confirmation ("Withdraw this document? Staff will no longer see it."). Status becomes `Withdrawn`, `WithdrawnAt` is set, the PDF is deleted from storage at once and the row stays as an audit stub, and an `AuditLog` row records who and when. Not allowed once it is `Reviewed`, `Closed` or `Rejected`; after that only staff act. A withdrawal sends no email; it drops out of the staff queue, which hides `Withdrawn` rows by default (a filter shows them). If a reviewer has the PDF open in a tab at that moment, the file route returns 404 on the next request. The submitter may upload a corrected document as a new one in a new batch.
- **My submissions**: every batch and document of the signed-in submitter only, with name, company, position, submitted date and the status badge (`Draft`, `Pending`, `Reviewed`, `Closed`, `Rejected` with the reason, `Withdrawn`). The submitter sees their own status but not staff notes, and can open their own PDF.

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
- Infected or `ScanError` documents are excluded from the list and from `Pending`; the submitter is told which file failed, in the page and in their confirmation mail. Staff get a separate alert only for infections.

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

Nothing is open on the owner's side. Remaining checks are technical and sit in phase 0: Ghostscript flatten on real PDFs, and `clamd` memory next to the app and MariaDB.

## 10. Build order

Tests first (RED, GREEN, refactor), as in the other modules; 80% coverage target on the new package.

0. **Spikes (no schema)**: Ghostscript flatten check on real PDFs; `clamd` and `freshclam` memory and startup on the Pi alongside the app and MariaDB.
1. Move Turnstile and limiter to shared packages; `sofi` tests stay green.
2. Migrations (sqlite and mysql) and `internal/mcusubmit` store layer, with tests that run on both engines.
3. Submitter request, email verification, admin approval, username and password setup, sign-in, lockout, reset (fake mailer and fake Turnstile in tests; tests: wrong password five times locks, unknown username gives the same message, expired and reused tokens fail, a `Requested` or `Disabled` account cannot sign in; `web/` node tests for the sanitizer and the dropdown wiring).
4. Upload pipeline behind `FileStore`, `Scanner` and `PDFProcessor` interfaces (tests: not a PDF, encrypted, too large after compression, EICAR, scanner down, flatten failure leaves no file behind).
5. Patient entry and matching (tests: NIK match, name plus date of birth match, near-duplicate, new employee, invalid NIK, future date of birth).
6. Batches and "My submissions" (tests: a submitter sees only their own rows, `Draft` rows are invisible to staff, Send is blocked while a file is `Scanning`, a sent batch is locked, withdraw works only while `Pending`, withdraw deletes the file and keeps the row, a withdrawn document disappears from the staff queue and its file route returns 404, a submitter cannot withdraw another submitter's document). Then the staff review list, file route (tests: staff-only, submitter cookie rejected, a submitter cannot open another submitter's PDF, headers, unscanned file refused), actions and `AuditLog`.
7. Batch notifications (tests: nothing is sent on upload, nothing is sent without the button however long the batch waits, Send sends exactly one reviewers' mail and one submitter mail listing only `Clean` documents, SMTP failure leaves the batch sent and retries only the unmarked mail, no resend after success, inactive reviewer addresses are skipped).
8. Hardening review: `security-reviewer` on the upload and auth code, `healthcare-reviewer` on PHI handling, a MariaDB run of every new query (the `LIKE ... ESCAPE` incident in `CLAUDE.md`), and a deploy runbook for migration 026 and the `clamd` service added to `README.md`.

Not in scope: bulk upload (several workers in one file or a spreadsheet; a batch is still one worker per document), submitter-side editing after sending, automatic extraction of results from the PDF, SMS or WhatsApp notices.
