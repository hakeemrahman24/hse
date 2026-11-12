# Business rules: working tickets, rotation, fatigue (slice 0b)

Extracted 2026-11-06 from `legacy/entry-permit-old/Code.js`. Line numbers refer to that file, so a later slice can jump straight to the source. **This document describes what the code does today, including behaviour that looks wrong.** Items marked **CHECK** are for the owner to decide; until then the Go port copies the legacy behaviour exactly so imported data and the golden tests match.

Policy numbers below are the production Reference values (Settings page): Onshore 8 weeks and Offshore 6 weeks for both WNI and Foreign, rest 14 days (both), extension 7 days, MinimumRestDays 3, MCU LEP grace 2 weeks. All are admin-editable and are read live, never hard-coded.

## 1. Vocabulary

- **WNI** = nationality `indonesia` (case-insensitive after normalising). Everything else is **Foreign** (L1242).
- **Work location**: `Onshore` or `Offshore` only; anything else is empty and rejected (L1247). It is chosen **per ticket**.
- **Ticket status**: `PendingDoctor`, `Approved`, `Rejected`, `Closed`, `Cancelled`; the legacy value `active` counts as `Approved` (L722-747).
- **Inclusive days**: `departure - arrival + 1`, calendar days, so arrival day counts and same-day = 1 (L858).
- **Cap days** = weeks for (location, nationality) x 7. **Max consecutive days** = cap + extension days. For WNI onshore today: 56 and 63.
- **Gap days** between a new arrival and the previous ticket's end = whole days, `new arrival - previous end` (L936).
- **Previous end** of a ticket = its `ClosedAt` if set, otherwise its planned departure (L854).
- **Counting tickets**: only work-entry tickets with status Approved or Closed (or `active`); PendingDoctor, Rejected and Cancelled never count (L750).
- **RotationNo** = count of counting tickets for that LEP + 1. Waiver tickets are always 1 (L766).

## 2. Proposing a ticket (`proposeWorkerTicket`, L8755)

Checks in this order; the first failure stops with a message (nothing is saved):

1. LEP number given; **waiver ids (`WVR-`) are rejected** (waiver tickets are read-only).
2. A valid access session for that LEP (section 6).
3. Planned arrival and departure given, parseable, departure >= arrival, **arrival not before today** (Jakarta calendar day).
4. A permit row exists for the LEP and its status is `Approved`.
5. Work location present (permit's own value first, then the form's).
6. **MCU not unfit** (L777): a blank result counts as unfit; text containing `unfit` or `tidak` is unfit. Blocked.
7. **Departure must not be after the LEP's valid-until date.**
8. **Only one open request**: if a PendingDoctor ticket with an extended pattern exists, block. (CHECK: see 2.1 below, this can no longer happen.)
9. **Proposal window**: if the LEP has a *working ticket* (Approved, not closed, today <= planned departure; the latest by departure), the next one may only be proposed when today >= that departure minus **14 days** (`ROTATION.PROPOSAL_WINDOW_DAYS`, a code constant, not in Settings). With no working ticket there is no window.
10. Policy analysis (section 3). **Arrival before the previous ticket's end is an error.**
11. Consent rules (section 4).

On success the ticket row is written with status `Approved` and an email goes to the requester. Fields set at creation: ticket id `TKT-` + random code, retried until unique; RotationNo; `FatigueDue` = arrival + (fatigue week x 7) - 1 days; `SubmittedAt`; DoctorReview = `Approved` by the system with note `_` (or `MCU note self-declaration`); gap days; short-break flag `Y/N`; rotation pattern text; fatigue flags; work location; MCU result and note copied from the permit.

### 2.1 Approval is automatic; the Doctor queue is effectively dead code (CHECK)

`resolveTicketProposalStatus_` (L1054) returns `Approved` in **every** success case. Even a rotation extension or a short break only sets `needsFatigue = true`, never `needsDoctor`. The header comments and `precious robin.md` say extension goes to the Doctor, but the code does not do that. So today no new ticket is ever `PendingDoctor`; the Doctor queue, `doctorReleaseTicket` (L8995) and the "one request in the pipeline" check only matter for old rows. **Decision for the owner:** keep automatic approval (port as is), or restore the Doctor step for extensions. Default in the port: automatic approval, and the Doctor screen is built only if old PendingDoctor rows exist in the import (slice 4a will count them).

