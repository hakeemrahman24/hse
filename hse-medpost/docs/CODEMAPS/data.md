<!-- Generated: 2026-10-20 | Files scanned: migrations/sqlite (14), migrations/mysql (14) | Token estimate: ~800 -->
# Data

SQLite (dev/tests) auto-migrates from `migrations/sqlite/001..014` at start (+ `Ensure*` Go upgrades). MariaDB (prod): `migrations/mysql/011..024`, run by hand, backup first.

## Tables
Staff/auth: MedpostStaffTable, AppUserTable, PositionRoleTable
Activity log: HealthProcedureTable(=program) → SubHeaderTable → ActivityTable → EvidenceTable; DocumentTable, SupervisorTable, ProjectTable
Masters: Company (EntityType, CompanyRelation), Patient (→Company, NationalityTable), Region (Code PK, Name, Level 1-4, ParentCode; 91,599 rows)
MCU: McuRecord (→Patient, CheckUpCriteriaTable, TaskCategoryTable…), McuCertificateLog, McuEmailNotificationConfig (shelved)
Screening (tablet/worksite uploads, append-only): DrugAlcoholScreening, FatigueScreening
Medical Record (new): Visit, MalariaScreening, MalariaPurposeTable

```
Patient 1──* Visit(VisitCategory Outpatient|Screening|Wellness, FK_StaffID, FK_CompanyID?)
Visit   1──* MalariaScreening(MalariaID PK, FK_VisitID idx, FK_PurposeID→MalariaPurposeTable)
MalariaPurposeTable: 1 On arrival (default), 2 Symptomatic
MalariaScreening: TestDate, SlideInitial (3 letters+digit, unique per TestDate), AgeYears, WeightKg(0-150),
  HeadOfHousehold/HeadIsPatient, Regency/District/Village Code+Name (copied, no FK), WorkPosition, Company (copied),
  ExamType RDT|BloodSmear, Result NULL(Pending)|Negative|Pv|Pf|Po|Mixed
Test ID = "MAL-"+MalariaID(5 digits), computed, not stored
```
Planned, not built: VisitOutpatient, VisitVitals, Referral, SickLeave (see plan/emr-plan.md).

## Migration history (recent)
013/021 region · 014/022 visit+malaria · 023 malaria purpose · 024 MalariaID own key (mysql only; sqlite via emr.EnsureMalariaID)
019 rename screening tables · 018 audit/documents · 011-017 screening, fatigue, alcohol fields, test-log kind/title

## Corrective actions (sqlite 028 / mysql 042)
CorrectiveActionTable (finding, Program FK_ProcID, optional FK_ActivityID, PicName+PicEmployeeID text copies, Due, Severity, Status, closing date, action taken)
CorrectiveActionPhotoTable (Before|After, FileLink, SortOrder, ≤3 per phase) · CorrectiveActionFollowUpTable (own PIC text + due, Open|Done)
CorrectiveActionHistoryTable (edit history, goes with the finding on delete)
