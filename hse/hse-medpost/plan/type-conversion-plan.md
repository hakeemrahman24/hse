# Type conversion plan — live MariaDB `hse_medpost` (draft, 2026-10-12)

Decision (2026-10-12): convert the `TEXT` keys, roles, statuses, dates and `double` measurements to proper types, as part of the migration baseline. Source of truth for the current schema: the schema-only `mysqldump` taken on the Pi (MariaDB 10.11.18). Nothing in this file has been run. Every statement below is a draft until the pre-checks pass.

Why it is cheap now: the database is about 3 MB (`McuRecord` about 2,163 rows, `Patient` 1,622), every tested date is valid `YYYY-MM-DD`, and the submission tables have never held a row. Restoring from a dump takes seconds.

## 1. Principles

- **No date or time column types.** The live database has none (checked: no `DATE`, `DATETIME`, `TIMESTAMP`, `TIME` or `YEAR` column exists), and this stays the rule. Dates and timestamps are **strings**: `CHAR(10)` and `CHAR(19)`. Converting `TEXT` to `CHAR` only changes the column length and index, never a value, and MariaDB and the driver never shift a time zone. The Go code already reads and writes strings, so nothing in the application changes shape. The remaining timestamp issue is that writers disagree on layout (space vs `T`) and the browser sends 24-character values; that is fixed in Go with one helper, not in the schema. Decided: timestamps UTC as `YYYY-MM-DDTHH:MM:SS` (the Pi clock is UTC, so existing rows already match), business dates in Asia/Jayapura (UTC+9). The Pi runs `STRICT_TRANS_TABLES`, so `MODIFY` fails loudly instead of truncating. See `emr-plan.md` §5 item 4.
- Statuses and roles become `VARCHAR(16)`, not `ENUM`, on existing tables. An unexpected value written by an older code path would otherwise fail the insert. The allowed values are enforced in Go and documented; tighten to `ENUM` later.
- Unique `TEXT ... USING HASH` indexes become ordinary B-tree unique indexes on `VARCHAR`.
- Free-text clinical columns (`*Notes`, `*Criteria`, `Recommendation`, `ResultNotes`, anamnesis-type fields) stay `TEXT`. The wide `McuRecord` layout is a deliberate design and is not changed.
- Foreign-key columns are already `int(11)` and are untouched.
- Collation stays `utf8mb4_unicode_ci`.
- SQLite (development) is unaffected: its `TEXT` affinity accepts the same strings. Only the MariaDB baseline changes.

## 2. Pre-checks (read-only, counts and lengths only — run on the Pi)

```sql
SELECT @@sql_mode;   -- must contain STRICT_TRANS_TABLES, or MODIFY can truncate silently

-- longest value per column to be shortened (pick VARCHAR sizes from these)
SELECT MAX(CHAR_LENGTH(Name)) name_len, MAX(CHAR_LENGTH(Gender)) gender_len, MAX(CHAR_LENGTH(BloodType)) blood_len,
       MAX(CHAR_LENGTH(EmployeeID)) emp_len, MAX(CHAR_LENGTH(WorkPosition)) pos_len, MAX(CHAR_LENGTH(PersonalID)) nik_len
FROM Patient;
SELECT MAX(CHAR_LENGTH(Username)) user_len, MAX(CHAR_LENGTH(PasswordHash)) hash_len FROM AppUserTable;
SELECT MAX(CHAR_LENGTH(StaffName)) staff_len, MAX(CHAR_LENGTH(Position)) staffpos_len FROM MedpostStaffTable;
SELECT MAX(CHAR_LENGTH(Name)) co_len, MAX(CHAR_LENGTH(LegacyCode)) code_len, MAX(CHAR_LENGTH(Abbreviation)) abbr_len FROM Company;
SELECT MAX(CHAR_LENGTH(CheckUpID)) checkup_len FROM McuRecord;

-- timestamp and date formats not yet verified (only ReviewDate, DateCheckUp, ResultValidUntil were)
SELECT CHAR_LENGTH(`Timestamp`) len, COUNT(*) n FROM McuRecord GROUP BY len;
SELECT CHAR_LENGTH(SubmissionDate) len, COUNT(*) n FROM McuRecord GROUP BY len;
SELECT CHAR_LENGTH(CreatedAt) len, COUNT(*) n FROM McuCertificateLog GROUP BY len;

-- decimals: any value with more than 2 decimals?
SELECT COUNT(*) FROM McuRecord WHERE HeightCm <> ROUND(HeightCm,2) OR WeightKg <> ROUND(WeightKg,2) OR BmiValue <> ROUND(BmiValue,2);

-- status and role values in use
SELECT Status, COUNT(*) FROM CheckUpCriteriaTable GROUP BY Status;
SELECT Role, COUNT(*) FROM PositionRoleTable GROUP BY Role;
SELECT Gender, COUNT(*) FROM Patient GROUP BY Gender;
SELECT BloodType, COUNT(*) FROM Patient GROUP BY BloodType;
```

