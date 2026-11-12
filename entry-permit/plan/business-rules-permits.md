# Business rules: work entry permits (LEP), waivers, duplicates, expiry (slice 0c)

Extracted 2026-11-06 from `legacy/entry-permit-old/Code.js`. Line numbers refer to that file. Same convention as `business-rules-tickets.md`: this describes what the code does today, **CHECK** marks behaviour the owner should decide, and until decided the Go port copies the legacy behaviour so imported data and golden tests match. Adjustable numbers go to Settings (plan section 3a-2).

Not yet read in this slice (belongs to slices 3b-3d, form building): `computeWaiverDerivedFromForm_` (L7275, derived durations and high-risk flag), the 9 risk and 15 health Y/N lists, `buildWepRow_`, `entityNeedsParent_`, the PDF/image checks `assertPdfOrImageMime_`. Gate behaviour is slice 0d.

## 0. Owner decision (2026-11-06): worker identity, company and position come from hse-medpost

The legacy form made the requester type or pick the worker's details and re-entered them on every permit. In the new app the requester **searches for the worker and picks them**; the following come **read-only from hse-medpost's directory API** (loopback, bearer token, never typed again):

| From medpost | Field in the API today |
|---|---|
| Identity | name, gender, date of birth, age, nationality, blood type, `patientId`, `employeeId` |
| **NIK / passport number** | `personalId`: **present on the `checkups` route, missing on the `employees` route**. Add it to the employees response in medpost (token-guarded, same bearer tokens), planned and tested there |
| **Company** | company, `companyEntity`, `parents` (name + depth) |
| **Position** | `workPosition` |
| MCU | `checkUpId`, `dateCheckUp`, `resultCriteria` (fit / fit with note / unfit), `resultValidUntil`, `resultNotes` |

What the requester still enters: requester name and email, work location, home address (province to village), phone, emergency contact, declaration, and the documents (ID file, ID photo, and for foreign workers visa/ITAS/ITAP, RPTKA, the two letters; contract, BPJS numbers and certificate where the toggles and position require them).

Consequences, all applied in the sections below:
1. **One identity key:** `PersonalID` (medpost enforces it unique: 16-digit NIK or 7-9 letters and digits for a passport). Duplicate checks match on it instead of name + date of birth.
2. **Company and position cannot be typed.** If they are wrong, fix them in medpost. The "company change allows a new permit" rule (section 4) now fires when medpost's current company differs from the company stored on the person's last permit.
3. **Entity and parent are not chosen or validated by the requester**: they arrive with the company from medpost (entity, parents with depth). The company-chain checks of `assertCompanyChainForm_` (L5638) become a single check: the medpost record has a company and an entity. If a company has several parents and the worker's record lists several, the card shows the first by lowest depth; CHECK with owner.
4. **The position lookup (plan 3b) runs on medpost's `workPosition`.** The legacy rule "site title typed by the requester wins over the MCU title" (`positionRawForCert_`, L4759) is dropped. Import: where the sheet's current position differs from medpost's, the importer lists the people so the owner can correct medpost.
5. **A worker who is not in medpost cannot get a permit** (this is the same as the legacy "No MCU record found" rule, now also covering identity).
6. **Waiver visitors are not employees**, so the waiver form keeps typed identity (NIK/passport included) and its own tables. Only the WEP flow changes.

Validation of the NIK/passport format therefore follows medpost's rule (NIK exactly 16 digits; passport 7-9 letters and digits), enforced there. The waiver form keeps the legacy looser rule (section 8.1.5).

## 1. Identifiers

| Id | Format | Made when | Rule (L5830-5879) |
|---|---|---|---|
| Request id | `WEP-` + 5 chars | WEP submit | random from `A-Z0-9`, retried on collision |
| LEP number | `LEP-` + 5 chars | Final approval | same alphabet, retried on collision |
| Waiver id | `WVR-` + 5 chars | Waiver submit | same |
| Ticket id | `TKT-` + 5 chars | ticket / waiver submit | same |

Go port: same formats (printed QR codes and emails already use them). Uniqueness comes from a **database unique index plus insert-retry**, not "read all, then pick" (the Apps Script script-lock did that job). 36^5 is about 60 million codes, so collisions stay rare at this volume.

## 2. WEP lifecycle and statuses

