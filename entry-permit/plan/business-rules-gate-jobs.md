# Business rules: gate, gate sync, scheduled jobs, email, QR (slice 0d)

Extracted 2026-11-06 from `legacy/entry-permit-old/gate/src/index.js` (the live Cloudflare gate Worker), `CloudflareSync.gs.js`, and `Code.js` (jobs, mail, QR). Same convention as the other rules documents: **CHECK** marks behaviour the owner should decide; until then the port copies the legacy behaviour. Adjustable numbers go to Settings (plan 3a-2).

**Important:** the gate rules that matter live in the **Worker**, not in the Apps Script `processGateAction` (L11758). That Apps Script version is an older copy (it uses `IN` / `OUT`, `GateState`, a different expiry rule) and is not what the gate scanner calls. This document describes the Worker, which stays in service (plan 4.2) with its logic mostly unchanged.

## 1. What the gate holds (D1 tables, filled by the sync)

| Table | Meaning | Filled by |
|---|---|---|
| `permits` | one row per **Approved** LEP or **Approved** waiver (`WVR-`): name, company, arrival, departure, `mcu` (= **MCU valid-until**, not the LEP date), `permit_valid_until` (= LEP valid-until, **display only**, never enforced by the Worker), status, `is_active`, `gate_status` (`INSIDE`/`OUTSIDE`), photo URL, malaria status | push from the system of record |
| `trip_tickets` | tickets with D1 statuses `PLANNED`, `ACTIVE`, `COMPLETED`; `record_start`, `closed_at`, `close_reason`, `lep_locked`, weeks on site | push **and** gate updates |
| `gate_state` | one row per permit: `INSIDE`/`OUTSIDE`, last action, first check-in, last check-out, officer, gate, anti-duplicate hash | the gate |
| `gate_log` | append-only: every scan (`checkin`, `checkout`, `force`) | the gate |
| `reference` | `officer` (code -> name, **code stored in plain text**), `gate`, `checkout_reason`; replaced wholesale on each sync | push |

For waivers: `arrival`/`departure` = the visit dates, `permit_valid_until` = to-date, `mcu` is empty, so **no MCU rule applies to waivers**.

What gets pushed (`CloudflareSync.gs.js`): only permits with status `Approved` that are marked pending-sync; deactivations for `Resigned`, `Expired`, `Expired in grace period` and `Renewed by LEP-...`; only waivers `Approved`; tickets mapped as: sheet `Closed` -> `COMPLETED` (with record start, closed-at, reason); `Approved` (or legacy `active`) **with** a record start -> `ACTIVE`; `Approved` without one -> `PLANNED`. Tickets in any other status (`PendingDoctor`, `Rejected`, `Cancelled`) are **not** pushed.

## 2. Scan lookup (`GET /api/lookup?lep=`, Worker L256)

`normalizeLep`: trims, upper-cases, strips spaces; accepts `LEP-XXXXX`, `WVR-XXXXX` or a bare 5-character code (treated as `LEP-`). **The scanner passes the id**; the QR on a LEP card contains the ticket page URL (section 8) and the waiver QR contains the bare `WVR-` id, so the scanner page extracts the id from a URL (kept in the page code; slice 5a checks `Gate.html`).

Decision order (first match returns; message in quotes):
1. No permit -> "LEP Not Found".
2. `Resigned` and not on site -> "Worker has resigned - check-in not allowed." Inactive or not `Approved` and not (resigned and on site) -> "LEP is inactive." ("On site" = `permits.gate_status = INSIDE` or the gate state is INSIDE/OVERDUE.)
3. Pick the ticket (section 4). **No ticket and not on site -> "NO TRIP TICKET".**
4. Ticket exists, is locked (`lep_locked`) and the worker is not on site -> "LEP LOCKED".
5. **Early arrival**: not on site and today is more than 3 days before the ticket's planned arrival -> allowed, with a warning (`earlyArrival`, days before the earliest day).
6. **Expired**: not on site and today is after planned departure **plus 3 days** -> "LEP EXPIRED". Also not on site and today is after the **LEP valid-until** date -> "LEP EXPIRED". (Legacy used the raw `mcu` date here; **decided 2026-11-06: the gate follows the LEP valid-until**, which is MCU valid-until minus the admin grace, see 3.1.6.)
7. Mode: on site -> `checkout`; on site and overdue (today after planned departure) -> `force`; else `checkin`. `forceEligible` when on site and (more than 3 days past departure or the LEP valid-until has passed); then mode is `force`. A resigned worker who is on site gets `checkout`/`force` only.
8. Response carries name, company, dates, MCU, malaria status, ticket(s), photo, `isWaiver`.

