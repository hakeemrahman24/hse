# EMR plan — clinical visits on top of the shared Patient table

Draft, 2026-10-12; visit taxonomy and outcomes revised 2026-10-20 after the project owner's answers. Extends `hse-medpost-plan.md` (the `emr` and screening sections of §2). Nothing here is built; `internal/emr/` is an empty directory. No DDL is final until the open questions at the bottom are answered.

## 1. Scope (from the project owner's outline)

`Patient` is the primary table. Every visit type references `FK_PatientID`.

A visit has one of three categories:

- **Outpatient**: triage is emergency or non-emergency.
- **Screening**: fatigue, malaria, or alcohol and drug.
  - Fatigue: simple physical exam, fatigue scoring.
  - Malaria: entered on the website only; there is no offline form (project owner, 2026-10-20). The columns the owner gave are in §4.2 (`MalariaScreening`). A malaria test can also be done inside an outpatient visit when the patient has a fever.
  - Alcohol and drug: the existing Drug & Alcohol consent and result form; its record is `DrugAlcoholScreening`.
- **Wellness**: blood pressure, body weight, cholesterol, and a blood test.

Outcomes of a visit, outpatient or screening. A visit can have more than one (for example sick leave plus a non-urgent referral):

- **Return to work**.
- **Referral**: urgent, non-urgent (can be done while on leave), or other. Medevac is an urgent referral.
- **Inpatient**: the patient is observed in the clinic. The stay is tracked (admitted, discharged), not just a handoff.
- **Sick leave**: the site is remote, so the patient rests in their own room for the stated dates.

## 2. Two layers

| Layer | Tables | Role |
|---|---|---|
| Screening records | `DrugAlcoholScreening`, `FatigueScreening` (renamed from `DrugAlcoholSubmission` and `FatigueSubmission` on 2026-10-20) | The record of every fatigue and drug/alcohol test, whether done in the clinic or at the worksite. One row per test, appended, never edited. Worker name, company and position are free text. `RawJSON` is the source of truth. The only update allowed is the patient/visit link (below). |
| Clinical | New `Visit` tables below | Outpatient, malaria, wellness and outcomes. Patient-linked, structured, correctable. A correction is recorded with a reason in `AuditLog`. |

**Two ways to do a fatigue or drug/alcohol test (decided 2026-10-20):**

| | In the clinic | At the worksite (random test) |
|---|---|---|
| Where | A screening page in the website | The offline form, on a phone or laptop |
| Form | The existing offline form served from the website with the patient pre-filled; same behaviour and same PDF output | The offline form as it is today |
| Patient | Picked from `Patient` before the test | Unknown; staff link it after upload |
| Visit | Created at the start, with a queue number | Created when staff link the upload; `VisitAt` is the test time, not the link time |
| Saved as | A submission row with `Source = Clinic` through the same upload endpoint | A submission row with `Source = Worksite` through the same upload |

Changes to both submission tables: `Source ENUM('Clinic','Worksite')`; `FK_PatientID` and `FK_VisitID`, both nullable; `LinkedAt` and `LinkedBy`; `FK_SupersedesID` nullable. A "needs linking" list shows worksite rows with no patient. Staff pick the patient from `Patient`; there is no automatic matching. Relinking changes only the link columns and writes an `AuditLog` row.

**Corrections never edit a row.** A retest or a mistake in the data is a new row whose `FK_SupersedesID` points at the old one; the old row stays. A visit has at most one current (non-superseded) screening row, checked in Go because MariaDB has no partial unique index.

The rename was done on 2026-10-20: the Go, SQLite and test changes are in the repo, and `migrations/mysql/019_rename_screening_tables.sql` is run by hand after a mysqldump, on a restored copy first. `ActivityTable.fk_activity_submission` follows the rename; 019 ends with a query that checks it. `migrations/mysql/013-018` are left as they were, since they record what already ran.

## 3. Patient identity