`PendingStage1` -> (Stage1 approve) `PendingFinal` -> (Final approve) `Approved`, LEP issued. Reject at either stage -> `Rejected`. Later system statuses: `Expired in grace period`, `Expired`, `Resigned`, and `Renewed by LEP-xxxxx` (free text built from the new LEP; L669-677). An empty status counts as `PendingStage1`.

- **Stage1 review** (`stage1ReviewWEP`, L6652): approve sets Stage1 fields (reviewer, time, note), status -> `PendingFinal`. Reject needs a note, status -> `Rejected`, email to the requester.
- **Final review** (`siteManagerReviewWEP`, L6760 and bulk `siteManagerApproveAllWEP`, L6837): approve **issues the LEP in one step**: new LEP number, `LEPValidUntil` recomputed from the stored row (section 3), Final fields set, status `Approved`, marked for gate sync, **all earlier LEPs of the same person become `Renewed by LEP-new`** (section 5), approval email with the card and QR. Reject needs a note, status `Rejected`.
- **Rejection note rule** (L4444): at least **3 words**, for every reviewer and every stage. -> Setting `rejection_note_min_words` (3).
- **Concurrency:** a script-wide lock serialises Final approvals so two reviewers cannot issue duplicate LEPs. Go: one database transaction per approval with a guarded update (`... WHERE Status = 'PendingFinal'`, require 1 row affected), so a double click or two reviewers cannot issue two LEPs.
- **Bulk approve** exists on the Final portal (and the Doctor portal for waivers) in batches of at most 10. The batch size and the "approval email quota" limit (`SITE_MANAGER_EMAIL_RECIPIENTS_PER_APPROVAL`) exist only because of the Gmail daily quota and **disappear** with SMTP.

### 2.1 Reviews do not check the current status (CHECK)

None of the review functions (WEP Stage1/Final, waiver Doctor/Stage1/Final) verify the record is in the matching `Pending...` status. As coded a Doctor "approve" on an already-`Approved` waiver sets it back to `PendingStage1`, a Stage1 approve on an `Approved` WEP sets it to `PendingFinal`, and a stale browser tab can reject an approved record. **Decision proposed: the Go port allows each action only from its own pending status** (anything else returns "already actioned by someone else"), as the old bulk approval already skips non-pending rows. This is a deliberate behaviour change; it only removes mistakes.

## 3. LEP validity (`resolveLepValidUntil`, L4669)

`LEPValidUntil` = the **earliest** of:
1. MCU valid-until (from medpost) minus the grace (`mcu_lep_grace_value` in `mcu_lep_grace_unit`, **weeks or days, owner decision 2026-11-06**; weeks count as 7 days each). Grace 0 disables the subtraction. Currently 2 weeks. This LEP date is also what the gate enforces (gate rules 3.1.6).
2. Contract valid-until (if given).
3. **Foreign workers only:** work visa expiry (see below) and passport valid-until.

Indonesians ignore visa and passport dates. Certificate-of-competence validity is stored but **never caps the LEP**. Rotation weeks are enforced at ticket proposal, not here. If no date is available at all -> error "Could not determine permit validity - MCU date is missing."

- **Work visa expiry of a stored row** (`workVisaExpiryFromWepRow_`, L6154) = the earliest of the Visa, ITAS and ITAP expiry dates that exist.
- **Binding reason** (`lepCeilingReason_`, L4686): the label of whichever date is earliest (`MCU validity`, `Contract validity`, `Work visa validity`, `Passport validity`), shown on the card and dashboard. Ties go to the first in that order.
- Validity is computed twice: at submit (shown to the requester) and again at Final approval from the saved row, which is the value that counts. The Go port keeps one pure function and calls it both times. Because the MCU date comes from medpost, **the permit stores a snapshot of the MCU result and valid-until at submit** and Final approval uses that snapshot, so a re-check-up in medpost between the two steps cannot change a permit mid-review (CHECK: or should approval re-read medpost and use the newest MCU? snapshot is the legacy behaviour).
- The ticket step later also requires departure <= LEP valid-until (ticket rules 2.7).

## 4. Duplicate and renewal gate for WEP (`evaluateWepDuplicateDecision_`, L11477)