## 3. Scan action (`POST /api/action`, Worker L524)

Input: id, `action` = `checkin` | `checkout` | `force`, gate name, officer code (upper-cased), reason. Checks in this order:

1. id, action, gate, officer present. **Officer code must exist in `reference` (case-insensitive after upper-casing)**, else "Access Denied". **Gate name must exist in `reference`.** Permit must exist.
2. **Check-in only:** not `Resigned`; permit `is_active`; status `Approved`. (Check-out is allowed for an inactive or resigned permit if the worker is on site.)
3. **Anti-duplicate:** the key is `lep|action|officer|floor(now/5s)` and is compared with the last key stored on the gate state -> "Duplicate scan blocked. Please wait a few seconds."
4. **Check-in:** refused if already INSIDE ("Already checked in...") or OVERDUE state; needs a ticket ("No valid trip ticket - submit rotation plan first."); ticket not locked ("LEP locked - maximum on-site period reached."). If the ticket was `PLANNED` it becomes `ACTIVE` with `record_start = now`. The gate state is upserted: INSIDE, first check-in kept if already set, officer, gate, hash. Permit `gate_status = INSIDE`.
5. **Check-out:** must be INSIDE. **Reason matching `selesai jadwal kerja` completes the ticket** (`COMPLETED`, closed-at, close reason). Refused with "Regular check-out is not available - use force check-out" if more than 3 days past departure or the LEP valid-until has passed. Reason required, **at least 3 characters** (the reason is free text; the server does not check it is in the list). Gate state OUTSIDE, last check-out set, permit `gate_status = OUTSIDE`.
6. **Force check-out:** must be INSIDE; allowed **only** when more than 3 days past departure or the LEP valid-until has passed ("Force check-out is only used after more than 3 days past scheduled departure, or when the permit validity has ended"); reason defaults to "Force check-out".
7. A `gate_log` row is written (timestamp, id, name, company, action, reason, officer name, session id, Jakarta date, gate).

### 3.1 Problems in the current gate logic (CHECK; the port fixes the marked ones)

1. **Expiry and early-arrival checks exist only in the lookup, not in the action.** A scan page left open, or a direct API call, can check in a worker whose permit expired (departure + 3 days, MCU date) because the action re-checks only status, ticket and lock. **Port: one shared decision function used by both lookup and action** (fix).
2. **Ticket is completed before the check-out is validated.** For a reason matching `selesai jadwal kerja` the ticket is set to `COMPLETED` first, and the check-out can then be refused (past departure + 3, or reason too short). The ticket stays completed while the worker stays inside. **Port: validate first, then write everything in one transaction** (fix).
3. **The reason that completes a ticket is not in today's list.** Production reasons are *Sesuai jadwal keluar, Tugas luar, Pindah area, PP harian, Selesai awal*; the code looks for "selesai jadwal kerja". As it stands **no check-out can complete a ticket** with the current list. **Decided 2026-11-06: both *Sesuai jadwal keluar* and *Selesai awal* complete the ticket.** Port: a Settings list `ticket_completing_checkout_reasons` (those two) instead of a hard-coded phrase; matching is by exact reason text chosen from the reason list.
4. **Time zone:** the lookup uses the Jakarta calendar for ticket days, but other date math in the Worker (`today.setHours(0,0,0,0)`, `new Date(today)`) uses the Worker's UTC clock, so between 00:00 and 07:00 Jakarta time "today" is the previous day for the MCU and departure tests. **Port: Jakarta calendar day everywhere** (fix).
5. **The 5-second duplicate window is a fixed bucket** (`floor(now/5000)`), so two identical scans just across a bucket boundary both pass. Port: compare with the stored last-action time instead (`gate_duplicate_scan_seconds`, 5) (fix).
6. **MCU date, not LEP date, decided the gate.** The legacy gate refused new check-ins after the raw **MCU** valid-until, while the LEP ends earlier (MCU minus the grace). **Decided 2026-11-06: the gate follows the LEP valid-until** (`permit_valid_until`, already pushed to D1), because "LEP active depends on MCU validity minus the grace period, which the admin sets". Consequences: the gate is stricter by the grace period; and since the LEP date is the earliest of MCU-minus-grace, contract, visa/ITAS/ITAP and passport (permit rules section 3), those dates are now enforced at the gate too, not only by the nightly expiry job. The nightly job still deactivates the permit after the LEP date (a backup if a sync is late). Force check-out for someone already inside after the LEP date stays possible. Waivers: LEP valid-until = visit to-date, so their rule is unchanged.
7. **Officer codes are stored and matched in plain text in D1.** Port: push a salted PBKDF2 verifier, never the code (plan 6); case-insensitive match preserved.
8. The Worker also exposes `/api/ticket/info` and `/api/ticket/submit` (an older path where Apps Script pushed a new ticket straight into D1, creating tickets with the status `PLANNED` and mirroring dates onto the permit). **Port: remove `submit`**; tickets reach D1 only through the sync.