Stop and fix the data first if: strict mode is off, any length exceeds the proposed size, a timestamp column has mixed lengths (`T` vs space format, 19 vs 16 characters), or a decimal would round.

## 3. Proposed statements (sizes adjusted after section 2)

```sql
-- users and staff
ALTER TABLE AppUserTable
  MODIFY Username VARCHAR(80) NOT NULL, MODIFY PasswordHash VARCHAR(255) NOT NULL,
  MODIFY Role VARCHAR(16) NOT NULL DEFAULT 'staff',
  DROP INDEX Username, ADD UNIQUE KEY uq_AppUser_Username (Username);
ALTER TABLE MedpostStaffTable
  MODIFY StaffName VARCHAR(150) NOT NULL, MODIFY Position VARCHAR(100) NULL,
  MODIFY Status VARCHAR(16) NOT NULL DEFAULT 'Active';
ALTER TABLE PositionRoleTable
  MODIFY PositionName VARCHAR(100) NOT NULL, MODIFY Role VARCHAR(16) NOT NULL,
  DROP INDEX PositionName, ADD UNIQUE KEY uq_PositionRole_Name (PositionName);

-- reference tables (same shape: unique Name, Status)
ALTER TABLE CheckUpCriteriaTable MODIFY Name VARCHAR(255) NOT NULL, MODIFY Status VARCHAR(16) NOT NULL DEFAULT 'Active',
  DROP INDEX Name, ADD UNIQUE KEY uq_CheckUpCriteria_Name (Name);
ALTER TABLE NationalityTable     MODIFY Name VARCHAR(100) NOT NULL, MODIFY Status VARCHAR(16) NOT NULL DEFAULT 'Active',
  DROP INDEX Name, ADD UNIQUE KEY uq_Nationality_Name (Name);
ALTER TABLE ProjectTable         MODIFY Name VARCHAR(150) NOT NULL, MODIFY Status VARCHAR(16) NOT NULL DEFAULT 'Active',
  DROP INDEX Name, ADD UNIQUE KEY uq_Project_Name (Name);
ALTER TABLE TaskCategoryTable    MODIFY Name VARCHAR(255) NOT NULL, MODIFY Status VARCHAR(16) NOT NULL DEFAULT 'Active',
  DROP INDEX Name, ADD UNIQUE KEY uq_TaskCategory_Name (Name);
ALTER TABLE EntityType           MODIFY Name VARCHAR(100) NOT NULL, MODIFY LegacyCode VARCHAR(32) NULL,
  DROP INDEX Name, ADD UNIQUE KEY uq_EntityType_Name (Name);

-- companies
ALTER TABLE Company
  MODIFY Name VARCHAR(255) NOT NULL, MODIFY LegacyCode VARCHAR(64) NULL, MODIFY Abbreviation VARCHAR(64) NULL,
  DROP INDEX Name, DROP INDEX LegacyCode,
  ADD UNIQUE KEY uq_Company_Name (Name), ADD UNIQUE KEY uq_Company_LegacyCode (LegacyCode);
ALTER TABLE CompanyRelation
  MODIFY RelationCode VARCHAR(64) NOT NULL, MODIFY Project VARCHAR(150) NULL,
  DROP INDEX RelationCode, ADD UNIQUE KEY uq_CompanyRelation_Code (RelationCode);

-- patients
ALTER TABLE Patient
  MODIFY Name VARCHAR(200) NOT NULL, MODIFY Gender VARCHAR(16) NULL, MODIFY DateOfBirth CHAR(10) NULL,
  MODIFY BloodType VARCHAR(8) NULL, MODIFY EmployeeID VARCHAR(64) NULL,
  MODIFY WorkPosition VARCHAR(150) NULL, MODIFY PersonalID VARCHAR(32) NULL,
  DROP INDEX idx_patient_name_dob, DROP INDEX idx_patient_personal_id,
  DROP INDEX idx_patient_name, DROP INDEX idx_patient_dob,
  ADD UNIQUE KEY uq_patient_name_dob (Name, DateOfBirth), ADD UNIQUE KEY uq_patient_personal_id (PersonalID),
  ADD KEY idx_patient_name (Name), ADD KEY idx_patient_dob (DateOfBirth);

-- MCU records (only the columns with a definite type; criteria/notes columns stay TEXT)
ALTER TABLE McuRecord
  MODIFY CheckUpID VARCHAR(64) NOT NULL,
  MODIFY `Timestamp` CHAR(19) NOT NULL,           -- only after the format check passes
  MODIFY ReviewDate CHAR(10) NOT NULL, MODIFY DateCheckUp CHAR(10) NULL,
  MODIFY ResultValidUntil CHAR(10) NULL, MODIFY SubmissionDate CHAR(10) NULL,
  MODIFY Age SMALLINT NULL,
  MODIFY HeightCm DECIMAL(6,2) NULL, MODIFY WeightKg DECIMAL(6,2) NULL, MODIFY BmiValue DECIMAL(6,2) NULL,
  MODIFY IsShortProject TINYINT(1) NOT NULL DEFAULT 0, MODIFY IsTerminal TINYINT(1) NOT NULL DEFAULT 1,
  MODIFY PersonalID VARCHAR(32) NULL, MODIFY BloodType VARCHAR(8) NULL, MODIFY WorkPosition VARCHAR(150) NULL,
  DROP INDEX CheckUpID, DROP INDEX idx_mcu_review_date,
  ADD UNIQUE KEY uq_mcu_checkup_id (CheckUpID), ADD KEY idx_mcu_review_date (ReviewDate);
```