## 3. Policy analysis (`analyzeTicketProposalPolicy_`, L988)

Inputs: LEP, new arrival, departure, work location, nationality.

1. `normalCap` = weeks(location, nationality) x 7. `extension` = Settings extension days (0 disables). `maxConsecutive` = cap + extension. `normalRest` = rest days by nationality. `postExtensionRest` = MinimumRestDays (falls back to normal rest if blank).
2. **Chain** (`getConsecutiveChainInfo_`, L943): take the counting tickets that have both arrival and an end, oldest first. Walk **backwards from the latest**, adding each ticket's inclusive days, stopping when the gap between a ticket and the one before it is **greater than 1 day** (so gap 0 or 1 keeps the chain). While walking record `usedExtension` (a ticket whose pattern text is an extended one), and `exceededOwnCaps` (a ticket longer than the cap for **its own** work location and the worker's nationality). The result also carries the latest ticket and its end date.
3. `chainUsedExtension` = chain used extension **or** exceeded its own caps **or** chain days > normalCap.
4. `gapDays` = arrival - chain's last end (null if no previous ticket).
5. `consecutive` = there is a previous ticket and `0 <= gapDays <= 1`.
6. `requiredRest` = `postExtensionRest` if `chainUsedExtension`, else `normalRest`.
7. `shortBreak` = there is a previous ticket and `gapDays > 1` and `gapDays < requiredRest`.
8. `chainBefore` = chain days if consecutive, else 0. `proposedDays` = inclusive days of the new ticket.
9. `allowedProposalDays` = if consecutive: `max(0, maxConsecutive - chainBefore)`, else `normalCap`.
10. `projectedChain` = chainBefore + proposedDays. `exceedsAllowance` = proposedDays > allowedProposalDays.
11. `rotationExtension` = consecutive **and** projectedChain > normalCap.
12. `pastDutyLimit` = exceedsAllowance **or** rotationExtension. This is stored as the "rotation extension flag".
13. **Pattern text** (`rotationPatternFromFlags_`, L903): consecutive+extended -> `ConsecutiveExtended`; consecutive -> `Consecutive`; shortBreak+extended -> `ShortBreakExtended`; shortBreak -> `ShortBreak`; extended -> `Extended`; else `Normal`. Doctor-review reason = the pattern, or `_` when Normal.

**Nothing in the proposal is ever blocked for going over a cap.** Over-cap and short-break cases only force the fatigue-consent tick (section 4). The only hard stops are the checks in section 2.

### 3.1 Behaviour that looks unintended (CHECK; ported as is)

- **A consecutive ticket that pushes the chain past the cap is always flagged "extended"** (rule 11), even if it stays within cap + extension. With cap 56 and extension 7, any consecutive ticket beyond day 56 is flagged but not blocked.
- **`MinimumRestDays` (3) is used as the required rest after an extension**, which is *shorter* than the normal 14. As coded, working an extension reduces the rest the worker must take. The code comment says "rest after any extension is used", defaulting to the normal rest. **Ask the owner whether 3 is a deliberate minimum or whether it should be at least the normal rest.**
- **Fatigue week is fixed in code** (8 WNI, 12 Foreign, L1199, `FatigueDue` at proposal) and is **not** in Settings, so with Settings caps of 8 and 6 weeks the fatigue threshold is at or after the cap (never before it for Foreign, never before it for offshore WNI). See section 5. **Decided: it moves to Settings** (`fatigue_week_wni`, `fatigue_week_foreign`); the default value is still open (question 3).
- The 14-day proposal window was a code constant. **Decided: it moves to Settings** (`proposal_window_days`, default 14).
- Also moved to Settings (owner decision: every adjustable variable): reminder lead days (7), short-ticket threshold (7 days) and short-ticket lead (2), the "weeks before cap" warning (1), the ticket access limits (5 tries, 15 and 30 minutes), and the job hours. Full list: plan section 3a-2.

## 4. Consent and declarations at proposal

- **Fatigue survey consent** (`fatigueSurveyConsent` = y/yes) is required when the extension flag **or** the short-break flag is set. Missing -> error "Fatigue survey consent is required for this proposal."
- **MCU result "Fit with Note"** (`isMcuFitWithNote_`, L782: contains `note` or `catatan`, and not unfit): requires (a) disclaimer acknowledged (`mcuNoteAgreement` y/yes); (b) personal medication answered Y or N; (c) if Y: details text **and** a medicine photo are required, the photo is uploaded to the medication folder. The stored disclaimer is the five fixed bullet lines in the user's UI language (en / id / zh, L789-811). Self-declaration replaces the Doctor step.
- Plain Fit = nothing extra. (`isMcuFit_`: not blank, not unfit, no note words.)