## 4. Which ticket the gate uses (`pickGateTripTicket`, Worker L128)

From the permit's open tickets (`PLANNED` or `ACTIVE`, oldest planned arrival first), for today's Jakarta date:
1. Tickets whose planned window **contains today** (inclusive both ends): prefer `ACTIVE`, then the earliest arrival.
2. Else, if the worker is on site: among tickets that already started, the one with the **latest** planned departure.
3. Else: among tickets not yet ended (departure today or later), the earliest arrival.
4. Else the first open ticket.

Future rotations stay listed but are not used for the scan.

## 5. Gate sync (Pi <-> Worker): what each side owns

The Worker already has the endpoints the new Pi job needs: `POST /api/sync` (push permits, tickets, references), `/api/sync/pull-gatelog` + `/ack-gatelog` (by id), `/pull-gatestate` + `/ack-gatestate` (by LEP), `/pull-tickets` + `/ack-tickets` (by ticket id), `/api/permit/deactivate` (statuses `Resigned`, `Expired`, `Expired in grace period`, `Renewed by ...`), `/api/sync/reset-sync-flags` (recovery). Every call carries the shared secret **in the JSON body** (`secret`); invalid -> 401. Port keeps these routes; **adds the secret as an `Authorization` header too** and accepts both during the cut-over.

