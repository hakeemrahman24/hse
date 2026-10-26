# HSE Observation Database: Implementation Plan

Version 1.8 (2026-10-09) · Prepared for implementation in VSCodium · Language: English (form labels bilingual Indonesian / English)

This plan contains no code. Each phase lists what to build, the exact content, and a "Definition of Done" you can check. Work phase by phase and do not start a phase until the previous one passes its checks.

## Change log

**Version 1.8 (2026-10-09)**
- Dashboard also ranks the responsible companies by number of findings (top 12) with a pie chart of closed / in progress / open per company; the company name links to its list.
- Flow revised: the submitter of a SOC post can add After photos and close it from My submissions; verification waits for HSE staff and can be given after the post is closed (a closed, unverified post counts as waiting for verification).
- Observation dashboard shows three blocks (HSE recommendations, SOC, PMR status) with total/open/in progress/closed, and SOC and PMR add hazard category, area and subcontractor breakdowns (SOC also positive findings). A From/To date filter on the date of the finding applies to the dashboard, the list and the sidebar counts.
- HSE Observation screen: Medpost-style shell with a sidebar (Dashboard, Observations, hazard classes expanding to categories) and the dashboard, list or one post's details on the right. Rules: the creator edits their own input until the post is closed; a SOC post is edited by the employee who sent it until HSE verifies it; every other user can only add After evidence and move the post to In Progress or Closed; any staff user can verify a SOC post; a Closed post is locked for everybody; only administrators delete. New posts, including SOC positive findings, start Open. Column `created_by` added.
- Observation fields revised: Source (SOC, Internal Audit with Site/HO/KL, External Audit with 3rd Party/Government, Management Walkthrough, Project Monitoring Action Report), Category (Unsafe Action, Unsafe Condition, Positive Finding), Topic and Hazard (suggested from the description by the keyword list, editable), Area (list edited in Settings), Responsible company (from Medpost's company names), Verification (SOC posts only, set by HSE staff), Status (Open, In Progress, Closed). Severity removed. Closing needs at least one After photo or PDF (Positive Finding exempt). Evidence and After each take 4 photos and 2 PDFs; the public form takes photos only. Migrations renumbered 007 to 009.
- Photos in the SOC form accept JPEG, PNG, WebP and GIF and are always stored as JPEG (shown by every phone and browser). The visitor sees thumbnails: a preview with a remove button before sending, and thumbnails of their own photos on the thank-you page and "My submissions".
- Photos: up to 4 per post and each at most 500 KB, for the "before" photos on a post and the "after" photos documenting the action. Table `observation_photos` (kind before or after); files in the evidence folder; EXIF removed.
- Employee lookup for SOC uses hse-medpost's existing `/api/directory/employees` with its token (decided). Consequences: only people with a current Fit result can sign in, and Medpost's answer carries the date of birth, which the register compares in memory and discards. The typed date of birth is never put in a URL (a change from section 7.2, which wanted the comparison inside the API).
- SOC (Safety Observation Communication) is the public interface for everyone; the login page is for the HSE crew. Both write to the one observation table (SOC rows have source SOC and review status Pending). First build: SOC pages, name search, date-of-birth check and limits are coded with development stubs for the Medpost API and the Cloudflare check; the Medpost client, photo upload and keyword classification are not built yet.
- New section 8.0: the web application copies the look and behaviour of hse-medpost. Registered users (username and password) sign in and land on a home page with three cards (Medical Activities, view only; Work Permit Register; HSE Observation) and a Settings card for HSE Admin only. The SOC flow for employees is: Cloudflare check, name search, date of birth, sign in, observation form, thank-you page. `app_user` gets username, password hash and lock fields.
- Keyword list v6 adopted (files `SOC_Keywords_v6.csv`, `SOC_Topics_v6.csv`, `SOC_Hierarchy_v6.csv`, notes in `SOC_Keyword_Improvement_Plan_v6.md`). This replaces the earlier 26-topic, 222-keyword list. There are now 40 hazard topics and 7 control tags (PPE, Permit To Work, Barricade / Sign, Safety Meeting / Induction / Checks, Working Without Helper / Supervision, Emergency Preparedness & First Aid, Procedure & Competency) with 1,276 keywords. Section 5.6 (topic table) and 8.2a (rules) are rewritten.
- A control tag is not a hazard. A finding gets one hazard topic (the winner) and any number of control tags (new table `finding_control_tag`). Reports that only talk about a meeting, PPE or a sign get tags and no hazard topic.
- `ref_keyword` gets two columns: `weight` (1, or 2 for specific words) and `source` (where the keyword came from, so HSE can prune proposed ones).
- Matching rules tightened after checking v6: text and keywords are cleaned of punctuation, a keyword that sits inside a longer matched keyword of the same topic is not counted twice, and the topic's score is the sum of weights.
- v6 groups topics into 10 "working categories". These are not used. The hazard class is the 7 classes above, derived from the topic's hazard category. Seven categories were added for topics the 45 did not cover (52 in total): Confined Space, Hand & Power Tools, Infection / Illness, Natural Event, Welfare - Accommodation, Welfare - Facilities, Welfare - Workload & Treatment. The other v6 "(new)" categories map to existing ones.
- Test on the 242 real reports (my run, v6 as given): 207 get a hazard topic, 32 get control tags only, 3 get nothing; 30 of the 207 are score ties resolved by topic order. Weights change the winner on 8 reports, the double-count rule on 1. Not a measured accuracy: v6 was built from the same 242 reports, and 964 of its 1,276 keywords (75%) are unproven.
- Hazard classes reduced from 13 to 7: Safety, Physical, Chemical, Biological, Ergonomic, Psychosocial, Environmental. The 45 hazard categories stay as they were (52 after the v6 revision below) and are regrouped under the 7 classes (section 5.6). Radiation moves to Physical; Mechanical, Gravity & Ground, Electrical, Fire & Explosion, Security and Other move to Safety. Dashboards and filters by hazard class now show 7 values.
- Seed check: the seed command stops with an error if a topic's hazard is not one of the 52 categories. Seed files: `migrations/seed/ref_hazard_seed.csv` (class, category), `ref_soc_topic_seed.csv`, `ref_keyword_seed.csv`.
- Keyword list upkeep (revision): after go-live the `ref_keyword` table is the only live copy of the keyword list. New table `ref_keyword_log` records every change (who, when, old and new value). The first list is loaded by a repeatable seed from CSV. The admin screen gets an export button. The Markdown copy of the list is a one-time starting point and is not kept up to date. Section 8.2a items 10-14.
- Network access decided: Cloudflare Tunnel (`cloudflared`) for employees, Tailscale for HSE staff, admins, and the database administrator. Section 8.1b.
- The program has two separate listeners: a public one that serves only the SOC pages, and a private one on the Tailscale address that serves everything else. This is a design change from "one program, one door".
- Rate limits and the "device" in the sign-in rules use the visitor address that Cloudflare passes on, not the tunnel's local address.
- HTTPS for employees is handled by Cloudflare. Open item 27 is answered; three new questions are open items 29 to 31.
- Assumptions applied (not yet confirmed): the management / client dashboard is also reached through Tailscale; Medpost is reached over the site network or Tailscale.

**Version 1.7 (2026-10-08)**
- Stack decided: the application is written in Go, runs as one program on an Orange Pi 3B, and uses the MariaDB installed natively on the same board. Node.js and Express are removed from the plan.
- Storage: eMMC now, NVMe (M.2) later. Section 8.1a explains the move.
- Load: about 100 reports a month. This is small; the limiting factor is memory, not speed.
- Reports: the "dashboard report" is a PDF of the management dashboard, made on the server with the Go library `gopdf`. Charts in the PDF are drawn by the server (section 9.5). No headless browser.
- Memory: the board is described as 2 GB in your last message and 3 GB in the one before. The plan uses 2 GB as the safe case (open item 25).
- Turnstile: your description ("puzzle piece") does not match the Turnstile modes I know (open item 26).

**Version 1.6 (2026-10-08)**
- The SOC form no longer asks the employee for a sub-category. The employee writes the finding; the system picks the topic and the hazard category from keywords in the text (plain keyword matching, no AI service). The SOC form now has 10 fields.
- The employee can only see the result, not change it. HSE confirms or corrects it at review.
- New table `ref_keyword` and two new columns (50 `topic_source`, 51 `matched_keywords`).
- A first keyword list built from the 242 real descriptions is in the separate file `HSE_SOC_Keyword_List.md` (26 topics, 222 keywords). Test on the 242 rows: 237 matched, 5 not matched. This is not a measured accuracy: I checked the 68 rows with more than one candidate topic by eye, and about 8 looked wrong.
- The 15 old sub-categories are no longer shown to employees. They stay in the migration mapping (section 6.4a), and `ref_soc_topic` becomes an internal list (section 8.2a).

**Version 1.5 (2026-10-08)**
- Built from the real SOC file (242 responses, 27 Jan to 6 Oct 2026). Section 6.4a lists every value mapping found in it.
- One findings table for all sources is confirmed. A new optional column `source_reference` holds the report number of an Audit, Inspection, MWT meeting, or PMR report.
- New hazard category: Landslide / Slope Instability (now 45 categories).
- New areas: Main / Access Road, Parking Area, Workshop, and "Not specified" (legacy rows only).
- New SOC topics: Dust, Smoke & Air Quality; Camp, Water & Accommodation; Wildlife & Pests. "Other" now keeps a short typed text (`soc_topic_other_text`).
- New KPI: share of reports from the top reporter.

Assumptions applied in version 1.5 (not yet confirmed by you; change them if wrong):
1. "LNG" in the company column means LAYAR NUSANTARA GAS.
2. The 9 positive observations marked OPEN, and the 2 with a blank status, become CLOSED.
3. The three new areas, three new topics, and the Landslide hazard are approved.
4. `source_reference` is a single optional column, not a separate report table.

**Version 1.4 (2026-10-08)**
- Name search decided: minimum 3 letters, maximum 5 results. Showing the company next to duplicate names is accepted.

**Version 1.3 (2026-10-08)**
- SOC sign-in order changed: Cloudflare Turnstile first, then a searchable name dropdown, then the required date of birth.
- The dropdown is searched on the server as the employee types. The browser never receives the full employee list or any employee ID.
- The Medpost API now needs three functions: search names, verify (reference + date of birth), and look up by ID.
- Sections 7.1, 7.2, 7.3, 8.2, security, tests, and open items updated. Typing errors in names are no longer possible, which removes most of the name-matching problem.

**Version 1.2 (2026-10-08)**
- Sign-in for the SOC form is decided: the employee enters their full name and date of birth and ticks the Cloudflare Turnstile check. The name and date of birth are verified against the Medpost API, and the verified name is saved as the reporter name.
- New safeguards: server-side check of the Turnstile token, limits on failed attempts, date of birth never stored or logged, a limited "My submissions" page.
- Section 7.1 (login), 7.2 (API), 7.3 (endpoints), 8.2 (SOC form), security, tests, and open items updated. Open item 11 (login method) is replaced by five new items about the API verify function, Cloudflare, limits, name matching, and legal check.

**Version 1.1 (2026-10-08)**
- SOC now has its own simple form face for all employees: the same fields and wording as the current SOC table, minus the sub-category, which the system now picks from the description. The full form stays for HSE staff (Audit, MWT, Inspections, PMR).
- New HSE review step: SOC submissions arrive as "Pending" and HSE confirms the classification (hazard, risk, owner, due date).
- The 15 SOC sub-categories stay as the employee-facing "Topic" list. A mapping table (`ref_soc_topic`) turns each topic into the structured columns. The same mapping serves live entry and migration.
- New columns: `soc_topic_id`, `review_status`, `reviewed_by_*`, `reviewed_date`, `data_origin`, `legacy_key`. Reporter type gains "Legacy". `hazard_category_id` may be empty while a finding is Pending.
- Migration section rewritten from the real SOC sheet header (11 columns).
- New rules, endpoints, KPIs (review backlog), tests, and open items.

Version 1.0 is unchanged in the file `HSE_Observation_Database_Implementation_Plan.md`.

---

## 0. How to use this plan in VSCodium

1. Create the repository folder structure in section 4.
2. Save this file as `docs/IMPLEMENTATION_PLAN.md` in the repository.
3. Work through the phases in order (section 14 gives the order and relative size).
4. If you use an AI coding assistant extension, give it one phase at a time, together with this file and the Definition of Done for that phase. Ask it to show a plan for the phase before writing files, and review each file before it is saved.
5. Never commit passwords, API keys, or the `.env` file.

Note: VSCodium uses the Open VSX extension registry, not the Microsoft marketplace. Some extensions you may know from VS Code can be missing or have different names. Check each one in the Extensions panel before relying on it (section 3).

---

## 1. Project summary and scope

**Goal:** one MariaDB database that stores every HSE finding (Audit, MWT, Inspections, SOC, PMR), with a web form for entry, closure follow-up with HSE verification, and two dashboards (site HSE team, management/client).

**In scope**
- MariaDB schema, lookup data, views, database users
- Migration of existing Excel dashboard data and SOC Google Form responses
- Web application: two form faces (a simple SOC form for all employees and a full form for HSE), HSE review queue, follow-up screens, two dashboards, CSV/PDF export
- Employee lookup through the existing Medpost API (identity fields only)

**Out of scope for version 1**
- Incident register and TRIR/LTIFR (lagging indicators)
- Mobile native app
- Automatic notifications (email/WhatsApp); listed as a later improvement

---

## 2. Decisions locked and placeholders

| Item | Status | Value |
|---|---|---|
| Database | Locked | MariaDB |
| Sources | Locked | Audit, MWT, Inspections, SOC, PMR |
| Statuses | Locked | OPEN, INPROGRESS, CLOSED |
| Closure rule | Locked | Subcontractor completes the action, HSE verifies, then CLOSED |
| Risk matrix | Locked | 5×5 from the project JSA form, stored as 25 lookup rows (section 5.6) |
| Hazard list | Locked | 7 classes (Safety, Physical, Chemical, Biological, Ergonomic, Psychosocial, Environmental), 52 categories |
| Work Activity | Locked | 25 items, each mapped to an IOGP activity type |
| Life-Saving Rule | Locked | IOGP's 9 rules plus Other |
| Root Cause | Locked | 8 IOGP causal-factor groups |
| Employee data | Locked | Read through the Medpost API, identity fields only |
| Data entry | Locked | Web form |
| Dashboards | Locked | Two audiences, separate database access |
| Employee sign-in for the SOC form | Locked | Full name + date of birth, checked against the Medpost API, plus Cloudflare Turnstile tick-box |
| SOC form | Locked | Separate simple form face for all employees; HSE reviews and classifies each submission |
| Legacy SOC data | Locked | Loaded as Pending review, no risk rating, no due date |
| Review time limit for HSE | **PLACEHOLDER** | 2 working days |
| Closure days per risk level | **PLACEHOLDER** | Very High 1, High 3, Medium 14, Low 30 (one lookup table, change later) |
| Medpost API address, authentication, fields | **PLACEHOLDER** | To be provided later |
| Web stack | Locked | Go, one program, server-rendered pages; deployed on Orange Pi 3B (section 8.1, 8.1a) |
| Web login method | **TO DECIDE** | Section 7.1 |
| MariaDB version, hosting, evidence storage location | **TO CONFIRM** | Section 5.1 |
| Plant Operations (GPP), Diving / Underwater Work | **TO CONFIRM** | Created but inactive until confirmed |

---

## 3. Environment setup (VSCodium)

**3.1 Tools to install on the development computer**
- VSCodium
- Git
- MariaDB server (same major version as production) or a container with it
- Go toolchain (current stable) on the development computer, with cross-compiling to Linux ARM64 for the Orange Pi
- A MariaDB/MySQL client extension for VSCodium (check Open VSX for availability), or a standalone client such as HeidiSQL or DBeaver if none is suitable

**3.2 Recommended extensions (verify each on Open VSX)**
- A MariaDB/MySQL client or SQL editor
- Markdown preview (built in)
- The Go extension with the `gopls` language server (verify it is available on Open VSX) and `gofmt` on save
- EditorConfig support
- A REST client for testing the API (or use a standalone tool)

**3.3 Environments**

| Environment | Purpose | Database name |
|---|---|---|
| Local development | Your computer | `hse_obs_dev` |
| Test | Shared testing with sample data | `hse_obs_test` |
| Production | Real data | `hse_obs` |

**3.4 Configuration rules**
- All connection settings and keys live in a `.env` file per environment. Commit only `.env.example` with empty values.
- Add `.env`, uploaded evidence folders, and backups to `.gitignore`.
- Time zone: store dates in local time (Asia/Jakarta, UTC+7) and make the application and database agree on it.

---

## 4. Repository structure

```
hse-observation/
├── README.md
├── .gitignore
├── .env.example
├── docs/
│   ├── IMPLEMENTATION_PLAN.md        (this file)
│   ├── data-dictionary.md            (generated from section 5 during Phase 1)
│   └── runbook.md                    (backup, restore, user management)
├── database/
│   ├── migrations/                   (numbered, one change per file, never edited after release)
│   │   ├── 001_reference_tables.sql
│   │   ├── 002_main_tables.sql
│   │   ├── 003_constraints_and_indexes.sql
│   │   ├── 004_views_site.sql
│   │   ├── 005_views_management.sql
│   │   └── 006_database_users.sql
│   ├── seed/                         (lookup data, one file per list)
│   ├── sample-data/                  (the 10 test rows, test database only)
│   └── tests/                        (SQL test scripts and expected results)
├── migration-tools/                  (Excel and Google Form import scripts and mapping tables)
├── backend/                          (Go module: cmd/ for the program, internal/ for the API, rules, and PDF code)
├── frontend/                         (Go HTML templates, static files, Chart.js file kept locally, no internet CDN)
└── ops/                              (backup scripts, deployment notes)
```

Rules: one migration file per change; files are numbered and applied in order; a released migration is never edited, a new one is added instead.

---

## 5. Phase 1: Database

### 5.1 Conventions
- Engine InnoDB, character set utf8mb4, collation utf8mb4_unicode_ci.
- Names in snake_case. Lookup tables start with `ref_`. Primary key column is `id`. Foreign key columns end with `_id`.
- Use `TINYINT UNSIGNED` for lists under 255 rows, `SMALLINT UNSIGNED` for larger lists.
- Every lookup table has `sort_order` and `is_active`. Items are deactivated, never deleted, so old records keep their meaning.
- Confirm the MariaDB version first. Version 10.5 or later is recommended so that CHECK constraints are enforced and generated columns are available.
- Decide the evidence storage location now: a folder on the application server or a shared drive. The database stores only the path.

### 5.2 Build order

1. Lookup tables (no dependencies)
2. Main table `hse_findings`
3. Supporting tables (manhours, audit log, application users)
4. Constraints and indexes
5. Views
6. Database users and privileges
7. Seed data
8. Tests

### 5.3 Main table: `hse_findings`

| # | Column | Type | Null | Notes |
|---|---|---|---|---|
| 1 | id | BIGINT UNSIGNED, PK, auto increment | No | Replaces "NO" |
| 2 | finding_date | DATE | No | Cannot be in the future |
| 3 | source_id | TINYINT UNSIGNED, FK ref_source | No | |
| 4 | audit_type_id | TINYINT UNSIGNED, FK ref_audit_type | Yes | Only when source = Audit |
| 5 | company_id | SMALLINT UNSIGNED, FK ref_company | No | Subcontractor |
| 6 | area_id | SMALLINT UNSIGNED, FK ref_area | No | |
| 7 | hazard_category_id | SMALLINT UNSIGNED, FK ref_hazard_category | Yes (until reviewed) | One primary hazard. Required once review_status is Reviewed |
| 8 | work_activity_id | SMALLINT UNSIGNED, FK ref_work_activity | Yes | |
| 9 | life_saving_rule_id | TINYINT UNSIGNED, FK ref_life_saving_rule | Yes | |
| 10 | root_cause_id | TINYINT UNSIGNED, FK ref_root_cause | Yes | |
| 11 | observation_type_id | TINYINT UNSIGNED, FK ref_observation_type | No | |
| 12 | finding_description | TEXT | No | |
| 13 | recommendation | TEXT | Yes | |
| 14 | action_taken | TEXT | Yes | |
| 15 | severity_id | TINYINT UNSIGNED, FK ref_severity | Yes | 1 to 5. Required for unsafe and near-miss |
| 16 | severity_basis | ENUM('Personnel','Environment','Asset','Reputation') | Yes | Which category drove the rating |
| 17 | likelihood_id | TINYINT UNSIGNED, FK ref_likelihood | Yes | 1 to 5. Required for unsafe and near-miss |
| 18 | due_date | DATE | Yes | Finding date plus closure days of the risk level; editable by HSE |
| 19 | status_id | TINYINT UNSIGNED, FK ref_status | No | Default OPEN |
| 20 | action_owner_employee_id | VARCHAR(30) | Yes | From Medpost API |
| 21 | action_owner_name | VARCHAR(100) | Yes | Snapshot, or typed if not in API |
| 22 | action_completed_date | DATE | Yes | Set by the action owner |
| 23 | verified_by_employee_id | VARCHAR(30) | Yes | |
| 24 | verified_by_name | VARCHAR(100) | Yes | Snapshot |
| 25 | verified_date | DATE | Yes | |
| 26 | verification_remarks | TEXT | Yes | |
| 27 | closed_date | DATE | Yes | Equals verified date for normal closure |
| 28 | is_repeat | BOOLEAN | No | Default 0 |
| 29 | repeat_of_id | BIGINT UNSIGNED, FK to hse_findings(id) | Yes | |
| 30 | evidence_before | VARCHAR(500) | Yes | Path to the finding photo |
| 31 | evidence_after | VARCHAR(500) | Yes | Path to the closure photo |
| 32 | reporter_type | ENUM('Employee','External','Legacy') | No | Legacy = name typed in the old Google Form, not matched to an employee |
| 33 | reporter_employee_id | VARCHAR(30) | Yes | Empty for External and Legacy |
| 34 | reporter_name | VARCHAR(100) | No | Snapshot from API, or typed for External |
| 35 | reporter_designation | VARCHAR(100) | Yes | Snapshot from API |
| 36 | reporter_email | VARCHAR(150) | Yes | |
| 37 | form_timestamp | DATETIME | Yes | Original Google Form time for migrated rows |
| 38 | created_by | VARCHAR(30) | No | Application user who entered the record |
| 39 | created_at | TIMESTAMP | No | Default now |
| 40 | updated_at | TIMESTAMP | No | Updates automatically on edit |
| 41 | soc_topic_id | SMALLINT UNSIGNED, FK ref_soc_topic | Yes | Hazard topic chosen by the keyword rules (kind = hazard), changed by HSE at review. Control tags are in `finding_control_tag` |
| 42 | review_status | ENUM('Pending','Reviewed') | No | Reviewed for entries made on the full HSE form; Pending for SOC employee entries and legacy rows |
| 43 | reviewed_by_employee_id | VARCHAR(30) | Yes | |
| 44 | reviewed_by_name | VARCHAR(100) | Yes | Snapshot |
| 45 | reviewed_date | DATE | Yes | |
| 46 | data_origin | ENUM('Web SOC form','Web full form','Google Form','Excel') | No | |
| 47 | legacy_key | VARCHAR(64), unique | Yes | Hash of timestamp, name, and date for migrated rows; stops duplicates when an import is repeated |
| 48 | soc_topic_other_text | VARCHAR(100) | Yes | Short text typed when the SOC topic is Other |
| 49 | source_reference | VARCHAR(100) | Yes | Report or meeting number for Audit, Inspections, MWT, PMR (for example audit report number); empty for SOC |
| 50 | topic_source | ENUM('Auto','HSE','Employee','Legacy') | No | Who set the topic and hazard: Auto = keywords, HSE = set or corrected at review, Employee = old dropdown choice (legacy rows), Legacy = migrated |
| 51 | matched_keywords | VARCHAR(255) | Yes | Keywords that triggered the automatic topic (for HSE to see why, and for tuning) |

Rows 41 to 47 were added in version 1.1, rows 48 and 49 in version 1.5, and rows 50 and 51 in version 1.6.

Implementation notes:
- Values suggested from the SOC topic (hazard, work activity, life-saving rule, root cause) are written into their columns as provisional. They count in breakdowns only after review_status is Reviewed; before that the breakdowns show the finding as "Unclassified".
- The risk level is **not stored**. It is derived from severity and likelihood through `ref_risk_matrix` in the views. This keeps one source of truth if the matrix is ever revised.
- The due date **is stored**, so historical due dates do not move when closure days change later.
- The Hazard Class is derived from the Hazard Category in the views.

### 5.4 Supporting tables

| Table | Columns | Purpose |
|---|---|---|
| `company_monthly_manhours` | id, company_id (FK), year_month (DATE, first of month), manhours (optional), headcount (optional), unique on company and month | Optional normalization |
| `finding_audit_log` | id, finding_id (FK), changed_at, changed_by, field_name, old_value, new_value, reason | Every change to status, due date, risk fields, verification, and reopening |
| `app_user` | id, username (unique), password_hash (bcrypt), display_name, role, employee_id (optional), is_active, failed_attempts, locked_until, last_login | Accounts of registered users (username and password); roles in 7.1. Employees using the SOC form have no account |
| `ref_soc_topic` | id, name_id, name_en, kind (ENUM 'hazard','control'), topic_group (optional, for example Welfare), suggested_hazard_category_id (empty for control tags), suggested_work_activity_id, suggested_life_saving_rule_id, suggested_root_cause_id, sort_order, is_active | Internal list of 40 hazard topics and 7 control tags with default classification (section 5.6) |
| `finding_control_tag` | finding_id (FK), topic_id (FK ref_soc_topic, kind = control), tag_source (ENUM 'Auto','HSE','Legacy'), matched_keywords (VARCHAR 255), primary key on finding and topic | Control tags of a finding, set by the keyword rules and edited by HSE at review. Provisional until review_status is Reviewed |
| `ref_keyword` | id, topic_id (FK ref_soc_topic), keyword (VARCHAR 60, stored without punctuation, lower case), match_type (ENUM 'whole','prefix'), weight (TINYINT, 1 or 2), source (VARCHAR 80), is_active, created_by, created_at, updated_by, updated_at, unique on topic and keyword | Keywords that trigger a topic (section 8.2a). HSE Admin edits them in a screen, without code changes. This table is the only live copy of the list |
| `ref_keyword_log` | id, keyword_id (FK ref_keyword), changed_at, changed_by, action (ENUM 'add','edit','switch_off','switch_on'), old_topic_id, new_topic_id, old_keyword, new_keyword, old_match_type, new_match_type, reason | One row for every change to a keyword (section 8.2a item 11). Rows are never edited or deleted. A keyword is never hard-deleted, only switched off |

### 5.5 Rules to enforce

Rules that depend on columns of the same row go in database CHECK constraints. Rules that depend on a lookup flag (for example "observation type requires risk") are enforced in the API and, as a second safety, by triggers.

| # | Rule | Where |
|---|---|---|
| 1 | audit_type_id is required when source is Audit and must be empty otherwise | CHECK, since source ids are fixed |
| 2 | severity_id and likelihood_id are both filled or both empty | CHECK |
| 3 | For observation types that need risk (Unsafe Action, Unsafe Condition, Near-Miss), severity and likelihood are required once the finding is Reviewed; for positive types they must be empty | API and trigger |
| 4 | due_date is not earlier than finding_date | CHECK |
| 5 | When status is CLOSED, closed_date is required | CHECK |
| 6 | For findings that need follow-up, CLOSED also requires verified_by, verified_date, and action_completed_date. Legacy rows are exempt (rule 17) | API and trigger |
| 7 | verified_date is not earlier than action_completed_date, and action_completed_date is not earlier than finding_date | CHECK |
| 8 | repeat_of_id requires is_repeat to be true, and a finding cannot repeat itself | CHECK |
| 9 | reporter_employee_id is required for Employee reporters, empty for External | CHECK |
| 10 | Positive observations (Safe Action, Safe Condition) are created with status CLOSED, no due date, and no verification | API and trigger |
| 11 | Every foreign key is enforced; deleting a lookup row that is in use is blocked | Foreign keys with restrict |
| 12 | Every change to a risk field, due date, status, or verification fields is written to `finding_audit_log` | Trigger or API |
| 13 | review_status = Reviewed requires hazard_category_id | CHECK |
| 14 | A Reviewed row has reviewed_by and reviewed_date; SOC employee entries and legacy rows start Pending | API and trigger |
| 15 | Findings that need follow-up cannot be verified or closed while Pending. Positive observations may be CLOSED while Pending | API and trigger |
| 16 | legacy_key is unique; data_origin is required | Unique index |
| 17 | Legacy rows (data_origin Google Form or Excel) have no severity, likelihood, or due date. They are excluded from overdue and on-time KPIs until HSE rates them | Views |
| 18 | When HSE reviews a SOC finding, due_date = review date + closure days of its risk level (HSE can edit with a reason) | API |

### 5.6 Seed data (lookup content)

**ref_source (5):** Audit, MWT, Inspections, SOC, PMR

**ref_audit_type (5):** Internal Audit group: Site Audit, HO Audit, KL Audit. External Audit group: 3rd Party Company, Government. Columns: id, audit_group, name.

**ref_company (9 + Other):** LAYAR NUSANTARA GAS, STARWAY ENGINEERING INDONESIA, SALAWATI MOTORINDO, ZHONGSIE CECLL, ELECO CNCEC, TECTONA MITRA UTAMA, NANGGALA 28, POWERCHINA, ARTAMA SENTOSA INDONESIA, Other

**ref_area (13 + Not specified + Other):** Argo 5, Argo 6, Batching Plant, Camp, Condensate Area, GPP, Jetty Nagote, Laydown, Office, PCC / TCF, Main / Access Road, Parking Area, Workshop, Other. "Not specified" is used only for legacy rows and is hidden from the forms (is_active = 0).

**ref_status (3):** OPEN, INPROGRESS, CLOSED

**ref_observation_type (6)**

| Name | is_positive | needs_risk | needs_followup |
|---|---|---|---|
| Safe Action | Yes | No | No |
| Safe Condition | Yes | No | No |
| Unsafe Action | No | Yes | Yes |
| Unsafe Condition | No | Yes | Yes |
| Near-Miss | No | Yes | Yes |
| Suggestion | No | No | Optional (HSE decides) |

**ref_hazard_class (7) and ref_hazard_category (52)**

The 7 classes are the common workplace hazard types. The 52 categories are the earlier 45 plus 7 added in the keyword list v6 revision (marked *new*). The class of a finding is derived from its category.

| Class | Categories |
|---|---|
| Safety (25) | Machinery & Work Equipment; Mechanical Lifting / Load Handling; Vehicles & Mobile Equipment; Pressurized / Vacuum Systems; Stored Energy; Caught In / Between; Struck By; Cuts & Sharp Edges; Fall from Height; Dropped / Falling Objects; Slips, Trips & Falls; Excavation Collapse / Underground Services; Structural Collapse; Landslide / Slope Instability; Electricity; Fire; Explosion / Flammable Atmosphere; Loss of Containment; Hot Surfaces / Burns; Drowning / Work Over Water; Unauthorized Access / Theft; Community & Civil Disturbance; Other; Confined Space *new*; Hand & Power Tools *new* |
| Physical (7) | Noise; Vibration; Heat Stress; Poor Lighting; Extreme Weather / Lightning; Ionizing Radiation; Non-Ionizing Radiation |
| Chemical (3) | Hazardous Substances; Toxic Gas / Oxygen Deficiency; Dust & Welding Fumes |
| Biological (3) | Infection / Contaminated Food or Water; Animals, Insects & Plants; Infection / Illness *new* |
| Ergonomic (2) | Manual Handling; Upper-Limb Disorders & Workstation |
| Psychosocial (7) | Mental Ill-Health & Stress; Violence & Harassment; Substance Abuse; Fatigue & Working Hours; Welfare - Accommodation *new*; Welfare - Facilities *new*; Welfare - Workload & Treatment *new* |
| Environmental (5) | Spill / Leakage; Waste Management; Air Emission & Dust; Land Disturbance & Biodiversity; Natural Event *new* |

Safety is the largest class, so a dashboard by class is dominated by Safety; use the category for detail. "Other" sits under Safety. The three Welfare categories sit under Psychosocial (as in v6); HSE can move them in the category list if it prefers another class.

Each category also has a short description for the form's help text. Sources of this list: the 7 common workplace hazard types (Safety, Physical, Chemical, Biological, Ergonomic, Psychosocial, Environmental), the IOGP incident/event cause list (Safety data reporting user guide, 2025 data, section 3.10), and the IOGP Line of Fire categories.

**ref_soc_topic (40 hazard topics + 7 control tags): internal list with default classification**

Employees never see this list (section 8.2a). The topic is chosen by the keyword rules; HSE can change it at review. A hazard topic gives the suggested hazard category (and with it the class); a control tag is stored apart from the hazard (section 5.3, `finding_control_tag`). `sort_order` is the priority used to break score ties (lower number wins). A blank cell means HSE chooses at review.

| Order | Topic | Kind | Group | Suggested hazard category | Suggested work activity | Suggested life-saving rule | Suggested root cause |
|---|---|---|---|---|---|---|---|
| 1 | Working at Height | Hazard |  | Fall from Height |  | Work at height |  |
| 2 | Confined Space | Hazard |  | Confined Space |  | Confined space |  |
| 3 | Hot Work | Hazard |  | Fire | Welding, Cutting & Grinding | Hot work |  |
| 4 | Fire & Ignition | Hazard |  | Fire |  |  |  |
| 5 | Process Safety / Hydrocarbon Release | Hazard |  | Loss of Containment |  |  |  |
| 6 | Lifting Operation | Hazard |  | Dropped / Falling Objects | Lifting Operation | Safe mechanical lifting |  |
| 7 | Excavation Work | Hazard |  | Excavation Collapse / Underground Services | Excavation & Earthworks |  |  |
| 8 | Electrical Work | Hazard |  | Electricity | Electrical & Instrument Work |  |  |
| 9 | Machinery & Energy Isolation | Hazard |  | Machinery & Work Equipment |  | Energy isolation |  |
| 10 | Line of Fire | Hazard |  | Struck By |  | Line of fire |  |
| 11 | Hand & Power Tools | Hazard |  | Hand & Power Tools |  |  |  |
| 12 | Transportation | Hazard |  | Vehicles & Mobile Equipment | Transportation (Land) | Driving |  |
| 13 | Wildlife & Pests | Hazard |  | Animals, Insects & Plants |  |  |  |
| 14 | Road Condition | Hazard |  | Slips, Trips & Falls |  |  |  |
| 15 | Marine & Working Over Water | Hazard |  | Drowning / Work Over Water |  |  |  |
| 16 | Housekeeping | Hazard |  | Slips, Trips & Falls | Housekeeping & Waste Handling |  |  |
| 17 | Waste & Environmental Control | Hazard |  | Waste Management | Housekeeping & Waste Handling |  |  |
| 18 | Dust, Smoke & Air Quality | Hazard |  | Air Emission & Dust |  |  |  |
| 19 | Hazardous Chemicals | Hazard |  | Hazardous Substances |  |  |  |
| 20 | Landslide | Hazard |  | Landslide / Slope Instability |  |  |  |
| 21 | Welfare - Sanitation & Drinking Water | Hazard | Welfare | Infection / Contaminated Food or Water | Camp, Catering & Welfare |  |  |
| 22 | Welfare - Accommodation & Laundry | Hazard | Welfare | Welfare - Accommodation | Camp, Catering & Welfare |  |  |
| 23 | Welfare - Rest Area, Shelter & Canteen | Hazard | Welfare | Welfare - Facilities | Camp, Catering & Welfare |  |  |
| 24 | Health & Infectious Disease | Hazard |  | Infection / Illness |  |  |  |
| 25 | Lighting | Hazard |  | Poor Lighting |  |  |  |
| 26 | Noise | Hazard |  | Noise |  |  |  |
| 27 | Radiation & Vibration | Hazard |  | Ionizing Radiation |  |  |  |
| 28 | Sharp Edges | Hazard |  | Cuts & Sharp Edges |  |  |  |
| 29 | Pressurized | Hazard |  | Pressurized / Vacuum Systems |  |  |  |
| 30 | Spill | Hazard |  | Spill / Leakage |  |  |  |
| 31 | Heat Stress | Hazard |  | Heat Stress |  |  |  |
| 32 | Natural Events / Weather | Hazard |  | Natural Event |  |  |  |
| 33 | Manual Handling & Ergonomics | Hazard |  | Manual Handling |  |  |  |
| 34 | Fatigue / Rest | Hazard |  | Fatigue & Working Hours |  |  |  |
| 35 | Welfare - Workload & Fair Treatment | Hazard | Welfare | Welfare - Workload & Treatment | Camp, Catering & Welfare |  |  |
| 36 | Bullying | Hazard |  | Violence & Harassment |  |  |  |
| 37 | Security & Intrusion | Hazard |  | Unauthorized Access / Theft |  |  |  |
| 38 | Community & Civil Unrest | Hazard |  | Community & Civil Disturbance |  |  |  |
| 39 | Third-Party Interference | Hazard |  | Unauthorized Access / Theft |  |  |  |
| 40 | Violence & Threats from Outsiders | Hazard |  | Violence & Harassment |  |  |  |
| 101 | PPE | Control tag |  |  |  |  | Use of protective methods |
| 102 | Permit To Work | Control tag |  |  |  | Work authorization |  |
| 103 | Barricade / Sign | Control tag |  |  |  |  | Protective systems |
| 104 | Safety Meeting, Induction & Checks | Control tag |  |  |  |  |  |
| 105 | Working Without Helper / Supervision | Control tag |  |  |  |  |  |
| 106 | Emergency Preparedness & First Aid | Control tag |  |  |  |  |  |
| 107 | Procedure & Competency | Control tag |  |  |  |  | Following procedures |

Notes:
- Natural Events / Weather maps to the new category Natural Event (Environmental). Extreme Weather / Lightning (Physical) overlaps with it; HSE decides whether to merge them (open item 33).
- Radiation & Vibration maps to Ionizing Radiation for now; vibration reports need the category changed at review (open item 33).
- Machinery & Energy Isolation maps to the existing category Machinery & Work Equipment; Marine & Working Over Water to Drowning / Work Over Water; Waste & Environmental Control to Waste Management; Hazardous Chemicals to Hazardous Substances; Manual Handling & Ergonomics to Manual Handling.
- Security & Intrusion and Third-Party Interference map to Unauthorized Access / Theft; Community & Civil Unrest to Community & Civil Disturbance; Violence & Threats from Outsiders to Violence & Harassment (workplace bullying also maps to Violence & Harassment).

**Old SOC sub-categories (legacy rows only).** The 15 sub-categories of the old SOC sheet are no longer shown. For the 242 migrated rows they map as follows:

| Old sub-category | New hazard topic | New control tag |
|---|---|---|
| APD / PPE | | PPE |
| Bekerja Di Ketinggian / Working at Height | Working at Height | |
| Bekerja Mengikuti Prosedure / Work According to Procedure | | Procedure & Competency |
| Bekerja Tidak Mengikuti Prosedur / Working Without Following Procedures | | Procedure & Competency |
| Bullying | Bullying | |
| Housekeeping | Housekeeping | |
| Ijin Kerja / Permit To Work | | Permit To Work |
| Jalur Bahaya / Line of Fire | Line of Fire | |
| Pekerjaan Listrik / Electrical Work | Electrical Work | |
| Pekerjaan Panas / Hot Work | Hot Work | |
| Pekerjaan Pengangkatan / Lifting Operation | Lifting Operation | |
| Pekerjaan Penggalian / Excavation Work | Excavation Work | |
| Perilaku Aman / Safe Behavior | | (none; positive observation) |
| Safety Sign / Barricade | | Barricade / Sign |
| Transportasi / Transportation | Transportation | |

Rows migrated this way have `topic_source` = Legacy and stay Pending until HSE reviews them.

**ref_iogp_activity_type (13):** Construction, commissioning, decommissioning; Diving (including decompression), subsea, ROV; Drilling, workover, well operations; Excavation, trenching, ground disturbance; Lifting, crane, rigging, deck operations; Maintenance, inspection, testing; Office, warehouse, accommodation, catering; Production operations; Seismic / survey operations; Transport – air; Transport – land; Transport – water, including marine activity; Unspecified – other

**ref_work_activity (25), each with its IOGP type**

| # | Work Activity | IOGP type | Active by default |
|---|---|---|---|
| 1 | Civil & Concrete Works | Construction, commissioning, decommissioning | Yes |
| 2 | Structural Steel Erection | Construction, commissioning, decommissioning | Yes |
| 3 | Piping & Mechanical Erection | Construction, commissioning, decommissioning | Yes |
| 4 | Electrical & Instrument Work | Construction, commissioning, decommissioning | Yes |
| 5 | Welding, Cutting & Grinding | Construction, commissioning, decommissioning | Yes |
| 6 | Scaffolding | Construction, commissioning, decommissioning | Yes |
| 7 | Painting, Blasting & Coating | Construction, commissioning, decommissioning | Yes |
| 8 | Hydrotest & Pressure Testing | Construction, commissioning, decommissioning | Yes |
| 9 | Pre-commissioning & Commissioning | Construction, commissioning, decommissioning | Yes |
| 10 | Heavy Equipment & Mobile Plant | Construction, commissioning, decommissioning | Yes |
| 11 | Excavation & Earthworks | Excavation, trenching, ground disturbance | Yes |
| 12 | Lifting Operation | Lifting, crane, rigging, deck operations | Yes |
| 13 | Maintenance, Inspection & Testing | Maintenance, inspection, testing | Yes |
| 14 | NDT / Radiography | Maintenance, inspection, testing | Yes |
| 15 | Transportation (Land) | Transport – land | Yes |
| 16 | Traffic & Pedestrian Movement | Transport – land | Yes |
| 17 | Marine / Jetty Operations | Transport – water, including marine activity | Yes |
| 18 | Material Handling & Storage | Office, warehouse, accommodation, catering | Yes |
| 19 | Office & Administrative Work | Office, warehouse, accommodation, catering | Yes |
| 20 | Camp, Catering & Welfare | Office, warehouse, accommodation, catering | Yes |
| 21 | Plant Operations (GPP) | Production operations | **No** until confirmed |
| 22 | Housekeeping & Waste Handling | Unspecified – other | Yes |
| 23 | Lone Working | Unspecified – other | Yes |
| 24 | Diving / Underwater Work | Diving, subsea, ROV | **No** until confirmed |
| 25 | Other | Unspecified – other | Yes |

**ref_life_saving_rule (10):** Bypassing safety controls; Confined space; Driving; Energy isolation; Hot work; Line of fire; Safe mechanical lifting; Work authorization; Work at height; Other. (Source: IOGP Life-Saving Rules, as listed in the 2025 user guide, section 3.13.)

**ref_root_cause (8):** with a `cause_type` column.
- People (acts): Following procedures; Use of tools, equipment, materials and products; Use of protective methods; Inattention / lack of awareness
- Process (conditions): Protective systems; Tools, equipment, materials, products; Work place hazards; Organizational

(Source: IOGP causal factors, section 3.12, at group level only.)

**ref_severity (5)**: id 1 to 5 with name and descriptors per category, from the project JSA form:

| Level | Name | Personnel | Environment (liquid spill) | Asset downtime | Reputation | Quality |
|---|---|---|---|---|---|---|
| 1 | Insignificant | FAC (first aid case) | < 1 gallon | < $25K, < 12 hrs | Slight impact to local media | 5% rework |
| 2 | Minor | MTC (medical treatment case) | < 10 gallons | $25K to < $100K, < 1 day | Limited impact to local media | 10% rework |
| 3 | Moderate | RWC (restricted work case) | < 100 gallons | $100K to < $1M, < 2 days | Considerable impact to regional media | 15% rework |
| 4 | Major | LTI (lost time injury) | < 1,000 gallons | $1M to < $10M, < 1 week | Major impact to national media | 20% rework |
| 5 | Severe | FAT (fatality) | > 1,000 gallons | > $10M, > 1 week | Severe impact to international media | 25% rework |

The JSA form also gives gas-leakage rates and security/personnel descriptors per level. Store the full text from the form in a `description` column during Phase 1 (copy it from the original form to avoid errors).

**ref_likelihood (5)**

| Level | Name | Probability |
|---|---|---|
| 1 | Remote | 0 to 20% (might occur only in exceptional circumstance, never occurred in the industry within 8 to 10 years) |
| 2 | Unlikely | 20 to 40% (could occur or has occurred in the industry within 5 to 7 years) |
| 3 | Possible | 40 to 60% (might occur at some time, occurred in the industry within 3 to 5 years) |
| 4 | Likely | 60 to 80% (occurs in many circumstances, occurred several times within 1 to 3 years) |
| 5 | Almost Certain | 80 to 100% (expected to occur in most circumstances, occurred several times within a 12 month period) |

**ref_risk_level (4)**

| Level | Colour | Required response (from the JSA form) | Closure days (PLACEHOLDER) |
|---|---|---|---|
| Low | Green | Acceptable | 30 |
| Medium | Yellow | Tolerable if reasonable safeguards and management systems are confirmed to be in place (ALARP) | 14 |
| High | Orange | Additional long-term risk reduction required; if no further action can reasonably be taken, document it | 3 |
| Very High | Red | Short-term interim risk reduction required, with a long-term reduction plan | 1 |

**ref_risk_matrix (25 rows):** severity_id, likelihood_id, risk_level_id.

| Severity ↓ / Likelihood → | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|
| 5 Severe | Medium | Medium | High | Very High | Very High |
| 4 Major | Low | Medium | High | High | Very High |
| 3 Moderate | Low | Low | Medium | High | High |
| 2 Minor | Low | Low | Low | Medium | Medium |
| 1 Insignificant | Low | Low | Low | Low | Medium |

Check this grid against the original JSA form before loading it. The level cannot be computed as severity times likelihood, so all 25 cells are stored.

### 5.7 Views

Naming: `v_` for site views, `v_mgmt_` for management views.

**Base view `v_findings`:** one row per finding, joined with every lookup name, the derived Hazard Class, the derived risk level, and these calculated fields:

| Calculated field | Definition |
|---|---|
| is_followup_required | The observation type needs follow-up (not Safe Action, not Safe Condition) |
| is_overdue | Follow-up required, status is not CLOSED, and due_date is before today |
| age_days | Today minus finding_date for open items; closed_date minus finding_date for closed items |
| days_overdue | Today minus due_date when overdue |
| awaiting_verification | Status INPROGRESS, action_completed_date filled, verified_date empty |
| is_unclassified | review_status is Pending |
| aging_bucket | 0 to 7, 8 to 14, 15 to 30, over 30 days |

**Site views (names, free text, and photos allowed)**
1. `v_site_overdue`: overdue list ordered by risk level then days overdue
2. `v_site_critical_open`: High and Very High items not closed
3. `v_site_awaiting_verification`
4. `v_site_due_next_7_days`
5. `v_site_near_miss_month`
6. `v_site_log`: full log with filters
7. `v_site_aging`
8. `v_site_review_queue`: Pending findings, oldest first, with the suggested classification

**Management views (no reporter names, no free text, no photos)**
1. `v_mgmt_status_all`: total, open, in progress, closed, all sources
2. `v_mgmt_status_by_source`: same by source (SOC and PMR panels)
3. `v_mgmt_breakdown_hazard`, `_area`, `_company`: counts by source (hazard and activity breakdowns count Reviewed findings; Pending ones appear as "Unclassified" so totals still add up)
4. `v_mgmt_positive_findings`: Safe Action and Safe Condition counts by source
5. `v_mgmt_monthly_trend`
6. `v_mgmt_kpi`: the KPIs in section 9.3
7. `v_mgmt_risk_profile`: counts per risk level and per severity
8. `v_mgmt_company_scorecard`
9. `v_mgmt_heatmap_area_hazard`
10. `v_mgmt_reporter_designation`: counts by designation only, no names

### 5.8 Database users and privileges

| User | Used by | Privileges |
|---|---|---|
| `hse_admin` | You, for migrations only | Full on the database |
| `hse_app` | The web application | Read and write on tables; no structure changes |
| `hse_site_ro` | Site dashboard | Read on `v_site_*` and `v_findings` only |
| `hse_mgmt_ro` | Management dashboard | Read on `v_mgmt_*` only; no access to base tables |

Allow connections only from the application server address. Store passwords only in `.env`.

### 5.9 Indexes
Single indexes on: finding_date, source_id, status_id, company_id, area_id, hazard_category_id, work_activity_id, due_date, repeat_of_id. Composite indexes on: (source_id, status_id), (company_id, status_id), (status_id, due_date). Re-check with real data volume after Phase 5.

### 5.10 Versioning and backup
- Apply migrations in order using the numbered files. Keep a `schema_version` record of the last applied file.
- Daily full backup of the production database, kept for at least 30 days, with a restore test once a month.
- Back up the evidence folder with the same schedule.

### 5.11 Phase 1 tests (in `database/tests`)

1. All 25 matrix cells return the expected level (spot-check every Very High and every Medium cell).
2. Each rule in section 5.5 has one test that must be rejected and one that must be accepted.
3. Deleting a lookup row that is in use is blocked.
4. Loading the 10 sample rows (Appendix A) produces the expected dashboard numbers.
5. The management user cannot read any base table or any `v_site_*` view.
6. The application user cannot alter or drop tables.

### Definition of Done: Phase 1
All migrations apply cleanly on an empty database, seed data loaded, counts match (7 classes, 52 categories, 47 SOC topics and control tags, 1,276 keywords, 25 activities, 10 life-saving rules including Other, 8 root-cause groups, 25 matrix rows), all six tests pass, backups run.

---

## 6. Phase 2: Data migration

**6.1 Sources**

SOC Google Form response sheet. Header, in the order of the sheet (11 columns plus Timestamp):
Timestamp; Tuliskan Nama Anda / Please write your name; Silahkan masukkan tanggal temuan anda / Please enter the date when the observation was identified; Silahkan Pilih Perusahaan Tempat Anda Bekerja / Please Select the Company Where You Work; Pilih kategori berikut ini / Select the following categories; Silahkan Pilih Sub-Kategori Temuan Anda / Please Select a Sub-Category for Your Findings; Jelaskan temuan anda / Describe your findings; Tindakan/ tindak lanjut yang dilakukan / Actions/follow-up taken; Rekomendasi / Recommendation (Jika Ada / if any); Dokumentasi / Documentation (Jika Ada / If Any); Pilih Area Observasi / Observation Area; Status Temuan / Finding Status.

Notes on the sheet:
- Area appears last in the sheet, unlike the form. Match columns by their header text, never by position.
- The sheet has no email column. If the real sheet has one, add it to the mapping.
- Excel dashboard (columns NO, Date, report by, HSE Findings, rekomendasi, Area, SUBCONTRACTOR, AUDIT, MWT, INSPECTIONS, SOC, PMR, evidence, STATUS).

**6.2 Method**

1. Export the sheet or Excel file to CSV.
2. Load it unchanged into a staging table (`stg_soc_google`, `stg_excel_dashboard`), with the batch number and row number. The staging copy is kept as proof.
3. Convert staging rows to findings through the mapping tables below.
4. Produce the check report (6.8). Only then insert into `hse_findings`.
5. Run on the test database as often as needed. Do the final run on cutover day.

**6.3 SOC sheet to database**

| SOC sheet column | Database column | Conversion |
|---|---|---|
| Timestamp | form_timestamp, and part of legacy_key | Direct copy |
| Tuliskan Nama Anda | reporter_name | reporter_type = Legacy (typed name, not an employee ID) |
| Silahkan masukkan tanggal temuan | finding_date | Check the date format of the sheet first (day/month order) |
| Silahkan Pilih Perusahaan | company_id | Match to the company list; typed "Other" values go to the review report |
| Pilih kategori | observation_type_id | See 6.4 |
| Silahkan Pilih Sub-Kategori | soc_topic_id, then suggested hazard, work activity, life-saving rule, root cause | Through ref_soc_topic (section 5.6) |
| Jelaskan temuan anda | finding_description | Direct copy |
| Tindakan / tindak lanjut | action_taken | Direct copy |
| Rekomendasi | recommendation | Direct copy |
| Dokumentasi | evidence_before | See 6.7 |
| Pilih Area Observasi | area_id | Match to the area list; typed "Other" values go to the review report |
| Status Temuan | status_id | OPEN or CLOSED, direct |
| (not in sheet) | source_id | SOC for every row |
| (not in sheet) | data_origin, review_status, legacy_key | Google Form, Pending, hash of timestamp + name + date |

**6.4 Category mapping**

| Sheet value | observation_type_id |
|---|---|
| Tindakan Aman / Safe Action | Safe Action |
| Kondisi Aman / Safe Condition | Safe Condition |
| Tindakan Tidak Aman / Unsafe Action | Unsafe Action |
| Kondisi Tidak Aman / Unsafe Condition | Unsafe Condition |
| Saran / Suggestion | Suggestion |

**6.4a Value mappings found in the real SOC file (242 responses)**

File facts: 242 responses from 27 Jan to 6 Oct 2026 (none in June). The sheet shows 343 rows but the rest are empty; ignore the empty extra column "Column 12". Dates are real date values, so import the Excel file directly. 43 rows have a photo link (all Google Drive). 57 different reporter names; one reporter has 109 entries.

Company:

| Typed value | Rows | Maps to |
|---|---|---|
| SALAWATI | 129 | SALAWATI MOTORINDO |
| STARWAY | 23 | STARWAY ENGINEERING INDONESIA |
| LNG | 2 | LAYAR NUSANTARA GAS (assumption 1) |
| Full names already matching the list | 88 | As is |

Area:

| Typed value | Rows | Maps to |
|---|---|---|
| PCC | 68 | PCC / TCF |
| Tcf, TCF Generator | 3 | PCC / TCF |
| Camp salawati | 1 | Camp |
| Pos 1, Jalan utama (all spellings), Road #2, Main Road, Jalan Rusak, Nagote tujuan PCC | 12 | Main / Access Road |
| Parkiran LV | 2 | Parking Area |
| Workshop Salawati | 2 | Workshop |
| salesgas | 1 | HSE review |
| An entry that is not an area ("kendala seluruh karyawan salawati") | 1 | HSE review |
| Blank | 3 | Not specified |
| Values already matching the list | 147 | As is |

Typed sub-categories (29 rows):

| Typed value | Rows | Topic | Provisional hazard |
|---|---|---|---|
| Bahaya Debu, Dust Spray, Kabut Asap, Pembakaran sampah | 4 | Dust, Smoke & Air Quality | Air Emission & Dust |
| Akomodasi, disediakan shelter temporary | 2 | Welfare - Accommodation & Laundry | Welfare - Accommodation |
| Air yang tidak memadai, Air Gelon tanpa tutup | 2 | Welfare - Sanitation & Drinking Water | Infection / Contaminated Food or Water |
| Tidur / menginap di Office | 1 | Fatigue / Rest | Fatigue & Working Hours |
| Hewan Liar, Bahaya gigitan ular, biological hazard (agas) | 3 | Wildlife & Pests | Animals, Insects & Plants |
| Parkiran LV, Kondisi tempat parkir | 2 | Transportation | Vehicles & Mobile Equipment |
| Bahaya longsor | 2 | Landslide | Landslide / Slope Instability |
| Kondisi lampu tidak memadai, Cahaya lampu | 2 | Lighting | Poor Lighting |
| Kebisingan di Area shelter | 1 | Noise | Noise |
| Sudut Tajam | 1 | Sharp Edges | Cuts & Sharp Edges |
| tabung bertekanan tinggi | 1 | Pressurized | Pressurized / Vacuum Systems |
| Spill Potential | 1 | Spill | Spill / Leakage |
| Perilaku tidak aman (2), Unsafe Action, Kondisi tidak aman | 4 | None | None, HSE reviews |
| TBM, Inspection, LEP | 3 | None | Control tag Safety Meeting, Induction & Checks; no hazard |

The provisional hazards stay provisional: the rows remain Pending until HSE confirms them.

Status and other rules:
- Positive observations (Safe Action, Safe Condition) that are OPEN (9 rows) or blank (2 rows) are set to CLOSED, and the original value is kept in the staging table (assumption 2).
- Suggestions and unsafe findings keep their status as entered.
- One finding date, 1995-11-09 (POWERCHINA, submitted 2026-10-01), is a typing error. Use the submission date and log the change.
- Reporter names stay as typed, with reporter_type Legacy. Several people appear under more than one spelling (for example 5 spellings of one name) and 14 names are a single word, so automatic matching to the employee data is not reliable. HSE can link a legacy name to an employee record during review, and all rows of that name are linked together.

**6.5 Excel dashboard to database**

| Excel column | Database column |
|---|---|
| NO | Not migrated (new id) |
| Date | finding_date |
| report by | reporter_name (reporter_type Legacy) |
| HSE Findings | finding_description |
| rekomendasi | recommendation |
| Area | area_id |
| SUBCONTRACTOR | company_id |
| AUDIT / MWT / INSPECTIONS / SOC / PMR | source_id, and audit_type_id for Audit (the filled column gives the source) |
| Hazard Categories | hazard_category_id through a mapping table (old Environment and Security map to the new categories) |
| evidence | evidence_before |
| STATUS (1. OPEN, 2. INPROGRESS, 3. CLOSED) | status_id |

**6.6 Rules for legacy rows**

- All legacy rows start with review_status Pending, data_origin Google Form or Excel, and reporter_type Legacy.
- The old data has no hazard category. Use the suggestion from the SOC topic where the topic gives one; otherwise leave the hazard **empty** (shown as "Unclassified"). Do not fill "Other" and do not guess from the description text.
- No severity, likelihood, or due date. Legacy rows are shown as "legacy, not rated" and are not counted as overdue.
- CLOSED legacy rows keep CLOSED, with verification_remarks "Migrated from legacy data; closure not verified in this system".
- Positive observations are always CLOSED after migration (see 6.4a). Unsafe findings marked CLOSED in the old data (68 rows) stay CLOSED without verification.
- HSE reviews legacy rows through the same review queue as new SOC submissions. Suggested order: open and unsafe rows from the last 90 days first. Older closed rows can stay Unclassified or be reclassified in bulk by topic.

**6.7 Photos**

The Dokumentasi column holds Google Drive links, which break when files are moved, deleted, or the sharing permission changes. Download each photo once during migration into the evidence folder and store the new path in evidence_before. Keep the original link in the staging table. Someone with Drive access must run this step. Links that cannot be fetched go in the check report.

**6.8 Check report (reconciliation)**

- Row counts by status, by observation type, and by company: old data versus new database must match. For the current file the expected result is 242 findings: Safe Action 98, Safe Condition 10, Unsafe Condition 83, Unsafe Action 30, Suggestion 21. Status after conversion: CLOSED 185 (all 108 positive + 68 unsafe + 9 suggestions) and OPEN 57 (45 unsafe + 12 suggestions).
- List of typed "Other" values for company, area, and topic, with the proposed match.
- Rows rejected, with the reason (for example an unreadable date).
- A sample of 30 rows checked by HSE against the original.

**6.9 Running both systems and the final import**

- Re-running an import is safe: legacy_key skips rows that already exist.
- No two-way sync. Choose one cutover date. After it, only the new system is edited, and the Google Form is closed and kept read-only as an archive.
- If HSE keeps editing statuses in the Google Sheet before cutover, take the final import on cutover day so the latest statuses come in.

### Definition of Done: Phase 2
The check report shows equal counts; every typed "Other" value is resolved or listed; photos are copied; HSE signs off on the 30-row sample; a repeated import creates no duplicates.
---

## 7. Phase 3: Backend API

### 7.1 Roles and access

| Role | Can do |
|---|---|
| Employee (SOC reporter) | No account. Signs in with name and date of birth for a short session; uses the SOC form; sees their own submissions (reference number, date, topic, and status only) |
| HSE Reporter | Use the full form for Audit, MWT, Inspections, PMR |
| Action Owner (subcontractor) | See findings assigned to their company; update action taken; mark action completed with after-photo |
| HSE Officer | See all; review pending SOC submissions (confirm or change classification, set risk, owner, due date); verify or reject closure; edit due date with a reason |
| HSE Admin | Everything above, plus reopen CLOSED findings, manage lookup lists, topics, and users |
| Management Viewer | Management dashboard only |

**Sign-in for the SOC form (decided)**

Employees have no accounts. The sign-in has three steps in this order:

1. **Cloudflare Turnstile first.** The employee ticks the box. The server verifies the token with Cloudflare. Until this passes, the form shows nothing else and the name search is closed.
2. **Name: searchable dropdown.** A short search permission opens (placeholder: 10 minutes). The employee types at least 3 letters and the server returns up to 5 matching names. The browser never receives the full list of employees and never receives employee IDs: each entry carries only a random one-time reference. If two people share a name, the company is shown next to each so they can be told apart.
3. **Date of birth (required).** The employee picks their name, enters their date of birth, and sends. The server passes the reference and the date of birth to the Medpost API to verify.

On a match, a short session opens (placeholder: 30 minutes). The form opens with the name filled in. The verified name from the employee data is saved as the reporter name, together with the employee ID, designation, and company. On no match, show one generic message such as "Details not found, please check and try again". Never say which part was wrong.

What each part does:
- Turnstile stops automated bots. It does **not** prove who the person is. It also keeps bots away from the name search.
- The dropdown removes typing errors and spelling variants, so the match is exact.
- The date of birth is the only secret. It is weak: colleagues often know each other's birthdays, and a birthday has few possible values. Without limits, someone who picks a name could guess the date of birth. So the safeguards below are required.

Safeguards:
- Serve names only after Turnstile has passed, and only through server-side search with a minimum number of letters and a maximum number of results. Do not send the whole list to the browser. This limits how much of the workforce list a visitor can collect.
- The search result contains only the name, a random reference, and, for duplicate names, the company. No employee ID, designation, or date of birth.
- Limit failed verifications per employee record and per device (placeholder: 5 failures in 15 minutes, then a 15-minute lock). Limit the number of name searches per device (placeholder: 30 in 10 minutes). Do not reveal which limit was hit.
- Never store the date of birth in the findings database, in logs, or in any answer sent back to the browser. Hide it in error logs.
- The "My submissions" page shows reference number, date, topic, status, and the visitor's own photo thumbnails (changed on request; the plan first excluded photos). It does not show descriptions. A photo is served only if the database says it belongs to a post of the signed-in employee, never by id alone. The remaining risk: anyone who knows a name and birthday can see that person's photo thumbnails, which is why descriptions stay hidden and why the fourth sign-in step (badge number) stays an option.
- HSE review catches impersonated or false reports before they affect the dashboard figures for hazards and risk.
- Stronger option, if you want it later: add the employee or badge number as a fourth step. It is still quick for the employee and makes impersonation much harder.

HSE staff, action owners, and management still need real accounts with passwords. Keep both sign-in methods behind one module so either can be replaced.

### 7.2 Employee search, verification, and lookup (Medpost API)

Three functions are needed from the Medpost API:

| Function | Used by | Input | Answer |
|---|---|---|---|
| Search names | SOC sign-in, step 2 | Letters typed (minimum 3), maximum 5 results | Name and a random reference for each active employee; the company only when two names are identical. Nothing else |
| Verify | SOC sign-in, step 3 | The reference and the date of birth | On a match: employee ID, name, designation, company, active status. On no match: "not found". The date of birth is never returned |
| Look up by ID | HSE staff on the full form (reporter, action owner, verifier) | Employee ID | The same five fields |

Rules:
- The backend calls the API; the browser never calls it directly. The comparison of the date of birth happens inside the API, not in this application.
- Use only the fields listed. Discard anything else immediately and never log the response.
- The random reference is single use and expires with the search permission.
- Search only returns active employees.
- If the API fails or times out: the SOC form still accepts the submission with a typed name, marks reporter_type External and "unverified", and HSE review can match it later. A safety report must not be blocked by an outage.
- Do not store the national ID (NIK), the date of birth, or any medical information.

### 7.3 Endpoints

| Method and path | Purpose | Roles |
|---|---|---|
| GET /api/lookups/{name} | Return an active lookup list | All logged in |
| POST /api/auth/soc/turnstile | Verify the Turnstile token; opens the short search permission | Public, rate limited |
| GET /api/auth/soc/names?q= | Search names (minimum 3 letters, maximum 5 results; names and random references only) | Search permission, rate limited |
| POST /api/auth/soc | Verify the chosen reference and date of birth; opens a short session | Search permission, rate limited |
| GET /api/employees/{id} | Proxy to the Medpost API, five fields only | HSE roles |
| GET /api/risk?severity=&likelihood= | Return risk level, colour, required response, closure days | All logged in |
| POST /api/findings | Create a finding | Reporter and above |
| GET /api/soc-topics | Topic list with suggested classification | Employee and above |
| GET /api/admin/keywords, POST /api/admin/keywords, PATCH /api/admin/keywords/{id} | Keyword list for HSE Admin; every POST and PATCH writes a row to `ref_keyword_log` in the same transaction | HSE Admin |
| GET /api/admin/keywords/export | Current keyword list (topic, hazard, match type, active flag, caught and corrected counts) as CSV, for HSE review or sharing | HSE Admin |
| GET /api/admin/keywords/{id}/log | Change history of one keyword | HSE Admin |
| POST /api/soc | Create a finding from the SOC form (short payload; the API fills reporter details, source = SOC, review_status = Pending, and the topic's suggested values) | Employee and above |
| GET /api/soc/mine | The employee's own submissions and their status | Employee |
| GET /api/review/queue | Pending findings, oldest first | HSE Officer |
| POST /api/findings/{id}/review | Confirm or change classification, set risk, owner, due date; sets Reviewed | HSE Officer |
| GET /api/findings | List with filters (date range, source, company, area, hazard, status, overdue, owner) and paging | By role |
| GET /api/findings/{id} | Detail with history | By role |
| PATCH /api/findings/{id} | Edit allowed fields | HSE Officer and above |
| POST /api/findings/{id}/complete | Action owner marks the action done (date, after-photo) | Action Owner |
| POST /api/findings/{id}/verify | HSE verifies (closes) or rejects (remarks required) | HSE Officer |
| POST /api/findings/{id}/reopen | Reopen a CLOSED finding (reason required) | HSE Admin |
| POST /api/evidence | Upload a photo | Reporter and above |
| GET /api/dashboard/site/{panel} | Site panels | HSE roles |
| GET /api/dashboard/mgmt/{panel} | Management panels | Management Viewer and HSE |
| GET /api/export/{csv|pdf} | Exports | By role |

### 7.4 Business rules (implemented in the API as well as in the database)

1. On create, if the observation type is positive: status = CLOSED, no risk fields, no due date.
2. On create through the full HSE form, if it needs risk: severity and likelihood are required; the API looks up the risk level and sets due_date = finding_date + closure days; status = OPEN. For SOC employee entries, risk and due date are set at review (rule 13).
3. A finding becomes INPROGRESS when an action owner is assigned or action text is added.
4. Marking the action complete sets action_completed_date and requires an after-photo; status stays INPROGRESS.
5. Verification by an HSE Officer sets verified_by, verified_date, closed_date, and status CLOSED. The verifier cannot be the same person as the reporter or the action owner.
6. Rejection keeps status INPROGRESS, clears action_completed_date, and stores the remarks.
7. Reopening sets status OPEN, clears verification fields, requires a reason, and is written to the audit log.
8. Changing severity, likelihood, or due date after creation requires HSE Officer, a reason, and an audit-log entry. Changing severity or likelihood does not change an existing due date unless the HSE Officer chooses to.
9. Photos: images only, maximum 10 MB, check the file type from the content (not only the extension), rename on save, reduce size on upload.
10. Every response to the management dashboard is built only from `v_mgmt_` views.
11. SOC submissions: source = SOC, review_status = Pending, reporter details from the API, and the topic and hazard filled by the keyword rules (8.2a) as provisional.
12. If the employee picks Status = CLOSED on the SOC form (for an unsafe category), set action_completed_date to the submission date and set status INPROGRESS (awaiting verification). It never goes straight to CLOSED. HSE verification then needs an after-photo or a verification remark. If the employee picks OPEN, status stays OPEN.
13. Review step: the HSE Officer must set the hazard category, and for Unsafe Action, Unsafe Condition, and Near-Miss also severity and likelihood. The API sets due_date = review date + closure days, requires an action owner, and saves review_status Reviewed with reviewer and date, and writes the audit log.
14. Findings that need follow-up cannot be verified or closed while Pending. Positive observations are closed on entry and wait only for classification.
15. Review time limit: PLACEHOLDER 2 working days. The dashboard flags Pending findings older than the limit.

### 7.5 Error handling and logging
- Return clear messages in the field's language (see 8.4).
- Log who did what, never the content of employee API responses or passwords.

### Definition of Done: Phase 3
Every endpoint has automated tests for success and for each rejection; the 12 rules in 5.5 and the 10 rules in 7.4 each have a test; the API fails safely when the Medpost API is down.

---

## 8. Phase 4: Web form and follow-up screens

### 8.0 Look, navigation, and pages (same as hse-medpost)

hse-register looks and behaves like hse-medpost, so staff who use one already know the other. Reference files in hse-medpost: `web/templates/login.html`, `start.html`, `settings.html`, `web/static/clearwave/` (theme), `login.js`, `page-shell.js`.

**Look (copied from medpost)**
- The same "clearwave" theme: `templatemo-622-clearwave.css` and `app.css` copied into this project, pale teal page background (`#E8F4F2`), square corners, light shadows, DM Sans for text and Playfair Display for headings.
- Fonts are copied into the program's static files (medpost loads them from Google Fonts; here the site network should reach only Cloudflare, section 8.1).
- A slim top bar with the logo "hse register" at the left (same style as "hse medpost"). On signed-in pages the right side shows the user's name and a **Log out** button. On the sign-in pages there is only the logo.
- Cards are centred in a `login-card`; the home page uses the wider card with the module cards.
- Page changes use the same short view transition (120 ms, off for reduced motion). The footer shows the developer credit and the same internal-use disclaimer, reworded for HSE records.
- Works on a phone first (the SOC pages are used on phones), then desktop.

**Behaviour (copied from medpost)**
- Sign-in sets a signed cookie session of 12 hours (medpost uses 12 hours). **Log out** clears it and returns to the login page.
- After sign-in the user lands on the home page. Clicking the logo on any signed-in page returns to the home page; the user stays signed in, with no second sign-in and no module switcher in the top bar.
- Pages that need a sign-in redirect to the login page when the session is missing or expired. The Settings pages need the HSE Admin role; anyone else gets "not allowed", not a blank page.
- The home page greets the user ("Welcome, <name>") and shows the module cards.

**A. Registered users (HSE staff, action owners, management, admins): private listener (Tailscale)**

1. **Login page.** Fields: Username and Password. Like medpost, the user can either pick their username from a list or type it (the list is only served on the private listener). A wrong username or password gives one generic message. After 5 failed attempts for a username in 15 minutes the account is locked for 15 minutes (placeholder, same as the SOC limits). Passwords are stored as bcrypt hashes. Unlike medpost they are not limited to 6 digits.
2. **Home page.** Module cards in this order:

| Card | Opens | Who sees it | Notes |
|---|---|---|---|
| Medical Activities | Medpost activity log, **view only** | All registered users | Programs and activities with their evidence, read from hse-medpost (open item 34). No add, edit, or delete here |
| Work Permit Register | The existing work permit register (list, filter, add, edit) | All registered users; what each can change follows the role | The register that already exists in this project |
| HSE Observation | Findings list, full HSE form, review queue, follow-up, dashboards (sections 8.3, 8.5, 9) | All registered users; what each sees follows the role in 7.1 (for example a Management Viewer sees only the management dashboard; an Action Owner sees only their company's findings) | The main module |
| Settings | Users, lookup lists, topics, keywords | **HSE Admin only** (card not shown to others) | Styled as the medpost Settings card |

3. **Settings** (admin): Users (add, switch off, reset password, set role), lookup lists (companies, areas, hazard categories, work activities, and so on), SOC topics and keywords (section 8.2a), exports.

**B. Employees (SOC): public listener (Cloudflare Tunnel)**

Same look, but no home page, no module cards and no Log out button in the top bar (a "Sign out" link on the form page ends the 30-minute session). Order of pages:

1. **Check page.** Cloudflare check first: a Turnstile tick-box, widget mode Managed (decided). Nothing else is shown or reachable until it passes.
2. **Name.** Searchable dropdown; it shows names only after 3 typed letters, up to 5 results.
3. **Date of birth.** Required, date picker.
4. **Sign in button.** The program verifies name and date of birth with the Medpost API (section 7.2). One generic message on any failure.
5. **Observation form.** The 10 SOC fields (8.2), name filled in and read-only.
6. **Thank-you page.** Reference number, the topic and hazard the system picked (read-only), a note that HSE will check it, and two links: "Submit another" and "My submissions".

Steps 1 to 4 are one page with the fields appearing in that order, as the sign-in screen in 8.2 describes.

**Routes (page level)**

| Route | Listener | Page |
|---|---|---|
| GET / | Private | Login |
| GET /start | Private, signed in | Home page |
| GET /activities | Private, signed in | Medical Activities (view only) |
| GET /workpermitregister | Private, signed in | Work Permit Register (existing screens, unchanged) |
| GET /observations | Private, signed in | HSE Observation |
| GET /admin/settings | Private, HSE Admin | Settings |
| POST /api/login, POST /api/logout | Private | Sign in, sign out |
| GET /soc | Public | Check, name, date of birth |
| GET /soc/form, GET /soc/thanks, GET /soc/mine | Public, SOC session | Observation form, thank-you page, my submissions |

The two listeners keep the rule of section 8.1b: the public listener has only the `/soc` pages and their API calls.

### 8.1 Recommended stack (change if your team prefers another)
- Backend: Go (current stable) with the standard library HTTP server, a small router, and the standard `database/sql` package with the MariaDB/MySQL driver. One program, built for Linux ARM64.
- Frontend: pages made on the server from Go HTML templates, mobile-friendly, with a light script layer. The look is the hse-medpost theme (section 8.0). Charts with Chart.js and the theme fonts are served from the program itself (no internet CDN), so the site network does not need to reach other sites except Cloudflare for Turnstile.
- Why this suits the board: one file to copy, low memory use, no separate runtime to install and update.

### 8.1a Deployment on the Orange Pi 3B

| Item | Plan |
|---|---|
| Machine | Orange Pi 3B, Linux ARM64. Planning figure: 2 GB RAM (open item 25) |
| Database | MariaDB installed natively on the same board, listening on the local address only |
| Application | The Go program runs as a system service that starts at boot and restarts after a crash |
| Load | About 100 reports a month, plus dashboard views. One small program can handle this easily |
| Storage now | eMMC holds the system, the database, and the photos |
| Storage later | NVMe (M.2). Move the database folder and the photo folder to the NVMe in a planned stop: stop the program, stop MariaDB, copy, change the two paths in the settings, start, run the Phase 1 tests. Keep the old copy until the tests pass |

Memory budget for 2 GB (placeholders to measure on the real board):
- System: about 300 to 400 MB.
- MariaDB: keep it small, for example a buffer pool of 256 MB, 30 connections at most, no query cache, performance schema off. Set these in a separate settings file for MariaDB.
- Go program: about 100 to 200 MB while working, with a limit on how many photo uploads are processed at the same time (2).
- Do not run a swap file on the eMMC (it wears the chip). A compressed memory swap (zram) is acceptable.

Protecting the storage and the data:
- Rotate and limit logs. Keep the database's binary log short (a few days) and use it only for recovery.
- A nightly backup of the database and the photos to another machine, not only to the board. Test a restore every quarter.
- A power cut can corrupt data on the board. Use a small UPS if the site power is unstable, and keep InnoDB at its safe default settings.
- Photos: images only, 10 MB maximum, resized to a maximum width on upload, one at a time, stored on disk with the database holding only the file name.
- HTTPS is required because the sign-in uses a date of birth. For employees, Cloudflare provides it (section 8.1b). For HSE staff, Tailscale encrypts the connection (see 8.1b for the certificate choice).

### 8.1b Network access: Cloudflare Tunnel and Tailscale

| Who | Path | What they can open |
|---|---|---|
| Employees (phones, any network) | A public web address on your Cloudflare domain, served by `cloudflared` running on the board | The SOC pages only: Turnstile check, name search, verification, the form, and "My submissions" |
| HSE staff, reviewers, action owners, admins | Tailscale (private network); the board has a Tailscale address | Everything: full HSE form, review queue, follow-up screens, both dashboards, exports, keyword screen |
| Database administrator | Tailscale | Remote shell and MariaDB tools. No port is opened on the site router |

Design rules:
1. **Two listeners, not one.** The program opens a public listener on the board's local address only (the tunnel points to it) and a private listener on the Tailscale address. The public listener contains only the SOC routes in section 7.3. The other routes do not exist there, so they cannot be reached even by mistake or a header trick.
2. **MariaDB listens locally only.** Nothing on the internet and nothing on Tailscale reaches it directly.
3. **No inbound port forwarding.** `cloudflared` makes an outbound connection; Tailscale makes its own. SSH is allowed only over Tailscale.
4. **Visitor address.** Behind the tunnel, every request appears to come from the board itself. The public listener must read the visitor address from the header Cloudflare adds (`Cf-Connecting-Ip`) and trust it only on that listener, because it is the base of the per-device limits (5 failed verifications, 30 searches). The private listener must ignore that header.
5. **Cloudflare settings (dashboard):** the Turnstile site key tied to the public hostname; Cloudflare's own rate-limiting rule on the SOC sign-in path as a second layer; and bot or country rules if wanted. Check the current limits of your Cloudflare plan.
6. **Management / client viewers:** assumed to be given Tailscale access (an invited user, or a shared device) with the Management Viewer role. If clients cannot install Tailscale, the alternative is a second protected hostname on Cloudflare with a login in front (Cloudflare Access); this is open item 30.
7. **HTTPS for HSE staff:** the Tailscale connection is already encrypted. For a normal address with no browser warning, use a Tailscale certificate for the board's Tailscale name, or serve the private listener through the same tunnel with Cloudflare Access. Choose one in open item 30.
8. **If the internet is down:** employees cannot open the SOC form, because Turnstile needs Cloudflare. HSE staff on the site network can still use the private side only if Tailscale or the local network works. Keep a paper or HSE-entered fallback for outages.
9. **Medpost API:** the Go program calls it from the board. Confirm that the board can reach it (site network or Tailscale) and that the address is never visible to the employee's browser.
10. **Memory:** `cloudflared` and Tailscale also run on the 2 GB board. Measure all four (MariaDB, Go program, `cloudflared`, Tailscale) together under test 47 (section 11).
11. **Updates:** keep `cloudflared`, Tailscale, MariaDB and the system updated on a schedule, and record the schedule in the runbook.

### 8.2 Face 1: SOC form for all employees

The SOC form stays as simple as the current SOC table: the same wording, and one field fewer (10 instead of 11). The sub-category field is removed because the system picks the topic and hazard from the description. The only change is a short sign-in step before the form (Cloudflare tick-box, name from a searchable dropdown, and the date of birth), so the reporter's details come from the Medpost API. Target: under two minutes on a phone.

| # | Field (as in the current SOC table) | Behaviour in the new form |
|---|---|---|
| 1 | Tuliskan Nama Anda / Please write your name | Chosen from the searchable dropdown at the sign-in step (after the Cloudflare check), together with the date of birth. After a match the name is filled in, read-only, from the employee data and saved as the reporter name (employee ID, designation, and company are saved with it). If the Medpost API is down, the name is typed and marked unverified |
| 2 | Silahkan masukkan tanggal temuan anda / Please enter the date | Default today, no future dates |
| 3 | Silahkan Pilih Perusahaan / Please Select the Company | Pre-filled from the employee data, can be changed; same list as now |
| 4 | Pilih kategori / Select the following categories | The five current choices: Safe Action, Safe Condition, Unsafe Action, Unsafe Condition, Suggestion |
| 5 | (removed) Sub-Category | Not asked. The system picks the topic and hazard from the description (field 6) with the keyword rules in 8.2a. Field numbers below are unchanged from the current table for easy comparison |
| 6 | Jelaskan temuan anda / Describe your findings | Text |
| 7 | Tindakan / tindak lanjut yang dilakukan / Actions/follow-up taken | Text |
| 8 | Rekomendasi / Recommendation (if any) | Text, optional |
| 9 | Dokumentasi / Documentation (if any) | Up to 4 photos, optional, camera allowed. Each is compressed in the browser and again on the server to 500 KB at most; location data (EXIF) is removed. The "after" photos that document a finished action follow the same rules (up to 4, 500 KB each) |
| 10 | Pilih Area Observasi / Observation Area | Same list as now, plus Main / Access Road, Parking Area, and Workshop |
| 11 | Status Temuan / Finding Status | OPEN or CLOSED, as now. See the note below |

Sign-in screen (before the form), in this order: the Cloudflare Turnstile tick-box; then the name field, a dropdown that searches as the person types (shows nothing until at least 3 letters are typed); then the date of birth (required, date picker so the format cannot be mistyped); then one button. Bilingual labels. One generic message for any failure.

Not shown to employees: hazard class and category, work activity, life-saving rule, root cause, severity, likelihood, owner, repeat flag, due date. HSE sets these at review, starting from the values suggested by the sub-category.

Note on Status: an employee who picks CLOSED means "I fixed it". The system records this as action completed and keeps the finding INPROGRESS until HSE verifies it. The employee sees it as "Closed, awaiting HSE verification". Positive observations (Safe Action, Safe Condition) are saved as CLOSED.

After sending: a confirmation with a reference number and, read-only, the topic and hazard category the system picked ("Classified as: Housekeeping"). The employee cannot change it. A note says HSE will check it. A "My submissions" page shows each submission with its stage (Received, Reviewed, In progress, Closed).

### 8.2a Automatic topic and hazard from keywords

1. When the SOC form is saved, the API reads the description (field 6) and, for a tie-break, the action text (field 7). The search runs on the server, not in the browser.
2. **Cleaning.** The text and every keyword are lower-cased, and every character that is not a letter or digit becomes a space ("x-ray" becomes "x ray", "km/jam" becomes "km jam"). A keyword of 4 letters or fewer must match a whole word (for example "ban", "las", "tbm"), whatever its `match_type` says. Any other `prefix` keyword matches where a word starts, so "sampah" also finds "sampahnya". A keyword with several words matches those words in order.
3. **Longest match.** Inside one topic, a matched keyword that lies inside the text matched by a longer keyword of the same topic is dropped, so "harness" does not also score "harnes" and "tangga bambu" does not also score "tangga".
4. **Score.** A topic's score is the sum of the weights of its remaining matched keywords (weight 2 for specific words, otherwise 1). Hazard topics and control tags are scored separately.
5. **Hazard topic.** Among hazard topics, the one with the highest score wins; a tie goes to the lower `sort_order`. The winner sets `soc_topic_id` and its suggested hazard category, activity, life-saving rule and root cause (where it has them). `topic_source` = Auto and `matched_keywords` stores the words found. In the test on the 242 reports 30 of 207 matches were ties, so the order of the topics matters and is part of what HSE approves.
6. **Control tags.** Every control tag with at least one matched keyword is attached to the finding in `finding_control_tag` (tag_source = Auto). Control tags never change the hazard topic. A report with control tags and no hazard topic (for example a toolbox meeting) is Unclassified for hazard but shows its tags to HSE.
7. **No match.** If no hazard topic and no control tag matches, the hazard topic is empty ("Unclassified"), `topic_source` = Auto, and the finding appears at the top of the HSE review list.
8. The keywords decide the topic, the hazard and the control tags only. The observation type (Safe, Unsafe, Suggestion) still comes from the employee's choice in field 4. The keywords never decide severity, likelihood, or due date.
9. Everything set by keywords is provisional until HSE sets review_status to Reviewed. When HSE changes a topic or hazard, `topic_source` becomes HSE and the old and new values go to the audit log, so the keyword list can be improved from the corrections.
10. A topic list screen and a keyword screen for HSE Admin: add, switch off, or edit keywords; see how many reports each keyword caught and how many HSE corrected. A keyword change applies only to new submissions, never to old ones.
11. Known limits: very short or vague text ("nothing", "logam dan besi") will not match. One report touching two topics gets only the winning topic. Misspellings need their own keywords (for example "puntung", "putung", "pontong" for cigarette butts). Plan a keyword review after the first month of live use.
12. **Starting list.** The first list is version 6: `SOC_Keywords_v6.csv`, `SOC_Topics_v6.csv` and `SOC_Hierarchy_v6.csv` (notes in `SOC_Keyword_Improvement_Plan_v6.md`; the earlier `HSE_SOC_Keyword_List.md` is superseded). 964 of its 1,276 keywords are proposals not yet proven on real reports, and each row keeps its `source` so HSE can approve or switch them off. After HSE approves the list it is loaded once. These files are a snapshot and are not updated afterwards; the table is the live list.
13. **Change history.** Every add, edit, switch-off and switch-on writes a row to `ref_keyword_log` (who, when, old and new value, optional reason) in the same database transaction as the change. In the Go code, the change and its log row are written together with one `database/sql` transaction (`BeginTx`, commit only if both succeed). The admin screen shows the history of each keyword. No keyword is hard-deleted.
14. **Seed.** The approved list is loaded from `migrations/seed/ref_soc_topic_seed.csv` and `migrations/seed/ref_keyword_seed.csv` (topic columns: sort_order, topic, kind, topic_group, suggested_hazard_category, suggested_work_activity, suggested_life_saving_rule, suggested_root_cause; keyword columns: topic, keyword, match_type, weight, is_active, source). The seed runs once on an empty `ref_keyword` and does nothing if it already has rows, so a later run cannot overwrite changes made in the screen. Each seeded row gets a `ref_keyword_log` row with action 'add' and changed_by = system. In the Go program, the seed is a separate command (`cmd/seed` in the `backend` module, built for Linux ARM64 with the main program) that the database administrator runs once on the board after the migrations. The main web program never runs it by itself. Before loading, it checks that every topic's hazard is one of the 52 categories in `migrations/seed/ref_hazard_seed.csv` and that every keyword's topic exists; on any mismatch it loads nothing and prints the rows at fault.
15. **Export.** The keyword screen has an "Export CSV" button (GET /api/admin/keywords/export). Use it when HSE wants a current copy to review. Do not edit the old Markdown file.
16. **Topics.** A keyword belongs to one topic from `ref_soc_topic`; the keyword screen picks the topic from that list. Adding or renaming a topic, or changing its hazard, is done in the topic screen and applies to new submissions only.

### 8.3 Face 2: Full HSE form (field order)

For HSE staff: Audit, MWT, Inspections, PMR, and SOC findings that HSE enters itself. Entries on this form are saved as Reviewed.

Section A: Basic
1. Reporter: enter employee ID, form fetches name and designation (read-only). If not found, choose "External" and type the name
2. Date of finding (default today, no future dates)
3. Source (Audit, MWT, Inspections, SOC, PMR)
4. Audit type (only visible when source = Audit)

Section B: Where
5. Company
6. Area

Section C: What
7. Observation type
8. Hazard category (grouped by Hazard Class)
9. Work activity (optional)
10. Life-saving rule (optional)
11. Root cause group (optional)
12. Description of finding
13. Photo (optional; one before-photo, camera allowed on phones)

Section D: Risk (shown only for Unsafe Action, Unsafe Condition, Near-Miss)
14. Severity (1 to 5, with the descriptors as help text) and which category drove it
15. Likelihood (1 to 5, with probability text)
16. The form shows the resulting risk level, colour, and due date immediately

Section E: Follow-up
17. Action taken or immediate action
18. Recommendation
19. Action owner (employee ID lookup)
20. Repeat finding? If yes, choose the earlier finding

### 8.4 Form behaviour (both faces)
- Labels are bilingual (Indonesian / English), as on the current SOC form.
- Required fields are marked; the form explains any rejection next to the field.
- Positive observations skip Sections D and E.
- Works on phones, in bright light, and with unreliable signal: keep the form light and show clear upload progress.
- The form never auto-submits twice (disable the button after the first click).

### 8.5 Follow-up screens and HSE review
- "My assigned findings" for Action Owners, with due date and days left
- "Awaiting my verification" for HSE Officers
- Finding detail with the full history from the audit log
- HSE review screen: shows the employee's entry next to the suggested classification. The HSE Officer confirms in one click or edits the hazard, activity, rule, root cause, severity, likelihood, owner, and due date. Positive observations can be confirmed in bulk.

### Definition of Done: Phase 4
An employee with no training completes a SOC submission (the 11 current fields) on a phone in under two minutes; an HSE tester enters one finding of every observation type on the full form; the review queue works end to end; invalid entries are rejected with a clear message; the Medpost API outage case works.

---

## 9. Phase 5: Dashboards

### 9.1 Site HSE view

| Panel | Content |
|---|---|
| Review queue | Pending reviews (count, oldest age, those past the review time limit), reviewed this week, average days to review |
| Attention required | Overdue (by risk level, days overdue), High and Very High not closed, awaiting verification, due in 7 days, new this week, near-misses this month |
| Status | Total, Open, In Progress, Closed |
| Aging | 0-7, 8-14, 15-30, over 30 days |
| Findings log | Filterable table: date, reporter, company, area, hazard, risk, status, due date, owner |
| Detail | Finding with photos and history |

### 9.2 Management / client view (no personal names, no free text, no photos)

| Panel | Content |
|---|---|
| HSE Recommendations Status | Total Findings, Open, In Progress, Closed (all sources) |
| SOC Status | Total, Open, In Progress, Closed, by Hazard Category, by Area, by Subcontractor, Positive Findings |
| PMR Status | Total, Open, In Progress, Closed, by Hazard Category, by Area, by Subcontractor |
| Trends | Monthly findings, closure rate, backlog; positive vs unsafe vs near-miss |
| Risk profile | Findings by risk level and by severity |
| Hazard analysis | Ranking by category and by class, 3-month trend |
| Area × Hazard heatmap | Where each hazard concentrates |
| Subcontractor scorecard | Findings, open, overdue, average days to close, closure rate; per 100 workers only when manhours or headcount exist |
| Reporter activity | Counts by designation and company only |

Filters on both views: date range, source, company, area, hazard class.

### 9.3 KPI definitions (use these exact definitions)

Only findings that require follow-up count in closure measures. Positive observations and Suggestions without follow-up are excluded.

| KPI | Definition |
|---|---|
| Closure rate | Closed ÷ Total, within the period |
| On-time closure rate | Closed with closed_date on or before due_date ÷ Closed |
| Average days to close | Average of (closed_date minus finding_date), by risk level |
| Open backlog | Count of findings not CLOSED at each month end |
| Overdue count | Not CLOSED and due_date before today |
| Awaiting verification | INPROGRESS, action completed, not verified |
| Positive-to-unsafe ratio | (Safe Action + Safe Condition) ÷ (Unsafe Action + Unsafe Condition) |
| Near-miss ratio | Near-Miss ÷ (Unsafe Action + Unsafe Condition) |
| Repeat rate | Repeat findings ÷ Total |
| Review backlog | Count of findings with review_status Pending |
| Average days to review | Average of (reviewed_date minus finding_date) for reviewed SOC findings |
| Unclassified share | Pending ÷ Total |
| Top reporter share | Reports from the single most active reporter ÷ Total reports, in the period. In the current SOC data this is 45% (one person) |
| Findings per 100 workers | Total findings ÷ headcount × 100, shown only when headcount data exists; otherwise "no manhours data" |

### 9.4 Rules for display
- Use the colours of your JSA matrix for risk levels: Low green, Medium yellow, High orange, Very High red. Do not rely on colour alone; also show the level name.
- Counts must match the database views exactly; no calculation in the browser that is not in a view.
- Exports: CSV from the site view; PDF dashboard report from the management view (section 9.5).

### 9.5 Dashboard PDF report

- Made on the server, in the Go program, with `gopdf` and a font file that supports Indonesian text. No headless browser (too heavy for the board).
- Content comes only from the `v_mgmt_` views, so it has no personal names, no free text, and no photos. The same data rules as the management dashboard apply.
- One or two A4 pages: report period and company filter; KPI figures (total findings, open, overdue, closure rate, top reporter share); findings by source and by hazard class; open findings by risk level; overdue by age; monthly trend.
- Charts are drawn by the server as images from the same numbers (a pure-Go plotting library). Tables and numbers are the fallback if a chart does not fit.
- Each export is written to the audit log (who, when, filters). Only HSE roles and the Management Viewer role can export.
- Generation time and memory are measured on the board (test 47).

### Definition of Done: Phase 5
Every panel matches the SQL view output; the sample data (Appendix A) produces the exact expected numbers; the management user cannot reach any name, description, or photo.

---

## 10. Phase 6: Security and privacy

- Medpost data: five identity fields only; the Medpost API credentials stay on the server in `.env`; no NIK; no medical fields; nothing from the API is written to logs.
- Use HTTPS for the web application and for the API.
- Accounts: unique per person, role-based, deactivated when someone leaves the project.
- SOC form for all employees: whichever login method is chosen, an employee can create findings only under their own identity and see only their own submissions.
- Cloudflare Turnstile: keep the secret key only on the server in `.env`. Verify every token on the server. Tokens are short-lived and single use.
- Name search: available only after Turnstile has passed, returns names only, with a minimum number of letters, a maximum number of results, and a limit per device.
- Date of birth: sent over HTTPS to the API for comparison only. Not stored, not logged, not returned. Confirm with your legal or compliance contact that using date of birth for sign-in is acceptable under the personal data protection rules.
- Limits on failed sign-ins, as in 7.1, and a log of failed attempts without the date of birth.
- Database users as in 5.8; the application never uses the admin account.
- Audit log is write-only for the application.
- Uploaded files: type check, size limit, stored outside the public web folder, served only through the application with a role check.
- Backups encrypted and stored in a second location.
- Document who can see names (HSE roles) and who cannot (management and client).
- Indonesian personal data protection rules apply; confirm with your legal or compliance contact how reporter names and photos of people should be handled.

---

## 11. Phase 7: Testing and acceptance

**11.1 Automated tests**
- Database tests (5.11)
- API tests (7.5)
- View tests against the sample data

**11.2 User acceptance scenarios**

| # | Scenario | Expected result |
|---|---|---|
| 1 | Reporter logs a Safe Action | Saved as CLOSED, no risk or due date |
| 2 | Reporter logs an Unsafe Condition, severity 4, likelihood 3 | Level High, due date finding date plus 3 days (placeholder) |
| 3 | Same with severity 2, likelihood 3 | Level Low, due date plus 30 days |
| 4 | Action owner marks done without a photo | Rejected |
| 5 | HSE Officer verifies | Status CLOSED, history shows who and when |
| 6 | HSE Officer rejects with remarks | Stays INPROGRESS, remarks visible |
| 7 | Reporter tries to verify own finding | Rejected |
| 8 | Employee ID not found | Form offers External entry |
| 9 | Medpost API down | Form still works, flagged |
| 10 | Management user opens dashboard | No names, descriptions, or photos anywhere |
| 11 | Finding passes its due date | Appears in Overdue |
| 12 | Reopen a CLOSED finding | Requires reason, logged |
| 13 | Employee submits a SOC Unsafe Condition, topic Electrical Work | OPEN, Pending, hazard Electricity shown as provisional, no risk, no due date |
| 14 | Employee submits a Safe Action | CLOSED, Pending classification |
| 15 | HSE reviews item 13 with severity 3, likelihood 4 | Level High, due date = review date + 3 days (placeholder), Reviewed |
| 16 | HSE tries to verify a Pending unsafe finding | Rejected |
| 17 | Employee picks Status CLOSED on an unsafe SOC entry | INPROGRESS awaiting verification, never CLOSED |
| 18 | The Google Form import is run twice | No duplicate rows |
| 19 | An imported legacy unsafe row | Pending, no due date, not in Overdue |
| 20 | Employee opens "My submissions" | Sees only their own entries, with the current stage |
| 21 | Correct name and date of birth, Turnstile passed | Form opens with the name filled in; saved with the verified name, employee ID, designation, and company |
| 22 | Wrong date of birth | One generic message; nothing shows which part was wrong |
| 23 | Repeated failed attempts beyond the limit | Locked for the lock period (placeholder values) |
| 24 | Submission with a missing, expired, or reused Turnstile token | Rejected by the server |
| 25 | Search the database, logs, and browser responses for the date of birth | It appears nowhere |
| 26 | Medpost API down | SOC form still accepts the report, marked External and unverified |
| 27 | "My submissions" page | Shows only reference number, date, topic, and status |
| 28 | Open the form without passing Turnstile | No name search is possible |
| 29 | Type fewer letters than the minimum | No results |
| 30 | Search for a common name | At most 5 names; the browser response contains no employee ID, designation, or date of birth |
| 31 | Two employees with the same name | Both listed, with the company shown |
| 32 | Many searches from one device | Blocked by the search limit |
| 33 | Import the current SOC file (242 rows) on the test database | 242 findings: 185 CLOSED and 57 OPEN; observation types 98 / 10 / 83 / 30 / 21 |
| 34 | Company check after import | SALAWATI and STARWAY rows appear under their full names; no company is left unmatched |
| 35 | The 1995 date row | Finding date equals the submission date, and the change is logged |
| 36 | Area check after import | 12 road entries under Main / Access Road; 3 blank areas under Not specified; 2 rows in the HSE review list |
| 37 | Import the file twice | Still 242 findings |
| 38 | Submit a SOC text with "tidak menggunakan helm" | No hazard topic; control tag PPE attached with `matched_keywords` containing "helm"; the employee sees the result but has no way to change it |
| 39 | Submit a text with no keyword ("nothing") | Topic empty, shown at the top of the HSE review list |
| 40 | Text that matches two topics, one with more keywords | The topic with more keywords wins; a tie goes to the topic with the lower sort order |
| 41 | The word "ban" inside "bantuan" | No match, because short keywords need a whole word |
| 42 | "sampahnya" with the keyword "sampah" | Match (word start) |
| 43 | HSE changes a topic at review | `topic_source` becomes HSE; the audit log holds old and new values |
| 44 | HSE Admin switches off a keyword | New submissions ignore it; old submissions keep their topic; `ref_keyword_log` has a 'switch_off' row with the user and time |
| 44a | HSE Admin edits a keyword and adds a new one | Both appear in `ref_keyword_log` with old and new values; the new one is used by the next submission |
| 44b | Run the seed on an empty table, then run it again | First run loads 222 keywords and 26 topics; the second run changes nothing |
| 44c | Click "Export CSV" | The file matches the active and inactive rows on screen, including counts |
| 45 | Run the 242 migrated descriptions through the rules | 207 get a hazard topic (30 of them by tie-break), 32 get control tags only, 3 get nothing (results of list v6 as given; they change if the list changes) |
| 45a | "harness" in a text (keywords "harnes" and "harness" both in Working at Height) | Scores once, not twice |
| 45b | "x-ray" in a text with the keyword "x ray" | Match (punctuation is cleaned on both sides) |
| 45c | A text with a toolbox meeting and no hazard word | No hazard topic; control tag Safety Meeting, Induction & Checks; shown in the review list |
| 45d | Two topics each match one keyword, one of weight 2 | The topic with the weight-2 keyword wins |
| 46 | Open the dashboard PDF report | No personal name, no free text, no photo; the numbers equal the management dashboard for the same filters |
| 47 | Ten people submit the SOC form at the same time, five with photos, on the real board | All saved; memory stays below the limit set for the program; no crash |
| 48 | Restart the board with the power switch | The program and MariaDB start by themselves; no data lost for saved submissions |
| 49 | Restore last night's backup on a spare machine | The database opens and the Phase 1 tests pass |
| 50 | Dashboard PDF while a form is being submitted | The PDF finishes within the target time (set after the first measurement) and the submission is saved |
| 51 | From a phone on mobile data, open the public address | The SOC sign-in page opens over HTTPS; the full HSE form, review, dashboard, and admin addresses return "not found" |
| 51a | Open the login page, enter a wrong password | One generic message; after 5 wrong attempts in 15 minutes the account is locked for 15 minutes |
| 51b | Sign in as a normal HSE user | Home page shows Medical Activities, Work Permit Register, and HSE Observation; no Settings card; opening /admin/settings gives "not allowed" |
| 51c | Sign in as HSE Admin | The Settings card is also shown and opens |
| 51d | Open Medical Activities | Programs and activities can be read; there is no add, edit, or delete control and the matching requests are refused |
| 51e | Click the logo on any signed-in page | Back to the home page, still signed in |
| 51f | Log out, then press the browser's back button | The page asks for a sign-in again |
| 51g | SOC: complete the check, pick a name, enter the date of birth, submit the form | Thank-you page with a reference number and the classification; the same look as the staff pages and no home page link |
| 51h | Open /start, /observations, or /admin/settings on the public address | Not found (the routes do not exist on the public listener) |
| 52 | From a computer on Tailscale, open the private address | All screens open for a user with the right role |
| 53 | Send a request to the public listener with a forged visitor-address header from outside Cloudflare | Rejected or ignored; limits use the real Cloudflare-supplied address |
| 54 | Six failed verifications from one phone | That phone is locked for the period; another phone is not affected |
| 55 | Stop `cloudflared` | The public SOC form is unreachable; the HSE side over Tailscale still works |

**11.3 Risk lookup test table**

| Severity | Likelihood | Expected level |
|---|---|---|
| 5 | 4 | Very High |
| 5 | 5 | Very High |
| 4 | 5 | Very High |
| 5 | 3 | High |
| 4 | 3 | High |
| 3 | 4 | High |
| 3 | 3 | Medium |
| 2 | 5 | Medium |
| 1 | 5 | Medium |
| 5 | 1 | Medium |
| 2 | 3 | Low |
| 4 | 1 | Low |
| 1 | 4 | Low |

---

## 12. Phase 8: Rollout

1. **Pilot** (2 weeks): two companies and the HSE team use the web form; the old Google Form stays open for everyone else.
2. **Parallel run** (2 to 4 weeks): all new findings go into the web form; compare counts weekly with the old form.
3. **Cutover:** close the old Google Form; keep it read-only as an archive.
4. **Training:** 30-minute sessions for reporters, action owners, HSE officers, and management; a one-page guide with screenshots, in Indonesian and English.
5. **First month:** weekly review of data quality (empty fields, "Other" use, wrong categories) and an overdue review meeting using the site dashboard.
6. **Quarterly:** review the "Other" entries and decide whether new categories are needed; review closure days and the matrix.

---

## 13. Open items register

| # | Item | Owner | Needed before |
|---|---|---|---|
| 1 | Closure days per risk level (placeholder in use) | HSE manager | Phase 3 sign-off |
| 2 | Medpost API: address, authentication, returned fields | Database admin | Phase 3 |
| 3 | Web stack decision | Project lead | Phase 3 |
| 4 | Login method for HSE staff, action owners, and management (accounts with passwords). Employees on the SOC form use the name and date of birth sign-in | Project lead / IT | Phase 3 |
| 5 | MariaDB version, hosting server, evidence storage location | Database admin | Phase 1 |
| 6 | Confirm Plant Operations (GPP) and Diving activities | HSE manager | Phase 4 |
| 7 | Jetty Nagote: construction only, or vessel operations too (decides whether a vessel category is added) | HSE manager | Phase 1 |
| 8 | Copy the full severity descriptors (gas leakage, security, personnel) from the JSA form into ref_severity | HSE | Phase 1 |
| 9 | Optional later: multiple photos per finding (separate evidence table), notifications | Project lead | After go-live |
| 10 | Decide who enters manhours or headcount and how often (optional) | HSE manager | Phase 5 |
| 11 | Medpost API functions: search names (name, random reference, company for duplicates only), verify (reference + date of birth, no date of birth in the answer), look up by ID. Confirm they exist or add them | Database admin | Phase 3 |
| 12 | Review time limit (placeholder 2 working days) and who the reviewers are | HSE manager | Phase 3 |
| 13 | Start of the due-date clock for SOC findings: from review date (assumed) or from finding date | HSE manager | Phase 3 |
| 14 | Near-Miss is available on the full HSE form only. Add it to the SOC form later if employees need it (not included now, to keep the form identical to the current one) | HSE manager | After go-live |
| 15 | Google Sheet details: resolved in version 1.5 (242 rows, real dates, mappings in 6.4a). Still open: who has Drive access to copy the 43 photos | Database admin | Phase 2 |
| 16 | Cloudflare Turnstile: create the site key and secret key; confirm that people's phones and the site network can reach Cloudflare, and that the server can reach it for the token check; decide what happens if Cloudflare cannot be reached | IT | Phase 3 |
| 17 | Sign-in limits (placeholders: 5 failed verifications per employee record and per device in 15 minutes, then a 15-minute lock; 30 name searches per device in 10 minutes; search permission 10 minutes; session 30 minutes) | Project lead | Phase 3 |
| 18 | Two employees with the same name: the dropdown shows the company next to each. Confirmed as acceptable | Database admin | Phase 3 |
| 19 | Name search rules: minimum letters (decided: 3), number of results (decided: 5), match from the start of the name or any part, handling of spelling variants | Database admin | Phase 3 |
| 20 | Compliance check that date of birth sign-in is acceptable | Compliance | Before go-live |
| 21 | Define the format of `source_reference` for each source (audit report number, inspection number, MWT or PMR meeting reference) | HSE manager | Phase 4 |
| 22 | Confirm the four assumptions listed in the change log of version 1.5 | HSE manager | Phase 2 |
| 23 | HSE approves or edits the first keyword list (files `SOC_Keywords_v6.csv`, `SOC_Topics_v6.csv`), including the hazard for each topic and the 964 unproven keywords | HSE manager | Phase 3 |
| 24 | Decide the review of keyword results after the first month live, and who owns the keyword list (the owner is the only person with the HSE Admin right to edit it) | HSE manager | Rollout |
| 25 | Confirm the board's RAM (2 GB or 3 GB) and the MariaDB version installed on it | Database admin | Phase 1 |
| 26 | Turnstile mode: decided as Managed (tick-box shown only when Cloudflare is unsure). Still to do: create the widget in the Cloudflare dashboard and tie its site key to the public hostname | Database admin | Phase 3 |
| 27 | How employees reach the board: answered in version 1.8 (Cloudflare Tunnel). Still to do: the public web address and the Cloudflare account/domain it will use, and confirming that employees' phones have internet on site | Database admin | Phase 3 |
| 28 | Date for moving the data from eMMC to NVMe, and the backup destination | Database admin | Before go-live |
| 29 | Which domain or sub-domain on Cloudflare will be used for the public SOC address, and who owns the Cloudflare account | Project lead | Phase 3 |
| 30 | How management and client viewers connect (Tailscale invitation or Cloudflare Access), and the certificate choice for the HSE side | Project lead | Phase 5 |
| 31 | Confirm that the board can reach the Medpost API (network path) | Database admin | Phase 3 |
| 32 | Review the 964 unproven keywords after the first month (use `source` and the caught and corrected counts); the cross-topic keywords "apar", "ambulans" and "tanah longsor" each appear in two topics | HSE manager | Rollout |
| 33 | Decide the mapping of Natural Events / Weather (new category Natural Event or the existing Extreme Weather / Lightning) and of Radiation & Vibration (Ionizing Radiation, Non-Ionizing Radiation, or Vibration) | HSE manager | Phase 3 |
| 34 | How Medical Activities gets its data from hse-medpost: a read-only service account or token in medpost for the activity views, or a link that opens medpost. The plan assumes the first (view only, nothing written to medpost) | Database admin, medpost owner | Phase 3 |
| 35 | Whether registered users sign in with their own hse-register accounts (assumed) or share the medpost staff accounts, and whether the password may be any length (assumed) or six digits as in medpost | HSE manager | Phase 3 |

---

## 14. Order and relative size

| Phase | Work | Relative size | Depends on |
|---|---|---|---|
| 1 | Database: schema, seed, views, users, tests | Medium | Open items 5, 7, 8 |
| 2 | Data migration | Medium | Phase 1 |
| 3 | Backend API | Large | Phase 1; items 1 to 4 |
| 4 | Web form and follow-up screens | Large | Phase 3 |
| 5 | Dashboards | Medium | Phases 1 and 3 |
| 6 | Security review | Small | Phases 3 to 5 |
| 7 | Testing and acceptance | Medium | All |
| 8 | Rollout | Medium | All |

Phases 1 and 2 can run in parallel with preparation of items 1 to 4. Phase 5 can start as soon as the views exist and can use the sample data.

---

## Appendix A: Sample dataset and expected results (for the test database)

All records are invented, for testing only.

| id | Date | Source | Audit Type | Company | Area | Status | Hazard | Observation |
|---|---|---|---|---|---|---|---|---|
| 1 | 2026-10-01 | SOC | | STARWAY ENGINEERING INDONESIA | Argo 5 | CLOSED | Fall from Height | Unsafe Action |
| 2 | 2026-10-02 | SOC | | POWERCHINA | Laydown | OPEN | Slips, Trips & Falls | Unsafe Condition |
| 3 | 2026-10-03 | SOC | | ELECO CNCEC | GPP | CLOSED | Electricity | Safe Condition |
| 4 | 2026-10-03 | PMR | | ZHONGSIE CECLL | Batching Plant | INPROGRESS | Machinery & Work Equipment | Unsafe Condition |
| 5 | 2026-10-04 | Audit | Site Audit | TECTONA MITRA UTAMA | Camp | INPROGRESS | Infection / Contaminated Food or Water | Unsafe Condition |
| 6 | 2026-10-05 | Inspections | | ARTAMA SENTOSA INDONESIA | Jetty Nagote | OPEN | Mechanical Lifting / Load Handling | Unsafe Condition |
| 7 | 2026-10-05 | SOC | | STARWAY ENGINEERING INDONESIA | Condensate Area | CLOSED | Toxic Gas / Oxygen Deficiency | Safe Action |
| 8 | 2026-10-06 | MWT | | LAYAR NUSANTARA GAS | Office | OPEN | Fire | Unsafe Condition |
| 9 | 2026-10-06 | SOC | | SALAWATI MOTORINDO | PCC / TCF | OPEN | Machinery & Work Equipment | Unsafe Action |
| 10 | 2026-10-07 | PMR | | NANGGALA 28 | Argo 6 | CLOSED | Spill / Leakage | Unsafe Condition |

Add a severity and likelihood to each unsafe row for the risk tests (any valid pair).

**Expected results**

| Panel | Total | Open | In Progress | Closed |
|---|---|---|---|---|
| All sources | 10 | 4 | 2 | 4 |
| SOC | 5 | 2 | 0 | 3 |
| PMR | 2 | 0 | 1 | 1 |

SOC Positive Findings: 2 (rows 3 and 7). SOC by company: STARWAY 2, POWERCHINA 1, ELECO CNCEC 1, SALAWATI MOTORINDO 1. SOC by area: Argo 5, Laydown, GPP, Condensate Area, PCC / TCF, one each. SOC by hazard: Fall from Height 1, Slips, Trips & Falls 1, Electricity 1, Toxic Gas / Oxygen Deficiency 1, Machinery & Work Equipment 1. PMR by hazard: Machinery & Work Equipment 1, Spill / Leakage 1.

In the test database, load the 10 sample rows as Reviewed. Rows entered through the SOC form stay Pending (shown as Unclassified in hazard breakdowns) until reviewed.

Note on the sample: rows 3 and 7 are positive observations. They are CLOSED on entry, so they have no verification fields and are excluded from closure KPIs.

---

## Appendix B: References

- IOGP, Safety data reporting user guide: Scope and definitions (2025 data), February 2026: incident/event causes (3.10), type of activity (3.11), causal factors (3.12), Life-Saving Rules (3.13), Line of Fire barriers (3.13.1)
- Project JSA and Risk Assessment Form (5×5 matrix, severity and likelihood descriptions)
- University of Alberta, Hazard Types (for the common hazard classes)