Live `Patient` has a unique index on `PersonalID` (NIK) and another on `(Name, DateOfBirth)`. Measured on the live database (2026-10-12): of 1,622 patients, 1,621 have no NIK, and all have a valid date of birth. So the working key is name plus date of birth, and NIK is almost unused. A visit stores `FK_PatientID`; staff search by name and birth date and see the company to tell people apart. 12 names appear more than once (different birth dates): homonyms or typos, needing manual review. Backfilling NIK is a data project, not a schema one.

## 4. Proposed structure (all new tables)

Revised 2026-10-20. Design sketch, not final DDL. Fatigue and drug/alcohol tests are not rebuilt here: their existing submission tables are the record (§2) and a `Visit` points to them. Key widths must match the existing `Patient`, `Staff` and `Company` keys.

### 4.1 Overview

```
Patient (existing)
  └─ Visit
       ├─ VisitVitals, VisitOutpatient                                                  (1:1 parts)
       ├─ MalariaScreening                                                              (1:many, own MalariaID)
       ├─ FatigueScreening, DrugAlcoholScreening   (via FK_VisitID, see §2)
       ├─ VisitLabResult  (N) → LabTest (lookup)
       ├─ VisitDiagnosis  (N) → Icd10 (lookup)
       ├─ Referral, Observation, SickLeave, ReturnToWork                              (outcomes, each 0..N)
       └─ PrescriptionLine (N)                                                         (phase 3)
  VisitQueue, AuditLog
```

### 4.2 Tables