Rules to keep:
- Push in chunks of about 40 rows with D1 `batch()`; pull up to 100 rows by default, **at most 200**; every pulled row stays `synced_to_sheet = 0` until acked, so a failed write is simply pulled again (at least once). The Pi must make every apply **idempotent** (gate log by the Worker's row id / session id; gate state by LEP with newest `updated_at` winning).
- `permit/deactivate`: if the worker is **still INSIDE**, the permit becomes inactive but stays `INSIDE` so check-out remains possible and the gate state is left alone; otherwise the permit is set `OUTSIDE` and its gate state deactivated.
- Push of a ticket with status `PLANNED` or `ACTIVE` also rewrites the permit's `arrival` / `departure` in D1.
- References (officers, gates, reasons) are **deleted and re-inserted** on every push, so an empty list sent by mistake wipes them. Port: never push an empty officer or gate list; keep the old copy and alert.

**Tickets are written by both sides** (my earlier "one writer per table" is only true for permits and gate state). Field ownership:

| Field | Owner | Direction |
|---|---|---|
| `planned_arrival`, `planned_departure`, name, company, medication fields, `lep_locked`, `submitted_at` | Pi | Pi -> gate |
| `status` PLANNED -> ACTIVE, `record_start` (set at first check-in) | gate | gate -> Pi (Pi stores it as the ticket's `RecordStart`, status stays `Approved`) |
| `status` COMPLETED, `closed_at`, `close_reason` (from check-out) | gate | gate -> Pi (Pi sets `Closed` with that reason) |
| `status` COMPLETED from the Pi (auto-close at departure, section 6) | Pi | Pi -> gate |
| `weeks_on_site` | Pi (job) | Pi -> gate |

Conflict rule: if both changed since the last ack, the gate's `record_start` and gate-set closing win; the Pi's planned dates and lock win. `TotalActualDays` (inclusive days from first check-in to the last check-out, or to today while inside; Jakarta days, same-day = 1) is computed **on the Pi** from pulled gate state and refreshed daily for everyone still inside; D1 does not hold it.

## 6. Scheduled jobs (the five Apps Script triggers)

| Job | Time (Jakarta) | What it does | Settings |
|---|---|---|---|
| Departure check | daily 06:00 | work tickets (ticket rules section 5) and waiver tickets / waivers / gate state (permit rules section 9); also refreshes `TotalActualDays` for everyone inside | `job_hour_departure_check` |
| WEP expiry | daily 06:00 | permit rules section 6 | `job_hour_expiry_check` |
| Stale review reminder | daily 07:00 | section 7 | `job_hour_stale_reminder`, `approver_reminder_hours` |
| Admin permit digest | every 2 weeks, Monday 08:00 | section 7 | `job_hour_admin_digest`, `digest_weeks` (2), `digest_weekday` (Monday), `digest_expiry_window_days` (30), `digest_urgent_days` (7), `digest_grace_urgent_days` (3) |
| Email outbox drain | hourly | section 9 | `outbox_drain_minutes` (60) |
| Gate sync (new) | every 5 minutes | section 5 | `gate_sync_minutes` |

Go scheduler requirements: Jakarta time; each run recorded in `JobRun` so a restart or a missed hour runs once, not twice; per-record errors logged and skipped; a "run now" button on the admin page for each job.

## 7. The two reminder emails

**Stale review reminder** (`runStaleReviewReminderCheck`, L11119): three stages, each with its own recipient list from the role-email mailboxes (Settings): *Stage 1 Reviewer* (WEP `PendingStage1` since submit time + waivers `PendingStage1` since the Doctor review time), *Doctor* (waivers `PendingDoctor` since submit), *Final Approver (Site Manager)* (WEP `PendingFinal` since the Stage1 time + waivers `PendingSiteManager` since the Stage1 time). For each stage with at least one pending item **at or over `approver_reminder_hours` (24)** and at least one valid recipient: one email listing all pending items sorted by hours pending, stale items highlighted and counted in the subject, and a link to the stage's portal. **Decided 2026-11-06: sent only when something is over 24 hours** (legacy sent every day while anything was pending), and it repeats daily while a stale item remains.

**Admin permit digest** (`runAdminPermitDigest`, L11250): to the admin mailboxes. Counts: registered work permits, active (permit with a working ticket today), permits with valid-until within the next 30 days (highlight when 7 days or fewer left), permits `Expired in grace period` (highlight when 3 days of grace or fewer remain: `14 + daysPast`). Only work permits (not waivers).

## 8. QR codes and public links (must keep working)

- **LEP card QR** = `https://z.ayahaif.my.id?page=ticket&lep=LEP-XXXXX` (the ticket page URL, `buildLepCardQrPayload_` L2884). With no LEP yet it encodes the base URL.
- **Waiver card QR** = the bare `WVR-XXXXX` id (`buildWaiverCardQrPayload_`, L3535).
- Old code fetches QR images from **external services** (`api.qrserver.com`, `quickchart.io`) and the Google chart API is gone. **Port: generate QR images locally in Go**, no outside call (a printed card must never depend on a third party), and keep the same payloads so already-printed cards still scan.
- Compatibility routes to serve: `/?page=ticket&lep=...`, `/?page=card&id=...`, plus all `?page=` values of the old router (welcome, requesterwelcome, reviewerwelcome, index, waiver, ticket, stage1, doctor, sitemanager/final, dashboard, gateofficer, admin) mapped to the new pages (redirect, not 404), because emails already sent contain them. Page titles are kept (`PAGE_TITLES`, L11979).
- Open-graph preview tags (the reason the shortlink Worker existed) are rendered by the new pages themselves.

## 9. Email system

- **Sender:** `medicalprojectlng@ayahaif.my.id`, display name *Medical Project LNG* (some mails use *Entry Permit System*). Gmail fallback and the Gmail/Brevo daily-quota logic are **dropped** (SMTP relay).
- **Mail kinds** (each stays a template file, English unless noted): WEP submitted (with photo), WEP approved (card + QR, link to the ticket page), WEP rejected (stage, reviewer, note), waiver submitted (with card), waiver approved (card + QR), waiver rejected, ticket approved (location, arrival, departure; plus the three-language Fit-with-Note disclaimer block when applicable), ticket pending doctor, ticket rejected, planned-departure reminder (7 or 2 days), week N-1 reminder, week-cap notice, stale review reminder, admin digest.
- **Recipients:** the requester email is a comma list (primary + CC, duplicates removed, lower-cased); week N-1 and cap notices also CC the Stage1 role mailboxes; stale reminders and digests go to the role/admin mailboxes. Emails carry a trust footer and a "never share your PIN" style note where present (`emailTrustFooterText_`; wording to copy in slice 4c).
- **Outbox:** every email is first stored (`EmailOutbox`: queued-at, id, status `Pending`/`Sent`/`Failed`, kind, source key, to, cc, subject, text + html body, attempts, scheduled-for, last error), then sent. A drain runs hourly: sends rows whose scheduled time has passed, **marks the source record's "email sent" time** (permit or ticket) on success, retries on error, gives up as `Failed` after **14 attempts** (`outbox_max_attempts`). Port: same table and lifecycle; send immediately after the database commit (not inside the approval transaction), retry with a growing delay (`outbox_retry_minutes`), and keep the "email sent" stamp on the source row. Failed rows are listed on the admin page with a retry button.
- **MX check** on requester addresses at submit (`email_mx_check`).

## 10. New settings found in this slice

| Key | Default | Where |
|---|---|---|
| `gate_early_arrival_days` | 3 | lookup warning (this is the existing `checkin_grace_days`, one setting) |
| `gate_overdue_grace_days` | 3 | no new check-in after departure + N; force check-out after N days |
| `gate_duplicate_scan_seconds` | 5 | duplicate scan block |
| `gate_checkout_min_reason_chars` | 3 | check-out |
| `ticket_completing_checkout_reasons` | Sesuai jadwal keluar, Selesai awal | closes the ticket at check-out |
| `mcu_lep_grace_value`, `mcu_lep_grace_unit` | 2, weeks (unit: weeks or days) | LEP valid-until = MCU valid-until minus this; replaces `mcu_lep_grace_weeks`; used by permit validity and therefore by the gate |
| `digest_weeks`, `digest_weekday`, `digest_expiry_window_days`, `digest_urgent_days`, `digest_grace_urgent_days` | 2, Monday, 30, 7, 3 | admin digest |
| `outbox_drain_minutes`, `outbox_retry_minutes` | 60, growing | outbox |
| `gate_sync_minutes`, `gate_sync_stale_warning_minutes` | 5, 15 | sync |

## 11. Golden test cases for slices 5a, 5b, 6a, 6b

Gate (Worker logic, tested as pure functions with a fake D1): policy early-arrival 3, overdue grace 3.

| # | Case | Expected |
|---|---|---|
| 1 | Unknown id | "LEP Not Found" |
| 2 | Resigned, outside; resigned and inside | refused, no check-in; mode `checkout` only |
| 3 | Approved permit, no ticket, outside | "NO TRIP TICKET" |
| 4 | Ticket locked, outside; inside | "LEP LOCKED"; still allowed to check out |
| 5 | Arrival in 5 days | check-in allowed with `earlyArrival`, 2 days before earliest |
| 6 | Today = departure + 3; departure + 4 | check-in allowed; "LEP EXPIRED" |
| 7 | Today after the LEP valid-until (MCU 2027-06-30, grace 2 weeks: LEP 2027-06-16), outside, on 2027-06-17; same worker on 2027-06-16 | "LEP EXPIRED"; allowed; and inside on 2027-06-17: regular check-out refused, force allowed |
| 8 | Inside, today = departure + 1 | overdue shown but regular check-out still allowed |
| 9 | Inside, departure + 4 | regular check-out refused, force allowed |
| 10 | Force before departure + 3 and MCU valid | refused |
| 11 | Check-in with a stale lookup (permit expired after the page loaded) | refused by the action (fixes 3.1.1) |
| 12 | Completing-reason check-out refused because of departure + 4 | ticket **not** completed (fixes 3.1.2) |
| 13 | Check-out with reason of 2 characters | refused |
| 14 | Two identical scans 3 seconds apart, even across a 5-second boundary | second blocked (fixes 3.1.5) |
| 15 | Scan at 01:00 Jakarta on the departure day | treated as the departure day, not the day before (fixes 3.1.4) |
| 16 | Ticket pick: two tickets, today inside the second; inside and none contains today | second used; latest started |
| 17 | `deactivate` while inside; while outside | permit inactive, still `INSIDE`, gate state untouched; gate state deactivated |
| 18 | First check-in on a `PLANNED` ticket; second check-in after a check-out | ticket `ACTIVE`, `record_start` set; `record_start` unchanged |
| 19 | Sync: pull 250 unsynced log rows | at most 200 returned; unacked rows reappear; duplicate apply is harmless |
| 20 | Empty officer list pushed | rejected, previous officers kept |

Jobs: stale reminder with only non-stale items still sends; no recipients -> skipped, logged; digest counts only work permits; digest grace left = 14 + days past; the digest runs on the Monday 8:00 of every second week and not twice if the service restarts that morning; an outbox row failing 14 times becomes `Failed`.

## 12. Open questions raised by this slice

All six decided by the owner on 2026-11-06:

1. ~~Completing reason~~ **Sesuai jadwal keluar and Selesai awal** (setting `ticket_completing_checkout_reasons`).
2. ~~Fixes in 3.1~~ **Approved** (shared lookup/action checks, transactional check-out, Jakarta days, duplicate window).
3. ~~Stale reminder~~ **Only when something is over 24 hours.**
4. ~~Digest~~ **Keep every second Monday.**
5. ~~Gate on MCU date~~ **Gate follows the LEP valid-until** (MCU minus an admin-set grace in weeks or days).
6. ~~Waiver gate rules~~ **Fine as they are** (no MCU rule for waivers).