## 5. After approval: on-site clock and the daily job

**Gate check-in sets `RecordStart`** on the ticket (done by the gate rules, slice 0d). Weeks on site = `floor(floor((now - RecordStart) / 1 day) / 7)` (L10462). No check-in -> no clock. **Port (owner rule, plan 2b): calendar days, `floor((today - day of RecordStart) / 7)`**, no time of day involved; at most one day different from legacy around the check-in hour.

`runDepartureCheck` runs daily at 06:00 Jakarta (L10825, per ticket in `computeDepartureCheckRowUpdate_`, L10712). It only looks at **work-entry tickets that are Approved**. Order for each ticket:

1. **Expiry lock:** if not already locked and (today > the ticket's source-permit valid-until **or** the permit status is `Expired` / `Expired in grace period`) -> `LepLocked = true`, mark for gate sync.
2. **Auto-close:** if today > planned departure and not closed -> status `Closed`, `ClosedAt = now`, reason `Planned departure passed`. **Stop processing this ticket.**
3. If `LepLocked`, stop.
4. **Next-rotation reminder:** `days left` = planned departure - today. Lead = **2 days if the ticket's inclusive length < 7, else 7**. If `0 <= daysLeft <= lead` and not yet notified -> email the requester (link to the ticket page, QR instruction), set `PlannedDep7dNotified`. Sent once.
5. If no `RecordStart`, stop.
6. Otherwise set `WeeksOnSite`. `maxWeeks` = weeks for the ticket's own location and the permit's nationality.
7. **Fatigue flag:** weeks >= fatigue week (8 / 12) and flag not set -> `EarlyReturnFatigueRequired = true`, record the week. (Flag only, no email.)
8. **Week N-1:** weeks >= maxWeeks - 1 and not yet sent -> departure reminder email (weeks on site, weeks left, "contact the site clinic for a burnout assessment") to the requester, CC the Stage1 role mailboxes; record the time.
9. **Week N0 (cap):** weeks >= maxWeeks and not yet locked -> record `WeekN0LockedAt`, set the fatigue flag, record `EscortNotified`, send the rotation-cap email (CC Stage1). **This does not set `LepLocked`.** The email wording says the permit is locked, but only the timestamp is stored (CHECK: does the gate need to block at N0?).

Each ticket is processed independently: an error on one row is logged and skipped; the whole run has a 4.5-minute budget, and unprocessed rows wait for the next day. The Go job needs no time budget but must keep the per-ticket isolation, and must be idempotent (the "notified" columns are the guard).

The waiver counterpart (`runWaiverDepartureCheck`) is covered in slice 0c.

## 6. Ticket access (`verifyTicketDob`, L8494)

- Input: LEP (`LEP-xxxxx` or `WVR-...`) and date of birth as 8 digits `DDMMYYYY`. Anything else -> the generic error "The permit number or date of birth is incorrect." (never says which part is wrong).
- Only **Approved** work permits or **Approved** waivers can open a ticket page.
- **Lockout:** 5 wrong tries per LEP within 15 minutes -> "Too many attempts. Please try again in 15 minutes." The counter is per LEP (not per person or IP). A correct entry clears it.
- On success: a random session token valid **30 minutes**, bound to that LEP, returned to the page; every later call must present it. The QR link `?page=ticket&lep=LEP-xxxxx` only pre-fills the LEP; DOB is still required.
- DOB comparison is by calendar date, tolerant of how the sheet stored it (Date value, `yyyy-mm-dd`, `dd/mm/yyyy`). In the new database the date of birth is stored once as `yyyy-mm-dd` text, so the comparison becomes exact.
- Go port: store sessions and failure counters in tables (or an in-process cache with expiry), not Apps Script cache; add per-IP rate limiting on top (medpost `ratelimit`).

## 7. Golden test cases for slice 2b (synthetic; policy = production values)

WNI, Onshore: cap 56, extension 7, max consecutive 63, rest 14, post-extension rest 3.

| # | Previous tickets | New ticket | Expected |
|---|---|---|---|
| 1 | none | 2027-01-01 -> 2027-02-25 (56 d) | Normal, no flags, no fatigue consent, RotationNo 1 |
| 2 | none | 2027-01-01 -> 2027-02-26 (57 d) | allowed 56, exceedsAllowance, pattern `Extended`, consent required |
| 3 | 56 d ending 2027-02-25 | arrival 2027-02-26 (gap 1), 7 d | consecutive, chainBefore 56, allowed 7, not over allowance but projected 63 > 56, pattern `ConsecutiveExtended` |
| 4 | 56 d ending 2027-02-25 | arrival 2027-02-26, 8 d | exceedsAllowance (8 > 7), `ConsecutiveExtended` |
| 5 | 30 d ending 2027-01-30 | arrival 2027-01-31 (gap 1), 20 d | projected 50 <= 56, pattern `Consecutive`, no consent |
| 6 | 56 d ending 2027-02-25 | arrival 2027-03-02 (gap 5) | short break (5 < 14), pattern `ShortBreak`, consent required |
| 7 | 56 d ending 2027-02-25 | arrival 2027-03-12 (gap 15) | Normal (15 >= 14) |
| 8 | 63 d chain using extension, ending 2027-03-04 | arrival 2027-03-14 (gap 10) | requiredRest = 3 (post-extension), so **not** a short break (see 3.1) |
| 9 | any, ending 2027-02-25 | arrival 2027-02-20 | error: arrival before previous end |
| 10 | gap 0 (same day) and gap 1 | | both consecutive; gap 2 breaks the chain |
| 11 | Offshore, WNI: cap 42 | 43 days, no previous | `Extended` |
| 12 | Foreign, Onshore | | uses the Foreign weeks (8 today) and Foreign rest |
| 13 | previous ticket Offshore, new Onshore | | chain's `exceededOwnCaps` uses the previous ticket's own location cap |
| 14 | MCU blank | | rejected as unfit |
| 15 | departure after LEP valid-until | | rejected |
| 16 | working ticket ending in 20 days | proposal today | rejected (window opens at 14 days before departure); at 14 days before: allowed |
| 17 | Daily job: planned departure yesterday | | Closed, reason `Planned departure passed`, nothing else done |
| 18 | Daily job: lead days: ticket of 6 days | | reminder at 2 days before; ticket of 7 days: at 7 days before; sent once |
| 19 | Daily job: weeks on site 5 (max 6) / 6 | | N-1 email once at 5; N0 timestamp + email at 6; no `LepLocked` |
| 20 | DOB verify: 5 wrong tries, then correct | | locked for 15 min; correct entry after the window works |

Real sheet rows (1,010 permits, 897 tickets) replace these synthetic cases in slice 2b: for each LEP, replay its tickets in submission order and compare each computed flag to the sheet's `ShortBreakFlag`, `RotationExtensionFlag`, `GapDays` and `RotationNo` columns. Mismatches are reported per ticket, and each is either a Go bug or a legacy bug the owner decides on.

## 8. Open questions raised by this slice

All five are decided (2026-11-06):

1. ~~Doctor step for extensions~~ **Decided: automatic approval, as coded.** The Doctor ticket screen is built only if the import contains old `PendingDoctor` tickets (slice 4a counts them).
2. ~~Rest after an extension~~ **Decided: keep it as a Settings value** (`minimum_rest_days`, default 3 as in production). Ported exactly as coded; the owner can change the number at any time.
3. ~~Fatigue week~~ **Decided (owner asked for a suggestion, accepted as default): setting `fatigue_weeks_before_cap`, default 2.** Fatigue flag fires at `max(1, maxWeeks - 2)` weeks on site, so with today's caps: Onshore 6 weeks, Offshore 4 weeks, for WNI and Foreign alike. This replaces the fixed 8 / 12 (which never fired before the cap). `FatigueDue` at proposal uses the same week.
4. ~~Week N0~~ **Decided: notify only (current behaviour), plus a setting `block_gate_at_cap`, default off.** When switched on, reaching the cap sets `LepLocked` (blocks check-in, check-out still allowed). The cap email wording says "locked", so with the switch off the text is changed to "you have reached the maximum rotation; please arrange your departure" to match what actually happens.
5. ~~Proposal window~~ **Decided:** moved to Settings, default 14.