```
Visit
  VisitID            INT UNSIGNED PK
  FK_PatientID       NOT NULL
  FK_CompanyID       NULL           company at the time of the visit (patients change employers)
  FK_SiteID          NOT NULL
  FK_StaffID         NOT NULL       attending
  VisitCategory      ENUM('Outpatient','Screening','Wellness')
  VisitAt            CHAR(19)       UTC
  ReportDate         CHAR(10)       clinic-zone business date
  Status             ENUM('Open','Closed','Amended')
  FK_ParentVisitID   NULL           return-to-work clearance, follow-ups
  ClosedAt           CHAR(19) NULL
  CreatedAt/By, UpdatedAt/By        audit columns

1:1 parts (PK = FK_VisitID)
  VisitVitals        SystolicBP, DiastolicBP, PulseBpm, RespRate SMALLINT UNSIGNED;
                     TempC DECIMAL(4,1); Spo2 TINYINT UNSIGNED;
                     HeightCm DECIMAL(4,1); WeightKg DECIMAL(5,1)
                     BMI and BP category are computed in Go, not stored.
  VisitOutpatient    Triage ENUM('Emergency','NonEmergency'), IsFollowUp TINYINT(1),
                     Anamnesis TEXT, PhysicalExam TEXT

Form-based screening tables (FatigueScreening, DrugAlcoholScreening)
  new columns         Source ENUM('Clinic','Worksite'), FK_PatientID NULL, FK_VisitID NULL,
                      LinkedAt NULL, LinkedBy NULL, FK_SupersedesID NULL
                      (the existing ClientID, FormVersion, CreatedAt, SubmittedAt, FK_StaffID and RawJSON stay)

Malaria (website only, no offline form)
  MalariaScreening   PK = MalariaID; FK_VisitID NOT NULL, not unique, so a visit can hold several tests (changed 2026-10-20, was 1:1 with PK = FK_VisitID). Provisional, from the project owner's column list (2026-10-20).
                     Patient and examiner come from Visit (FK_PatientID, FK_StaffID = the signed-in user).
                     TestDate CHAR(10)              defaults to the visit's date
                     SlideInitial VARCHAR(4)        3 letters from the patient's name + 1 digit (BSU1), written on the blood slide. Unique per TestDate (the digit separates people with the same letters, BSU1, BSU2, ... BSU9); one patient keeps the same code for all of that day's tests. Chosen by the server, checked in Go. (Added 2026-10-20.)
                     FK_PurposeID TINYINT           Why the test was done: references MalariaPurposeTable, 1 On arrival (default), 2 Symptomatic. Not the same as the visit category. (Added 2026-10-20.)
                     AgeYears TINYINT UNSIGNED      age at TestDate, from Patient.DateOfBirth, copied
                     WeightKg DECIMAL(4,1)          CHECK (WeightKg > 0 AND WeightKg <= 150)
                     HeadOfHousehold VARCHAR(150)   cleaned text (see rules)
                     HeadIsPatient TINYINT(1)       the "Sama dengan pasien" option
                     RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName
                                                    codes and names from the region data, both kept
                     WorkPosition VARCHAR(150), Company VARCHAR(150)   from the patient's latest MCU, copied at test time
                     ExamType ENUM('RDT','BloodSmear')
                     Result ENUM('Negative','Pv','Pf','Po','Mixed') NULL   NULL until a smear result is in
                     The week of the year is not stored: it is the ISO week of TestDate, computed in reports.

Lab values as rows
  LabTest            LabTestID PK, Code VARCHAR(20) UNIQUE, Name, Unit VARCHAR(20),
                     RefLow DECIMAL(8,2) NULL, RefHigh DECIMAL(8,2) NULL, IsActive TINYINT(1)
  VisitLabResult     VisitLabResultID PK, FK_VisitID, FK_LabTestID,
                     ValueNum DECIMAL(10,3) NULL, ValueText VARCHAR(50) NULL,
                     Flag ENUM('Normal','Low','High','Abnormal') NULL, TestedAt CHAR(19)
                     UNIQUE(FK_VisitID, FK_LabTestID)

Diagnoses
  Icd10              Code VARCHAR(10) PK, Name VARCHAR(255)        (import about 24,000 rows)
  VisitDiagnosis     VisitDiagnosisID PK, FK_VisitID, FK_Icd10Code, IsPrimary TINYINT(1)
                     UNIQUE(FK_VisitID, FK_Icd10Code)

Outcomes (each 0..N, independent)
  Referral           ReferralID PK, FK_VisitID, Urgency ENUM('Urgent','NonUrgent','Other'),
                     FK_ReferralDestinationID NULL, DestinationText VARCHAR(150) NULL,
                     Reason TEXT, ReferredAt CHAR(19),
                     Status ENUM('Planned','Done','Cancelled'), CompletedDate CHAR(10) NULL
  ReferralDestination  ReferralDestinationID PK, Name, IsActive
  Observation        ObservationID PK, FK_VisitID, AdmittedAt CHAR(19),
                     DischargedAt CHAR(19) NULL, Bed VARCHAR(20) NULL, Notes TEXT
  SickLeave          SickLeaveID PK, FK_VisitID, StartDate CHAR(10), EndDate CHAR(10),
                     RestLocation VARCHAR(100) DEFAULT 'Own room', Notes TEXT
                     CHECK (EndDate >= StartDate); days are computed, not stored
  ReturnToWork       ReturnToWorkID PK, FK_VisitID UNIQUE, ClearedAt CHAR(19), Restrictions TEXT NULL

Phase 3
  PrescriptionLine   PrescriptionLineID PK, FK_VisitID, FK_ItemID, Quantity DECIMAL(8,2),
                     Dosage VARCHAR(100), Status ENUM('Pending','Dispensed','Cancelled'),
                     DispensedBy NULL, DispensedAt CHAR(19) NULL

Queue and audit
  VisitQueue         VisitQueueID PK, QueueDate CHAR(10),
                     Service ENUM('Outpatient','Screening','Wellness','Pharmacy'),
                     QueueNumber SMALLINT, FK_PatientID, FK_VisitID NULL,
                     Status ENUM('Waiting','InProgress','Done','Cancelled')
                     UNIQUE(QueueDate, Service, QueueNumber)
  AuditLog           AuditLogID PK, EntityTable VARCHAR(50), EntityID INT UNSIGNED,
                     Action ENUM('Create','Update','Correct','Close'), FK_StaffID,
                     At CHAR(19), Reason VARCHAR(255) NULL, ChangeJSON TEXT NULL
```