Looks up the **latest** permit for the same person and returns one of `none`, `active` (block), `reuse` (allowed, prefill from the old permit). **Person key: `PersonalID` from medpost** (legacy rows without a stored NIK fall back to normalised name + date of birth during import and lookup; the sheet's permit rows do hold a personal id column, so most will match).

Decision order:
1. No previous permit -> `none`.
2. Previous status `Renewed by...` or `Resigned` -> `none`.
3. **Company change** (medpost's current company differs from the previous permit's stored current company, compared after normalising) -> `none`. A new company always allows a new permit.
4. Work out `mcuChanged` (current MCU valid-until day differs from the stored one) and, for Foreign, `visaChanged` (new visa expiry day differs from stored). `docChanged = mcuChanged or visaChanged`.
5. Previous status `PendingStage1` or `PendingFinal` -> **`active` (blocked)**: one request in flight per person.
6. Previous `Approved` and LEP not yet expired:
   - not `docChanged`: Foreign with no visa date entered yet -> `reuse` with `visaRenewalPending` (they must enter a new visa date on page 2, checked again at submit); otherwise **`active` (blocked)**.
   - `docChanged` -> `reuse` (renewal while still approved), flags `mcuRenewal` / `visaRenewal`.
7. Previous `Approved` but LEP date passed, or status expired / grace -> `reuse`.
8. Otherwise `none`.

At **submit** the same decision runs again; `active` blocks with "This employee already has an active permit (Request ..., LEP ..., valid until ...). Please use the existing permit instead of submitting a new one."

**Prefill rules for `reuse`** (`checkWorkerDuplicate`, L11583): the old permit's details are offered again except expired documents are cleared: expired passport clears the ID document, expired visa/ITAS/ITAP clears the visa block (unless a renewal is pending, then the files are kept for editing), expired RPTKA / Surat Imigrasi / Surat Kepolisian clear the foreign-permit block, expired certificate clears the certificate. Reused now also means: phone, address and emergency contact are offered again (identity, company and position are no longer part of the prefill, they come from medpost). **The old code also deletes the expired files from Drive** when the old permit is already dead. Go port: **do not delete anything automatically**, only stop offering expired files for reuse (CHECK: owner may want clean-up later as a retention setting).

## 5. Renewal (`markPriorLepsRenewedBy_`, L10989)

On Final approval of a new LEP, every other permit for the same person (same `PersonalID`; legacy fallback name + date of birth) whose status is `Approved` or expired-like (not already `Renewed`) and which has a real LEP number becomes `Renewed by LEP-new`, is marked for gate sync (so the gate deactivates it), and **its tickets are locked** (`LepLocked`, ticket rules section 5). `Resigned` and `Rejected` permits are left alone.

## 6. Expiry job (`runWepExpiryCheck`, L11044; daily 06:00)

For every permit that is `Approved` or already expired-like, compute from `LEPValidUntil` (calendar days, Jakarta):
- valid-until **today or later** -> still valid, no change.
- 1 to 14 days past (`lep_expiry_grace_days`) -> `Expired in grace period`.
- more than 14 days past -> `Expired`.

If the status changes, mark for gate sync. **When an `Approved` permit first goes past its date, all of its tickets are locked.** A permit that is expired or in grace **cannot propose tickets** (ticket rules require `Approved`). The job also repairs the legacy case "Approved but already `Expired` within 14 days" back to grace.

## 7. What a WEP submission must satisfy (`submitWorkEntryPermit`, L6441)

Checked in this order; the first failure stops with a message:

1. A worker was picked from medpost (identity, company, entity, parents, position, MCU all come with the record); requester name; requester email present.
2. Requester email list: primary first, duplicates and CC copies of the primary removed, lower-cased. **Domain must be able to receive mail** (`checkEmailDomainMx`): Go `net.LookupMX`; a failed lookup is an error, so keep a Settings switch `email_mx_check` (on) for when DNS is down.
3. Declaration consent ticked.
4. Work location Onshore or Offshore. (The permit stores it, but tickets choose their own, see ticket rules.)
5. **Company record complete:** medpost returned a company and an entity (replaces the legacy company-chain form checks, section 0.3).
6. **Contract:** required only if the contract toggles are Shown and Required: then a file and a validity date not before today. If a date is given anyway it must not be before today.
7. **BPJS (Indonesians only):** Kesehatan / Ketenagakerjaan numbers required only if the toggle is Shown and Required.
8. **Emergency contact:** name and number required; name differs from the worker's; personal and emergency phone both valid and **different**.
9. **Position and certificate:** position lookup per plan 3b on medpost's `workPosition` gives the canonical position and whether a certificate is required. Required only if the position says `Y` **and** the admin toggle `CertificateRequired` is on. Then certificate file (or a reusable earlier one) and a validity date not before today. Paramedic, Nurse and Doctor / Site Physician certificates get a fixed validity of **2090-12-31** (perpetual; L4740, a list in code: moves to Settings under the "all adjustable" rule).
10. **MCU must exist and not be unfit** in medpost for this worker (replaces "No MCU record found"; unfit also blocks tickets, see ticket rules 2.6).
11. Duplicate gate (section 4).
12. **Foreign workers:** work-visa type Visa / ITAS / ITAP, its file and its dates (Visa: issued + expiry; ITAS: code + expiry; ITAP: code + issued-by + expiry; expiry not before today); RPTKA (issuance date, duration, expiry not before today, file); Surat Melapor Imigrasi (valid-until not before today, file); Surat Keterangan Kepolisian (valid-until not before today, file); passport valid-until not before today.
13. LEP validity can be computed (section 3).
14. ID document and ID photo present (or reused). All uploads limited in size (settings) and must be PDF or image.

"Not before today" means the Jakarta calendar day. On success: permit saved as `PendingStage1` with a **snapshot** of the medpost data used (name, NIK, company, entity, parents, position, MCU id, result, valid-until, as of the submit time), a confirmation email (with the photo) goes to the requester, a pending request id is shown.

### 7a. Worker lookup replaces MCU matching (`lookupMCU`, L4624)

The legacy matching (CheckUpId, then name + date of birth, then **name only**, latest check-up wins) is replaced by medpost's directory search, which the requester drives by typing a name, date of birth or NIK/passport and picking the result. **The name-only fallback disappears by design** (it let a person with the same name borrow another person's MCU). The latest check-up for the picked `patientId` is used, unless the requester picks a specific `checkUpId` from the list medpost returns. Imported legacy permits are not re-matched, so history is unaffected; the importer links each to a `patientId` by `PersonalID` or name + date of birth and lists the ones it cannot link.