Not in this pass (decide later, different risk): activity-log tables (`ActivityTable`, `EvidenceTable`, `DocumentTable`, `SupervisorTable` dates and link columns), `McuCertificateLog`, `McuEmailNotificationConfig`, `DirectoryAPIToken`. `ReviewResultCriteriaTable` and `WorkLocationTable` are already `VARCHAR`. The `TaskCategoryTable`, `ReviewResultCriteriaTable` and `WorkLocationTable` rows are rewritten by the Excel import (their next IDs are 3985, 1964 and 655 against 15, 6 and 2 rows), so they cannot gain foreign keys until the import updates rows in place.

## 4. Combine with the submission-table work (one maintenance window)

`DrugAlcoholScreening` and `FatigueScreening` have never held a row. The rename from `DrugAlcoholSubmission` / `FatigueSubmission` was done separately on 2026-10-20: the Go, SQLite and test changes are in the repo, and `migrations/mysql/019_rename_screening_tables.sql` is run by hand (its check query confirms `ActivityTable.fk_activity_submission` followed the rename). `020_fatigue_sleep_hours.sql` adds `Sleep24h`, `Sleep48h` and `AwakeHours`; convert them with the Score columns in this pass. In the same window:
- give the later-added `DrugAlcoholScreening` columns `DEFAULT ''` so MariaDB matches the SQLite behaviour;
- the timestamp columns are `VARCHAR(32)` today (fixed on 2026-10-17 after a production 500); the server now stores the 19-character UTC layout, so they can become `CHAR(19)` here without rewriting rows.

## 5. Execution

1. Rehearsal: on the Pi, restore a full `mysqldump` (with data) into a scratch database `hse_medpost_rehearsal`. Run sections 2 and 3 there. Point a copy of the app at it (`DB_NAME`) and smoke-test: login, patient search, open and save an MCU record, generate a certificate, run the near-expiry job, directory API call.
2. Production window: `systemctl --user stop hse-medpost`; `sudo mysqldump hse_medpost > ~/hse_medpost_pre_types_<date>.sql` (copy off the Pi); run section 3; start the service; smoke-test again.
3. Verify: row counts unchanged for every table; `SHOW CREATE TABLE` for each altered table matches this file; unique-key counts (`COUNT(DISTINCT Name, DateOfBirth)` etc.) equal the pre-change values.
4. Rollback: stop the service, `DROP DATABASE hse_medpost`, recreate it, restore the pre-change dump, start the previous binary. The database is small, so this takes seconds.
5. Commit the final statements as the first numbered MariaDB migration after the baseline dump, once the migration tool is chosen.

## 6. Go code to review before the window

- Any `Scan` of `IsTerminal`/`IsShortProject` (now `tinyint(1)`) and of the height/weight/BMI columns (now `DECIMAL`, returned as bytes by the driver; `database/sql` converts it to `float64`, but confirm no `interface{}` scan reads them).
- Anything comparing dates as strings or ordering by them: unchanged for ISO strings.
- `internal/screening/record.go` and `internal/directory/token.go` startup `ALTER TABLE` code: replace with migrations in the same release.
- Inserts that omit a column: `NOT NULL` columns without defaults will fail in strict mode (check `Timestamp`, `CheckUpID`).

## 7. Secrets found while auditing (2026-10-12)

- `McuEmailNotificationConfig.SmtpPassword`: one config row, password empty. Encrypt at rest (key from the environment, never stored in the database) before the first save.
- `DirectoryAPIToken.FullToken`: one real token stored in plaintext, and returned to the admin screen by design. Encrypt at rest to keep that screen working, then rotate the token, since the old value may be in backups. Hashing with show-once would be stronger but changes the admin screen and forces the consumer to re-key.