Indexes: `Visit(VisitAt)`, `Visit(FK_PatientID, VisitAt)`, `Visit(FK_CompanyID, ReportDate)`, `VisitLabResult(FK_LabTestID, TestedAt)`, `VisitQueue(QueueDate, Service)`, `Referral(Status)`, `SickLeave(EndDate)`, `Observation(DischargedAt)`.

### 4.3 Rules

- **Parts allowed per category**, checked in the Go `visit` package when the visit is closed (a `CHECK` cannot span tables):

  | Category | Required part | Optional parts |
  |---|---|---|
  | Outpatient | `VisitOutpatient` | `MalariaScreening` |
  | Screening | exactly one of a `MalariaScreening`, a current `FatigueScreening` or a current `DrugAlcoholScreening` | none |
  | Wellness | `VisitVitals` | none |

  Whether an outpatient visit may also carry fatigue or alcohol/drug parts is open (§10 item 4); the default is no.
- **Malaria** is a clinical record keyed to the visit, not an upload: there is no offline form, so no `ClientID`, `RawJSON` or `Source`. It is editable until the visit closes; afterwards a correction goes through `Amended` plus an `AuditLog` row. A fever test in an outpatient visit is a `MalariaScreening` on that Outpatient visit; a routine screening is one on a Screening visit. The category already says which, so there is no `Context` or `EntryPoint` column. Count malaria tests from `MalariaScreening`. A positive `Result` can lead to a `Referral` or `SickLeave` on the same visit. A smear result that arrives later fills in `Result`.
- **Region fields** are a cascade: Regency, then District, then Village. The codes and names come from the Cahyadi region data (https://github.com/cahyadsn/wilayah). Both code and name are stored, so a record keeps its meaning if the source data changes.
- **Head of household** is plain text. The server cleans it: trim, collapse repeated spaces, remove characters that are not letters, spaces, `.`, `'` or `-`, then Title Case. The form mirrors this as the user types, but the server is the rule. The option "Sama dengan pasien" copies the patient's name and sets `HeadIsPatient`.
- **Outcomes are independent rows**, not a column on `Visit`. A non-urgent referral done while on leave is a `SickLeave` plus a `Referral` on the same visit. A positive malaria smear in a screening or an outpatient visit adds a `Referral` (or `SickLeave`) to that visit. "Return to work" is a `ReturnToWork` row. The UI shows a derived summary. A new outcome is one new table; `Visit` does not change.
- `Referral.Urgency`: `Urgent` includes medevac; `NonUrgent` can be done while on leave; `Other`.
- **Inpatient** is an observation stay inside the clinic, so `Observation` records admit and discharge times, not a hospital handoff.
- **Sick leave**: the site is remote, so the patient rests in their own room for the stated dates (`RestLocation`).
- **Return-to-work clearance** after sick leave or observation is a later visit whose `FK_ParentVisitID` points at the original.
- **Fatigue and drug/alcohol** records are the existing submission tables (§2). Scoring, breath and urine fields, signatures, photos and the PDF stay in the form; the table keeps the columns it has plus `RawJSON`. Blood pressure and pulse from the fatigue form are not copied into `VisitVitals`.
- **Lab values** (cholesterol total, LDL, HDL, triglycerides, glucose, uric acid, Hb) are rows in `VisitLabResult`, defined by `LabTest`. Changing a panel adds `LabTest` rows, not a migration. The malaria result stays in `MalariaScreening` because it is a coded result that drives referral logic. Vitals stay as columns.
- **Wellness** has no `VisitWellness` table yet: vitals and lab results cover blood pressure, weight, cholesterol and the blood test. Add the table when wellness gets fields of its own.
- **Link direction**: the foreign key lives on the submission tables (`FK_VisitID`), not on `Visit`, because there are two submission tables and one column on `Visit` would be ambiguous.
- **Closed visits are read-only.** A change sets status `Amended` and writes an `AuditLog` row with a reason; the signed original is untouched.
- One shared vitals table replaces the three shapes in use today (free-text `EmrTable.Vitals`, the planned `VitalSignsCheckTable`, the fatigue form's BP/pulse).
- `EmrTable` and `EmrMedicationTable` in the master plan fold into `Visit`/`VisitOutpatient`/`PrescriptionLine`; nothing is built, so there is no migration cost.

### 4.4 Scaling

Data volume is not the constraint (about 1,600 patients, a 3 MB database). The design targets change: a new screening type is one part table and one Go package; a new outcome is one table; a new lab test is a row; a second clinic is a `Site` row. Code is organised as one package per part under `internal/emr/` (`outpatient`, `malaria`, `referral`, and so on), each owning its table and validation, with a shared `visit` package for the header, status and locking.

## 5. Schema conventions for new tables

Existing tables: decided 2026-10-12 to convert the `TEXT` keys, roles, statuses and dates (usernames, `Role`, `Status`, `DateOfBirth`, MCU dates, and the `double` height/weight/BMI) to proper types as part of the baseline migration, because the live database is about 3 MB and every tested date is valid ISO. This is a backed-up production migration: validate with the count queries first, run it by hand on the Pi after a `mysqldump`, and keep a rollback script. The wide `McuRecord` criteria/notes layout stays as designed. Column-by-column statements, pre-checks, rehearsal and rollback are in [type-conversion-plan.md](type-conversion-plan.md). The convention list below applies to all new and converted tables.

1. PascalCase, no `Table` suffix; `<Table>ID` primary key; `FK_<Name>ID` foreign keys; `ON DELETE RESTRICT` for clinical data, `CASCADE` only for owned children.
2. Keys, statuses and dates are `VARCHAR`/`CHAR`/`ENUM`, never `TEXT`. Measurements are `DECIMAL`/`SMALLINT`.
3. `ENUM` (MariaDB) / `CHECK` (SQLite) for closed lists code branches on; lookup tables for staff-editable lists.
4. **No date or time column types, ever.** Verified on the live database (2026-10-12): there is no `DATE`, `DATETIME`, `TIMESTAMP`, `TIME` or `YEAR` column anywhere, and that stays the rule, so MariaDB and the driver never convert a time zone. All dates are `CHAR(10)` `YYYY-MM-DD`; all timestamps are `CHAR(19)`. Written through one helper in `internal/dates` so every writer uses the same layout and zone. Today the writers disagree: a space in `McuCertificateLog.CreatedAt` but `T` in the import, uploads and email job; the Pi's local time in some places and UTC in others; and the forms send a 24-character `toISOString()` value that does not fit `CHAR(19)`. **Decided 2026-10-12** (the Pi runs `Etc/UTC` with `STRICT_TRANS_TABLES`; the clinic is UTC+9):
   - Timestamps (an instant: created, submitted, logged, sent) are stored in **UTC** as `YYYY-MM-DDTHH:MM:SS`, no milliseconds, no `Z`. This is the format five of six writers already use, and since the Pi clock is UTC the existing rows already follow it, so nothing needs rewriting.
   - Business dates (what day it is at the clinic: expiry, report day, a date stamped on a record, "today") use the clinic zone **Asia/Jayapura (UTC+9)**, set once as a constant via `time.LoadLocation`, not read from the OS. `dates.Today()` already uses the clinic zone (`dates.ClinicNow`), so the night-shift date is correct.
   - Browser values (`toISOString()`, 24 characters with milliseconds and `Z`) are parsed and re-formatted to the 19-character UTC layout on the server (`dates.NormalizeTimestamp`), and an unparsable value makes that record invalid (it is not listed as accepted, so the form keeps it queued; the existing upload contract, not a `400`). `RawJSON` keeps the original. Without this, MariaDB strict mode rejects the value for a `CHAR(19)` `CreatedAt` column: production returned a 500 for fatigue uploads on 2026-10-17 and the columns were widened to `VARCHAR(32)` as a stop-gap (SQLite does not enforce `CHAR(n)`, so the tests passed). Done 2026-10-20 for both screening uploads.
   - One helper (`dates.NowStamp()`, with `dates.Stamp` and `dates.NormalizeTimestamp`) replaces every `time.Now().Format(...)` write; `McuCertificateLog.CreatedAt` changes from a space to `T` for new rows only. The helpers exist since 2026-10-20 and the two screening uploads use them. The other writers (`internal/entrymcu`, `internal/directory`) keep their own format until their tables are converted.
   - Historical rows are left as they are if each column is consistent (pre-check in `type-conversion-plan.md` §2).
5. Audit columns on every clinical table (created/updated at and by) plus one generic `AuditLog` (entity, entity ID, action, staff, time, reason).
6. Files on disk with path and SHA-256 in the database.
7. No startup `ALTER TABLE` in new code. Collation stays `utf8mb4_unicode_ci` like every live table.
8. Index at least: `Visit(VisitAt)`, `Visit(FK_PatientID, VisitAt)`, `VisitQueue(QueueDate, Service)`.

Migrations: add a tool (goose or golang-migrate) with a directory per driver. Commit a cleaned copy of the live `mysqldump --no-data` as the MariaDB baseline; production is marked as already at baseline.

## 6. What the old Flask clinic system (old-data/clinic_system) contributes

It was used very little (its dump shows 1 patient, 1 outpatient visit), so this is design reference, not a data migration. The April dump does hold 283 inventory items with their lookups, 276 stock batches and about 24,000 ICD-10 codes worth importing.

- Malaria screening fields: entry point (outpatient/home), context (symptomatic/routine), work related, special exam, smear result (Pf/Pv/Mixed/Po/negative), finalized flag, required disposition.
- Outpatient: vitals with stored categories (BP by JNC 7), anamnesis, physical exam, primary and secondary ICD-10, new vs follow-up, optional glucose/cholesterol/uric acid, report date shifted to the next day after a 17:00 cutoff (Asia/Jayapura, configurable).
- Prescribing is two-step: a clinician creates a pending line (no stock moved); the pharmacy dispenses it, deducting stock first-expired-first-out.
- Do not copy: hardcoded secrets and a default admin, no CSRF, no role checks on patient routes, `max+1` queue numbers, unlocked stock deduction, `LIKE`-based dispense reversal, a cached stock total next to batches, startup `ALTER TABLE`.

## 7. Port order (clinical first)

0. Before anything else (cheap now because the data is small and the submission tables are empty): the rename to `*Screening` and the raw sleep-hours columns are done (2026-10-20, `migrations/mysql/019` and `020`, to run by hand); still to do: fix the `DEFAULT ''` drift; check whether `SmtpPassword` and `FullToken` hold values, then encrypt them at rest; review the 12 repeated patient names by hand.
1. Foundation: migration tool and baseline schema (including the type cleanup above), timestamp helper, `Visit`, `VisitVitals`, `VisitQueue`, `AuditLog`.
2. Outpatient with ICD-10, outcomes (`Referral`, `Observation`, `SickLeave`, `ReturnToWork`); then malaria.
   Screening integration, after step 0: add the link and `Source` columns, the "needs linking" list, the supersede rule, and the in-clinic page that serves the offline forms with the patient pre-filled.
3. Prescribing, then batch-based inventory (`Item`, `StockBatch`, `StockTransaction`) with a Go transaction and `SELECT ... FOR UPDATE`; import the 283 items.
4. Later: wellness clinical record, consent/ROMIF, task checklist, reports, medical requests.

## 8. Access and privacy

Every clinical route needs an explicit role check (reuse `RequireDoctorOrCMO`). Companies see fit/unfit and leave dates, never diagnoses. Indonesia's data protection law (UU PDP) applies. `old-data/` holds patient data, credentials and database dumps: keep it out of git and rotate any secrets found there.

## 9. Corrections to the master plan (stale text)

- §4 "no stable identifier" is only partly resolved: `Patient.PersonalID` has a unique index but 1,621 of 1,622 rows are empty, so name plus date of birth remains the real key.
- Live measurements (2026-10-12): database about 3 MB; `McuRecord` about 2,163 rows (601 superseded), `Patient` 1,622; every tested date is valid ISO; `DrugAlcoholSubmission` and `FatigueSubmission` (now `DrugAlcoholScreening` and `FatigueScreening`) have never had a row, so the rename cost no data migration.
- `McuRecord` has `TaskCategoryNames` (text), not `FK_TaskCategoryID`.
- `AlcoholTestTable`, `DrugTestTable`, `FatigueSurveyTable` and `VitalSignsCheckTable` were never built; the submission tables are what exist.
- Live tables not in the plan: `ReviewResultCriteriaTable`, `WorkLocationTable`, `DirectoryAPIToken`.
- Live `DrugAlcoholScreening` (then `DrugAlcoholSubmission`) has about 20 columns that `011_screening.sql` lacks, because `internal/screening/record.go` adds columns at startup.
- The repo has no base MySQL schema.
- To verify: `McuEmailNotificationConfig.SmtpPassword` and `DirectoryAPIToken.FullToken` are plain `text` columns; confirm whether they are encrypted.
- Fatigue upload (checked 2026-10-20): `score_24h`, `score_48h` and `score_awake` are points (4 x (6 - X), 2 x (12 - Y), Z - 14), not hours. The form already uploads the raw hours as `sleep_24h`, `sleep_48h` and `awake_hours`, but the table had no columns for them, so they lived only in `RawJSON`. Fixed 2026-10-20: `Sleep24h`, `Sleep48h`, `AwakeHours` (`migrations/mysql/020`, `EnsureFatigueColumns`); no form change was needed. `feels_healthy`, `takes_medicine` and `medicine_name` are also sent and are still `RawJSON`-only.

## 10. Open questions

1. Malaria screening: the owner's columns are in §4.2. Only the region data source is still open (item 15).
2. Wellness vs MCU: how do they differ in practice? (MCU is a fitness certificate with a validity period.) Which `McuRecord` columns already hold blood and cholesterol?
3. ~~Fatigue: a quick daily check, or a full visit?~~ — decided 2026-10-20: a quick daily check. It needs no queue number and no visit workflow of its own beyond the link to a `Visit`.
4. ~~Can one visit contain more than one type?~~ — partly decided 2026-10-20: a malaria test can sit inside an outpatient visit (fever), with no `EntryPoint` column. Whether outpatient may also carry fatigue or alcohol/drug parts is open; default no.
5. Are patients only employees, or contractors and visitors too?
6. ~~Timezone rule for timestamp strings~~ — decided: UTC for timestamps, Asia/Jayapura (UTC+9) for business dates (§5 item 4).
7. Sick leave: required fields and the document it should produce (rest location is the patient's own room; does the company need a notice?).
8. Return-to-work: restrictions (light duty) as a free-text field, or structured?
9. ~~Alcohol and drug: what follows a non-negative result? Breath reading unit?~~ — decided 2026-10-20: the system does not track an HR report or add a medical outcome; it just shows the result (Negative, NonNegative, SentToLab). The breath reading unit is %, from the project owner's reply; confirm it against the form label.
10. Wellness blood test: which panel (Hb, glucose, others)? Cholesterol is total, LDL, HDL, triglycerides.
11. Observation stay: bed or room tracked, or only admit and discharge times?
12. Referral: destination as free text or a list of hospitals?
13. In-clinic forms: how the patient and visit IDs reach the form (URL parameter or message), and what the form does when it is opened without a patient.
14. ~~Malaria result: is a negative result recorded?~~ — decided 2026-10-20: yes, `Negative` is a value.
15. Region data: the cascade is Regency, District, Village (decided 2026-10-20) and the source is https://github.com/cahyadsn/wilayah. Open: I have not opened it. Is it a hosted API or a data repository, and should the website call it live or load a copy into our own database (a remote clinic's connection may be unreliable)? Which province or regencies are needed?
16. ~~Offline malaria form~~ — decided 2026-10-20: malaria has no offline form, so the patient search, the MCU lookup and the region lookup all happen on the website.
17. ~~Week of the year~~ — decided 2026-10-20: the week of the year; I read that as the ISO week (Monday start). Confirm if the HSE report uses another week numbering.
18. ~~Head of household~~ — decided 2026-10-20: text only, cleaned and Title Case, with a "Sama dengan pasien" option (see §4.3).