## 8. Waiver lifecycle and statuses

`PendingDoctor` -> (Doctor approve) `PendingStage1` -> (Stage1 approve) `PendingSiteManager` (the Final stage) -> (Final approve) `Approved`. Reject at any stage -> `Rejected` (note of at least 3 words, email to the requester). After the visit: `Approved` -> `Completed`; a waiver still pending when the visit ended -> `Cancelled`. A requester can self-cancel a waiver that is `PendingDoctor` only (`cancelBlockingWaiverForResubmit`, L7766).

- **A TripTicket is created at submit** with status `PendingDoctor` and rotation 1, and its id is stored on the waiver (L7814). Final approval **activates** it (status `Approved`, doctor review fields filled from the waiver's Doctor step) and creates the gate state for the `WVR-` id (valid until the planned departure). The earlier docs say "auto ticket on approval"; the real flow creates it at submit.
- Approval email carries the waiver card and QR.

### 8.1 Waiver validation at submit (`validateWaiverPayload_`, L7400)

1. Applicant type `Government` or `VisitorCompany` (empty counts as VisitorCompany). **VisitorCompany is refused when the admin toggle `waiverVisitorCompanyEnabled` is N.**
2. Name cleaned; company must be picked from the company list (the medpost mirror). A **Government** applicant must pick a company marked GOV; a VisitorCompany applicant must **not** pick a GOV company.
3. Requester name; valid requester email whose domain can receive mail; phone valid.
4. Gender, date of birth, nationality, position, department, visit location and purpose present.
5. **NIK / passport number:** digits only 6 to 16 long, or letters and digits 6 to 9 long, upper-cased with other characters removed. (Looser than medpost's rule because visitors are not in medpost.)
6. **Duration 1 to `waiver_max_days` (7)**, from-date and to-date present, to >= from, **from-date not before today**.
7. **No blocking waiver** (section 8.2).
8. **Annual company cap, VisitorCompany only** (Government is exempt): `daysUsed + duration` must not exceed `waiver_company_cap_days_per_year` (14). Days used = for waiver tickets of the same company (name compared normalised) whose **check-in year (`RecordStart`) is the current year**, the sum of `TotalActualDays`.
9. Deviation reasons (identified risk, precaution, how to prevent) all required. If medicines are taken, medicine 1 name and dosage required.
10. Declaration accepted.
11. Uploads required: ID (KTP or passport), ID photo, appointment letter; **ITAS / e-visa for foreigners**, plus passport valid-until and visa valid-until (both not before today). Each file under the waiver size limit (700 KB, appointment letter 1 MB by `assertWaiverAssignmentUnder1Mb_`).

### 8.2 Blocking waiver (`findBlockingWaiverRow_`, L7495)

A previous waiver blocks a new one when it is **not terminal** (`Cancelled`, `Rejected`, `Completed`, `Expired`, or any status containing "not approved") **and its visit end date is today or later**, and it matches on:
- **NIK/passport**, or
- **name + date of birth**, or
- **name + company** (CHECK: two different people with the same name at one company block each other).

**For Government waivers only**, the blocking also requires the two visit date ranges to **overlap**; for VisitorCompany any matching open waiver blocks regardless of dates. The message offers self-cancel when the old one is `PendingDoctor`; the UI also offers it for `PendingStage1` (`cancelEligible`), but the server refuses anything except `PendingDoctor` (CHECK: inconsistent; port allows PendingDoctor only, UI shows the button only then).

### 8.3 Behaviour that looks unintended in waivers (CHECK; ported as is unless decided)

1. **Duration is taken from the form** (`durationDays`) and not recomputed from the from/to dates in this function; slice 3d verifies `computeWaiverDerivedFromForm_`. Port **derives it from the dates** (inclusive) and rejects a mismatch.
2. **Annual cap counts only days actually checked in** (and a waiver that was never used counts 0). Several waivers submitted before anyone checks in can exceed 14 days. Owner decision proposed: **count the requested days of every non-rejected, non-cancelled waiver in the year (by visit start) plus actual days where they differ**, or keep the old rule?
3. **Reviews ignore current status** (2.1).
4. The waiver ticket exists from submit (8) while old docs say it is created at approval.

## 9. Waiver departure job (`runWaiverDepartureCheck`, L10639; daily 06:00)

"Visit has ended" = the to-date is **before today** (calendar day).
- Waiver tickets (not closed): `Approved` -> `Closed`, reason `Planned departure passed`; `PendingDoctor` -> `Cancelled`, reason `Visit window ended before approval`. Other statuses untouched.
- Waivers: `Approved` -> `Completed`; `PendingDoctor`, `PendingStage1` or `PendingSiteManager` -> `Cancelled`.
- Gate state rows for `WVR-` permits that are active and whose valid-until is before today -> status `Expired`, inactive.
- Everything is marked for gate sync.

## 10. Phone numbers (L7081-7160; used by WEP and waivers)

Digits only. A stored number is split into a country code and local part by matching the longest known country code (list at L7110), defaulting to Indonesia (62) and treating a leading 0 as Indonesian. Valid when: country code up to 3 digits; Indonesia: local number (with its leading 0) is **10 to 13 digits**; any other country: **7 to 15 digits**. Emergency number must differ from the personal number. Port as one tested pure function (`phone` package) with the same country-code list.

## 11. New and changed Settings found in this slice

| Key | Default | Where |
|---|---|---|
| `rejection_note_min_words` | 3 | all reviews |
| `email_mx_check` | on | submit, waiver |
| `perpetual_cert_positions`, `perpetual_cert_valid_until` | Paramedic, Nurse, Doctor / Site Physician; 2090-12-31 | certificate rule |
| `waiver_visitor_company_enabled` | existing toggle | waiver |
| `waiver_max_days`, `waiver_company_cap_days_per_year`, `lep_expiry_grace_days`, `mcu_lep_grace_value` + `mcu_lep_grace_unit` (weeks or days) | existing | waivers, expiry, validity |
| Upload limits (500 KB / 700 KB / 1 MB) | existing group | uploads |

Not settings: the medpost API address and bearer token are secrets and live in `.env` (`MEDPOST_API_URL`, `MEDPOST_API_TOKEN`), never in the Settings table. Format rules (waiver NIK/passport letters and digits, phone lengths) stay in code as format validation, not business policy.

## 12. Golden test cases for slice 2c (synthetic names and dates)

Policy: MCU grace 2 weeks, expiry grace 14 days, waiver max 7 days, cap 14.

| # | Case | Expected |
|---|---|---|
| 1 | WNI, MCU valid until 2027-06-30, no contract | LEP valid until 2027-06-16, reason MCU |
| 2 | WNI, contract ends 2027-03-01, MCU 2027-06-30 | 2027-03-01, reason Contract |
| 3 | Foreign, MCU 2027-06-30, visa 2027-05-01, ITAS 2027-04-10, passport 2028-01-01 | 2027-04-10 (earliest visa date), reason Work visa |
| 4 | Foreign, passport 2027-02-01 earliest | 2027-02-01, reason Passport |
| 5 | WNI with visa and passport dates in the form | those dates ignored |
| 6 | grace weeks set to 0 | MCU date used as is |
| 7 | no usable date at all | error |
| 8 | Permit Approved, LEP valid until yesterday | job -> `Expired in grace period`, tickets locked |
| 9 | LEP date 14 days ago | still grace; 15 days ago -> `Expired` |
| 10 | LEP valid until today | no change |
| 11 | Duplicate: previous `PendingFinal`, same PersonalID | block |
| 12 | Duplicate: Approved, valid, same MCU date, WNI | block |
| 13 | Same, but new MCU valid-until differs | `reuse`, `mcuRenewal` |
| 14 | Same, Foreign, no visa date entered | `reuse`, `visaRenewalPending` |
| 15 | Same, medpost company now differs from the permit's stored company | `none` (allowed) |
| 16 | Previous `Resigned` or `Renewed by ...` | `none` |
| 17 | Final approval of a new LEP while an old LEP is Approved and another Expired, same PersonalID | both become `Renewed by LEP-new`; tickets of both locked; a Rejected permit untouched |
| 18 | Two reviewers approve the same `PendingFinal` permit at once | exactly one LEP issued, the other gets "already actioned" |
| 19 | Rejection note of 2 words / 3 words | refused / accepted |
| 20 | Waiver: Government, overlapping dates with an open waiver for same NIK | blocked; non-overlapping dates -> allowed |
| 21 | Waiver: VisitorCompany, open waiver for same name + company, different dates | blocked |
| 22 | Waiver: 8 days / 7 days | refused / accepted |
| 23 | Waiver: from-date yesterday | refused |
| 24 | Waiver cap: company used 10 days, request 5 / 4 | refused (15 > 14) / accepted |
| 25 | Waiver: Government company with VisitorCompany type and the reverse | refused |
| 26 | Waiver job: Approved, to-date yesterday | waiver `Completed`, ticket `Closed`, gate state `Expired` |
| 27 | Waiver job: PendingStage1, to-date yesterday | waiver `Cancelled`, ticket `Cancelled` |
| 28 | Waiver ID `1234567890123456` / `12345` / `AB12345` / `AB1234567890` | valid / too short / valid / too long |
| 29 | Phone `081234567890` / `0812345` / `+60123456789` | valid / invalid / valid |
| 30 | Review out of order (Doctor approves an `Approved` waiver) | refused (2.1) |
| 31 | Worker picked from medpost with no company or no MCU | permit refused |
| 32 | Medpost returns two people with the same name and different PersonalID | requester must pick one; the duplicate gate uses PersonalID, so they never block each other |
| 33 | Medpost record changes company between submit and Final approval | permit keeps the submit-time snapshot |

Real data check in slice 2c: for each of the 1,010 permits recompute `LEPValidUntil` and status-as-of-today from the stored dates and compare with the sheet's `LEPValidUntil` and `LEPStatus` columns; for each waiver check the cap and duplicate decisions the sheet implies. Mismatches are listed per row for the owner.

## 13. Open questions raised by this slice

1. **Review order:** enforce "only from the matching pending status" (recommended, 2.1)?
2. **Waiver annual cap:** count requested days of all non-rejected waivers (recommended), or keep "only days actually checked in" (8.3.2)?
3. **Expired file deletion:** stop deleting expired uploads automatically (recommended), keep a retention setting for later?
4. **Perpetual-certificate list** (Paramedic, Nurse, Doctor): confirm it moves to Settings (assumed yes).
5. **Medpost change:** add `personalId` to the `employees` directory response (planned in medpost, token-guarded). Confirm.
6. **Several parents:** when medpost lists more than one parent company for a worker's company, which one shows on the LEP card (proposed: lowest depth first)?
7. **MCU snapshot:** the permit keeps the MCU data seen at submit (as legacy did). Should Final approval re-read medpost and use a newer MCU instead?
8. **Position source:** no requester override (decided by owner: it comes from medpost). Confirm the importer's "position differs from medpost" list is wanted so medpost can be corrected before cutover.
